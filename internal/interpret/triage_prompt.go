package interpret

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// The caps on what a triage's prompt lists: the lines of the change's diff stat, and the lines of each list of facts
// drawn from the gate's output.
const (
	triageStatLines = 60
	triageFactLines = 12
	triageBaseLines = 15
)

// The lines of a gate's output a triage's prompt names: a failing Go test, a failing Go package, a source file and line,
// a line that reports an error, a timeout or a kill, and a line scripts/gate-remote.sh prints about where the gate ran.
var (
	triageFailingTest    = regexp.MustCompile(`^\s*--- FAIL: \S+`)
	triageFailingPackage = regexp.MustCompile(`^FAIL\s+\S+`)
	triageFileLine       = regexp.MustCompile(
		`[\w./-]*[\w-]\.(?:go|ts|tsx|js|jsx|mjs|py|rs|kt|kts|java|swift|sh|nix|sql|proto|c|h|cc|cpp|rb):\d+`,
	)
	triageErrorLine = regexp.MustCompile(
		`(?i)(^\s*(error|fatal|panic)\b|level=error|timeout|timed out|exceeded|deadline|killed|out of memory|` +
			`no space left)`,
	)
	triageGateRemote = regexp.MustCompile(`^\s*gate-remote: `)
	// triageGateLoad matches the line scripts/gate-remote.sh prints as it starts the command: here, with its reason, or
	// on a box, then the load averages over 1, 5 and 15 minutes and the CPU count of the machine that runs it.
	triageGateLoad = regexp.MustCompile(
		`^gate-remote: running .*? (here|on (\S+)) \(.*?load ([0-9.]+) ([0-9.]+) ([0-9.]+), nproc ([0-9]+)\)$`,
	)
)

// triageFacts are the facts of a failed check that a triage's prompt states. orreryd gathers them in code, so the judge
// weighs them rather than goes looking for them: the kind of the node run the check belongs to, the gate's name, "" for
// a command, the run's path, the host the epic lives on as the store names it, the base the change under test is
// measured from, the check, the runs of the same gate before it, the change's diff stat or why it could not be read,
// the paths the change touches, and the same gate's last run at the base, when one is recorded.
type triageFacts struct {
	nodeKind, gate, path, host, base string
	check                            store.NodeCheck
	earlier                          []triageEarlier
	diffStat, diffErr                string
	changed                          []string
	atBase                           store.NodeCheck
	hasBase                          bool
}

// triageEarlier is a run of the same gate at the same commit before the triaged check: its check, and its triage when
// one is recorded.
type triageEarlier struct {
	check   store.NodeCheck
	triage  store.GateTriage
	triaged bool
}

// what names the triaged gate as a person reads it: a Done gate of an integrate run, a gate node's gate, or a command.
func (f triageFacts) what() string {
	switch {
	case f.nodeKind == kindIntegrate:
		return "Done gate " + f.gate
	case f.gate != "":
		return "gate " + f.gate
	}
	return "command"
}

// triageFacts gathers the facts of the check act triages, whose epic's rows are flow. The change under test is measured
// from the epic's stored base, or from its base when it has no stored base yet, to the check's revision, read with git
// in the epic's repository on its host. A diff git cannot read is a fact too: its error is stated in place of the diff.
func (r *Runner) triageFacts(
	ctx context.Context, epic store.EpicRow, act Action, check store.NodeCheck, flow store.EpicFlow,
) (triageFacts, error) {
	facts := triageFacts{
		nodeKind: act.NodeKind, gate: act.Items[1], path: act.Path, host: epic.Host, check: check,
		base: epic.StoredBase,
	}
	if facts.base == "" {
		facts.base = epic.Base
	}
	for _, id := range act.IDs[2:] {
		earlier, found := checkOf(flow.Checks, id)
		if !found {
			continue
		}
		run := triageEarlier{check: earlier}
		if i := slices.IndexFunc(flow.Triages, func(t store.GateTriage) bool { return t.Check == id }); i >= 0 {
			run.triage, run.triaged = flow.Triages[i], true
		}
		facts.earlier = append(facts.earlier, run)
	}
	facts.atBase, facts.hasBase = baseRun(flow.Checks, check, facts.base)
	if facts.base == "" {
		facts.diffErr = "the epic has no base to measure the change from"
		return facts, nil
	}
	host, err := r.hostOf(epic)
	if err != nil {
		return triageFacts{}, fmt.Errorf("gather the facts of triage %s: %w", act.IDs[0], err)
	}
	facts.diffStat, facts.changed, facts.diffErr = changeOf(ctx, host, epic.Repository, facts.base, check.Revision)
	return facts, nil
}

// changeOf returns the change from base to revision in repo on host: its diff stat, cut by capStat, and the paths it
// touches, a rename listed under both its names. When git cannot read either, it returns why in place of both, since a
// triage states that as a fact of its own.
func changeOf(ctx context.Context, host hostexec.Host, repo, base, revision string) (string, []string, string) {
	stat, err := gitOutput(ctx, host, repo, "diff", "--stat=200", base, revision)
	if err != nil {
		return "", nil, err.Error()
	}
	listing, err := gitOutput(ctx, host, repo, "diff", "--name-only", "-z", "--no-renames", base, revision)
	if err != nil {
		return "", nil, err.Error()
	}
	var changed []string
	for path := range strings.SplitSeq(listing, "\x00") {
		if path != "" {
			changed = append(changed, path)
		}
	}
	return capStat(stat), changed, ""
}

// baseRun returns the latest check among checks, in their stored order, that ran the same argv as check at base and
// ended having run, other than check itself, and whether there is one.
func baseRun(checks []store.NodeCheck, check store.NodeCheck, base string) (store.NodeCheck, bool) {
	var latest store.NodeCheck
	found := false
	for _, other := range checks {
		if other.ID == check.ID || other.Revision != base || other.State == store.NodeRunning || other.NotRun != "" ||
			!slices.Equal(other.Argv, check.Argv) {
			continue
		}
		latest, found = other, true
	}
	return latest, found
}

// capStat returns stat, a git diff stat, cut to its first triageStatLines lines of files and its summary line, with a
// line that says how many lines of files were left out.
func capStat(stat string) string {
	lines := strings.Split(strings.TrimRight(stat, "\n"), "\n")
	if len(lines) <= triageStatLines+1 {
		return strings.Join(lines, "\n")
	}
	kept := slices.Clone(lines[:triageStatLines])
	kept = append(kept, fmt.Sprintf(" ... %d more files", len(lines)-1-triageStatLines), lines[len(lines)-1])
	return strings.Join(kept, "\n")
}

// triagePrompt returns the prompt of a gate triage: the facts orreryd gathered (triageFacts), the gate's output tails as
// TriageText reads them, how to weigh the facts, and what to return. The judgement of the facts is the judge's.
func triagePrompt(facts triageFacts) string {
	var prompt strings.Builder
	prompt.WriteString("A gate failed, and you decide why: because of the change under test, because of the machine " +
		"it ran on, or by chance. orreryd gathered the facts below; the judgement is yours.\n\n" +
		"A verdict of environment or flake runs the gate again, within a small budget. A verdict of change fails the " +
		"gate as it would fail with no triage, and for a Done gate that sends the work it checked back to be fixed. " +
		"A wrong change costs a round of work, and a wrong environment or flake costs a rerun.\n\n")
	facts.writeGate(&prompt)
	facts.writeWhere(&prompt)
	facts.writeChange(&prompt)
	facts.writeNamed(&prompt)
	facts.writeBase(&prompt)
	writeTails(&prompt, facts.check)
	prompt.WriteString(triageGuide)
	return prompt.String()
}

// writeGate writes the Gate section: what ran, at which commit, its argv and limit, how it ended, and the runs of the
// same gate at the same commit before it, with what their triages found.
func (f triageFacts) writeGate(prompt *strings.Builder) {
	prompt.WriteString("## Gate\n")
	fmt.Fprintf(prompt, "The %s of %s run %s, at commit %s.\n", f.what(), f.nodeKind, f.path, f.check.Revision)
	fmt.Fprintf(prompt, "It ran: %s\n", strings.Join(f.check.Argv, " "))
	if f.check.Limit > 0 {
		fmt.Fprintf(prompt, "Its limit is %ds.\n", f.check.Limit)
	}
	fmt.Fprintf(prompt, "It %s.\n", checkEnding(f.check))
	if !f.check.Ended.IsZero() && !f.check.Created.IsZero() {
		fmt.Fprintf(prompt, "It ended %ds after it was queued, its wait for pool slots included.\n",
			int64(f.check.Ended.Sub(f.check.Created).Seconds()))
	}
	if len(f.earlier) == 0 {
		prompt.WriteString("It is the first run of this gate at this commit.\n\n")
		return
	}
	fmt.Fprintf(prompt, "It is run %d of this gate at this commit. The runs before it:\n", len(f.earlier)+1)
	for i, run := range f.earlier {
		fmt.Fprintf(prompt, "- run %d %s", i+1, checkEnding(run.check))
		if run.triaged {
			fmt.Fprintf(prompt, ", and its triage found %s", run.triage.Verdict)
			if run.triage.Evidence != "" {
				fmt.Fprintf(prompt, ", quoting %q", run.triage.Evidence)
			}
			if run.triage.Reason != "" {
				fmt.Fprintf(prompt, ": %s", run.triage.Reason)
			}
		}
		prompt.WriteString(".\n")
	}
	prompt.WriteString("\n")
}

// writeWhere writes the Where it ran section: the host the epic lives on, and the lines scripts/gate-remote.sh printed
// about where the gate ran, the load and CPU count there, and how long it took.
func (f triageFacts) writeWhere(prompt *strings.Builder) {
	prompt.WriteString("## Where it ran\n")
	if f.host == "" {
		prompt.WriteString("The store names no host for the epic.\n")
	} else {
		fmt.Fprintf(prompt, "The epic lives on host %s, as the store names it.\n", f.host)
	}
	lines := matching(f.check, triageGateRemote)
	if len(lines) == 0 {
		prompt.WriteString("The output tails hold no gate-remote line, so where the gate ran and the load there are " +
			"not known.\n\n")
		return
	}
	prompt.WriteString("The gate's own lines about where it ran, the load average (1, 5 and 15 minutes) and CPU " +
		"count there, and how long it took:\n")
	writeList(prompt, lines)
	if load := gateLoad(lines); load != "" {
		prompt.WriteString(load + "\n")
	}
	prompt.WriteString("\n")
}

// gateLoad returns the sentence that states where the gate ran and the load there as the first of lines that
// triageGateLoad matches gives them, with the load over the last minute as a multiple of the CPU count. It returns ""
// when no line matches.
func gateLoad(lines []string) string {
	for _, line := range lines {
		match := triageGateLoad.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		where := "on the epic's host itself, not on a gate box"
		if match[2] != "" {
			where = "on the gate box " + match[2]
		}
		load := fmt.Sprintf("It ran %s. As it started, the load average there was %s, %s and %s over 1, 5 and 15 "+
			"minutes, on %s CPUs", where, match[3], match[4], match[5], match[6])
		one, oneErr := strconv.ParseFloat(match[3], 64)
		cpus, cpusErr := strconv.ParseFloat(match[6], 64)
		if oneErr == nil && cpusErr == nil && cpus > 0 {
			load += fmt.Sprintf(": %.2f times the CPU count over the last minute", one/cpus)
		}
		return load + "."
	}
	return ""
}

// writeChange writes the Change section: the diff stat of the change under test from the base to the gate's commit, or
// why it could not be read.
func (f triageFacts) writeChange(prompt *strings.Builder) {
	prompt.WriteString("## The change under test\n")
	switch {
	case f.diffErr != "":
		fmt.Fprintf(prompt, "The change from the base could not be read: %s\n\n", f.diffErr)
	case strings.TrimSpace(f.diffStat) == "":
		fmt.Fprintf(prompt, "The gate's commit changes nothing over the base %s.\n\n", f.base)
	default:
		fmt.Fprintf(prompt, "git diff --stat %s %s:\n```\n%s\n```\n\n", f.base, f.check.Revision, f.diffStat)
	}
}

// writeNamed writes the What the failure names section: the failing tests and packages, the files and lines, each with
// whether the change touches its file, and the error lines the output tails hold.
func (f triageFacts) writeNamed(prompt *strings.Builder) {
	prompt.WriteString("## What the failure names\n")
	tests := matching(f.check, triageFailingTest)
	packages := matching(f.check, triageFailingPackage)
	files := fileLines(f.check)
	reported := matching(f.check, triageErrorLine)
	if len(tests)+len(packages)+len(files)+len(reported) == 0 {
		prompt.WriteString("The output tails name no failing test, package, file line or error line.\n\n")
		return
	}
	writeNamedList(prompt, "Failing tests", tests)
	writeNamedList(prompt, "Failing packages", packages)
	if len(files) > 0 {
		prompt.WriteString("Files and lines:\n")
		for _, ref := range files {
			touched := "the change does not touch it"
			if f.touches(ref) {
				touched = "the change touches this file"
			}
			fmt.Fprintf(prompt, "- %s (%s)\n", ref, touched)
		}
	}
	writeNamedList(prompt, "Error, timeout and kill lines", reported)
	prompt.WriteString("\n")
}

// touches reports whether the change touches the file ref names, a file and line: a changed path is the file, or ends
// with it after a slash.
func (f triageFacts) touches(ref string) bool {
	file, _, _ := strings.Cut(ref, ":")
	file = strings.TrimPrefix(file, "./")
	return slices.ContainsFunc(f.changed, func(path string) bool {
		return path == file || strings.HasSuffix(path, "/"+file)
	})
}

// writeBase writes the At the base section: how the same command last ended at the base, with the last lines of its
// output tails, or that no run of it there is recorded.
func (f triageFacts) writeBase(prompt *strings.Builder) {
	prompt.WriteString("## The same gate at the base\n")
	if !f.hasBase {
		fmt.Fprintf(prompt, "No run of the same command at the base %s is recorded.\n\n", f.base)
		return
	}
	fmt.Fprintf(prompt, "The latest run of the same command at the base %s %s.\n", f.base, checkEnding(f.atBase))
	if !f.atBase.Ended.IsZero() && !f.atBase.Created.IsZero() {
		fmt.Fprintf(prompt, "It ended %ds after it was queued.\n",
			int64(f.atBase.Ended.Sub(f.atBase.Created).Seconds()))
	}
	for _, tail := range []struct{ name, text string }{{"stdout", f.atBase.Stdout}, {"stderr", f.atBase.Stderr}} {
		if lines := lastLines(kinds.TriageText(tail.text), triageBaseLines); lines != "" {
			fmt.Fprintf(prompt, "The last lines of its %s:\n```\n%s\n```\n", tail.name, lines)
		}
	}
	prompt.WriteString("\n")
}

// writeTails writes the Output tails section: the newest part of each stream of check that orreryd kept, as
// kinds.TriageText reads it, which is the text every quote is checked against.
func writeTails(prompt *strings.Builder, check store.NodeCheck) {
	fmt.Fprintf(prompt, "## Output tails\norreryd keeps the newest %d bytes of each stream, so a stream may start "+
		"part way. Every quote you return is checked against these tails, with runs of white space read as one "+
		"space.\n", store.NodeOutputMax)
	for _, tail := range []struct{ name, text string }{{"stdout", check.Stdout}, {"stderr", check.Stderr}} {
		text := strings.TrimRight(kinds.TriageText(tail.text), "\n")
		if strings.TrimSpace(text) == "" {
			fmt.Fprintf(prompt, "### %s\n(empty)\n", tail.name)
			continue
		}
		fmt.Fprintf(prompt, "### %s\n```\n%s\n```\n", tail.name, text)
	}
	prompt.WriteString("\n")
}

// triageGuide is the part of a triage's prompt that says how to weigh the facts and what to return.
const triageGuide = `## How to decide
- environment: the machine the gate ran on failed it, not the change. A lint or test run cut short by its own time
  budget or a timeout while the load was far above the CPU count, a lost connection or box, a full disk, or a killed
  process are such failures, when nothing points at the change.
- flake: the gate failed by chance and would pass if run again as it is: a test known to be timing-sensitive or racy
  failed, the change does not touch it or what it tests, and nothing in the change makes it fail.
- change: the change under test failed the gate. A failing test, a lint finding or a build error in code the change
  touches, or a failure the same gate does not show at the base, points here.

Weigh the facts against each other rather than any one alone. A heavy load is no proof: a change can make a test hang,
and a hang under load reads as a timeout. Ask whether the same gate passed at the base, whether the failing test,
package or file is one the change touches, and whether the earlier runs of this gate failed the same way. A gate that
failed the same way again after a rerun is less likely a flake. When the facts do not settle it, answer change.

You may read files in this worktree, which is checked out at the gate's commit, and run read-only git commands such as
git log, git show and git diff. Do not run the gate, its tests, builds or linters: the machine may be loaded, and the
facts above are what you judge.

## Return
Return status done, with:
- verdict: environment, flake or change.
- evidence: for environment or flake, the line or lines of the output tails above that show it, one per line, copied
  exactly as they stand. Each is checked against the tails, and a verdict whose evidence is not found there counts as
  change. Leave it empty for change.
- citation: for change, the failing test or lint line copied exactly from the tails, such as its --- FAIL line or its
  file:line finding. Leave it empty when the tails show none.
- reason: why, in one or two sentences.
`

// checkEnding names how check ended: it did not run, it was lost at a restart, it ran past its limit and was killed,
// or it exited with its exit.
func checkEnding(check store.NodeCheck) string {
	switch {
	case check.NotRun == store.LostAtRestart:
		return "was lost at a restart of orreryd"
	case check.NotRun != "":
		return "did not run: " + check.NotRun
	case check.TimedOut:
		return fmt.Sprintf("ran past its limit and was killed (exit %d)", check.Exit)
	case check.Exit == store.EnvironmentExit:
		return fmt.Sprintf("exited %d, which says its environment failed it, and was rerun", check.Exit)
	}
	return fmt.Sprintf("exited %d without running past its limit", check.Exit)
}

// matching returns the lines of check's stderr and then its stdout, as kinds.TriageText reads them and trimmed, that
// pattern matches, each once, in order, keeping the newest triageFactLines of them.
func matching(check store.NodeCheck, pattern *regexp.Regexp) []string {
	var lines []string
	for _, text := range []string{check.Stderr, check.Stdout} {
		for raw := range strings.Lines(kinds.TriageText(text)) {
			line := strings.TrimSpace(raw)
			if line != "" && pattern.MatchString(raw) && !slices.Contains(lines, line) {
				lines = append(lines, line)
			}
		}
	}
	return newest(lines)
}

// fileLines returns the source files and lines check's output names, such as tmuxctl_test.go:533, each once, in the
// order matching reads them, keeping the newest triageFactLines of them.
func fileLines(check store.NodeCheck) []string {
	var refs []string
	for _, text := range []string{check.Stderr, check.Stdout} {
		for _, ref := range triageFileLine.FindAllString(kinds.TriageText(text), -1) {
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return newest(refs)
}

// newest returns the last triageFactLines of lines, all of them when there are no more.
func newest(lines []string) []string {
	if len(lines) <= triageFactLines {
		return lines
	}
	return lines[len(lines)-triageFactLines:]
}

// lastLines returns the last n lines of text that are not blank, joined by newlines.
func lastLines(text string, n int) string {
	var lines []string
	for raw := range strings.Lines(text) {
		if line := strings.TrimRight(raw, "\n"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// writeList writes each of lines as an item of a list.
func writeList(prompt *strings.Builder, lines []string) {
	for _, line := range lines {
		fmt.Fprintf(prompt, "- %s\n", line)
	}
}

// writeNamedList writes lines under name as a list, and nothing when lines is empty.
func writeNamedList(prompt *strings.Builder, name string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(prompt, "%s:\n", name)
	writeList(prompt, lines)
}
