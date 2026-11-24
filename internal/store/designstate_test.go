package store_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// stageContract returns the design contract with id, in state, whose epic is in epicPhase ("" for no epic), starting
// after the contracts named by after.
func stageContract(id, state, epicPhase string, after ...string) store.DesignContract {
	return store.DesignContract{ID: id, State: state, EpicPhase: epicPhase, After: after}
}

// stageRecord returns a design record holding the contracts, in the order given.
func stageRecord(contracts ...store.DesignContract) store.DesignRecord {
	return store.DesignRecord{Contracts: contracts}
}

// TestDesignState asserts that DesignState returns the exact phrase of each stage for records in that stage, with the
// counts the phrase shows. Each stage's records include the cases the stages before it must not catch, such as a set
// aside contract left out of N, and the held rule's predecessors that do not lose.
func TestDesignState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []store.DesignRecord
		want    string
	}{
		{
			name:    "no_contracts",
			records: []store.DesignRecord{{}},
			want:    "no contracts yet",
		},
		{
			name: "set_aside",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractSetAside, ""),
					stageContract("b", store.ContractSetAside, ""),
				),
			},
			want: "set aside",
		},
		{
			name: "predecessor_will_not_ship",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractSetAside, ""),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, store.PhaseClosedSuperseded),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, store.PhaseClosedAbandoned),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, "ended_with_gaps"),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
				),
				// A proposed contract does not outrank the predecessor that will not ship.
				stageRecord(
					stageContract("a", store.ContractSetAside, ""),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
					stageContract("c", store.ContractProposed, ""),
				),
			},
			want: "held: a predecessor will not ship",
		},
		{
			name: "drafting",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractAccepted, ""),
					stageContract("b", store.ContractProposed, ""),
					stageContract("c", store.ContractSetAside, ""),
				),
			},
			want: "drafting: 1 of 2 accepted",
		},
		{
			name: "released",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractAccepted, "released"),
					stageContract("b", store.ContractAccepted, "done"),
					stageContract("c", store.ContractSetAside, ""),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, store.PhaseClosedShipped),
				),
			},
			want: "released",
		},
		{
			name: "building",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractAccepted, "released"),
					stageContract("b", store.ContractAccepted, "accepted"),
					stageContract("c", store.ContractAccepted, ""),
				),
			},
			want: "building: 1 of 3 released",
		},
		{
			name: "waiting",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractAccepted, "released"),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, "released"),
					stageContract("b", store.ContractAccepted, store.PhaseHeld, "a"),
					stageContract("c", store.ContractAccepted, store.PhaseClosedShipped),
				),
			},
			want: "waiting: 1 held",
		},
		{
			name: "accepted",
			records: []store.DesignRecord{
				stageRecord(
					stageContract("a", store.ContractAccepted, ""),
					stageContract("b", store.ContractAccepted, ""),
				),
				stageRecord(
					stageContract("a", store.ContractAccepted, ""),
					stageContract("b", store.ContractAccepted, ""),
					stageContract("c", store.ContractSetAside, ""),
				),
			},
			want: "accepted: 0 of 2 started",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for i, record := range tt.records {
				if got := store.DesignState(record); got != tt.want {
					t.Errorf("record %d: DesignState = %q, want %q", i, got, tt.want)
				}
			}
		})
	}
}
