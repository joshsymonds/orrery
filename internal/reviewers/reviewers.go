// Package reviewers declares the reviewers an epic runs. Layers of entries (orrery's defaults, the project file, the
// epic's contract) merge by name into the reviewers an epic runs. An entry that cannot hold is refused with an Error
// naming its file and key.
package reviewers

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"
)

// Stages a reviewer runs at: each task, and the epic once its tasks are done.
const (
	StageTask = "task"
	StageEpic = "epic"
)

// Reviewer is one reviewer after the layers merge. Limit is in seconds. An agent reviewer names Agent; a model
// reviewer names Model with the Tools and Effort it runs under.
type Reviewer struct {
	Name    string
	Enabled bool
	Stage   []string
	Must    []string
	MustNot []string
	Prompt  string
	Agent   string
	Model   string
	Tools   []string
	Effort  string
	Limit   int64
	Verify  bool
}

// Entry is one reviewer as one layer declares it. Source names the layer's file, or the layer itself ("epic_create",
// "orrery defaults"); Index is the entry's position in its layer. A nil pointer or nil slice is an unset key; a
// non-nil slice, empty included, is set.
type Entry struct {
	Source  string
	Index   int
	Name    string
	Enabled *bool
	Stage   []string
	Must    []string
	MustNot []string
	Prompt  *string
	Agent   *string
	Model   *string
	Tools   []string
	Effort  *string
	Limit   *int64
	Verify  *bool
}

// Error refuses one entry, or the reviewer the layers leave incomplete. Source and Index name the entry that last set
// the reviewer; Key names the key at fault.
type Error struct {
	Source string
	Index  int
	Name   string
	Key    string
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: reviewers[%d] %s: %s: %s", e.Source, e.Index, e.Name, e.Key, e.Reason)
}

// namePattern is the shape of a reviewer name, the same shape gates and pools take.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxNameBytes bounds a name so the inline agent name built from it stays within the agent name limit.
const maxNameBytes = 48

// defaultLimit is the run limit, in seconds, of a reviewer no layer sets a limit for.
const defaultLimit = 1800

// maxLimitSeconds is the largest limit whose duration fits in a time.Duration. It mirrors the bound internal/gates
// checks when it turns a limit into a duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

// pending is one reviewer under merge, with the entry that last set it: the entry an Error names.
type pending struct {
	r      Reviewer
	source string
	index  int
}

// Merge applies the layers in order and returns the reviewers they declare, in the order their names first appear.
// It refuses the first entry a layer cannot hold, and then the first enabled reviewer the layers leave incomplete.
func Merge(layers ...[]Entry) ([]Reviewer, error) {
	var order []*pending
	byName := make(map[string]*pending)
	for _, layer := range layers {
		seen := make(map[string]bool, len(layer))
		for _, entry := range layer {
			if err := checkEntry(entry); err != nil {
				return nil, err
			}
			if seen[entry.Name] {
				return nil, refuse(entry, "name", "appears twice in one layer")
			}
			seen[entry.Name] = true
			pend, ok := byName[entry.Name]
			if !ok {
				pend = &pending{r: Reviewer{Name: entry.Name, Enabled: true, Limit: defaultLimit, Verify: true}}
				byName[entry.Name] = pend
				order = append(order, pend)
			}
			pend.apply(entry)
		}
	}
	out := make([]Reviewer, 0, len(order))
	for _, pend := range order {
		if key, reason := incomplete(pend.r); key != "" {
			return nil, &Error{Source: pend.source, Index: pend.index, Name: pend.r.Name, Key: key, Reason: reason}
		}
		out = append(out, pend.r)
	}
	return out, nil
}

// apply sets the keys entry sets on the reviewer and records entry as the one that last set it.
func (pend *pending) apply(entry Entry) {
	pend.source, pend.index = entry.Source, entry.Index
	rev := &pend.r
	if entry.Enabled != nil {
		rev.Enabled = *entry.Enabled
	}
	if entry.Stage != nil {
		rev.Stage = entry.Stage
	}
	if entry.Must != nil {
		rev.Must = entry.Must
	}
	if entry.MustNot != nil {
		rev.MustNot = entry.MustNot
	}
	if entry.Prompt != nil {
		rev.Prompt = *entry.Prompt
	}
	if entry.Limit != nil {
		rev.Limit = *entry.Limit
	}
	if entry.Verify != nil {
		rev.Verify = *entry.Verify
	}
	// Agent clears the model, tools and effort, and each of those clears the agent. checkKeys keeps one entry from
	// setting agent beside them, so the order of these blocks within an entry does not matter.
	if entry.Agent != nil {
		rev.Agent, rev.Model, rev.Tools, rev.Effort = *entry.Agent, "", nil, ""
	}
	if entry.Model != nil {
		rev.Model, rev.Agent = *entry.Model, ""
	}
	if entry.Tools != nil {
		rev.Tools, rev.Agent = entry.Tools, ""
	}
	if entry.Effort != nil {
		rev.Effort, rev.Agent = *entry.Effort, ""
	}
}

// checkEntry refuses an entry no layer can hold, whatever the other entries say.
func checkEntry(entry Entry) *Error {
	if err := checkName(entry); err != nil {
		return err
	}
	if err := checkStage(entry); err != nil {
		return err
	}
	if err := checkLists(entry); err != nil {
		return err
	}
	return checkKeys(entry)
}

// checkName refuses a name outside the pattern or over the byte bound.
func checkName(entry Entry) *Error {
	if !namePattern.MatchString(entry.Name) {
		return refuse(entry, "name", "must match [a-z][a-z0-9_]*")
	}
	if len(entry.Name) > maxNameBytes {
		return refuse(entry, "name", fmt.Sprintf("is over %d bytes", maxNameBytes))
	}
	return nil
}

// checkStage refuses a set stage that is empty, names an unknown stage, or repeats one.
func checkStage(entry Entry) *Error {
	if entry.Stage == nil {
		return nil
	}
	if len(entry.Stage) == 0 {
		return refuse(entry, "stage", "is empty")
	}
	seen := make(map[string]bool, len(entry.Stage))
	for _, stage := range entry.Stage {
		if stage != StageTask && stage != StageEpic {
			return refuse(entry, "stage", fmt.Sprintf("element %q must be task or epic", stage))
		}
		if seen[stage] {
			return refuse(entry, "stage", fmt.Sprintf("repeats %q", stage))
		}
		seen[stage] = true
	}
	return nil
}

// checkLists refuses an empty element in must, must_not or tools.
func checkLists(entry Entry) *Error {
	if slices.Contains(entry.Must, "") {
		return refuse(entry, "must", "has an empty element")
	}
	if slices.Contains(entry.MustNot, "") {
		return refuse(entry, "must_not", "has an empty element")
	}
	if slices.Contains(entry.Tools, "") {
		return refuse(entry, "tools", "has an empty element")
	}
	return nil
}

// checkKeys refuses a scalar key no layer can hold: agent beside model, tools or effort; an empty agent, model or
// prompt; an effort off the list; a limit outside 1 to maxLimitSeconds seconds.
func checkKeys(entry Entry) *Error {
	if entry.Agent != nil {
		switch {
		case entry.Model != nil:
			return refuse(entry, "model", "cannot sit beside agent")
		case entry.Tools != nil:
			return refuse(entry, "tools", "cannot sit beside agent")
		case entry.Effort != nil:
			return refuse(entry, "effort", "cannot sit beside agent")
		case *entry.Agent == "":
			return refuse(entry, "agent", "is empty")
		}
	}
	if entry.Model != nil && *entry.Model == "" {
		return refuse(entry, "model", "is empty")
	}
	if entry.Prompt != nil && *entry.Prompt == "" {
		return refuse(entry, "prompt", "is empty")
	}
	if entry.Effort != nil && !validEffort(*entry.Effort) {
		return refuse(entry, "effort", fmt.Sprintf("%q is not low, medium, high, xhigh or max", *entry.Effort))
	}
	if entry.Limit != nil {
		switch {
		case *entry.Limit < 1:
			return refuse(entry, "limit", "must be at least 1 second")
		case *entry.Limit > maxLimitSeconds:
			return refuse(entry, "limit", fmt.Sprintf("must be at most %d seconds", maxLimitSeconds))
		}
	}
	return nil
}

// validEffort reports whether effort is a level a reviewer may run at.
func validEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

// incomplete names the key an enabled reviewer lacks, and why, or returns "" when the reviewer is complete or disabled.
func incomplete(rev Reviewer) (key, reason string) {
	switch {
	case !rev.Enabled:
		return "", ""
	case len(rev.Stage) == 0:
		return "stage", "needs a stage"
	case rev.Agent == "" && rev.Model == "":
		return "agent", "needs an agent or a model"
	case rev.Model != "" && rev.Prompt == "":
		return "prompt", "is required when model is set"
	case len(rev.Must) == 0 && len(rev.MustNot) == 0:
		return "must", "needs a must or must_not"
	}
	return "", ""
}

// refuse names the entry and the key at fault.
func refuse(entry Entry, key, reason string) *Error {
	return &Error{Source: entry.Source, Index: entry.Index, Name: entry.Name, Key: key, Reason: reason}
}

// ForStage returns the enabled reviewers whose Stage holds stage, in the order of rs.
func ForStage(rs []Reviewer, stage string) []Reviewer {
	var out []Reviewer
	for _, rev := range rs {
		if rev.Enabled && slices.Contains(rev.Stage, stage) {
			out = append(out, rev)
		}
	}
	return out
}
