package route_test

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

// alertName names the alert the alert tests' notices are about.
const alertName = "synthetic-alert"

// alertNotice is a notice keyed by alertName alone. Its desk and push builds succeed.
func alertNotice() route.Notice {
	return route.Notice{
		Key:   notice.Key{Alert: alertName},
		State: stateWaiting,
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskPost(stateWaiting), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{Alert: alertName, Summary: testSummary, State: protocol.State(stateWaiting)}, nil
		},
	}
}

// alertPush is the phone push of alertNotice.
func alertPush() push.Notification {
	return push.Notification{Alert: alertName, Summary: testSummary, State: protocol.State(stateWaiting)}
}

// alertLine is the line route writes for alertNotice at clockStart. The notice log's line has no alert field, so it
// names no key.
func alertLine(routed, reason string) route.Line {
	return route.Line{Time: clockStart, Host: testHost, State: stateWaiting, Route: routed, Reason: reason}
}

func TestAlertNoticeGoesToDeskWhenDeskIsAtHand(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.setDesk(clockStart)
	r := newRouter(t, f)

	r.Notify(t.Context(), alertNotice())
	r.Wait()

	f.check(t, []route.Line{alertLine(route.Desk, "at the desk")}, []deskstream.Notice{deskPost(stateWaiting)}, nil)
	f.checkActiveTimers(t, 1)
}

func TestAlertNoticeGoesToPhoneWhenDeskIsAway(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.setDesk(clockStart - int64(route.DeskWindow/time.Second) - 1)
	r := newRouter(t, f)

	r.Notify(t.Context(), alertNotice())
	r.Wait()

	f.check(t, []route.Line{alertLine(route.DeskPhone, "away")},
		[]deskstream.Notice{deskPost(stateWaiting)}, []push.Notification{alertPush()})
	f.checkActiveTimers(t, 0)
}
