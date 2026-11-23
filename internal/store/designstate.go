package store

import "fmt"

// DesignState returns the phrase that says what state the design record is in, as its contracts and their epics show
// it. It is a pure function of the record's contracts: each contract's State and EpicPhase, and the After lists for the
// held rule. It returns the first of these phrases that applies, in this order:
//
//   - "no contracts yet": the design has no contracts.
//   - "set aside": every contract is set aside.
//   - "held: a predecessor will not ship": some held epic starts after a contract that is set aside, or whose epic
//     is in a phase that closes it without shipping.
//   - "drafting: A of N accepted": some contract is proposed.
//   - "released": every contract not set aside has an epic that is released, done or closed shipped.
//   - "building: R of N released": some epic is neither held nor in an ended phase.
//   - "waiting: H held": some epic is held.
//   - "accepted: E of N started": anything else.
//
// N is the contracts not set aside, A the accepted ones among them, R the epics released, done or closed shipped, H the
// held epics and E the contracts with an epic.
func DesignState(record DesignRecord) string {
	if len(record.Contracts) == 0 {
		return "no contracts yet"
	}
	tally := tallyDesign(record.Contracts)
	switch {
	case tally.open == 0:
		return "set aside"
	case tally.lostPredecessor:
		return "held: a predecessor will not ship"
	case tally.proposed:
		return fmt.Sprintf("drafting: %d of %d accepted", tally.accepted, tally.open)
	case tally.allShipped:
		return "released"
	case tally.building:
		return fmt.Sprintf("building: %d of %d released", tally.shipped, tally.open)
	case tally.held > 0:
		return fmt.Sprintf("waiting: %d held", tally.held)
	default:
		return fmt.Sprintf("accepted: %d of %d started", tally.started, tally.open)
	}
}

// designTally counts a design's contracts and epics for DesignState, and records the facts its phrases test. open is
// N, accepted is A, shipped is R, held is H and started is E. allShipped is true when every contract not set aside has
// an epic that ships.
type designTally struct {
	open, accepted, shipped, held, started int

	proposed, building, lostPredecessor, allShipped bool
}

// tallyDesign counts the contracts of a design for DesignState.
func tallyDesign(contracts []DesignContract) designTally {
	byID := make(map[string]DesignContract, len(contracts))
	for _, contract := range contracts {
		byID[contract.ID] = contract
	}
	tally := designTally{allShipped: true}
	for _, contract := range contracts {
		if contract.State != ContractSetAside {
			tally.open++
			if contract.State == ContractAccepted {
				tally.accepted++
			}
			if contract.State == ContractProposed {
				tally.proposed = true
			}
			if !isShippedPhase(contract.EpicPhase) {
				tally.allShipped = false
			}
		}
		if isShippedPhase(contract.EpicPhase) {
			tally.shipped++
		}
		if contract.EpicPhase != "" {
			tally.started++
		}
		switch {
		case contract.EpicPhase == PhaseHeld:
			tally.held++
			if losesPredecessor(contract.After, byID) {
				tally.lostPredecessor = true
			}
		case contract.EpicPhase != "" && !EndedPhase(contract.EpicPhase):
			tally.building = true
		}
	}
	return tally
}

// losesPredecessor reports whether one of the contracts a held epic starts after, named by after, is set aside or has
// an epic in a phase that closes it without shipping. byID holds the contracts of the design by id; an id it does not
// hold reads as a contract with no state and no epic.
func losesPredecessor(after []string, byID map[string]DesignContract) bool {
	for _, id := range after {
		predecessor := byID[id]
		if predecessor.State == ContractSetAside || isLostPhase(predecessor.EpicPhase) {
			return true
		}
	}
	return false
}

// isShippedPhase reports whether phase is one an epic ends in having shipped: released, done or closed shipped.
func isShippedPhase(phase string) bool {
	switch phase {
	case phaseReleased, phaseDone, PhaseClosedShipped:
		return true
	}
	return false
}

// isLostPhase reports whether phase is one an epic ends in without shipping: closed superseded, closed abandoned or
// ended with gaps.
func isLostPhase(phase string) bool {
	switch phase {
	case PhaseClosedSuperseded, PhaseClosedAbandoned, phaseEndedWithGaps:
		return true
	}
	return false
}
