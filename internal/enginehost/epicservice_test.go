package enginehost

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// epicServiceID is the id of the synthetic epic the tests create.
const epicServiceID = "synthetic-repo-0a1b2c3/epic-service"

// epicTestStore opens a store in a temporary directory. The store closes at cleanup.
func epicTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

// fixedSteerID is the session id fixedSteering starts.
const fixedSteerID = "steering-fixed"

// fixedSteering is the SteeringSessions the epic server helpers serve with: Start returns fixedSteerID and starts no
// session, and Stop does nothing.
type fixedSteering struct{}

// Start returns fixedSteerID.
func (fixedSteering) Start(context.Context, store.EpicRow) (string, error) { return fixedSteerID, nil }

// Stop does nothing.
func (fixedSteering) Stop(context.Context, string) error { return nil }

// startEpicServer serves epics with fixedSteering on a sessions socket in a temporary directory and returns the socket's
// path. The server stops at cleanup.
func startEpicServer(t *testing.T, epics EpicCommands) string {
	t.Helper()
	return serveEpicsWith(t, epics, fixedSteering{})
}

// serveEpicsWith serves epics with steering on a sessions socket in a temporary directory and returns the socket's
// path. The server stops at cleanup.
func serveEpicsWith(t *testing.T, epics EpicCommands, steering SteeringSessions) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, epics, steering, nil, nil, nil, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path
}

// newEpicsClient returns an Epics client that dials the socket at path.
func newEpicsClient(t *testing.T, path string) enginev1connect.EpicsClient {
	t.Helper()
	return enginev1connect.NewEpicsClient(unixHTTPClient(t, path), "http://epics.invalid")
}

// fullEpicRequest returns a create request for a synthetic contract with id and every section at two or more rows,
// naming the git repository repo and based at its HEAD. Its seed decisions are D1, D2 and D3.
func fullEpicRequest(repo, id string) *v1.CreateEpicRequest {
	return &v1.CreateEpicRequest{
		Contract: &v1.EpicContract{
			Id:         id,
			Repository: repo,
			Title:      "Epic service",
			Approved:   "Synthetic approval sentence.",
			Intent:     "Synthetic intent.",
			Failures: []*v1.EpicFailure{
				{Id: "F1", Text: "Synthetic failure one.", Rating: "serious", Response: "Synthetic response one."},
				{Id: "F2", Text: "Synthetic failure two.", Rating: "severe", Response: "Synthetic response two."},
			},
			Care: "serious",
			Additions: []*v1.EpicAddition{
				{Location: "R1", What: "Synthetic addition one.", Why: "Synthetic reason.", Cost: "Synthetic cost."},
				{Location: "F2", What: "Synthetic addition two.", Why: "Synthetic reason.", Cost: "None."},
				{Location: "docs/guide.md", What: "Synthetic note.", Why: "Synthetic reason.", Cost: "None."},
			},
			Requirements: []*v1.EpicRequirement{
				{Id: "R1", Text: "Synthetic requirement one.", Item: epicTestItem()},
				{Id: "R2", Text: "Synthetic requirement two.", Item: epicTestItem()},
			},
			Premises: []*v1.EpicPremise{
				{Id: "P1", Text: "Synthetic premise one.", IfWrong: "Synthetic consequence one."},
				{Id: "P2", Text: "Synthetic premise two.", IfWrong: "Synthetic consequence two."},
			},
			Exclusions: []string{"Synthetic exclusion one.", "Synthetic exclusion two."},
			Approach:   "Synthetic approach.",
			Alternatives: []*v1.EpicAlternative{
				{
					Alternative: "Synthetic alternative one.", WhyNot: "Synthetic reason.",
					Reconsider: "Synthetic trigger.",
				},
				{Alternative: "Synthetic alternative two.", WhyNot: "Synthetic reason.", Reconsider: "Never."},
			},
			ReleaseSteps: []*v1.EpicReleaseStep{
				{Step: "Synthetic release one.", Target: "Synthetic target one.", Undo: "Synthetic undo one."},
				{Step: "Synthetic release two.", Target: "Synthetic target two.", Undo: "Synthetic undo two."},
			},
			ReleaseThen: "Synthetic release line.",
			Checks:      "go test ./...",
			Base:        epicTestBase(repo),
			Gates:       epicTestGates(),
			DoneGates:   []string{"check"},
		},
		Decisions: []*v1.EpicDecisionInput{
			{Id: "D1", Text: "Synthetic decision one."},
			{Id: "D2", Text: "Synthetic decision two."},
			{Id: "D3", Text: "Synthetic decision three."},
		},
	}
}

// wantConnectError asserts that err is a Connect error with code and exactly message.
func wantConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v %q", code, message)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error is %T, want *connect.Error", err)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("code = %v, want %v (%v)", got, code, err)
	}
	if got := connectErr.Message(); got != message {
		t.Fatalf("message = %q, want %q", got, message)
	}
}

// createEpicOK creates req through client and returns the stored epic the reply carries. It fails the test on error.
func createEpicOK(t *testing.T, client enginev1connect.EpicsClient, req *v1.CreateEpicRequest) *v1.StoredEpic {
	t.Helper()
	created, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", req.GetContract().GetId(), err)
	}
	return created.Msg.GetEpic()
}

// getEpicOK returns the stored epic with epicServiceID through client. It fails the test on error.
func getEpicOK(t *testing.T, client enginev1connect.EpicsClient) *v1.StoredEpic {
	t.Helper()
	got, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
	if err != nil {
		t.Fatalf("GetEpic %s: %v", epicServiceID, err)
	}
	return got.Msg.GetEpic()
}

func TestEpicCreateThenGetRoundTripsContract(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	req := fullEpicRequest(repo, epicServiceID)

	stored := createEpicOK(t, client, req)

	want := fullEpicRequest(repo, epicServiceID).GetContract()
	// A stored Requirement reads with the state its runs give; none has run yet.
	for _, requirement := range want.GetRequirements() {
		requirement.State = "pending"
	}
	if !proto.Equal(stored.GetContract(), want) {
		t.Fatalf("created contract = %v, want %v", stored.GetContract(), want)
	}
	if stored.GetPhase() != "accepted" {
		t.Fatalf("phase = %q, want accepted", stored.GetPhase())
	}
	if stored.GetChanged() != stored.GetCreated() {
		t.Fatalf("changed = %d, want created %d", stored.GetChanged(), stored.GetCreated())
	}
	if stored.GetPhaseAt() != stored.GetCreated() {
		t.Fatalf("phase_at = %d, want created %d", stored.GetPhaseAt(), stored.GetCreated())
	}
	if report := stored.GetReport(); report != nil {
		t.Fatalf("report = %v before one is stored, want unset", report)
	}
	seeds := req.GetDecisions()
	if len(stored.GetDecisions()) != len(seeds) {
		t.Fatalf("decisions = %d, want %d seeds", len(stored.GetDecisions()), len(seeds))
	}
	for i, seed := range seeds {
		// The seeds are made by the caller, which here names no conversation and so is the owner.
		want := &v1.EpicDecision{
			Seq: int64(i + 1), Id: seed.GetId(), Text: seed.GetText(), Time: stored.GetCreated(), By: "owner",
		}
		if !proto.Equal(stored.GetDecisions()[i], want) {
			t.Fatalf("decision %d = %v, want seed %v", i, stored.GetDecisions()[i], want)
		}
	}

	got := getEpicOK(t, client)
	if !proto.Equal(got, stored) {
		t.Fatalf("GetEpic = %v, want the created epic %v", got, stored)
	}

	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("store Epic: %v", err)
	}
	if row.ID != epicServiceID || row.Phase != "accepted" {
		t.Fatalf("stored row = %q phase %q, want %q accepted", row.ID, row.Phase, epicServiceID)
	}
	if created := stored.GetCreated(); created != row.Created.Unix() {
		t.Fatalf("created = %d, want the stored time %d", created, row.Created.Unix())
	}
}

func TestEpicCreateOfStoredIDIsAlreadyExists(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	req := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
	createEpicOK(t, client, req)

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
	wantConnectError(t, err, connect.CodeAlreadyExists, "id: "+epicServiceID+" is already in the database")
}

func TestEpicCreateRefusesWhatCheckEpicRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(req *v1.CreateEpicRequest)
		want   string
	}{
		{
			name:   "intent blank",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Intent = "  " },
			want:   "intent: empty",
		},
		{
			name:   "base blank",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Base = " " },
			want:   "base: empty",
		},
		{
			name:   "requirement item missing",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Item = nil },
			want:   "requirements[0].item: missing",
		},
		{
			name:   "release undo empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.ReleaseSteps[0].Undo = "" },
			want:   "release_steps[0].undo: empty",
		},
		{
			name:   "care not a level",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Care = "dire" },
			want:   "care: not limited, serious or severe",
		},
		{
			name:   "review gates set",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.ReviewGates = []string{"review"} },
			want:   "review_gates: refused, the reviewers fill the review list",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			req := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
			tc.mutate(req)

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
			if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
				t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
			}
		})
	}
}

// epicReviewerNames returns the names of the reviewers msgs holds, in order.
func epicReviewerNames(msgs []*v1.EpicReviewer) []string {
	names := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		names = append(names, msg.GetName())
	}
	return names
}

// TestEpicCreateStoresMergedReviewers asserts a create merges the reviewers of orrery's defaults, the repository's
// project file and the contract, stores them in the order their names first appear, and returns them from GetEpic. Its
// review list is the enabled task-stage names of those reviewers, in the same order, and GetEpic echoes neither a
// review gate nor a contract reviewer.
func TestEpicCreateStoresMergedReviewers(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".orrery/project.toml", strings.Join([]string{
		"[[reviewers]]",
		`name = "floor"`,
		"limit = 600",
		"",
		"[[reviewers]]",
		`name = "synthetic_check"`,
		`stage = ["task"]`,
		`agent = "code-reviewer"`,
		`must = ["Synthetic statement holds."]`,
		"",
	}, "\n"))
	req := fullEpicRequest(repo, epicServiceID)
	req.Contract.Reviewers = []*v1.EpicReviewer{
		{Name: "floor", Limit: new(int64(300))},
		{Name: "security", Enabled: new(false)},
	}

	stored := createEpicOK(t, client, req)
	got := getEpicOK(t, client)
	if !proto.Equal(got, stored) {
		t.Fatalf("GetEpic = %v, want the created epic %v", got, stored)
	}
	want := []string{"floor", "conformance", "integration", "security", "test_parsimony", "release", "synthetic_check"}
	if names := epicReviewerNames(got.GetReviewers()); !slices.Equal(names, want) {
		t.Fatalf("reviewers = %v, want %v", names, want)
	}
	byName := make(map[string]*v1.EpicReviewer, len(got.GetReviewers()))
	for _, rev := range got.GetReviewers() {
		byName[rev.GetName()] = rev
	}
	if limit := byName["floor"].GetLimit(); limit != 300 {
		t.Fatalf("floor limit = %d, want 300 from the contract over the project's 600", limit)
	}
	if byName["security"].GetEnabled() {
		t.Fatal("security enabled, want the contract to disable it")
	}
	if agent := byName["synthetic_check"].GetAgent(); agent != "code-reviewer" {
		t.Fatalf("synthetic_check agent = %q, want code-reviewer from the project file", agent)
	}
	if gates := got.GetContract().GetReviewGates(); len(gates) != 0 {
		t.Fatalf("contract review_gates = %q, want none echoed", gates)
	}
	if declared := got.GetContract().GetReviewers(); len(declared) != 0 {
		t.Fatalf("contract reviewers = %d, want none echoed", len(declared))
	}

	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("store Epic: %v", err)
	}
	wantReview := []string{"floor", "synthetic_check"}
	if !slices.Equal(row.ReviewGates, wantReview) {
		t.Fatalf("review list = %v, want the enabled task-stage reviewers %v", row.ReviewGates, wantReview)
	}
}

// TestEpicCreateRefusesBadReviewerEntry asserts an entry of the project file, or a reviewer of the contract, that the
// layers cannot hold is refused InvalidArgument with the message naming its file or epic_create, its entry and its
// key, and nothing is stored.
func TestEpicCreateRefusesBadReviewerEntry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		project  string
		declared []*v1.EpicReviewer
		want     func(repo string) string
	}{
		{
			name:    "project file entry",
			project: "[[reviewers]]\nname = \"floor\"\nlimit = \"soon\"\n",
			want: func(repo string) string {
				return "read the reviewers of " + repo + ": " + filepath.Join(repo, ".orrery", "project.toml") +
					": reviewers[0] floor: limit: has the wrong type"
			},
		},
		{
			name:     "contract entry",
			declared: []*v1.EpicReviewer{{Name: "floor", Limit: new(int64(0))}},
			want: func(repo string) string {
				return "merge the reviewers of " + repo +
					": epic_create: reviewers[0] floor: limit: must be at least 1 second"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			repo := stepGitTestRepo(t)
			if tc.project != "" {
				stepGitTestWrite(t, repo, ".orrery/project.toml", tc.project)
			}
			req := fullEpicRequest(repo, epicServiceID)
			req.Contract.Reviewers = tc.declared

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want(repo))
			if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
				t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
			}
		})
	}
}

// TestEpicGetShowsUnverifiedFinding asserts a task finding stored unverified, as a review with verify off settles it,
// reads back unverified, and a finding a verifier settled reads back verified.
func TestEpicGetShowsUnverifiedFinding(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := plannedBuildEpic(t)
	review := store.TaskBuild{Stage: "review", Base: buildBaseRevision, Attempt: 1, Revision: buildHeadRevision}
	if err := st.SetTaskBuild(ctx, epicServiceID, 1, store.TaskBuild{}, review); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := st.AddTaskReview(ctx, epicServiceID, 1, review, review, store.TaskReview{
		Attempt: 1, Position: 0, Gate: "floor", Step: buildReviewStepID, Verdict: "fails",
		Findings: []store.TaskFinding{
			{Position: 0, ID: "F1", Claim: "Synthetic claim one.", Citation: "R1", Location: "a.go:10",
				Evidence: "Synthetic evidence one.", VerifyBy: "Synthetic check one.",
				Step: buildReviewStepID, Result: "confirmed", Unverified: true},
			{Position: 1, ID: "F2", Claim: "Synthetic claim two.", Citation: "R2", Location: "b.go:20",
				Evidence: "Synthetic evidence two.", VerifyBy: "Synthetic check two.",
				Step: buildVerifyStepID, Result: "dropped", Reason: "Synthetic reason two."},
		},
	}); err != nil {
		t.Fatalf("AddTaskReview: %v", err)
	}

	got := taskWithID(t, getEpicOK(t, client), "T1")
	wantProtos(t, "T1 reviews", got.GetReviews(), []*v1.EpicTaskReview{
		{
			Attempt: 1, Gate: "floor", Step: buildReviewStepID, Verdict: "fails",
			Findings: []*v1.EpicTaskFinding{
				{
					Id: "F1", Claim: "Synthetic claim one.", Location: "a.go:10", Step: buildReviewStepID,
					Result: "confirmed", Unverified: true,
				},
				{
					Id: "F2", Claim: "Synthetic claim two.", Location: "b.go:20", Step: buildVerifyStepID,
					Result: "dropped", Reason: "Synthetic reason two.",
				},
			},
		},
	})
}

func TestEpicGetUnknownIsNotFound(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))

	_, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeNotFound, "id: no epic "+epicServiceID)
}

// TestEpicGetShowsStoredReportOnceWritten checks that GetEpic carries the report of an ending epic only after the store
// holds one, and then as the store's report, its outcome the report's phase.
func TestEpicGetShowsStoredReportOnceWritten(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	now := time.Unix(turnStarted, 0)
	row := store.EpicRow{
		ID:         epicServiceID,
		Repository: releaseTestRepo,
		Base:       releaseTestBase,
		Phase:      "released",
		Created:    now,
		Changed:    now,
	}
	if err := st.CreateEpic(ctx, row); err != nil {
		t.Fatalf("CreateEpic released: %v", err)
	}
	client := newEpicsClient(t, startEpicServer(t, st))
	if report := getEpicOK(t, client).GetReport(); report != nil {
		t.Fatalf("report = %v before one is stored, want unset", report)
	}

	err := st.SetEpicReport(ctx, epicServiceID, store.EpicReport{
		Phase: "released", Shipped: []string{"R1"}, NotShipped: []string{"R2"}, Why: "Synthetic why.",
	})
	if err != nil {
		t.Fatalf("SetEpicReport: %v", err)
	}
	want := &v1.EpicReport{
		Outcome: "released", Shipped: []string{"R1"}, NotShipped: []string{"R2"}, Why: "Synthetic why.",
	}
	if got := getEpicOK(t, client).GetReport(); !proto.Equal(got, want) {
		t.Fatalf("report = %v, want %v", got, want)
	}
}

func TestEpicListReturnsSummariesByID(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	repo := stepGitTestRepo(t)
	zeta := createEpicOK(t, client, fullEpicRequest(repo, "synthetic-repo-1111111/zeta"))
	alpha := createEpicOK(t, client, fullEpicRequest(repo, "synthetic-repo-0000000/alpha"))

	listed, err := client.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	want := []*v1.EpicSummary{summaryOf(alpha), summaryOf(zeta)}
	got := listed.Msg.GetEpics()
	if len(got) != len(want) {
		t.Fatalf("ListEpics returned %d epics, want %d", len(got), len(want))
	}
	for i := range want {
		if phaseAt := got[i].GetPhaseAt(); phaseAt != got[i].GetCreated() {
			t.Fatalf("summary %d phase_at = %d, want created %d", i, phaseAt, got[i].GetCreated())
		}
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("summary %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// summaryOf returns the summary of a stored epic: its id, repository, title, phase, phase time, created and changed,
// revision, steerer, pause, park and open question count.
func summaryOf(epic *v1.StoredEpic) *v1.EpicSummary {
	var open int32
	for _, question := range epic.GetQuestions() {
		if question.GetState() == store.QuestionOpen {
			open++
		}
	}
	return &v1.EpicSummary{
		Id:            epic.GetContract().GetId(),
		Repository:    epic.GetContract().GetRepository(),
		Title:         epic.GetContract().GetTitle(),
		Phase:         epic.GetPhase(),
		PhaseAt:       epic.GetPhaseAt(),
		Created:       epic.GetCreated(),
		Changed:       epic.GetChanged(),
		Revision:      epic.GetRevision(),
		Steerer:       epic.GetSteerer(),
		Paused:        epic.GetPaused(),
		PauseSource:   epic.GetPauseSource(),
		Parked:        epic.GetParked(),
		OpenQuestions: open,
	}
}

func TestEpicAnswerAppendsOnceAndRepeatsStoredDecision(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	answer := &v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: "Ship it."}

	first, err := client.AnswerEpic(t.Context(), connect.NewRequest(answer))
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	decision := first.Msg.GetDecision()
	if decision.GetSeq() != 4 || decision.GetId() != "A1" || decision.GetText() != "Ship it." {
		t.Fatalf("decision = %v, want seq 4 A1 %q", decision, "Ship it.")
	}
	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("store Epic: %v", err)
	}
	if len(row.Decisions) != 4 {
		t.Fatalf("stored decisions = %d, want 4", len(row.Decisions))
	}
	if at := decision.GetTime(); at != row.Decisions[3].Time.Unix() {
		t.Fatalf("decision time = %d, want the stored time %d", at, row.Decisions[3].Time.Unix())
	}

	repeat, err := client.AnswerEpic(t.Context(), connect.NewRequest(answer))
	if err != nil {
		t.Fatalf("repeat AnswerEpic: %v", err)
	}
	if !proto.Equal(repeat.Msg.GetDecision(), decision) {
		t.Fatalf("repeat decision = %v, want the stored %v", repeat.Msg.GetDecision(), decision)
	}

	decisions := getEpicOK(t, client).GetDecisions()
	if len(decisions) != 4 {
		t.Fatalf("decisions = %d, want 4 (three seeds and one answer)", len(decisions))
	}
	if !proto.Equal(decisions[3], decision) {
		t.Fatalf("last decision = %v, want %v", decisions[3], decision)
	}
}

func TestEpicAnswerWithOtherTextIsAlreadyExists(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	_, err := client.AnswerEpic(t.Context(), connect.NewRequest(
		&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: "Ship it."},
	))
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}

	_, err = client.AnswerEpic(t.Context(), connect.NewRequest(
		&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: "Hold it."},
	))
	wantConnectError(t, err, connect.CodeAlreadyExists, "id: A1 was recorded with other text")

	decisions := getEpicOK(t, client).GetDecisions()
	if len(decisions) != 4 || decisions[3].GetText() != "Ship it." {
		t.Fatalf("decisions = %v, want the first answer unchanged after four", decisions)
	}
}

func TestEpicAnswerUnknownEpicIsNotFound(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))

	_, err := client.AnswerEpic(t.Context(), connect.NewRequest(
		&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: "Ship it."},
	))
	wantConnectError(t, err, connect.CodeNotFound, "epic_id: no epic "+epicServiceID)
}

func TestEpicAnswerNamesEmptyField(t *testing.T) {
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	cases := []struct {
		name string
		req  *v1.AnswerEpicRequest
		want string
	}{
		{
			name: "epic_id",
			req:  &v1.AnswerEpicRequest{Id: "A1", Text: "Ship it."},
			want: "epic_id: empty",
		},
		{
			name: "id",
			req:  &v1.AnswerEpicRequest{EpicId: epicServiceID, Text: "Ship it."},
			want: "id: empty",
		},
		{
			name: "text",
			req:  &v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1"},
			want: "text: empty",
		},
		{
			name: "text blank after trimming",
			req:  &v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: " \t "},
			want: "text: empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.AnswerEpic(t.Context(), connect.NewRequest(tc.req))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
		})
	}
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 3 {
		t.Fatalf("decisions = %d after refused answers, want the three seeds", len(decisions))
	}
}

func TestEpicServiceWithoutStoreIsUnavailable(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, nil))
	ctx := t.Context()
	repo := stepGitTestRepo(t)

	_, err := client.CreateEpic(ctx, connect.NewRequest(fullEpicRequest(repo, epicServiceID)))
	wantConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
	_, err = client.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
	_, err = client.ListEpics(ctx, connect.NewRequest(&v1.ListEpicsRequest{}))
	wantConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
	_, err = client.AnswerEpic(ctx, connect.NewRequest(
		&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: "A1", Text: "Ship it."},
	))
	wantConnectError(t, err, connect.CodeUnavailable, "epic store unavailable")
}
