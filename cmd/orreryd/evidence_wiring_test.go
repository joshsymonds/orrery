package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epic, its run, its judge step and the texts are invented. No role file exists, so the planner
// starts no turn. The run's judge step is stored already ended, so the verdict is read from it and no judge runs.
const (
	evidenceWiringEpic     = "orrery-5a6b7c8/evidence-wiring"
	evidenceWiringAbsent   = "orrery-9z9y9x9/no-such-epic"
	evidenceWiringRepo     = "/work/synthetic-repo"
	evidenceWiringHost     = "synthetic-host"
	evidenceWiringRun      = "synthetic-run"
	evidenceWiringStep     = "synthetic-judge"
	evidenceWiringRev      = "abc123"
	evidenceWiringWait     = 10 * time.Second
	evidenceWiringPoll     = 10 * time.Millisecond
	evidenceWiringStamp    = 1_700_000_000
	evidenceWiringJudgeEnd = "done"
)

// TestSessionsEngineAnswersListEvidenceWithStore checks that a --sessions orreryd with a store answers ListEvidence for an
// epic it does not hold with an empty list, not Unavailable.
func TestSessionsEngineAnswersListEvidenceWithStore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	opts := evidenceWiringOptions(t)
	host, err := startSessions(
		ctx, opts, evidenceWiringHost, st, &recordingRouter{},
		slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	client := evidenceWiringClient(t, protocol.SessionSocketPath(opts.socket))
	resp, err := client.ListEvidence(ctx, connect.NewRequest(&v1.ListEvidenceRequest{Epic: evidenceWiringAbsent}))
	if err != nil {
		t.Errorf("ListEvidence of an unknown epic: %v, want an empty list", err)
	} else if n := len(resp.Msg.GetRuns()); n != 0 {
		t.Errorf("runs = %d, want none", n)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsHostTakesUpJudgingEvidenceRun checks that a --sessions orreryd takes up a judging evidence run its previous
// run left open. The run's judge step is stored and ended with done, so the verdict holds, is recorded once, and no second
// judge step starts beside it.
func TestSessionsHostTakesUpJudgingEvidenceRun(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	getenv, path := evidenceWiringEnv(t)
	st := openPaneStore(t, path)
	evidenceWiringCreateEpic(t, st)
	evidenceWiringJudging(t, st)

	opts := evidenceWiringOptions(t)
	host, err := startSessions(
		ctx, opts, evidenceWiringHost, st, &recordingRouter{},
		slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	run := waitEvidenceVerdict(t, st, evidenceWiringRun)
	if run.Verdict != evidence.VerdictHolds {
		t.Errorf("verdict = %q, want %q: the open run is taken up at start", run.Verdict, evidence.VerdictHolds)
	}
	if run.Judges != 1 {
		t.Errorf("judges = %d, want 1: the stored judge is the only one", run.Judges)
	}
	steps, err := st.LabelledSteps(ctx, evidenceWiringEpic, "evidence-"+evidenceWiringRun)
	if err != nil {
		t.Fatalf("labeled steps: %v", err)
	}
	if len(steps) != 1 {
		t.Errorf("judge steps = %d, want 1: no judge starts beside the stored one", len(steps))
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSessionsEngineAnswersListEvidenceUnavailableWithoutStore checks that a --sessions orreryd without a store refuses
// ListEvidence as unavailable.
func TestSessionsEngineAnswersListEvidenceUnavailableWithoutStore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	getenv := func(string) string { return "" }
	opts := evidenceWiringOptions(t)
	host, err := startSessions(
		ctx, opts, evidenceWiringHost, nil, &recordingRouter{},
		slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	client := evidenceWiringClient(t, protocol.SessionSocketPath(opts.socket))
	_, err = client.ListEvidence(ctx, connect.NewRequest(&v1.ListEvidenceRequest{Epic: evidenceWiringEpic}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("ListEvidence without a store: %v, want code %v", err, connect.CodeUnavailable)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// evidenceWiringEnv returns a getenv over a fresh state and config directory, and the store path it resolves to.
func evidenceWiringEnv(t *testing.T) (func(string) string, string) {
	t.Helper()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return getenv, path
}

// evidenceWiringOptions returns options whose daemon socket is in a fresh directory, so the engine socket beside it is too.
func evidenceWiringOptions(t *testing.T) options {
	t.Helper()
	return options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
}

// evidenceWiringClient returns an Evidence client that dials the engine socket at path.
func evidenceWiringClient(t *testing.T, path string) enginev1connect.EvidenceClient {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return enginev1connect.NewEvidenceClient(&http.Client{Transport: transport}, "http://evidence.invalid")
}

// evidenceWiringCreateEpic stores the synthetic epic in st.
func evidenceWiringCreateEpic(t *testing.T, st *store.Store) {
	t.Helper()
	created := time.Unix(evidenceWiringStamp, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:          evidenceWiringEpic,
		Repository:  evidenceWiringRepo,
		Title:       "Wire the evidence runner",
		Approved:    "approved text",
		Intent:      "take up the open runs",
		Care:        "serious",
		Approach:    "one resume at start",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the open run is taken up", Evidence: "wiring test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
}

// evidenceWiringJudging stores run in phase green in state judging, with the judge step stored and ended with done. The
// step is labeled with the run's epic and evidence task, as a judge step is.
func evidenceWiringJudging(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	stamp := time.Unix(evidenceWiringStamp, 0)
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: evidenceWiringRun, Epic: evidenceWiringEpic, Item: "R1", Revision: evidenceWiringRev,
		Phase: evidence.PhaseGreen, Source: evidence.SourceSupplied,
		Spec: evidence.Item{
			Supplied:   []string{"result.txt"},
			Conclusion: "the open run is taken up",
			Judge:      "judge the kept result",
		},
		Created: stamp,
	}); err != nil {
		t.Fatalf("add evidence run: %v", err)
	}
	if err := st.RecordEvidenceCapture(ctx, evidenceWiringRun, store.EvidenceCapture{At: stamp}); err != nil {
		t.Fatalf("record capture: %v", err)
	}
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepAdd{
			ID: evidenceWiringStep, Workspace: evidenceWiringRepo, Revision: evidenceWiringRev, ReadOnly: true,
			GateName: "evidence_judge", Epic: evidenceWiringEpic, Task: "evidence-" + evidenceWiringRun,
		},
		store.StepEnd{ID: evidenceWiringStep, End: evidenceWiringJudgeEnd, Status: "passed"},
	}, stamp); err != nil {
		t.Fatalf("add judge step: %v", err)
	}
	if err := st.SetEvidenceJudge(ctx, evidenceWiringRun, evidenceWiringStep); err != nil {
		t.Fatalf("set judge: %v", err)
	}
}

// waitEvidenceVerdict reads the run id from st until it has a verdict or evidenceWiringWait has passed, and returns the
// last read.
func waitEvidenceVerdict(t *testing.T, st *store.Store, id string) store.EvidenceRun {
	t.Helper()
	deadline := time.Now().Add(evidenceWiringWait)
	for {
		run, err := st.EvidenceRun(t.Context(), id)
		if err != nil {
			t.Fatalf("read evidence run %s: %v", id, err)
		}
		if run.Verdict != "" || time.Now().After(deadline) {
			return run
		}
		time.Sleep(evidenceWiringPoll)
	}
}
