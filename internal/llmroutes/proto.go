package llmroutes

import (
	"maps"
	"slices"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// FromProto converts a route registry from its protobuf form. Seats and contexts keep their order, and maps and slices
// are copied. A nil message gives the zero Registry.
func FromProto(reg *orreryv1.RouteRegistry) Registry {
	if reg == nil {
		return Registry{}
	}
	return Registry{
		DefaultContext: reg.GetDefaultContext(),
		Seats:          mapSlice(reg.GetSeats(), seatFromProto),
		Contexts:       mapSlice(reg.GetContexts(), contextFromProto),
	}
}

// ToProto converts a route registry to its protobuf form. Seats and contexts keep their order, and maps and slices are
// copied.
func ToProto(reg Registry) *orreryv1.RouteRegistry {
	return &orreryv1.RouteRegistry{
		DefaultContext: reg.DefaultContext,
		Seats:          mapSlice(reg.Seats, seatToProto),
		Contexts:       mapSlice(reg.Contexts, contextToProto),
	}
}

// mapSlice converts every element of in with convert, in order. A nil slice stays nil.
func mapSlice[From, To any](in []From, convert func(From) To) []To {
	if in == nil {
		return nil
	}
	out := make([]To, 0, len(in))
	for _, item := range in {
		out = append(out, convert(item))
	}
	return out
}

func seatFromProto(seat *orreryv1.RouteSeat) Seat {
	return Seat{
		ID:                     seat.GetId(),
		AuthMode:               AuthMode(seat.GetAuthMode()),
		Upstream:               seat.GetUpstream(),
		Model:                  seat.GetModel(),
		APIKeyEnvFile:          seat.GetApiKeyEnvFile(),
		AWSProfile:             seat.GetAwsProfile(),
		AWSRegion:              seat.GetAwsRegion(),
		ModelMapEnvFile:        seat.GetModelMapEnvFile(),
		Billing:                seat.GetBilling(),
		Speed:                  seat.GetSpeed(),
		CacheTTL:               seat.GetCacheTtl(),
		Insecure:               seat.GetInsecure(),
		StripCacheControl:      seat.GetStripCacheControl(),
		StrictStructuredOutput: seat.GetStrictStructuredOutput(),
		MaxInputTokens:         seat.GetMaxInputTokens(),
		StripBetas:             slices.Clone(seat.GetStripBetas()),
	}
}

func seatToProto(seat Seat) *orreryv1.RouteSeat {
	return &orreryv1.RouteSeat{
		Id:                     seat.ID,
		AuthMode:               string(seat.AuthMode),
		Upstream:               seat.Upstream,
		Model:                  seat.Model,
		ApiKeyEnvFile:          seat.APIKeyEnvFile,
		AwsProfile:             seat.AWSProfile,
		AwsRegion:              seat.AWSRegion,
		ModelMapEnvFile:        seat.ModelMapEnvFile,
		Billing:                seat.Billing,
		Speed:                  seat.Speed,
		CacheTtl:               seat.CacheTTL,
		Insecure:               seat.Insecure,
		StripCacheControl:      seat.StripCacheControl,
		StrictStructuredOutput: seat.StrictStructuredOutput,
		MaxInputTokens:         seat.MaxInputTokens,
		StripBetas:             slices.Clone(seat.StripBetas),
	}
}

func contextFromProto(ctx *orreryv1.RouteContext) Context {
	return Context{
		ID:          ctx.GetId(),
		DefaultSeat: ctx.GetDefaultSeat(),
		Account:     ctx.GetAccount(),
		Models:      maps.Clone(ctx.GetModels()),
		Subagents:   subagentsFromProto(ctx.GetSubagents()),
	}
}

func contextToProto(ctx Context) *orreryv1.RouteContext {
	return &orreryv1.RouteContext{
		Id:          ctx.ID,
		DefaultSeat: ctx.DefaultSeat,
		Account:     ctx.Account,
		Models:      maps.Clone(ctx.Models),
		Subagents:   subagentsToProto(ctx.Subagents),
	}
}

// subagentsFromProto maps a nil message to nil, so a context that keeps no subagent routing stays nil.
func subagentsFromProto(sub *orreryv1.RouteSubagents) *Subagents {
	if sub == nil {
		return nil
	}
	return &Subagents{
		DefaultSeat: sub.GetDefaultSeat(),
		AgentTypes:  maps.Clone(sub.GetAgentTypes()),
		Models:      maps.Clone(sub.GetModels()),
	}
}

// subagentsToProto maps a nil Subagents to a nil message, so a context that keeps no subagent routing stays unset.
func subagentsToProto(sub *Subagents) *orreryv1.RouteSubagents {
	if sub == nil {
		return nil
	}
	return &orreryv1.RouteSubagents{
		DefaultSeat: sub.DefaultSeat,
		AgentTypes:  maps.Clone(sub.AgentTypes),
		Models:      maps.Clone(sub.Models),
	}
}
