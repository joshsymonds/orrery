package llmroutes

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// The grammars and vocabulary a registry is held to.
const (
	idGrammar  = "^[a-z0-9][a-z0-9-]*$"
	envGrammar = "^[A-Za-z_][A-Za-z0-9_]*$"

	claudeMarker = "claude"

	billingSubscription = "subscription"
	billingMetered      = "metered"
	billingSelfHosted   = "self_hosted"

	speedFast    = "fast"
	cacheTTL1h   = "1h"
	loopbackHost = "127.0.0.1"
	localHost    = "localhost"

	// maxAgentTypeBytes is the longest agent type key, in bytes.
	maxAgentTypeBytes = 256

	// firstAgentTypeByte and lastAgentTypeByte bound the bytes an agent type key may hold: printable ASCII, no space.
	firstAgentTypeByte = 0x21
	lastAgentTypeByte  = 0x7e
)

var (
	idPattern      = regexp.MustCompile(idGrammar)
	envPattern     = regexp.MustCompile(envGrammar)
	regionPattern  = regexp.MustCompile(`^[a-z0-9-]*$`)
	profilePattern = regexp.MustCompile(`^[a-z0-9._-]*$`)
)

// Validate reports whether reg is a route registry the gateway admits, given the ids of the accounts that exist. It
// returns nil for a valid registry. Otherwise it returns an error naming the field and the seat or context id that
// breaks a rule. The error never carries the value of a field holding a credential, and never quotes an upstream URL,
// which may carry user information.
func Validate(reg Registry, accounts []string) error {
	seats, err := seatIndex(reg.Seats)
	if err != nil {
		return err
	}
	contextIDs, err := contextIndex(reg.Contexts)
	if err != nil {
		return err
	}
	// Every context id is held to the grammar above, so a default that names a context is well formed too.
	if _, ok := contextIDs[reg.DefaultContext]; !ok {
		return fmt.Errorf("default_context %q does not name a context", reg.DefaultContext)
	}
	for _, ctx := range reg.Contexts {
		if err = validateContext(ctx, seats, accounts); err != nil {
			return fmt.Errorf("context %q: %w", ctx.ID, err)
		}
	}
	return nil
}

// seatIndex holds every seat to its rules and indexes it by id. It refuses an id that is malformed or defined twice.
func seatIndex(seats []Seat) (map[string]Seat, error) {
	index := make(map[string]Seat, len(seats))
	for _, seat := range seats {
		if !idPattern.MatchString(seat.ID) {
			return nil, fmt.Errorf("seat %q: id must match %s", seat.ID, idGrammar)
		}
		if _, dup := index[seat.ID]; dup {
			return nil, fmt.Errorf("seat %q: id is defined more than once", seat.ID)
		}
		if err := validateSeat(seat); err != nil {
			return nil, fmt.Errorf("seat %q: %w", seat.ID, err)
		}
		index[seat.ID] = seat
	}
	return index, nil
}

// contextIndex checks every context id and returns the set of ids. It refuses an id that is malformed or defined twice.
func contextIndex(contexts []Context) (map[string]struct{}, error) {
	ids := make(map[string]struct{}, len(contexts))
	for _, ctx := range contexts {
		if !idPattern.MatchString(ctx.ID) {
			return nil, fmt.Errorf("context %q: id must match %s", ctx.ID, idGrammar)
		}
		if _, dup := ids[ctx.ID]; dup {
			return nil, fmt.Errorf("context %q: id is defined more than once", ctx.ID)
		}
		ids[ctx.ID] = struct{}{}
	}
	return ids, nil
}

// validateSeat holds a seat to the rules of its auth mode, then to the rules every mode shares.
func validateSeat(seat Seat) error {
	var err error
	switch seat.AuthMode {
	case AuthForward:
		err = validateForward(seat)
	case AuthInject:
		err = validateInject(seat)
	case AuthSigV4:
		err = validateSigV4(seat)
	default:
		err = fmt.Errorf("auth_mode %q must be forward, inject or sigv4", seat.AuthMode)
	}
	if err != nil {
		return err
	}
	return validateTuning(seat)
}

func validateForward(seat Seat) error {
	if err := validateUpstreamURL(seat.Upstream, seat.Insecure); err != nil {
		return err
	}
	if seat.Upstream != AnthropicUpstream {
		return errors.New("upstream must be the Anthropic upstream for a forward seat")
	}
	if seat.APIKeyEnvFile != "" {
		return errors.New("forward seats must not set api_key_env_file")
	}
	if seat.Billing != "" {
		return errors.New("forward seats must not set billing")
	}
	return validateNoAWSFields(seat)
}

func validateInject(seat Seat) error {
	if err := validateUpstreamURL(seat.Upstream, seat.Insecure); err != nil {
		return err
	}
	if !envPattern.MatchString(seat.APIKeyEnvFile) {
		return fmt.Errorf("api_key_env_file must match %s", envGrammar)
	}
	if mentionsClaude(seat.Model) {
		return errors.New(`model must not contain "claude"`)
	}
	if err := validateNoAWSFields(seat); err != nil {
		return err
	}
	switch seat.Billing {
	case billingSubscription, billingMetered, billingSelfHosted:
		return nil
	default:
		return fmt.Errorf("billing %q must be subscription, metered or self_hosted", seat.Billing)
	}
}

func validateSigV4(seat Seat) error {
	if seat.Upstream != "" {
		return errors.New("sigv4 seats must not set upstream")
	}
	if seat.APIKeyEnvFile != "" {
		return errors.New("sigv4 seats must not set api_key_env_file")
	}
	if seat.Billing != "" {
		return errors.New("sigv4 seats must not set billing")
	}
	if seat.Insecure {
		return errors.New("sigv4 seats must not set insecure")
	}
	if strings.TrimSpace(seat.AWSProfile) == "" {
		return errors.New("sigv4 seats require aws_profile")
	}
	if strings.TrimSpace(seat.AWSRegion) == "" {
		return errors.New("sigv4 seats require aws_region")
	}
	if strings.TrimSpace(seat.ModelMapEnvFile) == "" {
		return errors.New("sigv4 seats require model_map_env_file")
	}
	if !regionPattern.MatchString(seat.AWSRegion) {
		return errors.New("aws_region must use only lowercase letters, digits and dashes")
	}
	if !profilePattern.MatchString(seat.AWSProfile) {
		return errors.New("aws_profile must use only lowercase letters, digits, dashes, dots and underscores")
	}
	return nil
}

// validateNoAWSFields refuses the AWS fields on a seat that does not sign with AWS.
func validateNoAWSFields(seat Seat) error {
	if seat.AWSProfile != "" {
		return fmt.Errorf("%s seats must not set aws_profile", seat.AuthMode)
	}
	if seat.AWSRegion != "" {
		return fmt.Errorf("%s seats must not set aws_region", seat.AuthMode)
	}
	if seat.ModelMapEnvFile != "" {
		return fmt.Errorf("%s seats must not set model_map_env_file", seat.AuthMode)
	}
	return nil
}

// validateTuning holds the settings every auth mode shares to the rules that tie them to a mode.
func validateTuning(seat Seat) error {
	if seat.Speed != "" && seat.Speed != speedFast {
		return fmt.Errorf("speed %q must be empty or fast", seat.Speed)
	}
	if seat.Speed != "" && seat.AuthMode == AuthSigV4 {
		return errors.New("speed must not be set on a sigv4 seat")
	}
	if len(seat.StripBetas) > 0 && seat.AuthMode != AuthSigV4 {
		return fmt.Errorf("strip_betas must not be set on %s seats", seat.AuthMode)
	}
	if seat.CacheTTL != "" {
		if err := validateCacheTTL(seat); err != nil {
			return err
		}
	}
	if seat.MaxInputTokens < 0 {
		return errors.New("max_input_tokens must not be negative")
	}
	return nil
}

func validateCacheTTL(seat Seat) error {
	if seat.CacheTTL != cacheTTL1h {
		return fmt.Errorf("cache_ttl %q must be empty or 1h", seat.CacheTTL)
	}
	if seat.AuthMode != AuthForward {
		return fmt.Errorf("cache_ttl must not be set on %s seats", seat.AuthMode)
	}
	if seat.StripCacheControl {
		return errors.New("cache_ttl must not be set with strip_cache_control")
	}
	return nil
}

// validateUpstreamURL holds an upstream to patchbay's rules: an absolute URL with a scheme and host, no user
// information, query or fragment, and https, or http only for loopback or with insecure.
func validateUpstreamURL(raw string, insecure bool) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("upstream is not a valid URL")
	}
	switch {
	case parsed.User != nil:
		return errors.New("upstream must not contain user information")
	case parsed.RawQuery != "":
		return errors.New("upstream must not contain query parameters")
	case parsed.Fragment != "":
		return errors.New("upstream must not contain a fragment")
	case !parsed.IsAbs() || parsed.Host == "":
		return errors.New("upstream must be an absolute URL with a scheme and host")
	}
	switch parsed.Scheme {
	case "https":
		if insecure {
			return errors.New("insecure must not be set with an https upstream")
		}
		return nil
	case "http":
		return validateHTTPUpstream(parsed.Hostname(), insecure)
	default:
		return errors.New("upstream must use https")
	}
}

func validateHTTPUpstream(host string, insecure bool) error {
	loopback := host == loopbackHost || host == localHost
	switch {
	case loopback && insecure:
		return errors.New("insecure must not be set with a loopback upstream")
	case !loopback && !insecure:
		return errors.New("http upstreams must be loopback, or set insecure")
	}
	return nil
}

// validateContext holds a context to its routing rules and to the accounts it may name.
func validateContext(ctx Context, seats map[string]Seat, accounts []string) error {
	if _, ok := seats[ctx.DefaultSeat]; !ok {
		return fmt.Errorf("default_seat %q does not name a seat", ctx.DefaultSeat)
	}
	for _, selector := range slices.Sorted(maps.Keys(ctx.Models)) {
		if err := validateModelSelector(selector, ctx.Models[selector], seats); err != nil {
			return err
		}
	}
	if ctx.Account != "" && !slices.Contains(accounts, ctx.Account) {
		return fmt.Errorf("account %q is not one of the accounts", ctx.Account)
	}
	if ctx.Subagents == nil {
		return nil
	}
	return validateSubagents(*ctx.Subagents, seats)
}

// validateModelSelector holds one model selector of a context to its rules: it is non-empty, names a seat, and does
// not bind an inject seat to a Claude model.
func validateModelSelector(selector, seatID string, seats map[string]Seat) error {
	if selector == "" {
		return errors.New("models selector must not be empty")
	}
	seat, ok := seats[seatID]
	if !ok {
		return fmt.Errorf("models selector %q names unknown seat %q", selector, seatID)
	}
	if seat.AuthMode == AuthInject && mentionsClaude(selector) {
		return fmt.Errorf("models selector %q must not bind inject seat %q", selector, seatID)
	}
	return nil
}

// validateSubagents holds the subagent routing of a context to its rules: every destination names a seat, and every
// agent type key is a value the agent type header could carry.
func validateSubagents(sub Subagents, seats map[string]Seat) error {
	if err := validateDestination(seats, sub.DefaultSeat, "subagents default_seat"); err != nil {
		return err
	}
	for _, agentType := range slices.Sorted(maps.Keys(sub.AgentTypes)) {
		if !validAgentType(agentType) {
			return fmt.Errorf(
				"subagents agent type %q must be 1 to %d bytes from 0x21 to 0x7e", agentType, maxAgentTypeBytes)
		}
		what := fmt.Sprintf("subagents agent type %q", agentType)
		if err := validateDestination(seats, sub.AgentTypes[agentType], what); err != nil {
			return err
		}
	}
	for _, selector := range slices.Sorted(maps.Keys(sub.Models)) {
		if selector == "" {
			return errors.New("subagents models selector must not be empty")
		}
		what := fmt.Sprintf("subagents model %q", selector)
		if err := validateDestination(seats, sub.Models[selector], what); err != nil {
			return err
		}
	}
	return nil
}

// validateDestination holds a subagent destination to the rules of the seat it names: the seat exists, and an inject
// seat has a model to rewrite to. what names the destination in the error.
func validateDestination(seats map[string]Seat, seatID, what string) error {
	seat, ok := seats[seatID]
	if !ok {
		return fmt.Errorf("%s names unknown seat %q", what, seatID)
	}
	if seat.AuthMode == AuthInject && seat.Model == "" {
		return fmt.Errorf("%s names inject seat %q, which needs a model", what, seatID)
	}
	return nil
}

// validAgentType reports whether key is 1 to 256 bytes, each in the printable ASCII range without the space.
func validAgentType(key string) bool {
	if key == "" || len(key) > maxAgentTypeBytes {
		return false
	}
	for index := range len(key) {
		if key[index] < firstAgentTypeByte || key[index] > lastAgentTypeByte {
			return false
		}
	}
	return true
}

// mentionsClaude reports whether value names a Claude model, in any case.
func mentionsClaude(value string) bool {
	return strings.Contains(strings.ToLower(value), claudeMarker)
}
