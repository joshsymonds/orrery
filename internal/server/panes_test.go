package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Fixture values of the pane that sets every option GET /v1/panes reads. Each differs from the others, so a
// swapped field fails the parity test.
const (
	panesFixtureSession       = "full"
	panesFixtureChanged       = 1794301000
	panesFixtureSeen          = 1794301100
	panesFixtureSummary       = "synthetic summary"
	panesFixtureLabel         = "synthetic label"
	panesFixtureContext       = "synthetic context"
	panesFixtureClaudeSession = "synthetic-claude-session"
	panesFixtureTool          = "synthetic tool"
)

// restPane is one element of the GET /v1/panes array, read by the keys the route writes.
type restPane struct {
	ID            string `json:"id"`
	Host          string `json:"host"`
	Session       string `json:"session"`
	Window        int32  `json:"window"`
	Pane          int32  `json:"pane"`
	Cwd           string `json:"cwd"`
	Command       string `json:"command"`
	State         string `json:"state"`
	Summary       string `json:"summary"`
	Label         string `json:"label"`
	Changed       int64  `json:"changed"`
	Seen          int64  `json:"seen"`
	Question      string `json:"question"`
	Context       string `json:"context"`
	Activity      int64  `json:"activity"`
	Tool          string `json:"tool"`
	ClaudeSession string `json:"claude_session"`
	QuestionHash  string `json:"question_hash"`
	NewestReply   int64  `json:"newest_reply"`
}

// message returns the Pane that holds the same nineteen fields as p.
func (p restPane) message() *orreryv1.Pane {
	return &orreryv1.Pane{
		Id:            p.ID,
		Host:          p.Host,
		Session:       p.Session,
		Window:        p.Window,
		Pane:          p.Pane,
		Cwd:           p.Cwd,
		Command:       p.Command,
		State:         p.State,
		Summary:       p.Summary,
		Label:         p.Label,
		Changed:       p.Changed,
		Seen:          p.Seen,
		Question:      p.Question,
		Context:       p.Context,
		Activity:      p.Activity,
		Tool:          p.Tool,
		ClaudeSession: p.ClaudeSession,
		QuestionHash:  p.QuestionHash,
		NewestReply:   p.NewestReply,
	}
}

// panesClient serves handler on an httptest server and returns a generated Panes client whose requests carry
// allowedLogin and the client header.
func panesClient(t *testing.T, handler http.Handler) orreryv1connect.PanesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPanesClient(httpClient, srv.URL)
}

// panesAgree reports whether rest and got hold the same panes in order.
func panesAgree(rest []restPane, got []*orreryv1.Pane) bool {
	if len(rest) != len(got) {
		return false
	}
	for i := range rest {
		if !proto.Equal(rest[i].message(), got[i]) {
			return false
		}
	}
	return true
}

// listBothUntilAgreed reads GET /v1/panes and Panes.List through handler until both answers hold the same panes.
// A pane's activity and command change while its shell starts, so the two reads can differ until it settles.
func listBothUntilAgreed(t *testing.T, handler http.Handler) ([]restPane, []*orreryv1.Pane) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	client := panesClient(t, handler)
	for {
		rec := get(t, handler, "/v1/panes", allowedLogin)
		assertStatus(t, rec, http.StatusOK)
		assertJSON(t, rec)
		var rest []restPane
		if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil {
			t.Fatalf("decode GET /v1/panes: %v", err)
		}
		got, err := client.List(ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
		if err != nil {
			t.Fatalf("Panes.List: %v", err)
		}
		if panesAgree(rest, got.Msg.GetPanes()) {
			return rest, got.Msg.GetPanes()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("GET /v1/panes = %v never agreed with Panes.List = %v", rest, got.Msg.GetPanes())
		case <-ticker.C:
		}
	}
}

func TestPanesListParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	startSession(t, tmux, "plain", longCommand)
	full := paneID(t, tmux, panesFixtureSession)
	options := map[string]string{
		protocol.OptionState:         string(protocol.StateWaiting),
		protocol.OptionChanged:       "1794301000",
		protocol.OptionSeen:          "1794301100",
		protocol.OptionQuestion:      synthQuestion,
		protocol.OptionSummary:       panesFixtureSummary,
		protocol.OptionLabel:         panesFixtureLabel,
		protocol.OptionContext:       panesFixtureContext,
		protocol.OptionClaudeSession: panesFixtureClaudeSession,
		protocol.OptionTool:          panesFixtureTool,
		protocol.OptionTranscript:    writeNewestTranscript(t, newestReplyLine(newestStamp0900)),
	}
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), full, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", full, name, err)
		}
	}

	rest, got := listBothUntilAgreed(t, newHandler(t, tmux))
	if len(rest) != 2 || len(got) != 2 {
		t.Fatalf("GET /v1/panes has %d panes and Panes.List %d, want 2 each", len(rest), len(got))
	}
	for _, pane := range rest {
		if pane.ID != full {
			continue
		}
		if pane.State != string(protocol.StateWaiting) || pane.Changed != panesFixtureChanged ||
			pane.Seen != panesFixtureSeen || pane.Question != synthQuestion || pane.Summary != panesFixtureSummary ||
			pane.Label != panesFixtureLabel || pane.Context != panesFixtureContext ||
			pane.ClaudeSession != panesFixtureClaudeSession || pane.Tool != panesFixtureTool ||
			pane.QuestionHash != protocol.QuestionHash(synthQuestion) || pane.NewestReply != int64(newestUnix0900) {
			t.Fatalf("pane %s fixture fields = %+v, want the options set on it", full, pane)
		}
		return
	}
	t.Fatalf("pane %s is not in GET /v1/panes %+v", full, rest)
}

func TestPanesListErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, which is neither a
	// missing server nor a success, so cli.List fails and both routes answer their internal error.
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	tmux, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(dir, "tmux.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}
	handler := newHandler(t, tmux)

	rec := get(t, handler, "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusInternalServerError)
	wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

	_, err = panesClient(t, handler).List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.List error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.List code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != wantMessage {
		t.Fatalf("Panes.List message = %q, want the REST body %q", connectErr.Message(), wantMessage)
	}
}

// screenParityCommand prints historyCount numbered lines, then the synthetic color marker in red, then keeps the
// pane alive. The marker is the last line on the visible screen, so ansi=1 changes what the screen shows, and the
// first numbered line is in scrollback, so history=N changes it too.
const screenParityCommand = `i=1; while [ $i -le 60 ]; do echo "synthetic history line $i"; i=$((i+1)); done; ` +
	`printf '\033[31m` + synthColor + `\033[0m\n'; ` + longCommand

func TestPanesScreenParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, screenParityCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"
	pollScreen(t, handler, path, synthColor)
	client := panesClient(t, handler)
	count := strconv.Itoa(historyCount)

	cases := []struct {
		name     string
		query    string
		req      *orreryv1.ScreenRequest
		contains []string
	}{
		{"plain", "", &orreryv1.ScreenRequest{Pane: pane}, []string{synthColor}},
		{"ansi", "ansi=1", &orreryv1.ScreenRequest{Pane: pane, Ansi: true}, []string{synthRed + synthColor}},
		{"history", "history=" + count, &orreryv1.ScreenRequest{Pane: pane, History: historyCount},
			[]string{historyLine(1), synthColor}},
		{"ansi and history", "ansi=1&history=" + count,
			&orreryv1.ScreenRequest{Pane: pane, Ansi: true, History: historyCount},
			[]string{historyLine(1), synthRed + synthColor}},
		{"history zero", "history=0", &orreryv1.ScreenRequest{Pane: pane, History: 0}, []string{synthColor}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			target := path
			if c.query != "" {
				target += "?" + c.query
			}
			rec := get(t, handler, target, allowedLogin)
			assertStatus(t, rec, http.StatusOK)
			for _, want := range c.contains {
				if !strings.Contains(rec.Body.String(), want) {
					t.Fatalf("REST body = %q, want it to contain %q", rec.Body.String(), want)
				}
			}
			got, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			if err != nil {
				t.Fatalf("Panes.Screen: %v", err)
			}
			if !bytes.Equal(got.Msg.GetScreen(), rec.Body.Bytes()) {
				t.Fatalf("Panes.Screen = %q, want the REST body %q", got.Msg.GetScreen(), rec.Body.String())
			}
		})
	}
}

func TestPanesScreenErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	cases := []struct {
		name   string
		id     string
		query  string
		req    *orreryv1.ScreenRequest
		status int
		code   connect.Code
	}{
		{"malformed pane id", "abc", "", &orreryv1.ScreenRequest{Pane: "abc"},
			http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", "", &orreryv1.ScreenRequest{Pane: "%999999"},
			http.StatusNotFound, connect.CodeNotFound},
		{"history above 2000", pane, "history=2001", &orreryv1.ScreenRequest{Pane: pane, History: 2001},
			http.StatusBadRequest, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			target := "/v1/panes/" + url.PathEscape(c.id) + "/screen"
			if c.query != "" {
				target += "?" + c.query
			}
			rec := get(t, handler, target, allowedLogin)
			assertStatus(t, rec, c.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != wantMessage {
				t.Fatalf("Panes.Screen message = %q, want the REST body %q", connectErr.Message(), wantMessage)
			}
		})
	}
}

func TestPanesScreenInternalErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, so checkPane fails and
	// both routes answer their internal error, as TestPanesListErrorParity arranges.
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	tmux, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(dir, "tmux.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}
	handler := newHandler(t, tmux)

	rec := get(t, handler, "/v1/panes/"+url.PathEscape("%1")+"/screen", allowedLogin)
	assertStatus(t, rec, http.StatusInternalServerError)
	wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

	_, err = panesClient(t, handler).Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{Pane: "%1"}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != wantMessage {
		t.Fatalf("Panes.Screen message = %q, want the REST body %q", connectErr.Message(), wantMessage)
	}
}
