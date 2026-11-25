package route_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
)

// hostedNotice is a watched pane notice for key, whose pane is testPane whatever its host.
func hostedNotice(f *fake, key notice.Key) route.Notice {
	n := f.notice(testPane, stateWaiting, true)
	n.Key = key
	return n
}

// TestRouteHostKeys checks that the router keeps two hosts' notices for the same pane id apart: a Changed of one leaves
// the other pending, and so does a Seen of one.
func TestRouteHostKeys(t *testing.T) {
	f := newFake()
	f.setWatching(true, nil)
	r := newRouter(t, f)
	watching := lineOf(testPane, clockStart, stateWaiting, route.None, "watching")
	changed := lineOf(testPane, clockStart, stateWaiting, route.None, "changed")
	seen := lineOf(testPane, clockStart, stateWaiting, route.None, "seen")
	hostA := notice.Key{Pane: testPane, Host: "host-a"}
	hostB := notice.Key{Pane: testPane, Host: "host-b"}

	r.Notify(t.Context(), hostedNotice(f, hostA))
	r.Wait()
	r.Notify(t.Context(), hostedNotice(f, hostB))
	r.Wait()
	f.check(t, []route.Line{watching, watching}, nil, nil)

	// The Changed of host-a ends host-a's notice alone: host-b's still ends with its own Seen.
	r.Changed(hostA)
	r.Wait()
	f.check(t, []route.Line{watching, watching, changed}, nil, nil)
	r.Seen(t.Context(), hostB, clockStart)
	r.Wait()
	f.check(t, []route.Line{watching, watching, changed, seen}, nil, nil)
	f.checkSeen(t, seenCall{key: hostB, seen: clockStart})

	// The Seen of host-c ends host-c's notice alone: host-d's still ends with its own Changed.
	hostC := notice.Key{Pane: testPane, Host: "host-c"}
	hostD := notice.Key{Pane: testPane, Host: "host-d"}
	r.Notify(t.Context(), hostedNotice(f, hostC))
	r.Wait()
	r.Notify(t.Context(), hostedNotice(f, hostD))
	r.Wait()
	r.Seen(t.Context(), hostC, clockStart)
	r.Wait()
	r.Changed(hostD)
	r.Wait()
	f.check(t, []route.Line{
		watching, watching, changed, seen,
		watching, watching, seen, changed,
	}, nil, nil)
	f.checkSeen(t, seenCall{key: hostB, seen: clockStart}, seenCall{key: hostC, seen: clockStart})
}
