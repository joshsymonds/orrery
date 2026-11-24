package server_test

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestDesignLogRecordsLogServedInSeqOrder checks that Designs.Get carries the design's log lines in seq order, each
// with its time, maker, session, what, ids and words, and that a line of another design does not show among them.
func TestDesignLogRecordsLogServedInSeqOrder(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignOfTwo(t, st)
	seedDesignOfOne(t, st)
	ctx := t.Context()
	lines := []store.DesignLogLine{
		{
			Design: designTwoID, At: designTime, Maker: store.LogMakerConversation, Session: designSession,
			What: "proposed", IDs: []string{designFirstID}, Words: "Synthetic proposal.",
		},
		{
			Design: designTwoID, At: designTime + 60, Maker: store.LogMakerOwner,
			What: "accepted", IDs: []string{designFirstID}, Words: "Synthetic accept.",
		},
		{
			Design: designLoneID, At: designTime, Maker: store.LogMakerOwner,
			What: "set aside", IDs: []string{designLoneID}, Words: "Synthetic other design.",
		},
	}
	for _, line := range lines {
		if err := st.AppendDesignLog(ctx, line); err != nil {
			t.Fatalf("AppendDesignLog %s: %v", line.What, err)
		}
	}
	client := designsClient(t, contractsHandler(t, store.NewReader(path), nil))

	get, err := client.Get(ctx, connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designTwoID}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantGet := &orreryv1.DesignsGetResponse{
		Design: wantDesignOfTwo(),
		Log: []*orreryv1.DesignLogLine{
			{
				Seq: 1, Time: designTime, Maker: &orreryv1.RecordMaker{Kind: store.LogMakerConversation},
				Session: designSession, What: "proposed", Ids: []string{designFirstID}, Words: "Synthetic proposal.",
			},
			{
				Seq: 2, Time: designTime + 60, Maker: &orreryv1.RecordMaker{Kind: store.LogMakerOwner},
				What: "accepted", Ids: []string{designFirstID}, Words: "Synthetic accept.",
			},
		},
	}
	if !proto.Equal(get.Msg, wantGet) {
		t.Fatalf("Get = %v, want %v", get.Msg, wantGet)
	}
}
