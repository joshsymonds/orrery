package inapp

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
)

// paneKey is the key of the pane notice a test shows.
func paneKey(pane string) notice.Key {
	return notice.Key{Pane: pane}
}

// deskNotice is the notice a desk sends about title.
func deskNotice(title string) deskstream.Notice {
	return deskstream.Notice{State: "stopped", Title: title, Body: "body of " + title}
}

// shownNotice is the Notice a hub holds for key once Show has been given deskNotice(title).
func shownNotice(key notice.Key, title string) Notice {
	return Notice{Key: key, State: "stopped", Title: title, Body: "body of " + title}
}

// recv returns the next change on ch, failing the test if ch closes first. It has no deadline of its own.
func recv(t *testing.T, ch <-chan Change) Change {
	t.Helper()
	c, ok := <-ch
	if !ok {
		t.Fatal("change channel closed, want a change")
	}
	return c
}

// expectChanges fails the test unless ch carries want, in order.
func expectChanges(t *testing.T, ch <-chan Change, want ...Change) {
	t.Helper()
	for i, w := range want {
		if got := recv(t, ch); got != w {
			t.Fatalf("change %d = %+v, want %+v", i, got, w)
		}
	}
}

// expectClosed fails the test unless ch is closed with nothing left on it.
func expectClosed(t *testing.T, ch <-chan Change) {
	t.Helper()
	c, ok := <-ch
	if ok {
		t.Fatalf("change channel carried %+v, want closed", c)
	}
}

func TestWatchSnapshotIsInFirstShownOrder(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	hub.Show(paneKey("b"), deskNotice("b"))
	hub.Show(paneKey("a"), deskNotice("a"))
	hub.Show(paneKey("c"), deskNotice("c"))

	shown, _, stop := hub.Watch()
	t.Cleanup(stop)

	want := []Notice{shownNotice(paneKey("b"), "b"), shownNotice(paneKey("a"), "a"), shownNotice(paneKey("c"), "c")}
	if !slices.Equal(shown, want) {
		t.Fatalf("snapshot = %+v, want %+v", shown, want)
	}
}

func TestShowReplacesTheNoticeUnderItsKey(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	hub.Show(paneKey("a"), deskNotice("first"))
	hub.Show(paneKey("b"), deskNotice("other"))

	_, changes, stop := hub.Watch()
	t.Cleanup(stop)
	hub.Show(paneKey("a"), deskNotice("second"))

	expectChanges(t, changes, Change{Notice: shownNotice(paneKey("a"), "second")})

	shown, _, stopAgain := hub.Watch()
	t.Cleanup(stopAgain)
	want := []Notice{shownNotice(paneKey("a"), "second"), shownNotice(paneKey("b"), "other")}
	if !slices.Equal(shown, want) {
		t.Fatalf("snapshot after replace = %+v, want %+v", shown, want)
	}
}

func TestEndOfShownAndUnshownKeys(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	hub.Show(paneKey("a"), deskNotice("a"))
	hub.Show(paneKey("b"), deskNotice("b"))
	_, changes, stop := hub.Watch()
	t.Cleanup(stop)

	hub.End(paneKey("a"))
	hub.End(paneKey("c")) // never shown: sends nothing
	hub.End(paneKey("a")) // already ended: sends nothing
	hub.Show(paneKey("d"), deskNotice("d"))

	expectChanges(t, changes,
		Change{Notice: Notice{Key: paneKey("a")}, Ended: true},
		Change{Notice: shownNotice(paneKey("d"), "d")},
	)

	shown, _, stopAgain := hub.Watch()
	t.Cleanup(stopAgain)
	want := []Notice{shownNotice(paneKey("b"), "b"), shownNotice(paneKey("d"), "d")}
	if !slices.Equal(shown, want) {
		t.Fatalf("snapshot after end = %+v, want %+v", shown, want)
	}
}

func TestChangesReachEveryWatcherInOrder(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	_, first, stopFirst := hub.Watch()
	t.Cleanup(stopFirst)
	_, second, stopSecond := hub.Watch()
	t.Cleanup(stopSecond)

	hub.Show(paneKey("a"), deskNotice("a"))
	hub.Show(paneKey("b"), deskNotice("b"))
	hub.End(paneKey("a"))
	hub.Show(paneKey("a"), deskNotice("a again"))

	want := []Change{
		{Notice: shownNotice(paneKey("a"), "a")},
		{Notice: shownNotice(paneKey("b"), "b")},
		{Notice: Notice{Key: paneKey("a")}, Ended: true},
		{Notice: shownNotice(paneKey("a"), "a again")},
	}
	expectChanges(t, first, want...)
	expectChanges(t, second, want...)
}

func TestStopClosesItsWatchChannel(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	_, changes, stop := hub.Watch()
	stop()
	stop() // a second stop does nothing

	// A stopped watch gets no sends: sending on its closed channel would panic.
	hub.Show(paneKey("a"), deskNotice("a"))
	hub.End(paneKey("a"))
	expectClosed(t, changes)
}

func TestCloseClosesWatchesAndRefusesNewOnes(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	hub.Show(paneKey("a"), deskNotice("a"))
	_, first, stopFirst := hub.Watch()
	_, second, _ := hub.Watch()

	hub.Close()
	hub.Close()
	expectClosed(t, first)
	expectClosed(t, second)
	stopFirst() // a stop after Close does nothing

	shown, late, stopLate := hub.Watch()
	stopLate()
	if len(shown) != 0 {
		t.Fatalf("Watch after Close snapshot = %+v, want none", shown)
	}
	expectClosed(t, late)
}

func TestFullWatcherIsDroppedWhileShowReturns(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	_, full, stop := hub.Watch()

	// Nothing reads full, so its queue fills and it is dropped. Show must keep returning for every call.
	for range queueLen + 2 {
		hub.Show(paneKey("a"), deskNotice("a"))
	}

	// The queued changes are still readable, then the channel is closed.
	for range queueLen {
		recv(t, full)
	}
	expectClosed(t, full)
	stop() // a stop after the drop does nothing
}
