// Package llmroutes holds the route registry the gateway serves, which orrery stores. Its seats are the upstreams a
// request may go through, its contexts choose among the seats, and each context names the account whose token its
// forward seats send.
package llmroutes

// AuthMode says how a seat authenticates upstream.
type AuthMode string

const (
	// AuthForward sends the context's account token, or the caller's own login when the context names no account.
	AuthForward AuthMode = "forward"

	// AuthInject sends the API key read from the seat's API key env file.
	AuthInject AuthMode = "inject"

	// AuthSigV4 signs requests with the seat's AWS profile and region.
	AuthSigV4 AuthMode = "sigv4"
)

// AnthropicUpstream is the URL of Anthropic's own API, the upstream a seat reaches when it names none.
const AnthropicUpstream = "https://api.anthropic.com"

// Seat is one seat a request may be sent through: the upstream it reaches, how it authenticates there, and the options
// that shape its requests.
type Seat struct {
	// ID is the seat's id, which the contexts name it by.
	ID string

	// AuthMode is how the seat authenticates upstream.
	AuthMode AuthMode

	// Upstream is the URL the seat's requests go to; empty for the default upstream.
	Upstream string

	// Model is the model the seat sends requests to.
	Model string

	// APIKeyEnvFile is the file the seat reads its API key from, for inject.
	APIKeyEnvFile string

	// AWSProfile is the AWS profile the seat signs with, for sigv4.
	AWSProfile string

	// AWSRegion is the AWS region the seat signs with, for sigv4.
	AWSRegion string

	// ModelMapEnvFile is the file the seat reads its model name mapping from; empty when none is used.
	ModelMapEnvFile string

	// Billing is how the seat's usage is billed, as the gateway names the billing.
	Billing string

	// Speed is the speed the seat asks the upstream for.
	Speed string

	// CacheTTL is the prompt cache lifetime the seat asks the upstream for.
	CacheTTL string

	// Insecure is whether the seat skips checking the upstream's TLS certificate.
	Insecure bool

	// StripCacheControl is whether the seat removes cache control markers from the requests it sends.
	StripCacheControl bool

	// StrictStructuredOutput is whether the seat asks for strict structured output.
	StrictStructuredOutput bool

	// MaxInputTokens is the most input tokens a request sent through the seat may carry; 0 for no limit.
	MaxInputTokens int64

	// StripBetas is the beta feature names the seat removes from a request's beta headers.
	StripBetas []string
}

// Subagents chooses the seat a context's subagents are sent through.
type Subagents struct {
	// DefaultSeat is the seat id a subagent goes through when no entry below names another.
	DefaultSeat string

	// AgentTypes is the seat id by agent type name.
	AgentTypes map[string]string

	// Models is the seat id by model name, for subagents.
	Models map[string]string
}

// Context chooses the seats a request goes through, by the model it names and by the account it sends.
type Context struct {
	// ID is the context's id.
	ID string

	// DefaultSeat is the seat id a request goes through when no entry below names another.
	DefaultSeat string

	// Account is the id of the account whose token the context's forward seats send; empty means the caller's own
	// login.
	Account string

	// Models is the seat id by model name.
	Models map[string]string

	// Subagents is how the context's subagents are sent, or nil when they follow the context's own choices.
	Subagents *Subagents
}

// Registry is the whole route registry: the seats and the contexts that choose among them.
type Registry struct {
	// DefaultContext is the id of the context a request uses when it names none.
	DefaultContext string

	// Seats are the seats, in the order they were given.
	Seats []Seat

	// Contexts are the contexts, in the order they were given.
	Contexts []Context
}
