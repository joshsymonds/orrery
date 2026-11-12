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

// pickerTokenBytes is the size of a picker token: 128 random bits, written as hex.
const pickerTokenBytes = 16

// pickerTTL is how long a token from an ask stays valid after the ask.
const pickerTTL = 5 * time.Minute

// pickerRow is one row of the answer Picker.Ask gives.
type pickerRow struct {
	Token  string `json:"token"`
	Kind   string `json:"kind"`
	Dir    string `json:"dir"`
	Pane   string `json:"pane"`
	Label  string `json:"label"`
	Note   string `json:"note"`
	Prompt bool   `json:"prompt"`
}

// pickerAnswer is the rows askPicker returns.
type pickerAnswer struct {
	Rows []pickerRow `json:"rows"`
}

// pickerEntry is what a token stands for: the open configuration and the action its ask used, valid until expires.
// acted is set once the action has run, and target is what it reached. pane is the pane that target answers, and is
// "" until the pane lookup succeeds.
type pickerEntry struct {
	cfg     open.Config
	action  open.Action
	expires time.Time
	acted   bool
	target  open.Target
	pane    string
}

// pickerTokens holds the entries of the tokens every ask issued, and is shared by Ask and Run. runMu is held
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

// recordAction keeps target as what token's action reached, when token still has an entry, so a retry of the run
// answers the pane of target without running the action again.
func (s *pickerTokens) recordAction(token string, target open.Target) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[token]; ok {
		entry.acted = true
		entry.target = target
		s.entries[token] = entry
	}
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

// askPicker returns the rows open.Choices offers for text, each carrying a new token, and stores the entries those
// tokens stand for. The ask executes nothing. A nil picker is a 404, blank text a 400, and a Picker, Choices or pane
// listing failure a 502 with its reason.
func askPicker(
	ctx context.Context,
	picker func() (open.Config, error),
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
	text string,
) (pickerAnswer, error) {
	if picker == nil {
		return pickerAnswer{}, routeError{status: http.StatusNotFound, message: "404 page not found"}
	}
	if strings.TrimSpace(text) == "" {
		return pickerAnswer{}, routeError{status: http.StatusBadRequest, message: "bad request"}
	}
	cfg, err := picker()
	if err != nil {
		return pickerAnswer{}, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	actions, err := open.Choices(ctx, cfg, text)
	if err != nil {
		return pickerAnswer{}, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	issued := now()
	answer, entries, err := pickerRows(ctx, tmux, cfg, actions, issued.Add(pickerTTL))
	if err != nil {
		return pickerAnswer{}, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	tokens.store(issued, entries)
	return answer, nil
}

// runPicker returns the pane of token's run, the core of Picker.Run. A blank token is 400,
// an unissued one 404, and an expired one 410, each running nothing. Runs are serial under runMu, so the action of
// a token runs at most once. With no tmux server it answers 409 and starts none. A failed action answers 502 and
// records nothing. An action that ran but whose pane lookup failed answers 502 and keeps its target, so a retry
// answers that pane and runs nothing again.
func runPicker(
	ctx context.Context,
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
	token string,
) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", routeError{status: http.StatusBadRequest, message: "bad request"}
	}
	tokens.runMu.Lock()
	defer tokens.runMu.Unlock()
	entry, ok := tokens.lookup(token)
	if !ok {
		return "", routeError{status: http.StatusNotFound, message: "unknown token"}
	}
	if !now().Before(entry.expires) {
		return "", routeError{status: http.StatusGone, message: "expired token"}
	}
	if entry.pane != "" {
		return entry.pane, nil
	}
	pane, status, err := answerPickerRun(ctx, tokens, tmux, token, entry)
	if err != nil {
		return "", routeError{status: status, message: err.Error()}
	}
	return pane, nil
}

// answerPickerRun returns the pane of token's run, running its action first unless an earlier run already did. The
// target of a run is kept as soon as its action succeeds, so a run whose pane lookup fails is retried by the lookup
// alone. The status is meaningful only with the error.
func answerPickerRun(
	ctx context.Context,
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	token string,
	entry pickerEntry,
) (string, int, error) {
	if !entry.acted {
		target, status, err := runPickerAction(ctx, tmux, entry)
		if err != nil {
			return "", status, err
		}
		tokens.recordAction(token, target)
		entry.target = target
	}
	pane, status, err := targetPane(ctx, tmux, entry.target)
	if err != nil {
		return "", status, err
	}
	tokens.recordRun(token, pane)
	return pane, http.StatusOK, nil
}

// runPickerAction runs the action of entry once and returns the target it reached. With no tmux server it refuses
// before running anything, as 409, and any other failure answers 502. The status is meaningful only with the error.
func runPickerAction(ctx context.Context, tmux *tmuxctl.Client, entry pickerEntry) (open.Target, int, error) {
	if _, err := tmux.ListPanes(ctx); err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting to") {
			status = http.StatusConflict
		}
		return open.Target{}, status, fmt.Errorf("server: list panes: %w", err)
	}
	target, err := open.ExecuteAction(ctx, entry.cfg, entry.action)
	if err != nil {
		return open.Target{}, http.StatusBadGateway, fmt.Errorf("server: picker run: %w", err)
	}
	return target, http.StatusOK, nil
}

// targetPane returns the pane target answers: the pane the action opened or jumped to, or else the active pane of
// the session it created. Any failure answers 502.
func targetPane(ctx context.Context, tmux *tmuxctl.Client, target open.Target) (string, int, error) {
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
