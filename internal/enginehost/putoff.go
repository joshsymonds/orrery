package enginehost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// ErrBadPutOff reports a NotNow or BringBack that names no question it can address: a kind other than headless or pane,
// an empty id, a headless call with an empty request_id, or a pane call with an empty question_hash or a changed time
// that is not positive. Nothing is stored or cleared.
var ErrBadPutOff = errors.New("bad put-off request")

// ErrQuestionNotOpen reports a NotNow or BringBack of a question that is not open now: a request its session does not
// have open, or a pane that is not waiting on that question. The error's text says which.
var ErrQuestionNotOpen = errors.New("question not open")

// ErrNoPane reports a pane call of a pane id that tmux lists no pane with.
var ErrNoPane = errors.New("no such pane")

// ErrPutOffUnavailable reports a NotNow or BringBack the manager cannot serve: it has no PutOffs, or it has no Panes for
// a pane call.
var ErrPutOffUnavailable = errors.New("put-off store unavailable")

// PutOffRequest is a NotNow or BringBack call. A headless call names the open request RequestID of the live session
// ID. A pane call names the pane ID and the question it asks, by QuestionHash and Changed. Kind is store.PutOffHeadless
// or store.PutOffPane.
type PutOffRequest struct {
	Kind, ID, RequestID, QuestionHash string
	Changed                           int64
}

// PutOffStore stores and clears the owner's put-off marks. *store.Store satisfies it.
type PutOffStore interface {
	PutOffQuestion(ctx context.Context, kind, session, question string, at time.Time) error
	BringBackQuestion(ctx context.Context, kind, session, question string) error
}

// PaneQuestion is what tmux reports of one pane: whether its @orrery_state is waiting, the protocol.QuestionHash of its
// @orrery_question, and its @orrery_changed in unix seconds.
type PaneQuestion struct {
	Waiting bool
	Hash    string
	Changed int64
}

// PaneQuestions reads the panes' questions and reports a pane seen to the notice router. PaneQuestion returns an error
// wrapping ErrNoPane when tmux lists no pane with that id. PaneSeen reports the pane seen at seen to the notice router,
// which ends the pane's pending notice as the notifier's Seen ends a session's. A tmux-backed implementation lives in
// cmd/orreryd, which a later change wires in.
type PaneQuestions interface {
	PaneQuestion(ctx context.Context, pane string) (PaneQuestion, error)
	PaneSeen(ctx context.Context, pane string, seen time.Time)
}

// NotNow puts the open question req names off, so it stays off the owner's list until BringBack. The mark is stored at
// the manager's time, and once it is stored the question's pending notice ends through the notice router's seen path: the
// notifier's Seen for a headless session, which is skipped when there is no notifier, or PaneSeen for a pane. A refused
// call stores nothing. The errors are those of putOffMark.
func (m *Manager) NotNow(ctx context.Context, req PutOffRequest) error {
	defer m.calls.enter()()
	kind, question, err := m.putOffMark(ctx, req)
	if err != nil {
		return err
	}
	now := m.now()
	if err = m.putOffs.PutOffQuestion(ctx, kind, req.ID, question, now); err != nil {
		return fmt.Errorf("not now %s: %w", req.ID, err)
	}
	if kind == store.PutOffPane {
		m.panes.PaneSeen(ctx, req.ID, now)
	} else if m.notifier != nil {
		m.notifier.Seen(ctx, req.ID, now)
	}
	return nil
}

// BringBack clears the put-off mark on the open question req names. It calls no notifier, PaneSeen or Notify, so it raises
// nothing. A refused call clears nothing. The errors are those of putOffMark.
func (m *Manager) BringBack(ctx context.Context, req PutOffRequest) error {
	defer m.calls.enter()()
	kind, question, err := m.putOffMark(ctx, req)
	if err != nil {
		return err
	}
	if err = m.putOffs.BringBackQuestion(ctx, kind, req.ID, question); err != nil {
		return fmt.Errorf("bring back %s: %w", req.ID, err)
	}
	return nil
}

// putOffMark checks req names a question that is open now, and returns the kind and question of its mark. It returns an
// error wrapping ErrBadPutOff for a req that names no question, ErrPutOffUnavailable when the manager lacks the store or
// the panes the kind needs, ErrNoSession when a headless session is not live, ErrQuestionNotOpen when the request or the
// pane's question is not open, and ErrNoPane when tmux lists no such pane.
func (m *Manager) putOffMark(ctx context.Context, req PutOffRequest) (kind, question string, err error) {
	if req.ID == "" {
		return "", "", fmt.Errorf("%w: empty id", ErrBadPutOff)
	}
	switch req.Kind {
	case store.PutOffHeadless:
		if req.RequestID == "" {
			return "", "", fmt.Errorf("%w: headless request_id is empty", ErrBadPutOff)
		}
		if m.putOffs == nil {
			return "", "", fmt.Errorf("put off request of %s: %w", req.ID, ErrPutOffUnavailable)
		}
		return m.headlessMark(req)
	case store.PutOffPane:
		if req.QuestionHash == "" {
			return "", "", fmt.Errorf("%w: pane question_hash is empty", ErrBadPutOff)
		}
		if req.Changed <= 0 {
			return "", "", fmt.Errorf("%w: pane changed %d is not a time", ErrBadPutOff, req.Changed)
		}
		if m.putOffs == nil || m.panes == nil {
			return "", "", fmt.Errorf("put off question of pane %s: %w", req.ID, ErrPutOffUnavailable)
		}
		return m.paneMark(ctx, req)
	default:
		return "", "", fmt.Errorf("%w: kind %q is neither %s nor %s",
			ErrBadPutOff, req.Kind, store.PutOffHeadless, store.PutOffPane)
	}
}

// headlessMark returns the mark of req's request in its live session. It returns an error wrapping ErrNoSession when the
// session is not live, and one wrapping ErrQuestionNotOpen when the session has no open request with req's id.
func (m *Manager) headlessMark(req PutOffRequest) (kind, question string, err error) {
	live, err := m.live(req.ID)
	if err != nil {
		return "", "", fmt.Errorf("put off request %s of session %s: %w", req.RequestID, req.ID, err)
	}
	m.mu.Lock()
	_, open := live.requests[req.RequestID]
	m.mu.Unlock()
	if !open {
		return "", "", fmt.Errorf("session %s has no open request %s: %w", req.ID, req.RequestID, ErrQuestionNotOpen)
	}
	return store.PutOffHeadless, req.RequestID, nil
}

// paneMark returns the mark of req's question on its pane. It returns an error wrapping ErrQuestionNotOpen when the pane is
// not waiting, or waits on a question other than req's, and the error of Panes.PaneQuestion when that fails.
func (m *Manager) paneMark(ctx context.Context, req PutOffRequest) (kind, question string, err error) {
	pane, err := m.panes.PaneQuestion(ctx, req.ID)
	if err != nil {
		return "", "", fmt.Errorf("put off question of pane %s: %w", req.ID, err)
	}
	if !pane.Waiting {
		return "", "", fmt.Errorf("pane %s is not waiting: %w", req.ID, ErrQuestionNotOpen)
	}
	if pane.Hash != req.QuestionHash || pane.Changed != req.Changed {
		return "", "", fmt.Errorf("pane %s asks %s now: %w",
			req.ID, store.PaneQuestionKey(pane.Hash, pane.Changed), ErrQuestionNotOpen)
	}
	return store.PutOffPane, store.PaneQuestionKey(req.QuestionHash, req.Changed), nil
}
