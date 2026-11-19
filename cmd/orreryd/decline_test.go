package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// showQuestion respawns pane to print paneQuestion and then sleep, and waits until the question is on the pane's
// screen, where a decline reads it.
func showQuestion(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	ctx := t.Context()
	command := "printf '%s\\n' '" + paneQuestion + "'; " + testCommand
	if _, err := client.Run(ctx, "respawn-pane", "-k", "-t", pane, command); err != nil {
		t.Fatalf("respawn-pane: %v", err)
	}
	waittest.Until(t, nil, "the question on the pane's screen", func() bool {
		screen, err := client.Run(ctx, "capture-pane", "-p", "-t", pane)
		return err == nil && strings.Contains(screen, paneQuestion)
	})
}

// pendingDecline is a waiting pane showing its question, whose waiting notice is pending at the desk, and the host that
// declines it. The sinks and phone are the ones the notice and its seen report reach.
type pendingDecline struct {
	pane   string
	client *tmuxctl.Client
	sinks  *paneSinks
	phone  *seenPhone
	host   *sessionHost
}

// startPendingDecline starts a waiting pane that shows paneQuestion, with its waiting notice pending at the desk, and a
// host that declines panes through the same router. The host stops when the test ends. Each test that calls it runs one
// at a time, since startPane sets LC_ALL with t.Setenv.
func startPendingDecline(t *testing.T) pendingDecline {
	t.Helper()
	ctx := t.Context()
	home := t.TempDir()
	server, client, pane := startWaitingPane(t, home)
	showQuestion(t, client, pane)
	setPaneChanged(t, client, pane, notNowChanged)
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	sinks := newPaneSinks(notNowClock, true)
	phone := &seenPhone{sinks: sinks}
	hub := notNowRelay(sinks, phone)
	t.Cleanup(hub.router.Close)

	// With web input the notice goes to the desk, where it stays pending until it is seen.
	transition := paneTransition(server, pane, protocol.StateWaiting, filepath.Join(home, paneProject), paneWaitingHook)
	hub.notify(ctx, transition, nil, nil)
	hub.router.Wait()
	sinks.check(t,
		[]route.Line{expectedLine(notNowClock, pane, protocol.StateWaiting, route.Desk, reasonAtDesk)},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)}, nil)

	host := startNotNowHost(t, getenv, server, st, hub.router)
	t.Cleanup(func() {
		if err := host.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
	return pendingDecline{pane: pane, client: client, sinks: sinks, phone: phone, host: host}
}

// checkPending asserts that the notice is still pending: the desk's one line, its one desk post, and no seen report to
// the phone.
func (d pendingDecline) checkPending(t *testing.T) {
	t.Helper()
	d.sinks.check(t,
		[]route.Line{expectedLine(notNowClock, d.pane, protocol.StateWaiting, route.Desk, reasonAtDesk)},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)}, nil)
	if got := d.phone.seenKeys(); len(got) != 0 {
		t.Errorf("phone seen keys = %+v, want none", got)
	}
}

// TestDeclinePaneEndsPendingNoticeOnDeskAndPhone declines the question on a pending notice. The pane is sent its Escape
// and reported seen, so the notice ends on the desk's log and the phone gets its seen report.
func TestDeclinePaneEndsPendingNoticeOnDeskAndPhone(t *testing.T) {
	d := startPendingDecline(t)

	if err := d.host.manager.DeclinePane(t.Context(), d.pane, protocol.QuestionHash(paneQuestion)); err != nil {
		t.Fatalf("decline pane: %v", err)
	}
	d.sinks.check(t,
		[]route.Line{
			expectedLine(notNowClock, d.pane, protocol.StateWaiting, route.Desk, reasonAtDesk),
			expectedLine(notNowClock, d.pane, protocol.StateWaiting, route.None, reasonSeen),
		},
		[]deskstream.Notice{expectedPost(protocol.StateWaiting, paneQuestion)}, nil)
	if got, want := d.phone.seenKeys(), []notice.Key{{Pane: d.pane}}; !slices.Equal(got, want) {
		t.Errorf("phone seen keys = %+v, want %+v", got, want)
	}
}

// TestDeclinePaneRefusedInCopyModeKeepsNotice puts the pane in copy mode, which takes the Escape key in place of the
// pane. The decline is refused as ErrPaneInMode, so the notice stays pending and the pane stays in copy mode.
func TestDeclinePaneRefusedInCopyModeKeepsNotice(t *testing.T) {
	ctx := t.Context()
	d := startPendingDecline(t)
	if _, err := d.client.Run(ctx, "copy-mode", "-t", d.pane); err != nil {
		t.Fatalf("copy-mode: %v", err)
	}

	err := d.host.manager.DeclinePane(ctx, d.pane, protocol.QuestionHash(paneQuestion))
	if !errors.Is(err, enginehost.ErrPaneInMode) {
		t.Fatalf("decline in copy mode = %v, want ErrPaneInMode", err)
	}
	t.Logf("decline in copy mode refused: %v", err)
	d.checkPending(t)

	mode, err := d.client.Run(ctx, "display-message", "-p", "-t", d.pane, "#{pane_in_mode}")
	if err != nil {
		t.Fatalf("pane_in_mode: %v", err)
	}
	if got := strings.TrimSpace(mode); got != "1" {
		t.Errorf("#{pane_in_mode} = %q, want 1", got)
	}
}

// TestDeclinePaneRefusedStaleHashKeepsNotice declines with the hash of a question the pane does not ask. The decline is
// refused as ErrQuestionNotOpen, so the notice stays pending.
func TestDeclinePaneRefusedStaleHashKeepsNotice(t *testing.T) {
	d := startPendingDecline(t)

	err := d.host.manager.DeclinePane(t.Context(), d.pane, protocol.QuestionHash("synthetic other question"))
	if !errors.Is(err, enginehost.ErrQuestionNotOpen) {
		t.Fatalf("decline with another question's hash = %v, want ErrQuestionNotOpen", err)
	}
	t.Logf("decline with another question's hash refused: %v", err)
	d.checkPending(t)
}
