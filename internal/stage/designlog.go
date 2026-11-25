package stage

import (
	"context"
	"fmt"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// loggedCall is what a Stage call logs: what it did, the ids it touched and the words it was given. Maker is the maker
// the call names, the zero StageMaker when it names none. Session is the sessions row id of the caller, "" when the
// caller resolves to none.
type loggedCall struct {
	Maker   store.StageMaker
	Session string
	What    string
	IDs     []string
	Words   string
}

// designLogLine returns the line of design's log that call logs, stamped at unix seconds at. Its maker is owner when
// the call's maker is owner_serve or owner_conversation, or when the caller resolves to no session, and conversation
// otherwise. Its session is the caller's, and its words are the maker's reason when the maker is conversation, and the
// call's words otherwise.
func designLogLine(design string, at int64, call loggedCall) store.DesignLogLine {
	maker := store.LogMakerConversation
	if call.Session == "" || call.Maker.Kind == store.StageMakerOwnerServe ||
		call.Maker.Kind == store.StageMakerOwnerConversation {
		maker = store.LogMakerOwner
	}
	words := call.Words
	if call.Maker.Kind == store.StageMakerConversation {
		words = call.Maker.Reason
	}
	return store.DesignLogLine{
		Design: design, At: at, Maker: maker, Session: call.Session, What: call.What, IDs: call.IDs, Words: words,
	}
}

// logCall appends the line call logs to the design log of design, stamped with the time now. It is called once the
// call's record is stored. A failed append is an internal error, as the store's other failed writes are.
func (s *Service) logCall(ctx context.Context, design string, call loggedCall) error {
	line := designLogLine(design, s.cfg.Now().Unix(), call)
	if err := s.cfg.Store.AppendDesignLog(ctx, line); err != nil {
		return internalError(fmt.Errorf("log %s of design %s: %w", call.What, design, err))
	}
	return nil
}

// callerSession returns the id of the sessions row caller names, "" when it names none. A failed read is an internal
// error.
func (s *Service) callerSession(ctx context.Context, caller store.EpicCaller) (string, error) {
	session, _, err := s.cfg.Store.CallerSession(ctx, caller)
	if err != nil {
		return "", internalError(fmt.Errorf("resolve caller: %w", err))
	}
	return session, nil
}

// questionID returns the id of the question numbered seq within its design, which is Q followed by seq.
func questionID(seq int64) string {
	return fmt.Sprintf("Q%d", seq)
}

// stageLogLineOf returns the StageLogLine of line as the design log holds it. Its maker is the line's maker kind.
func stageLogLineOf(line store.DesignLogLine) *v1.StageLogLine {
	return &v1.StageLogLine{
		Seq: line.Seq, At: line.At, Maker: &v1.StageMaker{Kind: line.Maker}, Session: line.Session,
		What: line.What, Ids: line.IDs, Words: line.Words,
	}
}
