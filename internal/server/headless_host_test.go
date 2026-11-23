package server

import (
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestStartHostListHeadlessCarriesRowHost(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openHeadlessStore(t, path)
	applyHeadless(t, s, time.Now(), store.EngineAdd{
		ID: headlessAlphaID, Dir: "/work/alpha", State: "working", Host: "b",
	})
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	got, err := listHeadlessClient(t, path).ListHeadless(
		t.Context(),
		connect.NewRequest(&orreryv1.ListHeadlessRequest{}),
	)
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	sessions := got.Msg.GetSessions()
	if len(sessions) != 1 {
		t.Fatalf("ListHeadless sessions = %d, want 1", len(sessions))
	}
	if host := sessions[0].GetHost(); host != "b" {
		t.Fatalf("HeadlessSession host = %q, want b", host)
	}
}
