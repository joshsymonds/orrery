package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Sanitiser turns raw stream-json lines into lines that are safe for a public repository. One
// Sanitiser serves every line of one probe, so an id maps to the same made-up value in each line.
type Sanitiser struct {
	entries []sanitiseEntry
	ids     map[string]map[string]string
}

// sanitiseAction says how the value at an allow-list path is written.
type sanitiseAction int

const (
	sanitiseKeep  sanitiseAction = iota // a scalar value is written verbatim
	sanitiseID                          // a string value becomes a made-up id under prefix
	sanitiseKeyed                       // the object's keys become made-up keys
	sanitiseDrop                        // the object member is left out of the line
	sanitiseEmpty                       // the object is written as {}
	sanitiseKnown                       // a string value is written verbatim only when known lists it
)

// sanitiseEntry is one allow-list path. A "*" segment matches an array index, never an object key.
type sanitiseEntry struct {
	path   []string
	action sanitiseAction
	prefix string
	known  []string // the string values a sanitiseKnown entry keeps
}

// sanitiseSegment is one step of a line path: an object key, or the position of an array element.
type sanitiseSegment struct {
	name  string
	index bool
}

// NewSanitiser returns a Sanitiser with an empty id table.
func NewSanitiser() *Sanitiser {
	return &Sanitiser{entries: sanitiseAllowList(), ids: map[string]map[string]string{}}
}

// Line returns the sanitized form of one stream-json line as compact JSON. keep is false for a
// rate_limit_event line, which the caller drops; clean is then nil. Invalid JSON is an error.
func (s *Sanitiser) Line(raw []byte) ([]byte, bool, error) {
	dropped, err := sanitiseDropped(raw)
	if err != nil {
		return nil, false, err
	}
	if dropped {
		return nil, false, nil
	}
	run := &sanitiseRun{s: s, dec: json.NewDecoder(bytes.NewReader(raw))}
	run.dec.UseNumber()
	err = run.value(nil)
	if err != nil {
		return nil, false, err
	}
	err = sanitiseEnd(run.dec)
	if err != nil {
		return nil, false, err
	}
	return run.out.Bytes(), true, nil
}

// sanitiseDropped reports whether any top-level type member of raw is rate_limit_event. It reads
// the whole line, so a truncated line or trailing data is an error even when its type matches.
func sanitiseDropped(raw []byte) (bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return false, fmt.Errorf("capture: sanitize line: %w", err)
	}
	if first != json.Delim('{') {
		return false, nil
	}
	dropped := false
	for dec.More() {
		var keyToken, value any
		keyToken, err = dec.Token()
		if err != nil {
			return false, fmt.Errorf("capture: sanitize line: %w", err)
		}
		err = dec.Decode(&value)
		if err != nil {
			return false, fmt.Errorf("capture: sanitize line: %w", err)
		}
		if keyToken == "type" && value == "rate_limit_event" {
			dropped = true
		}
	}
	_, err = dec.Token()
	if err != nil {
		return false, fmt.Errorf("capture: sanitize line: %w", err)
	}
	err = sanitiseEnd(dec)
	if err != nil {
		return false, err
	}
	return dropped, nil
}

// sanitiseEnd fails unless the decoder has reached the end of its input.
func sanitiseEnd(dec *json.Decoder) error {
	_, err := dec.Token()
	if !errors.Is(err, io.EOF) {
		return errors.New("capture: sanitize line: data after the JSON value")
	}
	return nil
}

// sanitiseRun holds the state of one pass over one line.
type sanitiseRun struct {
	s   *Sanitiser
	dec *json.Decoder
	out bytes.Buffer
}

// value writes the sanitized form of the next JSON value, which sits at path.
func (r *sanitiseRun) value(path []sanitiseSegment) error {
	token, err := r.dec.Token()
	if err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return r.scalar(path, token)
	}
	entry, found := r.s.find(path)
	if delim == '{' && found && entry.action == sanitiseEmpty {
		return r.emptyObject()
	}
	keyed := found && entry.action == sanitiseKeyed
	if delim == '{' {
		return r.object(path, keyed)
	}
	return r.array(path)
}

// scalar writes one non-container value at path: verbatim when a keep entry lists it, verbatim when
// a known entry lists the string, a made-up id when an id entry lists a string, and otherwise a
// made-up value of the same JSON type.
func (r *sanitiseRun) scalar(path []sanitiseSegment, token any) error {
	entry, found := r.s.find(path)
	if found && entry.action == sanitiseKeep {
		return r.write(token)
	}
	str, isString := token.(string)
	if found && isString && entry.action == sanitiseKnown && slices.Contains(entry.known, str) {
		return r.write(str)
	}
	if found && isString && entry.action == sanitiseID {
		return r.write(r.s.sanitiseID(entry.prefix, str))
	}
	switch token.(type) {
	case string:
		return r.write("redacted")
	case json.Number:
		return r.write(json.Number("0"))
	case bool:
		return r.write(false)
	default:
		return r.write(nil)
	}
}

// object writes the members of the object at path. keyed replaces each key with a made-up key. A
// member at a sanitiseDrop path is consumed unwritten, with no comma and no numbering.
func (r *sanitiseRun) object(path []sanitiseSegment, keyed bool) error {
	r.out.WriteByte('{')
	first := true
	for r.dec.More() {
		keyToken, err := r.dec.Token()
		if err != nil {
			return fmt.Errorf("capture: sanitize line: %w", err)
		}
		key, isString := keyToken.(string)
		if !isString {
			return errors.New("capture: sanitize line: object key is not a string")
		}
		child := sanitiseChild(path, sanitiseSegment{name: key})
		if r.s.dropped(child) {
			err = r.skip()
			if err != nil {
				return err
			}
			continue
		}
		name := key
		if keyed {
			name = r.s.sanitiseID("key", key)
		}
		if !first {
			r.out.WriteByte(',')
		}
		first = false
		err = r.write(name)
		if err != nil {
			return err
		}
		r.out.WriteByte(':')
		err = r.value(child)
		if err != nil {
			return err
		}
	}
	if _, err := r.dec.Token(); err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	r.out.WriteByte('}')
	return nil
}

// emptyObject consumes the members of the object whose opening brace was already read, and writes
// {} in their place. The members are consumed unwritten and number nothing.
func (r *sanitiseRun) emptyObject() error {
	for r.dec.More() {
		if _, err := r.dec.Token(); err != nil {
			return fmt.Errorf("capture: sanitize line: %w", err)
		}
		if err := r.skip(); err != nil {
			return err
		}
	}
	if _, err := r.dec.Token(); err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	r.out.WriteString("{}")
	return nil
}

// array writes the elements of the array at path. When no allow-list entry names its elements, only
// the first is written and the rest are consumed unwritten.
func (r *sanitiseRun) array(path []sanitiseSegment) error {
	named := r.s.namesElements(path)
	r.out.WriteByte('[')
	for index := 0; r.dec.More(); index++ {
		if index > 0 && !named {
			if err := r.skip(); err != nil {
				return err
			}
			continue
		}
		if index > 0 {
			r.out.WriteByte(',')
		}
		segment := sanitiseSegment{name: strconv.Itoa(index), index: true}
		if err := r.value(sanitiseChild(path, segment)); err != nil {
			return err
		}
	}
	if _, err := r.dec.Token(); err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	r.out.WriteByte(']')
	return nil
}

// skip consumes the next JSON value without writing it or numbering any id in it.
func (r *sanitiseRun) skip() error {
	var discarded any
	if err := r.dec.Decode(&discarded); err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	return nil
}

// write appends v as compact JSON.
func (r *sanitiseRun) write(v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("capture: sanitize line: %w", err)
	}
	r.out.Write(encoded)
	return nil
}

// sanitiseChild returns path extended by one segment, in a fresh slice.
func sanitiseChild(path []sanitiseSegment, segment sanitiseSegment) []sanitiseSegment {
	return append(path[:len(path):len(path)], segment)
}

// find returns the first allow-list entry whose path matches the given path.
func (s *Sanitiser) find(path []sanitiseSegment) (sanitiseEntry, bool) {
	for _, entry := range s.entries {
		if sanitiseMatches(entry.path, path) {
			return entry, true
		}
	}
	return sanitiseEntry{}, false
}

// dropped reports whether an allow-list entry leaves out the object member at path.
func (s *Sanitiser) dropped(path []sanitiseSegment) bool {
	entry, found := s.find(path)
	return found && entry.action == sanitiseDrop
}

// namesElements reports whether an allow-list entry names the elements of the array at path: some
// entry's path continues path with a "*" segment.
func (s *Sanitiser) namesElements(path []sanitiseSegment) bool {
	for _, entry := range s.entries {
		if len(entry.path) > len(path) && entry.path[len(path)] == "*" &&
			sanitiseMatches(entry.path[:len(path)], path) {
			return true
		}
	}
	return false
}

// sanitiseMatches reports whether pattern matches path segment by segment. "*" matches an array
// index; any other pattern segment matches an object key of the same name.
func sanitiseMatches(pattern []string, path []sanitiseSegment) bool {
	if len(pattern) != len(path) {
		return false
	}
	for i, segment := range pattern {
		if segment == "*" {
			if !path[i].index {
				return false
			}
		} else if path[i].index || segment != path[i].name {
			return false
		}
	}
	return true
}

// sanitiseID returns the made-up value for original under prefix, numbering distinct originals from
// 1 in order of first appearance.
func (s *Sanitiser) sanitiseID(prefix, original string) string {
	seen := s.ids[prefix]
	if seen == nil {
		seen = map[string]string{}
		s.ids[prefix] = seen
	}
	if made, ok := seen[original]; ok {
		return made
	}
	made := prefix + "_" + strconv.Itoa(len(seen)+1)
	seen[original] = made
	return made
}

// sanitiseAllowList returns the allow-list. Every path not in it is replaced by its JSON type.
func sanitiseAllowList() []sanitiseEntry {
	var entries []sanitiseEntry
	for _, path := range []string{
		"type", "subtype", "permissionMode", "claude_code_version",
		"status", "state", "is_error", "num_turns", "stop_reason", "terminal_reason",
		"message.type", "message.role", "message.stop_reason",
		"message.content.*.type", "message.content.*.is_error",
		"event.type", "event.index", "event.message.type", "event.message.role",
		"event.content_block.type", "event.delta.type",
		"event.delta.stop_reason",
		"request.subtype", "request.mode", "request.requires_user_interaction",
		"request.supportedDialogKinds.*",
		"request.permission_suggestions.*.type", "request.permission_suggestions.*.mode",
		"request.permission_suggestions.*.destination",
		"response.subtype", "response.response.behavior", "response.response.mode",
		"response.response.current_permission_mode",
	} {
		entries = append(entries, sanitiseEntry{path: strings.Split(path, "."), action: sanitiseKeep})
	}
	for _, group := range []struct {
		prefix string
		paths  []string
	}{
		{"uuid", []string{"uuid", "command_uuid", "user_message_uuid", "user_message_uuids.*"}},
		{"session", []string{"session_id"}},
		{"req", []string{"request_id", "response.request_id"}},
		{"msg", []string{"message.id", "event.message.id"}},
		{"toolu", []string{
			"parent_tool_use_id", "message.content.*.id", "message.content.*.tool_use_id",
			"event.content_block.id", "permission_denials.*.tool_use_id", "request.tool_use_id",
		}},
	} {
		for _, path := range group.paths {
			entries = append(
				entries,
				sanitiseEntry{path: strings.Split(path, "."), action: sanitiseID, prefix: group.prefix},
			)
		}
	}
	for _, path := range []string{
		"modelUsage", "model_usage_since_last_result", "tool_use_result.answers", "wire_tool_inputs",
		"response.response.updatedInput.answers", "subagent_stats.by_type",
	} {
		entries = append(entries, sanitiseEntry{path: strings.Split(path, "."), action: sanitiseKeyed})
	}
	entries = append(entries,
		sanitiseEntry{path: []string{"credential_expires_at"}, action: sanitiseDrop},
		sanitiseEntry{path: []string{"response", "response", "account"}, action: sanitiseEmpty},
	)
	return append(entries, sanitiseKnownEntries()...)
}

// sanitiseKnownEntries returns the rows that keep a string only when it is on a fixed public list: a
// model id at a model path, a tool name at a tool-name path. Any other string at those paths is
// replaced by "redacted".
func sanitiseKnownEntries() []sanitiseEntry {
	models := []string{"claude-haiku-5-5"}
	tools := []string{sanitiseToolAskUserQuestion, sanitiseToolWrite}
	rows := []struct {
		path  string
		known []string
	}{
		{"model", models},
		{"message.model", models},
		{"event.message.model", models},
		{"message.content.*.name", tools},
		{"event.content_block.name", tools},
		{"permission_denials.*.tool_name", tools},
		{"request.tool_name", tools},
		{"request.display_name", tools},
	}
	entries := make([]sanitiseEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, sanitiseEntry{
			path:   strings.Split(row.path, "."),
			action: sanitiseKnown,
			known:  row.known,
		})
	}
	return entries
}

// The tool names the probes use. A sanitiseKnown row keeps only these at tool-name paths.
const (
	sanitiseToolAskUserQuestion = "AskUserQuestion"
	sanitiseToolWrite           = "Write"
)
