package store

import (
	"path/filepath"
	"testing"
)

// TestStoreCloseHandlesRefuseCallsAndLeaveNoConnectionOpen opens a connection on each handle, then closes the store.
// Afterwards calls on both the writer and the read pool fail, and neither connector has a connection open.
func TestStoreCloseHandlesRefuseCallsAndLeaveNoConnectionOpen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.db.PingContext(ctx); err != nil {
		t.Fatalf("Ping the writer: %v", err)
	}
	if err := s.read.PingContext(ctx); err != nil {
		t.Fatalf("Ping the read pool: %v", err)
	}
	writer, reads := s.connector.open.Load(), s.readConnector.open.Load()
	t.Logf("before Close: writer connections open %d, read connections open %d", writer, reads)
	if writer == 0 || reads == 0 {
		t.Fatalf("before Close: writer connections open = %d, read connections open = %d, want both open",
			writer, reads)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.db.PingContext(ctx); err == nil {
		t.Error("Ping the writer after Close succeeded, want an error")
	}
	if err := s.read.PingContext(ctx); err == nil {
		t.Error("Ping the read pool after Close succeeded, want an error")
	}
	writer, reads = s.connector.open.Load(), s.readConnector.open.Load()
	t.Logf("after Close: calls on both handles refused; writer connections open %d, read connections open %d",
		writer, reads)
	if writer != 0 {
		t.Errorf("writer connections open after Close = %d, want 0", writer)
	}
	if reads != 0 {
		t.Errorf("read connections open after Close = %d, want 0", reads)
	}
}
