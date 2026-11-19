package build

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
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
// was before the orchestrator: the task climbs a rung off the request's failure.
const decisionLimit = 3

// The decisions an orchestrator's return makes. A cannot_meet decision confirms that the task's spec cannot be met as
// written, and climbs the task straight to the recarve, as carry gives it; a fail climbs the task off its failure. A
// re_brief decision reruns the task's rung with a version that may add and remove files, once per failure, and spends
// no attempt.
const (
	decisionRerun      = "rerun"
	decisionGrant      = "grant"
	decisionReBrief    = "re_brief"
	decisionCannotMeet = "cannot_meet"
	decisionFail       = "fail"
	decisionAsk        = "ask"
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

// The keys of an orchestrator's return that its decoder reads apart: after_tasks and needs_files, and test_changes
// inside version, which the version's JSON tags cannot name.
const (
	keyAfterTasks  = "after_tasks"
	keyNeedsFiles  = "needs_files"
	keyVersion     = "version"
	keyTestChanges = "test_changes"
)

// OrchestratorSchema is the schema an orchestrator step's return must match: one flat object, every field required and
// left empty when its decision does not use it. A rerun, grant or re_brief decision returns version, the task's new spec
// version, which every agent on the task reads as standing spec: a version may change any field of the task's spec but
// never the contract.
const OrchestratorSchema = `{
  "title": "Implementer request decision",
  "description": "A rerun, grant or re_brief decision returns version, the task's new spec version, which every agent on the task reads, written as standing spec. A version may change any field of the task's spec (deliverable, files, test_changes, gate, evidence; an evidence change is red-checked at the task's base) but never the contract. files adds the files it names to the task and remove drops the files it names from the task.",
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "evidence", "decision", "version", "after_tasks", "failure", "needs_files", "question",
    "reason"],
  "properties": {
    "status": {"type": "string", "enum": ["done", "blocked"]},
    "evidence": {"type": "string",
      "description": "What the request comes down to, each fact as file:line or the exact command and its output."},
    "decision": {"type": "string", "enum": ["", "rerun", "grant", "re_brief", "cannot_meet", "fail", "ask"]},
    "version": {
      "type": "object",
      "additionalProperties": false,
      "required": ["changed", "deliverable", "files", "remove", "test_changes", "gate", "evidence"],
      "properties": {
        "changed": {"type": "array",
          "items": {"type": "string", "enum": ["deliverable", "files", "test_changes", "gate", "evidence"]}},
        "deliverable": {"type": "string"},
        "files": {"type": "array", "items": {"type": "string"}},
        "remove": {"type": "array", "items": {"type": "string"}},
        "test_changes": {"type": "array", "items": {"type": "string"}},
        "gate": {"type": "string"},
        "evidence": {
          "type": "object",
          "properties": {
            "capture": {"type": "array", "items": {"type": "string"}},
            "limit": {"type": "integer"},
            "artifacts": {"type": "array", "items": {"type": "string"}},
            "supplied": {"type": "array", "items": {"type": "string"}},
            "conclusion": {"type": "string"},
            "judge": {"type": "string"},
            "kind": {"type": "string"},
            "tests": {"type": "array", "items": {"type": "string"}}
          },
          "required": ["capture", "limit", "artifacts", "supplied", "conclusion", "judge", "kind", "tests"],
          "additionalProperties": false
        }
      }
    },
    "after_tasks": {"type": "array", "items": {"type": "integer"}},
    "failure": {"type": "string",
      "description": "The task's failure as the planner reads it, for fail and cannot_meet; cause in the first sentence."},
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

// orchestratorVersion is the version an orchestrator's rerun, grant or re_brief returns: the fields it changes, and the
// value of each. Changed names the fields, Files are the files the version adds to the task, Remove the files it drops
// from the task, and TestChanges is read apart from its key, test_changes (decodeOrchestratorReturn).
type orchestratorVersion struct {
	Changed     []string      `json:"changed"`
	Deliverable string        `json:"deliverable"`
	Files       []string      `json:"files"`
	Remove      []string      `json:"remove"`
	TestChanges []string      `json:"-"`
	Gate        string        `json:"gate"`
	Evidence    evidence.Item `json:"evidence"`
}

// empty reports whether every field of the version is zero, as a fail or an ask leaves it. A list is zero when it has
// no entries.
func (v orchestratorVersion) empty() bool {
	return len(v.Changed) == 0 && v.Deliverable == "" && len(v.Files) == 0 && len(v.Remove) == 0 &&
		len(v.TestChanges) == 0 && v.Gate == "" && itemEmpty(v.Evidence)
}

// itemEmpty reports whether every field of item is zero, as a version that does not change its evidence leaves it.
func itemEmpty(item evidence.Item) bool {
	return len(item.Capture) == 0 && item.Limit == 0 && len(item.Artifacts) == 0 && len(item.Supplied) == 0 &&
		item.Conclusion == "" && item.Judge == "" && item.Kind == "" && len(item.Tests) == 0
}

// orchestratorReturn is an orchestrator step's return, as OrchestratorSchema gives it. AfterTasks and NeedsFiles are
// read apart from their keys, after_tasks and needs_files, and Version.TestChanges from its key
// (decodeOrchestratorReturn).
type orchestratorReturn struct {
	Status     string               `json:"status"`
	Evidence   string               `json:"evidence"`
	Decision   string               `json:"decision"`
	Version    orchestratorVersion  `json:"version"`
	AfterTasks []int64              `json:"-"`
	Failure    string               `json:"failure"`
	NeedsFiles []string             `json:"-"`
	Question   orchestratorQuestion `json:"question"`
	Reason     string               `json:"reason"`
}

// decodeOrchestratorReturn decodes an orchestrator's return from raw JSON, with after_tasks and needs_files read from
// their keys when the return has them, and version's test_changes from its key. A return that is not a JSON object of
// the schema's types is an error.
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
	if value, ok := fields[keyVersion]; ok {
		if err := decodeVersionTestChanges(value, &ret.Version); err != nil {
			return orchestratorReturn{}, err
		}
	}
	return ret, nil
}

// decodeVersionTestChanges reads the test_changes key of raw, an orchestrator's version, into version.
func decodeVersionTestChanges(raw json.RawMessage, version *orchestratorVersion) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return fmt.Errorf("orchestrator return: %s: %w", keyVersion, err)
	}
	if value, ok := keys[keyTestChanges]; ok {
		if err := json.Unmarshal(value, &version.TestChanges); err != nil {
			return fmt.Errorf("orchestrator return: %s.%s: %w", keyVersion, keyTestChanges, err)
		}
	}
	return nil
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

// integrationOf returns task as a Peer with its integration and no changed paths. A failed task is lost, and so is a
// task that ended as a gap. A task an ended integrate run claimed is integrated when the run composed it, and lost when
// the run left it out. Every other task is pending.
func integrationOf(task store.EpicTask, runs []store.NodeRun) Peer {
	peer := Peer{Task: task, State: Pending}
	if task.Status == taskFailed {
		peer.State, peer.Why = Lost, "failed"
		return peer
	}
	if task.Status == store.TaskGap {
		peer.State, peer.Why = Lost, "ended as a gap"
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

// holding is who holds a file a task asks for: Text says it as the Requested files section does, and Tasks are the seqs
// of the tasks whose ownership or change keeps it from a grant, none for a free file. Own is a file of the task, and
// Refused a file no task may own.
type holding struct {
	Text    string
	Tasks   []int64
	Own     bool
	Refused bool
}

// holderOf returns who holds file for task among peers. A file planner.FileRefusal refuses is refused. A file of the
// task is its own. A file that open tasks own, or accepted pending tasks changed, is held by each of them, as when a
// task waiting behind another was granted the other's file. Any other is free.
func holderOf(file string, task store.EpicTask, peers []Peer) holding {
	if reason := planner.FileRefusal(file); reason != "" {
		return holding{Text: "refused: " + reason, Refused: true}
	}
	if slices.Contains(task.Files, file) {
		return holding{Text: "already this task's", Own: true}
	}
	var held holding
	var texts []string
	for _, peer := range peers {
		other := peer.Task
		if other.Seq == task.Seq {
			continue
		}
		switch {
		case other.Status == taskOpen && slices.Contains(other.Files, file):
			texts = append(texts, fmt.Sprintf("owned by open %s (%s)", taskLabel(other.Seq), unitOf(other)))
		case other.Status == taskDone && peer.State == Pending && slices.Contains(peer.Changed, file):
			texts = append(texts, fmt.Sprintf("changed by accepted %s, not yet integrated", taskLabel(other.Seq)))
		default:
			continue
		}
		held.Tasks = append(held.Tasks, other.Seq)
	}
	if len(texts) == 0 {
		return holding{Text: "free"}
	}
	held.Text = strings.Join(texts, " and ")
	return held
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
		return stateRefusals(ret, task, peers, false)
	}
}

// shapeRefusals returns one `field: reason` line for each rule of its decision's shape that ret breaks. A blocked
// return names no decision and gives evidence. A done return names a decision and gives a reason. A rerun, grant or
// re_brief gives a version under versionShape and leaves failure, needs_files and question empty. A fail or a
// cannot_meet gives a failure and leaves version, after_tasks and question empty, with needs_files allowed. An ask gives
// every question field and leaves the rest empty.
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
	case decisionRerun, decisionGrant, decisionReBrief:
		refusals = append(refusals, rerunShape(ret)...)
	case decisionFail, decisionCannotMeet:
		refusals = append(refusals, nonEmpty("failure", ret.Failure)...)
		refusals = append(refusals, emptyFields(ret.Decision,
			field{keyVersion, !ret.Version.empty()}, field{keyAfterTasks, len(ret.AfterTasks) > 0},
			field{"question", !ret.Question.empty()},
		)...)
	case decisionAsk:
		refusals = append(refusals, askShape(ret.Question)...)
		refusals = append(refusals, emptyFields(ret.Decision,
			field{keyVersion, !ret.Version.empty()}, field{keyAfterTasks, len(ret.AfterTasks) > 0},
			field{"failure", ret.Failure != ""}, field{keyNeedsFiles, len(ret.NeedsFiles) > 0},
		)...)
	default:
		refusals = append(refusals, "decision: empty on a done return")
	}
	return refusals
}

// rerunShape returns the shape refusals of a rerun, grant or re_brief: its version (versionShape), and no failure,
// needs_files or question.
func rerunShape(ret orchestratorReturn) []string {
	return append(versionShape(ret.Decision, ret.Version), emptyFields(ret.Decision,
		field{"failure", ret.Failure != ""}, field{keyNeedsFiles, len(ret.NeedsFiles) > 0},
		field{"question", !ret.Question.empty()},
	)...)
}

// versionShape returns the shape refusals of the version of a rerun, grant or re_brief. Each field changed names once. A
// field changed does not name is empty, and remove is empty unless changed names files: each file it removes is named
// once, and is not one the version adds. The fields a version names are checked by namedShape.
func versionShape(decision string, version orchestratorVersion) []string {
	var refusals []string
	for i, name := range version.Changed {
		if slices.Index(version.Changed, name) < i {
			refusals = append(refusals, fmt.Sprintf("version.changed[%d]: %s is named twice", i, name))
		}
	}
	for _, item := range []field{
		{"deliverable", version.Deliverable != ""}, {"files", len(version.Files) > 0},
		{"test_changes", len(version.TestChanges) > 0}, {"gate", version.Gate != ""},
		{"evidence", !itemEmpty(version.Evidence)},
	} {
		if item.set && !slices.Contains(version.Changed, item.name) {
			refusals = append(refusals, fmt.Sprintf("version.%s: not empty when changed does not name it", item.name))
		}
	}
	if len(version.Remove) > 0 && !slices.Contains(version.Changed, "files") {
		refusals = append(refusals, "version.remove: not empty when changed does not name files")
	}
	for i, file := range version.Remove {
		switch {
		case slices.Index(version.Remove, file) < i:
			refusals = append(refusals, fmt.Sprintf("version.remove[%d]: %s is named twice", i, file))
		case slices.Contains(version.Files, file):
			refusals = append(refusals, fmt.Sprintf("version.remove[%d]: %s is also in version.files", i, file))
		}
	}
	return append(refusals, namedShape(decision, version)...)
}

// namedShape returns the refusals of the fields a version names: a blank deliverable or gate, a grant that names no
// file or names none to add, a grant that names evidence, a rerun that names files, and a named evidence item
// (evidenceShape).
func namedShape(decision string, version orchestratorVersion) []string {
	var refusals []string
	if slices.Contains(version.Changed, "deliverable") {
		refusals = append(refusals, nonEmpty("version.deliverable", version.Deliverable)...)
	}
	if slices.Contains(version.Changed, "gate") {
		refusals = append(refusals, nonEmpty("version.gate", version.Gate)...)
	}
	if slices.Contains(version.Changed, "evidence") {
		refusals = append(refusals, evidenceShape(version.Evidence)...)
		if decision == decisionGrant {
			refusals = append(refusals, "version.evidence: not changed on a grant")
		}
	}
	switch {
	case decision == decisionGrant && !slices.Contains(version.Changed, "files"):
		refusals = append(refusals, "version.changed: files not named on a grant")
	case decision == decisionGrant && len(version.Files) == 0:
		refusals = append(refusals, "version.files: empty on a grant")
	case decision == decisionGrant && len(version.Remove) > 0:
		refusals = append(refusals, "version.remove: not empty on a grant")
	case decision == decisionRerun && slices.Contains(version.Changed, "files"):
		refusals = append(refusals, "version.changed: files named on a rerun")
	}
	return refusals
}

// evidenceShape returns the refusals of an evidence item a version names: each line evidence.CheckTask gives it, under
// version.evidence, and a closure kind, which only a task's own evidence in a correction round may have.
func evidenceShape(item evidence.Item) []string {
	var refusals []string
	for _, line := range evidence.CheckTask(item) {
		refusals = append(refusals, "version.evidence."+line)
	}
	if item.Kind == evidence.KindClosure {
		refusals = append(refusals, "version.evidence.kind: closure is not a kind a version sets")
	}
	return refusals
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

// stateRefusals returns the refusals of a rerun, grant or re_brief against the epic's tasks: an after_tasks entry that is
// this task, no task of the epic, a task that is neither open nor accepted and pending, a task that waits on this one, or
// a task named twice; a file of version.files that planner.FileRefusal refuses, that is already the task's, that is named
// twice, or that an open task owns or an accepted pending task changed when after_tasks does not name that task; a file
// of version.remove that is not one of the task's; and a test change that is none of the files the task has once the
// version removes and adds its files. Any other decision has none. With integrated set, an after_tasks entry a batch has
// integrated since the step started is not refused, since the wait for it is already over. An ask is refused as the
// task's last decision, since the orchestrator that would read the answer could not run.
func stateRefusals(ret orchestratorReturn, task store.EpicTask, peers []Peer, integrated bool) []string {
	if ret.Decision == decisionAsk && task.Build.Decisions+1 >= decisionLimit {
		return []string{
			"decision: an ask is the task's last decision, and no decision would be left to act on the answer",
		}
	}
	if !slices.Contains([]string{decisionRerun, decisionGrant, decisionReBrief}, ret.Decision) {
		return nil
	}
	var refusals []string
	for i, seq := range ret.AfterTasks {
		if reason := afterRefusal(seq, task.Seq, peers, ret.AfterTasks[:i], integrated); reason != "" {
			refusals = append(refusals, fmt.Sprintf("after_tasks[%d]: %s", i, reason))
		}
	}
	for i, file := range ret.Version.Remove {
		if !slices.Contains(task.Files, file) {
			refusals = append(refusals, fmt.Sprintf("version.remove[%d]: not one of the task's files", i))
		}
	}
	files := ret.Version.Files
	for i, file := range files {
		if reason := fileRefusal(file, task, peers, ret.AfterTasks, files[:i]); reason != "" {
			refusals = append(refusals, fmt.Sprintf("version.files[%d]: %s", i, reason))
		}
	}
	kept := filesLeft(task, ret.Version.Remove)
	if len(kept)+len(files) == 0 {
		refusals = append(refusals, "version.remove: the task would be left no files")
	}
	whole := slices.Concat(kept, files)
	for i, file := range ret.Version.TestChanges {
		if !slices.Contains(whole, file) {
			refusals = append(refusals, fmt.Sprintf("version.test_changes[%d]: not one of the task's files", i))
		}
	}
	return refusals
}

// afterRefusal returns why task self may not wait for task seq, given the entries before it, or "" when it may. With
// integrated set, an integrated task may be waited for.
func afterRefusal(seq, self int64, peers []Peer, before []int64, integrated bool) string {
	label := taskLabel(seq)
	peer, ok := peerOf(peers, seq)
	switch {
	case seq == self:
		return label + " is this task"
	case !ok:
		return label + " is not a task of this epic"
	case slices.Contains(before, seq):
		return label + " is named twice"
	case peer.State == Integrated && integrated:
		return ""
	case peer.State != Pending:
		return label + " is neither open nor accepted and waiting to be integrated"
	case waitsOn(peers, seq, self):
		return label + " waits on this task"
	}
	return ""
}

// filesLeft returns the files task owns that remain once the files in remove are dropped, in order.
func filesLeft(task store.EpicTask, remove []string) []string {
	return slices.DeleteFunc(slices.Clone(task.Files), func(file string) bool { return slices.Contains(remove, file) })
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
	case held.Own:
		return file + " is already one of the task's files"
	}
	for _, seq := range held.Tasks {
		if !slices.Contains(after, seq) {
			return fmt.Sprintf("%s is %s, and after_tasks does not name %s", file, held.Text, taskLabel(seq))
		}
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
// of the orchestrator is carried out: a returned end climbs the task as returnedFail gives, and a checks_failed end
// spends the attempt. A step that is not read climbs the task.
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
// rerun, grant or re_brief names is checked again against the peers now, and a refusal settles the request as fallback
// does, with the refusal added to its reason. Every decision counts one more decision on the build and records an epic
// decision (decisionText). A rerun, grant or re_brief moves the build to implement at its next rerun, with no step and
// the failure fallbackText gives, which on a needs_context request also says what the version granted (grantedText),
// held waiting when it names tasks to wait for, and stores the version it returns (versionInput). A rerun, grant or
// fail of a needs_context request refuses the files it named that the task does not own and the version does not add
// (requestRefusals), and the decision records them. A rerun that changes the evidence moves the build to red at that
// rerun instead, so the new evidence is judged at the base before the implementer runs again, and keeps its attempt. A
// fail climbs the task off its failure and needs_files, moved by a fail decision. A cannot_meet climbs it the same way,
// moved by a cannot_meet decision, to the recarve when the task is below it and one rung up otherwise. An ask holds the
// build until the owner answers its question. A re_brief spends no attempt, as a rerun does, and records the first line
// of the failure it answers, so a re_brief on a first line the task already answered is refused.
func carry(view View, ret orchestratorReturn, fallback Action) Action {
	if refusals := stateRefusals(ret, view.Task, view.Peers, true); len(refusals) > 0 {
		return refused(fallback, ret.Decision, refusals)
	}
	line := rebriefLine(fallbackText(fallback))
	if ret.Decision == decisionReBrief && view.Task.Build.ReBriefed(line) {
		return refused(fallback, ret.Decision, []string{"already re-briefed on this failure"})
	}
	seq := view.Task.Seq
	next := view.Task.Build
	next.Decisions++
	decision := &store.TaskDecision{
		ID: fmt.Sprintf("%s-request-%d", taskLabel(seq), next.Decisions), Text: decisionText(seq, ret),
	}
	switch ret.Decision {
	case decisionFail:
		decision.Refused = requestRefusals(view, fallback, nil)
		return Action{
			Kind: ActFail, Reason: ret.Failure, NeedsFiles: ret.NeedsFiles, Decision: decision,
			Move: store.RungMove{MovedBy: store.MovedByFailDecision},
		}
	case decisionCannotMeet:
		var to int64
		if store.RungOf(view.Task.Build) < store.ApproachRung {
			to = store.ApproachRung
		}
		return Action{
			Kind: ActFail, Reason: ret.Failure, NeedsFiles: ret.NeedsFiles, Decision: decision,
			Move: store.RungMove{MovedBy: store.MovedByCannotMeet, To: to},
		}
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
	if slices.Contains(ret.Version.Changed, "evidence") {
		next.Stage = StageRed
	}
	next.Rerun++
	next.NoVerdict = 0
	next.Step = ""
	next.Failure = fallbackText(fallback)
	if ret.Decision == decisionReBrief {
		next = next.WithReBrief(line)
	}
	if requestsContext(view) {
		next.Failure += "; " + grantedText(ret.Version.Files)
	}
	decision.Refused = requestRefusals(view, fallback, ret.Version.Files)
	if len(ret.AfterTasks) > 0 {
		next.Held = HeldWaiting
		next.After = afterText(ret.AfterTasks)
	}
	return Action{
		Kind: ActDecide, Next: next, Behind: ret.AfterTasks, Decision: decision, Version: versionInput(view, ret),
		Fallback: &fallback,
	}
}

// fallbackText returns the failure orreryd gives the implementer's step a decision answers: the failure fallback moves
// the build to when it moves it, and the reason it climbs the task with otherwise.
func fallbackText(fallback Action) string {
	if fallback.Kind == ActMove {
		return fallback.Next.Failure
	}
	return fallback.Reason
}

// rebriefLine returns the first line of failure, the failure a re_brief answers, as TaskBuild.ReBriefed reads it.
func rebriefLine(failure string) string {
	line, _, _ := strings.Cut(failure, "\n")
	return line
}

// requestsContext reports whether the implementer step an orchestrate unit decides ended returned with needs_context,
// so the files it names are the files its request asked for.
func requestsContext(view View) bool {
	return view.Returned != nil && view.Returned.End == endReturned && view.Returned.Status == statusNeedsContext
}

// grantedText returns what the implementer of a needs_context request is told of an orchestrator's rerun or grant: the
// files its version granted, or that none were. It names no other file.
func grantedText(granted []string) string {
	if len(granted) == 0 {
		return "the orchestrator granted none"
	}
	return "the orchestrator granted " + strings.Join(granted, ", ")
}

// requestRefusals returns the files a rerun, grant or fail of a needs_context request refuses: the files fallback's
// request named that the task of view does not own and the decision's version, added, does not add. A request that is
// not needs_context refuses none.
func requestRefusals(view View, fallback Action, added []string) []string {
	if !requestsContext(view) {
		return nil
	}
	var refused []string
	for _, file := range fallback.NeedsFiles {
		if slices.Contains(view.Task.Files, file) || slices.Contains(added, file) || slices.Contains(refused, file) {
			continue
		}
		refused = append(refused, file)
	}
	return refused
}

// versionInput returns the version ret stores on the task of view: the step the orchestrator ran, ret's reason and
// files, and each field ret's changed list names, set to its value.
func versionInput(view View, ret orchestratorReturn) store.TaskVersionInput {
	version := ret.Version
	in := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: view.Step.ID, Reason: ret.Reason, Files: version.Files,
		Remove: version.Remove,
	}
	if slices.Contains(version.Changed, "deliverable") {
		in.Deliverable = &version.Deliverable
	}
	if slices.Contains(version.Changed, "test_changes") {
		in.TestChanges = &version.TestChanges
	}
	if slices.Contains(version.Changed, "gate") {
		in.Gate = &version.Gate
	}
	if slices.Contains(version.Changed, "evidence") {
		in.Evidence = &version.Evidence
	}
	return in
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

// decisionText returns the epic decision that records ret on task seq: `T<seq> <decision>`, with the files a grant's
// version adds, the catastrophe of an ask and the tasks a rerun or grant waits for, then `: ` and the reason.
func decisionText(seq int64, ret orchestratorReturn) string {
	head := taskLabel(seq) + " " + ret.Decision
	switch ret.Decision {
	case decisionGrant:
		head += " " + strings.Join(ret.Version.Files, ", ")
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
// it waits for is pending. A task it waits for that is lost, or is not a task of the epic, climbs it. Once each is
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
