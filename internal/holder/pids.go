package holder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// PidsName is the record a holder writes once its child has started.
const PidsName = "holder.json"

// ErrPidsExist means the pids record already exists, so the holder's record was already written.
var ErrPidsExist = errors.New("holder: pids file already exists")

// startFieldIndex is the index of field 22, the process's start time, among the fields after the command name. Field 3,
// the state, is index 0.
const startFieldIndex = 22 - 3

// Pids records a holder and its child: each process id with its start time from /proc/PID/stat, as a decimal string.
// A start is empty where /proc holds no stat for the process.
type Pids struct {
	Holder      int    `json:"holder"`
	HolderStart string `json:"holderStart"`
	Child       int    `json:"child"`
	ChildStart  string `json:"childStart"`
}

// recordChild writes the pids record for this holder and child into dir. When the record cannot be written, it stops
// the child and returns once the child is reaped, so the exit file the caller writes never precedes the child's end.
func recordChild(dir string, child *Child) error {
	pids, err := currentPids(child.Pid())
	if err == nil {
		err = WritePids(filepath.Join(dir, PidsName), pids)
	}
	if err != nil {
		child.Stop()
		<-child.Done()
	}
	return err
}

// currentPids returns the record of this holder and of the child whose pid is childPid.
func currentPids(childPid int) (Pids, error) {
	holderPid := os.Getpid()
	holderStart, err := procStartTime(holderPid)
	if err != nil {
		return Pids{}, err
	}
	childStart, err := procStartTime(childPid)
	if err != nil {
		return Pids{}, err
	}
	return Pids{Holder: holderPid, HolderStart: holderStart, Child: childPid, ChildStart: childStart}, nil
}

// WritePids writes pids as JSON to path. The file is created with mode 0600. WritePids refuses with ErrPidsExist when
// path exists and never replaces an existing file. It writes the record to a temporary file in path's directory and
// hard-links that file to path, so a reader sees either no file or the whole record. The temporary file is removed on
// every return.
func WritePids(path string, pids Pids) (err error) {
	data, err := json.Marshal(pids)
	if err != nil {
		return fmt.Errorf("holder: encode pids: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("holder: create temporary pids file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if rmErr := os.Remove(tmpPath); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("holder: remove temporary pids file: %w", rmErr))
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("holder: write temporary pids file: %w", err), tmp.Close())
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("holder: close temporary pids file: %w", err)
	}
	if err = os.Link(tmpPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrPidsExist, path)
		}
		return fmt.Errorf("holder: link pids %s: %w", path, err)
	}
	return nil
}

// procStartTime returns the start time of process pid, read from /proc/PID/stat. It returns "" with no error where
// /proc holds no stat for pid: /proc is not available, or the process has already ended.
func procStartTime(pid int) (string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	// A process reaped between the open and the read fails the read with ESRCH; it is gone all the same.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("holder: read stat of pid %d: %w", pid, err)
	}
	return startTimeOf(raw)
}

// startTimeOf returns field 22, the start time, of a /proc/PID/stat line. The command name is in parentheses and may
// hold spaces and parentheses, so the fields are counted after the last ')'.
func startTimeOf(stat []byte) (string, error) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("holder: stat has no command name: %q", stat)
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= startFieldIndex {
		return "", fmt.Errorf("holder: stat has %d fields after its command name, want more than %d: %q",
			len(fields), startFieldIndex, stat)
	}
	start := fields[startFieldIndex]
	if _, err := strconv.ParseUint(start, 10, 64); err != nil {
		return "", fmt.Errorf("holder: stat start time %q: %w", start, err)
	}
	return start, nil
}
