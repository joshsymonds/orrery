package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The accounts, tokens and seats the reload tests serve are invented. The forward seat is alpha, and bravo is a seat
// the gateway does not serve yet, so a request that resolves to bravo is refused rather than sent upstream.
const (
	reloadAccountOne    = "example-account-one"
	reloadAccountTwo    = "example-account-two"
	reloadTokenOne      = "example-token-one"
	reloadTokenTwo      = "example-token-two"
	reloadSeatForward   = "alpha"
	reloadSeatNotServed = "bravo"
	reloadInterval      = 3 * time.Second
	reloadReadFailure   = "example route registry read failure"
	reloadBody          = `{"model":"model-one","max_tokens":16,"messages":[]}`
	reloadMainPath      = "/v1/messages"
	reloadWorkPath      = "/ctx/work/v1/messages"
	reloadClients       = 4
	reloadRequests      = 25
	reloadSwaps         = 10
	reloadErrorTypeAPI  = "api_error"
)

// reloadRegistry is the route registry the reload tests serve. Its main context forwards through alpha, except that
// model-one goes to modelSeat, and its work context forwards through alpha and sends the token of account.
func reloadRegistry(account, modelSeat string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{
			{ID: "alpha", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
			{
				ID: "bravo", AuthMode: llmroutes.AuthInject, Upstream: "https://bravo.example.test",
				Model: "example-model",
			},
		},
		Contexts: []llmroutes.Context{
			{ID: "main", DefaultSeat: "alpha", Models: map[string]string{"model-one": modelSeat}},
			{ID: "work", DefaultSeat: "alpha", Account: account},
		},
	}
}

// reloadSnapshot is the snapshot the reload tests read: reloadRegistry(account, modelSeat) with the tokens of both
// invented accounts.
func reloadSnapshot(account, modelSeat string) store.RoutesSnapshot {
	return store.RoutesSnapshot{
		Routes: reloadRegistry(account, modelSeat),
		Tokens: map[string]string{reloadAccountOne: reloadTokenOne, reloadAccountTwo: reloadTokenTwo},
	}
}

// reloadRoutes is a RoutesReader whose answer the test sets between reads. Each read answers with the snapshot and the
// error that were set last.
type reloadRoutes struct {
	mu       sync.Mutex
	snapshot store.RoutesSnapshot
	err      error
}

// newReloadRoutes returns a reloadRoutes that answers with snapshot.
func newReloadRoutes(snapshot store.RoutesSnapshot) *reloadRoutes {
	return &reloadRoutes{snapshot: snapshot}
}

// set makes the next read answer with snapshot and err.
func (r *reloadRoutes) set(snapshot store.RoutesSnapshot, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.snapshot, r.err = snapshot, err
}

// RoutesSnapshot answers with the snapshot and error that were set last.
func (r *reloadRoutes) RoutesSnapshot(context.Context) (store.RoutesSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.snapshot, r.err
}

// reloadWait is one wait Run has begun: the interval it asked for, and the channel that fires the wait.
type reloadWait struct {
	interval time.Duration
	fire     chan time.Time
}

// reloadClock is the Clock of a reload test. Each wait Run begins is handed to the test on waits, and it ends only when
// the test fires it, so no reload test waits on the wall clock.
type reloadClock struct {
	waits chan reloadWait
}

// newReloadClock returns a reloadClock with no wait begun.
func newReloadClock() *reloadClock {
	return &reloadClock{waits: make(chan reloadWait, 1)}
}

// Now returns the Unix epoch, since a reload test reads no time.
func (*reloadClock) Now() time.Time {
	return time.Unix(0, 0)
}

// After hands the wait to the test and returns the channel the test fires.
func (c *reloadClock) After(d time.Duration) <-chan time.Time {
	fire := make(chan time.Time, 1)
	c.waits <- reloadWait{interval: d, fire: fire}

	return fire
}

// reloadRun is a gateway's Run, started on a reloadClock. The test moves Run one wait at a time: fire ends the wait Run
// is in, and returns once Run is waiting again, so the reload that the fire started has finished.
type reloadRun struct {
	t      *testing.T
	clock  *reloadClock
	cancel context.CancelFunc
	done   chan struct{}
	wait   reloadWait
}

// startReload starts gw's Run on clock and returns once Run is waiting on its first interval. The run ends when the
// test does.
func startReload(t *testing.T, gw *gateway.Gateway, clock *reloadClock) *reloadRun {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	run := &reloadRun{t: t, clock: clock, cancel: cancel, done: make(chan struct{})}
	go func() {
		gw.Run(ctx)
		close(run.done)
	}()
	run.wait = run.next()

	return run
}

// next returns the wait Run begins next. A Run that returns before it waits fails the test.
func (r *reloadRun) next() reloadWait {
	r.t.Helper()

	var wait reloadWait
	select {
	case wait = <-r.clock.waits:
	case <-r.done:
		r.t.Fatal("Run returned before it waited on the clock")
	}

	return wait
}

// fire ends the wait Run is in, and returns once Run is waiting again: the reload the fire started has finished, and
// its swap is visible to the test.
func (r *reloadRun) fire() {
	r.t.Helper()

	r.wait.fire <- r.clock.Now()
	r.wait = r.next()
}

// stop cancels Run's context and returns once Run has returned. A Run that does not return when its context ends hangs
// the test until the test binary's timeout.
func (r *reloadRun) stop() {
	r.cancel()
	<-r.done
}

// serveReload sends one request to gw, a POST of reloadBody to path with the caller's own credential, and returns what
// the gateway answered.
func serveReload(t *testing.T, gw *gateway.Gateway, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := newProxyRequest(t, http.MethodPost, path, reloadBody)
	req.Header.Set("Authorization", callerAuthorization)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)

	return rec
}

// newReloadGateway returns a Gateway that forwards to upstream, re-reads its routes on clock every reloadInterval,
// and has loaded routes.
func newReloadGateway(
	t *testing.T,
	routes gateway.RoutesReader,
	upstream string,
	clock *reloadClock,
	logger *slog.Logger,
) *gateway.Gateway {
	t.Helper()

	gw, err := gateway.New(gateway.Config{
		Routes:            routes,
		Ledger:            fakeRecorder{},
		Clock:             clock,
		Interval:          reloadInterval,
		AnthropicUpstream: upstream,
		Logger:            logger,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if err = gw.Load(t.Context()); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	return gw
}

func TestGatewayReloadSendsChangedAccountToken(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward))
	clock := newReloadClock()
	gw := newReloadGateway(t, routes, fake.url(), clock, discardLogger())
	run := startReload(t, gw, clock)

	serveReload(t, gw, reloadWorkPath)
	routes.set(reloadSnapshot(reloadAccountTwo, reloadSeatForward), nil)
	run.fire()
	serveReload(t, gw, reloadWorkPath)

	seen := fake.requests()
	if len(seen) != 2 {
		t.Fatalf("the fake Anthropic received %d requests; want 2", len(seen))
	}
	if v := seen[0].header.Get("Authorization"); v != "Bearer "+reloadTokenOne {
		t.Errorf("first request Authorization = %q; want the first account's token", v)
	}
	if v := seen[1].header.Get("Authorization"); v != "Bearer "+reloadTokenTwo {
		t.Errorf("second request Authorization = %q; want the changed account's token", v)
	}
}

func TestGatewayReloadRoutesToChangedSeat(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward))
	clock := newReloadClock()
	gw := newReloadGateway(t, routes, fake.url(), clock, discardLogger())
	run := startReload(t, gw, clock)

	serveReload(t, gw, reloadMainPath)
	if got := len(fake.requests()); got != 1 {
		t.Fatalf("the fake Anthropic received %d requests before the reload; want 1", got)
	}

	routes.set(reloadSnapshot(reloadAccountOne, reloadSeatNotServed), nil)
	run.fire()
	rec := serveReload(t, gw, reloadMainPath)

	// bravo is not forwarded yet, so the gateway refuses the request as bravo's, not sending it upstream as alpha's.
	if got := len(fake.requests()); got != 1 {
		t.Errorf("the fake Anthropic received %d requests after the reload; want 1, the new seat is not forwarded", got)
	}
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("request after the reload got %d; want 501 from the new seat %s", rec.Code, reloadSeatNotServed)
	}
	if typ := errorTypeOf(t, rec.Body.Bytes()); typ != reloadErrorTypeAPI {
		t.Errorf("request after the reload got error type %q; want %q", typ, reloadErrorTypeAPI)
	}
}

func TestGatewayReloadKeepsLastRoutesOnFailedRead(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward))
	clock := newReloadClock()
	var logs bytes.Buffer
	gw := newReloadGateway(t, routes, fake.url(), clock, slog.New(slog.NewTextHandler(&logs, nil)))
	run := startReload(t, gw, clock)

	serveReload(t, gw, reloadWorkPath)
	routes.set(reloadSnapshot(reloadAccountTwo, reloadSeatForward), errors.New(reloadReadFailure))
	run.fire()
	serveReload(t, gw, reloadWorkPath)

	seen := fake.requests()
	if len(seen) != 2 {
		t.Fatalf("the fake Anthropic received %d requests; want 2", len(seen))
	}
	if v := seen[1].header.Get("Authorization"); v != "Bearer "+reloadTokenOne {
		t.Errorf("request after the failed read Authorization = %q; want the last routes' token", v)
	}
	logged := logs.String()
	if !strings.Contains(logged, "level=WARN") || !strings.Contains(logged, reloadReadFailure) {
		t.Errorf("log = %q; want a warn carrying the read's error", logged)
	}
	if strings.Contains(logged, reloadTokenOne) || strings.Contains(logged, reloadTokenTwo) {
		t.Errorf("log = %q; want no token in it", logged)
	}

	// A read that succeeds applies again, so one failed read does not stop the re-reads.
	routes.set(reloadSnapshot(reloadAccountTwo, reloadSeatForward), nil)
	run.fire()
	serveReload(t, gw, reloadWorkPath)

	seen = fake.requests()
	if len(seen) != 3 {
		t.Fatalf("the fake Anthropic received %d requests; want 3", len(seen))
	}
	if v := seen[2].header.Get("Authorization"); v != "Bearer "+reloadTokenTwo {
		t.Errorf("request after the successful read Authorization = %q; want the changed account's token", v)
	}
}

func TestGatewayReloadWaitsOnInterval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{name: "configured interval", interval: reloadInterval, want: reloadInterval},
		{name: "default interval", interval: 0, want: gateway.DefaultInterval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clock := newReloadClock()
			gw, err := gateway.New(gateway.Config{
				Routes:   newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward)),
				Ledger:   fakeRecorder{},
				Clock:    clock,
				Interval: tc.interval,
				Logger:   discardLogger(),
			})
			if err != nil {
				t.Fatalf("New() = %v", err)
			}
			run := startReload(t, gw, clock)

			if got := run.wait.interval; got != tc.want {
				t.Errorf("Run waited %v; want %v", got, tc.want)
			}
		})
	}
}

func TestGatewayReloadRefusesNegativeInterval(t *testing.T) {
	t.Parallel()

	_, err := gateway.New(gateway.Config{
		Routes:   newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward)),
		Ledger:   fakeRecorder{},
		Interval: -reloadInterval,
	})
	if err == nil {
		t.Error("New with a negative Interval = nil error; want a refusal")
	}
}

func TestGatewayReloadRunReturnsNilWhenContextEnds(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward))
	clock := newReloadClock()
	gw := newReloadGateway(t, routes, fake.url(), clock, discardLogger())
	run := startReload(t, gw, clock)

	// stop returns only once Run has returned, so reaching the end of the test is the check that Run returns when its
	// context is cancelled.
	run.stop()
}

func TestGatewayReloadSwapsWhileRequestsServe(t *testing.T) {
	t.Parallel()

	fake := newFakeAnthropic(t, replyWith(t, http.StatusOK))
	routes := newReloadRoutes(reloadSnapshot(reloadAccountOne, reloadSeatForward))
	clock := newReloadClock()
	gw := newReloadGateway(t, routes, fake.url(), clock, discardLogger())
	run := startReload(t, gw, clock)

	var wg sync.WaitGroup
	for range reloadClients {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range reloadRequests {
				if rec := serveReload(t, gw, reloadWorkPath); rec.Code != http.StatusOK {
					t.Errorf("request got %d during the swaps; want 200", rec.Code)
				}
			}
		}()
	}

	accounts := []string{reloadAccountOne, reloadAccountTwo}
	for i := range reloadSwaps {
		routes.set(reloadSnapshot(accounts[i%len(accounts)], reloadSeatForward), nil)
		run.fire()
	}
	wg.Wait()

	seen := fake.requests()
	if len(seen) != reloadClients*reloadRequests {
		t.Fatalf("the fake Anthropic received %d requests; want %d", len(seen), reloadClients*reloadRequests)
	}
	for _, req := range seen {
		if v := req.header.Get("Authorization"); v != "Bearer "+reloadTokenOne && v != "Bearer "+reloadTokenTwo {
			t.Errorf("upstream got Authorization %q during the swaps; want one account's token", v)
		}
	}
}
