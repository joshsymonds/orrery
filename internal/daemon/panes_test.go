package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panetranscript"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Synthetic transcript lines with fixed times: a prompt at second 1, then assistant lines at seconds 2 and 3.
const (
	registryPrompt = `{"type":"user","timestamp":"1970-01-01T00:00:01Z","message":{"role":"user","content":"go"}}`
	registryBash   = `{"type":"assistant","timestamp":"1970-01-01T00:00:02Z","message":{"role":"assistant",` +
		`"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make test"}}]}}`
	registryFinal = `{"type":"assistant","timestamp":"1970-01-01T00:00:03Z","message":{"role":"assistant",` +
		`"content":[{"type":"text","text":"synthetic final message"}]}}`
	registryAPIError = `{"type":"assistant","timestamp":"1970-01-01T00:00:03Z","isApiErrorMessage":true,` +
		`"error":"authentication_failed","message":{"role":"assistant",` +
		`"content":[{"type":"text","text":"API Error: 401"}]}}`
)

// Synthetic session facts of a registry update.
const (
	registryBaseURL    = "http://127.0.0.1:9/ctx/synthetic"
	registryCwd        = "/synthetic/project"
	registryTranscript = "/synthetic/transcripts/one.jsonl"
	registrySession    = "synthetic-session"
)

// registryTurnOf writes lines to a transcript under t.TempDir() and returns the turn that a panetranscript.Reader reads
// from it.
func registryTurnOf(t *testing.T, lines ...string) panetranscript.Turn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	turn, err := panetranscript.NewReader(path).Read()
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	return turn
}

// registryPaneUpdate returns an update of srv's pane for its synthetic session, with registry status and waitingFor
// and the turn that the transcript lines show.
func registryPaneUpdate(t *testing.T, srv testServer, status, waitingFor string, lines ...string) daemon.PaneUpdate {
	t.Helper()
	return daemon.PaneUpdate{
		PaneID:     srv.pane,
		Tmux:       fmt.Sprintf("%s,1234,0", srv.socket),
		SessionID:  registrySession,
		Cwd:        registryCwd,
		BaseURL:    registryBaseURL,
		Transcript: registryTranscript,
		Status:     status,
		WaitingFor: waitingFor,
		Turn:       registryTurnOf(t, lines...),
	}
}

// serveRegistry serves srv's server with home as its home directory and mirror as its Mirror, and returns the channel
// that takes pane updates and the channel that receives the observers' transitions.
func serveRegistry(
	t *testing.T, srv testServer, home string, mirror *daemon.Mirror,
) (chan<- daemon.PaneUpdate, <-chan daemon.Transition) {
	t.Helper()
	updates := make(chan daemon.PaneUpdate)
	transitions := make(chan daemon.Transition, 16)
	serve(t, daemon.Config{
		Server: srv.server,
		Home:   home,
		Mirror: mirror,
		// Nothing sends on TidyTicks, so the tidy loop runs no timer of its own.
		TidyTicks: make(chan time.Time),
		Updates:   updates,
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})
	return updates, transitions
}

// sendUpdate hands update to the daemon, which takes it once it has applied the update before it.
func sendUpdate(updates chan<- daemon.PaneUpdate, update daemon.PaneUpdate) {
	updates <- update
}

// nextTransition returns the next transition the observers receive.
func nextTransition(transitions <-chan daemon.Transition) daemon.Transition {
	return <-transitions
}

func TestPaneRegistryStateEachChangeReachesObserversOnce(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	unbound := registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt)
	unbound.Unbound = true
	sends := []daemon.PaneUpdate{
		registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt),
		registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt),
		registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingPermission,
			registryPrompt, registryBash),
		registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingDialog,
			registryPrompt, registryBash),
		registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryFinal),
		registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryFinal),
		unbound,
	}
	want := [][2]protocol.State{
		{"", protocol.StateWorking},
		{protocol.StateWorking, protocol.StateWaiting},
		{protocol.StateWaiting, protocol.StateDone},
		{protocol.StateDone, ""},
	}
	// Updates are applied in order, so a change the updates in between should have made arrives before the next
	// expected one and fails its comparison.
	for _, update := range sends {
		sendUpdate(updates, update)
	}
	for _, pair := range want {
		got := nextTransition(transitions)
		if got.PaneID != srv.pane || got.From != pair[0] || got.To != pair[1] {
			t.Fatalf("transition = %+v, want %s from %q to %q", got, srv.pane, pair[0], pair[1])
		}
	}
}

func TestPaneRegistryStateWaitingCarriesQuestionAndTool(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingPermission,
		registryPrompt, registryBash))
	got := nextTransition(transitions)
	if got.From != "" || got.To != protocol.StateWaiting {
		t.Fatalf("transition = %+v, want from no state to waiting", got)
	}
	wantOption(t, srv, protocol.OptionState, string(protocol.StateWaiting))
	wantOption(t, srv, protocol.OptionQuestion, `{"tool":"Bash","input":{"command":"make test"}}`)
	wantOption(t, srv, protocol.OptionTool, `{"id":"t1","name":"Bash","input":"make test","start":2}`)
}

func TestPaneRegistryStateDoneCarriesTurnFacts(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt))
	nextTransition(transitions)
	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryFinal))

	got := nextTransition(transitions)
	want := daemon.Transition{
		PaneID:      srv.pane,
		Server:      srv.server,
		From:        protocol.StateWorking,
		To:          protocol.StateDone,
		Cwd:         registryCwd,
		BaseURL:     registryBaseURL,
		Transcript:  registryTranscript,
		LastMessage: "synthetic final message",
	}
	if got != want {
		t.Fatalf("done transition = %+v, want %+v", got, want)
	}
}

func TestPaneRegistryStateAPIErrorIsFailedDone(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt))
	nextTransition(transitions)
	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryAPIError))

	got := nextTransition(transitions)
	want := daemon.Transition{
		PaneID:      srv.pane,
		Server:      srv.server,
		From:        protocol.StateWorking,
		To:          protocol.StateDone,
		Cwd:         registryCwd,
		BaseURL:     registryBaseURL,
		Transcript:  registryTranscript,
		LastMessage: "API Error: 401",
		Failed:      true,
		APIError:    "authentication_failed",
	}
	if got != want {
		t.Fatalf("API-error transition = %+v, want %+v", got, want)
	}
}

func TestPaneRegistryStateWritesSessionAndContext(t *testing.T) {
	home, attainProject, _, _ := contextHome(t)
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, home, nil)

	update := registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt)
	update.Cwd = attainProject
	update.BaseURL = personalBaseURL
	update.SessionID = "synthetic-session-7"
	update.Transcript = "/synthetic/transcripts/seven.jsonl"
	sendUpdate(updates, update)
	nextTransition(transitions)

	// The stamp below the update's cwd names the context, ahead of the update's base URL.
	wantOption(t, srv, protocol.OptionContext, "example")
	wantOption(t, srv, protocol.OptionClaudeSession, "synthetic-session-7")
	wantOption(t, srv, protocol.OptionTranscript, "/synthetic/transcripts/seven.jsonl")
}

func TestPaneRegistryStateUnboundEndsSession(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt))
	nextTransition(transitions)
	wantOption(t, srv, protocol.OptionClaudeSession, registrySession)

	// The unbinding names the session, but it must not be written: the pane's session options are unset.
	unbound := registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt)
	unbound.Unbound = true
	sendUpdate(updates, unbound)

	got := nextTransition(transitions)
	if got.From != protocol.StateWorking || got.To != "" {
		t.Fatalf("unbound transition = %+v, want working to no state", got)
	}
	wantOption(t, srv, protocol.OptionState, "")
	wantOption(t, srv, protocol.OptionClaudeSession, "")
	wantOption(t, srv, protocol.OptionTranscript, "")
}

func TestPaneRegistryStateUpdateWithoutPaneIsDropped(t *testing.T) {
	srv := startServer(t)
	updates, transitions := serveRegistry(t, srv, "", nil)

	// An update with no pane would write to tmux's current pane, so it is dropped with no transition. The valid update
	// after it is the first transition the observers see.
	paneless := registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt)
	paneless.PaneID = ""
	sendUpdate(updates, paneless)
	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt))

	got := nextTransition(transitions)
	if got.PaneID != srv.pane || got.From != "" || got.To != protocol.StateWorking {
		t.Fatalf("first transition = %+v, want the valid pane from no state to working", got)
	}
}

func TestPaneRegistryStateMirrorRowMatchesTmux(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	clock := func() time.Time { return time.Unix(1700000000, 0) }
	updates, transitions := serveRegistry(t, srv, "", daemon.NewMirror(st, clock, nil))

	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt))
	nextTransition(transitions)
	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingPermission,
		registryPrompt, registryBash))
	nextTransition(transitions)
	// The dialog update changes nothing, and the daemon takes it only after the waiting update's store sync.
	sendUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingDialog,
		registryPrompt, registryBash))

	checkPaneRow(t, srv, db)
}
