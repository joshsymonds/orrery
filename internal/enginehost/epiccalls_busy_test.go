package enginehost

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// busyEpicRecorder refuses the first refusals records with busy, then records each call it is given.
type busyEpicRecorder struct {
	fakeEpicRecorder
	busy      error
	refusals  int
	refusedAt chan time.Time
}

func (r *busyEpicRecorder) RecordEpicCall(
	ctx context.Context, epic string, caller store.EpicCaller, call string, at time.Time,
) (bool, error) {
	r.mu.Lock()
	refuse := r.refusals > 0
	if refuse {
		r.refusals--
	}
	r.mu.Unlock()
	if refuse {
		r.refusedAt <- at
		return false, r.busy
	}
	return r.fakeEpicRecorder.RecordEpicCall(ctx, epic, caller, call, at)
}

// TestEpicCallRecordRefusedBusyIsTriedAgainBeforeTheCallAnswers records a GetEpic whose record the store refuses as busy
// twice. Each refusal waits epicCallRetry and tries the record again with the call's time, and the call answers once the
// third try records it.
func TestEpicCallRecordRefusedBusyIsTriedAgainBeforeTheCallAnswers(t *testing.T) {
	t.Parallel()
	rec := &busyEpicRecorder{busy: storetest.BusyError(t), refusals: 2, refusedAt: make(chan time.Time, 2)}
	clock := endRetryClock{waits: make(chan time.Duration, 2), tick: make(chan time.Time)}
	st := epicTestStore(t)
	interceptor := EpicCallInterceptor(rec, epicCallAt, clock.after, slog.New(slog.DiscardHandler))
	path, handler := enginev1connect.NewEpicsHandler(
		&epicService{epics: st, steering: fixedSteering{}}, connect.WithInterceptors(interceptor),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := enginev1connect.NewEpicsClient(srv.Client(), srv.URL)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	answered := make(chan error, 1)
	go func() {
		_, err := client.GetEpic(t.Context(), req)
		answered <- err
	}()
	for range 2 {
		if at := <-rec.refusedAt; !at.Equal(epicCallAt()) {
			t.Fatalf("refused record at %v, want the call's time %v", at, epicCallAt())
		}
		if d := <-clock.waits; d != epicCallRetry {
			t.Fatalf("wait before the next record = %v, want %v", d, epicCallRetry)
		}
		select {
		case err := <-answered:
			t.Fatalf("GetEpic answered %v before its record landed", err)
		default:
		}
		clock.tick <- time.Time{}
	}
	if err := <-answered; err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_get", at: epicCallAt(),
	})
}
