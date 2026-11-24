// Package gateway is the Claude traffic gateway orrery runs. It reads the route registry orrery stores, resolves each
// request to the seat it goes through, forwards the request upstream, and records the request in the ledger.
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/joshsymonds/orrery/internal/gateway/awssig"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/store"
)

// DefaultListen is the address the gateway listens on when it is given none.
const DefaultListen = "127.0.0.1:4110"

// DefaultInterval is how often the gateway re-reads the route registry when its Config names no interval.
const DefaultInterval = 10 * time.Second

// errNotImplemented is returned by each entry point whose body is not written yet.
var errNotImplemented = errors.New("gateway: not implemented yet")

// RoutesReader reads the route registry and the token of each account its contexts name. *store.Reader satisfies it.
type RoutesReader interface {
	// RoutesSnapshot reads the route registry and the tokens of the accounts it names, in one read.
	RoutesSnapshot(ctx context.Context) (store.RoutesSnapshot, error)
}

// Recorder records each request the gateway serves: Start when the request arrives and Finish when it completes.
// *ledger.Ledger satisfies it.
type Recorder interface {
	// Start records the start of a request and returns the id the request is known by.
	Start(ctx context.Context, start ledger.Start) (int64, error)

	// Finish records the completion of the request id.
	Finish(ctx context.Context, id int64, finish ledger.Finish) error
}

// Clock is the time source the gateway reads, so that the re-read loop can be driven without the wall clock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time

	// After returns a channel that receives the time once d has elapsed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the Clock that reads the wall clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// After returns a channel that receives the wall-clock time once d has elapsed.
func (SystemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// Config is what a Gateway is built from.
type Config struct {
	// Routes reads the route registry. It is required.
	Routes RoutesReader

	// Interval is how often Run re-reads the route registry. Zero means DefaultInterval.
	Interval time.Duration

	// Clock is the time source Run waits on. Nil means SystemClock.
	Clock Clock

	// Ledger records each request the gateway serves. It is required.
	Ledger Recorder

	// Logger receives the gateway's warnings. Nil means slog.Default().
	Logger *slog.Logger

	// Client is the HTTP client the gateway makes upstream calls with.
	Client *http.Client

	// AnthropicUpstream is the URL of Anthropic's API. Empty means llmroutes.AnthropicUpstream. It is the only URL
	// that forward seats and account tokens are ever sent to.
	AnthropicUpstream string

	// CallerKeyFile is the path that the PATCHBAY_CALLER_KEY_FILE variable names.
	CallerKeyFile string

	// Getenv looks up an environment variable by name. The gateway uses it to resolve the variable names that a
	// seat's api_key_env_file and model_map_env_file hold.
	Getenv func(string) string

	// AWSCredentials exports the credentials of the named AWS profile, for sigv4 seats.
	AWSCredentials func(ctx context.Context, profile string) (awssig.Credentials, error)

	// BedrockEndpoint returns the base URL of the Bedrock runtime in region. Empty means
	// https://bedrock-runtime.<region>.amazonaws.com.
	BedrockEndpoint func(region string) string
}

// Gateway serves Claude traffic. Build one with New.
type Gateway struct{}

// New returns a Gateway serving cfg. It refuses a Config whose Routes or Ledger is nil.
func New(_ Config) (*Gateway, error) {
	return nil, errNotImplemented
}

// Load reads the route registry once, so that the gateway serves from it before Run starts re-reading it.
func (*Gateway) Load(_ context.Context) error {
	return errNotImplemented
}

// Run re-reads the route registry every interval until ctx ends.
func (*Gateway) Run(_ context.Context) error {
	return errNotImplemented
}

// ServeHTTP serves one request to the gateway.
func (*Gateway) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, errNotImplemented.Error(), http.StatusNotImplemented)
}
