package planner

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

// endingCase is one phase an epic ends in and the gaps its ending body names.
type endingCase struct {
	phase string
	gaps  []string
}

func TestAttentionEndingRoutesOneNoticeKeyedToItsEpic(t *testing.T) {
	t.Parallel()
	for _, tc := range []endingCase{
		{phase: "released"},
		{phase: "done"},
		{phase: "ended_with_gaps", gaps: []string{"the ledger misses a review", "the release run is not stored"}},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunnerStore(t)
			createRunnerEpic(t, s)
			endWorkTurn(t, s, claimedAt())
			router := &fakeAttentionRouter{}

			body := kinds.EndingBody(tc.phase, tc.gaps)
			if err := s.EndEpic(ctx, runnerEpicID, tc.phase, body, nil, claimedAt().Add(time.Second)); err != nil {
				t.Fatalf("EndEpic %s: %v", tc.phase, err)
			}
			attentionOver(s, router).pass(ctx)

			want := shown{
				key:   notice.Key{Epic: runnerEpicID},
				state: "waiting",
				desk:  deskstream.Notice{State: "waiting", Title: runnerEpicID, Body: body, Block: "report"},
				push: push.Notification{
					Project: "Plan the runner", State: protocol.StateWaiting, Summary: body, Block: "report",
				},
			}
			if len(router.notices) != 1 {
				t.Fatalf("notices after ending %s = %d, want exactly one", tc.phase, len(router.notices))
			}
			if got := shownOf(t, router.notices[0]); got != want {
				t.Errorf("notice = %+v, want %+v", got, want)
			}
		})
	}
}
