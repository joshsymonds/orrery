package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// fieldToken is the key of the token POST /v1/picker/run reads from its body.
const fieldToken = "token"

// pickerTokenBytes is the size of a picker token: 128 random bits, written as hex.
const pickerTokenBytes = 16

// pickerTTL is how long a token from an ask stays valid after the ask.
const pickerTTL = 5 * time.Minute

// pickerRow is one row of a 200 answer from POST /v1/picker/ask.
type pickerRow struct {
	Token  string `json:"token"`
	Kind   string `json:"kind"`
	Dir    string `json:"dir"`
	Pane   string `json:"pane"`
	Label  string `json:"label"`
	Note   string `json:"note"`
	Prompt bool   `json:"prompt"`
}

// pickerAnswer is the body of a 200 answer from POST /v1/picker/ask.
type pickerAnswer struct {
	Rows []pickerRow `json:"rows"`
}

// pickerEntry is what a token stands for: the open configuration and the action its ask used, valid until expires.
// pane is the pane the run of the action answered, and is "" until that run succeeds.
type pickerEntry struct {
	cfg     open.Config
	action  open.Action
	expires time.Time
	pane    string
}

// pickerTokens holds the entries of the tokens every ask issued, and is shared by the picker routes. runMu is held
// for the whole of each run, so the runs of one token are serial and its action runs at most once.
type pickerTokens struct {
	mu      sync.Mutex
	runMu   sync.Mutex
	entries map[string]pickerEntry
}

// lookup returns the entry token stands for, and whether there is one.
func (s *pickerTokens) lookup(token string) (pickerEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	return entry, ok
}

// recordRun keeps pane as the answer of token's run, when token still has an entry.
func (s *pickerTokens) recordRun(token, pane string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[token]; ok {
		entry.pane = pane
		s.entries[token] = entry
	}
}

// store adds entries and drops every entry that has expired at now.
func (s *pickerTokens) store(now time.Time, entries map[string]pickerEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, token)
		}
	}
	maps.Copy(s.entries, entries)
}

// servePickerAsk answers POST /v1/picker/ask with the rows open.Choices offers for the body's text, each row
// carrying a one-time token. A nil picker answers 404. Blank text or a body without a text string answers 400.
// A Picker, Choices or pane listing error answers 502 with its reason. The ask executes nothing.
func servePickerAsk(
	picker func() (open.Config, error),
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if picker == nil {
			http.NotFound(w, r)
			return
		}
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		text, ok := jsonString(fields[fieldText])
		if !ok || strings.TrimSpace(text) == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		cfg, err := picker()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		actions, err := open.Choices(r.Context(), cfg, text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		issued := now()
		answer, entries, err := pickerRows(r.Context(), tmux, cfg, actions, issued.Add(pickerTTL))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		tokens.store(issued, entries)
		writeJSON(w, http.StatusOK, answer)
	}
}

// pickerRunAnswer is the body of a 200 answer from POST /v1/picker/run.
type pickerRunAnswer struct {
	Pane string `json:"pane"`
}

// servePickerRun answers POST /v1/picker/run by running the action the body's token names, once, and answering the
// pane it opened or jumped to. A repeat of a run answers the same pane and runs nothing. A body without a token
// string answers 400, an unissued token 404, and an expired one 410, each running nothing. With no tmux server it
// answers 409 and starts none. A failed run answers 502 and records nothing.
func servePickerRun(tokens *pickerTokens, tmux *tmuxctl.Client, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fields, ok := decodeBody(r)
		if !ok {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		token, ok := jsonString(fields[fieldToken])
		if !ok || strings.TrimSpace(token) == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		tokens.runMu.Lock()
		defer tokens.runMu.Unlock()
		entry, ok := tokens.lookup(token)
		if !ok {
			http.Error(w, "unknown token", http.StatusNotFound)
			return
		}
		if !now().Before(entry.expires) {
			http.Error(w, "expired token", http.StatusGone)
			return
		}
		if entry.pane == "" {
			pane, status, err := runPickerEntry(r.Context(), tmux, entry)
			if err != nil {
				http.Error(w, err.Error(), status)
				return
			}
			tokens.recordRun(token, pane)
			entry.pane = pane
		}
		writeJSON(w, http.StatusOK, pickerRunAnswer{Pane: entry.pane})
	}
}

// runPickerEntry runs the action of entry and returns the pane it answers: the pane the action opened or jumped to,
// or else the active pane of the session it created. With no tmux server it refuses before running anything, as
// 409, and any other failure answers 502. The status is meaningful only with the error.
func runPickerEntry(ctx context.Context, tmux *tmuxctl.Client, entry pickerEntry) (string, int, error) {
	if _, err := tmux.ListPanes(ctx); err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting to") {
			status = http.StatusConflict
		}
		return "", status, fmt.Errorf("server: list panes: %w", err)
	}
	target, err := open.ExecuteAction(ctx, entry.cfg, entry.action)
	if err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("server: picker run: %w", err)
	}
	if target.Pane != "" {
		return target.Pane, http.StatusOK, nil
	}
	out, err := tmux.Run(ctx, "display-message", "-p", "-t", "="+target.Session+":", "#{pane_id}")
	if err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("server: session %q pane: %w", target.Session, err)
	}
	pane := strings.TrimSpace(out)
	if pane == "" {
		return "", http.StatusBadGateway, fmt.Errorf("server: session %q has no active pane", target.Session)
	}
	return pane, http.StatusOK, nil
}

// pickerRows returns the rows for actions in rank order, each with a new token, and the entries those tokens
// stand for. A jump takes its folder and label from its pane in the pane list, and is left out when tmux does
// not list that pane.
func pickerRows(
	ctx context.Context,
	tmux *tmuxctl.Client,
	cfg open.Config,
	actions []open.Action,
	expires time.Time,
) (pickerAnswer, map[string]pickerEntry, error) {
	answer := pickerAnswer{Rows: make([]pickerRow, 0, len(actions))}
	entries := make(map[string]pickerEntry, len(actions))
	var panes map[string]protocol.Pane
	for _, action := range actions {
		row := pickerRow{Kind: string(action.Kind), Dir: action.Dir, Note: action.Note, Prompt: action.Prompt != ""}
		if action.Kind == open.ActionJump {
			if panes == nil {
				listed, err := tmux.ListPanes(ctx)
				if err != nil {
					return pickerAnswer{}, nil, fmt.Errorf("server: list panes: %w", err)
				}
				panes = make(map[string]protocol.Pane, len(listed))
				for _, pane := range listed {
					panes[pane.ID] = pane
				}
			}
			pane, ok := panes[action.Pane]
			if !ok {
				continue
			}
			row.Dir = pane.Cwd
			row.Pane = action.Pane
			row.Label = pane.Label
		}
		token, err := newPickerToken()
		if err != nil {
			return pickerAnswer{}, nil, err
		}
		row.Token = token
		entries[token] = pickerEntry{cfg: cfg, action: action, expires: expires}
		answer.Rows = append(answer.Rows, row)
	}
	return answer, entries, nil
}

// newPickerToken returns 128 random bits as hex.
func newPickerToken() (string, error) {
	var raw [pickerTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("server: picker token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
