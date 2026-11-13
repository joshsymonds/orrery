package holder

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	// pidsChildPidFile is the file the test child writes its own pid to in its working directory.
	pidsChildPidFile = "child.pid"
	// absentPid is a pid no process holds: Linux pids stay below 2^22, so /proc holds no stat for it.
	absentPid = 1<<31 - 1
	// pidsStartField is the index of field 22, the start time, among the fields after a stat line's command name.
	// Field 3, the state, is index 0.
	pidsStartField = 22 - 3
	// pidsStatPlain is a stat line whose command name is plain. Its start time, field 22, is 987654.
	pidsStatPlain = "4242 (sh) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 1000000 200\n"
	// pidsStatCommWithSpaces is a stat line whose command name holds a space and a ')'. Its start time is 987654 too.
	pidsStatCommWithSpaces = "4242 (a b) c) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 1000000 200\n"
	// pidsStartTime is the start time both stat lines above carry.
	pidsStartTime = "987654"
)

// procAvailable reports whether this system has /proc, where a start time is read from.
func procAvailable() bool {
	_, err := os.Stat("/proc/self/stat")
	return err == nil
}

// procStartOf returns pid's start time from /proc/PID/stat, read by the test itself. Without /proc there is no start
// time, so the result is empty.
func procStartOf(t *testing.T, pid int) string {
	t.Helper()
	if !procAvailable() {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatalf("read /proc/%d/stat: %v", pid, err)
	}
	// The command name ends at the last ')', and field 3 is the first field after it.
	fields := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
	if len(fields) <= pidsStartField {
		t.Fatalf("/proc/%d/stat has %d fields after its command name: %q", pid, len(fields), raw)
	}
	return fields[pidsStartField]
}

// readPidsRecord returns the pids record at path. It fails the test when the file cannot be read or decoded.
func readPidsRecord(t *testing.T, path string) Pids {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read pids %s: %v", path, err)
	}
	var pids Pids
	if err = json.Unmarshal(data, &pids); err != nil {
		t.Fatalf("decode pids %s: %v", path, err)
	}
	return pids
}

// requirePidsKeys fails unless the record at path holds exactly the keys its shape names.
func requirePidsKeys(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read pids %s: %v", path, err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode pids %s: %v", path, err)
	}
	got := slices.Sorted(maps.Keys(fields))
	want := []string{"child", "childStart", "holder", "holderStart"}
	if !slices.Equal(got, want) {
		t.Errorf("pids record keys = %q, want %q", got, want)
	}
}

// processesIn returns the pids of the processes whose working directory is dir. A process that has ended is not
// listed.
func processesIn(t *testing.T, dir string) []int {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd"))
		if err == nil && cwd == resolved {
			pids = append(pids, pid)
		}
	}
	return pids
}

func TestPidsStartTimeOfReadsFieldTwentyTwoAfterTheCommandName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		stat string
	}{
		{name: "plain command name", stat: pidsStatPlain},
		{name: "command name with a space and a parenthesis", stat: pidsStatCommWithSpaces},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := startTimeOf([]byte(tc.stat))
			if err != nil {
				t.Fatalf("startTimeOf: %v", err)
			}
			if got != pidsStartTime {
				t.Errorf("startTimeOf = %q, want %q", got, pidsStartTime)
			}
		})
	}
}

func TestPidsStartTimeOfRefusesMalformedStat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		stat string
	}{
		{name: "no command name", stat: "4242 S 1\n"},
		{name: "too few fields", stat: "4242 (sh) S 1 4242 4242\n"},
		{name: "start time not decimal", stat: strings.Replace(pidsStatPlain, pidsStartTime, "98x654", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := startTimeOf([]byte(tc.stat)); err == nil {
				t.Errorf("startTimeOf = %q, nil; want an error", got)
			}
		})
	}
}

func TestPidsProcStartTimeReadsLivePidAndLeavesAbsentPidEmpty(t *testing.T) {
	t.Parallel()

	self, err := procStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("procStartTime(own pid): %v", err)
	}
	if want := procStartOf(t, os.Getpid()); self != want {
		t.Errorf("procStartTime(own pid) = %q, want %q", self, want)
	}
	absent, err := procStartTime(absentPid)
	if err != nil {
		t.Fatalf("procStartTime(absent pid): %v", err)
	}
	if absent != "" {
		t.Errorf("procStartTime(absent pid) = %q, want empty", absent)
	}
}

func TestPidsWriteRoundTripsModeAndLeavesNoTempFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, PidsName)
	want := Pids{Holder: 11, HolderStart: "100", Child: 22, ChildStart: "200"}
	if err := WritePids(path, want); err != nil {
		t.Fatalf("WritePids: %v", err)
	}
	if got := readPidsRecord(t, path); got != want {
		t.Errorf("pids record = %+v, want %+v", got, want)
	}
	assertMode(t, path, filesTestMode)
	assertDirNames(t, dir, PidsName)
}

func TestPidsWriteRefusesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), PidsName)
	first := Pids{Holder: 1, HolderStart: "10", Child: 2, ChildStart: "20"}
	if err := WritePids(path, first); err != nil {
		t.Fatalf("first WritePids: %v", err)
	}
	second := Pids{Holder: 3, HolderStart: "30", Child: 4, ChildStart: "40"}
	if err := WritePids(path, second); !errors.Is(err, ErrPidsExist) {
		t.Fatalf("second WritePids: err = %v, want ErrPidsExist", err)
	}
	if got := readPidsRecord(t, path); got != first {
		t.Errorf("pids record after refused write = %+v, want %+v", got, first)
	}
}

func TestPidsHolderRecordsItsOwnAndItsChildsPidsWithStartTimes(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	// The pid goes to a temporary file renamed into place, so the file never appears empty.
	cfg.Child = shChild(t,
		"echo $$ > "+pidsChildPidFile+".tmp && mv "+pidsChildPidFile+".tmp "+pidsChildPidFile+"; exec sleep 60")
	startHolder(t.Context(), t, cfg)
	recordPath := filepath.Join(cfg.Dir, PidsName)
	awaitFile(t, recordPath)
	awaitFile(t, filepath.Join(cfg.Child.Dir, pidsChildPidFile))

	childPid := readChildPid(t, filepath.Join(cfg.Child.Dir, pidsChildPidFile))
	holderPid := os.Getpid()
	want := Pids{
		Holder:      holderPid,
		HolderStart: procStartOf(t, holderPid),
		Child:       childPid,
		ChildStart:  procStartOf(t, childPid),
	}
	if got := readPidsRecord(t, recordPath); got != want {
		t.Errorf("pids record = %+v, want %+v", got, want)
	}
	assertMode(t, recordPath, filesTestMode)
	requirePidsKeys(t, recordPath)
}

func TestPidsHolderWithUnwritableRecordStopsChildAndWritesExit(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	cfg.Child = shChild(t, "exec sleep 60")
	if err := PrepareDir(cfg.Dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	// A record already in place is one the holder's write must not replace, so the write fails.
	if err := os.WriteFile(filepath.Join(cfg.Dir, PidsName), nil, filesTestMode); err != nil {
		t.Fatalf("WriteFile pids: %v", err)
	}

	run := startHolder(t.Context(), t, cfg)
	err := run.wait(t)
	if !errors.Is(err, ErrPidsExist) {
		t.Fatalf("Run = %v, want ErrPidsExist", err)
	}
	exit := readExitFile(t, cfg.Dir)
	if exit.Reason != ReasonExited || exit.Code != -1 || !strings.Contains(exit.Stderr, ErrPidsExist.Error()) {
		t.Errorf("exit = %+v, want reason %q, code -1, stderr naming %v", exit, ReasonExited, ErrPidsExist)
	}
	// The child started before its record was refused, so it ran in the child's directory. None may outlive Run.
	if procAvailable() {
		if alive := processesIn(t, cfg.Child.Dir); len(alive) != 0 {
			t.Errorf("processes %v still run in the child's directory after Run", alive)
		}
	}
	requireNoSocket(t, socketOf(t, cfg.Dir))
	awaitLockFree(t, cfg.Dir)
	assertDirNames(t, cfg.Dir, ExitName, PidsName, LockName, segmentName(0))
}
