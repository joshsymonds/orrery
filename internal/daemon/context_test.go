package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic base URLs on loopback. Nothing here reaches a real endpoint.
const (
	stampedBaseURL  = "http://127.0.0.1:9/ctx/example"
	personalBaseURL = "http://127.0.0.1:9/ctx/personal"
)

// contextHome is a synthetic home under t.TempDir(). Its attain folder is stamped with stampedBaseURL, its broken
// folder has a settings file that does not parse, and its personal folder has no stamp. Each folder has a project
// child, and the paths of the three children are returned in that order.
func contextHome(t *testing.T) (home, attainProject, brokenProject, personalProject string) {
	t.Helper()
	home = t.TempDir()
	attain := filepath.Join(home, "attain")
	broken := filepath.Join(home, "broken")
	personal := filepath.Join(home, "personal")
	writeSettings(t, attain, `{"env":{"ANTHROPIC_BASE_URL":"`+stampedBaseURL+`"}}`)
	writeSettings(t, broken, `{"env":`)
	attainProject = filepath.Join(attain, "project")
	brokenProject = filepath.Join(broken, "project")
	personalProject = filepath.Join(personal, "project")
	for _, dir := range []string{attainProject, brokenProject, personalProject} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	return home, attainProject, brokenProject, personalProject
}

// writeSettings writes body as the settings file of folder, creating folder/.claude.
func writeSettings(t *testing.T, folder, body string) {
	t.Helper()
	dir := filepath.Join(folder, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings in %s: %v", dir, err)
	}
}

// sendFrameIn sends a user prompt frame for srv's pane whose Cwd is cwd and whose base URL is baseURL.
func sendFrameIn(t *testing.T, socket string, srv testServer, cwd, baseURL string) {
	t.Helper()
	frame := frameFor(userPromptSubmitHook, srv.pane, srv.socket)
	frame.Cwd = cwd
	frame.AnthropicBaseURL = baseURL
	sendFrame(t, socket, frame)
}

func TestDaemonContextBelowStampWithoutFrameURL(t *testing.T) {
	home, attainProject, _, _ := contextHome(t)
	srv := startServer(t)
	socket := serve(t, daemon.Config{Home: home})

	sendFrameIn(t, socket, srv, attainProject, "")
	wantOption(t, srv, protocol.OptionContext, "example")
}

func TestDaemonContextStampOverridesFrameURL(t *testing.T) {
	home, attainProject, _, _ := contextHome(t)
	srv := startServer(t)
	socket := serve(t, daemon.Config{Home: home})

	sendFrameIn(t, socket, srv, attainProject, personalBaseURL)
	wantOption(t, srv, protocol.OptionContext, "example")
}

func TestDaemonContextWithoutStampUsesFrameURL(t *testing.T) {
	home, _, _, personalProject := contextHome(t)
	srv := startServer(t)
	socket := serve(t, daemon.Config{Home: home})

	sendFrameIn(t, socket, srv, personalProject, personalBaseURL)
	wantOption(t, srv, protocol.OptionContext, "personal")
}

func TestDaemonContextBelowBrokenStampUnsets(t *testing.T) {
	home, _, brokenProject, personalProject := contextHome(t)
	srv := startServer(t)
	socket := serve(t, daemon.Config{Home: home})

	sendFrameIn(t, socket, srv, personalProject, personalBaseURL)
	wantOption(t, srv, protocol.OptionContext, "personal")

	sendFrameIn(t, socket, srv, brokenProject, personalBaseURL)
	wantOption(t, srv, protocol.OptionContext, "")
}
