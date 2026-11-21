package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/exectest"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// sendClient is the client header the SendText requests of these tests carry.
const sendClient = "orrery-android"

// fakePaneSends is a PaneSends client that keeps each request it is sent, calls mark on each Record, and answers err.
type fakePaneSends struct {
	mark func()
	err  error
	mu   sync.Mutex
	reqs []*enginev1.PaneSendsRecordRequest
}

// Record keeps the request, marks the call, and answers the fake's err.
func (f *fakePaneSends) Record(
	_ context.Context,
	req *connect.Request[enginev1.PaneSendsRecordRequest],
) (*connect.Response[enginev1.PaneSendsRecordResponse], error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req.Msg)
	f.mu.Unlock()
	f.mark()
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(&enginev1.PaneSendsRecordResponse{}), nil
}

// recorded returns the requests the fake has been sent, in the order they arrived.
func (f *fakePaneSends) recorded() []*enginev1.PaneSendsRecordRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// clientHeader is a RoundTripper that sets the client header of each request to sendClient. It runs under
// headerTransport, which sets the header to its own value first.
type clientHeader struct {
	base http.RoundTripper
}

// RoundTrip sends a copy of req with the client header set to sendClient.
func (h clientHeader) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set(server.HeaderClient, sendClient)
	return h.base.RoundTrip(clone)
}

// sendClientPanes returns a Panes client, served by handler, whose requests carry allowedLogin and the sendClient
// header.
func sendClientPanes(t *testing.T, handler http.Handler) orreryv1connect.PanesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{
		Transport: headerTransport{base: clientHeader{base: http.DefaultTransport}, login: allowedLogin},
	}
	return orreryv1connect.NewPanesClient(httpClient, srv.URL)
}

// sendTextHandler builds the guarded handler over tmux, with paneSends as its PaneSends client and errs as the writer
// its failed records go to.
func sendTextHandler(
	t *testing.T, tmux *tmuxctl.Client, paneSends enginev1connect.PaneSendsClient, errs io.Writer,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:       []string{allowedLogin},
		Tmux:         tmux,
		Push:         newPushStore(t),
		PaneSends:    paneSends,
		PaneSendErrs: errs,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// installTmuxArgvLog puts a tmux shim first on PATH. Each tmux command the shim runs appends its arguments, joined by
// spaces, as one line to a log file, then runs the tmux that PATH named before. It returns the log file's path.
func installTmuxArgvLog(t *testing.T) string {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("LookPath(tmux): %v", err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(log) +
		"\nexec " + shellQuote(tmuxPath) + " \"$@\"\n"
	exectest.WriteFile(t, filepath.Join(dir, "tmux"), []byte(script), 0o700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// appendLine appends line and a newline to the file at path.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Errorf("open %s: %v", path, err)
		return
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Errorf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("close %s: %v", path, err)
	}
}

// TestPromptClientSendTextRecordsBeforeTyping checks that a SendText from orrery-android records its pane, text and
// client through the PaneSends client before the text reaches tmux, and that the record logs nothing.
func TestPromptClientSendTextRecordsBeforeTyping(t *testing.T) {
	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "sendclient", synthScreen)
	logPath := installTmuxArgvLog(t)
	errs := filepath.Join(t.TempDir(), "stderr")
	capture, err := os.Create(errs)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	t.Cleanup(func() {
		if err := capture.Close(); err != nil {
			t.Errorf("close stderr capture: %v", err)
		}
	})
	fake := &fakePaneSends{mark: func() { appendLine(t, logPath, "record") }}
	panes := sendClientPanes(t, sendTextHandler(t, tmux, fake, capture))

	_, err = panes.SendText(t.Context(), connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: synthText}))
	if err != nil {
		t.Fatalf("Panes.SendText: %v", err)
	}
	want := "\x1b[200~" + synthText + "\x1b[201~\r"
	awaitInput(t, paneExited(t, tmux, pane), file, want)

	got := fake.recorded()
	if len(got) != 1 {
		t.Fatalf("PaneSends saw %d records, want 1", len(got))
	}
	if got[0].GetPane() != pane || got[0].GetText() != synthText || got[0].GetClient() != sendClient {
		t.Fatalf("record = pane %q text %q client %q, want pane %q text %q client %q",
			got[0].GetPane(), got[0].GetText(), got[0].GetClient(), pane, synthText, sendClient)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read tmux log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	record := slices.Index(lines, "record")
	typed := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, synthText) })
	if record < 0 {
		t.Fatalf("the log has no record line: %q", lines)
	}
	if typed < 0 {
		t.Fatalf("no tmux command carries the text: %q", lines)
	}
	if typed < record {
		t.Fatalf("tmux carried the text at line %d, before the record at line %d: %q", typed, record, lines)
	}
	logged, err := os.ReadFile(errs)
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	if len(logged) != 0 {
		t.Fatalf("a successful record wrote %q to stderr, want nothing", logged)
	}
}

// TestPromptClientSendTextSendsWhenRecordFails checks that a SendText still types its text when the PaneSends client
// fails, answers the SendText as before, and writes the failure to the stderr writer as one line.
func TestPromptClientSendTextSendsWhenRecordFails(t *testing.T) {
	t.Parallel()
	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "sendclient-fail", synthScreen)
	errs := filepath.Join(t.TempDir(), "stderr")
	capture, err := os.Create(errs)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	t.Cleanup(func() {
		if err := capture.Close(); err != nil {
			t.Errorf("close stderr capture: %v", err)
		}
	})
	fake := &fakePaneSends{
		mark: func() {},
		err:  connect.NewError(connect.CodeUnavailable, errors.New("orreryd is down")),
	}
	panes := sendClientPanes(t, sendTextHandler(t, tmux, fake, capture))

	_, err = panes.SendText(t.Context(), connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: synthText}))
	if err != nil {
		t.Fatalf("Panes.SendText after a failed record: %v", err)
	}
	want := "\x1b[200~" + synthText + "\x1b[201~\r"
	awaitInput(t, paneExited(t, tmux, pane), file, want)

	if got := fake.recorded(); len(got) != 1 {
		t.Fatalf("PaneSends saw %d records, want 1", len(got))
	}
	logged, err := os.ReadFile(errs)
	if err != nil {
		t.Fatalf("read stderr capture: %v", err)
	}
	const prefix = "orrery serve: record pane send: "
	if !strings.HasPrefix(string(logged), prefix) || !strings.HasSuffix(string(logged), "\n") ||
		!strings.Contains(string(logged), "orreryd is down") {
		t.Fatalf("stderr = %q, want one line starting %q that names the failure", logged, prefix)
	}
}
