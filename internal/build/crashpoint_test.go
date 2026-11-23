package build

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// crashEpic is the epic a crash run drives.
const crashEpic = "acme/epic"

// crashRounds is how many rounds a run takes, and crashEpoch and crashRoundSeconds set the fake clock: round r is at the
// epoch plus r rounds of crashRoundSeconds. Round 0 is when the epic is created.
const (
	crashRounds       = 12
	crashEpoch        = 1700001000
	crashRoundSeconds = 1800
)

// roundAt returns the fake clock's time at round.
func roundAt(round int) time.Time {
	return time.Unix(crashEpoch+int64(round)*crashRoundSeconds, 0)
}

// crashAddress matches the addresses %+v prints for pointers, which differ between runs.
var crashAddress = regexp.MustCompile(`0x[0-9a-f]+`)

// crashResult is what a run leaves behind: its end rows, the labels of its performed actions in order, and the outside
// logs: the starts of each step and run id, the resume prompts sent per prompt id, and the notices routed per id.
type crashResult struct {
	rows   string
	labels []string
	starts map[string]int
	sends  map[string]int
	routed map[string]int
}

// crashWindow is a span of the uninterrupted run between two named actions: it opens at the last action from before the
// first action to, and closes at that action to. A kill after any action in the span, from the action from through
// the action to, must restart to the uninterrupted rows.
type crashWindow struct {
	name, from, to string
}

// TestCrashPointRestartsToTheUninterruptedRows kills the epic run after each of its actions, restarts it from the store
// with fresh components, and checks each restart against the run that was never killed: the same end rows, each derived
// id started once, and each notice id routed once. Each kill point is a subtest, and each named window of the run is one
// more, which fails when a kill point inside it does not restart to the uninterrupted rows.
func TestCrashPointRestartsToTheUninterruptedRows(t *testing.T) {
	t.Parallel()
	want := crashUninterrupted(t)
	// The windows the brief names: a planner step's start and its turn record, a notice and its told marker, and a
	// resume prompt and its cleared limit. The run's one routed notice is the limit's. Its tell is a ref move that
	// carries no park question, so it routes no notice, and its window runs from its write to its told marker.
	windows := []crashWindow{
		{name: "planner step start to turn record", from: "planner step start", to: "SetTurnStep"},
		{name: "limit notice route to told marker", from: "notice route", to: "MarkTold"},
		{name: "ref move tell write to told marker", from: "SetTaskBuildTelling", to: "MarkTellTold"},
		{name: "resume prompt to cleared limit", from: "resume prompt", to: "resume clear"},
	}
	var mu sync.Mutex
	failed := make(map[int]bool)
	t.Run("kill points", func(t *testing.T) {
		for action := 1; action <= len(want.labels); action++ {
			t.Run(fmt.Sprintf("after action %03d %s", action, want.labels[action-1]), func(t *testing.T) {
				t.Parallel()
				ok := crashCompare(t, action, want)
				mu.Lock()
				defer mu.Unlock()
				failed[action] = !ok
			})
		}
	})
	for _, window := range windows {
		t.Run(window.name, func(t *testing.T) {
			t.Parallel()
			end := slices.Index(want.labels, window.to)
			if end < 0 {
				t.Fatalf("the uninterrupted run performs no %q action", window.to)
			}
			start := lastBefore(want.labels, window.from, end)
			if start < 0 {
				t.Fatalf("the uninterrupted run performs no %q action before %q", window.from, window.to)
			}
			mu.Lock()
			defer mu.Unlock()
			for action := start + 1; action <= end; action++ {
				if failed[action] {
					t.Errorf(
						"a kill after action %d, inside the window, did not restart to the uninterrupted run",
						action,
					)
				}
			}
		})
	}
}

// crashUninterrupted runs the epic through every round without a kill, checks that it did the work the run is about,
// and returns what it left behind.
func crashUninterrupted(t *testing.T) crashResult {
	t.Helper()
	w := newCrashWorld(t)
	if _, killed := w.incarnation(-1, 1); killed {
		t.Fatal("an uninterrupted run was killed")
	}
	got := w.result(t)
	epic, err := w.store.Epic(context.Background(), crashEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(epic.Turns) != 2 || epic.Turns[0].State != "free" || !epic.Turns[0].Limited ||
		epic.Turns[1].State != "succeeded" {
		t.Errorf("turns = %+v, want a limited free turn 1 then a succeeded turn 2", epic.Turns)
	}
	if len(epic.Tasks) != 1 || epic.Tasks[0].Status != "done" {
		t.Errorf("tasks = %+v, want one accepted task", epic.Tasks)
	}
	if len(got.starts) != 5 || len(got.sends) != 1 || len(got.routed) != 1 {
		t.Errorf("starts = %v, sends = %v, routed = %v, want 5 starts, 1 resume prompt and 1 notice",
			got.starts, got.sends, got.routed)
	}
	if refs := buildRun(t, w.repo, "for-each-ref", "refs/orrery"); refs == "" {
		t.Error("no refs/orrery pin after the accept")
	}
	return got
}

// crashCompare runs the epic killed after action, restarts it, and compares what it left behind with want. It reports
// each difference and returns false when there is one.
func crashCompare(t *testing.T, action int, want crashResult) bool {
	t.Helper()
	got := crashRunKilledAt(t, action)
	ok := true
	if got.rows != want.rows {
		t.Errorf("end rows after a kill after action %d differ from the uninterrupted run at %s",
			action, crashDiff(got.rows, want.rows))
		ok = false
	}
	if !slices.Equal(got.labels[:action], want.labels[:action]) {
		t.Errorf("the first %d actions differ: got %v, want %v", action, got.labels[:action], want.labels[:action])
		ok = false
	}
	for id, count := range got.starts {
		if count != 1 {
			t.Errorf("id %s was started %d times after a kill after action %d, want once", id, count, action)
			ok = false
		}
	}
	if !maps.Equal(got.starts, want.starts) || !maps.Equal(got.sends, want.sends) {
		t.Errorf("starts = %v, sends = %v after a kill after action %d, want %v and %v",
			got.starts, got.sends, action, want.starts, want.sends)
		ok = false
	}
	for id, count := range got.routed {
		if count != 1 {
			t.Errorf("notice %s was routed %d times after a kill after action %d, want once", id, count, action)
			ok = false
		}
	}
	if !maps.Equal(got.routed, want.routed) {
		t.Errorf("routed = %v after a kill after action %d, want %v", got.routed, action, want.routed)
		ok = false
	}
	return ok
}

// crashRunKilledAt runs the epic in a new world, killed after action, then restarts it from the round the kill stopped
// in, with fresh components over the same store.
func crashRunKilledAt(t *testing.T, action int) crashResult {
	t.Helper()
	w := newCrashWorld(t)
	round, _ := w.incarnation(action, 1)
	w.incarnation(-1, round)
	return w.result(t)
}

// crashTimerNever arms a router timer, its routing timers and its store-call bound alike, that never fires. The
// harness's clock moves by rounds and by nothing else, so no store call reads as late and the action order does not
// depend on the machine's speed. And a killed incarnation's router, which the harness leaves with no shutdown path,
// writes nothing after the kill, as a killed process cannot: its retry of the row writes the kill refused never runs.
func crashTimerNever(time.Duration, func()) func() bool {
	return func() bool { return true }
}

// newCrashWorld returns a world over a new store that holds the epic in its accepted phase, with its planner's first
// turn pending, in a new scratch repository.
func newCrashWorld(t *testing.T) *crashWorld {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"))
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err = s.CreateEpic(ctx, store.EpicRow{
		ID: crashEpic, Repository: setup.epic.Repository, Base: setup.base, Title: "Build the thing",
		Approved: "approved text", Intent: "build the thing", Care: "serious", Approach: "one planner turn",
		ReleaseThen: "release it", Checks: "just check", Phase: "accepted", Created: roundAt(0), Changed: roundAt(0),
		Gates: setup.epic.Gates,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the thing holds", Evidence: "crash harness", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return &crashWorld{
		t: t, store: s, dbPath: path, repo: setup.epic.Repository, base: setup.base, stateDir: setup.stateDir,
		getenv: setup.getenv, gate: &gate{limit: -1, cancel: func() {}},
		starts: make(map[string]int), sends: make(map[string]int), routed: make(map[string]int),
	}
}

// incarnation builds fresh components over the world's store, as a restarted executor does: the router attaches to the
// store's notice rows and the planner recovers its running turns. Then it runs rounds from round from to crashRounds.
// Each round runs in order: the due pass, the open steps finish, the planner, attention, and the build. It returns the
// round a refused action stopped it in and true, or crashRounds and false when it ran to the end. A refused action ends
// the incarnation at once with no shutdown path: the planner's turn goroutines and the router's decisions are waited on
// so none is left running, and nothing they would write is written.
func (w *crashWorld) incarnation(limit, from int) (int, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.setGate(&gate{limit: limit, cancel: cancel})
	w.setClock(from)
	kstore := crashStore{Store: w.store, w: w}
	router := route.New(route.Config{
		Host: "crash", Now: w.now, Desk: crashDesk{w: w}, Phone: crashPhone{w: w},
		Subscribed: func() (bool, error) { return true, nil }, Log: crashLog{w: w},
		Rebuild: map[string]route.Rebuild{planner.EpicSource: planner.RestoreNotice},
		// A store call that reads as late is written again, which adds actions; the harness's actions are counted, so
		// its store-call bound never fires. Nor does any routing timer, the retry of failed row writes among them.
		After:      crashTimerNever,
		StoreAfter: crashTimerNever,
	})
	router.Attach(ctx, kstore)
	router.Wait()
	w.drain()
	plan := planner.New(planner.Config{
		Store: kstore, Steps: crashPlanSteps{w: w}, Agent: crashAgent, Retry: time.Millisecond, Now: w.now,
		Resolve: crashResolver{},
	})
	plan.Recover(ctx)
	plan.WaitTurns()
	attention := planner.NewAttention(planner.AttentionConfig{
		Store: kstore, Router: crashRouter{w: w, router: router}, Now: w.now,
	})
	runner := New(Config{
		Store: kstore, Steps: crashBuildSteps{w: w}, Evidence: crashEvidence{w: w}, Getenv: w.getenv,
		StateDir: w.stateDir, Git: crashGit{w: w}, Now: w.now,
	})
	due := enginehost.NewDuePass(enginehost.DuePassConfig{Store: kstore, Steps: crashResume{w: w}, Now: w.now})
	for round := from; round <= crashRounds; round++ {
		w.setClock(round)
		stages := []func(){
			func() { due.Pass(ctx) },
			func() { w.check(w.finishOpen(ctx)) },
			func() { plan.Pass(ctx); plan.WaitTurns() },
			func() { attention.Pass(ctx) },
			func() { runner.pass(ctx) },
		}
		for _, stage := range stages {
			if w.killed() {
				break
			}
			stage()
		}
		router.Wait()
		if w.killed() {
			return round, true
		}
	}
	return crashRounds, false
}

// check reports err, an error a stage returned, unless the incarnation was killed, when every failure is the kill's.
func (w *crashWorld) check(err error) {
	if err != nil && !w.killed() {
		w.t.Errorf("crash run stage failed: %v", err)
	}
}

// result returns what the run left behind.
func (w *crashWorld) result(t *testing.T) crashResult {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	return crashResult{
		rows:   w.endRows(t),
		labels: slices.Clone(w.labels),
		starts: maps.Clone(w.starts),
		sends:  maps.Clone(w.sends),
		routed: maps.Clone(w.routed),
	}
}

// endRows returns the end rows of the run as text, with the paths and commit ids that differ between runs replaced: the
// epic with its turns and tasks, the due rows, the notice rows, the step rows, and the attention each epic's tells and
// turns hold. The epic's revision is left out: it counts the writes the run made, not what they left, and a restart
// makes again the writes a kill cut short.
func (w *crashWorld) endRows(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	epic, err := w.store.Epic(ctx, crashEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	epic.Revision = 0
	runs := make([]string, 0, len(epic.Tasks))
	for i := range epic.Tasks {
		runs = append(runs, fmt.Sprintf("T%d red=%s green=%s", epic.Tasks[i].Seq,
			runText(epic.Tasks[i].Red), runText(epic.Tasks[i].Green)))
		epic.Tasks[i].Red, epic.Tasks[i].Green = nil, nil
	}
	notices, err := w.store.Notices(ctx)
	if err != nil {
		t.Fatalf("Notices: %v", err)
	}
	steps, err := w.store.LabelledSteps(ctx, "", "")
	if err != nil {
		t.Fatalf("LabelledSteps: %v", err)
	}
	attention, err := w.store.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	text := fmt.Sprintf("epic=%+v\nruns=%v\ndue=%v\nnotices=%+v\nsteps=%+v\nattention=%+v",
		epic, runs, w.dueRows(t), notices, steps, attention)
	text = strings.NewReplacer(w.repo, "REPO", w.stateDir, "STATE", w.base, "BASE").Replace(text)
	return crashAddress.ReplaceAllString(text, "ADDR")
}

// lastBefore returns the index of the last action before end that is labeled label, or -1 when there is none.
func lastBefore(labels []string, label string, end int) int {
	for i := end - 1; i >= 0; i-- {
		if labels[i] == label {
			return i
		}
	}
	return -1
}

// crashDiff returns the first span where got and want differ, with its context of want, for a failure message.
func crashDiff(got, want string) string {
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	from := max(at-120, 0)
	return fmt.Sprintf("byte %d\n  got:  ...%s\n  want: ...%s", at, span(got, from, at+200), span(want, from, at+200))
}

// span returns text from from to to, clamped to its length.
func span(text string, from, to int) string {
	return text[min(from, len(text)):min(to, len(text))]
}

// runText returns the text of an evidence run a task holds, or "nil" when it holds none.
func runText(run *store.EvidenceRun) string {
	if run == nil {
		return "nil"
	}
	return fmt.Sprintf("%+v", *run)
}

// dueRows returns the due rows of the store, done or not, read from the store's file.
func (w *crashWorld) dueRows(t *testing.T) []string {
	t.Helper()
	db, err := sql.Open("sqlite3", w.dbPath)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	rows, err := db.QueryContext(context.Background(),
		"SELECT id, kind, target, resets, state FROM due_resumes ORDER BY id")
	if err != nil {
		t.Fatalf("list due rows: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close due rows: %v", err)
		}
	}()
	var out []string
	for rows.Next() {
		var id, kind, target, state string
		var resets int64
		if err := rows.Scan(&id, &kind, &target, &resets, &state); err != nil {
			t.Fatalf("scan due row: %v", err)
		}
		out = append(out, fmt.Sprintf("%s/%s/%s/%d/%s", id, kind, target, resets, state))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read due rows: %v", err)
	}
	return out
}
