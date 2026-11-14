package desk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	// lostTitle titles the popup for a host whose stream has been down for five minutes.
	lostTitle = "orrery desk"
	// noticesArg is the orrery subcommand that prints a host's notice stream.
	noticesArg = "notices"
)

// Config is what Run needs. Orrery is the orrery binary the local link runs, and Socket is the --socket path it
// passes on, or empty for the default. Hosts are the other machines whose streams the desk keeps. Sound is the file
// pw-play plays for each popup that is not quiet. Clock times the links.
type Config struct {
	Orrery string
	Socket string
	Hosts  []string
	Sound  string
	Clock  Clock
	Logger *slog.Logger
}

// SystemClock is the Clock of the running desk.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// After returns a channel that receives the time once d has passed.
func (SystemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// SSHCommand returns the argv that runs `orrery notices` on host over ssh. The connection is batch-only, never shares
// a ControlMaster, and sends its own keepalive, so a dead network ends the stream within about 45 seconds.
func SSHCommand(host string) []string {
	return []string{
		"ssh", "-T",
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--", host, "orrery", noticesArg,
	}
}

// Run keeps a stream open to the local machine and to each of cfg.Hosts, and shows each notice and each lost host as a
// popup with cfg.Sound, except that a quiet notice plays no sound. It returns nil once ctx has ended and every link has
// returned.
func Run(ctx context.Context, cfg Config) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	local, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("desk host name: %w", err)
	}

	queue := &popupQueue{sound: cfg.Sound, logger: logger, wake: make(chan struct{}, 1)}
	popupsDone := make(chan struct{})
	go func() {
		queue.run(ctx)
		close(popupsDone)
	}()

	localCommand := []string{cfg.Orrery, noticesArg}
	if cfg.Socket != "" {
		localCommand = []string{cfg.Orrery, "--socket", cfg.Socket, noticesArg}
	}
	links := []*Link{newLink(local, localCommand, cfg, queue, logger)}
	for _, host := range cfg.Hosts {
		links = append(links, newLink(host, SSHCommand(host), cfg, queue, logger))
	}

	errs := make([]error, len(links))
	var wg sync.WaitGroup
	for i, link := range links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = link.Run(ctx)
		}()
	}
	wg.Wait()
	<-popupsDone
	return errors.Join(errs...)
}

// newLink returns the link to host that runs command and reports into queue.
func newLink(host string, command []string, cfg Config, queue *popupQueue, logger *slog.Logger) *Link {
	return &Link{
		Host:    host,
		Command: command,
		Notify:  queue.notify,
		Lost:    queue.lost,
		Clock:   cfg.Clock,
		Logger:  logger,
	}
}

// popup is one popup waiting to be shown.
type popup struct {
	title, body string
	waiting     bool
	quiet       bool
}

// popupQueue shows popups one at a time in the order they were added. Adding never waits, so a link is never held up
// by a popup that is still showing.
type popupQueue struct {
	sound  string
	logger *slog.Logger
	// wake receives a value when a popup is added. Its buffer of one keeps at most one wake pending.
	wake chan struct{}

	mu      sync.Mutex
	pending []popup
}

// add queues p and wakes the runner.
func (q *popupQueue) add(p popup) {
	q.mu.Lock()
	q.pending = append(q.pending, p)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// notify queues the popup for a notice line. A waiting notice is critical, and a quiet notice plays no sound.
func (q *popupQueue) notify(line deskstream.Line) {
	waiting := line.State == string(protocol.StateWaiting)
	q.add(popup{title: line.Title, body: line.Body, waiting: waiting, quiet: line.Quiet})
}

// lost queues the popup for a host whose stream has been down for five minutes.
func (q *popupQueue) lost(host string) {
	q.add(popup{title: lostTitle, body: host + " unreachable for 5 minutes", waiting: true})
}

// run shows queued popups in order until ctx ends. A popup that fails is logged, and the next one still shows.
func (q *popupQueue) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
			}
			continue
		}
		next := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		sound := q.sound
		if next.quiet {
			sound = ""
		}
		if err := Popup(ctx, next.title, next.body, next.waiting, sound); err != nil {
			q.logger.WarnContext(ctx, "show popup", "title", next.title, "error", err)
		}
	}
}
