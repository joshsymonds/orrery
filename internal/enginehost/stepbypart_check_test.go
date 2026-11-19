package enginehost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// checkRowsSteps is a StepCommands that answers Status from its rows, as resultRowsSteps does, and keeps its gate logs in
// logs.
type checkRowsSteps struct {
	resultRowsSteps
	logs string
}

// The service finds the kept gate logs by a type assertion, so this keeps checkRowsSteps satisfying it at compile time.
var _ keptGateLogs = checkRowsSteps{}

// KeptGateLogs returns the logs directory the fake keeps its gate logs in.
func (f checkRowsSteps) KeptGateLogs() (string, error) {
	return f.logs, nil
}

// stepsOnGateDir is a real *Steps, so its KeptGateLogs names the logs directory under its gate state directory. Its Status
// answers from rows, as resultRowsSteps does; every other method is the Steps'.
type stepsOnGateDir struct {
	*Steps
	rows resultRowsSteps
}

// Status answers from rows.
func (f stepsOnGateDir) Status(ctx context.Context, id string) (store.StepRow, error) {
	return f.rows.Status(ctx, id)
}

// writeCheckLog writes text as the file name in dir and returns its path, failing t when the write fails.
func writeCheckLog(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// readCheckReply calls ReadStepCheck with req and returns its reply, failing t when the call fails.
func readCheckReply(
	t *testing.T,
	client enginev1connect.StepsClient,
	req *v1.ReadStepCheckRequest,
) *v1.ReadStepCheckResponse {
	t.Helper()
	resp, err := client.ReadStepCheck(context.Background(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("ReadStepCheck(%v): %v", req, err)
	}
	return resp.Msg
}

func TestStepByPartCheck(t *testing.T) {
	logs := t.TempDir()

	// The last gate round's log holds 100 numbered lines, and each 25th line also says FAIL.
	gateLines := make([]string, 0, 100)
	for i := 1; i <= 100; i++ {
		line := fmt.Sprintf("line %03d", i)
		if i%25 == 0 {
			line += " FAIL"
		}
		gateLines = append(gateLines, line)
	}
	gateLog := writeCheckLog(t, logs, "gate-round-2.log", strings.Join(gateLines, "\n")+"\n")
	firstGateLog := writeCheckLog(t, logs, "gate-round-1.log", "first run\nonly line\n")
	longLog := writeCheckLog(t, logs, "long.log", strings.Repeat("x", 200)+"\nshort\n")
	missingLog := filepath.Join(logs, "never-written.log")

	steps := checkRowsSteps{
		resultRowsSteps: resultRowsSteps{rows: map[string]store.StepRow{
			"s1": {ID: "s1", Checks: []store.StepCheck{
				{Round: 1, Name: "gate", OK: false, Detail: "exit 1", Log: firstGateLog},
				{Round: 2, Name: "gate", OK: false, Detail: "exit 2", Log: gateLog},
				{
					Round: 1, Name: "lint", OK: true, Detail: "clean",
					Stdout: "stdout line 1\nstdout line 2", Stderr: "warn",
				},
				{Round: 2, Name: "long", OK: false, Detail: strings.Repeat("d", 4500), Log: longLog},
				{Round: 2, Name: "unkept", OK: true, Detail: "ok", Stdout: "tail", Log: missingLog},
			}},
		}},
		logs: logs,
	}
	path, stop := startServerWithSteps(t, nil, steps)
	client := newStepClient(t, path)

	t.Run("detail and last 40 lines of a kept log by default, in the last round", func(t *testing.T) {
		got := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate"})
		want := &v1.ReadStepCheckResponse{
			Round:      2,
			Name:       "gate",
			Ok:         false,
			Detail:     "exit 2",
			Kept:       true,
			Output:     strings.Join(gateLines[60:], "\n"),
			FirstLine:  61,
			Total:      100,
			NextBefore: 61,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("gate = %v, want %v", got, want)
		}
	})

	t.Run("lines narrows the slice and a lines above 80 is clamped", func(t *testing.T) {
		narrow := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Lines: 5})
		wantNarrow := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output: strings.Join(gateLines[95:], "\n"), FirstLine: 96, Total: 100, NextBefore: 96,
		}
		if !proto.Equal(narrow, wantNarrow) {
			t.Fatalf("lines 5 = %v, want %v", narrow, wantNarrow)
		}
		clamped := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Lines: 200})
		wantClamped := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output: strings.Join(gateLines[20:], "\n"), FirstLine: 21, Total: 100, NextBefore: 21,
		}
		if !proto.Equal(clamped, wantClamped) {
			t.Fatalf("lines 200 = %v, want %v", clamped, wantClamped)
		}
	})

	t.Run("before pages backward with next_before", func(t *testing.T) {
		page := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Lines: 10})
		wantPage := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output: strings.Join(gateLines[90:], "\n"), FirstLine: 91, Total: 100, NextBefore: 91,
		}
		if !proto.Equal(page, wantPage) {
			t.Fatalf("lines 10 = %v, want %v", page, wantPage)
		}
		older := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Lines: 10, Before: 91})
		wantOlder := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output: strings.Join(gateLines[80:90], "\n"), FirstLine: 81, Total: 100, NextBefore: 81,
		}
		if !proto.Equal(older, wantOlder) {
			t.Fatalf("before 91 = %v, want %v", older, wantOlder)
		}
		first := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Lines: 10, Before: 11})
		wantFirst := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output: strings.Join(gateLines[:10], "\n"), FirstLine: 1, Total: 100,
		}
		if !proto.Equal(first, wantFirst) {
			t.Fatalf("before 11 = %v, want %v", first, wantFirst)
		}
		err := stepCheckError(client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Before: 102})
		requireInvalidArgument(t, err, "total of 100")
	})

	t.Run("match keeps only matching lines", func(t *testing.T) {
		got := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Match: "FAIL"})
		want := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output:    "line 025 FAIL\nline 050 FAIL\nline 075 FAIL\nline 100 FAIL",
			FirstLine: 1, Total: 4,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("match FAIL = %v, want %v", got, want)
		}
		last := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Match: "FAIL", Lines: 2})
		wantLast := &v1.ReadStepCheckResponse{
			Round: 2, Name: "gate", Detail: "exit 2", Kept: true,
			Output:    "line 075 FAIL\nline 100 FAIL",
			FirstLine: 3, Total: 4, NextBefore: 3,
		}
		if !proto.Equal(last, wantLast) {
			t.Fatalf("match FAIL lines 2 = %v, want %v", last, wantLast)
		}
	})

	t.Run("an earlier round reads that round's check, and an unrun round is refused", func(t *testing.T) {
		got := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "gate", Round: 1})
		want := &v1.ReadStepCheckResponse{
			Round: 1, Name: "gate", Detail: "exit 1", Kept: true,
			Output: "first run\nonly line", FirstLine: 1, Total: 2,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("round 1 = %v, want %v", got, want)
		}
		err := stepCheckError(client, &v1.ReadStepCheckRequest{Id: "s1", Check: "lint", Round: 2})
		requireInvalidArgument(t, err, "did not run in round 2", "rounds 1")
	})

	t.Run("kept logs are read from the gate state directory's logs", func(t *testing.T) {
		stateDir := t.TempDir()
		stateLogs := filepath.Join(stateDir, "logs")
		if err := os.Mkdir(stateLogs, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", stateLogs, err)
		}
		kept := writeCheckLog(t, stateLogs, "step-round-1.log", "kept line\n")
		onDisk := stepsOnGateDir{
			Steps: &Steps{gateStateDir: stateDir},
			rows: resultRowsSteps{rows: map[string]store.StepRow{
				"g1": {ID: "g1", Checks: []store.StepCheck{
					{Round: 1, Name: "gate", Detail: "exit 1", Stdout: "stored tail", Log: kept},
				}},
			}},
		}
		dir, err := onDisk.KeptGateLogs()
		if err != nil {
			t.Fatalf("KeptGateLogs: %v", err)
		}
		if dir != stateLogs {
			t.Fatalf("KeptGateLogs = %q, want %q", dir, stateLogs)
		}
		onDiskPath, onDiskStop := startServerWithSteps(t, nil, onDisk)
		got := readCheckReply(t, newStepClient(t, onDiskPath), &v1.ReadStepCheckRequest{Id: "g1", Check: "gate"})
		want := &v1.ReadStepCheckResponse{
			Round: 1, Name: "gate", Detail: "exit 1", Kept: true,
			Output: "kept line", FirstLine: 1, Total: 1,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("g1 = %v, want %v", got, want)
		}
		if err := onDiskStop(); err != nil {
			t.Fatalf("Serve: %v", err)
		}
	})

	t.Run("a check with no kept log returns its detail and stored tails", func(t *testing.T) {
		got := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "lint"})
		want := &v1.ReadStepCheckResponse{
			Round: 1, Name: "lint", Ok: true, Detail: "clean", Kept: false,
			Output:    "== stdout ==\nstdout line 1\nstdout line 2\n== stderr ==\nwarn",
			FirstLine: 1, Total: 5,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("lint = %v, want %v", got, want)
		}
		missing := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "unkept"})
		wantMissing := &v1.ReadStepCheckResponse{
			Round: 2, Name: "unkept", Ok: true, Detail: "ok", Kept: false,
			Output:    "== stdout ==\ntail\n== stderr ==",
			FirstLine: 1, Total: 3,
		}
		if !proto.Equal(missing, wantMissing) {
			t.Fatalf("unkept = %v, want %v", missing, wantMissing)
		}
	})

	t.Run("a long line and a long detail are cut", func(t *testing.T) {
		got := readCheckReply(t, client, &v1.ReadStepCheckRequest{Id: "s1", Check: "long"})
		want := &v1.ReadStepCheckResponse{
			Round: 2, Name: "long", Detail: strings.Repeat("d", 3999) + "…", Kept: true,
			Output:    strings.Repeat("x", 149) + "…\nshort",
			FirstLine: 1, Total: 2,
		}
		if !proto.Equal(got, want) {
			t.Fatalf("long = %v, want %v", got, want)
		}
	})

	t.Run("an unknown check is an error naming the checks", func(t *testing.T) {
		err := stepCheckError(client, &v1.ReadStepCheckRequest{Id: "s1", Check: "nope"})
		requireInvalidArgument(t, err, `no check "nope"`, "gate, lint, long, unkept")
		empty := stepCheckError(client, &v1.ReadStepCheckRequest{Id: "s1"})
		requireInvalidArgument(t, empty, "gate, lint, long, unkept")
	})

	t.Run("an unknown id is not_found", func(t *testing.T) {
		err := stepCheckError(client, &v1.ReadStepCheckRequest{Id: "missing", Check: "gate"})
		if got := connect.CodeOf(err); got != connect.CodeNotFound {
			t.Fatalf("code = %v, want %v (error %v)", got, connect.CodeNotFound, err)
		}
	})

	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// stepCheckError calls ReadStepCheck with req and returns the error the call fails with, or nil when it succeeds.
func stepCheckError(client enginev1connect.StepsClient, req *v1.ReadStepCheckRequest) error {
	_, err := client.ReadStepCheck(context.Background(), connect.NewRequest(req))
	return err
}
