package store_test

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// paneCallerServer is the tmux server column the pane caller tests sync their rows under.
const paneCallerServer = "-L orrery"

func TestPaneCallerStoreResolvesByHost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	// Pane %3 of one tmux server is a row under host b and another row under host ctl.
	for _, host := range []string{"b", "ctl"} {
		panes := []store.Pane{{ID: host + "-3", Pane: "%3"}}
		if err := s.SyncServer(ctx, host, paneCallerServer, panes, epicCallStart()); err != nil {
			t.Fatalf("SyncServer under host %s: %v", host, err)
		}
	}

	cases := []struct{ host, want string }{
		{host: "b", want: "b-3"},
		{host: "ctl", want: "ctl-3"},
		{host: "c", want: ""},
	}
	for _, c := range cases {
		caller := store.EpicCaller{Host: c.host, Server: paneCallerServer, Pane: "%3"}
		got, found, err := s.CallerSession(ctx, caller)
		if err != nil {
			t.Fatalf("CallerSession by host %s: %v", c.host, err)
		}
		if got != c.want || found != (c.want != "") {
			t.Errorf("CallerSession by host %s = %q, %t, want %q, %t", c.host, got, found, c.want, c.want != "")
		}
	}
}
