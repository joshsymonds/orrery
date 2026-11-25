package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// paneSourceKind is the source kind of a pane notice. Its data is the daemon.Transition that notify routed, as JSON.
const paneSourceKind = "pane"

// unknownAPIError names the API error of a StopFailure whose error field is empty or absent.
const unknownAPIError = "unknown"

// paneSource is the stored form of a daemon.Transition. It has the transition's fields under the JSON names a stored
// source uses, and it converts to and from the transition field for field. A row stored before a transition carried
// its facts has a frame instead: the cwd, base URL and hook the transition came from, which decodePaneSource reads back
// into the same fields.
type paneSource struct {
	PaneID      string         `json:"paneId"`
	Server      paneServer     `json:"server"`
	From        protocol.State `json:"from"`
	To          protocol.State `json:"to"`
	Cwd         string         `json:"cwd"`
	BaseURL     string         `json:"baseUrl"`
	Transcript  string         `json:"transcript"`
	LastMessage string         `json:"lastMessage"`
	Failed      bool           `json:"failed"`
	APIError    string         `json:"apiError"`
	Frame       *legacyFrame   `json:"frame,omitempty"`
}

// legacyFrame is the frame form of a stored row: the cwd and base URL of the hook frame that a transition came from,
// and the hook it carried.
type legacyFrame struct {
	Cwd              string          `json:"cwd"`
	AnthropicBaseURL string          `json:"anthropicBaseUrl"`
	Hook             json.RawMessage `json:"hook,omitempty"`
}

// paneServer is the stored form of a tmuxctl.Server.
type paneServer struct {
	Name   string `json:"name"`
	Socket string `json:"socket"`
}

// encodePaneSource returns the source data that stores transition's fields.
func encodePaneSource(transition daemon.Transition) ([]byte, error) {
	data, err := json.Marshal(paneSource{
		PaneID:      transition.PaneID,
		Server:      paneServer(transition.Server),
		From:        transition.From,
		To:          transition.To,
		Cwd:         transition.Cwd,
		BaseURL:     transition.BaseURL,
		Transcript:  transition.Transcript,
		LastMessage: transition.LastMessage,
		Failed:      transition.Failed,
		APIError:    transition.APIError,
	})
	if err != nil {
		return nil, fmt.Errorf("%s notice: %w", paneSourceKind, err)
	}
	return data, nil
}

// decodePaneSource returns the transition that source data stores. A legacy row's frame gives the transition its cwd
// and base URL, and its hook gives the rest of its facts. A hook that does not decode is logged, and the facts that do
// decode stay set.
func decodePaneSource(data []byte, logger *slog.Logger) (daemon.Transition, error) {
	var source paneSource
	if err := json.Unmarshal(data, &source); err != nil {
		return daemon.Transition{}, fmt.Errorf("%s notice: %w", paneSourceKind, err)
	}
	transition := daemon.Transition{
		PaneID:      source.PaneID,
		Server:      tmuxctl.Server(source.Server),
		From:        source.From,
		To:          source.To,
		Cwd:         source.Cwd,
		BaseURL:     source.BaseURL,
		Transcript:  source.Transcript,
		LastMessage: source.LastMessage,
		Failed:      source.Failed,
		APIError:    source.APIError,
	}
	if source.Frame != nil {
		transition.Cwd = source.Frame.Cwd
		transition.BaseURL = source.Frame.AnthropicBaseURL
		if err := transition.SetHookFacts(source.Frame.Hook); err != nil {
			logger.Warn("read stored hook facts", "pane", source.PaneID, "error", err)
		}
	}
	return transition, nil
}

// paneNotice is the notice transition makes, with the closures that route it: Watching, Desk and Push, and Judge for a
// done that is not silent and not a StopFailure. notify and rebuildPane both build it, so a restored notice is the
// notice notify built. A StopFailure done names its API error as its desk body and push summary. Any other notice's
// Desk and Push first wait for summarized to close, or for their ctx to end, so a done's body is the summary of the
// turn it ends, written by the Stop's summary run, and not the summary before it. A nil summarized waits for nothing:
// a waiting notice's body is the question its hook wrote, and a restored notice's summary run ended with the orreryd
// that started it.
func (r *relay) paneNotice(transition daemon.Transition, summarized <-chan struct{}, view *doneView) route.Notice {
	silent := ""
	if !push.Wanted(transition.To) {
		silent = silentNotWanted
	}
	failure, failed := stopFailureText(transition)
	incoming := route.Notice{
		Key:      notice.Key{Pane: transition.PaneID, Host: r.orrery},
		State:    string(transition.To),
		Silent:   silent,
		Watching: r.watchingFrom(transition, view),
		Desk: func(ctx context.Context) (deskstream.Notice, error) {
			if !failed {
				awaitSummary(ctx, summarized)
			}
			desk, err := r.deskNotice(ctx, transition)
			if err == nil && failed {
				desk.Body = failure
			}
			return desk, err
		},
		Push: func(ctx context.Context) (push.Notification, error) {
			if !failed {
				awaitSummary(ctx, summarized)
			}
			note, err := r.pushNotification(ctx, transition)
			if err == nil && failed {
				note.Summary = failure
			}
			return note, err
		},
	}
	if transition.To == protocol.StateDone && silent == "" && !failed {
		incoming.Judge = r.judgeDone(transition)
	}
	return incoming
}

// stopFailureText returns the text of a done that a StopFailure hook reported, and whether transition is one. The text
// is "API error: " and the transition's API error, or unknownAPIError when that is empty.
func stopFailureText(transition daemon.Transition) (string, bool) {
	if transition.To != protocol.StateDone || !transition.Failed {
		return "", false
	}
	apiError := transition.APIError
	if apiError == "" {
		apiError = unknownAPIError
	}
	return "API error: " + apiError, true
}

// awaitSummary returns once summarized has closed or ctx has ended. A nil summarized returns at once.
func awaitSummary(ctx context.Context, summarized <-chan struct{}) {
	if summarized == nil {
		return
	}
	select {
	case <-summarized:
	case <-ctx.Done():
	}
}

// rebuildPane is the route.Rebuild of paneSourceKind. It decodes the transition that notify stored, checks the pane
// the notice is about, and builds the notice for the transition. at is the row's recorded time, unix seconds.
func (r *relay) rebuildPane(ctx context.Context, data []byte, at int64) (route.Notice, error) {
	transition, err := decodePaneSource(data, r.logger)
	if err != nil {
		return route.Notice{}, err
	}
	err = checkPane(ctx, transition, at)
	if err != nil {
		return route.Notice{}, err
	}
	return r.paneNotice(transition, nil, nil), nil
}

// checkPane reads transition's pane from its tmux server once. It returns route.ErrChanged when the pane is gone or in
// another state than transition's To, and route.ErrSeen when the pane was seen at or after at. Any other read failure
// is returned as is.
func checkPane(ctx context.Context, transition daemon.Transition, at int64) error {
	client, err := tmuxClient(transition.Server)
	if err != nil {
		return err
	}
	panes, err := client.ListPanes(ctx)
	if err != nil {
		if paneServerNotRunning(err) {
			return fmt.Errorf("pane %s: server not running: %w", transition.PaneID, route.ErrChanged)
		}
		return fmt.Errorf("list panes: %w", err)
	}
	i := slices.IndexFunc(panes, func(pane protocol.Pane) bool { return pane.ID == transition.PaneID })
	if i < 0 {
		return fmt.Errorf("pane %s is gone: %w", transition.PaneID, route.ErrChanged)
	}
	pane := panes[i]
	if pane.State != transition.To {
		return fmt.Errorf("pane %s is %q, not %q: %w", pane.ID, pane.State, transition.To, route.ErrChanged)
	}
	if pane.Seen >= at {
		return fmt.Errorf("pane %s seen at %d: %w", pane.ID, pane.Seen, route.ErrSeen)
	}
	return nil
}

// paneServerNotRunning reports whether err is tmux saying that its server is not running, the messages internal/open
// tests for the same.
func paneServerNotRunning(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to")
}

// watchingFrom returns the Watching check of the transition's notice. Its first call answers from view, the viewing
// check the transition's observer already made, when view is not nil and that check succeeded; every other call checks
// afresh (see relay.watching). A read report within readWindow counts as watching either way.
func (r *relay) watchingFrom(transition daemon.Transition, view *doneView) func(context.Context) (bool, error) {
	var first atomic.Pointer[doneView]
	first.Store(view)
	return func(ctx context.Context) (bool, error) {
		if checked := first.Swap(nil); checked != nil && checked.err == nil {
			return checked.viewing ||
				r.reads.recent(notice.Key{Pane: transition.PaneID, Host: r.orrery}, time.Now()), nil
		}
		return r.watching(ctx, transition)
	}
}
