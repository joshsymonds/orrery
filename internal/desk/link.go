package desk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
)

const (
	// activityInterval is how often a live stream reports the desk's input.
	activityInterval = 30 * time.Second
	// initialRestart is the first wait before the command restarts. Each restart that follows no hello doubles the
	// wait, up to maxRestart.
	initialRestart = time.Second
	maxRestart     = time.Minute
	backoffFactor  = 2
	// lostAfter is how long the stream may stay down before Lost runs.
	lostAfter = 5 * time.Minute
)

// Clock reads the time and starts timers. Link takes every wait from it.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Link keeps a stream open to Host. Run starts Command with no shell, reads its stdout as deskstream lines, and writes
// the desk's Activity to its stdin. Command is ssh in production.
type Link struct {
	Host    string
	Command []string
	// Input reports the desk's newest input in unix seconds and whether there is any. A nil Input reports none.
	Input func(context.Context) (int64, bool, error)
	// Notify receives each notice line in the order the command sent it.
	Notify func(deskstream.Line)
	// Lost runs once for each stretch of five minutes without a live stream.
	Lost   func(host string)
	Clock  Clock
	Logger *slog.Logger
}

// linkState is what the link keeps across runs of the command.
type linkState struct {
	backoff time.Duration
	// lostArmed is set once the current down period has its lost wait, whether or not the wait has fired.
	lostArmed bool
	// lostCh delivers the lost wait. It is nil once the wait has fired or the stream is live.
	lostCh <-chan time.Time
	// activityCh delivers the next activity report. It is nil unless the stream is live.
	activityCh <-chan time.Time
}

// session is one run of Command. Its reader sends each parsed line on lines, then waits for the command and sets
// exitErr before closing lines.
type session struct {
	lines   chan deskstream.Line
	stdin   io.WriteCloser
	exitErr error
}

// Run keeps the stream open until ctx ends. Once ctx has ended and the command has exited, Run returns nil.
func (l *Link) Run(ctx context.Context) error {
	if len(l.Command) == 0 {
		return errors.New("desk link has no command")
	}
	logger := l.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	st := &linkState{
		backoff:   initialRestart,
		lostArmed: true,
		lostCh:    l.Clock.After(lostAfter),
	}
	for {
		l.runOnce(ctx, logger, st)
		if !l.pause(ctx, st) {
			return nil
		}
	}
}

// runOnce runs the command until it exits and marks the stream down. A command that fails to start is logged and
// counts as one that exited at once.
func (l *Link) runOnce(ctx context.Context, logger *slog.Logger, st *linkState) {
	sess, err := l.start(ctx, logger)
	if err != nil {
		logger.WarnContext(ctx, "start desk link", "error", err)
	} else {
		l.serve(ctx, logger, st, sess)
	}
	l.markDown(st)
}

// start runs Command once with its stdin and stdout piped and begins reading its lines.
func (l *Link) start(ctx context.Context, logger *slog.Logger) (*session, error) {
	cmd := exec.CommandContext(ctx, l.Command[0], l.Command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("desk link stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("desk link stdout: %w", err)
	}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("run desk link command: %w", err)
	}
	sess := &session{lines: make(chan deskstream.Line), stdin: stdin}
	go sess.read(cmd, stdout, logger)
	return sess, nil
}

// read sends each line of stdout on lines until stdout ends, then waits for the command to exit and closes lines.
func (s *session) read(cmd *exec.Cmd, stdout io.Reader, logger *slog.Logger) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var line deskstream.Line
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			logger.Debug("ignored malformed desk link line", "error", err)
			continue
		}
		s.lines <- line
	}
	if err := scanner.Err(); err != nil {
		logger.Warn("read desk link", "error", err)
		if err = cmd.Process.Kill(); err != nil {
			logger.Warn("kill desk link", "error", err)
		}
	}
	s.exitErr = cmd.Wait()
	close(s.lines)
}

// serve handles one session until the command exits. It reports activity and fires the lost wait while the session
// runs, and it keeps draining the session after ctx ends so the command can exit.
func (l *Link) serve(ctx context.Context, logger *slog.Logger, st *linkState, sess *session) {
	done := ctx.Done()
	for {
		select {
		case line, ok := <-sess.lines:
			if !ok {
				logger.InfoContext(ctx, "desk link exited", "error", sess.exitErr)
				return
			}
			l.handle(ctx, logger, st, sess, line)
		case <-st.activityCh:
			l.report(ctx, logger, sess)
			st.activityCh = l.Clock.After(activityInterval)
		case <-st.lostCh:
			st.lostCh = nil
			l.Lost(l.Host)
		case <-done:
			done = nil
			st.activityCh = nil
			st.lostCh = nil
		}
	}
}

// handle acts on one line. A hello makes the stream live, and a notice goes to Notify. Other kinds are ignored.
func (l *Link) handle(ctx context.Context, logger *slog.Logger, st *linkState, sess *session, line deskstream.Line) {
	switch line.Kind {
	case deskstream.KindHello:
		st.backoff = initialRestart
		st.lostCh = nil
		st.lostArmed = false
		st.activityCh = l.Clock.After(activityInterval)
		l.report(ctx, logger, sess)
	case deskstream.KindNotice:
		l.Notify(line)
	}
}

// report writes the desk's input to the command's stdin when Input has one.
func (l *Link) report(ctx context.Context, logger *slog.Logger, sess *session) {
	if l.Input == nil {
		return
	}
	input, ok, err := l.Input(ctx)
	if err != nil {
		logger.WarnContext(ctx, "read desk input", "error", err)
		return
	}
	if !ok {
		return
	}
	b, err := json.Marshal(deskstream.Activity{Input: input})
	if err != nil {
		logger.WarnContext(ctx, "encode desk activity", "error", err)
		return
	}
	if _, err = sess.stdin.Write(append(b, '\n')); err != nil {
		logger.WarnContext(ctx, "send desk activity", "error", err)
	}
}

// markDown records that the stream is not live. The lost wait starts at the first loss after a hello, so restarts do
// not restart it.
func (l *Link) markDown(st *linkState) {
	st.activityCh = nil
	if !st.lostArmed {
		st.lostArmed = true
		st.lostCh = l.Clock.After(lostAfter)
	}
}

// pause waits out the backoff before the command restarts and fires Lost if its wait ends first. It returns false
// when ctx ends first.
func (l *Link) pause(ctx context.Context, st *linkState) bool {
	wait := st.backoff
	st.backoff = min(backoffFactor*wait, maxRestart)
	restart := l.Clock.After(wait)
	for {
		select {
		case <-restart:
			return true
		case <-st.lostCh:
			st.lostCh = nil
			l.Lost(l.Host)
		case <-ctx.Done():
			return false
		}
	}
}
