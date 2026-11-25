package kinds

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/joshsymonds/orrery/internal/store"
)

// The verdicts a gate triage is recorded with: environment when the failure came from the machine the gate ran on,
// such as its load or a lost connection, flake when the gate failed by chance and would pass if run again as it is, and
// change when the change under test failed it. A triage that did not start, whose step did not end done, or whose answer
// was refused is recorded change too, so the gate fails as it would with no triage.
const (
	TriageEnvironment = "environment"
	TriageFlake       = "flake"
	TriageChange      = "change"
)

// The modes of an ActTriage action, its first item. TriageStart starts the triage step of a failed check, and
// TriageRecord records the triage once its step has ended.
const (
	TriageStart  = "start"
	TriageRecord = "record"
)

// The triage budgets of one part of a run attempt, such as a Done gate of an integrate run or the check of a gate run:
// how many reruns an environment verdict and a flake verdict each start, and how many reruns all its triages start in
// all. They are apart from the reruns an exit of store.EnvironmentExit starts and from the rounds of a task integrator.
// Once the part has spent them, a check that fails is not triaged again and fails its part.
const (
	triageEnvironmentReruns = 2
	triageFlakeReruns       = 1
	triageRerunCap          = 3
)

// triageWork returns the work of the triage step of the check whose work is work.
func triageWork(work string) string {
	return "triage " + work
}

// triageSpent counts the reruns a walk over the runs of a part passed over on a triage's verdict: those an environment
// verdict started and those a flake verdict started.
type triageSpent struct {
	environment, flake int
}

// reruns returns how many reruns the part's triages started in all.
func (s triageSpent) reruns() int {
	return s.environment + s.flake
}

// after returns s with the rerun a check that ended with outcome starts counted, when it is a triaged check's.
func (s triageSpent) after(outcome chainOutcome) triageSpent {
	switch outcome {
	case chainTriagedEnvironment:
		s.environment++
	case chainTriagedFlake:
		s.flake++
	case chainNone, chainRunning, chainLost, chainEnvironment, chainEnded, chainTriage:
	}
	return s
}

// allows reports whether a triage that answers verdict may still start a rerun of the part: the part's triages have
// started fewer than triageRerunCap reruns, and fewer than the budget of verdict. A change, or any other verdict, starts
// none.
func (s triageSpent) allows(verdict string) bool {
	if s.reruns() >= triageRerunCap {
		return false
	}
	switch verdict {
	case TriageEnvironment:
		return s.environment < triageEnvironmentReruns
	case TriageFlake:
		return s.flake < triageFlakeReruns
	}
	return false
}

// allowed returns the verdicts a triage may still start a rerun of the part on, in the order environment, flake.
func (s triageSpent) allowed() []string {
	var verdicts []string
	for _, verdict := range []string{TriageEnvironment, TriageFlake} {
		if s.allows(verdict) {
			verdicts = append(verdicts, verdict)
		}
	}
	return verdicts
}

// open reports whether a triage of the part could still start a rerun of it on some verdict. A failed check is triaged
// only while open holds, so no triage starts once no rerun could follow it.
func (s triageSpent) open() bool {
	return len(s.allowed()) > 0
}

// triagedOutcome returns how the check with id among rows.Checks ended as a walk that triages failed checks reads it,
// given outcome, how checkOutcome reads it. A check that ended and is triageable is chainTriage while no triage of it is
// recorded, and chainTriagedEnvironment or chainTriagedFlake when its recorded triage gave that verdict. Every other
// check keeps outcome, and so does a check whose triage gave a change.
func triagedOutcome(rows Rows, id string, outcome chainOutcome) chainOutcome {
	if outcome != chainEnded {
		return outcome
	}
	check, _ := checkByID(rows.Checks, id)
	if !triageable(check) {
		return outcome
	}
	triage, ok := triageOf(rows.Triages, id)
	switch {
	case !ok:
		return chainTriage
	case triage.Verdict == TriageEnvironment:
		return chainTriagedEnvironment
	case triage.Verdict == TriageFlake:
		return chainTriagedFlake
	}
	return outcome
}

// triageable reports whether check is one a triage judges: it ran and did not pass, by its exit or by running past its
// limit, and did not exit store.EnvironmentExit, which keeps its own rerun rule whether or not it timed out.
func triageable(check store.NodeCheck) bool {
	return check.NotRun == "" && !checkPassed(check) && check.Exit != store.EnvironmentExit
}

// triageOf returns the triage recorded among triages for the check with id, and whether one is.
func triageOf(triages []store.GateTriage, id string) (store.GateTriage, bool) {
	return firstMatch(triages, func(triage store.GateTriage) bool { return triage.Check == id })
}

// triageAction decides the triage of the check that decides a part of run, which end names: a check that ran and failed
// and has no triage recorded. The triage's step is the work triageWork names under run's id and attempt. A triage with
// no step starts one (TriageStart), and a paused or parked epic starts none. A triage whose step is running waits, and
// one whose step has ended is recorded (TriageRecord). The action's IDs are the step's id, the check's id, and then the
// ids of the checks of the part before it in order. Its Items are its mode, the name of the gate, "" for a command, and
// then the verdicts a rerun may still follow (triageSpent.allowed), which the record keeps the triage within.
func triageAction(run store.NodeRun, rows Rows, end chainEnd, gate string) Action {
	id := store.NodeWorkID(run.ID, run.Attempt, triageWork(end.work))
	act := Action{
		Kind:  ActTriage,
		IDs:   append([]string{id, end.id}, end.before...),
		Items: append([]string{TriageStart, gate}, end.spent.allowed()...),
	}
	step, started := firstMatch(rows.Steps, func(row store.StepRow) bool { return row.ID == id })
	switch {
	case started && step.End == "":
		return Action{Kind: ActNone}
	case started:
		act.Items[0] = TriageRecord
		return act
	case rows.Epic.Paused || rows.Epic.Parked != "":
		return Action{Kind: ActNone}
	}
	return act
}

// triageNote returns the line a failure's text carries for what triage found of the part end names, whose check
// failed: the triage of that check (triageLine), when one is recorded among triages, and the verdicts its part was rerun
// on before it, when any. It returns "" when neither is so, as for a part no triage judged.
func triageNote(triages []store.GateTriage, end chainEnd) string {
	var parts []string
	if triage, ok := triageOf(triages, end.id); ok {
		parts = append(parts, triageLine(triage))
	}
	var reran []string
	for _, id := range end.before {
		if triage, ok := triageOf(triages, id); ok && triage.Verdict != TriageChange {
			reran = append(reran, triage.Verdict)
		}
	}
	if len(reran) > 0 {
		parts = append(parts, fmt.Sprintf(
			"the gate was rerun on its earlier triages' verdicts (%s) and failed again", strings.Join(reran, ", "),
		))
	}
	if len(parts) == 0 {
		return ""
	}
	return "triage: " + strings.Join(parts, "; ")
}

// triageLine returns what triage found, as a failure's text reads it: its verdict, then the line it cites or else the
// evidence it quotes, then its reason, which says so when a change its judge gave cites nothing (judgeChange).
func triageLine(triage store.GateTriage) string {
	line := triage.Verdict
	switch {
	case triage.Citation != "":
		line += fmt.Sprintf(", citing %q", triage.Citation)
	case triage.Evidence != "":
		line += fmt.Sprintf(", quoting %q", triage.Evidence)
	}
	if triage.Reason != "" {
		line += ": " + triage.Reason
	}
	return line
}

// triageReturn is the structured return of a triage step: its verdict, the evidence it quotes from the gate's output,
// the failing test or lint line it cites, and its reason.
type triageReturn struct {
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
	Citation string `json:"citation"`
	Reason   string `json:"reason"`
}

// decodeTriage decodes raw, a triage step's return. A return that is not a JSON object of that shape is an error.
func decodeTriage(raw []byte) (triageReturn, error) {
	var ret triageReturn
	if err := json.Unmarshal(raw, &ret); err != nil {
		return triageReturn{}, fmt.Errorf("the return is not a JSON object: %w", err)
	}
	return ret, nil
}

// TriageFailures returns the lines on which raw, the return of the step that triages check, fails, which the step is
// sent back to fix: it does not decode, it names no known verdict, an environment or flake verdict quotes no evidence
// or quotes a line the check's output does not hold, or a change cites a line the output does not hold. A change that
// cites nothing passes, since the output may show no failing line. It returns none for a return that passes.
func TriageFailures(check store.NodeCheck, raw []byte) []string {
	ret, err := decodeTriage(raw)
	if err != nil {
		return []string{"triage return: " + err.Error()}
	}
	switch ret.Verdict {
	case TriageEnvironment, TriageFlake:
		return evidenceFailures(check, ret)
	case TriageChange:
		return citationFailures(check, ret)
	}
	return []string{fmt.Sprintf(
		"verdict: %q is none of %s, %s and %s", ret.Verdict, TriageEnvironment, TriageFlake, TriageChange,
	)}
}

// evidenceFailures returns the lines on which the evidence of ret, an environment or flake verdict on check, fails: it
// quotes nothing, or it quotes a line the check's output does not hold.
func evidenceFailures(check store.NodeCheck, ret triageReturn) []string {
	if spaced(TriageText(ret.Evidence)) == "" {
		return []string{fmt.Sprintf(
			"evidence: a verdict of %s quotes the line or lines of the gate's output that show it", ret.Verdict,
		)}
	}
	var failures []string
	for _, line := range unquoted(check, ret.Evidence) {
		failures = append(failures, fmt.Sprintf(
			"evidence: %q does not appear in the gate's output; quote its lines exactly as they stand", line,
		))
	}
	return failures
}

// citationFailures returns the lines on which the citation of ret, a change verdict on check, fails: each line it cites
// that the check's output does not hold.
func citationFailures(check store.NodeCheck, ret triageReturn) []string {
	missing := unquoted(check, ret.Citation)
	failures := make([]string, 0, len(missing))
	for _, line := range missing {
		failures = append(failures, fmt.Sprintf(
			"citation: %q does not appear in the gate's output; cite the failing line exactly as it stands, or "+
				"leave citation empty when the output shows none", line,
		))
	}
	return failures
}

// JudgeTriage returns the triage to record for check from row, the step that triaged it, and allowed, the verdicts a
// rerun of check's part may still follow. Its Verdict, Evidence, Citation and Reason are set, and the rest is left to
// the caller. An environment or flake verdict stands, with its evidence and reason, only when its evidence passes
// (evidenceFailures) and allowed holds it. A change stands with its citation when the check's output holds the line it
// cites, and without it otherwise. Every other answer is recorded change, with why: the step did not end done, its
// return does not decode or names no known verdict, a rerun verdict's evidence fails, or its reruns are spent.
func JudgeTriage(check store.NodeCheck, row store.StepRow, allowed []string) store.GateTriage {
	if row.End != stepDone {
		return store.GateTriage{
			Verdict: TriageChange,
			Reason:  "the triage step " + stepEnding(row) + ", so the failure counts as a change",
		}
	}
	ret, err := decodeTriage([]byte(row.Result))
	if err != nil {
		return store.GateTriage{
			Verdict: TriageChange,
			Reason:  "the triage's return is refused, so the failure counts as a change: " + err.Error(),
		}
	}
	switch ret.Verdict {
	case TriageEnvironment, TriageFlake:
		return judgeRerun(check, ret, allowed)
	case TriageChange:
		return judgeChange(check, ret)
	}
	return store.GateTriage{
		Verdict: TriageChange,
		Reason:  fmt.Sprintf("the triage named no known verdict (%q), so the failure counts as a change", ret.Verdict),
	}
}

// judgeRerun returns the triage to record for ret, an environment or flake verdict on check, with allowed the verdicts a
// rerun may still follow: the verdict, when its evidence passes and allowed holds it, and otherwise a change with why
// and with the reason the triage gave.
func judgeRerun(check store.NodeCheck, ret triageReturn, allowed []string) store.GateTriage {
	if failures := evidenceFailures(check, ret); len(failures) > 0 {
		return store.GateTriage{Verdict: TriageChange, Reason: fmt.Sprintf(
			"the triage answered %s, but its evidence is refused (%s), so the failure counts as a change; its reason: %s",
			ret.Verdict,
			strings.Join(failures, "; "),
			ret.Reason,
		)}
	}
	if !slices.Contains(allowed, ret.Verdict) {
		return store.GateTriage{Verdict: TriageChange, Evidence: ret.Evidence, Reason: fmt.Sprintf(
			"the triage answered %s, but this gate's %s reruns are spent, so the failure counts as a change; its "+
				"reason: %s",
			ret.Verdict, ret.Verdict, ret.Reason,
		)}
	}
	return store.GateTriage{Verdict: ret.Verdict, Evidence: ret.Evidence, Reason: ret.Reason}
}

// judgeChange returns the triage to record for ret, a change verdict on check: with its citation when the check's output
// holds every line of it. Otherwise it has none, and its reason ends by naming the citation the output does not hold,
// or by saying that it cites no failing test or lint line.
func judgeChange(check store.NodeCheck, ret triageReturn) store.GateTriage {
	citation := strings.TrimSpace(ret.Citation)
	var note string
	switch {
	case citation == "":
		note = "it cites no failing test or lint line"
	case len(unquoted(check, citation)) > 0:
		note = fmt.Sprintf("its citation %q does not appear in the gate's output", citation)
	default:
		return store.GateTriage{Verdict: TriageChange, Citation: citation, Reason: ret.Reason}
	}
	reason := strings.TrimSpace(ret.Reason)
	if reason == "" {
		return store.GateTriage{Verdict: TriageChange, Reason: note}
	}
	return store.GateTriage{Verdict: TriageChange, Reason: fmt.Sprintf("%s (%s)", reason, note)}
}

// stepEnding names how row, a step that did not end done, ended: its end, the status it returned, and the reason it
// stored.
func stepEnding(row store.StepRow) string {
	ending := "ended " + row.End
	if row.Status != "" {
		ending += " with status " + row.Status
	}
	if row.Reason != "" {
		ending += ": " + row.Reason
	}
	return ending
}

// unquoted returns each line of quote, with its spacing normalized, that the output of check does not hold: its stdout
// and stderr, read as TriageText reads them, with their spacing normalized too. A blank line of quote is passed over, so
// a quote of nothing holds every line.
func unquoted(check store.NodeCheck, quote string) []string {
	output := spaced(TriageText(check.Stdout) + "\n" + TriageText(check.Stderr))
	var missing []string
	for raw := range strings.Lines(TriageText(quote)) {
		if line := spaced(raw); line != "" && !strings.Contains(output, line) {
			missing = append(missing, line)
		}
	}
	return missing
}

// spaced returns text with each run of white space made one space, and none at its ends.
func spaced(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// terminalEscape matches an escape sequence a terminal reads: a control sequence such as a color, an operating system
// command, or a two-byte escape.
var terminalEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// TriageText returns text as a triage reads it: each escape sequence a terminal reads taken out, each carriage return
// read as the end of a line, and every other control character but a tab or a newline taken out. A triage's prompt
// shows a gate's output so, and its quotes are checked against the output read so.
func TriageText(text string) string {
	text = terminalEscape.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "")
	var cleaned strings.Builder
	for _, r := range text {
		switch {
		case r == '\r':
			cleaned.WriteByte('\n')
		case r == '\n' || r == '\t' || !unicode.IsControl(r):
			cleaned.WriteRune(r)
		}
	}
	return cleaned.String()
}
