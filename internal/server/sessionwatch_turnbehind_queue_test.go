package server_test

import (
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// nextChange returns the next message that changes what the client holds: one with items or state, or with an
// unfinished or queued set other than the message before it's. A keepalive repeats the sets of the message before it,
// so it is read past. It has no deadline of its own.
func nextChange(t *testing.T, stream *sessionStream) *orreryv1.SessionsWatchConversationResponse {
	t.Helper()
	for {
		prev := stream.last
		msg := stream.next(t)
		same := len(msg.GetItems()) == 0 && msg.GetState() == nil &&
			slices.EqualFunc(msg.GetUnfinished(), prev.GetUnfinished(), func(a, b *orreryv1.EngineText) bool {
				return proto.Equal(a, b)
			}) &&
			slices.EqualFunc(msg.GetQueued(), prev.GetQueued(), func(a, b *orreryv1.QueuedMessage) bool {
				return proto.Equal(a, b)
			})
		if !same {
			return msg
		}
	}
}

// TestSessionWatchTurnBehindQueuedWaitsTurnWhileWorking sends an owner message that is still queued while the session is
// working. The first delta serves it with waits turn, since Claude takes it at the turn's next tool result.
func TestSessionWatchTurnBehindQueuedWaitsTurnWhileWorking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		ownerMessage("p-1", "w-1", "fix the bug"),
		setPromptState("w-1", store.PromptQueued),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	msg := openSessionStream(t, client, sessionReq(eventEngine, 0, 0)).next(t)
	wantQueued(t, msg.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-1", "fix the bug", store.PromptQueued, "turn"),
	})
}

// TestSessionWatchTurnBehindQueuedWaitsStartWhenTurnEnds sends an owner message that is still queued while the session
// is working, then a move to idle at turnBehindMidAt. The delta after the move serves the message with waits start, since
// no turn runs to take it.
func TestSessionWatchTurnBehindQueuedWaitsStartWhenTurnEnds(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt,
		interruptMarkSetState("working"),
		ownerMessage("p-1", "w-1", "fix the bug"),
		setPromptState("w-1", store.PromptQueued),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkSetState("idle"))
	wantQueued(t, nextChange(t, stream).GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-1", "fix the bug", store.PromptQueued, "start"),
	})
}

// TestSessionWatchTurnBehindSayWhileTurnRunsQueuesAtOnce opens a stream on a working session, then writes a Say as the
// manager writes it: one write holding the owner's engine_prompt, the working state, and the queued state. The first
// delta after that write serves the message with waits turn, whichever poll the write lands in, since the stream reads
// the prompt states after the events. Once its started state is recorded, the next delta no longer serves it.
func TestSessionWatchTurnBehindSayWhileTurnRunsQueuesAtOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkSetState("working"))
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s,
		ownerMessage("p-1", "w-1", "also check the spool"),
		interruptMarkSetState("working"),
		setPromptState("w-1", store.PromptQueued),
	)
	said := nextChange(t, stream)
	wantQueued(t, said.GetQueued(), []*orreryv1.QueuedMessage{
		queuedMessage("p-1", "also check the spool", store.PromptQueued, "turn"),
	})
	if len(said.GetItems()) == 0 {
		// The write landed after the step read the events, so the Say's prompt item comes in the delta after.
		stream.nextDelta(t)
	}

	applyEngineOps(t, s, setPromptState("w-1", store.PromptStarted))
	wantQueued(t, nextChange(t, stream).GetQueued(), nil)
}
