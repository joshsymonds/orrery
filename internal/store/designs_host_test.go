package store_test

import (
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// designHostOf returns the host the store holds for the design id. It fails the test on error.
func designHostOf(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	record, err := s.Design(t.Context(), id)
	if err != nil {
		t.Fatalf("Design %s: %v", id, err)
	}
	return record.Host
}

// TestDesignHostUnhostedNamed checks that NameUnhostedDesigns writes the name into the host of each design that reads
// no host and no other: a design another host holds keeps its host, a second call names none, and an empty name is
// refused and writes nothing.
func TestDesignHostUnhostedNamed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openEpicCallStore(t)
	newDesign(t, s, "orrery-8a9b0c1/design-unhosted")
	if err := s.CreateDesign(ctx, store.Design{
		ID: "orrery-8a9b0c1/design-other", Title: "Synthetic design", Conversation: contractWriter, Host: "orrery-b",
		Created: time.Unix(designCreatedAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign with a host: %v", err)
	}

	if _, err := s.NameUnhostedDesigns(ctx, ""); err == nil {
		t.Fatal("NameUnhostedDesigns with an empty name: got nil, want an error")
	}
	if got := designHostOf(t, s, "orrery-8a9b0c1/design-unhosted"); got != "" {
		t.Fatalf("host after the refused name = %q, want none", got)
	}

	named, err := s.NameUnhostedDesigns(ctx, "orrery-a")
	if err != nil {
		t.Fatalf("NameUnhostedDesigns: %v", err)
	}
	if named != 1 {
		t.Fatalf("NameUnhostedDesigns named %d designs, want the 1 unhosted one", named)
	}
	if got := designHostOf(t, s, "orrery-8a9b0c1/design-unhosted"); got != "orrery-a" {
		t.Errorf("host of the unhosted design = %q, want orrery-a", got)
	}
	if got := designHostOf(t, s, "orrery-8a9b0c1/design-other"); got != "orrery-b" {
		t.Errorf("host of the design another host holds = %q, want orrery-b", got)
	}

	named, err = s.NameUnhostedDesigns(ctx, "orrery-c")
	if err != nil {
		t.Fatalf("second NameUnhostedDesigns: %v", err)
	}
	if named != 0 {
		t.Fatalf("second NameUnhostedDesigns named %d designs, want none", named)
	}
}
