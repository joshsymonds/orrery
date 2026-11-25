package llmroutes

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

func TestRegistryProtoRoundTrip(t *testing.T) {
	message := fullProto()
	if got := ToProto(FromProto(message)); !proto.Equal(got, message) {
		t.Errorf("ToProto(FromProto(p)) = %v; want %v", got, message)
	}
	reg := fullRegistry()
	if got := FromProto(ToProto(reg)); !reflect.DeepEqual(got, reg) {
		t.Errorf("FromProto(ToProto(r)) = %+v; want %+v", got, reg)
	}
	if got := FromProto(nil); !reflect.DeepEqual(got, Registry{}) {
		t.Errorf("FromProto(nil) = %+v; want the zero Registry", got)
	}
}

// fullRegistry is a registry that sets every field of a seat, and of a context with subagent routing. The second seat
// and context keep the fields that their values leave empty.
func fullRegistry() Registry {
	return Registry{
		DefaultContext: "main",
		Seats:          registrySeats(),
		Contexts:       registryContexts(),
	}
}

func registrySeats() []Seat {
	return []Seat{
		{
			ID: "inj", AuthMode: AuthInject, Upstream: "http://gateway.example.test:8080", Model: "example-model",
			APIKeyEnvFile: "EXAMPLE_API_KEY", AWSProfile: "example-profile", AWSRegion: "us-east-1",
			ModelMapEnvFile: "/etc/example/models.env", Billing: "metered", Speed: "fast", CacheTTL: "1h",
			Insecure: true, StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 4096,
			StripBetas: []string{"example-beta-a", "example-beta-b"},
		},
		{ID: "fwd", AuthMode: AuthForward, Upstream: AnthropicUpstream, MaxInputTokens: 8192},
	}
}

func registryContexts() []Context {
	return []Context{
		{
			ID: "main", DefaultSeat: "fwd", Account: "acct-example",
			Models: map[string]string{"example-model": "inj", "example-sigv4": "fwd"},
			Subagents: &Subagents{
				DefaultSeat: "fwd",
				AgentTypes:  map[string]string{"explore": "inj", "plan": "fwd"},
				Models:      map[string]string{"example-haiku": "inj"},
			},
		},
		{ID: "side", DefaultSeat: "inj", Models: map[string]string{"example-model": "inj"}},
	}
}

// fullProto is the protobuf form of fullRegistry, written field by field.
func fullProto() *orreryv1.RouteRegistry {
	return &orreryv1.RouteRegistry{
		DefaultContext: "main",
		Seats:          protoSeats(),
		Contexts:       protoContexts(),
	}
}

func protoSeats() []*orreryv1.RouteSeat {
	return []*orreryv1.RouteSeat{
		{
			Id: "inj", AuthMode: "inject", Upstream: "http://gateway.example.test:8080", Model: "example-model",
			ApiKeyEnvFile: "EXAMPLE_API_KEY", AwsProfile: "example-profile", AwsRegion: "us-east-1",
			ModelMapEnvFile: "/etc/example/models.env", Billing: "metered", Speed: "fast", CacheTtl: "1h",
			Insecure: true, StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 4096,
			StripBetas: []string{"example-beta-a", "example-beta-b"},
		},
		{Id: "fwd", AuthMode: "forward", Upstream: "https://api.anthropic.com", MaxInputTokens: 8192},
	}
}

func protoContexts() []*orreryv1.RouteContext {
	return []*orreryv1.RouteContext{
		{
			Id: "main", DefaultSeat: "fwd", Account: "acct-example",
			Models: map[string]string{"example-model": "inj", "example-sigv4": "fwd"},
			Subagents: &orreryv1.RouteSubagents{
				DefaultSeat: "fwd",
				AgentTypes:  map[string]string{"explore": "inj", "plan": "fwd"},
				Models:      map[string]string{"example-haiku": "inj"},
			},
		},
		{Id: "side", DefaultSeat: "inj", Models: map[string]string{"example-model": "inj"}},
	}
}
