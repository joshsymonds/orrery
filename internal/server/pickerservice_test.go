package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// wireRowOf returns the wire row that holds the same fields as row.
func wireRowOf(row *orreryv1.PickerRow) wireRow {
	return wireRow{
		Token:  row.GetToken(),
		Kind:   row.GetKind(),
		Dir:    row.GetDir(),
		Pane:   row.GetPane(),
		Label:  row.GetLabel(),
		Note:   row.GetNote(),
		Prompt: row.GetPrompt(),
	}
}

// TestPickerAskParity asks one fixture through one handler over REST and over Picker.Ask. The rows must match in
// order with every field except token, and a token from Picker.Ask must run through POST /v1/picker/run.
func TestPickerAskParity(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	pane := startPickerPane(t, tmux, "example-session-a", paneDir)
	claude, _ := installPickerClaude(t, "jump "+pane+"\npane d1\ntask: yes\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(filepath.Join(root, "dir-01"))), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	body := jsonBody(t, map[string]any{"text": "example text"})
	rest := askRows(t, postJSON(t, handler, pickerAskPath, body))

	request := connect.NewRequest(&orreryv1.AskRequest{Text: "example text"})
	answer, err := pickerClient(t, handler).Ask(t.Context(), request)
	if err != nil {
		t.Fatalf("Picker.Ask: %v", err)
	}
	got := answer.Msg.GetRows()
	if len(got) != len(rest) {
		t.Fatalf("Picker.Ask rows = %d, want %d as REST answers", len(got), len(rest))
	}
	for i := range rest {
		if !isPickerToken(rest[i].Token) {
			t.Errorf("REST token %q is not 128 random bits as hex", rest[i].Token)
		}
		want := rest[i]
		want.Token = ""
		assertRow(t, wireRowOf(got[i]), want)
	}
	if len(got) == 0 {
		t.Fatalf("Picker.Ask rows are empty; the fixture must answer rows")
	}

	run := postJSON(t, handler, pickerRunPath, jsonBody(t, map[string]any{"token": got[0].GetToken()}))
	assertStatus(t, run, http.StatusOK)
	var answered struct {
		Pane string `json:"pane"`
	}
	if err := json.Unmarshal(run.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decode %q: %v", run.Body.String(), err)
	}
	if answered.Pane != pane {
		t.Errorf("run of the Picker.Ask token answered pane %q, want %q", answered.Pane, pane)
	}
}

// TestPickerAskErrorParity answers each error of Picker.Ask with the code of the status table and the REST body as
// its message.
func TestPickerAskErrorParity(t *testing.T) {
	cases := map[string]struct {
		text   string
		build  func(t *testing.T, tmux *tmuxctl.Client) http.Handler
		status int
		code   connect.Code
	}{
		"blank text": {
			text: " \t ",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, func() (open.Config, error) { return open.Config{}, nil })
			},
			status: http.StatusBadRequest,
			code:   connect.CodeInvalidArgument,
		},
		"no picker": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, nil)
			},
			status: http.StatusNotFound,
			code:   connect.CodeNotFound,
		},
		"picker failure": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				return pickerHandler(t, tmux, func() (open.Config, error) {
					return open.Config{}, errors.New("example picker failure")
				})
			},
			status: http.StatusBadGateway,
			code:   connect.CodeUnavailable,
		},
		"failed claude": {
			text: "example text",
			build: func(t *testing.T, tmux *tmuxctl.Client) http.Handler {
				t.Helper()
				claude, _ := installPickerClaude(t, "", true)
				cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
				return pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
			},
			status: http.StatusBadGateway,
			code:   connect.CodeUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tmux := newTmuxClient(t)
			handler := tc.build(t, tmux)

			rec := postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": tc.text}))
			assertStatus(t, rec, tc.status)

			_, err := pickerClient(t, handler).Ask(t.Context(), connect.NewRequest(&orreryv1.AskRequest{Text: tc.text}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Picker.Ask error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != tc.code {
				t.Errorf("Picker.Ask code = %v, want %v", connectErr.Code(), tc.code)
			}
			if want := strings.TrimSuffix(rec.Body.String(), "\n"); connectErr.Message() != want {
				t.Errorf("Picker.Ask message = %q, want the REST body %q", connectErr.Message(), want)
			}
		})
	}
}

// TestPickerRunParity runs a token through Picker.Run and an equivalent token through POST /v1/picker/run on one
// fixture. Each answers a pane of the fixture's session, and the two panes differ because each run opens its own
// window. A repeat through the other twin answers the same pane, and runs nothing, so neither the window count nor
// the claude run count moves.
func TestPickerRunParity(t *testing.T) {
	tmux := newTmuxServer(t)
	root := t.TempDir()
	paneDir := t.TempDir()
	startPickerDirSession(t, tmux, paneDir)
	claude, runs := installPickerClaude(t, "pane d1\ntask: no\n", false)
	cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(paneDir)), claude, root)
	handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })

	first := askRows(t, postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example text"})))
	second := askRows(t, postJSON(t, handler, pickerAskPath, jsonBody(t, map[string]any{"text": "example text"})))
	if len(first) == 0 || first[0].Kind != "pane" || len(second) == 0 || second[0].Kind != "pane" {
		t.Fatalf("rows = %+v and %+v, want a pane row first in each ask", first, second)
	}

	answer, err := pickerClient(t, handler).Run(
		t.Context(),
		connect.NewRequest(&orreryv1.RunRequest{Token: first[0].Token}),
	)
	if err != nil {
		t.Fatalf("Picker.Run: %v", err)
	}
	connectPane := answer.Msg.GetPane()
	restPane := runPane(t, runPickerToken(t, handler, second[0].Token))
	if connectPane == "" || restPane == "" || connectPane == restPane {
		t.Fatalf("Picker.Run pane %q and REST pane %q, want two distinct panes", connectPane, restPane)
	}
	for _, pane := range []string{connectPane, restPane} {
		if got := paneSession(t, tmux, pane); got != pickerDirSession {
			t.Errorf("pane %s is in session %q, want %q", pane, got, pickerDirSession)
		}
	}

	panes := serverPaneIDs(t, tmux)
	ran := claudeRuns(t, runs)
	repeat := runPane(t, runPickerToken(t, handler, first[0].Token))
	if repeat != connectPane {
		t.Errorf("REST repeat of the Picker.Run token answered pane %q, want %q", repeat, connectPane)
	}
	repeatAnswer, err := pickerClient(t, handler).Run(
		t.Context(),
		connect.NewRequest(&orreryv1.RunRequest{Token: second[0].Token}),
	)
	if err != nil {
		t.Fatalf("Picker.Run repeat: %v", err)
	}
	if got := repeatAnswer.Msg.GetPane(); got != restPane {
		t.Errorf("Picker.Run repeat of the REST token answered pane %q, want %q", got, restPane)
	}
	if after := serverPaneIDs(t, tmux); len(after) != len(panes) {
		t.Errorf("repeats changed the pane count from %d to %d", len(panes), len(after))
	}
	if got := claudeRuns(t, runs); got != ran {
		t.Errorf("repeats ran claude %d times, want none", got-ran)
	}
}

// TestPickerRunErrorParity answers each error of Picker.Run with the code of the status table and the REST body as
// its message.
func TestPickerRunErrorParity(t *testing.T) {
	cases := map[string]struct {
		build  func(t *testing.T) (http.Handler, string)
		status int
		code   connect.Code
	}{
		"blank token": {
			build: func(t *testing.T) (http.Handler, string) {
				t.Helper()
				return pickerHandler(t, newTmuxClient(t), nil), " \t "
			},
			status: http.StatusBadRequest,
			code:   connect.CodeInvalidArgument,
		},
		"unknown token": {
			build: func(t *testing.T) (http.Handler, string) {
				t.Helper()
				return pickerHandler(t, newTmuxClient(t), nil), unissuedToken
			},
			status: http.StatusNotFound,
			code:   connect.CodeNotFound,
		},
		"expired token": {
			build: func(t *testing.T) (http.Handler, string) {
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
				body := jsonBody(t, map[string]any{"text": "example text"})
				rows := askRows(t, postJSON(t, handler, pickerAskPath, body))
				clock = issued.Add(5 * time.Minute)
				return handler, rows[0].Token
			},
			status: http.StatusGone,
			code:   connect.CodeFailedPrecondition,
		},
		"no tmux server": {
			build: func(t *testing.T) (http.Handler, string) {
				t.Helper()
				tmux := newTmuxClient(t)
				claude, _ := installPickerClaude(t, "pane d1\ntask: no\n", false)
				cfg := pickerConfig(tmux, installPickerZoxide(t, scoredLine(t.TempDir())), claude, t.TempDir())
				handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
				body := jsonBody(t, map[string]any{"text": "example text"})
				rows := askRows(t, postJSON(t, handler, pickerAskPath, body))
				return handler, rows[0].Token
			},
			status: http.StatusConflict,
			code:   connect.CodeFailedPrecondition,
		},
		"failed action": {
			build: func(t *testing.T) (http.Handler, string) {
				t.Helper()
				tmux := newTmuxServer(t)
				pane := startPickerPane(t, tmux, "example-session-a", t.TempDir())
				startPickerPane(t, tmux, "example-session-b", t.TempDir())
				claude, _ := installPickerClaude(t, "jump "+pane+"\ntask: no\n", false)
				cfg := pickerConfig(tmux, installPickerZoxide(t, ""), claude, t.TempDir())
				handler := pickerHandler(t, tmux, func() (open.Config, error) { return cfg, nil })
				body := jsonBody(t, map[string]any{"text": "example text"})
				rows := askRows(t, postJSON(t, handler, pickerAskPath, body))
				if len(rows) == 0 || rows[0].Kind != "jump" {
					t.Fatalf("rows = %+v, want a jump row first", rows)
				}
				if _, err := tmux.Run(t.Context(), "kill-pane", "-t", pane); err != nil {
					t.Fatalf("kill-pane %s: %v", pane, err)
				}
				return handler, rows[0].Token
			},
			status: http.StatusBadGateway,
			code:   connect.CodeUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handler, token := tc.build(t)

			rec := runPickerToken(t, handler, token)
			assertStatus(t, rec, tc.status)

			_, err := pickerClient(t, handler).Run(t.Context(), connect.NewRequest(&orreryv1.RunRequest{Token: token}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Picker.Run error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != tc.code {
				t.Errorf("Picker.Run code = %v, want %v", connectErr.Code(), tc.code)
			}
			if want := strings.TrimSuffix(rec.Body.String(), "\n"); connectErr.Message() != want {
				t.Errorf("Picker.Run message = %q, want the REST body %q", connectErr.Message(), want)
			}
		})
	}
}
