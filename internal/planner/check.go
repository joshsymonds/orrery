package planner

import (
	"fmt"
	"path"
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
// question order, then one per broken rule of each action, in action order, or nil when the return passes. Each task
// refusal names the task by index and title, then the field, then the reason. Each question refusal names the question by
// index and label, then the field and reason that store.QuestionRefusals gives. Each action refusal names the action by
// index and kind, then the field and the reason. requirements are the epic's Requirement ids, and owned are the files owned
// by the epic's unfinished tasks. gate and judge resolve a task's gate name and judge name, and a name they refuse gives
// the refusal its error text. Check does not test whether an action's target is valid; the engine does that.
func Check(ret Return, requirements []string, owned []string, gate, judge func(name string) error) []string {
	known := stringSet(requirements)
	taken := stringSet(owned)
	named := make(map[string]bool)
	refusals := make([]string, 0, len(ret.Tasks)+len(ret.Questions)+len(ret.Actions))
	for i, task := range ret.Tasks {
		refusals = append(refusals, checkTask(i, task, known, taken, named, gate, judge)...)
	}
	for i, question := range ret.Questions {
		refusals = append(refusals, checkQuestion(i, question)...)
	}
	for i, action := range ret.Actions {
		refusals = append(refusals, checkAction(i, action)...)
	}
	if len(refusals) == 0 {
		return nil
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
	for _, line := range evidence.Check(task.Evidence) {
		refusals = append(refusals, prefix+" evidence."+line)
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

// actionKinds returns the kinds an action may name, in the order the refusal lists them.
func actionKinds() []string {
	return []string{
		store.ActionRetryNode, store.ActionRetryEpic, store.ActionFailTask, store.ActionResumeRelease,
		store.ActionCloseEpic,
	}
}

// actionFields returns the fields kind uses, by the names the return gives them, and whether kind is one of actionKinds.
// resume_release uses undo_none, so the shape check passes a named position on to the engine, which refuses it: only the
// owner names an undo-none step.
func actionFields(kind string) ([]string, bool) {
	switch kind {
	case store.ActionRetryNode:
		return []string{"node"}, true
	case store.ActionRetryEpic:
		return nil, true
	case store.ActionFailTask:
		return []string{"task"}, true
	case store.ActionResumeRelease:
		return []string{"undo_none"}, true
	case store.ActionCloseEpic:
		return []string{"close", "by"}, true
	}
	return nil, false
}

// checkAction returns the refusals of one action of a return, at position i, in the order of the rules: the kind, the
// fields its kind needs, the fields its kind does not use, then the reason. An unknown kind has no other field to check.
func checkAction(i int, action EngineAction) []string {
	prefix := fmt.Sprintf(`actions[%d] "%s"`, i, action.Kind)
	var refusals []string
	refuse := func(field, reason string) {
		refusals = append(refusals, prefix+" "+field+": "+reason)
	}
	used, known := actionFields(action.Kind)
	if known {
		checkNeededFields(action, refuse)
		checkUnusedFields(action, used, refuse)
	} else {
		refuse("kind", "not one of "+strings.Join(actionKinds(), ", "))
	}
	if strings.TrimSpace(action.Reason) == "" {
		refuse("reason", reasonEmpty)
	}
	return refusals
}

// checkNeededFields calls refuse for each field action's kind needs that is missing or wrong: a retry_node names a node,
// a fail_task names a task above 0, and a close_epic names a close of superseded or abandoned and, when superseded, a by.
func checkNeededFields(action EngineAction, refuse func(field, reason string)) {
	switch action.Kind {
	case store.ActionRetryNode:
		if strings.TrimSpace(action.Node) == "" {
			refuse("node", reasonEmpty)
		}
	case store.ActionFailTask:
		if action.Task <= 0 {
			refuse("task", reasonAboveZero)
		}
	case store.ActionCloseEpic:
		if action.Close != closeSuperseded && action.Close != closeAbandoned {
			refuse("close", reasonNotClose)
		}
		if action.Close == closeSuperseded && strings.TrimSpace(action.By) == "" {
			refuse("by", reasonEmpty)
		}
	}
}

// checkUnusedFields calls refuse for each field action sets that its kind, whose fields are used, does not use.
func checkUnusedFields(action EngineAction, used []string, refuse func(field, reason string)) {
	fields := []struct {
		name string
		set  bool
	}{
		{"node", action.Node != ""},
		{"task", action.Task != 0},
		{"close", action.Close != ""},
		{"by", action.By != ""},
		{"undo_none", len(action.UndoNone) > 0},
	}
	for _, field := range fields {
		if field.set && !slices.Contains(used, field.name) {
			refuse(field.name, reasonUnused)
		}
	}
}

// fileReason returns the reason file is refused, or "" when it passes. It records file as named before it checks
// ownership, so the plan's own naming is counted whether or not the file is owned.
func fileReason(file string, taken, named map[string]bool) string {
	if !fileFormOK(file) {
		return reasonFileForm
	}
	if inGitDir(file) {
		return reasonGitDir
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
