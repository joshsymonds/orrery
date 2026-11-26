package stepmcp

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic names only. designHostNamed is the orrery name of the host the stored design runs its steps on, and
// designHostAt is the unix second the stored design was created at.
const (
	designHostNamed = "synthetic-host-b"
	designHostAt    = 1700005000
)

// openDesignHostStore opens a migrated store of the test's own, which closes at cleanup.
func openDesignHostStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

// startDesignsHandler serves the real Designs handler over st on a loopback listener, and returns the Designs client
// that dials it. The server stops at cleanup.
func startDesignsHandler(t *testing.T, st *store.Store) enginev1connect.DesignsClient {
	t.Helper()
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cfg := enginehost.ServeConfig{
		Designs: &enginehost.Designs{Store: st, Now: func() time.Time { return time.Unix(designHostAt, 0) }},
	}
	go func() { done <- enginehost.ServeWith(ctx, listener, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	return enginev1connect.NewDesignsClient(http.DefaultClient, "http://"+listener.Addr().String())
}

// TestDesignHostToolsShowHost checks that design_get and design_list, over the real Designs handler, print the host a
// design is stored on.
func TestDesignHostToolsShowHost(t *testing.T) {
	t.Parallel()
	st := openDesignHostStore(t)
	if err := st.CreateDesign(t.Context(), store.Design{
		ID: "design-hosted", Title: "Hosted design", Conversation: "synthetic-session", Host: designHostNamed,
		Created: time.Unix(designHostAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	p := designPeer(t, startDesignsHandler(t, st))

	got := p.tool("design_get", `{"id":"design-hosted"}`)
	listed := p.tool("design_list", `{}`)
	for name, outcome := range map[string]toolOutcome{"design_get": got, "design_list": listed} {
		if outcome.isError {
			t.Fatalf("%s refused: %s", name, outcome.text)
		}
	}

	var getReply v1.GetDesignResponse
	decodeDesignReply(t, got.text, &getReply)
	if host := getReply.GetDesign().GetHost(); host != designHostNamed {
		t.Errorf("design_get host = %q, want %q", host, designHostNamed)
	}
	var listReply v1.ListDesignsResponse
	decodeDesignReply(t, listed.text, &listReply)
	if len(listReply.GetDesigns()) != 1 || listReply.GetDesigns()[0].GetHost() != designHostNamed {
		t.Errorf("design_list reply = %v, want one design on host %q", &listReply, designHostNamed)
	}
}
