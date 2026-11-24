package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The controller and the two hosts each run a pane %3, so the notices of the three must stay
// apart by host.
const (
	noticeHostController = "synthetic-controller"
	noticeHostA          = "synthetic-host-a"
	noticeHostB          = "synthetic-host-b"
	noticeHostPane       = "%3"
	noticeHostCwd        = "/synthetic/project"
	noticeHostProject    = "project"
	noticeHostStart      = int64(1_800_000_000)
)

// noticeHostPush is the part of a push a notice host test checks: the host and pane the push names, and its text.
type noticeHostPush struct {
	host, pane, project string
	state               protocol.State
	summary, label      string
	question            string
}

// remoteEvent returns the event of host's pane %3 moving from one state to another, with the facts of its update. Its
// snapshot's texts name host, so each host's notice can be told from the other's.
func remoteEvent(host string, from, to protocol.State) node.PaneEvent {
	return node.PaneEvent{
		Pane: &node.PaneSnapshot{
			Server:  "-L synthetic-server",
			Pane:    noticeHostPane,
			ID:      "synthetic-id-" + host,
			Session: "synthetic-session-" + host,
			Window:  2,
			Fields: store.Fields{
				State:    string(to),
				Summary:  "synthetic summary of " + host,
				Label:    "synthetic label of " + host,
				Question: "synthetic question of " + host,
			},
		},
		Transition: &node.PaneTransition{From: from, To: to, Cwd: noticeHostCwd},
	}
}

// waitingDesk is the desk notice of the waiting pane %3 on host, as its snapshot builds it.
func waitingDesk(host string) deskstream.Notice {
	return deskstream.Notice{
		State: string(protocol.StateWaiting),
		Title: deskstream.PaneTitle(noticeHostProject, "synthetic-session-"+host, 2),
		Body:  "synthetic question of " + host,
	}
}

// waitingPush is the push of the waiting pane %3 on host, as its snapshot builds it.
func waitingPush(host string) noticeHostPush {
	return noticeHostPush{
		host:     host,
		pane:     noticeHostPane,
		project:  noticeHostProject,
		state:    protocol.StateWaiting,
		summary:  "synthetic summary of " + host,
		label:    "synthetic label of " + host,
		question: "synthetic question of " + host,
	}
}

// newNoticeHostRelay returns the controller's relay for the notice host tests. Its router writes to sinks' desk and log
// and reads sinks' clock, its phone is phone, and its own pane's read times are kept. Every pane the tests add with
// observeRemote is a remote pane. The router is closed when the test ends.
func newNoticeHostRelay(t *testing.T, sinks *paneSinks, phone route.PhoneSink) *relay {
	t.Helper()
	hub := newRelay(route.Config{
		Host:       noticeHostController,
		Now:        sinks.Now,
		After:      sinks.After,
		StoreAfter: sinks.After,
		Desk:       sinks,
		Phone:      phone,
		Subscribed: sinks.Subscribed,
		Log:        sinks,
		Logger:     slog.New(slog.DiscardHandler),
	}, &relay{orrery: noticeHostController, reads: newReadTimes()})
	t.Cleanup(hub.router.Close)
	return hub
}

// wantDesk asserts the desk notices the sinks posted. Notices that route at once decide on their own goroutines, so
// their posts come in no fixed order, and the posts are compared sorted by body.
func wantDesk(t *testing.T, sinks *paneSinks, want ...deskstream.Notice) {
	t.Helper()
	sinks.mu.Lock()
	got := slices.Clone(sinks.posts)
	sinks.mu.Unlock()
	byBody := func(a, b deskstream.Notice) int { return strings.Compare(a.Body, b.Body) }
	slices.SortFunc(got, byBody)
	slices.SortFunc(want, byBody)
	if !slices.Equal(got, want) {
		t.Errorf("desk posts = %+v, want %+v", got, want)
	}
}

// wantPushes asserts the pushes sender recorded, in order. Each push's time is the clock's, and is not compared.
func wantPushes(t *testing.T, sender *recordSender, want ...noticeHostPush) {
	t.Helper()
	sender.mu.Lock()
	defer sender.mu.Unlock()
	got := make([]noticeHostPush, 0, len(sender.pushes))
	for _, note := range sender.pushes {
		got = append(got, noticeHostPush{
			host: note.Host, pane: note.Pane, project: note.Project, state: note.State,
			summary: note.Summary, label: note.Label, question: note.Question,
		})
	}
	if !slices.Equal(got, want) {
		t.Errorf("pushes = %+v, want %+v", got, want)
	}
}

// TestNoticeHostTwoPanes checks that the waiting pane %3 on host a and the waiting pane %3 on host b hold two notices
// apart. A change of a's pane ends a's notice alone, b's escalates with its own host, and a seen mark of b clears b's
// push alone, so a's push is not cleared.
func TestNoticeHostTwoPanes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	sinks := newPaneSinks(noticeHostStart, true)
	sender := &recordSender{}
	notices := notice.New(noticeHostController, sender, sinks.Now, slog.New(slog.DiscardHandler))
	hub := newNoticeHostRelay(t, sinks, notices)

	// The desk is at hand, so each notice is held at the desk until its escalation, and both are held at once.
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateWaiting))
	hub.observeRemote(ctx, noticeHostB, remoteEvent(noticeHostB, protocol.StateWorking, protocol.StateWaiting))
	hub.router.Wait()
	wantDesk(t, sinks, waitingDesk(noticeHostA), waitingDesk(noticeHostB))
	if got := sinks.activeTimers(route.Escalation); got != 2 {
		t.Fatalf("escalations waiting = %d, want 2: one per held notice", got)
	}

	// a's pane starts working, which ends a's notice alone. b's notice is still held, and escalates to the phone.
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWaiting, protocol.StateWorking))
	hub.router.Wait()
	if got := sinks.activeTimers(route.Escalation); got != 1 {
		t.Fatalf("escalations waiting after a's change = %d, want 1: b's notice alone", got)
	}
	sinks.fire(t, route.Escalation)
	hub.router.Wait()
	notices.Wait()
	wantPushes(t, sender, waitingPush(noticeHostB))

	// a's pane waits again and escalates, so its push follows b's and carries host a.
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateWaiting))
	hub.router.Wait()
	sinks.fire(t, route.Escalation)
	hub.router.Wait()
	notices.Wait()
	wantPushes(t, sender, waitingPush(noticeHostB), waitingPush(noticeHostA))

	// A seen mark of b's pane clears b's push alone.
	seen := sinks.Now().Unix()
	hub.router.Seen(ctx, notice.Key{Pane: noticeHostPane, Host: noticeHostB}, seen)
	notices.Wait()
	sender.mu.Lock()
	clears := slices.Clone(sender.clears)
	sender.mu.Unlock()
	want := []push.Clear{{Host: noticeHostB, Pane: noticeHostPane, Seen: seen}}
	if !slices.Equal(clears, want) {
		t.Errorf("clears = %+v, want %+v: a's push is not cleared", clears, want)
	}
}

// TestNoticeHostPaneOnlyReports checks that a hook-socket seen or read report naming %3 alone touches only the
// controller's %3. Its seen report ends the controller's notice and leaves the notice of %3 on host a, which then
// escalates with host a, and its read time is kept for the controller's %3 alone.
func TestNoticeHostPaneOnlyReports(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	sinks := newPaneSinks(noticeHostStart, true)
	sender := &recordSender{}
	notices := notice.New(noticeHostController, sender, sinks.Now, slog.New(slog.DiscardHandler))
	hub := newNoticeHostRelay(t, sinks, notices)
	controller := notice.Key{Pane: noticeHostPane, Host: noticeHostController}
	hostA := notice.Key{Pane: noticeHostPane, Host: noticeHostA}

	hub.router.Notify(ctx, viewNotice(controller))
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateWaiting))
	hub.router.Wait()
	if got := sinks.activeTimers(route.Escalation); got != 2 {
		t.Fatalf("escalations waiting = %d, want 2: the controller's and a's notices", got)
	}

	hookSeen(ctx, hub.router, noticeHostController)(daemon.SeenReport{PaneID: noticeHostPane, Seen: noticeHostStart})
	hub.router.Wait()
	if got := sinks.activeTimers(route.Escalation); got != 1 {
		t.Fatalf("escalations waiting after the seen report = %d, want 1: a's notice alone", got)
	}
	sinks.fire(t, route.Escalation)
	hub.router.Wait()
	notices.Wait()
	wantPushes(t, sender, waitingPush(noticeHostA))

	now := sinks.Now()
	hookRead(hub.reads, noticeHostController)(daemon.ReadReport{PaneID: noticeHostPane, Read: now.Unix()})
	if !hub.reads.recent(controller, now) {
		t.Errorf("read time of the controller's %s is not kept", noticeHostPane)
	}
	if hub.reads.recent(hostA, now) {
		t.Errorf("the read report of %s was kept for host %s's %s", noticeHostPane, noticeHostA, noticeHostPane)
	}
}

// noticeHostsWithA is a hosts file that lists one other host, noticeHostA.
const noticeHostsWithA = `[[hosts]]
name = "` + noticeHostA + `"
address = "synthetic-host-a.example:7443"
`

// noticeHostEnv returns the getenv that locates a hosts file under a temp dir: a file holding contents, or no file when
// contents is empty.
func noticeHostEnv(t *testing.T, contents string) func(string) string {
	t.Helper()
	config := t.TempDir()
	if contents != "" {
		dir := filepath.Join(config, "orrery")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make hosts directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(contents), 0o600); err != nil {
			t.Fatalf("write hosts file: %v", err)
		}
	}
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// hostlessRelay returns the controller's relay for the hostless tests: a real Notices that sends through a
// recordSender, and a router, both attached to st as serve attaches them. The clock is sinks'.
func hostlessRelay(t *testing.T, sinks *paneSinks, st *store.Store) (*relay, *notice.Notices, *recordSender) {
	t.Helper()
	ctx := t.Context()
	sender := &recordSender{}
	notices := notice.New(
		noticeHostController, sender, sinks.Now, slog.New(slog.DiscardHandler),
		notice.WithStoreDeadline(testStoreDeadline),
	)
	hub := newNoticeHostRelay(t, sinks, notices)
	notices.Attach(ctx, st)
	hub.router.Attach(ctx, st)
	return hub, notices, sender
}

// wantPushedRows asserts the pushed rows st holds, in the order the store lists them.
func wantPushedRows(t *testing.T, st *store.Store, want ...store.PushedRow) {
	t.Helper()
	got, err := st.Pushed(t.Context())
	if err != nil {
		t.Fatalf("list pushed: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("pushed rows = %+v, want %+v", got, want)
	}
}

// wantHistoryRead asserts that key has at least one notice history row, and that each of its rows reads as read.
func wantHistoryRead(t *testing.T, st *store.Store, key notice.Key, read bool) {
	t.Helper()
	rows, err := st.NoticeHistory(t.Context(), 0)
	if err != nil {
		t.Fatalf("list notice history: %v", err)
	}
	want := store.NoticeHistoryKey(key)
	found := 0
	for _, row := range rows {
		if row.Key != want {
			continue
		}
		found++
		if row.Read != read {
			t.Errorf("history row %d of %+v reads %t, want %t", row.ID, key, row.Read, read)
		}
	}
	if found == 0 {
		t.Errorf("notice history holds no row of %+v", key)
	}
}

// TestNoticeHostHostlessSeenNoHostsFile checks that a seen report naming the pane %3 and no host is the controller's
// own pane, while the hosts file lists no other host. It ends the controller's pending notice, clears its push,
// deletes its pushed row and marks its history read. It checks a missing hosts file and one that lists no host alike.
func TestNoticeHostHostlessSeenNoHostsFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, hostsFile string }{
		{name: "no hosts.toml"},
		{name: "a hosts.toml that lists no host", hostsFile: `name = "` + noticeHostController + `"` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			sinks := newPaneSinks(noticeHostStart, true)
			st := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
			hub, notices, sender := hostlessRelay(t, sinks, st)
			controller := notice.Key{Pane: noticeHostPane, Host: noticeHostController}

			// The controller's %3 is pushed once, so its push row is stored. It then waits again, held at the desk.
			hub.router.Notify(ctx, viewNotice(controller))
			hub.router.Wait()
			sinks.fire(t, route.Escalation)
			hub.router.Wait()
			notices.Wait()
			hub.router.Notify(ctx, viewNotice(controller))
			hub.router.Wait()
			if got := sinks.activeTimers(route.Escalation); got != 1 {
				t.Fatalf("escalations waiting = %d, want 1: the controller's pending notice", got)
			}
			wantHistoryRead(t, st, controller, false)

			pageNotices, err := controllerPageNotices(
				inapp.NewHub(), hub.router, noticeHostController, sinks.Now, noticeHostEnv(t, tc.hostsFile),
			)
			if err != nil {
				t.Fatalf("page notices: %v", err)
			}
			seen := sinks.Now().Unix()
			pageNotices.Seen(ctx, notice.Key{Pane: noticeHostPane})
			notices.Wait()

			if got := sinks.activeTimers(route.Escalation); got != 0 {
				t.Errorf("escalations waiting after the seen = %d, want 0: the pending notice ends", got)
			}
			sender.mu.Lock()
			clears := slices.Clone(sender.clears)
			sender.mu.Unlock()
			wantClears := []push.Clear{{Host: noticeHostController, Pane: noticeHostPane, Seen: seen}}
			if !slices.Equal(clears, wantClears) {
				t.Errorf("clears = %+v, want %+v", clears, wantClears)
			}
			wantPushedRows(t, st)
			wantHistoryRead(t, st, controller, true)
		})
	}
}

// TestNoticeHostHostlessSeenOtherHostListed checks that a seen report naming the pane %3 and no host is dropped once
// the hosts file lists another host. The controller's %3 and host a's %3 both hold notices, and the report leaves both
// pending, clears nothing, and keeps both pushed rows and unread history. A notice under the bare key %3 is a probe:
// were the report passed on as it came, it would end that notice.
func TestNoticeHostHostlessSeenOtherHostListed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	sinks := newPaneSinks(noticeHostStart, true)
	st := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	hub, notices, sender := hostlessRelay(t, sinks, st)
	controller := notice.Key{Pane: noticeHostPane, Host: noticeHostController}
	hostA := notice.Key{Pane: noticeHostPane, Host: noticeHostA}
	probe := notice.Key{Pane: noticeHostPane}
	// escalated is how many seconds one escalation takes to fire on the sinks' clock.
	escalated := int64(route.Escalation / time.Second)

	// Each of the controller's and host a's notices is pushed once, so each push row is stored, as the clock moves on.
	hub.router.Notify(ctx, viewNotice(controller))
	hub.router.Wait()
	sinks.fire(t, route.Escalation)
	hub.router.Wait()
	notices.Wait()
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateWaiting))
	hub.router.Wait()
	sinks.fire(t, route.Escalation)
	hub.router.Wait()
	notices.Wait()

	// The controller's %3, host a's %3 and the probe each wait at the desk.
	hub.router.Notify(ctx, viewNotice(controller))
	hub.observeRemote(ctx, noticeHostA, remoteEvent(noticeHostA, protocol.StateWorking, protocol.StateWaiting))
	hub.router.Notify(ctx, viewNotice(probe))
	hub.router.Wait()
	if got := sinks.activeTimers(route.Escalation); got != 3 {
		t.Fatalf("escalations waiting = %d, want 3: the controller's, host a's and the probe's notices", got)
	}
	wantHistoryRead(t, st, controller, false)
	wantHistoryRead(t, st, hostA, false)

	pageNotices, err := controllerPageNotices(
		inapp.NewHub(), hub.router, noticeHostController, sinks.Now, noticeHostEnv(t, noticeHostsWithA),
	)
	if err != nil {
		t.Fatalf("page notices: %v", err)
	}
	pageNotices.Seen(ctx, notice.Key{Pane: noticeHostPane})
	notices.Wait()

	if got := sinks.activeTimers(route.Escalation); got != 3 {
		t.Errorf("escalations waiting after the seen = %d, want 3: every notice still pending", got)
	}
	sender.mu.Lock()
	clears := slices.Clone(sender.clears)
	sender.mu.Unlock()
	if len(clears) != 0 {
		t.Errorf("clears = %+v, want none", clears)
	}
	wantPushedRows(t, st,
		store.PushedRow{Host: noticeHostController, Pane: noticeHostPane, Time: noticeHostStart + escalated},
		store.PushedRow{Host: noticeHostA, Pane: noticeHostPane, Time: noticeHostStart + 2*escalated},
	)
	wantHistoryRead(t, st, controller, false)
	wantHistoryRead(t, st, hostA, false)
	wantHistoryRead(t, st, probe, false)
}
