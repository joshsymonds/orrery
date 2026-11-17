package build

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// OrchestratorRole is the role text of an orchestrator step: the read-only step that decides what happens to a task
// whose implementer's request orreryd could not settle by rule.
//
//go:embed orchestrator.md
var OrchestratorRole string

// The roles in the role file an orchestrator step runs under: orchestrator, or the planner's agent when the role file
// names no orchestrator.
const (
	roleOrchestrator = "orchestrator"
	rolePlanner      = "planner"
)

// decisionLimit is how many orchestrator decisions orreryd carries out on one task. A request past it is settled as it
// was before the orchestrator: the task fails, or its attempt is spent.
const decisionLimit = 3

// The decisions an orchestrator's return makes.
const (
	decisionRerun = "rerun"
	decisionGrant = "grant"
	decisionFail  = "fail"
	decisionAsk   = "ask"
)

// The end of an implementer step whose return failed a check a fourth time, and the check that fails a commit changing a
// path outside the task's files.
const (
	endChecksFailed = "checks_failed"
	checkOwned      = "owned"
)

// statusBlocked is the status of an orchestrator's return that reached no decision.
const statusBlocked = "blocked"

// taskDone is the status of an accepted task.
const taskDone = "done"

// catastrophesText names the classes of catastrophe an orchestrator's question to the owner names one of.
const catastrophesText = "irreversible-step, contract-cannot-hold, premise-false, cannot-proceed"

// isCatastrophe reports whether class is one of the classes catastrophesText names.
func isCatastrophe(class string) bool {
	switch class {
	case "irreversible-step", "contract-cannot-hold", "premise-false", "cannot-proceed":
		return true
	}
	return false
}

// The keys of an orchestrator's return that its decoder reads apart, and the field the shape rules name most.
const (
	keyAfterTasks = "after_tasks"
	keyNeedsFiles = "needs_files"
	fieldFiles    = "files"
)

// OrchestratorSchema is the schema an orchestrator step's return must match: one flat object, every field required and
// left empty when its decision does not use it.
const OrchestratorSchema = `{
  "title": "Implementer request decision",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "evidence", "decision", "note", "files", "after_tasks", "failure", "needs_files", "question",
    "reason"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "evidence": {"type": "string",
      "description": "What the request comes down to, each fact as file:line or the exact command and its output."},
    "decision": {"type": "string", "enum": ["", "rerun", "grant", "fail", "ask"]},
    "note": {"type": "string",
      "description": "Read by the next implementer run after the words: From your previous try on this task:"},
    "files": {"type": "array", "items": {"type": "string"}},
    "after_tasks": {"type": "array", "items": {"type": "integer"}},
    "failure": {"type": "string",
      "description": "The task's failure as the planner reads it; cause in the first sentence."},
    "needs_files": {"type": "array", "items": {"type": "string"}},
    "question": {
      "type": "object",
      "additionalProperties": false,
      "required": ["label", "text", "choices", "recommended", "why", "confidence", "catastrophe"],
      "properties": {
        "label": {"type": "string"},
        "text": {"type": "string"},
        "choices": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["id", "does"],
            "properties": {"id": {"type": "string"}, "does": {"type": "string"}}
          }
        },
        "recommended": {"type": "string"},
        "why": {"type": "string"},
        "confidence": {"type": "string", "enum": ["", "low", "medium", "high"]},
        "catastrophe": {"type": "string",
          "enum": ["", "irreversible-step", "contract-cannot-hold", "premise-false", "cannot-proceed"]}
      }
    },
    "reason": {"type": "string", "description": "Why this decision is right. Recorded as a decision of the epic."}
  }
}`

// orchestratorQuestion is the question an orchestrator's ask puts to the owner.
type orchestratorQuestion struct {
	Label       string             `json:"label"`
	Text        string             `json:"text"`
	Choices     []store.EpicChoice `json:"choices"`
	Recommended string             `json:"recommended"`
	Why         string             `json:"why"`
	Confidence  string             `json:"confidence"`
	Catastrophe string             `json:"catastrophe"`
}

// empty reports whether every field of the question is empty, as a decision other than ask leaves it.
func (q orchestratorQuestion) empty() bool {
	return q.Label == "" && q.Text == "" && len(q.Choices) == 0 && q.Recommended == "" && q.Why == "" &&
		q.Confidence == "" && q.Catastrophe == ""
}

// orchestratorReturn is an orchestrator step's return, as OrchestratorSchema gives it. AfterTasks and NeedsFiles are
// read apart from their keys, after_tasks and needs_files (decodeOrchestratorReturn).
type orchestratorReturn struct {
	Status     string               `json:"status"`
	Evidence   string               `json:"evidence"`
	Decision   string               `json:"decision"`
	Note       string               `json:"note"`
	Files      []string             `json:"files"`
	AfterTasks []int64              `json:"-"`
	Failure    string               `json:"failure"`
	NeedsFiles []string             `json:"-"`
	Question   orchestratorQuestion `json:"question"`
	Reason     string               `json:"reason"`
}

// decodeOrchestratorReturn decodes an orchestrator's return from raw JSON, with after_tasks and needs_files read from
// their keys when the return has them. A return that is not a JSON object of the schema's types is an error.
func decodeOrchestratorReturn(raw []byte) (orchestratorReturn, error) {
	var ret orchestratorReturn
	if err := json.Unmarshal(raw, &ret); err != nil {
		return orchestratorReturn{}, fmt.Errorf("orchestrator return: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return orchestratorReturn{}, fmt.Errorf("orchestrator return: %w", err)
	}
	if value, ok := fields[keyAfterTasks]; ok {
		if err := json.Unmarshal(value, &ret.AfterTasks); err != nil {
			return orchestratorReturn{}, fmt.Errorf("orchestrator return: %s: %w", keyAfterTasks, err)
		}
	}
	if value, ok := fields[keyNeedsFiles]; ok {
		if err := json.Unmarshal(value, &ret.NeedsFiles); err != nil {
			return orchestratorReturn{}, fmt.Errorf("orchestrator return: %s: %w", keyNeedsFiles, err)
		}
	}
	return ret, nil
}

// parseOrchestratorReturn decodes an orchestrator step's kept return, and reports false when it does not decode.
func parseOrchestratorReturn(result string) (orchestratorReturn, bool) {
	ret, err := decodeOrchestratorReturn([]byte(result))
	return ret, err == nil
}

// Integration is how far a task of the epic is from the commit later tasks build on.
type Integration int

// The integrations of a task. Pending is an open task, or an accepted one that no ended integrate batch has composed.
// Integrated is a task an ended batch composed. Lost is a failed task, or one an ended batch left out, so no batch will
// ever compose it.
const (
	Pending Integration = iota
	Integrated
	Lost
)

// Peer is one task of the epic as an implementer's request reads it: the task, its integration, and the paths its
// change touched from its base, read for an accepted task that is pending. Composed is the commit an ended batch made of
// an integrated task, and Why is why a lost task never integrates.
type Peer struct {
	Task     store.EpicTask
	State    Integration
	Changed  []string
	Composed string
	Why      string
}

// batchResult is the part of an integrate run's result a request reads: the tasks compose made into commits and the
// tasks it left out.
type batchResult struct {
	Composed []struct {
		Task   int64  `json:"task"`
		Commit string `json:"commit"`
	} `json:"composed"`
	LeftOut []struct {
		Task   int64  `json:"task"`
		Reason string `json:"reason"`
	} `json:"leftOut"`
}

// peersOf returns every task of epic as a Peer, in seq order, given the epic's node runs. An accepted task that is
// pending has the paths git names between its base and its revision as Changed.
func peersOf(ctx context.Context, epic store.EpicRow, runs []store.NodeRun) ([]Peer, error) {
	peers := make([]Peer, 0, len(epic.Tasks))
	for _, task := range epic.Tasks {
		peer := integrationOf(task, runs)
		if task.Status == taskDone && peer.State == Pending && task.Build.Revision != "" {
			changed, err := diffPaths(ctx, epic.Repository, task.Build.Base, task.Build.Revision)
			if err != nil {
				return nil, fmt.Errorf("changed paths of %s: %w", taskLabel(task.Seq), err)
			}
			peer.Changed = changed
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

// integrationOf returns task as a Peer with its integration and no changed paths. A failed task is lost. A task an
// ended integrate run claimed is integrated when the run composed it, and lost when the run left it out. Every other
// task is pending.
func integrationOf(task store.EpicTask, runs []store.NodeRun) Peer {
	peer := Peer{Task: task, State: Pending}
	if task.Status == taskFailed {
		peer.State, peer.Why = Lost, "failed"
		return peer
	}
	if task.Batch == "" {
		return peer
	}
	i := slices.IndexFunc(runs, func(run store.NodeRun) bool { return run.Path == task.Batch })
	if i < 0 || runs[i].State != store.NodeEnded {
		return peer
	}
	var result batchResult
	if err := json.Unmarshal([]byte(runs[i].Result), &result); err != nil {
		peer.State, peer.Why = Lost, "was claimed by a batch whose result does not decode"
		return peer
	}
	for _, composed := range result.Composed {
		if composed.Task == task.Seq {
			peer.State, peer.Composed = Integrated, composed.Commit
			return peer
		}
	}
	peer.State, peer.Why = Lost, "was left out of its batch"
	for _, left := range result.LeftOut {
		if left.Task == task.Seq {
			peer.Why += ": " + left.Reason
		}
	}
	return peer
}

// diffPaths returns the paths that differ between from and to in repo.
func diffPaths(ctx context.Context, repo, from, to string) ([]string, error) {
	out, err := runGit(ctx, repo, "diff", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// splitLines returns the non-empty lines of out.
func splitLines(out string) []string {
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// peerOf returns the peer with seq among peers, and false when there is none.
func peerOf(peers []Peer, seq int64) (Peer, bool) {
	i := slices.IndexFunc(peers, func(peer Peer) bool { return peer.Task.Seq == seq })
	if i < 0 {
		return Peer{}, false
	}
	return peers[i], true
}

// AfterSeqs returns the seqs of the tasks a build's After names, in order. A part that is not a number names no task.
func AfterSeqs(after string) []int64 {
	var seqs []int64
	for part := range strings.SplitSeq(after, ",") {
		if seq, err := strconv.ParseInt(part, 10, 64); err == nil {
			seqs = append(seqs, seq)
		}
	}
	return seqs
}

// afterText returns seqs as a build's After stores them: their decimals joined with commas.
func afterText(seqs []int64) string {
	parts := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		parts = append(parts, decimal(seq))
	}
	return strings.Join(parts, ",")
}

// Waiting reports whether task is open with its build held waiting on other tasks, which an integrate walk does not
// wait for.
func Waiting(task store.EpicTask) bool {
	return task.Status == taskOpen && task.Build.Held == HeldWaiting
}

// waitsOn reports whether the task with seq among peers waits now, directly or through the tasks it waits for, on
// target.
func waitsOn(peers []Peer, seq, target int64) bool {
	seen := map[int64]bool{}
	queue := []int64{seq}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		peer, ok := peerOf(peers, current)
		if !ok || !Waiting(peer.Task) {
			continue
		}
		for _, next := range AfterSeqs(peer.Task.Build.After) {
			if next == target {
				return true
			}
			queue = append(queue, next)
		}
	}
	return false
}

// holding is who holds a file a task asks for: Text says it as the Requested files section does, and Task is the seq of
// the task whose ownership or change keeps it from a grant, 0 for none. Refused is a file no task may own.
type holding struct {
	Text    string
	Task    int64
	Refused bool
}

// holderOf returns who holds file for task among peers. A file planner.FileRefusal refuses is refused. A file of the
// task is its own. A file an open task owns, or an accepted pending task changed, is that task's. Any other is free.
func holderOf(file string, task store.EpicTask, peers []Peer) holding {
	if reason := planner.FileRefusal(file); reason != "" {
		return holding{Text: "refused: " + reason, Refused: true}
	}
	if slices.Contains(task.Files, file) {
		return holding{Text: "already this task's"}
	}
	for _, peer := range peers {
		other := peer.Task
		if other.Seq == task.Seq {
			continue
		}
		switch {
		case other.Status == taskOpen && slices.Contains(other.Files, file):
			return holding{Text: fmt.Sprintf("owned by open %s (%s)", taskLabel(other.Seq), unitOf(other)),
				Task: other.Seq}
		case other.Status == taskDone && peer.State == Pending && slices.Contains(peer.Changed, file):
			return holding{Text: fmt.Sprintf("changed by accepted %s, not yet integrated", taskLabel(other.Seq)),
				Task: other.Seq}
		}
	}
	return holding{Text: "free"}
}

// unitOf names the unit an open task's build is on, with its hold when it has one, and `not started` before red.
func unitOf(task store.EpicTask) string {
	unit := task.Build.Stage
	if unit == "" {
		unit = "not started"
	}
	if task.Build.Held != "" {
		unit += ", " + task.Build.Held
	}
	return unit
}

// OrchestratorCheck returns the return check of an orchestrator step deciding a request of task, with peers the epic's
// tasks when the step started: the shape of the return, then, for a rerun or a grant, the state it names. A value that
// does not decode is refused with its decode error.
func OrchestratorCheck(task store.EpicTask, peers []Peer) func(json.RawMessage) []string {
	return func(value json.RawMessage) []string {
		ret, err := decodeOrchestratorReturn(value)
		if err != nil {
			return []string{err.Error()}
		}
		refusals := shapeRefusals(ret)
		if len(refusals) > 0 {
			return refusals
		}
		return stateRefusals(ret, task, peers)
	}
}

// shapeRefusals returns one `field: reason` line for each rule of its decision's shape that ret breaks. A blocked return
// names no decision and gives evidence. A done return names a decision and gives a reason. A rerun gives a note and no
// files; a grant gives a note and files. Both leave failure, needs_files and question empty. A fail gives a failure and
// leaves note, files, after_tasks and question empty. An ask gives every question field and leaves the rest empty.
func shapeRefusals(ret orchestratorReturn) []string {
	if ret.Status == statusBlocked {
		var refusals []string
		if ret.Decision != "" {
			refusals = append(refusals, "decision: not empty on a blocked return")
		}
		if strings.TrimSpace(ret.Evidence) == "" {
			refusals = append(refusals, "evidence: empty on a blocked return")
		}
		return refusals
	}
	var refusals []string
	if strings.TrimSpace(ret.Reason) == "" {
		refusals = append(refusals, "reason: empty")
	}
	switch ret.Decision {
	case decisionRerun, decisionGrant:
		refusals = append(refusals, rerunShape(ret)...)
	case decisionFail:
		refusals = append(refusals, nonEmpty("failure", ret.Failure)...)
		refusals = append(refusals, emptyFields(ret.Decision,
			field{"note", ret.Note != ""}, field{fieldFiles, len(ret.Files) > 0},
			field{keyAfterTasks, len(ret.AfterTasks) > 0}, field{"question", !ret.Question.empty()},
		)...)
	case decisionAsk:
		refusals = append(refusals, askShape(ret.Question)...)
		refusals = append(refusals, emptyFields(ret.Decision,
			field{"note", ret.Note != ""}, field{fieldFiles, len(ret.Files) > 0},
			field{keyAfterTasks, len(ret.AfterTasks) > 0}, field{"failure", ret.Failure != ""},
			field{keyNeedsFiles, len(ret.NeedsFiles) > 0},
		)...)
	default:
		refusals = append(refusals, "decision: empty on a done return")
	}
	return refusals
}

// rerunShape returns the shape refusals of a rerun or grant: a note, files only for a grant, and no failure,
// needs_files or question.
func rerunShape(ret orchestratorReturn) []string {
	refusals := nonEmpty("note", ret.Note)
	if ret.Decision == decisionGrant && len(ret.Files) == 0 {
		refusals = append(refusals, "files: empty on a grant")
	}
	return append(refusals, emptyFields(ret.Decision,
		field{fieldFiles, ret.Decision == decisionRerun && len(ret.Files) > 0}, field{"failure", ret.Failure != ""},
		field{keyNeedsFiles, len(ret.NeedsFiles) > 0}, field{"question", !ret.Question.empty()},
	)...)
}

// askShape returns the refusals of an ask's question: every field set, a catastrophe of the four, and the rules
// store.QuestionRefusals gives a planner's question.
func askShape(question orchestratorQuestion) []string {
	var refusals []string
	if !isCatastrophe(question.Catastrophe) {
		refusals = append(refusals, "question.catastrophe: not "+catastrophesText)
	}
	for _, line := range store.QuestionRefusals(store.EpicQuestionInput{
		Label: question.Label, Text: question.Text, Choices: question.Choices, Recommended: question.Recommended,
		Why: question.Why, Confidence: question.Confidence,
	}) {
		refusals = append(refusals, "question."+line)
	}
	return refusals
}

// nonEmpty returns the refusal of field when text is blank.
func nonEmpty(field, text string) []string {
	if strings.TrimSpace(text) == "" {
		return []string{field + ": empty"}
	}
	return nil
}

// field is one field of a return and whether it is set.
type field struct {
	name string
	set  bool
}

// emptyFields returns one refusal for each of fields that is set, in order: a field decision does not use.
func emptyFields(decision string, fields ...field) []string {
	var refusals []string
	for _, f := range fields {
		if f.set {
			refusals = append(refusals, fmt.Sprintf("%s: not empty on %s", f.name, decision))
		}
	}
	return refusals
}

// stateRefusals returns the refusals of a rerun or grant against the epic's tasks: an after_tasks entry that is this
// task, no task of the epic, a task that is neither open nor accepted and pending, a task that waits on this one, or a
// task named twice; and a granted file that planner.FileRefusal refuses, that is already the task's, that is named
// twice, or that an open task owns or an accepted pending task changed when after_tasks does not name that task. Any
// other decision has none.
func stateRefusals(ret orchestratorReturn, task store.EpicTask, peers []Peer) []string {
	if ret.Decision != decisionRerun && ret.Decision != decisionGrant {
		return nil
	}
	var refusals []string
	for i, seq := range ret.AfterTasks {
		if reason := afterRefusal(seq, task.Seq, peers, ret.AfterTasks[:i]); reason != "" {
			refusals = append(refusals, fmt.Sprintf("after_tasks[%d]: %s", i, reason))
		}
	}
	for i, file := range ret.Files {
		if reason := fileRefusal(file, task, peers, ret.AfterTasks, ret.Files[:i]); reason != "" {
			refusals = append(refusals, fmt.Sprintf("files[%d]: %s", i, reason))
		}
	}
	return refusals
}

// afterRefusal returns why task self may not wait for task seq, given the entries before it, or "" when it may.
func afterRefusal(seq, self int64, peers []Peer, before []int64) string {
	label := taskLabel(seq)
	peer, ok := peerOf(peers, seq)
	switch {
	case seq == self:
		return label + " is this task"
	case !ok:
		return label + " is not a task of this epic"
	case slices.Contains(before, seq):
		return label + " is named twice"
	case peer.State != Pending:
		return label + " is neither open nor accepted and waiting to be integrated"
	case waitsOn(peers, seq, self):
		return label + " waits on this task"
	}
	return ""
}

// fileRefusal returns why file may not be granted to task behind after, given the files before it, or "" when it may.
func fileRefusal(file string, task store.EpicTask, peers []Peer, after []int64, before []string) string {
	if slices.Contains(before, file) {
		return file + " is named twice"
	}
	held := holderOf(file, task, peers)
	switch {
	case held.Refused:
		return file + " is " + held.Text
	case held.Text == "already this task's":
		return file + " is already one of the task's files"
	case held.Task != 0 && !slices.Contains(after, held.Task):
		return fmt.Sprintf("%s is %s, which after_tasks does not name", file, held.Text)
	}
	return ""
}

// request returns the action for an implementer step that ended in a request orreryd cannot settle by rule. With
// orchestrator decisions left, the build moves to orchestrate, keeping the implementer's step as its step so the
// orchestrator decides that step's end. Without, fallback, what the build did before the orchestrator, is taken.
func request(view View, fallback Action) Action {
	build := view.Task.Build
	if build.Decisions >= decisionLimit {
		return fallback
	}
	build.Stage = StageOrchestrate
	build.Step = view.Step.ID
	build.NoVerdict = 0
	return Action{Kind: ActMove, Next: build}
}

// returnedFail returns the failure of an implementer step that ended returned with ret: `returned <status>: <summary>`,
// carrying the files a needs_context return names.
func returnedFail(step *store.StepRow, ret implementerReturn) Action {
	fail := Action{Kind: ActFail, Reason: fmt.Sprintf("returned %s: %s", step.Status, ret.Summary)}
	if step.Status == statusNeedsContext {
		fail.NeedsFiles = ret.NeedsFiles
	}
	return fail
}

// onlyOwnedFailed reports whether the last round of step's checks failed the owned check and no other.
func onlyOwnedFailed(step *store.StepRow) bool {
	last := -1
	for _, check := range step.Checks {
		last = max(last, check.Round)
	}
	var failed []string
	for _, check := range step.Checks {
		if check.Round == last && !check.OK {
			failed = append(failed, check.Name)
		}
	}
	return len(failed) == 1 && failed[0] == checkOwned
}

// requestFallback returns what the build does with the implementer step an orchestrate unit decides when no decision
// of the orchestrator is carried out: a returned end fails the task as returnedFail gives, and a checks_failed end
// spends the attempt. A step that is not read fails the task.
func requestFallback(view View) Action {
	step := view.Returned
	if step == nil {
		return Action{
			Kind:   ActFail,
			Reason: "orchestrate: the implementer's step " + view.Task.Build.Step + " is not stored",
		}
	}
	if step.End == endChecksFailed {
		return spend(view, endText(step))
	}
	ret, _ := parseImplementerReturn(step.Result)
	return returnedFail(step, ret)
}

// decideOrchestrate returns the action for the orchestrate unit. Without a step, its orchestrator starts under the
// derived id of the unit, which counts the decisions made, once the epic is running, unless the task has had
// decisionLimit decisions, when requestFallback settles it. A stopped step holds the build, and a step the engine ended
// on a restart reruns. A done return with status done is carried out (carry). Any other end, a blocked return, and a
// return that does not decode are settled by requestFallback.
func decideOrchestrate(view View) Action {
	build := view.Task.Build
	step := view.Step
	if step == nil {
		if build.Decisions >= decisionLimit {
			return requestFallback(view)
		}
		if view.Paused || view.Parked {
			return Action{Kind: ActNone}
		}
		return Action{
			Kind: ActStartStep, Unit: StageOrchestrate,
			ID: StepID(view.Epic, view.Task.Seq, StageOrchestrate, build.Attempt, build.Rerun, build.Decisions, 0),
		}
	}
	switch step.End {
	case "":
		return Action{Kind: ActNone}
	case endStopped:
		return holdStopped(build)
	case endEngineRestarted:
		return restartUnit(build)
	case endDone:
		if ret, ok := parseOrchestratorReturn(step.Result); ok && ret.Status == statusDone {
			return carry(view, ret, requestFallback(view))
		}
	}
	return requestFallback(view)
}

// carry returns the action that carries out ret, an orchestrator's done return, on the task of view. The state its
// rerun or grant names is checked again against the peers now, and a refusal settles the request as fallback does, with
// the refusal added to its reason. Every decision counts one more decision on the build and records an epic decision
// (decisionText). A rerun or grant moves the build to implement at its next rerun, with no step and the note as its
// failure, held waiting when it names tasks to wait for, and a grant adds its files. A fail fails the task with its
// failure and needs_files. An ask holds the build until the owner answers its question.
func carry(view View, ret orchestratorReturn, fallback Action) Action {
	if refusals := stateRefusals(ret, view.Task, view.Peers); len(refusals) > 0 {
		return refused(fallback, ret.Decision, refusals)
	}
	seq := view.Task.Seq
	next := view.Task.Build
	next.Decisions++
	decision := &store.TaskDecision{
		ID: fmt.Sprintf("%s-request-%d", taskLabel(seq), next.Decisions), Text: decisionText(seq, ret),
	}
	switch ret.Decision {
	case decisionFail:
		return Action{Kind: ActFail, Reason: ret.Failure, NeedsFiles: ret.NeedsFiles, Decision: decision}
	case decisionAsk:
		next.Held = HeldAsked
		question := ret.Question
		text := fmt.Sprintf("%s (%s): %s", taskLabel(seq), question.Catastrophe, question.Text)
		return Action{
			Kind: ActAsk, Next: next, Reason: text, Decision: decision, Fallback: &fallback,
			Question: &store.EpicQuestionInput{
				Label: question.Label, Text: text, Choices: question.Choices, Recommended: question.Recommended,
				Why: question.Why, Confidence: question.Confidence,
			},
		}
	}
	next.Stage = StageImplement
	next.Rerun++
	next.NoVerdict = 0
	next.Step = ""
	next.Failure = ret.Note
	if len(ret.AfterTasks) > 0 {
		next.Held = HeldWaiting
		next.After = afterText(ret.AfterTasks)
	}
	return Action{
		Kind: ActDecide, Next: next, NeedsFiles: ret.Files, Behind: ret.AfterTasks, Decision: decision,
		Fallback: &fallback,
	}
}

// refused returns fallback with the refusal of the orchestrator's decision added to what it records (withNote).
func refused(fallback Action, decision string, refusals []string) Action {
	return withNote(
		fallback,
		fmt.Sprintf("orreryd refused the orchestrator's %s: %s", decision, strings.Join(refusals, "; ")),
	)
}

// withNote returns fallback with text added to what it records: to the reason of a fail, and to the failure of a spent
// attempt.
func withNote(fallback Action, text string) Action {
	switch fallback.Kind {
	case ActFail:
		fallback.Reason += "; " + text
	case ActMove:
		fallback.Next.Failure += "; " + text
	default:
	}
	return fallback
}

// decisionText returns the epic decision that records ret on task seq: `T<seq> <decision>`, with the files of a grant,
// the catastrophe of an ask and the tasks a rerun or grant waits for, then `: ` and the reason.
func decisionText(seq int64, ret orchestratorReturn) string {
	head := taskLabel(seq) + " " + ret.Decision
	switch ret.Decision {
	case decisionGrant:
		head += " " + strings.Join(ret.Files, ", ")
	case decisionAsk:
		head += " (" + ret.Question.Catastrophe + ")"
	}
	if len(ret.AfterTasks) > 0 {
		labels := make([]string, 0, len(ret.AfterTasks))
		for _, after := range ret.AfterTasks {
			labels = append(labels, taskLabel(after))
		}
		head += " after " + strings.Join(labels, ", ")
	}
	return head + ": " + ret.Reason
}

// decideWait returns the action for a build held waiting. It waits while the epic is paused or parked, or while a task
// it waits for is pending. A task it waits for that is lost, or is not a task of the epic, fails it. Once each is
// integrated, the build resumes on NewBase: its worktree moves there, and the build is stored with that base and no
// hold. Its After stays, the record that it waited, which a late walk of the plan reads.
func decideWait(view View) Action {
	if view.Paused || view.Parked {
		return Action{Kind: ActNone}
	}
	build := view.Task.Build
	label := taskLabel(view.Task.Seq)
	for _, seq := range AfterSeqs(build.After) {
		peer, ok := peerOf(view.Peers, seq)
		switch {
		case !ok:
			return Action{Kind: ActFail, Reason: fmt.Sprintf(
				"%s waited on %s, which is not a task of the epic", label, taskLabel(seq))}
		case peer.State == Lost:
			return Action{
				Kind:   ActFail,
				Reason: fmt.Sprintf("%s waited on %s, which %s", label, taskLabel(seq), peer.Why),
			}
		case peer.State == Pending:
			return Action{Kind: ActNone}
		}
	}
	next := build
	next.Held = ""
	next.Base = view.NewBase
	return Action{Kind: ActResume, Next: next, Revision: view.NewBase}
}

// decideAsked returns the action for a build held on its question to the owner: once the question is no longer open,
// the hold ends and the orchestrate unit runs again under its next decision count.
func decideAsked(view View) Action {
	if !view.Answered {
		return Action{Kind: ActNone}
	}
	next := view.Task.Build
	next.Held = ""
	next.Question = 0
	return Action{Kind: ActMove, Next: next}
}
