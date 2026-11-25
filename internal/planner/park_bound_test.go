package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// boundReturn is a planner return that waits, with questions and actions, each a list of JSON objects joined by commas.
func boundReturn(questions, actions string) string {
	return `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[` + questions + `],"actions":[` +
		actions + `],"outcome":"wait"}`
}

// The actions and the question the park bound tests return.
const (
	recursAction = `{"kind":"park_recurs","args":"","reason":"the same check fails the same way"}`
	newAction    = `{"kind":"park_new","args":"","reason":"a different check fails now"}`
	retryAction  = `{"kind":"retry_epic","args":"","reason":"the check runs again"}`
	ownerAsk     = `{"label":"owner-call","text":"which way?","choices":[{"id":"a","does":"do a","closes":""},` +
		`{"id":"b","does":"do b","closes":""}],"recommended":"a","why":"a is safer","catastrophe":"cannot-proceed",` +
		`"confidence":"low","replaces":""}`
)

// TestParkBoundCheckCall checks the return check's rule for a call on a park. At the bound, a return makes one call:
// park_recurs alone, or park_new first with an action after it; a return with only a question passes; and any other
// return is refused with a line that says what is missing. Off the bound, a call is refused and a return without one is
// checked as before.
func TestParkBoundCheckCall(t *testing.T) {
	t.Parallel()
	const noCall = "actions: the park is at the bound of its chain and waits on your call; return park_recurs as the " +
		"only action, or park_new first and then the actions that answer the park"
	parked := store.EpicRow{Parked: "workflow node check failed: exit 1", ParkClass: store.ParkClassNode}
	cases := []struct {
		name  string
		bound bool
		ret   string
		want  []string
	}{
		{name: "recurs alone", bound: true, ret: boundReturn("", recursAction)},
		{name: "new then an action", bound: true, ret: boundReturn("", newAction+","+retryAction)},
		{name: "only a question", bound: true, ret: boundReturn(ownerAsk, "")},
		{name: "no call", bound: true, ret: boundReturn("", retryAction), want: []string{noCall}},
		{name: "nothing", bound: true, ret: boundReturn("", ""), want: []string{noCall}},
		{
			name: "recurs with an action", bound: true, ret: boundReturn("", recursAction+","+retryAction),
			want: []string{"actions: park_recurs sends the park to the owner, so it is the only action of the return"},
		},
		{
			name:  "new alone",
			bound: true,
			ret:   boundReturn("", newAction),
			want: []string{
				"actions: park_new answers nothing on its own; return the actions that answer the park after it",
			},
		},
		{
			name: "new after an action", bound: true, ret: boundReturn("", retryAction+","+newAction),
			want: []string{`actions[1] "park_new": the call comes first, before the actions that answer the park`},
		},
		{
			name:  "two calls",
			bound: true,
			ret:   boundReturn("", newAction+","+recursAction),
			want: []string{
				`actions[1] "park_recurs": a second call on the park; return one of park_recurs and park_new`,
			},
		},
		{
			name: "call off the bound", ret: boundReturn("", newAction+","+retryAction),
			want: []string{`actions[0] "park_new": the epic holds no park at the bound of its chain; only such a ` +
				`park is called`},
		},
		{name: "action off the bound", ret: boundReturn("", retryAction)},
		{
			name: "nothing off the bound", ret: boundReturn("", ""),
			want: []string{"actions: the epic is parked (workflow node check failed: exit 1); return an action for " +
				"the park or a catastrophe question"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := returnCheck(context.Background(), parked, store.Review{}, testResolver{}, nil, tc.bound)
			if got := check(json.RawMessage(tc.ret)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("returnCheck() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParkBoundPromptShowsTheChain checks that the prompt of a turn whose epic holds a park at the bound of its chain has
// the Park bound section between Triggers and Spent lines, with the park, its chain's earlier parks, the actions on it
// and what changed since its previous park, and that a turn with no such park has no Park bound section.
func TestParkBoundPromptShowsTheChain(t *testing.T) {
	t.Parallel()
	chain := store.BoundChain{
		Seq: 3, Park: "release step 1 failed: blocked",
		Earlier: []string{"merge of origin's commit c1 conflicts", "release step 1 failed: blocked"},
		Actions: []string{"resume release: the restart cut the step short (taken)"},
		Changes: []string{"stored base: unchanged at b1", "release steps passed since: none"},
	}
	prompt, err := Prompt(syntheticEpic(), 2, syntheticTriggers(), nil, nil, chain)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	const section = "\n## Park bound\n" +
		"Park: release step 1 failed: blocked\n" +
		"Earlier parks on this chain:\n" +
		"- merge of origin's commit c1 conflicts\n" +
		"- release step 1 failed: blocked\n" +
		"Planner actions on this chain:\n" +
		"- resume release: the restart cut the step short (taken)\n" +
		"Changed since the chain's previous park:\n" +
		"- stored base: unchanged at b1\n" +
		"- release steps passed since: none\n"
	inOrder(t, prompt, "## Triggers\n", section, "## Spent lines\n")

	plain, err := Prompt(syntheticEpic(), 2, syntheticTriggers(), nil, nil, store.BoundChain{})
	if err != nil {
		t.Fatalf("Prompt with no bound chain: %v", err)
	}
	if strings.Contains(plain, "## Park bound") {
		t.Errorf("prompt with no park at the bound has a Park bound section:\n%s", plain)
	}
}

// boundSteps is a Steps whose steps end as done as soon as they start, each with result.
type boundSteps struct {
	*fakeSteps
	result string
}

// Start starts the step on the embedded fakeSteps and ends it as done with result.
func (s boundSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	id, err := s.fakeSteps.Start(ctx, start)
	if err != nil {
		return "", err
	}
	s.end(id, store.StepRow{End: "done", Result: s.result})
	return id, nil
}

// boundParkReason is the reason of the node park n the runner park bound test stores, counted from 1.
func boundParkReason(n int) string {
	return "workflow node check failed: run " + string(rune('0'+n))
}

// parkRunnerEpicAndRetry parks the runner epic as node park n, then claims a turn, completes it with a retry_epic and
// carries the retry out, so the park's chain holds one more taken action.
func parkRunnerEpicAndRetry(t *testing.T, s *store.Store, n int) {
	t.Helper()
	ctx := context.Background()
	at := claimedAt().Add(time.Duration(n) * time.Minute)
	if err := s.ParkEpic(ctx, runnerEpicID, store.ParkClassNode, boundParkReason(n), at); err != nil {
		t.Fatalf("ParkEpic %d: %v", n, err)
	}
	turn, err := s.ClaimTurn(ctx, runnerEpicID, at)
	if err != nil {
		t.Fatalf("ClaimTurn after park %d: %v", n, err)
	}
	plan := store.EpicPlan{
		Actions: []store.EpicActionInput{{Kind: store.ActionRetryEpic, Reason: "the check runs again"}},
	}
	if err = s.CompleteTurn(ctx, runnerEpicID, turn.Seq, plan, at); err != nil {
		t.Fatalf("CompleteTurn after park %d: %v", n, err)
	}
	actions, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range actions {
		if _, err = s.CarryOutAction(ctx, action, at); err != nil {
			t.Fatalf("CarryOutAction: %v", err)
		}
	}
}

// TestParkBoundRunnerTurnCallsTheParkAtTheBound checks a planner turn on an epic whose third node park came to a chain
// holding two taken retries: the turn's prompt shows the chain in its Park bound section, its return check refuses a
// return that makes no call, and the turn's park_new and retry_epic, carried out by the next pass, end the park.
func TestParkBoundRunnerTurnCallsTheParkAtTheBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	parkRunnerEpicAndRetry(t, s, 1)
	parkRunnerEpicAndRetry(t, s, 2)
	if err := s.ParkEpic(ctx, runnerEpicID, store.ParkClassNode, boundParkReason(3),
		claimedAt().Add(3*time.Minute)); err != nil {
		t.Fatalf("ParkEpic 3: %v", err)
	}
	steps := boundSteps{fakeSteps: newFakeSteps(), result: boundReturn("", newAction+","+retryAction)}
	var reads atomic.Int64
	runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	runner.Pass(ctx)
	runner.WaitTurns()
	starts := steps.started()
	if len(starts) != 1 {
		t.Fatalf("steps started = %d, want 1", len(starts))
	}
	inOrder(t, starts[0].Prompt,
		"## Park bound\nPark: "+boundParkReason(3)+"\n",
		"Earlier parks on this chain:\n- "+boundParkReason(1)+"\n- "+boundParkReason(2)+"\n",
		"Planner actions on this chain:\n- retry epic: the check runs again (taken)\n"+
			"- retry epic: the check runs again (taken)\n",
		"Changed since the chain's previous park:\n",
		"## Spent lines\n",
	)
	if got := starts[0].ReturnCheck(json.RawMessage(boundReturn("", retryAction))); len(got) != 1 ||
		!strings.HasPrefix(got[0], "actions: the park is at the bound of its chain") {
		t.Errorf("return check of a return with no call = %q, want the no-call refusal", got)
	}

	runner.Pass(ctx)
	runner.WaitTurns()
	if epic := epicOf(t, s); epic.Parked != "" {
		t.Errorf("park after the new call and its retry = %q %q, want none", epic.ParkClass, epic.Parked)
	}
}
