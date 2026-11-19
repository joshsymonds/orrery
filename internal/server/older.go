package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// Older answers Sessions.Older with one page of a headless session's main-thread items older than before. The limit is
// checked as Sessions.WatchConversation checks it, and the session is admitted as WatchConversation admits it: an
// engine session outright, and a step session once StepStatus has labeled it. The page holds the newest limit items
// below before, oldest first, and more says whether older items remain.
func (s sessionsServer) Older(
	ctx context.Context,
	req *connect.Request[v1.SessionsOlderRequest],
) (*connect.Response[v1.SessionsOlderResponse], error) {
	if req.Msg.GetLimit() > conversationLimitMax {
		return nil, listError(connect.CodeInvalidArgument, badQueryMessage)
	}
	limit := int(req.Msg.GetLimit())
	if limit == 0 {
		limit = conversationLimitDefault
	}
	if s.reader == nil {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	watch := &sessionWatch{reader: s.reader, steps: s.steps, id: req.Msg.GetId()}
	items, oldest, more, err := watch.older(ctx, req.Msg.GetBefore(), limit)
	if err != nil {
		return nil, err
	}
	resp := &v1.SessionsOlderResponse{Items: items, More: more}
	resp.Oldest = oldest
	return connect.NewResponse(resp), nil
}

// older returns the newest limit main-thread items of the session with a seq below before, oldest first, the seq of the
// oldest of them, and whether items older than them remain. It reads the events backward in pages of eventPageSize. It
// stops at the first item past limit, which tells it that more remain. It leaves out each item that withholds reports,
// as the stream does (see readPrompts), and its items carry their step facts (see withStepFacts).
func (w *sessionWatch) older(
	ctx context.Context,
	before int64,
	limit int,
) ([]*v1.ConversationItem, int64, bool, error) {
	if err := w.readPrompts(ctx); err != nil {
		return nil, 0, false, err
	}
	var newest []keptItem
	cursor := before
	for {
		page, err := w.reader.EngineEventsBefore(ctx, w.id, cursor, eventPageSize)
		if err != nil {
			return nil, 0, false, sessionReadError(err)
		}
		if admitErr := w.admit(ctx, page.Kind); admitErr != nil {
			return nil, 0, false, admitErr
		}
		for _, event := range page.Events {
			item, ok, itemErr := sessionItem(event)
			if itemErr != nil {
				return nil, 0, false, listError(connect.CodeInternal, "read sessions")
			}
			if !ok || w.withholds(item) {
				continue
			}
			if len(newest) == limit {
				return olderAnswer(ctx, w.reader, newest, true)
			}
			newest = append(newest, keptItem{seq: event.Seq, item: item})
		}
		if len(page.Events) < eventPageSize {
			break
		}
		cursor = page.Events[len(page.Events)-1].Seq
	}
	return olderAnswer(ctx, w.reader, newest, false)
}

// olderAnswer returns the page of newest (see olderItems) with more as its more flag, after withStepFacts has set the
// step facts of its items. A failed facts read is answered as a failed session read is.
func olderAnswer(
	ctx context.Context,
	reader stepFactsReader,
	newest []keptItem,
	more bool,
) ([]*v1.ConversationItem, int64, bool, error) {
	items, oldest := olderItems(newest)
	if err := withStepFacts(ctx, reader, items); err != nil {
		return nil, 0, false, sessionReadError(err)
	}
	return items, oldest, more, nil
}

// olderItems returns the items of newest, which are newest first, oldest first, and the seq of the oldest of them, or 0
// when there are none.
func olderItems(newest []keptItem) ([]*v1.ConversationItem, int64) {
	items := make([]*v1.ConversationItem, 0, len(newest))
	for i := len(newest) - 1; i >= 0; i-- {
		items = append(items, newest[i].item)
	}
	if len(newest) == 0 {
		return items, 0
	}
	return items, newest[len(newest)-1].seq
}

// Older answers Panes.Older with one page of a pane's transcript older than the line start before. The pane and the
// limit are checked as WatchConversation checks them. The transcript must be the one the client holds: its id must be
// the base name of the pane's live transcript, or the request fails with failed_precondition. The page holds the newest
// items of the whole lines that end before the line start, oldest first, and more says whether older items remain.
// Unlike Conversation and WatchConversation, Older does not report the pane to the read reporter.
func (s panesServer) Older(
	ctx context.Context,
	req *connect.Request[v1.OlderRequest],
) (*connect.Response[v1.OlderResponse], error) {
	query := url.Values{}
	if req.Msg.GetLimit() != 0 {
		query.Set(queryLimit, strconv.FormatUint(uint64(req.Msg.GetLimit()), 10))
	}
	limit, ok := conversationLimit(query)
	if !ok {
		return nil, connectError(routeError{status: http.StatusBadRequest, message: badQueryMessage})
	}
	if !validPaneID(req.Msg.GetPane()) {
		return nil, connectError(paneRouteError(errMalformedPaneID))
	}
	_, path, err := s.livePane(ctx, req.Msg.GetPane())
	if err != nil {
		return nil, connectError(err)
	}
	if filepath.Base(path) != req.Msg.GetTranscript() {
		return nil, listError(connect.CodeFailedPrecondition, "transcript is not the held transcript")
	}
	page, err := conversation.Older(path, req.Msg.GetBefore(), limit)
	switch {
	case errors.Is(err, conversation.ErrOffset):
		return nil, listError(connect.CodeInvalidArgument, "before is not a line start")
	case err != nil:
		return nil, connectError(transcriptError(err))
	}
	resp := &v1.OlderResponse{Items: conversationItems(page.Items), More: page.More}
	resp.Oldest = page.Oldest
	return connect.NewResponse(resp), nil
}
