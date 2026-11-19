package server

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// putOffKey names one question by the kind of list row it is on, the session it belongs to and the question.
type putOffKey struct {
	kind, session, question string
}

// putOffMarks is the set of questions the owner has put off. A question with no mark is absent.
type putOffMarks map[putOffKey]struct{}

// has reports whether the question of kind in session has a mark.
func (m putOffMarks) has(kind, session, question string) bool {
	_, ok := m[putOffKey{kind: kind, session: session, question: question}]
	return ok
}

// readPutOffMarks reads every put-off mark through reader into a set. A nil reader has no marks, so every question is
// open. A failed read answers unavailable for store.ErrUnavailable and internal otherwise, with fixed messages.
func readPutOffMarks(ctx context.Context, reader *store.Reader) (putOffMarks, error) {
	marks := putOffMarks{}
	if reader == nil {
		return marks, nil
	}
	rows, err := reader.PutOffQuestions(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read put-off marks")
	}
	for _, row := range rows {
		marks[putOffKey{kind: row.Kind, session: row.Session, question: row.Question}] = struct{}{}
	}
	return marks, nil
}

// headlessPutOff reports whether the question requestID of the headless session id is put off. A question is put off
// only while its session, in state, waits on it, only when requestID is not empty, and only when a headless mark names
// both id and requestID.
func headlessPutOff(marks putOffMarks, id, requestID, state string) bool {
	return state == string(protocol.StateWaiting) && requestID != "" &&
		marks.has(store.PutOffHeadless, id, requestID)
}

// panePutOff reports whether the question of pane is put off. It is put off only while the pane waits, and only when a
// pane mark names the pane and the question it asked at its changed time, by PaneQuestionKey.
func panePutOff(marks putOffMarks, pane *v1.Pane) bool {
	return pane.GetState() == string(protocol.StateWaiting) &&
		marks.has(store.PutOffPane, pane.GetId(), store.PaneQuestionKey(pane.GetQuestionHash(), pane.GetChanged()))
}

// markPanePutOff sets the put_off field of each pane in panes by panePutOff, reading the marks through reader. A store
// that is unavailable has no marks, so no pane is put off, and the listing still answers as it does without a store
// (see turnEnds). Any other failed read is returned.
func markPanePutOff(ctx context.Context, reader *store.Reader, panes []*v1.Pane) error {
	marks, err := readPutOffMarks(ctx, reader)
	if connect.CodeOf(err) == connect.CodeUnavailable {
		marks, err = putOffMarks{}, nil
	}
	if err != nil {
		return err
	}
	for _, pane := range panes {
		pane.PutOff = panePutOff(marks, pane)
	}
	return nil
}
