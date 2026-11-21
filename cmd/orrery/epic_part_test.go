package main

import (
	"errors"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// epicPartForm is the form a usage error of `orrery epic get` names.
const epicPartForm = "get ID [--part PART] [--task T<n>] [--question Q<n>] [--decision ID] [--turn N] [--offset N] " +
	"[--limit N]"

// newEpicPartFake returns a recordingEpics whose epic has two of each item a part reads, each named by its text, so
// that a read's output names the item or page it returned.
func newEpicPartFake() *recordingEpics {
	fake := newRecordingEpics()
	fake.epic.Tasks = []*v1.EpicTask{
		{Id: "T1", Status: "todo", Title: "Ship login"},
		{Id: "T2", Status: "todo", Title: "Write tests"},
	}
	fake.epic.Questions = []*v1.EpicQuestion{
		{Id: "Q1", Text: "Retry the parked build?"},
		{Id: "Q2", Text: "Keep the session cookie?"},
	}
	fake.epic.Decisions = []*v1.EpicDecision{
		{Id: "DL1", Seq: 1, Text: "use OIDC"},
		{Id: "DL2", Seq: 2, Text: "use passkeys"},
	}
	fake.epic.Turns = []*v1.EpicTurn{
		{Seq: 1, Outline: "plan the login"},
		{Seq: 2, Outline: "write the tests"},
	}
	return fake
}

// TestEpicPartFlagsReadThroughEpicGet checks that each flag of `orrery epic get` reaches epic_get as its argument: the
// part, item and page it names are the ones read, and the output shows the read and nothing it leaves out.
func TestEpicPartFlagsReadThroughEpicGet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		want    []string
		notWant []string
	}{
		{
			name:    "task",
			args:    []string{"get", epicTestID, "--part", "task", "--task", "T2"},
			want:    []string{"Write tests"},
			notWant: []string{"Ship login"},
		},
		{
			name:    "question",
			args:    []string{"get", epicTestID, "--part", "question", "--question", "Q2"},
			want:    []string{"Keep the session cookie?"},
			notWant: []string{"Retry the parked build?"},
		},
		{
			name:    "decision",
			args:    []string{"get", epicTestID, "--part", "decision", "--decision", "DL2"},
			want:    []string{"use passkeys"},
			notWant: []string{"use OIDC"},
		},
		{
			name:    "turn",
			args:    []string{"get", epicTestID, "--part", "turn", "--turn", "2"},
			want:    []string{"write the tests"},
			notWant: []string{"plan the login"},
		},
		{
			name:    "offset",
			args:    []string{"get", epicTestID, "--part", "tasks", "--offset", "1"},
			want:    []string{"tasks: 1 of 2 from offset 1", "Write tests"},
			notWant: []string{"Ship login"},
		},
		{
			name:    "limit",
			args:    []string{"get", epicTestID, "--part", "tasks", "--limit", "1"},
			want:    []string{"tasks: 1 of 2 from offset 0", "Ship login", "offset 1 continues"},
			notWant: []string{"Write tests"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newEpicPartFake()
			stdout, stderr, err := runEpicCommand(t, fake, nil, tc.args...)
			if err != nil {
				t.Fatalf("epicCommand(%v) = %v, want nil", tc.args, err)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			for _, text := range tc.want {
				if !strings.Contains(stdout, text) {
					t.Errorf("stdout has no %q:\n%s", text, stdout)
				}
			}
			for _, text := range tc.notWant {
				if strings.Contains(stdout, text) {
					t.Errorf("stdout has %q, want only the part the flags name:\n%s", text, stdout)
				}
			}
		})
	}
}

// TestEpicPartGetAloneSendsOnlyTheID checks that `get ID` with no flag reads the epic's summary: epic_get reads the
// epic the id names, and nothing else reaches it, so the summary is printed and no refusal is given.
func TestEpicPartGetAloneSendsOnlyTheID(t *testing.T) {
	t.Parallel()
	fake := newEpicPartFake()

	stdout, stderr, err := runEpicCommand(t, fake, nil, "get", epicTestID)
	if err != nil {
		t.Fatalf("epicCommand(get) = %v, want nil", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	requests := fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	req, ok := requests[0].(*v1.GetEpicRequest)
	if !ok {
		t.Fatalf("request is %T, want *v1.GetEpicRequest", requests[0])
	}
	if got := req.GetId(); got != epicTestID {
		t.Fatalf("request id = %q, want %q", got, epicTestID)
	}
	if want := "epic " + epicTestID + ": Login\n"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout does not start with the summary %q:\n%s", want, stdout)
	}
}

// TestEpicPartRefusesABadForm checks that a form of `orrery epic get` naming no ID, naming a flag as the ID, leaving a
// positional argument, or giving an --offset or --limit that is not an integer is a usage error naming the form, and
// calls no tool.
func TestEpicPartRefusesABadForm(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"get", "--part", "tasks"},
		{"get", "--part", "tasks", epicTestID},
		{"get", epicTestID, "extra"},
		{"get", epicTestID, "--part", "tasks", "extra"},
		{"get", epicTestID, "--part"},
		{"get", epicTestID, "--bogus", "x"},
		{"get", epicTestID, "--offset", "two"},
		{"get", epicTestID, "--offset", "1.5"},
		{"get", epicTestID, "--offset"},
		{"get", epicTestID, "--part", "tasks", "--limit", "x"},
		{"get", epicTestID, "--limit", "1.5"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			fake := newEpicPartFake()
			stdout, _, err := runEpicCommand(t, fake, nil, args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("epicCommand(%v) = %v, want a usage error", args, err)
			}
			if !strings.Contains(err.Error(), epicPartForm) {
				t.Errorf("epicCommand(%v) error %q does not name the form %q", args, err, epicPartForm)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(fake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", args, n)
			}
		})
	}
}

// TestEpicPartLeavesOtherRefusalsToEpicGet checks that a form the CLI accepts, which epic_get refuses, returns
// epic_get's refusal rather than a usage error, and reads nothing.
func TestEpicPartLeavesOtherRefusalsToEpicGet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"get", epicTestID, "--offset", "2"}, want: "offset needs a part"},
		{args: []string{"get", epicTestID, "--limit", "2"}, want: "limit needs a part"},
		{args: []string{"get", epicTestID, "--task", "T1"}, want: "task needs part task"},
		{args: []string{"get", epicTestID, "--part", "task"}, want: "part task needs task"},
		{
			args: []string{"get", epicTestID, "--part", "tasks", "--task", "T1"},
			want: "task needs part task, not part tasks",
		},
		{
			args: []string{"get", epicTestID, "--part", "turn", "--turn", "0"},
			want: `turn "0" is not a positive integer`,
		},
		{args: []string{"get", epicTestID, "--part", "tasks", "--offset", "-1"}, want: "offset must not be negative"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			fake := newEpicPartFake()
			stdout, _, err := runEpicCommand(t, fake, nil, tc.args...)
			if err == nil {
				t.Fatalf("epicCommand(%v) = nil, want epic_get's refusal", tc.args)
			}
			if _, ok := errors.AsType[usageError](err); ok {
				t.Errorf("epicCommand(%v) error %q is a usage error, want epic_get's refusal", tc.args, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("epicCommand(%v) error %q does not carry %q", tc.args, err, tc.want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(fake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", tc.args, n)
			}
		})
	}
}
