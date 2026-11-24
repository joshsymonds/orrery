package stage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// requireWorkingConversation refuses a write to design by a caller that resolves to session, when session is not the
// design's working conversation. The refusal is permission denied, and its message names the working conversation. A
// caller that resolves to no session, the owner through serve, passes, and so does a write to no design, since a new
// draft with no design is not guarded. A design no record holds passes too: its write is then refused as not found.
func (s *Service) requireWorkingConversation(ctx context.Context, session, design string) error {
	if session == "" || design == "" {
		return nil
	}
	working, err := s.cfg.Store.DesignConversation(ctx, design)
	if errors.Is(err, store.ErrNoDesign) {
		return nil
	}
	if err != nil {
		return internalError(fmt.Errorf("read working conversation of design %s: %w", design, err))
	}
	if session == working {
		return nil
	}
	return refuse(connect.CodePermissionDenied, fmt.Sprintf(
		"caller: resolves to session %s, not the working conversation %s of design %s", session, working, design,
	))
}

// requireContractConversation refuses a write to contract by a caller that resolves to session, unless session is the
// working conversation of the design the contract is in (see contractDesignFor). A caller that resolves to no session
// passes without a lookup.
func (s *Service) requireContractConversation(ctx context.Context, session, contract, requested string) error {
	if session == "" {
		return nil
	}
	design, err := s.contractDesignFor(ctx, contract, requested)
	if err != nil {
		return err
	}
	return s.requireWorkingConversation(ctx, session, design)
}

// contractDesignFor returns the design a write to contract is guarded by: the design that holds the contract, which is
// the contract itself when it is in no design, since a contract in no design reads as a design of one. A draft is held
// by its design, which is "" for none. A contract no one holds is a new draft, which joins requested.
func (s *Service) contractDesignFor(ctx context.Context, contract, requested string) (string, error) {
	designs, err := s.cfg.Store.Designs(ctx)
	if err != nil {
		return "", internalError(fmt.Errorf("list designs: %w", err))
	}
	for _, record := range designs {
		if designHolds(record, contract) {
			return record.ID, nil
		}
	}
	draft, err := s.cfg.Store.ContractDraft(ctx, contract)
	if errors.Is(err, store.ErrNoDraft) {
		return requested, nil
	}
	if err != nil {
		return "", internalError(fmt.Errorf("read draft %s: %w", contract, err))
	}
	return draft.Design, nil
}

// ContinueDesign starts a session that continues the design's conversation, and makes it the design's working
// conversation. The session starts in the directory of the working conversation's sessions row, with a prompt that
// names the design, its title, the conversation it continues and the design's log. A failed start stores nothing. A
// failed store after the start stops the started session, even when ctx has ended, and refuses the continue. The design
// must exist.
func (s *Service) ContinueDesign(
	ctx context.Context,
	req *connect.Request[v1.ContinueDesignRequest],
) (*connect.Response[v1.ContinueDesignResponse], error) {
	design := req.Msg.GetDesign()
	if err := requireField("design", design); err != nil {
		return nil, err
	}
	record, err := s.readDesign(ctx, design)
	if err != nil {
		return nil, err
	}
	if s.cfg.Sessions == nil {
		return nil, refuse(connect.CodeUnavailable, "sessions: no session starter")
	}
	dir, err := s.conversationDir(ctx, record.Conversation)
	if err != nil {
		return nil, err
	}
	lines, err := s.cfg.Store.DesignLog(ctx, design)
	if err != nil {
		return nil, internalError(fmt.Errorf("read log of design %s: %w", design, err))
	}
	session, err := s.cfg.Sessions.Start(ctx, enginehost.StartRequest{
		Dir: dir, Prompt: continuePrompt(record, lines),
	})
	if err != nil {
		return nil, refuse(connect.CodeUnavailable, fmt.Sprintf("start session of design %s: %v", design, err))
	}
	if err = s.cfg.Store.ContinueDesign(ctx, design, session, s.cfg.Now()); err != nil {
		return nil, s.stopStarted(ctx, session, internalError(fmt.Errorf("continue design %s: %w", design, err)))
	}
	return connect.NewResponse(&v1.ContinueDesignResponse{Design: design, Session: session}), nil
}

// conversationDir returns the directory of the sessions row conversation names. A conversation with no sessions row
// has no directory to continue in, and is refused as a failed precondition.
func (s *Service) conversationDir(ctx context.Context, conversation string) (string, error) {
	row, err := s.cfg.Store.EngineSession(ctx, conversation)
	if errors.Is(err, store.ErrNoEngineSession) {
		return "", refuse(connect.CodeFailedPrecondition,
			fmt.Sprintf("conversation: session %s has no sessions row", conversation))
	}
	if err != nil {
		return "", internalError(fmt.Errorf("read session %s: %w", conversation, err))
	}
	return row.Dir, nil
}

// stopStarted stops the session a continue started when the continue's store write fails, even when ctx has ended. It
// returns refusal, joined with the stop's error when the stop fails.
func (s *Service) stopStarted(ctx context.Context, started string, refusal error) error {
	if err := s.cfg.Sessions.Stop(context.WithoutCancel(ctx), started); err != nil {
		return errors.Join(refusal, fmt.Errorf("stop session %s: %w", started, err))
	}
	return refusal
}

// continuePrompt returns the prompt a continued session starts with: the design's id and title, the conversation it
// continues, and each line of the design's log, oldest first.
func continuePrompt(record store.DesignRecord, lines []store.DesignLogLine) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"You continue the contract stage of design %s, titled %q. The conversation it continues is session %s.\n",
		record.ID, record.Title, record.Conversation)
	b.WriteString("The design's log so far, oldest first:\n")
	for _, line := range lines {
		fmt.Fprintf(&b, "%d. %s %s %s [%s] %s\n", line.Seq, time.Unix(line.At, 0).UTC().Format(time.RFC3339),
			line.Maker, line.What, strings.Join(line.IDs, ", "), line.Words)
	}
	return b.String()
}
