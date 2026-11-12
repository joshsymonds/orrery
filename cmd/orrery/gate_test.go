package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// gatePoolFile defines the default pool a gate with no needs takes its member from.
const gatePoolFile = `[pools.gate]
host = "lab"
cap = 1
members = ["gate-1"]
wake = ["wake-gate"]
wake_limit = 3
status = ["status-gate"]
status_limit = 4
admit = ["admit-gate"]
admit_limit = 5
`

// gatesFileName is the name of the gates file the gate tests write beside the project.
const gatesFileName = "gates.toml"

// gateTable returns the [gates.name] table that runs argv with a limit of limit seconds. needs is the inline table's
// text, such as `builds = 2`, or empty for a gate with no needs key.
func gateTable(name string, argv []string, limit int, needs string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, strconv.Quote(arg))
	}
	table := fmt.Sprintf("[gates.%s]\nargv = [%s]\nlimit = %d\n", name, strings.Join(quoted, ", "), limit)
	if needs != "" {
		table += fmt.Sprintf("needs = { %s }\n", needs)
	}
	return table
}

// gateDefinition is the definition gatePoolFile makes, as the request carries it.
func gateDefinition() *v1.PoolDefinition {
	return &v1.PoolDefinition{
		Name: "gate", Host: "lab", Cap: 1, Members: []string{"gate-1"},
		Wake: []string{"wake-gate"}, WakeLimitSeconds: 3,
		Status: []string{"status-gate"}, StatusLimitSeconds: 4,
		Admit: []string{"admit-gate"}, AdmitLimitSeconds: 5,
	}
}

// gateLine returns the result line `gate lint` writes for a gate defined by file in source, with argv, needs, and the
// exit status exit.
func gateLine(t *testing.T, source, file string, argv []string, needs string, exit int) string {
	t.Helper()
	argvJSON, err := json.Marshal(argv)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("orrery gate: lint from %s %s: argv %s needing %s; exit %d\n",
		source, file, argvJSON, needs, exit)
}

// TestGateUsageErrorsExitTwo refuses each command line whose gate arguments are wrong. Each exits 2 with the usage
// text, names the problem on its first line, and acquires nothing.
func TestGateUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no name", []string{}, "NAME"},
		{"two names", []string{"lint", "other"}, "NAME"},
		{"flag after name", []string{"lint", "--gates", "gates.toml"}, "NAME"},
		{"unknown flag", []string{"--bogus", "lint"}, "-bogus"},
		{"flag without value", []string{"--gates"}, "-gates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, gatePoolFile, "")
			fake := newFakePools(0)
			daemon := startPools(t, fake)
			var stdout bytes.Buffer
			args := append([]string{"--socket", daemon, "gate"}, tc.args...)
			res := awaitOrrery(t, startOrrery(t, &stdout, args...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			first, _, _ := strings.Cut(res.stderr, "\n")
			if !strings.HasPrefix(first, "orrery: gate: ") || !strings.Contains(first, tc.want) {
				t.Errorf("first stderr line %q, want gate's usage error naming %q", first, tc.want)
			}
			if !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr does not end with the usage text: %q", res.stderr)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("pools received %d requests, want none", len(got))
			}
			if strings.Contains(stdout.String(), "ran") {
				t.Errorf("the command ran: stdout %q", stdout.String())
			}
		})
	}
}

// TestGateFromGatesFileRunsUnderNeeds runs a gate named by --gates FILE under its needs, with its argv as the command,
// and reports it in one result line. The project file's gate of the same name does not win.
func TestGateFromGatesFileRunsUnderNeeds(t *testing.T) {
	argv := helperArgv(t, "print")
	project := buildsPoolFile + testsPoolFile + gateTable("lint", []string{"/nonexistent/other/tool"}, 9, "tests = 1")
	dir := setupProject(t, project, "")
	gatesFile := filepath.Join(dir, gatesFileName)
	writeRunFile(t, gatesFile, gateTable("lint", argv, 30, "tests = 1, builds = 2"))
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "gate", "--gates", gatesFile, "lint"))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	if !strings.Contains(stdout.String(), "ran\n") {
		t.Errorf("the command did not run: stdout %q", stdout.String())
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("pools received %d requests, want 1", len(got))
	}
	want := &v1.AcquireRequest{
		Project:   dir,
		Pools:     []*v1.PoolDefinition{buildsDefinition(), testsDefinition()},
		Needs:     []*v1.PoolNeed{{Pool: "builds", Count: 2}, {Pool: "tests", Count: 1}},
		ClientPid: int32(os.Getpid()),
		Command:   argv,
	}
	if !proto.Equal(got[0], want) {
		t.Errorf("request\n%v\nwant\n%v", got[0], want)
	}
	wantLine := gateLine(t, "epic", gatesFile, argv, "builds=2,tests=1", 0)
	if res.stderr != wantLine {
		t.Errorf("stderr %q, want %q", res.stderr, wantLine)
	}
}

// TestGateFromProjectFileDefaultsToGateMember runs a gate that names no needs from .orrery/project.toml. It takes one
// member of the gate pool, and the result line names the project file as its source.
func TestGateFromProjectFileDefaultsToGateMember(t *testing.T) {
	argv := helperArgv(t, "print")
	dir := setupProject(t, gatePoolFile+gateTable("lint", argv, 30, ""), "")
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "gate", "lint"))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("pools received %d requests, want 1", len(got))
	}
	want := &v1.AcquireRequest{
		Project:   dir,
		Pools:     []*v1.PoolDefinition{gateDefinition()},
		Needs:     []*v1.PoolNeed{{Pool: "gate", Count: 1}},
		ClientPid: int32(os.Getpid()),
		Command:   argv,
	}
	if !proto.Equal(got[0], want) {
		t.Errorf("request\n%v\nwant\n%v", got[0], want)
	}
	projectFile := filepath.Join(dir, ".orrery", "project.toml")
	wantLine := gateLine(t, "project", projectFile, argv, "gate=1", 0)
	if res.stderr != wantLine {
		t.Errorf("stderr %q, want %q", res.stderr, wantLine)
	}
}

// TestGateExitsWithCommandStatus exits with the status the gate's command exits with, and the result line says so.
func TestGateExitsWithCommandStatus(t *testing.T) {
	argv := helperArgv(t, "exit")
	dir := setupProject(t, buildsPoolFile+gateTable("lint", argv, 30, "builds = 1"), "")
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "gate", "lint"))
	if res.code != helperExitStatus {
		t.Errorf("exit status %d, want the command's %d; stderr %q", res.code, helperExitStatus, res.stderr)
	}
	projectFile := filepath.Join(dir, ".orrery", "project.toml")
	wantLine := gateLine(t, "project", projectFile, argv, "builds=1", helperExitStatus)
	if res.stderr != wantLine {
		t.Errorf("stderr %q, want %q", res.stderr, wantLine)
	}
}

// TestGateLimitKillsCommandOnce exits ExitLimit when the gate's command runs past its limit, and the command is
// acquired once: the limit does not rerun it.
func TestGateLimitKillsCommandOnce(t *testing.T) {
	argv := helperArgv(t, "sleep")
	dir := setupProject(t, buildsPoolFile+gateTable("lint", argv, 1, "builds = 1"), "")
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "gate", "lint"))
	if res.code != cli.ExitLimit {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitLimit, res.stderr)
	}
	if !strings.Contains(res.stderr, "ran past its limit") {
		t.Errorf("stderr %q does not say the limit killed the command", res.stderr)
	}
	projectFile := filepath.Join(dir, ".orrery", "project.toml")
	wantLine := gateLine(t, "project", projectFile, argv, "builds=1", cli.ExitLimit)
	if !strings.HasSuffix(res.stderr, wantLine) {
		t.Errorf("stderr %q does not end with %q", res.stderr, wantLine)
	}
	if got := fake.received(); len(got) != 1 {
		t.Errorf("pools received %d requests, want 1", len(got))
	}
}

// TestGateRefusalsExitNoGrant refuses each gate that cannot run: an unknown name, a gates file that is missing or
// refused, a need on an undeclared pool, and a pool file that is refused. Each exits 75 with the reason, and acquires
// nothing.
func TestGateRefusalsExitNoGrant(t *testing.T) {
	argv := helperArgv(t, "print")
	cases := []struct {
		name    string
		project string
		gates   string // written to gates.toml beside the project when not empty
		flag    bool   // passes --gates naming gates.toml, which may be missing
		gate    string
		want    string
	}{
		{"unknown name", buildsPoolFile + gateTable("other", argv, 30, "builds = 1"), "", false, "lint",
			"unknown gate"},
		{"no gate files", "", "", false, "lint", "unknown gate"},
		{"gates file missing", "", "", true, "lint", "read "},
		{"gates file refused", "", "# no gates table\n", true, "lint", "missing [gates] table"},
		{"undeclared pool", buildsPoolFile + gateTable("lint", argv, 30, "tests = 1"), "", false, "lint",
			`"tests" is not declared`},
		{"no gate pool", buildsPoolFile + gateTable("lint", argv, 30, ""), "", false, "lint",
			`"gate" is not declared`},
		{"pool file refused", badPoolFile + gateTable("lint", argv, 30, "builds = 1"), "", false, "lint",
			"unknown key"},
		{"cap too large", hugeCapPoolFile + gateTable("lint", argv, 30, "builds = 1"), "", false, "lint",
			"too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupProject(t, tc.project, "")
			if tc.gates != "" {
				writeRunFile(t, filepath.Join(dir, gatesFileName), tc.gates)
			}
			fake := newFakePools(0)
			daemon := startPools(t, fake)
			args := []string{"--socket", daemon, "gate"}
			if tc.flag {
				args = append(args, "--gates", filepath.Join(dir, gatesFileName))
			}
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append(args, tc.gate)...))
			if res.code != cli.ExitNoGrant {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitNoGrant, res.stderr)
			}
			first, _, _ := strings.Cut(res.stderr, "\n")
			if !strings.HasPrefix(first, "orrery gate: ") || !strings.Contains(first, tc.want) {
				t.Errorf("first stderr line %q, want the refusal naming %q", first, tc.want)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("pools received %d requests, want none", len(got))
			}
			if strings.Contains(stdout.String(), "ran") {
				t.Errorf("the command ran: stdout %q", stdout.String())
			}
		})
	}
}

// TestGateSignalEndsGrant ends the grant and the command when SIGINT, SIGTERM or SIGHUP reaches orrery while the gate's
// command runs. The command is ended by SIGTERM, so the gate exits with 128 plus SIGTERM.
func TestGateSignalEndsGrant(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			setupProject(t, buildsPoolFile+gateTable("lint", helperArgv(t, "sleep"), 30, "builds = 1"), "")
			fake := newFakePools(0)
			daemon := startPools(t, fake)
			stdoutR, stdoutW, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			t.Cleanup(func() {
				_ = stdoutR.Close()
				_ = stdoutW.Close()
			})
			res := startOrrery(t, stdoutW, "--socket", daemon, "gate", "lint")
			awaitReadyLine(t, stdoutR)
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatalf("send %v: %v", sig, err)
			}
			got := awaitOrrery(t, res)
			if got.code != sigtermStatus {
				t.Errorf("exit status %d, want %d; stderr %q", got.code, sigtermStatus, got.stderr)
			}
			select {
			case <-fake.ended:
			case <-time.After(runTimeout):
				t.Error("the grant's stream did not end")
			}
		})
	}
}

// TestUsageTextListsGate lists the gate subcommand and its arguments in the usage text.
func TestUsageTextListsGate(t *testing.T) {
	if !strings.Contains(usageText, "gate [--gates FILE] NAME") {
		t.Errorf("usage text does not list gate:\n%s", usageText)
	}
}
