package holder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// orphanPoll is how often StopOrphan checks whether the recorded child has stopped.
const orphanPoll = 10 * time.Millisecond

// zombieState is the state /proc gives a process that has ended and is not yet reaped.
const zombieState = "Z"

// ReadPids reads the pids record of the holder in dir. The error wraps fs.ErrNotExist when dir holds no record.
func ReadPids(dir string) (Pids, error) {
	path := filepath.Clean(filepath.Join(dir, PidsName))
	data, err := os.ReadFile(path)
	if err != nil {
		return Pids{}, fmt.Errorf("holder: read pids: %w", err)
	}
	var pids Pids
	if err = json.Unmarshal(data, &pids); err != nil {
		return Pids{}, fmt.Errorf("holder: decode pids %s: %w", path, err)
	}
	return pids, nil
}

// Running reports whether pid is a live process, not a zombie, whose /proc start time equals start. A pid that /proc
// holds no stat for is not running.
func Running(pid int, start string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	// A process reaped between the open and the read fails the read with ESRCH; it is gone all the same.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("holder: read stat of pid %d: %w", pid, err)
	}
	state, err := stateOf(raw)
	if err != nil {
		return false, err
	}
	got, err := startTimeOf(raw)
	if err != nil {
		return false, err
	}
	return state != zombieState && got == start, nil
}

// StopOrphan stops the child that a gone holder in dir recorded. It does nothing when dir holds an exit file, since the
// child ended, or no pids record. Otherwise, while Running confirms the recorded child, it sends SIGKILL to the child's
// process group and waits until the child stops. It fails when the record has an empty start, which cannot be checked,
// and when ctx ends before the child stops.
func StopOrphan(ctx context.Context, dir string) error {
	pids, found, err := orphanRecord(dir)
	if err != nil || !found {
		return err
	}
	if pids.HolderStart == "" || pids.ChildStart == "" {
		return fmt.Errorf("holder: pids record %s has an empty start, so its processes cannot be checked",
			filepath.Join(dir, PidsName))
	}
	running, err := Running(pids.Child, pids.ChildStart)
	if err != nil || !running {
		return err
	}
	if err = syscall.Kill(-pids.Child, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("holder: kill orphan group %d: %w", pids.Child, err)
	}
	for {
		if err = ctx.Err(); err != nil {
			return fmt.Errorf("holder: wait for orphan child %d to stop: %w", pids.Child, err)
		}
		running, err = Running(pids.Child, pids.ChildStart)
		if err != nil || !running {
			return err
		}
		select {
		case <-ctx.Done():
		case <-time.After(orphanPoll):
		}
	}
}

// orphanRecord returns the pids record in dir and whether there is one to stop. found is false when dir holds an exit
// file, which means the child ended, or holds no pids record.
func orphanRecord(dir string) (pids Pids, found bool, err error) {
	_, statErr := os.Lstat(filepath.Join(dir, ExitName))
	switch {
	case statErr == nil:
		return Pids{}, false, nil
	case !errors.Is(statErr, fs.ErrNotExist):
		return Pids{}, false, fmt.Errorf("holder: inspect exit file: %w", statErr)
	}
	pids, err = ReadPids(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Pids{}, false, nil
	}
	if err != nil {
		return Pids{}, false, err
	}
	return pids, true, nil
}

// stateOf returns the state letter of a /proc/PID/stat line: field 3, the first field after the command name.
func stateOf(stat []byte) (string, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("holder: stat has no command name: %q", stat)
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) == 0 {
		return "", fmt.Errorf("holder: stat has no state after its command name: %q", stat)
	}
	return fields[0], nil
}
