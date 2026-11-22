package llmroutes

import (
	"slices"
	"strings"
	"testing"
)

// tooLongAgentType is one byte longer than an agent type key may be.
const tooLongAgentType = 257

// exampleAccounts are the accounts the registries in these tests may name.
func exampleAccounts() []string {
	return []string{"acct-example"}
}

// validRegistry is a registry Validate accepts: a seat of each auth mode, and a context of each kind of routing. Each
// refusal case breaks it in one way.
func validRegistry() Registry {
	return Registry{
		DefaultContext: "main",
		Seats: []Seat{
			{ID: "fwd", AuthMode: AuthForward, Upstream: AnthropicUpstream},
			{
				ID: "inj", AuthMode: AuthInject, Upstream: "https://gateway.example.test", Model: "example-model",
				APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered",
			},
			{
				ID: "sig", AuthMode: AuthSigV4, AWSProfile: "example-profile", AWSRegion: "us-east-1",
				ModelMapEnvFile: "/etc/example/models.env",
			},
		},
		Contexts: []Context{
			{
				ID: "main", DefaultSeat: "fwd", Models: map[string]string{"example-model": "inj"},
				Subagents: &Subagents{
					DefaultSeat: "sig",
					AgentTypes:  map[string]string{"explore": "inj"},
					Models:      map[string]string{"example-haiku": "sig"},
				},
			},
			{ID: "side", DefaultSeat: "sig", Account: "acct-example"},
		},
	}
}

func TestValidateAcceptsEveryAuthMode(t *testing.T) {
	reg := Registry{
		DefaultContext: "main",
		Seats: []Seat{
			{
				ID: "fwd", AuthMode: AuthForward, Upstream: AnthropicUpstream, Model: "example-model",
				Speed: "fast", CacheTTL: "1h", StrictStructuredOutput: true, MaxInputTokens: 200000,
			},
			{
				ID: "inj", AuthMode: AuthInject, Upstream: "http://gateway.example.test:8080", Insecure: true,
				Model: "example-model", APIKeyEnvFile: "EXAMPLE_API_KEY", Billing: "metered", Speed: "fast",
				StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 100000,
			},
			{
				ID: "sig", AuthMode: AuthSigV4, AWSProfile: "example-profile", AWSRegion: "us-east-1",
				ModelMapEnvFile: "/etc/example/models.env", StripBetas: []string{"example-beta"},
				StripCacheControl: true, StrictStructuredOutput: true, MaxInputTokens: 50000,
			},
		},
		Contexts: []Context{
			{
				ID: "main", DefaultSeat: "fwd",
				Models: map[string]string{"example-model": "inj", "example-sigv4": "sig"},
				Subagents: &Subagents{
					DefaultSeat: "sig",
					AgentTypes:  map[string]string{"explore": "inj"},
					Models:      map[string]string{"example-haiku": "fwd"},
				},
			},
			{ID: "side", DefaultSeat: "fwd", Account: "acct-example"},
		},
	}
	if err := Validate(reg, exampleAccounts()); err != nil {
		t.Fatalf("Validate(valid registry) = %v; want nil", err)
	}
}

// refusal is a registry Validate must refuse. mutate breaks validRegistry, want is the id the error must name, and
// hidden, when set, is a value the error must not carry.
type refusal struct {
	name   string
	mutate func(*Registry)
	want   string
	hidden string
}

func regCase(name, want string, mutate func(*Registry)) refusal {
	return refusal{name: name, mutate: mutate, want: want}
}

func seatCase(name, id string, mutate func(*Seat)) refusal {
	return regCase(name, id, func(reg *Registry) { mutateSeat(reg, id, mutate) })
}

// contextCase breaks the context with the given id, and the error must name want.
func contextCase(name, ctxID, want string, mutate func(*Context)) refusal {
	return regCase(name, want, func(reg *Registry) {
		for index := range reg.Contexts {
			if reg.Contexts[index].ID == ctxID {
				mutate(&reg.Contexts[index])
			}
		}
	})
}

// mutateSeat applies mutate to the seat of reg with the given id.
func mutateSeat(reg *Registry, id string, mutate func(*Seat)) {
	for index := range reg.Seats {
		if reg.Seats[index].ID == id {
			mutate(&reg.Seats[index])
		}
	}
}

func registryRefusals() []refusal {
	return []refusal{
		regCase("default_context with a malformed id is refused", "Main-Ctx", func(reg *Registry) {
			reg.DefaultContext = "Main-Ctx"
		}),
		regCase("default_context names a context", "absent", func(reg *Registry) {
			reg.DefaultContext = "absent"
		}),
		regCase("seat id matches the id grammar", "Bad_Seat", func(reg *Registry) {
			reg.Seats = append(reg.Seats, Seat{ID: "Bad_Seat", AuthMode: AuthForward, Upstream: AnthropicUpstream})
		}),
		regCase("seat id is defined once", "fwd", func(reg *Registry) {
			reg.Seats = append(reg.Seats, reg.Seats[0])
		}),
		regCase("context id matches the id grammar", "Bad_Ctx", func(reg *Registry) {
			reg.Contexts[1].ID = "Bad_Ctx"
		}),
		regCase("context id is defined once", "side", func(reg *Registry) {
			reg.Contexts = append(reg.Contexts, reg.Contexts[1])
		}),
	}
}

func seatRefusals() []refusal {
	return []refusal{
		seatCase("auth_mode is forward, inject or sigv4", "fwd", func(seat *Seat) {
			seat.AuthMode = "bearer"
		}),
		seatCase("speed is empty or fast", "fwd", func(seat *Seat) {
			seat.Speed = "turbo"
		}),
		seatCase("speed is never set on sigv4", "sig", func(seat *Seat) {
			seat.Speed = "fast"
		}),
		seatCase("strip_betas is set only on sigv4", "fwd", func(seat *Seat) {
			seat.StripBetas = []string{"example-beta"}
		}),
		seatCase("cache_ttl is empty or 1h", "fwd", func(seat *Seat) {
			seat.CacheTTL = "5m"
		}),
		seatCase("cache_ttl is set only on forward", "inj", func(seat *Seat) {
			seat.CacheTTL = "1h"
		}),
		seatCase("cache_ttl is never set with strip_cache_control", "fwd", func(seat *Seat) {
			seat.CacheTTL = "1h"
			seat.StripCacheControl = true
		}),
		seatCase("max_input_tokens is not negative", "fwd", func(seat *Seat) {
			seat.MaxInputTokens = -1
		}),
		seatCase("forward upstream is the Anthropic upstream", "fwd", func(seat *Seat) {
			seat.Upstream = "https://gateway.example.test"
		}),
		seatCase("forward seat is not insecure over https", "fwd", func(seat *Seat) {
			seat.Insecure = true
		}),
		seatCase("forward api_key_env_file is empty", "fwd", func(seat *Seat) {
			seat.APIKeyEnvFile = "EXAMPLE_API_KEY"
		}),
		seatCase("forward aws_profile is empty", "fwd", func(seat *Seat) {
			seat.AWSProfile = "example-profile"
		}),
		seatCase("forward aws_region is empty", "fwd", func(seat *Seat) {
			seat.AWSRegion = "us-east-1"
		}),
		seatCase("forward model_map_env_file is empty", "fwd", func(seat *Seat) {
			seat.ModelMapEnvFile = "/etc/example/models.env"
		}),
		seatCase("forward billing is empty", "fwd", func(seat *Seat) {
			seat.Billing = "metered"
		}),
	}
}

func injectRefusals() []refusal {
	return []refusal{
		{
			name:   "inject upstream holds no user information",
			want:   "inj",
			hidden: "example-secret",
			mutate: func(reg *Registry) {
				mutateSeat(reg, "inj", func(seat *Seat) {
					seat.Upstream = "https://user:example-secret@gateway.example.test"
				})
			},
		},
		seatCase("inject upstream has no query", "inj", func(seat *Seat) {
			seat.Upstream = "https://gateway.example.test/?key=1"
		}),
		seatCase("inject upstream has no fragment", "inj", func(seat *Seat) {
			seat.Upstream = "https://gateway.example.test/#top"
		}),
		seatCase("inject upstream is an absolute URL", "inj", func(seat *Seat) {
			seat.Upstream = "gateway.example.test"
		}),
		seatCase("inject upstream parses as a URL", "inj", func(seat *Seat) {
			seat.Upstream = "https://[::1"
		}),
		seatCase("inject upstream uses https or http", "inj", func(seat *Seat) {
			seat.Upstream = "ftp://gateway.example.test"
		}),
		seatCase("inject http upstream is loopback or insecure", "inj", func(seat *Seat) {
			seat.Upstream = "http://gateway.example.test"
		}),
		seatCase("inject insecure is refused over https", "inj", func(seat *Seat) {
			seat.Insecure = true
		}),
		seatCase("inject insecure is refused over loopback", "inj", func(seat *Seat) {
			seat.Upstream = "http://127.0.0.1:8080"
			seat.Insecure = true
		}),
		seatCase("inject api_key_env_file matches the env grammar", "inj", func(seat *Seat) {
			seat.APIKeyEnvFile = "1EXAMPLE"
		}),
		seatCase("inject api_key_env_file is set", "inj", func(seat *Seat) {
			seat.APIKeyEnvFile = ""
		}),
		seatCase("inject model does not contain claude", "inj", func(seat *Seat) {
			seat.Model = "Claude-Example"
		}),
		seatCase("inject aws_profile is empty", "inj", func(seat *Seat) {
			seat.AWSProfile = "example-profile"
		}),
		seatCase("inject aws_region is empty", "inj", func(seat *Seat) {
			seat.AWSRegion = "us-east-1"
		}),
		seatCase("inject model_map_env_file is empty", "inj", func(seat *Seat) {
			seat.ModelMapEnvFile = "/etc/example/models.env"
		}),
		seatCase("inject billing is required", "inj", func(seat *Seat) {
			seat.Billing = ""
		}),
		seatCase("inject billing is subscription, metered or self_hosted", "inj", func(seat *Seat) {
			seat.Billing = "free"
		}),
	}
}

func sigv4Refusals() []refusal {
	return []refusal{
		seatCase("sigv4 upstream is empty", "sig", func(seat *Seat) {
			seat.Upstream = "https://gateway.example.test"
		}),
		seatCase("sigv4 api_key_env_file is empty", "sig", func(seat *Seat) {
			seat.APIKeyEnvFile = "EXAMPLE_API_KEY"
		}),
		seatCase("sigv4 billing is empty", "sig", func(seat *Seat) {
			seat.Billing = "metered"
		}),
		seatCase("sigv4 seat is not insecure", "sig", func(seat *Seat) {
			seat.Insecure = true
		}),
		seatCase("sigv4 aws_profile is non-empty after trimming", "sig", func(seat *Seat) {
			seat.AWSProfile = "   "
		}),
		seatCase("sigv4 aws_region is non-empty after trimming", "sig", func(seat *Seat) {
			seat.AWSRegion = " "
		}),
		seatCase("sigv4 model_map_env_file is non-empty after trimming", "sig", func(seat *Seat) {
			seat.ModelMapEnvFile = ""
		}),
		seatCase("sigv4 aws_region uses lowercase letters, digits and dashes", "sig", func(seat *Seat) {
			seat.AWSRegion = "US_EAST_1"
		}),
		seatCase("sigv4 aws_profile uses the profile characters", "sig", func(seat *Seat) {
			seat.AWSProfile = "Example Profile"
		}),
	}
}

func contextRefusals() []refusal {
	return []refusal{
		contextCase("context default_seat names a seat", "main", "ghost", func(ctx *Context) {
			ctx.DefaultSeat = "ghost"
		}),
		contextCase("context models selector is non-empty", "main", "main", func(ctx *Context) {
			ctx.Models[""] = "fwd"
		}),
		contextCase("context models selector names a seat", "main", "ghost", func(ctx *Context) {
			ctx.Models["sonnet-example"] = "ghost"
		}),
		contextCase("context models selector does not bind inject seat to claude", "main", "inj",
			func(ctx *Context) {
				ctx.Models["Claude-Example"] = "inj"
			}),
		contextCase("context account is one of the accounts", "side", "ghost-acct", func(ctx *Context) {
			ctx.Account = "ghost-acct"
		}),
	}
}

func subagentRefusals() []refusal {
	return []refusal{
		contextCase("subagents default_seat names a seat", "main", "ghost", func(ctx *Context) {
			ctx.Subagents.DefaultSeat = "ghost"
		}),
		regCase("subagents default_seat inject seat needs a model", "inj", func(reg *Registry) {
			mutateSeat(reg, "inj", func(seat *Seat) { seat.Model = "" })
			reg.Contexts[0].Subagents.DefaultSeat = "inj"
			reg.Contexts[0].Subagents.AgentTypes = map[string]string{}
		}),
		contextCase("subagents agent type key is non-empty", "main", "main", func(ctx *Context) {
			ctx.Subagents.AgentTypes[""] = "sig"
		}),
		contextCase("subagents agent type key uses printable ASCII without the space", "main", "bad key",
			func(ctx *Context) {
				ctx.Subagents.AgentTypes["bad key"] = "sig"
			}),
		contextCase("subagents agent type key is at most 256 bytes", "main", strings.Repeat("a", tooLongAgentType),
			func(ctx *Context) {
				ctx.Subagents.AgentTypes[strings.Repeat("a", tooLongAgentType)] = "sig"
			}),
		contextCase("subagents agent type names a seat", "main", "ghost", func(ctx *Context) {
			ctx.Subagents.AgentTypes["explore"] = "ghost"
		}),
		regCase("subagents agent type inject seat needs a model", "inj", func(reg *Registry) {
			mutateSeat(reg, "inj", func(seat *Seat) { seat.Model = "" })
		}),
		contextCase("subagents models selector is non-empty", "main", "main", func(ctx *Context) {
			ctx.Subagents.Models[""] = "sig"
		}),
		contextCase("subagents models selector names a seat", "main", "ghost", func(ctx *Context) {
			ctx.Subagents.Models["example-haiku"] = "ghost"
		}),
		regCase("subagents models inject seat needs a model", "inj", func(reg *Registry) {
			mutateSeat(reg, "inj", func(seat *Seat) { seat.Model = "" })
			reg.Contexts[0].Subagents.AgentTypes = map[string]string{}
			reg.Contexts[0].Subagents.Models["example-haiku"] = "inj"
		}),
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := slices.Concat(
		registryRefusals(),
		seatRefusals(),
		injectRefusals(),
		sigv4Refusals(),
		contextRefusals(),
		subagentRefusals(),
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := validRegistry()
			tc.mutate(&reg)
			err := Validate(reg, exampleAccounts())
			if err == nil {
				t.Fatalf("Validate accepted the registry; want an error naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate error %v does not name %q", err, tc.want)
			}
			if tc.hidden != "" && strings.Contains(err.Error(), tc.hidden) {
				t.Errorf("Validate error %v carries %q", err, tc.hidden)
			}
		})
	}
}
