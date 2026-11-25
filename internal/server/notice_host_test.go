package server_test

import (
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
)

// TestNoticeHostRemoteReads checks that a MarkSeen, a Screen and a Conversation of the pane %3 on host b report nothing
// to the controller: no seen time and no read time is reported for them. So the controller's own %3 keeps its seen time
// and the notice that reads it. Each call answers, so a served call is the one the test sees reported or not.
func TestNoticeHostRemoteReads(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	a := newTmuxServer(t)
	b := newTmuxServer(t)
	aPane, _ := startRoutingPane(t, a, routingController)
	bPane, _ := startRoutingPane(t, b, routingHost)
	setRoutedOptions(t, a, aPane, map[string]string{protocol.OptionSeen: routedSeenBefore})
	setRoutedOptions(t, b, bPane, liveOptions(writeNewestTranscript(t, routedPrompt("b newer prompt"))))

	reports := &reportCalls{}
	agent := startRoutingAgent(t, b)
	client := routingClient(t, a, server.Config{
		Hosts:      []*node.Client{agent},
		ReportSeen: func(pane string, _ int64) error { reports.note("seen " + pane); return nil },
		ReportRead: func(pane string, _ int64) { reports.note("read " + pane) },
	})

	readUpTo := int64(routedSeen)
	if _, err := client.MarkSeen(ctx, connect.NewRequest(&orreryv1.MarkSeenRequest{
		Pane: routingPane, ReadUpTo: &readUpTo, Host: routingHost,
	})); err != nil {
		t.Fatalf("MarkSeen of %s on %s: %v", routingPane, routingHost, err)
	}
	if _, err := client.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{
		Pane: routingPane, Host: routingHost,
	})); err != nil {
		t.Fatalf("Screen of %s on %s: %v", routingPane, routingHost, err)
	}
	if _, err := client.Conversation(ctx, connect.NewRequest(&orreryv1.ConversationRequest{
		Pane: routingPane, Host: routingHost,
	})); err != nil {
		t.Fatalf("Conversation of %s on %s: %v", routingPane, routingHost, err)
	}

	if calls := reports.noted(); len(calls) != 0 {
		t.Fatalf("the controller reports %v for %s on %s, want none", calls, routingPane, routingHost)
	}
	if got := routedOption(t, a, aPane, protocol.OptionSeen); got != routedSeenBefore {
		t.Fatalf("seen of %s on %s = %q, want %q unchanged", aPane, routingController, got, routedSeenBefore)
	}
}
