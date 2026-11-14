package enginehost

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestRecorderRateLimit(t *testing.T) {
	const id = "orrery-session-7"
	resets := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "a rejected rate limit is kept for the recorder's session",
			steps: []step{
				{
					session.RateLimit{Status: "rejected", Kind: "five_hour", ResetsAt: resets},
					[]store.EngineOp{
						store.EngineRateLimited{ID: id, Status: "rejected", Kind: "five_hour", ResetsAt: resets},
					},
				},
			},
		},
		{
			name: "allowed and allowed_warning rate limits record nothing",
			steps: []step{
				{session.RateLimit{Status: "allowed", Kind: "five_hour", ResetsAt: resets}, nil},
				{session.RateLimit{Status: "allowed_warning", Kind: "five_hour", ResetsAt: resets}, nil},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRecorder(id)
			for i, st := range tc.steps {
				got := r.ops(st.event)
				if !reflect.DeepEqual(got, st.want) {
					t.Errorf("step %d (%T): ops\n got  %#v\n want %#v", i, st.event, got, st.want)
				}
			}
		})
	}
}
