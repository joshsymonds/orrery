package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/closurejudge"
	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/summary"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// verdictStore writes a closure verdict. *store.Store satisfies it.
type verdictStore interface {
	SetClosureVerdict(ctx context.Context, verdict store.ClosureVerdict, at time.Time) error
}

// paneKeyReader reads a pane's store id and change time, its @orrery_id and @orrery_changed options, from the tmux
// server of a transition.
type paneKeyReader func(ctx context.Context, server tmuxctl.Server, pane string) (id, changed string, err error)

// newClosureJudge returns the judge of closed conversations, which asks claude for each verdict under the per-context
// models.
func newClosureJudge(claude string, models map[string]string) *closurejudge.Judge {
	return closurejudge.New(enginehost.NewHaiku(enginehost.HaikuConfig{Claude: claude, Environ: os.Environ()}), models)
}

// attachVerdicts hands the relay the store its closure verdicts are written to, once the store opens. A nil store
// attaches nothing, so the closure checks store nothing.
func (r *relay) attachVerdicts(st *store.Store) {
	if st == nil {
		return
	}
	var writer verdictStore = st
	r.verdicts.Store(&writer)
}

// checkClosure starts the closure check of a transition to done in its own goroutine, and returns a channel that
// closes when the check ends. Any other transition starts nothing and returns nil.
func (r *relay) checkClosure(ctx context.Context, transition daemon.Transition) <-chan struct{} {
	if transition.To != protocol.StateDone {
		return nil
	}
	done := make(chan struct{})
	r.closing.Go(func() {
		defer close(done)
		r.judgeClosure(ctx, transition)
	})
	return done
}

// judgeClosure asks the closure judge whether the conversation of a done transition is closed, and stores the verdict
// under the pane's id and change time. With no store attached it reads and asks nothing. A pane with no id stores
// nothing without a call. A failed read, call or write is logged and stores nothing. The write ignores ctx's
// cancellation, so a stop that lands after a reply still stores it.
func (r *relay) judgeClosure(ctx context.Context, transition daemon.Transition) {
	writer := r.verdicts.Load()
	if writer == nil {
		return
	}
	id, changed, err := r.closureKey(ctx, transition)
	if err != nil {
		r.logger.ErrorContext(ctx, "read closure pane key", "pane", transition.PaneID, "error", err)
		return
	}
	if id == "" {
		return
	}
	changedAt, err := strconv.ParseInt(changed, 10, 64)
	if err != nil {
		r.logger.ErrorContext(ctx, "parse closure change time", "pane", transition.PaneID, "error", err)
		return
	}
	recent, err := closureRecentText(transition)
	if err != nil {
		r.logger.ErrorContext(ctx, "read closure transcript", "pane", transition.PaneID, "error", err)
		return
	}
	baseURL, env, err := r.closureAccount(transition)
	if err != nil {
		r.logger.ErrorContext(ctx, "closure account", "pane", transition.PaneID, "error", err)
		return
	}
	verdict, err := r.closures.Closed(ctx, baseURL, env, recent)
	if err != nil {
		r.logger.ErrorContext(ctx, "closure judge", "pane", transition.PaneID, "error", err)
		return
	}
	err = (*writer).SetClosureVerdict(context.WithoutCancel(ctx), store.ClosureVerdict{
		Session: id, Changed: changedAt, Closed: verdict.Closed, Why: verdict.Why,
	}, r.clock())
	if err != nil {
		r.logger.ErrorContext(ctx, "store closure verdict", "pane", transition.PaneID, "error", err)
	}
}

// closureKey returns the pane's store id and change time through paneKeys when it is set, and through tmux otherwise.
func (r *relay) closureKey(ctx context.Context, transition daemon.Transition) (string, string, error) {
	if r.paneKeys != nil {
		return r.paneKeys(ctx, transition.Server, transition.PaneID)
	}
	return readPaneKey(ctx, transition.Server, transition.PaneID)
}

// readPaneKey reads pane's @orrery_id and @orrery_changed options on its tmux server, in one tmux invocation.
func readPaneKey(ctx context.Context, server tmuxctl.Server, pane string) (string, string, error) {
	client, err := tmuxClient(server)
	if err != nil {
		return "", "", err
	}
	values, err := client.PaneValues(ctx, pane, protocol.OptionID, protocol.OptionChanged)
	if err != nil {
		return "", "", fmt.Errorf("read pane %s key: %w", pane, err)
	}
	return values[0], values[1], nil
}

// closureRecentText returns the recent text of the transition's transcript, through the Stop hook's last assistant
// message, as the summarizer reads it.
func closureRecentText(transition daemon.Transition) (string, error) {
	transcript, err := hookString(transition.Frame.Hook, keyTranscriptPath)
	if err != nil {
		return "", fmt.Errorf("read transcript path: %w", err)
	}
	last, err := hookString(transition.Frame.Hook, keyLastAssistantMessage)
	if err != nil {
		return "", fmt.Errorf("read last assistant message: %w", err)
	}
	excerpt, err := summary.ReadExcerpt(transcript, last)
	if err != nil {
		return "", fmt.Errorf("read transcript excerpt: %w", err)
	}
	return excerpt.Text, nil
}

// closureAccount returns the base URL and env the closure call runs under, resolved as the done judge resolves its
// account: the context stamp in effect for the transition's cwd under the relay's home, or else the frame's base URL,
// with that URL as the env. A stamp that cannot be read is an error, and no call is made.
func (r *relay) closureAccount(transition daemon.Transition) (string, map[string]string, error) {
	found, err := stamp.Find(r.home, transition.Frame.Cwd)
	if err != nil {
		return "", nil, fmt.Errorf("find context stamp: %w", err)
	}
	if found.Folder != "" {
		return found.BaseURL(), found.Env, nil
	}
	baseURL := transition.Frame.AnthropicBaseURL
	if baseURL == "" {
		return "", nil, nil
	}
	return baseURL, map[string]string{envAnthropicBaseURL: baseURL}, nil
}
