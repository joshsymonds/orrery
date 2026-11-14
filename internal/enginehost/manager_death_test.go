package enginehost

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// failedNoticeOf reports whether notices holds a failed notice of session id with the body reason.
func failedNoticeOf(notices []Notice, id, reason string) bool {
	for _, notice := range notices {
		if notice.Session == id && notice.State == "failed" && notice.Body == reason {
			return true
		}
	}
	return false
}

func TestDyingSessionFailsAloneAndResumes(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, 2, fakeClaudePath(t), sink)
	deadDir, liveDir := t.TempDir(), t.TempDir()
	dead, err := m.Start(t.Context(), StartRequest{Dir: deadDir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start of the session that dies: %v", err)
	}
	waitHistory(t, path, dead, "the dying session's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	live, err := m.Start(t.Context(), StartRequest{Dir: liveDir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start of the session that keeps running: %v", err)
	}
	waitHistory(t, path, live, "the other session's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	if err := m.Say(t.Context(), SayRequest{ID: dead, Text: "DIE"}); err != nil {
		t.Fatalf("Say DIE: %v", err)
	}
	history := waitHistory(t, path, dead, "the dead session's failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	reason := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	if !strings.Contains(reason, "exit status 3") || !strings.HasSuffix(reason, "fake died") {
		t.Fatalf("failed reason = %q, want the exit status 3 and a suffix fake died", reason)
	}
	waitUntil(t, "the dead session to leave the live set", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.sessions[dead]
		return !ok
	})
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("engine_text rows after the death = %d, want 0 unfinished text", n)
	}
	waitUntil(t, "the failed notice of the dead session", func() bool {
		return failedNoticeOf(sink.got(), dead, reason)
	})

	if err := m.Say(t.Context(), SayRequest{ID: live, Text: "again"}); err != nil {
		t.Fatalf("Say to the other session: %v", err)
	}
	waitHistory(t, path, live, "the other session's reply to its next prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if state := historyOf(t, path, live).State; state == "failed" {
		t.Fatalf("other session state after the death = %q, want it undisturbed", state)
	}

	if err := m.Say(t.Context(), SayRequest{ID: dead, Text: "again"}); err != nil {
		t.Fatalf("Say to the dead session: %v", err)
	}
	waitHistory(t, path, dead, "the dead session's reply after resume", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	args := fakeArgs(t, deadDir)
	if len(args) != 2 || !strings.Contains(args[1], "--resume session_1") {
		t.Fatalf("fake claude starts for the dead session = %q, want a second start resuming session_1", args)
	}
	want := []string{"prompt: hi", "reply: fake reply 1", "prompt: DIE", "prompt: again", "reply: fake reply 3"}
	if got := conversationOf(t, historyOf(t, path, dead)); !reflect.DeepEqual(got, want) {
		t.Fatalf("conversation of the dead session = %q, want %q", got, want)
	}

	liveNotices := 0
	for _, notice := range sink.got() {
		if notice.Session != live {
			continue
		}
		liveNotices++
		if notice.State != string(protocol.StateDone) {
			t.Fatalf("notice for the other session = %+v, want only done notices", notice)
		}
	}
	if liveNotices != 2 {
		t.Fatalf("notices for the other session = %d, want its two replies", liveNotices)
	}
}
