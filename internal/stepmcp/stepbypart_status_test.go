package stepmcp

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepByPartStatusToolIsStateOnly checks that step_status answers a step's state only: the object has exactly the
// state keys, its reason is the first line cut to 200 runes, each check is its round, name and ok, and no result,
// workspace, revision, worktree, check detail, check output or log reaches the caller.
func TestStepByPartStatusToolIsStateOnly(t *testing.T) {
	t.Parallel()
	row := store.StepRow{
		ID:        "s1",
		Agent:     "reviewer",
		Workspace: "/invented/workspace",
		Revision:  "invented-revision",
		ReadOnly:  true,
		Worktree:  "/invented/worktree",
		LoopStage: "checks",
		Doing:     "invented-doing",
		Epic:      "invented-epic",
		Task:      "invented-task",
		Checks: []store.StepCheck{{
			Round: 1, Name: "gate", OK: false,
			Detail: "invented-detail", Stdout: "invented-stdout", Stderr: "invented-stderr",
			Log: "/invented/gate.log",
		}},
		Result: `{"summary":"invented-result"}`,
		Status: "fail",
		End:    "checks_failed",
		// The first line is 250 runes of two-byte letters, so a cut by bytes would not match the cut by runes.
		Reason:  strings.Repeat("é", 250) + "\ninvented-reason-tail",
		Started: time.Unix(1700000000, 0),
		Ended:   time.Unix(1700000600, 0),
	}
	fake := &fakeSteps{row: row}
	p := newPeer(t, startDaemon(t, fake), nil)

	outcome := p.tool("step_status", `{"id":"s1"}`)
	if outcome.isError {
		t.Fatalf("step_status reported an error: %s", outcome.text)
	}
	want := map[string]any{
		"id":          "s1",
		"agent":       "reviewer",
		"end":         "checks_failed",
		"status":      "fail",
		"stage":       "checks",
		"doing":       "invented-doing",
		"epic":        "invented-epic",
		"task":        "invented-task",
		"started":     float64(1700000000),
		"ended":       float64(1700000600),
		"limited":     false,
		"limitKind":   "",
		"limitResets": float64(0),
		"reason":      strings.Repeat("é", 199) + "…",
		"checks": []any{
			map[string]any{"round": float64(1), "name": "gate", "ok": false},
		},
	}
	got := decodeObject(t, outcome.text)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("step_status = %v, want %v", got, want)
	}
	t.Logf("step_status keys = [%s]", strings.Join(slices.Sorted(maps.Keys(got)), " "))
	// The DeepEqual above holds, so reason is a string and checks is a list of objects.
	reason, _ := got["reason"].(string)
	t.Logf("step_status reason is the first line cut to %d runes (199 + …)", utf8.RuneCountInString(reason))
	checks, _ := got["checks"].([]any)
	for _, item := range checks {
		check, _ := item.(map[string]any)
		t.Logf("step_status check = {round:%v name:%v ok:%v}, no detail, stdout, stderr or log",
			check["round"], check["name"], check["ok"])
	}
	for _, leaked := range []string{
		"invented-result", "invented-workspace", "invented-revision", "invented-worktree",
		"invented-detail", "invented-stdout", "invented-stderr", "/invented/gate.log", "invented-reason-tail",
	} {
		if strings.Contains(outcome.text, leaked) {
			t.Errorf("step_status carries %q, want the state only: %s", leaked, outcome.text)
		} else {
			t.Logf("step_status carries no %q", leaked)
		}
	}
}

// TestStepByPartStartResultNamesNext checks that step_start's result is the started step's id and a next that says the
// step runs in the background and that its end arrives as an orrery-event of kind step_end.
func TestStepByPartStartResultNamesNext(t *testing.T) {
	t.Parallel()
	fake := &fakeSteps{startID: "0123456789abcdef0123456789abcdef"}
	p := newPeer(t, startDaemon(t, fake), nil)

	outcome := p.tool("step_start", `{"prompt":"check the change","workspace":"/work"}`)
	if outcome.isError {
		t.Fatalf("step_start reported an error: %s", outcome.text)
	}
	got := decodeObject(t, outcome.text)
	next, _ := got["next"].(string)
	if got["id"] != fake.startID {
		t.Errorf("step_start id = %v, want %s", got["id"], fake.startID)
	} else {
		t.Logf("step_start id = %v; next = %s", got["id"], next)
	}
	for _, want := range []string{"orrery-event", "step_end"} {
		if !strings.Contains(next, want) {
			t.Errorf("step_start next = %q, want it to name %s", next, want)
		} else {
			t.Logf("step_start next names %s", want)
		}
	}
}
