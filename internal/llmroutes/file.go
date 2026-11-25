package llmroutes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
)

// The member names of a routes file, the registry JSON document patchbay reads.
const (
	fieldDefaultContext         = "default_context"
	fieldSeats                  = "seats"
	fieldContexts               = "contexts"
	fieldDefaultSeat            = "default_seat"
	fieldAccount                = "account"
	fieldModels                 = "models"
	fieldSubagents              = "subagents"
	fieldAgentTypes             = "agent_types"
	fieldAuthMode               = "auth_mode"
	fieldUpstream               = "upstream"
	fieldModel                  = "model"
	fieldKeyEnvFile             = "api_key_env_file"
	fieldAWSProfile             = "aws_profile"
	fieldAWSRegion              = "aws_region"
	fieldModelMapEnvFile        = "model_map_env_file"
	fieldBilling                = "billing"
	fieldSpeed                  = "speed"
	fieldCacheTTL               = "cache_ttl"
	fieldInsecure               = "insecure"
	fieldStripCacheControl      = "strip_cache_control"
	fieldStrictStructuredOutput = "strict_structured_output"
	fieldMaxInputTokens         = "max_input_tokens"
	fieldStripBetas             = "strip_betas"
)

// ParseFile parses a routes file, the registry JSON document patchbay reads, into a Registry. Its seats and contexts
// come out in id order. ParseFile checks the document's shape and no rule of the registry: Validate holds the result to
// those when it is stored. It refuses a document that repeats a member name within one object, that has data after
// the document, that lacks a required member, that gives a member the wrong type, or that names a member orrery does
// not store, such as extra_body, max_output_tokens or capture_bodies. Each refusal names the member at fault.
func ParseFile(data []byte) (Registry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return Registry{}, fmt.Errorf("decode routes file: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Registry{}, errors.New("unexpected data after the registry document")
	}

	root, err := object(raw, "registry document")
	if err != nil {
		return Registry{}, err
	}
	required := []string{fieldDefaultContext, fieldSeats, fieldContexts}
	err = checkMembers(root, required, required)
	if err != nil {
		return Registry{}, err
	}
	var reg Registry
	err = decodeMembers(root, map[string]any{fieldDefaultContext: &reg.DefaultContext})
	if err != nil {
		return Registry{}, err
	}
	if reg.Seats, err = parseSeats(root[fieldSeats]); err != nil {
		return Registry{}, err
	}
	if reg.Contexts, err = parseContexts(root[fieldContexts]); err != nil {
		return Registry{}, err
	}
	return reg, nil
}

// parseSeats parses the seats object, which is keyed by seat id, into seats in id order.
func parseSeats(raw json.RawMessage) ([]Seat, error) {
	entries, err := object(raw, fieldSeats)
	if err != nil {
		return nil, err
	}
	seats := make([]Seat, 0, len(entries))
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		seat, parseErr := parseSeat(id, entries[id])
		if parseErr != nil {
			return nil, fmt.Errorf("seat %q: %w", id, parseErr)
		}
		seats = append(seats, seat)
	}
	return seats, nil
}

// parseSeat parses one seat with the id it is keyed by. The seat takes the members a seat takes, and no others.
func parseSeat(id string, raw json.RawMessage) (Seat, error) {
	fields, err := object(raw, "seat")
	if err != nil {
		return Seat{}, err
	}
	seat := Seat{ID: id}
	targets := map[string]any{
		fieldAuthMode:               &seat.AuthMode,
		fieldUpstream:               &seat.Upstream,
		fieldModel:                  &seat.Model,
		fieldKeyEnvFile:             &seat.APIKeyEnvFile,
		fieldAWSProfile:             &seat.AWSProfile,
		fieldAWSRegion:              &seat.AWSRegion,
		fieldModelMapEnvFile:        &seat.ModelMapEnvFile,
		fieldBilling:                &seat.Billing,
		fieldSpeed:                  &seat.Speed,
		fieldCacheTTL:               &seat.CacheTTL,
		fieldInsecure:               &seat.Insecure,
		fieldStripCacheControl:      &seat.StripCacheControl,
		fieldStrictStructuredOutput: &seat.StrictStructuredOutput,
		fieldMaxInputTokens:         &seat.MaxInputTokens,
		fieldStripBetas:             &seat.StripBetas,
	}
	err = checkMembers(fields, slices.Collect(maps.Keys(targets)), nil)
	if err != nil {
		return Seat{}, err
	}
	err = decodeMembers(fields, targets)
	if err != nil {
		return Seat{}, err
	}
	return seat, nil
}

// parseContexts parses the contexts object, which is keyed by context id, into contexts in id order.
func parseContexts(raw json.RawMessage) ([]Context, error) {
	entries, err := object(raw, fieldContexts)
	if err != nil {
		return nil, err
	}
	contexts := make([]Context, 0, len(entries))
	for _, id := range slices.Sorted(maps.Keys(entries)) {
		ctx, parseErr := parseContext(id, entries[id])
		if parseErr != nil {
			return nil, fmt.Errorf("context %q: %w", id, parseErr)
		}
		contexts = append(contexts, ctx)
	}
	return contexts, nil
}

// parseContext parses one context with the id it is keyed by.
func parseContext(id string, raw json.RawMessage) (Context, error) {
	fields, err := object(raw, "context")
	if err != nil {
		return Context{}, err
	}
	allowed := []string{fieldDefaultSeat, fieldAccount, fieldModels, fieldSubagents}
	err = checkMembers(fields, allowed, []string{fieldDefaultSeat, fieldModels})
	if err != nil {
		return Context{}, err
	}
	ctx := Context{ID: id}
	scalars := map[string]any{fieldDefaultSeat: &ctx.DefaultSeat, fieldAccount: &ctx.Account}
	err = decodeMembers(fields, scalars)
	if err != nil {
		return Context{}, err
	}
	if ctx.Models, err = parseBindings(fieldModels, fields[fieldModels]); err != nil {
		return Context{}, err
	}
	if rawSubagents, ok := fields[fieldSubagents]; ok {
		subagents, parseErr := parseSubagents(rawSubagents)
		if parseErr != nil {
			return Context{}, fmt.Errorf("subagents: %w", parseErr)
		}
		ctx.Subagents = &subagents
	}
	return ctx, nil
}

// parseSubagents parses a context's subagents object. Its default seat is required. Its agent types and models are
// bindings, and each is optional.
func parseSubagents(raw json.RawMessage) (Subagents, error) {
	fields, err := object(raw, fieldSubagents)
	if err != nil {
		return Subagents{}, err
	}
	allowed := []string{fieldDefaultSeat, fieldAgentTypes, fieldModels}
	err = checkMembers(fields, allowed, []string{fieldDefaultSeat})
	if err != nil {
		return Subagents{}, err
	}
	var sub Subagents
	err = decodeMembers(fields, map[string]any{fieldDefaultSeat: &sub.DefaultSeat})
	if err != nil {
		return Subagents{}, err
	}
	if rawAgentTypes, ok := fields[fieldAgentTypes]; ok {
		if sub.AgentTypes, err = parseBindings(fieldAgentTypes, rawAgentTypes); err != nil {
			return Subagents{}, err
		}
	}
	if rawModels, ok := fields[fieldModels]; ok {
		if sub.Models, err = parseBindings(fieldModels, rawModels); err != nil {
			return Subagents{}, err
		}
	}
	return sub, nil
}

// parseBindings parses a bindings object, which maps a selector to the id of a seat. Each value must be a string.
func parseBindings(name string, raw json.RawMessage) (map[string]string, error) {
	entries, err := object(raw, name)
	if err != nil {
		return nil, err
	}
	bindings := make(map[string]string, len(entries))
	for _, selector := range slices.Sorted(maps.Keys(entries)) {
		var seat string
		if err = json.Unmarshal(entries[selector], &seat); err != nil {
			return nil, fmt.Errorf("%s selector %q: %w", name, selector, err)
		}
		bindings[selector] = seat
	}
	return bindings, nil
}

// object decodes raw as a JSON object into its members, and refuses a member name that repeats. encoding/json keeps the
// last value of a repeated name, so a document that names a thing twice would load as whichever value came second.
// name says what raw is, in the errors.
func object(raw json.RawMessage, name string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		member, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("decode %s: member name is not a string", name)
		}
		if _, repeated := fields[member]; repeated {
			return nil, fmt.Errorf("%s repeats member %q", name, member)
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %s: %w", name, err)
		}
		fields[member] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return fields, nil
}

// checkMembers refuses a member of fields that allowed does not name, and a required member that fields lacks. The
// refusal of an unknown member names it and says orrery does not store it.
func checkMembers(fields map[string]json.RawMessage, allowed, required []string) error {
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("field %q is not one orrery stores", name)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("missing required field %q", name)
		}
	}
	return nil
}

// decodeMembers decodes each member of fields that targets names into its target. A member targets does not name is
// left alone, since the caller decodes it another way. A value that does not decode is an error naming its member.
func decodeMembers(fields map[string]json.RawMessage, targets map[string]any) error {
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		target, ok := targets[name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(fields[name], target); err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
	}
	return nil
}
