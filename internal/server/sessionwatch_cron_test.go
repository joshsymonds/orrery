package server_test

import (
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestCronDeliverPromptLeavesSpeakLabel records an owner interrupt on eventEngine, which labels the pending events
// speak. A cron prompt after it leaves them speak, since it is not the owner's prompt, and an owner prompt after that
// labels them free again.
func TestCronDeliverPromptLeavesSpeakLabel(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addCardEvents(t, s)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), s, nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantPendingEvents(t, stream.next(t), cardPending(cardFree)...)

	applyEngineOps(t, s, store.EngineInterrupt{ID: eventEngine, Turning: false})
	wantPendingEvents(t, stream.nextDelta(t), cardPending(cardSpeak)...)

	applyEngineOps(t, s, store.EnginePrompt{
		ID: eventEngine, Text: "cron words", Sender: store.PromptSenderCron,
	})
	wantPendingEvents(t, stream.nextDelta(t), cardPending(cardSpeak)...)

	applyEngineOps(t, s, store.EnginePrompt{
		ID: eventEngine, Text: "carry on", Sender: store.PromptSenderOwner,
	})
	wantPendingEvents(t, stream.nextDelta(t), cardPending(cardFree)...)
}
