// Package gateway is the Claude traffic gateway orrery runs. It reads the route registry orrery stores, resolves each
// request to the seat it goes through, forwards the request upstream, and records the request in the ledger.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/joshsymonds/orrery/internal/gateway/awssig"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// DefaultListen is the address the gateway listens on when it is given none.
const DefaultListen = "127.0.0.1:4110"

// DefaultInterval is how often the gateway re-reads the route registry when its Config names no interval.
const DefaultInterval = 10 * time.Second

// Timeouts for the default upstream client. There is deliberately no overall request timeout: a long thinking turn can
// stream for many minutes, and cutting it short would look the same as an upstream failure.
const (
	dialTimeout           = 10 * time.Second
	handshakeTimeout      = 10 * time.Second
	idleTimeout           = 90 * time.Second
	responseHeaderTimeout = 10 * time.Minute
)

// errNotImplemented is returned by each entry point and seat kind whose body is not written yet.
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
type Gateway struct {
	routes   RoutesReader
	ledger   Recorder
	logger   *slog.Logger
	client   *http.Client
	upstream *url.URL

	// snapshot is the route registry requests are served from. Load sets it. It stays nil, and every request is refused
	// with 503, until a read has succeeded.
	snapshot atomic.Pointer[store.RoutesSnapshot]

	// admission holds one permit for each request being served, so that no more than requestAdmissionCapacity are.
	admission chan struct{}
}

// New returns a Gateway serving cfg. It refuses a Config whose Routes or Ledger is nil, and an AnthropicUpstream that
// is not an absolute http or https URL.
func New(cfg Config) (*Gateway, error) {
	if cfg.Routes == nil {
		return nil, errors.New("gateway: Config.Routes is required")
	}
	if cfg.Ledger == nil {
		return nil, errors.New("gateway: Config.Ledger is required")
	}
	upstream, err := anthropicUpstream(cfg.AnthropicUpstream)
	if err != nil {
		return nil, err
	}

	client := cfg.Client
	if client == nil {
		client = defaultClient()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Gateway{
		routes:    cfg.Routes,
		ledger:    cfg.Ledger,
		logger:    logger,
		client:    client,
		upstream:  upstream,
		admission: make(chan struct{}, requestAdmissionCapacity),
	}, nil
}

// Load reads the route registry once, so that the gateway serves from it before Run starts re-reading it. A read that
// fails leaves the gateway serving whatever it read before, or nothing, and returns the failure.
func (g *Gateway) Load(ctx context.Context) error {
	snap, err := g.routes.RoutesSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("gateway: read the route registry: %w", err)
	}
	g.snapshot.Store(&snap)

	return nil
}

// Run re-reads the route registry every interval until ctx ends.
func (*Gateway) Run(_ context.Context) error {
	return errNotImplemented
}

// anthropicUpstream parses the base URL forward seats send to: configured, or Anthropic's own URL when configured is
// empty. It refuses a URL that is not an absolute http or https URL with a host, and one that carries a query or
// fragment, since a request path is appended to it.
func anthropicUpstream(configured string) (*url.URL, error) {
	if configured == "" {
		configured = llmroutes.AnthropicUpstream
	}
	upstream, err := url.Parse(configured)
	if err != nil {
		return nil, fmt.Errorf("gateway: parse Config.AnthropicUpstream: %w", err)
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" ||
		upstream.RawQuery != "" || upstream.Fragment != "" {
		return nil, fmt.Errorf("gateway: Config.AnthropicUpstream %q is not an absolute http or https URL", configured)
	}

	return upstream, nil
}

// defaultClient is the upstream client used when Config names none. It bounds connection setup and the wait for
// response headers, and it follows no redirect.
func defaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       idleTimeout,
			TLSHandshakeTimeout:   handshakeTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
		},
		CheckRedirect: relayRedirect,
	}
}

// relayRedirect stops the client at the first response, redirect or not, so the caller gets the upstream's own answer.
// Following a redirect would send the caller's credential, and the account token, to whatever host the upstream named.
func relayRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}
