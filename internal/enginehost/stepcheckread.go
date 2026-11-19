package enginehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrBadCheckRead reports a ReadStepCheck request that names no check the step ran, a round the check did not run in, or a
// before past the check's lines.
var ErrBadCheckRead = errors.New("step check read is refused")

const (
	// checkDetailLimit is the most runes a check's detail holds.
	checkDetailLimit = 4000
	// checkLineLimit is the most runes a line of a check's output holds.
	checkLineLimit = 150
	// checkLinesDefault is the number of lines a slice holds when the request names none.
	checkLinesDefault = 40
	// checkLinesMax is the most lines a slice holds.
	checkLinesMax = 80
)

// keptGateLogs is the optional interface a StepCommands implements to name the directory a failed gate's kept logs are in.
type keptGateLogs interface {
	KeptGateLogs() (string, error)
}

// KeptGateLogs returns the directory a failed gate's kept logs are in: the logs directory under the gate state directory.
func (s *Steps) KeptGateLogs() (string, error) {
	stateDir, err := gateStateDir(s.gateStateDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, gateLogsName), nil
}

// readStepCheck returns the check of row that req names, with the slice of its output that req bounds. logs names the kept
// gate logs, or is nil when the steps keep none to read.
func readStepCheck(
	row store.StepRow,
	req *v1.ReadStepCheckRequest,
	logs keptGateLogs,
) (*v1.ReadStepCheckResponse, error) {
	check, err := pickStepCheck(row, req.GetCheck(), req.GetRound())
	if err != nil {
		return nil, err
	}
	text, kept, err := checkOutput(check, logs)
	if err != nil {
		return nil, err
	}
	lines := logLines(text, req.GetMatch())
	slice, start, err := sliceLines(lines, req.GetLines(), req.GetBefore())
	if err != nil {
		return nil, err
	}
	cut := make([]string, 0, len(slice))
	for _, line := range slice {
		cut = append(cut, cutRunes(line, checkLineLimit))
	}
	round, err := uint32Count(check.Round)
	if err != nil {
		return nil, err
	}
	total, err := uint32Count(len(lines))
	if err != nil {
		return nil, err
	}
	firstLine := 0
	if len(slice) > 0 {
		firstLine = start + 1
	}
	first, err := uint32Count(firstLine)
	if err != nil {
		return nil, err
	}
	var nextBefore uint32
	if firstLine > 1 {
		nextBefore = first
	}
	return &v1.ReadStepCheckResponse{
		Round:      round,
		Name:       check.Name,
		Ok:         check.OK,
		Detail:     cutRunes(check.Detail, checkDetailLimit),
		Kept:       kept,
		Output:     strings.Join(cut, "\n"),
		FirstLine:  first,
		Total:      total,
		NextBefore: nextBefore,
	}, nil
}

// pickStepCheck returns the check of row named name in round. Round zero picks the named check's last round. A name that
// no check of row has, or a round the name did not run in, is refused, and the refusal names the checks or the rounds.
func pickStepCheck(row store.StepRow, name string, round uint32) (store.StepCheck, error) {
	var named []store.StepCheck
	for _, check := range row.Checks {
		if check.Name == name {
			named = append(named, check)
		}
	}
	if len(named) == 0 {
		return store.StepCheck{}, fmt.Errorf("%w: step %s has no check %q; its checks are %s",
			ErrBadCheckRead, row.ID, name, checkNames(row.Checks))
	}
	if round == 0 {
		last := named[0]
		for _, check := range named[1:] {
			if check.Round >= last.Round {
				last = check
			}
		}
		return last, nil
	}
	for _, check := range named {
		if int64(check.Round) == int64(round) {
			return check, nil
		}
	}
	return store.StepCheck{}, fmt.Errorf("%w: check %q did not run in round %d; it ran in rounds %s",
		ErrBadCheckRead, name, round, roundList(named))
}

// checkNames returns the distinct names of checks, comma-separated in the order each first appears, or none when there are
// no checks.
func checkNames(checks []store.StepCheck) string {
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name)
	}
	return listOrNone(distinct(names))
}

// roundList returns the distinct rounds of checks, comma-separated in the order each first appears.
func roundList(checks []store.StepCheck) string {
	rounds := make([]string, 0, len(checks))
	for _, check := range checks {
		rounds = append(rounds, strconv.Itoa(check.Round))
	}
	return strings.Join(distinct(rounds), ", ")
}

// listOrNone returns names comma-separated, or none when there are none.
func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// distinct returns the values of values, each once, in the order each first appears.
func distinct[T comparable](values []T) []T {
	seen := make(map[T]bool, len(values))
	out := make([]T, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// checkOutput returns the output a check's slice is taken from, and whether that output is the check's kept gate log. The
// kept log is read when the check names one and the steps keep logs and the file is there. Otherwise the output is the log
// the check's stored stdout and stderr make.
func checkOutput(check store.StepCheck, logs keptGateLogs) (string, bool, error) {
	if check.Log == "" || logs == nil {
		return storedTailsLog(check), false, nil
	}
	text, err := readKeptLog(check.Log, logs)
	if errors.Is(err, os.ErrNotExist) {
		return storedTailsLog(check), false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// readKeptLog returns the kept gate log at path. The log is read through an os.Root of the logs directory logs names, at
// the path relative to that directory, so a stored path cannot read outside the directory. A missing directory or file
// reports an error that matches os.ErrNotExist.
func readKeptLog(path string, logs keptGateLogs) (string, error) {
	dir, err := logs.KeptGateLogs()
	if err != nil {
		return "", fmt.Errorf("find kept gate logs: %w", err)
	}
	name, err := filepath.Rel(dir, path)
	if err != nil {
		return "", fmt.Errorf("kept gate log %s is not under %s: %w", path, dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open kept gate logs: %w", err)
	}
	data, err := root.ReadFile(name)
	if closeErr := root.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close kept gate logs: %w", closeErr))
	}
	if err != nil {
		return "", fmt.Errorf("read kept gate log: %w", err)
	}
	return string(data), nil
}

// storedTailsLog returns the log a check's stored tails make, in the form keepGateLog writes a kept log: its stdout, then its
// stderr, each under its label and ended with a newline.
func storedTailsLog(check store.StepCheck) string {
	var log strings.Builder
	for _, stream := range []struct{ label, text string }{{"stdout", check.Stdout}, {"stderr", check.Stderr}} {
		fmt.Fprintf(&log, "== %s ==\n", stream.label)
		log.WriteString(stream.text)
		if stream.text != "" && !strings.HasSuffix(stream.text, "\n") {
			log.WriteString("\n")
		}
	}
	return log.String()
}

// logLines returns the lines of text without a trailing empty line, keeping only those that contain match as plain text.
// An empty match contains in every line, so it keeps them all.
func logLines(text, match string) []string {
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var kept []string
	for _, line := range lines {
		if strings.Contains(line, match) {
			kept = append(kept, line)
		}
	}
	return kept
}

// sliceLines returns the slice of lines a request bounds: the last want lines numbered below before, numbered from 1. A
// zero want means checkLinesDefault, and one above checkLinesMax reads as checkLinesMax. A zero before means after the last
// line, and a before past the last line is refused. It returns the slice and the index of its first line.
func sliceLines(lines []string, want, before uint32) ([]string, int, error) {
	count := len(lines)
	stop := int(before)
	if stop == 0 {
		stop = count + 1
	}
	if stop > count+1 {
		return nil, 0, fmt.Errorf("%w: before %d is past the check's total of %d lines",
			ErrBadCheckRead, stop, count)
	}
	n := int(want)
	switch {
	case n == 0:
		n = checkLinesDefault
	case n > checkLinesMax:
		n = checkLinesMax
	}
	// The lines numbered below stop are the first stop-1 of lines, and the slice is the last n of them.
	end := stop - 1
	start := max(end-n, 0)
	return lines[start:end], start, nil
}

// cutRunes returns text when it holds at most limit runes. Otherwise it returns the first limit-1 runes and an ellipsis.
func cutRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
