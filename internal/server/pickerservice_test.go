package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// pickerClient serves handler on an httptest server and returns a generated Picker client whose requests carry
// allowedLogin and the client header.
func pickerClient(t *testing.T, handler http.Handler) orreryv1connect.PickerClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPickerClient(httpClient, srv.URL)
}

// TestPickerAskParity asks one fixture through Picker.Ask and checks its rows and the token of the first row. The
// rows are the jump row to the fixture's pane, the pane row of the folder zoxide lists, and the pane row of the root.
func TestPickerAskParity(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	pane := startPickerPane(t, tmux, "example-session-a", paneDir)
	claude, _ := installPickerClaude(t, "jump "+pane+"\npane d1\ntask: yes\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(filepath.Join(root, "dir-01"))), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	rows := askRows(t, handler, "example text")
	if len(rows) != 3 {
		t.Fatalf("Picker.Ask rows = %d, want 3", len(rows))
	}
	assertRow(t, rows[0], pickerRow{Kind: "jump", Dir: paneDir, Pane: pane, Label: pickerLabel})
	assertRow(t, rows[1], pickerRow{Kind: "pane", Dir: filepath.Join(root, "dir-01"), Prompt: true})
	assertRow(t, rows[2], pickerRow{Kind: "pane", Dir: root, Prompt: true})

	if answered := runPane(t, handler, rows[0].GetToken()); answered != pane {
		t.Errorf("run of the jump row answered pane %q, want %q", answered, pane)
	}
}

// TestPickerAskErrorParity answers each error of Picker.Ask with its code of the docs/contract.md table and its
// reason as the message.
func TestPickerAskErrorParity(t *testing.T) {
	cases := map[string]struct {
		text    string
		build   func(t *testing.T, tmux *tmuxctl.Client) http.Handler
		code    connect.Code
		message string
	}{
		"blank text": {
			text: " \t ",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, func() (open.Config, error) { return open.Config{}, nil })
			},
			code:    connect.CodeInvalidArgument,
			message: "bad request",
		},
		"no picker": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, nil)
			},
			code:    connect.CodeNotFound,
			message: "404 page not found",
		},
		"picker failure": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, func() (open.Config, error) {
					return open.Config{}, errors.New("example picker failure")
				})
			},
			code:    connect.CodeUnavailable,
			message: "example picker failure",
		},
		"failed claude": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				claude, _ := installPickerClaude(t, "", true)
				cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
				return pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
			},
			code:    connect.CodeUnavailable,
			message: "intent: claude run: exit status 1: example claude failure",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tmux := newTmuxClient(t)
			handler := tc.build(t, tmux)

			_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: tc.text}))
			assertConnectError(t, err, tc.code, tc.message)
		})
	}
}

// TestPickerRunParity runs a token through Picker.Run for each of two asks of one fixture. Each answers a pane of the
// fixture's session, and the two panes differ because each run opens its own window. A repeat of either token answers
// the same pane, and runs nothing, so neither the window count nor the claude run count moves.
func TestPickerRunParity(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	first := askRows(t, handler, "example text")
	second := askRows(t, handler, "example text")
	if len(first) == 0 || first[0].GetKind() != "pane" || len(second) == 0 || second[0].GetKind() != "pane" {
		t.Fatalf("rows = %d and %d, want a pane row first in each ask", len(first), len(second))
	}

	before := serverPaneIDs(t, tmux)
	firstPane := runPane(t, handler, first[0].GetToken())
	secondPane := runPane(t, handler, second[0].GetToken())
	if firstPane == "" || secondPane == "" || firstPane == secondPane {
		t.Fatalf("Picker.Run panes %q and %q, want two distinct panes", firstPane, secondPane)
	}
	for _, pane := range []string{firstPane, secondPane} {
		if got := paneSession(t, tmux, pane); got != pickerDirSession {
			t.Errorf("pane %s is in session %q, want %q", pane, got, pickerDirSession)
		}
	}
	if after := serverPaneIDs(t, tmux); len(after) != len(before)+2 {
		t.Errorf("two runs left %d panes, want %d: each run opens one window", len(after), len(before)+2)
	}

	panes := serverPaneIDs(t, tmux)
	// Two asks run claude synchronously, and each pane run opens a window whose claude starts asynchronously.
	awaitClaudeRuns(t, runs, 4)
	ran := claudeRuns(t, runs)
	if repeat := runPane(t, handler, first[0].GetToken()); repeat != firstPane {
		t.Errorf("repeat of the first Picker.Run token answered pane %q, want %q", repeat, firstPane)
	}
	if repeat := runPane(t, handler, second[0].GetToken()); repeat != secondPane {
		t.Errorf("repeat of the second Picker.Run token answered pane %q, want %q", repeat, secondPane)
	}
	if after := serverPaneIDs(t, tmux); len(after) != len(panes) {
		t.Errorf("repeats changed the pane count from %d to %d", len(panes), len(after))
	}
	if got := claudeRuns(t, runs); got != ran {
		t.Errorf("repeats ran claude %d times, want none", got-ran)
	}
}

// TestPickerRunErrorParity answers each error of Picker.Run with its code of the docs/contract.md table and its reason
// as the message. Each build returns the handler, the token to run, and the message its run must answer.
func TestPickerRunErrorParity(t *testing.T) {
	cases := map[string]struct {
		build func(t *testing.T) (http.Handler, string, string)
		code  connect.Code
	}{
		"blank token": {
			build: func(t *testing.T) (http.Handler, string, string) {
				t.Helper()
				return pickerHandler(t, newTmuxClient(t), nil), " \t ", "bad request"
			},
			code: connect.CodeInvalidArgument,
		},
		"unknown token": {
			build: func(t *testing.T) (http.Handler, string, string) {
				t.Helper()
				return pickerHandler(t, newTmuxClient(t), nil), unissuedToken, "unknown token"
			},
			code: connect.CodeNotFound,
		},
		"expired token": {
			build: func(t *testing.T) (http.Handler, string, string) {
				t.Helper()
				tmux := newTmuxServer(t)
				paneDir := t.TempDir()
				startPickerDirSession(t, tmux, paneDir)
				claude, _ := installPickerClaude(t, "pane d1\ntask: no\n", false)
				cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, t.TempDir())
				issued := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
				clock := issued
				picker := func() (open.Config, error) { return cfg, nil }
				handler := pickerHandlerAt(t, tmux, picker, func() time.Time { return clock })
				rows := askRows(t, handler, "example text")
				clock = issued.Add(5 * time.Minute)
				return handler, rows[0].GetToken(), "expired token"
			},
			code: connect.CodeFailedPrecondition,
		},
		"no tmux server": {
			build: func(t *testing.T) (http.Handler, string, string) {
				t.Helper()
				name, tmux := newNamedTmuxClient(t)
				claude, _ := installPickerClaude(t, "pane d1\ntask: no\n", false)
				cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(t.TempDir())), claude, t.TempDir())
				handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
				rows := askRows(t, handler, "example text")
				return handler, rows[0].GetToken(), noServerMessage(name)
			},
			code: connect.CodeFailedPrecondition,
		},
		"failed action": {
			build: func(t *testing.T) (http.Handler, string, string) {
				t.Helper()
				tmux := newTmuxServer(t)
				pane := startPickerPane(t, tmux, "example-session-a", t.TempDir())
				startPickerPane(t, tmux, "example-session-b", t.TempDir())
				claude, _ := installPickerClaude(t, "jump "+pane+"\ntask: no\n", false)
				cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
				handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
				rows := askRows(t, handler, "example text")
				if len(rows) == 0 || rows[0].GetKind() != "jump" {
					t.Fatalf("rows = %d, want a jump row first", len(rows))
				}
				if _, err := tmux.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
					t.Fatalf("kill-pane %s: %v", pane, err)
				}
				return handler, rows[0].GetToken(), `server: picker run: intent: pane "%0" does not exist`
			},
			code: connect.CodeUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler, token, message := tc.build(t)

			_, err := runToken(t, handler, token)
			assertConnectError(t, err, tc.code, message)
		})
	}
}
