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
type pickerEntry struct {
	cfg     open.Config
	action  open.Action
	expires time.Time
}

// pickerTokens holds the entries of the tokens every ask issued, and is shared by the picker routes.
type pickerTokens struct {
	mu      sync.Mutex
	entries map[string]pickerEntry
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
