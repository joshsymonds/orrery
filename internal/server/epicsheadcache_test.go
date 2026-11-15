package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// headCacheSteps is a StepClient that holds no state, so concurrent calls share nothing they write. Its ListSteps
// answers no steps. The epics handler calls no other StepClient method, so those answer unimplemented.
type headCacheSteps struct{}

func (headCacheSteps) StepStatus(
	context.Context,
	*connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (headCacheSteps) ListSteps(
	context.Context,
	*connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	return connect.NewResponse(&enginev1.ListStepsResponse{}), nil
}

func (headCacheSteps) HoldStep(
	context.Context,
	*connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (headCacheSteps) NudgeStep(
	context.Context,
	*connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (headCacheSteps) StopStep(
	context.Context,
	*connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// headCacheAnswer is what one List and one Get of repo/epic answered.
type headCacheAnswer struct {
	epics []*orreryv1.Epic
	get   *orreryv1.EpicsGetResponse
}

// readHeadCacheConcurrently calls List and then Get of repo/epic from four goroutines at once, and returns each
// goroutine's answer in goroutine order.
func readHeadCacheConcurrently(t *testing.T, client orreryv1connect.EpicsClient) []headCacheAnswer {
	t.Helper()
	const readers = 4
	answers := make([]headCacheAnswer, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() {
			list, err := client.List(context.Background(), connect.NewRequest(&orreryv1.EpicsListRequest{}))
			if err != nil {
				t.Errorf("List: %v", err)
				return
			}
			get, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: "repo/epic"}))
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			answers[i] = headCacheAnswer{epics: list.Msg.GetEpics(), get: get.Msg}
		})
	}
	wg.Wait()
	return answers
}

// sameHeadCacheAnswers checks that every answer in got equals want, message for message.
func sameHeadCacheAnswers(t *testing.T, label string, want headCacheAnswer, got []headCacheAnswer) {
	t.Helper()
	for i, answer := range got {
		if len(answer.epics) != len(want.epics) {
			t.Errorf("%s: answer %d listed %d epics, want %d", label, i, len(answer.epics), len(want.epics))
			continue
		}
		for j := range want.epics {
			if !proto.Equal(answer.epics[j], want.epics[j]) {
				t.Errorf("%s: answer %d List epic %d = %v, want %v", label, i, j, answer.epics[j], want.epics[j])
			}
		}
		if !proto.Equal(answer.get, want.get) {
			t.Errorf("%s: answer %d Get = %v, want %v", label, i, answer.get, want.get)
		}
	}
}

// expectHeadCacheProblem checks that every answer lists repo/epic with problem and Gets it with tasks tasks.
func expectHeadCacheProblem(t *testing.T, label string, answers []headCacheAnswer, problem string, tasks int) {
	t.Helper()
	for i, answer := range answers {
		if len(answer.epics) != 1 || answer.epics[0].GetProblem() != problem {
			t.Errorf("%s: answer %d List = %v, want one epic with problem %q", label, i, answer.epics, problem)
		}
		if answer.get.GetEpic().GetProblem() != problem || len(answer.get.GetTasks()) != tasks {
			t.Errorf("%s: answer %d Get problem %q with %d tasks, want problem %q with %d tasks",
				label, i, answer.get.GetEpic().GetProblem(), len(answer.get.GetTasks()), problem, tasks)
		}
	}
}

// garbleKeepingMtime overwrites the file at path with as many x bytes as it held, so it no longer parses at the same
// size, then sets its mtime back to mtime.
func garbleKeepingMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(info.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	setHeadCacheMtime(t, path, mtime)
}

// setHeadCacheMtime sets the mtime of the file at path.
func setHeadCacheMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// TestEpicsHeadCacheAnswersHoldUntilHeadChanges checks that List and Get, called from several goroutines at once,
// keep answering from a head's decode while its size and mtime hold, and answer its new problem once its mtime moves.
func TestEpicsHeadCacheAnswersHoldUntilHeadChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	epicDir := filepath.Join(root, "repo", "epic")
	headPath := filepath.Join(epicDir, "state.json")
	effortPath := filepath.Join(epicDir, "efforts", "1", "state.json")
	mtime := epicsNow()
	later := mtime.Add(time.Minute)
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, mtime)
	writeEffortState(t, epicDir, "1", `{"tasks":[{"slug":"one-a","subject":"One A","status":"done"}]}`)
	setHeadCacheMtime(t, effortPath, mtime)
	client := epicsServe(t, root, headCacheSteps{})

	first := readHeadCacheConcurrently(t, client)
	expectHeadCacheProblem(t, "first read", first, "", 1)
	want := first[0]
	sameHeadCacheAnswers(t, "first read", want, first)

	garbleKeepingMtime(t, headPath, mtime)
	garbleKeepingMtime(t, effortPath, mtime)
	sameHeadCacheAnswers(t, "same-size garbage under old mtimes", want,
		readHeadCacheConcurrently(t, client))

	setHeadCacheMtime(t, headPath, later)
	expectHeadCacheProblem(t, "new head mtime", readHeadCacheConcurrently(t, client),
		"repo/epic/state.json: does not parse", 1)

	setHeadCacheMtime(t, effortPath, later)
	expectHeadCacheProblem(t, "new effort mtime", readHeadCacheConcurrently(t, client),
		"repo/epic/state.json: does not parse; efforts/1/state.json: does not parse", 0)
}
