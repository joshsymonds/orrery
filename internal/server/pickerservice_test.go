package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
