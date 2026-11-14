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

// Link keeps a stream open to Host. Run starts Command with no shell and reads its stdout as deskstream lines. Command
// is ssh in production.
type Link struct {
	Host    string
	Command []string
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
}

// session is one run of Command. Its reader sends each parsed line on lines, then waits for the command and sets
// exitErr before closing lines.
type session struct {
	lines   chan deskstream.Line
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

// start runs Command once with its stdin and stdout piped and begins reading its lines. The command keeps its stdin
// pipe open until it exits, because `orrery notices` exits when its stdin ends.
func (l *Link) start(ctx context.Context, logger *slog.Logger) (*session, error) {
	cmd := exec.CommandContext(ctx, l.Command[0], l.Command[1:]...)
	if _, err := cmd.StdinPipe(); err != nil {
		return nil, fmt.Errorf("desk link stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("desk link stdout: %w", err)
	}
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("run desk link command: %w", err)
	}
	sess := &session{lines: make(chan deskstream.Line)}
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

// serve handles one session until the command exits. It fires the lost wait while the session runs, and it keeps
// draining the session after ctx ends so the command can exit.
func (l *Link) serve(ctx context.Context, logger *slog.Logger, st *linkState, sess *session) {
	done := ctx.Done()
	for {
		select {
		case line, ok := <-sess.lines:
			if !ok {
				logger.InfoContext(ctx, "desk link exited", "error", sess.exitErr)
				return
			}
			l.handle(st, line)
		case <-st.lostCh:
			st.lostCh = nil
			l.Lost(l.Host)
		case <-done:
			done = nil
			st.lostCh = nil
		}
	}
}

// handle acts on one line. A hello makes the stream live, and a notice goes to Notify. Other kinds are ignored.
func (l *Link) handle(st *linkState, line deskstream.Line) {
	switch line.Kind {
	case deskstream.KindHello:
		st.backoff = initialRestart
		st.lostCh = nil
		st.lostArmed = false
	case deskstream.KindNotice:
		l.Notify(line)
	}
}

// markDown records that the stream is not live. The lost wait starts at the first loss after a hello, so restarts do
// not restart it.
func (l *Link) markDown(st *linkState) {
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
