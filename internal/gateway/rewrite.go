package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// Request fields the rewrite reads or writes, at the top level or nested in a cache_control marker.
const (
	fieldModel        = "model"
	fieldSpeed        = "speed"
	fieldCacheControl = "cache_control"
	fieldTTL          = "ttl"
	fieldTools        = "tools"
)

// headerAnthropicBeta is the header the caller's beta flags travel in.
const headerAnthropicBeta = "Anthropic-Beta"

// The messages a refused rewrite is answered with. They are fixed strings, so a body never echoes what the caller sent;
// the detail of the refusal goes to the log instead.
const (
	msgRewriteNotObject = "gateway: the selected seat rewrites the request, which is not a JSON object"
	msgRewriteFailed    = "gateway: the selected seat could not rewrite the request"
)

// rewriteFailure is a request the selected seat could not rewrite: the status and error type it is answered with, the
// message its body carries, and the error the log records.
type rewriteFailure struct {
	status  int
	errType string
	message string
	err     error
}

// rewriteBody returns the body the seat sends upstream. A seat that rewrites nothing forwards body as it arrived. A
// seat whose only rewrite is strict structured output marks its tool where the body allows it, and otherwise forwards
// body as it arrived, since such a seat never refuses a request. Any other seat decodes body as a JSON object, applies
// its rewrites, and refuses a body that is not one.
func rewriteBody(body []byte, seat llmroutes.Seat) ([]byte, *rewriteFailure) {
	if isIdentity(seat) {
		return identityBody(body, seat), nil
	}

	document, failure := decodeRewriteBody(body)
	if failure != nil {
		return nil, failure
	}

	if err := rewriteDocument(document, seat); err != nil {
		return nil, seatFailure(err)
	}

	rewritten, err := json.Marshal(document)
	if err != nil {
		return nil, seatFailure(fmt.Errorf("encode rewritten request: %w", err))
	}

	return rewritten, nil
}

// isIdentity reports whether a seat forwards the body unchanged. Strict structured output is not counted: it is applied
// by identityBody, which leaves the body as it arrived wherever the tool does not qualify.
func isIdentity(seat llmroutes.Seat) bool {
	return seat.Model == "" && !seat.StripCacheControl && seat.Speed == "" && seat.CacheTTL == ""
}

// identityBody returns the bytes an identity seat sends upstream. Only a strict seat rewrites, and only a body whose
// tools hold a qualifying StructuredOutput tool is re-encoded with that tool marked strict. Every other body goes
// upstream as it arrived, and so does any body whose rewrite fails: an identity seat never refuses a request.
func identityBody(body []byte, seat llmroutes.Seat) []byte {
	if !seat.StrictStructuredOutput {
		return body
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return body
	}

	tools, changed, err := strictTools(document[fieldTools])
	if err != nil || !changed {
		return body
	}

	document[fieldTools] = tools

	rewritten, err := json.Marshal(document)
	if err != nil {
		return body
	}

	return rewritten
}

// decodeRewriteBody decodes body as a JSON object. A body that is not one is refused. JSON null decodes into a nil map
// without an error, and nothing can be written into a nil map, so null is refused as well. A null body names no model,
// so it rides its context's default seat, which may rewrite.
func decodeRewriteBody(body []byte) (map[string]json.RawMessage, *rewriteFailure) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, notObject(fmt.Errorf("decode request body: %w", err))
	}

	if document == nil {
		return nil, notObject(errors.New("request body is JSON null rather than an object"))
	}

	return document, nil
}

// notObject is the refusal of a body the rewrite cannot use. It is the caller's own 400, typed as an invalid request,
// because the body is refused on its own face rather than because the gateway failed.
func notObject(err error) *rewriteFailure {
	return &rewriteFailure{
		status:  http.StatusBadRequest,
		errType: errorTypeInvalidRequest,
		message: msgRewriteNotObject,
		err:     err,
	}
}

// seatFailure is a rewrite the gateway failed to apply, which is its own fault and is answered as a gateway error.
func seatFailure(err error) *rewriteFailure {
	return &rewriteFailure{
		status:  http.StatusInternalServerError,
		errType: errorTypeAPI,
		message: msgRewriteFailed,
		err:     err,
	}
}

// rewriteDocument applies the seat's rewrites to document: the model, the speed, the cache control, and last the strict
// tools. The seat's settings are written after the caller's values, so they win over them.
func rewriteDocument(document map[string]json.RawMessage, seat llmroutes.Seat) error {
	if seat.Model != "" {
		model, err := encodeString(seat.Model)
		if err != nil {
			return fmt.Errorf("encode seat model: %w", err)
		}

		document[fieldModel] = model
	}

	if seat.Speed != "" {
		speed, err := encodeString(seat.Speed)
		if err != nil {
			return fmt.Errorf("encode seat speed: %w", err)
		}

		document[fieldSpeed] = speed
	}

	if seat.StripCacheControl {
		stripCacheControl(document)
	}

	if seat.CacheTTL != "" {
		ttl, err := encodeString(seat.CacheTTL)
		if err != nil {
			return fmt.Errorf("encode seat cache_ttl: %w", err)
		}

		setCacheTTL(document, ttl)
	}

	if err := applyStrict(document, seat); err != nil {
		return fmt.Errorf("the selected seat's strict tools: %w", err)
	}

	return nil
}

// encodeString returns value encoded as a JSON string.
func encodeString(value string) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode string: %w", err)
	}

	return encoded, nil
}

// stripCacheControl removes every cache_control key from the whole document, at any depth. Prompt caching belongs to
// the upstream the caller chose, and an upstream that does not understand the marker may reject the request over it.
func stripCacheControl(document map[string]json.RawMessage) {
	rewriteCacheControl(document, func(object map[string]json.RawMessage) {
		delete(object, fieldCacheControl)
	})
}

// setCacheTTL writes ttl, an encoded JSON string, into every cache_control marker in the whole document, at any depth,
// replacing whatever lifetime the caller asked for. Every marker gets the same lifetime. A marker that is not an object
// is left as it arrived.
func setCacheTTL(document map[string]json.RawMessage, ttl json.RawMessage) {
	rewriteCacheControl(document, func(object map[string]json.RawMessage) {
		marker := object[fieldCacheControl]
		if firstToken(marker) != '{' {
			return
		}

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(marker, &fields); err != nil {
			return
		}

		fields[fieldTTL] = ttl
		object[fieldCacheControl] = remarshal(fields, marker)
	})
}

// rewriteCacheControl applies rewrite to every object in the document that carries a cache_control member, the document
// itself included, at any depth.
func rewriteCacheControl(document map[string]json.RawMessage, rewrite func(map[string]json.RawMessage)) {
	if _, present := document[fieldCacheControl]; present {
		rewrite(document)
	}

	for name, value := range document {
		document[name] = rewrittenValue(value, rewrite)
	}
}

// rewrittenValue returns value with rewrite applied to every nested object that carries a cache_control member.
// Anything that is not an object or an array is returned as the exact bytes it arrived as, so numbers keep the precision
// they were written with.
func rewrittenValue(value json.RawMessage, rewrite func(map[string]json.RawMessage)) json.RawMessage {
	// A value that never names the field cannot hold a marker, so its unmarshal and remarshal are skipped. A value that
	// names it only inside a string falls through to the decode below, which leaves it correct.
	if !bytes.Contains(value, []byte(fieldCacheControl)) {
		return value
	}

	switch firstToken(value) {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(value, &object); err != nil {
			return value
		}

		rewriteCacheControl(object, rewrite)

		return remarshal(object, value)
	case '[':
		var array []json.RawMessage
		if err := json.Unmarshal(value, &array); err != nil {
			return value
		}

		for i, element := range array {
			array[i] = rewrittenValue(element, rewrite)
		}

		return remarshal(array, value)
	default:
		return value
	}
}

// remarshal encodes a rewritten container, falling back to the original bytes if it cannot be encoded. Those bytes were
// decoded a moment ago, so they are valid JSON.
func remarshal(container any, original json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(container)
	if err != nil {
		return original
	}

	return encoded
}

// firstToken returns the first non-whitespace byte of a JSON value, or zero if there is none.
func firstToken(value json.RawMessage) byte {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return 0
	}

	return trimmed[0]
}

// stripBetaHeader removes the strip flags from the anthropic-beta header a request carries, across every value of a
// repeated header. A header that names none of them is left exactly as it arrived. One that names any is rebuilt from
// the flags that remain, and is dropped when none remain.
func stripBetaHeader(header http.Header, strip []string) {
	if len(strip) == 0 {
		return
	}

	stripped := make(map[string]bool, len(strip))
	for _, name := range strip {
		stripped[name] = true
	}

	named := false
	var kept []string
	for _, value := range header.Values(headerAnthropicBeta) {
		for _, name := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(name)
			if stripped[trimmed] {
				named = true

				continue
			}

			if trimmed != "" {
				kept = append(kept, trimmed)
			}
		}
	}

	if !named {
		return
	}

	header.Del(headerAnthropicBeta)
	if len(kept) > 0 {
		header.Set(headerAnthropicBeta, strings.Join(kept, ","))
	}
}
