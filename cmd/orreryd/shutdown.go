package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"
)

// shutdownStacksAfter is how long a shutdown runs before orreryd writes every goroutine's stack to a file beside the
// flight traces. systemd kills orreryd 90 s into a stop, so the stacks are written well before that, and a stop that
// hangs names the wait it hangs in.
const shutdownStacksAfter = 30 * time.Second

// shutdownStacksPrefix starts the name of each stacks file a slow shutdown writes.
const shutdownStacksPrefix = "shutdown-stacks-"

// Log messages of a shutdown: its start, each phase's start and end, and the stacks a slow one writes.
const (
	shutdownStartedMsg = "shutdown started"
	shutdownPhaseMsg   = "shutdown phase"
	shutdownStacksMsg  = "shutdown slow, goroutine stacks written"
)

// shutdownPhase logs that the shutdown phase name starts, runs phase, and logs that it is done and how long it took. A
// phase that never returns leaves its start as the last line of the shutdown.
func shutdownPhase(logger *slog.Logger, name string, phase func()) {
	logger.Info(shutdownPhaseMsg, "phase", name, "step", "start")
	start := time.Now()
	phase()
	logger.Info(shutdownPhaseMsg, "phase", name, "step", "done", "took", time.Since(start))
}

// watchShutdown logs the start of the shutdown once ctx ends. When the shutdown is still running after after, it writes
// every goroutine's stack, with how long each has waited, to a file in dir and logs the file's path, once. A write that
// fails is logged. The returned func ends the watch, and returns once a write already under way has finished; serve
// calls it as it returns.
func watchShutdown(ctx context.Context, logger *slog.Logger, dir string, after time.Duration) func() {
	var mu sync.Mutex
	ended := false
	var timer *time.Timer
	stopStart := context.AfterFunc(ctx, func() {
		logger.Info(shutdownStartedMsg)
		mu.Lock()
		defer mu.Unlock()
		if ended {
			return
		}
		timer = time.AfterFunc(after, func() {
			mu.Lock()
			defer mu.Unlock()
			if ended {
				return
			}
			path, err := writeStacks(dir)
			if err != nil {
				logger.Error(shutdownStacksMsg, "error", err)
				return
			}
			logger.Warn(shutdownStacksMsg, "path", path)
		})
	})
	return func() {
		stopStart()
		mu.Lock()
		defer mu.Unlock()
		ended = true
		if timer != nil {
			timer.Stop()
		}
	}
}

// writeStacks writes the stack of every goroutine to a new file in dir, which it creates when missing, and returns the
// file's path.
func writeStacks(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("stacks directory: %w", err)
	}
	path := filepath.Join(dir, shutdownStacksPrefix+strconv.FormatInt(time.Now().UnixNano(), 10)+".txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create stacks file: %w", err)
	}
	writeErr := pprof.Lookup("goroutine").WriteTo(file, 2)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return "", fmt.Errorf("write stacks file %s: %w", path, err)
	}
	return path, nil
}
