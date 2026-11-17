package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicsGetOrchestratorParkMarksOnlyAPlannerClassPark shows that Get marks a park the planner holds as
// planner_parked and a park the owner holds as not, and that both keep their reason in parked.
func TestEpicsGetOrchestratorParkMarksOnlyAPlannerClassPark(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		class store.ParkClass
		want  bool
	}{
		{name: "a node park is planner_parked", class: store.ParkClassNode, want: true},
		{name: "a branch park is not planner_parked", class: store.ParkClassBranch, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stored := storedEpicOf("repoa/parked", "accepted", epicsAgo(time.Hour))
			stored.Parked = "synthetic park reason"
			stored.ParkClass = string(tt.class)
			fake := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
			client := epicStoreServe(t, t.TempDir(), &epicsFake{}, fake)

			got := epicsGet(t, client, "repoa/parked")
			if got.GetPlannerParked() != tt.want {
				t.Errorf("planner_parked = %v, want %v", got.GetPlannerParked(), tt.want)
			}
			if got.GetParked() != "synthetic park reason" {
				t.Errorf("parked = %q, want the park reason", got.GetParked())
			}
		})
	}
}
