package stage

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// checkMaker returns the maker a stage call names, as maker gives it, and the id of the sessions row the call's header
// resolves to, which is "" when it resolves to none. The kind decides what the caller must be and what evidence the
// maker carries:
//
//   - owner_serve is the owner through serve's call, so the caller must resolve to no session.
//   - owner_conversation is the owner in the conversation, so the caller must resolve to a session and the maker must
//     quote the owner's words.
//   - conversation is the conversation deciding for the owner, so the caller must resolve to a session and the maker
//     must give the reason and the delegation it rests on.
//
// A caller of the wrong kind is refused as permission denied. A kind that is none of the three, or evidence it lacks,
// is refused as an invalid argument that names the missing field. The returned maker holds only the evidence its kind
// takes.
func (s *Service) checkMaker(
	ctx context.Context, header http.Header, maker *v1.StageMaker,
) (store.StageMaker, string, error) {
	session, _, err := s.cfg.Store.CallerSession(ctx, callerOf(header))
	if err != nil {
		return store.StageMaker{}, "", internalError(fmt.Errorf("resolve caller: %w", err))
	}
	kind := maker.GetKind()
	switch kind {
	case store.StageMakerOwnerServe:
		if session != "" {
			return store.StageMaker{}, "", refuse(connect.CodePermissionDenied,
				"maker: owner_serve is serve's call, which carries no session")
		}
		return store.StageMaker{Kind: kind}, "", nil
	case store.StageMakerOwnerConversation:
		if session == "" {
			return store.StageMaker{}, "", refuse(connect.CodePermissionDenied, "caller: resolves to no session")
		}
		return quotedMaker(maker, session)
	case store.StageMakerConversation:
		if session == "" {
			return store.StageMaker{}, "", refuse(connect.CodePermissionDenied, "caller: resolves to no session")
		}
		return reasonedMaker(maker, session)
	}
	return store.StageMaker{}, "", refuse(connect.CodeInvalidArgument,
		fmt.Sprintf("kind: %q is not owner_serve, owner_conversation or conversation", kind))
}

// quotedMaker returns the owner_conversation maker that quotes the owner's words, made by session. A blank quote is an
// invalid argument that names the quote.
func quotedMaker(maker *v1.StageMaker, session string) (store.StageMaker, string, error) {
	if err := requireField("quote", maker.GetQuote()); err != nil {
		return store.StageMaker{}, "", err
	}
	return store.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: maker.GetQuote()}, session, nil
}

// reasonedMaker returns the conversation maker that gives its reason and the delegation it rests on, made by session. A
// blank reason or delegation is an invalid argument that names the field.
func reasonedMaker(maker *v1.StageMaker, session string) (store.StageMaker, string, error) {
	if err := requireField("reason", maker.GetReason()); err != nil {
		return store.StageMaker{}, "", err
	}
	if err := requireField("delegation", maker.GetDelegation()); err != nil {
		return store.StageMaker{}, "", err
	}
	return store.StageMaker{
		Kind: store.StageMakerConversation, Reason: maker.GetReason(), Delegation: maker.GetDelegation(),
	}, session, nil
}

// refuse returns the error a Stage RPC answers with for a refused call: code, with message as its text.
func refuse(code connect.Code, message string) error {
	return errors.Join(connect.NewError(code, errors.New(message)))
}
