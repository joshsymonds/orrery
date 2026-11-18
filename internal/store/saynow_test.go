package store

import (
	"path/filepath"
	"testing"
)

// TestSayNowStoreHasNoEngineHeldTable opens a fresh store and checks that it has no engine_held table: a Say is written to
// Claude at once, so the store keeps no held messages.
func TestSayNowStoreHasNoEngineHeldTable(t *testing.T) {
	t.Parallel()
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	var tables int
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'engine_held'`
	if err := s.db.QueryRowContext(t.Context(), query).Scan(&tables); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	if tables != 0 {
		t.Fatalf("engine_held tables in a fresh store = %d, want none", tables)
	}
}
