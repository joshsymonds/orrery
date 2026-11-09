// Package hoststatus runs the configured host status command and returns its validated stats.
package hoststatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Stat is one host status line.
type Stat struct {
	Label   string   `json:"label"`
	Value   string   `json:"value"`
	Percent *float64 `json:"percent,omitempty"`
	Level   string   `json:"level"`
}

const (
	commandTimeout = 2 * time.Second
	waitDelay      = 500 * time.Millisecond
	cacheWindow    = 5 * time.Second
	maxOutputBytes = 64 << 10
	maxStats       = 12
	maxLabelRunes  = 32
	maxValueRunes  = 64
	maxPercent     = 100
)

var (
	errOutputTooLarge = errors.New("host status output exceeds 64 KiB")
	errInvalidOutput  = errors.New("host status output is invalid")
)

// Runner runs the host status command at path and caches its outcome.
type Runner struct {
	path string
	now  func() time.Time

	mu       sync.Mutex
	filled   bool
	cachedAt time.Time
	stats    []Stat
	runErr   error
}

// New returns a Runner for the command at path, timed by now.
func New(path string, now func() time.Time) *Runner {
	return &Runner{path: path, now: now}
}

// Stats returns the host status stats, from the cache when a call ran within the last 5 s.
// A call whose context ends before the command finishes does not cache its outcome.
func (r *Runner) Stats(ctx context.Context) ([]Stat, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if r.filled && now.Sub(r.cachedAt) < cacheWindow {
		return r.stats, r.runErr
	}
	stats, err := run(ctx, r.path)
	if ctx.Err() != nil {
		return stats, err
	}
	r.stats, r.runErr, r.cachedAt, r.filled = stats, err, now, true
	return stats, err
}

// run executes path with no arguments and no shell, and parses its stdout.
func run(ctx context.Context, path string) ([]Stat, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out capWriter
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdout = &out
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("host status command: %w", err)
	}
	return parse(out.buf)
}

// capWriter buffers command output and fails the write that would pass maxOutputBytes.
type capWriter struct {
	buf []byte
}

func (w *capWriter) Write(p []byte) (int, error) {
	if len(w.buf)+len(p) > maxOutputBytes {
		return 0, errOutputTooLarge
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// rawStat holds one decoded stat; pointer fields distinguish missing and wrong-typed values.
type rawStat struct {
	Label   *string  `json:"label"`
	Value   *string  `json:"value"`
	Percent *float64 `json:"percent"`
	Level   *string  `json:"level"`
}

// parse validates the command output as a JSON array of at most 12 stats.
func parse(data []byte) ([]Stat, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raws []rawStat
	if err := dec.Decode(&raws); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidOutput, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the array", errInvalidOutput)
	}
	if raws == nil {
		return nil, fmt.Errorf("%w: not an array", errInvalidOutput)
	}
	if len(raws) > maxStats {
		return nil, fmt.Errorf("%w: %d stats, at most %d allowed", errInvalidOutput, len(raws), maxStats)
	}
	stats := make([]Stat, 0, len(raws))
	for _, raw := range raws {
		stat, err := checkStat(raw)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

// checkStat validates one raw stat and cuts its label and value to their limits.
func checkStat(raw rawStat) (Stat, error) {
	switch {
	case raw.Label == nil || *raw.Label == "":
		return Stat{}, fmt.Errorf("%w: label must be a non-empty string", errInvalidOutput)
	case raw.Value == nil:
		return Stat{}, fmt.Errorf("%w: value must be a string", errInvalidOutput)
	case raw.Level == nil || !validLevel(*raw.Level):
		return Stat{}, fmt.Errorf("%w: level must be ok, warn or crit", errInvalidOutput)
	case raw.Percent != nil && (*raw.Percent < 0 || *raw.Percent > maxPercent):
		return Stat{}, fmt.Errorf("%w: percent must be within 0-100", errInvalidOutput)
	}
	return Stat{
		Label:   truncateRunes(*raw.Label, maxLabelRunes),
		Value:   truncateRunes(*raw.Value, maxValueRunes),
		Percent: raw.Percent,
		Level:   *raw.Level,
	}, nil
}

func validLevel(level string) bool {
	switch level {
	case "ok", "warn", "crit":
		return true
	default:
		return false
	}
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
