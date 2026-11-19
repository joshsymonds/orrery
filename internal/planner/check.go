package planner

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// The reasons a refusal gives after its field. Each says what the field must be.
const (
	reasonFileForm    = "not a normalized repository-relative file path"
	reasonFileTwice   = "named twice in the plan"
	reasonFileOwned   = "owned by an unfinished task"
	reasonGitDir      = "inside a .git directory"
	reasonRequirement = "not a Requirement of the epic"
	reasonNotTaskFile = "not one of the task's files"
	reasonEmpty       = "empty"
	reasonNoFiles     = "no files"
)

// Check returns one refusal per bad task field of ret, in plan order, then one per broken rule of each question, in
// question order, then one per broken rule of each action, in action order, then one for an unresolvable list that is
// not a JSON array of objects, or else one per broken rule of each unresolvable entry, in entry order, or nil when the
// return passes. Each task refusal names the task by index and title, then the field, then the reason. Each question
// refusal names the question by index and label, then the field and reason that store.QuestionRefusals gives. Each action
// refusal names the action by index and kind, then the field and the reason. Each unresolvable refusal names the entry by
// index, then the field and the reason. requirements are the epic's Requirement ids, and owned are the files owned by the
// epic's unfinished tasks. gate and judge resolve a task's gate name and judge name, and a name they refuse gives the
// refusal its error text. Check does not test whether an action's target is valid; the engine does that.
func Check(ret Return, requirements []string, owned []string, gate, judge func(name string) error) []string {
	known := stringSet(requirements)
	taken := stringSet(owned)
	named := make(map[string]bool)
	refusals := make([]string, 0, len(ret.Tasks)+len(ret.Questions)+len(ret.Actions)+len(ret.Unresolvable))
	for i, task := range ret.Tasks {
		refusals = append(refusals, checkTask(i, task, known, taken, named, gate, judge)...)
	}
	for i, question := range ret.Questions {
		refusals = append(refusals, checkQuestion(i, question)...)
	}
	for i, action := range ret.Actions {
		refusals = append(refusals, checkAction(i, action)...)
		refusals = append(refusals, checkAmendAction(i, action, known, judge)...)
		refusals = append(refusals, checkApproachAction(i, action, judge)...)
	}
	refusals = append(refusals, checkUnresolvable(ret)...)
	if len(refusals) == 0 {
		return nil
	}
	return refusals
}

// checkAmendKeeps returns one refusal for each amend_evidence action of ret whose item changes the conclusion of the
// Requirement it names, and one for each whose item changes its kind, which only the owner may change: the planner amends
// how a Requirement is shown, never what it claims or what its kind checks. requirements are the epic's Requirements. An
// action that names no Requirement of the epic is refused by Check, so it gets no line here.
func checkAmendKeeps(ret Return, requirements []store.EpicRequirement) []string {
	var refusals []string
	for i, action := range ret.Actions {
		if action.Kind != store.ActionAmendEvidence {
			continue
		}
		j := slices.IndexFunc(requirements, func(requirement store.EpicRequirement) bool {
			return requirement.ID == action.Requirement
		})
		if j < 0 {
			continue
		}
		prefix := fmt.Sprintf(`actions[%d] "%s" %s`, i, action.Kind, argItem)
		if requirements[j].Item.Conclusion != action.Item.Conclusion {
			refusals = append(refusals, fmt.Sprintf(
				"%s.conclusion: differs from the conclusion of %s; only the owner amends a conclusion", prefix,
				action.Requirement))
		}
		if requirements[j].Item.Kind != action.Item.Kind {
			refusals = append(
				refusals,
				fmt.Sprintf("%s.kind: differs from the kind of %s; only the owner amends a kind",
					prefix, action.Requirement),
			)
		}
	}
	return refusals
}

// phaseCorrecting is the phase store.EndReview sets while a review's ledger is corrected, the only phase in which a
// closure-kind task may be planned.
const phaseCorrecting = "correcting"

// checkClosureTasks returns the refusals of the tasks of ret whose evidence is of kind closure, for an epic in phase. A
// closure task is planned only in a correction round, so each such task gets one line outside phaseCorrecting. It names
// only test files, so each file it names that is not a test file gets one line, named by its index in files.
func checkClosureTasks(ret Return, phase string) []string {
	var refusals []string
	for i, task := range ret.Tasks {
		if task.Evidence.Kind != evidence.KindClosure {
			continue
		}
		if phase != phaseCorrecting {
			refusals = append(refusals, fmt.Sprintf(
				`tasks[%d] "%s" evidence.kind: closure is only for a task planned in a correction round; `+
					`this epic is in phase %s`,
				i, task.Title, phase))
		}
		for j, file := range task.Files {
			if !evidence.TestFile(file) {
				refusals = append(refusals, fmt.Sprintf(
					`tasks[%d] "%s" files[%d]: not a test file; a closure-kind task names only test files `+
						`(Go _test.go, web .test.ts or .test.tsx, Kotlin under android/app/src/test/)`,
					i, task.Title, j))
			}
		}
	}
	return refusals
}

// checkCloses returns the refusals of the findings the choices of ret's questions close, for an epic in phase whose
// next closure round checks the findings open. A choice closes findings only in a correction round, so outside
// phaseCorrecting each choice that closes any gets one line. In it, each id that is not an open finding gets one line,
// which lists the open findings, so a choice never names a finding that no closure round would close.
func checkCloses(ret Return, phase string, open []string) []string {
	var refusals []string
	for i, question := range ret.Questions {
		for j, choice := range question.Choices {
			closes := closesOf(choice.Closes)
			if len(closes) == 0 {
				continue
			}
			prefix := fmt.Sprintf(`questions[%d] "%s" choices[%d].closes`, i, question.Label, j)
			if phase != phaseCorrecting {
				refusals = append(refusals, fmt.Sprintf(
					"%s: a choice closes findings only in a correction round; this epic is in phase %s", prefix, phase))
				continue
			}
			listed := "no finding is open"
			if len(open) > 0 {
				listed = "open: " + strings.Join(open, ", ")
			}
			for k, finding := range closes {
				if finding != "" && !slices.Contains(open, finding) {
					refusals = append(refusals, fmt.Sprintf("%s[%d]: %s is not an open finding of the ledger (%s)",
						prefix, k, finding, listed))
				}
			}
		}
	}
	return refusals
}

// checkPark returns the refusal of a return that answers no park: one with no action and no question. park is the
// reason of the planner-class park the epic holds, or "" when it holds none, and then every return passes. The line names
// the actions field and the reason, so the planner sees which park its return must answer.
func checkPark(ret Return, park string) []string {
	if park == "" || len(ret.Actions) > 0 || len(ret.Questions) > 0 {
		return nil
	}
	return []string{
		"actions: the epic is parked (" + park + "); return an action for the park or a catastrophe question",
	}
}

// checkUnresolvable returns the refusals of ret's unresolvable list. When the string it was read from was not a JSON array
// of objects, the list is refused as a whole with one line and no entry is checked. Otherwise an entry with an empty gap or
// an empty reason gets one line for it, and an entry whose recommendation is not release or correct gets one. An entry
// whose gap an earlier entry already names gets one line for the repeat. Each line names the entry by index, then the field.
func checkUnresolvable(ret Return) []string {
	if ret.UnresolvableError != "" {
		return []string{"unresolvable: " + reasonNotArray}
	}
	var refusals []string
	named := make(map[string]bool, len(ret.Unresolvable))
	for i, entry := range ret.Unresolvable {
		prefix := fmt.Sprintf("unresolvable[%d]", i)
		switch {
		case strings.TrimSpace(entry.Gap) == "":
			refusals = append(refusals, prefix+" gap: "+reasonEmpty)
		case named[entry.Gap]:
			refusals = append(refusals, prefix+" gap: "+reasonNamedEarlier)
		}
		named[entry.Gap] = true
		if strings.TrimSpace(entry.Reason) == "" {
			refusals = append(refusals, prefix+" reason: "+reasonEmpty)
		}
		if entry.Recommended != "release" && entry.Recommended != "correct" {
			refusals = append(refusals, prefix+" recommended: "+reasonNotReleaseOrCorrect)
		}
	}
	return refusals
}

// checkSpentGaps returns one refusal for each rerun_review action of ret that names a reviewer whose gap, "reviewer:" and
// the reviewer's name, has had store.GapTurnBound planner turns in turns, the planner turns each gap of the epic was
// given before the checked turn. Such a gap goes to the owner, so the planner declares it in unresolvable instead. An
// action that names no reviewer gets none; checkNeededFields refuses it.
func checkSpentGaps(ret Return, turns []store.GapTurn) []string {
	var refusals []string
	for i, action := range ret.Actions {
		if action.Kind != store.ActionRerunReview || action.Reviewer == "" {
			continue
		}
		gap := "reviewer:" + action.Reviewer
		given := 0
		for _, turn := range turns {
			if turn.Gap == gap {
				given++
			}
		}
		if given >= store.GapTurnBound {
			refusals = append(refusals, fmt.Sprintf(
				`actions[%d] "%s" %s: gap %s has had its %d planner turns; declare it in unresolvable instead`,
				i, action.Kind, argReviewer, gap, store.GapTurnBound))
		}
	}
	return refusals
}

// gapTriggerPrefix is the prefix of the id of each trigger a gap round writes for a live gap.
const gapTriggerPrefix = "gap:"

// priorGapTurns returns the planner turns of turns that were given before the turn woken by triggers. A gap round
// records each gap's turn when it starts, so the checked turn's own turn is already among turns. For each gap the checked
// turn was given, its highest-Turn entry is left out, and every other entry is kept, in order. The checked turn was given
// gap K when one of triggers has an id that starts gapTriggerPrefix and a cause whose first line starts "gap K: ". K is
// the text between "gap " and the first ": " of that line. A key holds no ": ", so K is the whole key.
func priorGapTurns(turns []store.GapTurn, triggers []store.EpicTrigger) []store.GapTurn {
	given := make(map[string]bool, len(triggers))
	for _, trigger := range triggers {
		if key, ok := gapKeyOf(trigger); ok {
			given[key] = true
		}
	}
	left := make(map[int]bool, len(given))
	for key := range given {
		last, found := 0, false
		var highest int64
		for i, turn := range turns {
			if turn.Gap == key && (!found || turn.Turn > highest) {
				last, highest, found = i, turn.Turn, true
			}
		}
		if found {
			left[last] = true
		}
	}
	prior := make([]store.GapTurn, 0, len(turns))
	for i, turn := range turns {
		if !left[i] {
			prior = append(prior, turn)
		}
	}
	return prior
}

// gapKeyOf returns the gap key that trigger is a gap trigger for, read from its cause's first line as priorGapTurns
// describes, and whether trigger is a gap trigger with one.
func gapKeyOf(trigger store.EpicTrigger) (string, bool) {
	if !strings.HasPrefix(trigger.ID, gapTriggerPrefix) {
		return "", false
	}
	first, _, _ := strings.Cut(trigger.Cause, "\n")
	rest, ok := strings.CutPrefix(first, "gap ")
	if !ok {
		return "", false
	}
	key, _, ok := strings.Cut(rest, ": ")
	return key, ok
}

// checkTask returns the refusals of one task's fields, in the order title, deliverable, files, test_changes,
// requirements, gate, each line of the evidence item's form, then the gate and the judge that do not resolve. named
// records each well-formed file the plan names, so a later naming of the same file is refused.
func checkTask(i int, task Task, known, taken, named map[string]bool, gate, judge func(name string) error) []string {
	prefix := fmt.Sprintf(`tasks[%d] "%s"`, i, task.Title)
	var refusals []string
	if strings.TrimSpace(task.Title) == "" {
		refusals = append(refusals, prefix+" title: "+reasonEmpty)
	}
	if strings.TrimSpace(task.Deliverable) == "" {
		refusals = append(refusals, prefix+" deliverable: "+reasonEmpty)
	}
	if len(task.Files) == 0 {
		refusals = append(refusals, prefix+" files: "+reasonNoFiles)
	}
	for j, file := range task.Files {
		if reason := fileReason(file, taken, named); reason != "" {
			refusals = append(refusals, fmt.Sprintf("%s files[%d]: %s", prefix, j, reason))
		}
	}
	refusals = append(refusals, testChangeRefusals(prefix, task)...)
	for k, id := range task.Requirements {
		if !known[id] {
			refusals = append(refusals, fmt.Sprintf("%s requirements[%d]: %s", prefix, k, reasonRequirement))
		}
	}
	if strings.TrimSpace(task.Gate) == "" {
		refusals = append(refusals, prefix+" gate: "+reasonEmpty)
	}
	for _, line := range evidence.CheckTask(task.Evidence) {
		refusals = append(refusals, prefix+" evidence."+line)
	}
	if testItemOfTestsOnly(task) {
		refusals = append(refusals, prefix+" evidence.kind: "+reasonTestsOnly)
	}
	if strings.TrimSpace(task.Gate) != "" {
		if err := gate(task.Gate); err != nil {
			refusals = append(refusals, prefix+" gate: "+err.Error())
		}
	}
	if strings.TrimSpace(task.Evidence.Judge) != "" {
		if err := judge(task.Evidence.Judge); err != nil {
			refusals = append(refusals, prefix+" evidence.judge: "+err.Error())
		}
	}
	return refusals
}

// reasonTestsOnly is why a test item is refused on a task whose files are all test files.
const reasonTestsOnly = "test, but every file of the task is a test file. A test item's green first runs its tests at " +
	"the base with the rest of the commit left out, and they must fail there; with no other file in the change " +
	"that run is the commit itself, so it passes as green does and the task can never be met. Give a task that " +
	"only adds, rewrites or fixes tests kind capture with no tests, and a conclusion its base output cannot show"

// testItemOfTestsOnly reports whether task's evidence is a test item while every one of its files is a test file
// (evidence.TestFile). Its reproduction overlays its tests on the base, so the change it would leave out is empty.
func testItemOfTestsOnly(task Task) bool {
	return task.Evidence.Kind == evidence.KindTest && len(task.Files) > 0 && !slices.ContainsFunc(task.Files,
		func(file string) bool { return !evidence.TestFile(file) })
}

// testChangeRefusals returns one refusal per test_changes element of task that is not one of the task's files.
func testChangeRefusals(prefix string, task Task) []string {
	var refusals []string
	for j, file := range task.TestChanges {
		if !slices.Contains(task.Files, file) {
			refusals = append(refusals, fmt.Sprintf("%s test_changes[%d]: %s", prefix, j, reasonNotTaskFile))
		}
	}
	return refusals
}

// catastropheKinds returns the four kinds of catastrophe a planner question names: the only grounds on which a question
// asks the owner. The schema's catastrophe enum lists the same four.
func catastropheKinds() []string {
	return []string{"irreversible-step", "contract-cannot-hold", "premise-false", "cannot-proceed"}
}

// checkQuestion returns the refusals of one question, in the order store.QuestionRefusals states its rules, then the
// catastrophe refusal, which the store does not state. Each line names the question by index and label, so the planner
// sees the same rules the store applies when it writes the turn.
func checkQuestion(i int, question Question) []string {
	prefix := fmt.Sprintf(`questions[%d] "%s"`, i, question.Label)
	lines := store.QuestionRefusals(questionInputOf(question))
	kinds := catastropheKinds()
	if !slices.Contains(kinds, question.Catastrophe) {
		lines = append(lines, "catastrophe: not one of "+strings.Join(kinds, ", "))
	}
	refusals := make([]string, 0, len(lines))
	for _, line := range lines {
		refusals = append(refusals, prefix+" "+line)
	}
	return refusals
}

// The closes an action may name: a superseded close names the epic that replaces it, and an abandoned close may name one.
const (
	closeSuperseded = "superseded"
	closeAbandoned  = "abandoned"
)

// The reason a refusal gives for an action field that is not above 0, and for one that is not a close the planner may
// name, and the reason for a field the action's kind does not use.
const (
	reasonAboveZero = "not above 0"
	reasonNotClose  = "not superseded or abandoned"
	reasonUnused    = "not used by this kind; leave it empty or 0"
)

// The reasons a refusal of an unresolvable list gives: the list is not a JSON array of objects, an entry's recommendation is
// not release or correct, and an entry's gap is one an earlier entry already names.
const (
	reasonNotArray            = "not a JSON array of objects with gap, reason and recommended"
	reasonNotReleaseOrCorrect = "not release or correct"
	reasonNamedEarlier        = "names a gap an earlier entry names"
)

// actionKinds returns the kinds an action may name, in the order the refusal lists them.
func actionKinds() []string {
	return []string{
		store.ActionRetryNode, store.ActionRetryEpic, store.ActionFailTask, store.ActionResumeRelease,
		store.ActionCloseEpic, store.ActionAmendEvidence, store.ActionChangeApproach,
		store.ActionRerunReview,
	}
}

// actionFields returns the fields kind uses, by the names the return gives them, and whether kind is one of actionKinds.
// resume_release uses undo_none, so the shape check passes a named position on to the engine, which refuses it: only the
// owner names an undo-none step.
func actionFields(kind string) ([]string, bool) {
	switch kind {
	case store.ActionRetryNode:
		return []string{argNode}, true
	case store.ActionRetryEpic:
		return nil, true
	case store.ActionFailTask:
		return []string{argTask}, true
	case store.ActionResumeRelease:
		return []string{argUndoNone}, true
	case store.ActionCloseEpic:
		return []string{argClose, argBy}, true
	case store.ActionAmendEvidence:
		return []string{argRequirement, argItem}, true
	case store.ActionChangeApproach:
		return []string{argTask, argDeliverable, argItem}, true
	case store.ActionRerunReview:
		return []string{argReviewer}, true
	}
	return nil, false
}

// checkAction returns the refusals of one action of a return, at position i, in the order of the rules: the kind, args that
// do not read or hold a key no kind uses, the fields its kind needs, the fields its kind does not use, then the reason. An
// unknown kind has no other field to check. An amend_evidence action's own fields are checkAmendAction's, and a
// change_approach action's item is checkApproachAction's.
func checkAction(i int, action EngineAction) []string {
	prefix := fmt.Sprintf(`actions[%d] "%s"`, i, action.Kind)
	var refusals []string
	refuse := func(field, reason string) {
		refusals = append(refusals, prefix+" "+field+": "+reason)
	}
	used, known := actionFields(action.Kind)
	switch {
	case known && action.ArgsError != "":
		refuse("args", action.ArgsError)
	case known:
		for _, key := range action.Unknown {
			refuse("args", "no kind uses the key "+key)
		}
		checkNeededFields(action, refuse)
		checkUnusedFields(action, used, refuse)
	default:
		refuse("kind", "not one of "+strings.Join(actionKinds(), ", "))
	}
	if strings.TrimSpace(action.Reason) == "" {
		refuse("reason", reasonEmpty)
	}
	return refusals
}

// checkNeededFields calls refuse for each field action's kind needs that is missing or wrong: a retry_node names a node,
// a fail_task names a task above 0, a close_epic names a close of superseded or abandoned and, when superseded, a by,
// a change_approach names a task above 0 and a deliverable, and a rerun_review names a reviewer. An amend_evidence
// action's fields are checkAmendAction's, and a change_approach action's item is checkApproachAction's.
func checkNeededFields(action EngineAction, refuse func(field, reason string)) {
	switch action.Kind {
	case store.ActionRetryNode:
		if strings.TrimSpace(action.Node) == "" {
			refuse(argNode, reasonEmpty)
		}
	case store.ActionRerunReview:
		if strings.TrimSpace(action.Reviewer) == "" {
			refuse(argReviewer, reasonEmpty)
		}
	case store.ActionFailTask:
		if action.Task <= 0 {
			refuse(argTask, reasonAboveZero)
		}
	case store.ActionCloseEpic:
		if action.Close != closeSuperseded && action.Close != closeAbandoned {
			refuse(argClose, reasonNotClose)
		}
		if action.Close == closeSuperseded && strings.TrimSpace(action.By) == "" {
			refuse(argBy, reasonEmpty)
		}
	case store.ActionChangeApproach:
		if action.Task <= 0 {
			refuse(argTask, reasonAboveZero)
		}
		if strings.TrimSpace(action.Deliverable) == "" {
			refuse(argDeliverable, reasonEmpty)
		}
	}
}

// checkAmendAction returns the refusals of the fields of an amend_evidence action at position i, named as checkAction
// names its lines: a requirement that is not one of the epic's Requirement ids, each line evidence.Check gives for the
// item, as item.<line>, and an item judge that judge does not resolve. An action of another kind, and one whose args did
// not read, which checkAction refuses, get none. Whether the item changes the Requirement's conclusion or kind is
// checkAmendKeeps' to refuse, and whether its red holds at the epic's base is the engine's.
func checkAmendAction(
	i int,
	action EngineAction,
	requirements map[string]bool,
	judge func(name string) error,
) []string {
	if action.Kind != store.ActionAmendEvidence || action.ArgsError != "" {
		return nil
	}
	prefix := fmt.Sprintf(`actions[%d] "%s"`, i, action.Kind)
	var refusals []string
	if !requirements[action.Requirement] {
		refusals = append(refusals, prefix+" "+argRequirement+": "+reasonRequirement)
	}
	return append(refusals, checkItemLines(prefix, action.Item, judge)...)
}

// checkApproachAction returns the refusals of the item of a change_approach action at position i, named as checkAction
// names its lines: each line evidence.Check gives for the item, as item.<line>, and an item judge that judge does not
// resolve. The zero item is no item, as the store stores it, so it gets none, and so does an action of another kind, and
// one whose args did not read, which checkAction refuses. Any other item is checked, so the store never keeps an item the
// planner did not check.
func checkApproachAction(i int, action EngineAction, judge func(name string) error) []string {
	if action.Kind != store.ActionChangeApproach || action.ArgsError != "" || reflect.ValueOf(action.Item).IsZero() {
		return nil
	}
	return checkItemLines(fmt.Sprintf(`actions[%d] "%s"`, i, action.Kind), action.Item, judge)
}

// checkItemLines returns the refusals of item as the item field of the action named by prefix: each line evidence.Check
// gives, as item.<line>, and an item.judge line when judge does not resolve item's judge.
func checkItemLines(prefix string, item evidence.Item, judge func(name string) error) []string {
	var refusals []string
	for _, line := range evidence.Check(item) {
		refusals = append(refusals, prefix+" "+argItem+"."+line)
	}
	if strings.TrimSpace(item.Judge) != "" {
		if err := judge(item.Judge); err != nil {
			refusals = append(refusals, prefix+" "+argItem+".judge: "+err.Error())
		}
	}
	return refusals
}

// checkUnusedFields calls refuse for each field action sets that its kind, whose fields are used, does not use.
func checkUnusedFields(action EngineAction, used []string, refuse func(field, reason string)) {
	fields := []struct {
		name string
		set  bool
	}{
		{argNode, action.Node != ""},
		{argTask, action.Task != 0},
		{argClose, action.Close != ""},
		{argBy, action.By != ""},
		{argRequirement, action.Requirement != ""},
		{argItem, itemSet(action.Item)},
		{argUndoNone, len(action.UndoNone) > 0},
		{argDeliverable, action.Deliverable != ""},
		{argReviewer, action.Reviewer != ""},
	}
	for _, field := range fields {
		if field.set && !slices.Contains(used, field.name) {
			refuse(field.name, reasonUnused)
		}
	}
}

// itemSet reports whether item sets any field: a list with an element, a limit, or a text that is not empty.
func itemSet(item evidence.Item) bool {
	lists := len(item.Capture) + len(item.Artifacts) + len(item.Supplied) + len(item.Tests)
	return lists > 0 || item.Limit != 0 || item.Conclusion != "" || item.Judge != "" || item.Kind != ""
}

// fileReason returns the reason file is refused, or "" when it passes. It records file as named before it checks
// ownership, so the plan's own naming is counted whether or not the file is owned.
func fileReason(file string, taken, named map[string]bool) string {
	if reason := FileRefusal(file); reason != "" {
		return reason
	}
	if named[file] {
		return reasonFileTwice
	}
	named[file] = true
	if taken[file] {
		return reasonFileOwned
	}
	return ""
}

// FileRefusal returns why file cannot be a task's file on its own, or "" when it can: a path that is not a normalized
// repository-relative file path, or one inside a .git directory.
func FileRefusal(file string) string {
	if !fileFormOK(file) {
		return reasonFileForm
	}
	if inGitDir(file) {
		return reasonGitDir
	}
	return ""
}

// fileFormOK reports whether file is a normalized repository-relative file path: non-empty, no surrounding white
// space, no leading slash or backslash, already clean, and no . or .. component.
func fileFormOK(file string) bool {
	if file == "" || strings.TrimSpace(file) != file || strings.HasPrefix(file, "/") || strings.Contains(file, `\`) {
		return false
	}
	if path.Clean(file) != file {
		return false
	}
	for part := range strings.SplitSeq(file, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

// inGitDir reports whether file is a .git directory or lies inside one, at any depth and in any letter case. A task
// that owned a file there, such as a hook, could run code on later git operations.
func inGitDir(file string) bool {
	for part := range strings.SplitSeq(file, "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// stringSet returns the set of items.
func stringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
