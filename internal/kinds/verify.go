package kinds

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store"
)

// The modes of an ActVerify action, its first item. VerifyStart starts the verification step of a candidate, and
// VerifyRecord records the choice of a step that has ended. The action of a task integrator round's verification also
// carries the task's seq and the round as its second and third items; a batch's carries no more.
const (
	VerifyStart  = "start"
	VerifyRecord = "record"
)

// The floors code applies to a verification step's choice, as GateChoice.Floor and RequirementChoice.Floor name them.
// FloorAlways is a gate declared always, which runs whatever the step chose. FloorUnresolved is a gate that does not
// resolve, which runs and fails as such a gate's check always has. FloorUnmentioned is a gate the step did not name,
// which runs, or a Requirement it did not name and no floor holds, which is not captured. FloorFallback is every choice
// of a verification that fell back to running everything. FloorClaimed is a Requirement a task the verification covers
// claims or contributes to, which is captured whatever the step chose.
const (
	FloorAlways      = "always"
	FloorUnresolved  = "unresolved"
	FloorUnmentioned = "unmentioned"
	FloorFallback    = "fallback"
	FloorClaimed     = "claimed"
)

// verifyReasonMax bounds the reason a choice's line in a run's output carries. The whole reason stays on the result.
const verifyReasonMax = 200

// GateChoice is the verification choice of one Done gate: whether it runs at the candidate and the verification step's
// reason. Floor names the floor code applied (FloorAlways, FloorUnresolved, FloorUnmentioned or FloorFallback), and is
// "" when the step's own choice stands.
type GateChoice struct {
	Gate   string `json:"gate"`
	Run    bool   `json:"run"`
	Reason string `json:"reason"`
	Floor  string `json:"floor,omitempty"`
}

// RequirementChoice is the verification choice of one Requirement that names an evidence item: whether it is captured
// at the candidate and the verification step's reason. Floor names the floor code applied (FloorClaimed,
// FloorUnmentioned or FloorFallback), and is "" when the step's own choice stands.
type RequirementChoice struct {
	Requirement string `json:"requirement"`
	Capture     bool   `json:"capture"`
	Reason      string `json:"reason"`
	Floor       string `json:"floor,omitempty"`
}

// Verification is the settled choice of what verifies one candidate: the step that chose it, why it fell back to
// running everything when it did (Fallback, "" when the step's choice was read), the choice of each Done gate in Done
// gate order and of each Requirement that names an evidence item in contract order, each with the floor code applied,
// and each entry of the step's return that named no Done gate or no such Requirement, which was ignored.
type Verification struct {
	Step         string              `json:"step"`
	Fallback     string              `json:"fallback,omitempty"`
	Gates        []GateChoice        `json:"gates"`
	Requirements []RequirementChoice `json:"requirements"`
	Ignored      []string            `json:"ignored,omitempty"`
}

// RoundVerification is the verification of the merged commit of round Round of the task integrator of task Task.
type RoundVerification struct {
	Task  int64 `json:"task"`
	Round int64 `json:"round"`
	Verification
}

// Skips reports whether v records gate as skipped. A gate v does not record runs, so a Done gate added after the choice
// was made, or one a record lost, is never skipped.
func (v Verification) Skips(gate string) bool {
	i := slices.IndexFunc(v.Gates, func(choice GateChoice) bool { return choice.Gate == gate })
	return i >= 0 && !v.Gates[i].Run
}

// Captures reports whether v records Requirement id as captured.
func (v Verification) Captures(id string) bool {
	return slices.ContainsFunc(v.Requirements, func(choice RequirementChoice) bool {
		return choice.Requirement == id && choice.Capture
	})
}

// DoneGate is what code knows of one Done gate when a verification is settled: its name, whether its declaration marks
// it always, and why it does not resolve, "" when it resolves to a command gate. A gate that does not resolve runs, and
// its check ends not run as such a gate's always has.
type DoneGate struct {
	Name       string
	Always     bool
	Unresolved string
}

// VerifyEnd is the ended verification step a choice is settled from: its id, whether it ended done, how it ended when
// it did not (Why), and its structured return.
type VerifyEnd struct {
	Step   string
	Done   bool
	Why    string
	Return string
}

// VerifyScope is what a choice of Requirements is settled within: the Requirements that name an evidence item, in
// contract order, those captured whatever the step chooses (Floor), and those captured when the choice falls back to
// what ran before it was made (Fallback).
type VerifyScope struct {
	Items    []string
	Floor    []string
	Fallback []string
}

// VerifyReturn is the structured return of a verification step: a choice for Done gates and one for Requirements, each
// with its reason. Its Floor fields are always empty, since the step's schema has none.
type VerifyReturn struct {
	Gates        []GateChoice
	Requirements []RequirementChoice
}

// verifyEntry is one entry of a verification step's return as it decodes: a pointer marks a field the entry left out.
type verifyEntry struct {
	Gate        *string `json:"gate"`
	Run         *bool   `json:"run"`
	Requirement *string `json:"requirement"`
	Capture     *bool   `json:"capture"`
	Reason      string  `json:"reason"`
}

// verifyReturn is a verification step's return as it decodes.
type verifyReturn struct {
	Gates        *[]verifyEntry `json:"gates"`
	Requirements *[]verifyEntry `json:"requirements"`
}

// VerifyID returns the step id of the verification step of run: NodeWorkID of the run's id and attempt with the work
// `verify` for the batch's own, when seq is 0, and `verify T<seq> <round>` for round round of the task integrator of
// task seq. Each has its own id, so each round's choice is its own.
func VerifyID(run store.NodeRun, seq, round int64) string {
	if seq == 0 {
		return store.NodeWorkID(run.ID, run.Attempt, "verify")
	}
	return store.NodeWorkID(run.ID, run.Attempt, fmt.Sprintf("verify T%d %d", seq, round))
}

// DecodeVerifyReturn decodes raw, a verification step's return. It returns why the return cannot be read as a choice,
// "" when it can: it does not decode as the step's schema shapes it, it lacks its gates or requirements, or an entry
// names nothing, leaves out run or capture, or gives no reason. Any of these makes the whole choice fall back.
func DecodeVerifyReturn(raw string) (VerifyReturn, string) {
	var decoded verifyReturn
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return VerifyReturn{}, "the return does not decode: " + err.Error()
	}
	if decoded.Gates == nil || decoded.Requirements == nil {
		return VerifyReturn{}, "the return lacks gates or requirements"
	}
	var out VerifyReturn
	for i, entry := range *decoded.Gates {
		reason := strings.TrimSpace(entry.Reason)
		switch {
		case entry.Gate == nil || strings.TrimSpace(*entry.Gate) == "":
			return VerifyReturn{}, fmt.Sprintf("gates[%d] names no gate", i)
		case entry.Run == nil:
			return VerifyReturn{}, fmt.Sprintf("gates[%d] (%s) says nothing of whether it runs", i, *entry.Gate)
		case reason == "":
			return VerifyReturn{}, fmt.Sprintf("gates[%d] (%s) gives no reason", i, *entry.Gate)
		}
		out.Gates = append(out.Gates, GateChoice{Gate: strings.TrimSpace(*entry.Gate), Run: *entry.Run, Reason: reason})
	}
	for i, entry := range *decoded.Requirements {
		reason := strings.TrimSpace(entry.Reason)
		switch {
		case entry.Requirement == nil || strings.TrimSpace(*entry.Requirement) == "":
			return VerifyReturn{}, fmt.Sprintf("requirements[%d] names no Requirement", i)
		case entry.Capture == nil:
			return VerifyReturn{}, fmt.Sprintf(
				"requirements[%d] (%s) says nothing of whether it is captured", i, *entry.Requirement)
		case reason == "":
			return VerifyReturn{}, fmt.Sprintf("requirements[%d] (%s) gives no reason", i, *entry.Requirement)
		}
		out.Requirements = append(out.Requirements, RequirementChoice{
			Requirement: strings.TrimSpace(*entry.Requirement), Capture: *entry.Capture, Reason: reason,
		})
	}
	return out, ""
}

// SettleVerification returns the verification an ended verification step settles, with code's floors applied. A step
// that did not end done, and one whose return DecodeVerifyReturn cannot read, fall back (FallbackVerification), with
// why. Otherwise each Done gate is settled by SettleGates and each Requirement by SettleRequirements, and the entries
// either ignores are kept.
func SettleVerification(end VerifyEnd, gates []DoneGate, scope VerifyScope) Verification {
	if !end.Done {
		return FallbackVerification(end.Step, "the verification step did not end done: "+end.Why, gates, scope)
	}
	chosen, why := DecodeVerifyReturn(end.Return)
	if why != "" {
		return FallbackVerification(end.Step, why, gates, scope)
	}
	settledGates, ignoredGates := SettleGates(gates, chosen.Gates)
	settledRequirements, ignoredRequirements := SettleRequirements(scope, chosen.Requirements)
	return Verification{
		Step: end.Step, Gates: settledGates, Requirements: settledRequirements,
		Ignored: slices.Concat(ignoredGates, ignoredRequirements),
	}
}

// FallbackVerification returns the verification that runs what ran before any choice was made, for why: every Done
// gate runs and each of scope's Fallback Requirements is captured, each with FloorFallback. step is the verification
// step's id.
func FallbackVerification(step, why string, gates []DoneGate, scope VerifyScope) Verification {
	requirements := make([]RequirementChoice, 0, len(scope.Items))
	for _, id := range scope.Items {
		requirements = append(requirements, RequirementChoice{
			Requirement: id, Capture: slices.Contains(scope.Fallback, id), Floor: FloorFallback,
		})
	}
	return Verification{Step: step, Fallback: why, Gates: FallbackGates(gates), Requirements: requirements}
}

// FallbackGates returns the choice that runs every gate of gates, in their order, each with FloorFallback.
func FallbackGates(gates []DoneGate) []GateChoice {
	out := make([]GateChoice, 0, len(gates))
	for _, gate := range gates {
		out = append(out, GateChoice{Gate: gate.Name, Run: true, Floor: FloorFallback})
	}
	return out
}

// SettleGates returns the choice of each gate of gates, in their order, from chosen, the verification step's choice,
// with code's floors applied: a gate declared always runs whatever chosen says (FloorAlways), a gate that does not
// resolve runs (FloorUnresolved), and a gate chosen does not name runs (FloorUnmentioned). Each keeps the reason chosen
// gave it. A gate chosen names twice runs when one of its entries runs. It also returns a line for each entry of chosen
// that names no gate of gates, which is ignored, and for each further entry of a gate already named.
func SettleGates(gates []DoneGate, chosen []GateChoice) ([]GateChoice, []string) {
	picked := make(map[string]GateChoice, len(chosen))
	var ignored []string
	for _, choice := range chosen {
		if !slices.ContainsFunc(gates, func(gate DoneGate) bool { return gate.Name == choice.Gate }) {
			ignored = append(ignored, fmt.Sprintf("gate %s, which is not a Done gate of the epic", choice.Gate))
			continue
		}
		earlier, named := picked[choice.Gate]
		if named {
			ignored = append(ignored, fmt.Sprintf("a second entry for gate %s", choice.Gate))
			if earlier.Run || !choice.Run {
				continue
			}
		}
		picked[choice.Gate] = choice
	}
	out := make([]GateChoice, 0, len(gates))
	for _, gate := range gates {
		choice, named := picked[gate.Name]
		choice.Gate = gate.Name
		switch {
		case gate.Always:
			choice.Run, choice.Floor = true, FloorAlways
		case gate.Unresolved != "":
			choice.Run, choice.Floor = true, FloorUnresolved
		case !named:
			choice.Run, choice.Floor = true, FloorUnmentioned
		}
		out = append(out, choice)
	}
	return out, ignored
}

// SettleRequirements returns the choice of each Requirement of scope's Items, in contract order, from chosen, the
// verification step's choice, with code's floors applied: a Requirement of scope's Floor is captured whatever chosen
// says (FloorClaimed), and one chosen does not name and no floor holds is not captured (FloorUnmentioned). Each keeps
// the reason chosen gave it. A Requirement chosen names twice is captured when one of its entries captures it. It also
// returns a line for each entry of chosen that names no Requirement of scope's Items, which is ignored, and for each
// further entry of a Requirement already named.
func SettleRequirements(scope VerifyScope, chosen []RequirementChoice) ([]RequirementChoice, []string) {
	picked := make(map[string]RequirementChoice, len(chosen))
	var ignored []string
	for _, choice := range chosen {
		if !slices.Contains(scope.Items, choice.Requirement) {
			ignored = append(ignored, fmt.Sprintf(
				"Requirement %s, which is not a Requirement of the epic that names an evidence item",
				choice.Requirement,
			))
			continue
		}
		earlier, named := picked[choice.Requirement]
		if named {
			ignored = append(ignored, "a second entry for Requirement "+choice.Requirement)
			if earlier.Capture || !choice.Capture {
				continue
			}
		}
		picked[choice.Requirement] = choice
	}
	out := make([]RequirementChoice, 0, len(scope.Items))
	for _, id := range scope.Items {
		choice, named := picked[id]
		choice.Requirement = id
		switch {
		case slices.Contains(scope.Floor, id):
			choice.Capture, choice.Floor = true, FloorClaimed
		case !named:
			choice.Capture, choice.Floor = false, FloorUnmentioned
		}
		out = append(out, choice)
	}
	return out, ignored
}

// VerifyScopeOf returns the scope a verification of run settles its Requirements within: the batch's own when seq is
// 0, and the merged commit of a round of the task integrator of task seq otherwise. Items are the Requirements that
// name an evidence item, in contract order. The batch's floor and fallback are both the Requirements a task it composed
// claims or contributes to (batchRequirements), as its captures were before a choice was made. A round's floor is
// roundRequirements', and its fallback every item Requirement, which a round captured before a choice was made.
func VerifyScopeOf(run store.NodeRun, rows Rows, seq int64) VerifyScope {
	items := requirementIDs(itemRequirements(rows.Epic.Requirements))
	if seq == 0 {
		floor := requirementIDs(batchRequirements(run, rows))
		return VerifyScope{Items: items, Floor: floor, Fallback: floor}
	}
	return VerifyScope{Items: items, Floor: requirementIDs(roundRequirements(run, rows, seq)), Fallback: items}
}

// roundRequirements returns the Requirements that name an evidence item, in contract order, that a round of the task
// integrator of task seq captures whatever its verification chooses: each that seq claims or contributes to, and each
// that a task the run's result has composed so far claims or contributes to. The round merges seq into a candidate that
// holds those tasks, so its resolutions can change what any of them proves, while a Requirement none of them touches is
// held by no code the merge brings in. A merge that silently breaks such a Requirement is what the verification step
// may add a capture for, and the batch's own captures, after its rounds, are the floor for the candidate. A result
// whose composed tasks do not decode returns every Requirement that names an item, since nothing is known of what the
// candidate holds.
func roundRequirements(run store.NodeRun, rows Rows, seq int64) []store.EpicRequirement {
	items := itemRequirements(rows.Epic.Requirements)
	seqs, err := composedSeqs(run.Result)
	if err != nil {
		return items
	}
	seqs = append(seqs, seq)
	touched := make(map[string]bool)
	for _, task := range rows.Epic.Tasks {
		if slices.Contains(seqs, task.Seq) {
			for _, id := range slices.Concat(task.Requirements, task.Contributes) {
				touched[id] = true
			}
		}
	}
	return slices.DeleteFunc(items, func(requirement store.EpicRequirement) bool { return !touched[requirement.ID] })
}

// chosenRequirements returns the Requirements that name an evidence item, in contract order, that a verification
// captures: each of floor, each of fallback when chosen fell back, and each that chosen records as captured. floor and
// fallback are recomputed from the rows, so a choice whose record lacks a Requirement still captures each they hold.
func chosenRequirements(
	rows Rows, floor, fallback []store.EpicRequirement, chosen Verification,
) []store.EpicRequirement {
	holds := func(set []store.EpicRequirement, id string) bool {
		return slices.ContainsFunc(set, func(requirement store.EpicRequirement) bool { return requirement.ID == id })
	}
	return slices.DeleteFunc(itemRequirements(rows.Epic.Requirements), func(requirement store.EpicRequirement) bool {
		fellBack := chosen.Fallback != "" && holds(fallback, requirement.ID)
		return !holds(floor, requirement.ID) && !fellBack && !chosen.Captures(requirement.ID)
	})
}

// requirementIDs returns the ids of requirements, in their order.
func requirementIDs(requirements []store.EpicRequirement) []string {
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		ids = append(ids, requirement.ID)
	}
	return ids
}

// recordedVerifications returns the verifications an integrate run's result records: the batch's own, nil when none is
// recorded, and each round's. A result whose verifications do not decode records none, so its steps decide again.
func recordedVerifications(result string) (*Verification, []RoundVerification) {
	var recorded struct {
		Verification *Verification       `json:"verification"`
		Rounds       []RoundVerification `json:"roundVerifications"`
	}
	if json.Unmarshal([]byte(result), &recorded) != nil {
		return nil, nil
	}
	return recorded.Verification, recorded.Rounds
}

// batchVerification decides the verification choice of run's batch, which comes before its Done gates: settled with
// the choice its result records, and otherwise the action its verification step asks for (verifyStep).
func batchVerification(run store.NodeRun, rows Rows) (Verification, Action, bool) {
	if recorded, _ := recordedVerifications(run.Result); recorded != nil {
		return *recorded, Action{}, true
	}
	return Verification{}, verifyStep(rows, VerifyID(run, 0, 0), nil), false
}

// roundVerification decides the verification choice of the merged commit of round of the task integrator of task seq,
// which comes before the round's Done gates: settled with the choice run's result records for that task and round, and
// otherwise the action its verification step asks for (verifyStep), whose items carry seq and round.
func roundVerification(run store.NodeRun, rows Rows, seq, round int64) (Verification, Action, bool) {
	_, rounds := recordedVerifications(run.Result)
	for _, recorded := range rounds {
		if recorded.Task == seq && recorded.Round == round {
			return recorded.Verification, Action{}, true
		}
	}
	items := []string{strconv.FormatInt(seq, 10), roundItem(round)}
	return Verification{}, verifyStep(rows, VerifyID(run, seq, round), items), false
}

// verifyStep returns the action of the verification step id, whose choice is not recorded: start it when it has not
// started (VerifyStart), wait while it runs, and record it once it has ended (VerifyRecord). items follow the mode.
func verifyStep(rows Rows, id string, items []string) Action {
	step, started := firstMatch(rows.Steps, func(row store.StepRow) bool { return row.ID == id })
	mode := VerifyStart
	switch {
	case started && step.End == "":
		return Action{Kind: ActNone}
	case started:
		mode = VerifyRecord
	}
	return Action{Kind: ActVerify, IDs: []string{id}, Items: append([]string{mode}, items...)}
}

// VerificationLines returns the lines a run's output gives of chosen: for a fallback, one line that says every Done
// gate ran with the captures that ran before a choice was made, and why. Otherwise one line for each Done gate, whether
// it ran or was skipped and why, with the floor that decided it; one line for each Requirement the choice captured or
// the step chose not to capture, with its reason; and one line for each entry the step's return named that was
// ignored. A Requirement no one named and no floor holds gives no line. Each reason is cut to verifyReasonMax bytes;
// the whole of it stays on the run's result.
func VerificationLines(chosen Verification) []string {
	if chosen.Fallback != "" {
		return []string{
			fmt.Sprintf(
				"verification %s fell back to every Done gate and the default captures: %s",
				chosen.Step,
				clip(chosen.Fallback),
			),
		}
	}
	lines := make([]string, 0, len(chosen.Gates)+len(chosen.Requirements)+len(chosen.Ignored))
	for _, gate := range chosen.Gates {
		lines = append(lines, gateLine(gate))
	}
	for _, requirement := range chosen.Requirements {
		if line := requirementLine(requirement); line != "" {
			lines = append(lines, line)
		}
	}
	for _, ignored := range chosen.Ignored {
		lines = append(lines, "verification ignored "+ignored)
	}
	return lines
}

// gateLine returns the line of one gate's choice: `verification ran Done gate <g>` or `verification skipped Done gate
// <g>`, then why: the step's reason, or the floor that ran it with the step's reason when it gave one.
func gateLine(choice GateChoice) string {
	verb := "skipped"
	if choice.Run {
		verb = "ran"
	}
	line := fmt.Sprintf("verification %s Done gate %s", verb, choice.Gate)
	floor := ""
	switch choice.Floor {
	case FloorAlways:
		floor = "it is declared always"
	case FloorUnresolved:
		floor = "it does not resolve, so it runs and fails as an unresolved gate does"
	case FloorUnmentioned:
		floor = "the verification step did not name it"
	}
	return withWhy(line, floor, choice.Reason)
}

// requirementLine returns the line of one Requirement's choice: `verification captured <R>` or `verification did not
// capture <R>`, then why, as gateLine gives it. A Requirement no one named and no floor holds returns "".
func requirementLine(choice RequirementChoice) string {
	if choice.Floor == FloorUnmentioned {
		return ""
	}
	line := "verification did not capture " + choice.Requirement
	if choice.Capture {
		line = "verification captured " + choice.Requirement
	}
	floor := ""
	if choice.Floor == FloorClaimed {
		floor = "a task it verifies claims or contributes to it"
	}
	return withWhy(line, floor, choice.Reason)
}

// withWhy returns line with why it holds: `: <floor>` when a floor decided it, then `; the step said: <reason>` when the
// step gave a reason, or `: <reason>` alone when no floor did.
func withWhy(line, floor, reason string) string {
	switch {
	case floor != "" && reason != "":
		return line + ": " + floor + "; the step said: " + clip(reason)
	case floor != "":
		return line + ": " + floor
	case reason != "":
		return line + ": " + clip(reason)
	}
	return line
}

// clip returns text cut to verifyReasonMax bytes at a rune start, with an ellipsis when it was cut.
func clip(text string) string {
	const ellipsis = "..."
	if len(text) <= verifyReasonMax {
		return text
	}
	cut := verifyReasonMax - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + ellipsis
}
