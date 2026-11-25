package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/joshsymonds/orrery/internal/gateway/awssig"
	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// The Bedrock request shape. anthropic_version is the field Bedrock requires in place of the model and stream fields it
// refuses, and anthropic_beta is where it reads the beta flags an Anthropic client sends as a header. Bedrock never
// reads the header, so a flag left there gates nothing and every field it would have unlocked is refused as an extra
// input. That is the whole of the request translation: everything else the caller sent rides along untouched.
const (
	fieldAnthropicVersion = "anthropic_version"
	fieldAnthropicBeta    = "anthropic_beta"
	fieldStream           = "stream"
	bedrockAnthropicTag   = "bedrock-2023-05-31"
)

// The paths a Bedrock seat relays, and the Bedrock runtime paths one model invocation is sent to.
const (
	messagesPath       = "/v1/messages"
	messagesCountPath  = "/v1/messages/count_tokens"
	invokePrefix       = "/model/"
	invokeSuffix       = "/invoke"
	invokeStreamSuffix = "/invoke-with-response-stream"
)

// bedrockService is the SigV4 service name bedrock-runtime signs under.
const bedrockService = "bedrock"

// bedrockStreamType is the content type a streamed Bedrock answer arrives as, before it is translated into SSE.
const bedrockStreamType = "application/vnd.amazon.eventstream"

// bedrockEndpointFormat is the Bedrock runtime's base URL in a region, used when the Config's BedrockEndpoint names
// none.
const bedrockEndpointFormat = "https://bedrock-runtime.%s.amazonaws.com"

// The headers a Bedrock request sets itself, and the values they carry.
const (
	headerAccept         = "Accept"
	headerAcceptEncoding = "Accept-Encoding"
	headerAmzDate        = "X-Amz-Date"
	headerAmzSession     = "X-Amz-Security-Token"
	encodingIdentity     = "identity"
	jsonContentType      = "application/json"
)

// firstPrintableByte is the lowest byte an SSE event name may carry: every byte below it is a control character, and CR
// and LF among them would end the line the name is written on.
const firstPrintableByte = 0x20

// errorEventName is the SSE event name the gateway's own failures are reported under. It is a constant of this package,
// never anything an upstream named.
const errorEventName = "error"

// The messages a Bedrock refusal's body carries. They are fixed strings, so a body never echoes what the caller or the
// upstream sent; the detail of a refusal goes to the log.
const (
	msgCountTokens            = "gateway: count_tokens is not supported for Bedrock seats"
	msgBedrockPath            = "gateway: a Bedrock seat relays only the messages path"
	msgBedrockBody            = "gateway: a Bedrock seat needs a JSON object body"
	msgSeatModelUnset         = "gateway: the seat names no model to send to Bedrock"
	msgBetaInvalid            = "gateway: anthropic_beta must be an array of strings"
	msgCredentialsUnavailable = "gateway: the seat's AWS credentials are unavailable"
	msgBedrockSign            = "gateway: the Bedrock request could not be signed"
	msgBedrockEventBad        = "gateway: Bedrock sent an event the gateway could not decode"
	msgBedrockEventUnsafe     = "gateway: Bedrock named an event the gateway will not render"
)

// bedrockEventEnvelope is how Bedrock carries one Anthropic stream event: the event JSON, base64 encoded, inside a
// one-field object.
type bedrockEventEnvelope struct {
	Bytes []byte `json:"bytes"`
}

// eventTypeProbe reads the type an Anthropic stream event names itself, which is the SSE event name the caller's client
// dispatches on.
type eventTypeProbe struct {
	Type string `json:"type"`
}

// bedrock serves a request that resolves to a sigv4 seat. The request is translated into the Bedrock invocation of the
// seat's model, signed with the seat's AWS profile and sent to the Bedrock runtime of the seat's region. A streamed
// answer is translated back into Anthropic's SSE; any other answer is relayed as it arrived.
func (g *Gateway) bedrock(w http.ResponseWriter, r *http.Request, seat llmroutes.Seat, body []byte) {
	ctx := r.Context()

	_, rest := SplitContext(r.URL.EscapedPath())
	if rest == messagesCountPath {
		g.abort(ctx, w, http.StatusNotImplemented, errorTypeAPI, msgCountTokens,
			errors.New("gateway: count_tokens is not supported for Bedrock seats"))
		return
	}
	if rest != messagesPath {
		g.abort(ctx, w, http.StatusNotFound, errorTypeNotFound, msgBedrockPath,
			fmt.Errorf("gateway: a Bedrock seat relays %s, not %s", messagesPath, rest))
		return
	}

	document, err := bedrockDocument(body)
	if err != nil {
		g.abort(ctx, w, http.StatusBadRequest, errorTypeInvalidRequest, msgBedrockBody, err)
		return
	}

	if seat.Model == "" {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgSeatModelUnset,
			fmt.Errorf("gateway: sigv4 seat %q names no model", seat.ID))
		return
	}

	// Whether the caller asked for a stream is read before the translation drops the field, from the same document the
	// translation reads.
	streamed := documentWantsStream(document)

	betas, err := mergedBetas(document, r.Header.Values(headerAnthropicBeta), seat.StripBetas)
	if err != nil {
		g.abort(ctx, w, http.StatusBadRequest, errorTypeInvalidRequest, msgBetaInvalid, err)
		return
	}

	payload, err := translateBedrockDocument(document, betas)
	if err != nil {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgRequestBuild, err)
		return
	}

	g.bedrockExchange(w, r, seat, streamed, payload)
}

// bedrockExchange signs the translated payload with the seat's AWS credentials, sends it to the seat's Bedrock runtime
// and answers the caller with what came back. The request is built from the caller's headers without the caller's own
// credentials and the caller key, so neither reaches Bedrock.
func (g *Gateway) bedrockExchange(
	w http.ResponseWriter,
	r *http.Request,
	seat llmroutes.Seat,
	streamed bool,
	payload []byte,
) {
	ctx := r.Context()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.bedrockURL(seat, streamed), bytes.NewReader(payload))
	if err != nil {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgRequestBuild,
			fmt.Errorf("gateway: build bedrock request: %w", err))
		return
	}

	copyHeader(req.Header, r.Header, droppedHeaders(r.Header,
		headerContentLength, headerAccept, headerAcceptEncoding, headerContentType, headerAuthorization,
		headerClientKey, headerAnthropicBeta, headerAmzDate, headerAmzSession, patchbayKeyHeader,
	))
	req.Header.Set(headerAcceptEncoding, encodingIdentity)
	req.Header.Set(headerContentType, jsonContentType)
	req.Header.Set(headerAccept, jsonContentType)

	// With no credential source configured, nothing can be signed, so the request is refused rather than sent unsigned.
	if g.awsCredentials == nil {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgCredentialsUnavailable,
			fmt.Errorf("gateway: no AWS credential source is configured, so sigv4 seat %q is refused", seat.ID))
		return
	}

	creds, err := g.awsCredentials(ctx, seat.AWSProfile)
	if err != nil {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgCredentialsUnavailable,
			fmt.Errorf("gateway: aws credentials for profile %q: %w", seat.AWSProfile, err))
		return
	}

	if signErr := awssig.Sign(req, payload, creds, bedrockService, seat.AWSRegion, g.clock.Now()); signErr != nil {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgBedrockSign,
			fmt.Errorf("gateway: sign the bedrock request: %w", signErr))
		return
	}

	resp, err := g.client.Do(req)
	if err != nil {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgUpstreamFailed,
			fmt.Errorf("gateway: bedrock request failed: %w", err))
		return
	}
	defer resp.Body.Close()

	if isBedrockStream(resp) {
		g.streamBedrock(ctx, w, resp)
		return
	}

	g.relay(ctx, w, resp)
}

// bedrockURL is the Bedrock invocation of the seat's model, streamed or not, at the Bedrock runtime the seat's region
// resolves to. The model id is escaped as one path segment, since a Bedrock model id carries colons.
func (g *Gateway) bedrockURL(seat llmroutes.Seat, streamed bool) string {
	suffix := invokeSuffix
	if streamed {
		suffix = invokeStreamSuffix
	}

	return strings.TrimSuffix(g.bedrockBase(seat.AWSRegion), "/") +
		invokePrefix + awssig.EscapePathSegment(seat.Model) + suffix
}

// bedrockBase is the base URL of the Bedrock runtime in region: the answer of the Config's BedrockEndpoint, or the AWS
// default when that is nil or empty.
func (g *Gateway) bedrockBase(region string) string {
	if g.bedrockEndpoint != nil {
		if base := g.bedrockEndpoint(region); base != "" {
			return base
		}
	}

	return fmt.Sprintf(bedrockEndpointFormat, region)
}

// bedrockDocument decodes the caller's body into the one document every other step of the translation reads. A body
// that is not a JSON object is the caller's own error.
func bedrockDocument(body []byte) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage

	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode the request body: %w", err)
	}
	if document == nil {
		return nil, errors.New("the request body is not a JSON object")
	}

	return document, nil
}

// documentWantsStream reports whether the decoded request asked for a streamed turn. Anything that is not a JSON true
// is not a request to stream.
func documentWantsStream(document map[string]json.RawMessage) bool {
	var streamed bool
	if err := json.Unmarshal(document[fieldStream], &streamed); err != nil {
		return false
	}

	return streamed
}

// mergedBetas returns the beta flags Bedrock is sent in the body's anthropic_beta: the body's own list, then the flags
// of the header values, each split on commas, joined in the order first seen and without repeats, minus every flag the
// seat strips. Bedrock refuses the whole request over a flag it does not know, so the operator names those on the seat.
// A body list that is not an array of strings is the caller's own error.
func mergedBetas(document map[string]json.RawMessage, header []string, strip []string) ([]string, error) {
	var body []string
	if raw, present := document[fieldAnthropicBeta]; present {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("%s must be an array of strings: %w", fieldAnthropicBeta, err)
		}
	}

	stripped := make(map[string]bool, len(strip))
	for _, name := range strip {
		stripped[name] = true
	}

	seen := make(map[string]bool)
	merged := make([]string, 0, len(body)+len(header))
	for _, name := range betaNames(body, header) {
		if name == "" || seen[name] || stripped[name] {
			continue
		}
		seen[name] = true
		merged = append(merged, name)
	}

	return merged, nil
}

// betaNames lists every flag named in the body list and then in the header values, each header value split on commas as
// the header grammar allows and each name trimmed of the whitespace that grammar also allows.
func betaNames(body []string, header []string) []string {
	names := make([]string, 0, len(body)+len(header))
	for _, name := range body {
		names = append(names, strings.TrimSpace(name))
	}
	for _, value := range header {
		for _, name := range strings.Split(value, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}

	return names
}

// translateBedrockDocument is the rest of the request translation after mergedBetas: it drops the two fields Bedrock
// carries in the URL rather than the body, sets the merged beta list and adds the version tag Bedrock requires. Every
// other field is left exactly as the caller wrote it. The document is walked as raw values rather than a struct on
// purpose: a field this build has never heard of must still reach the model.
func translateBedrockDocument(document map[string]json.RawMessage, betas []string) ([]byte, error) {
	delete(document, fieldModel)
	delete(document, fieldStream)

	if len(betas) == 0 {
		delete(document, fieldAnthropicBeta)
	} else {
		encoded, err := json.Marshal(betas)
		if err != nil {
			return nil, fmt.Errorf("encode the merged %s list: %w", fieldAnthropicBeta, err)
		}
		document[fieldAnthropicBeta] = encoded
	}

	version, err := json.Marshal(bedrockAnthropicTag)
	if err != nil {
		return nil, fmt.Errorf("encode the bedrock version tag: %w", err)
	}
	document[fieldAnthropicVersion] = version

	translated, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode the translated request: %w", err)
	}

	return translated, nil
}

// isBedrockStream reports whether resp is an event-stream Bedrock answer, the only answer the gateway translates rather
// than relays.
func isBedrockStream(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get(headerContentType)), bedrockStreamType)
}

// streamBedrock translates a Bedrock event stream into the Anthropic SSE the caller's client speaks, flushing every
// event as it arrives. A frame that does not decode ends the stream: the events already written are real, and
// everything after them is unreadable.
func (g *Gateway) streamBedrock(ctx context.Context, w http.ResponseWriter, resp *http.Response) {
	copyHeader(w.Header(), resp.Header, droppedHeaders(resp.Header, headerContentLength, headerContentType))
	w.Header().Set(headerContentType, eventStreamType)
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	decoder := awssig.NewDecoder(resp.Body)

	for {
		msg, err := decoder.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				g.logger.WarnContext(ctx, "bedrock stream ended early", slog.Any("error", err))
			}

			return
		}

		event, done := bedrockSSE(msg)
		if event != nil {
			if _, writeErr := w.Write(event); writeErr != nil {
				g.logger.WarnContext(ctx, "client went away mid-stream", slog.Any("error", writeErr))

				return
			}
			if canFlush {
				flusher.Flush()
			}
		}

		if done {
			return
		}
	}
}

// bedrockSSE renders one decoded event-stream message as an SSE event, and reports whether it ends the stream. An
// exception message becomes an Anthropic error event, which is what a client knows how to surface. A message that
// carries neither an event nor an exception is not the caller's business and is skipped.
func bedrockSSE(msg awssig.Message) ([]byte, bool) {
	if exception := msg.Headers[":exception-type"]; exception != "" {
		body := typedErrorBody(errorTypeAPI, "gateway: the Bedrock stream failed with "+exception)

		return sseEvent(errorEventName, body), true
	}

	eventType := msg.Headers[":event-type"]
	if eventType == "" {
		return nil, false
	}

	var envelope bedrockEventEnvelope
	if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
		return sseEvent(errorEventName, typedErrorBody(errorTypeAPI, msgBedrockEventBad)), true
	}

	// The event JSON is compacted, so no whitespace inside it can end the SSE data line early, and it must decode, since
	// its type names the event.
	var data bytes.Buffer
	if err := json.Compact(&data, envelope.Bytes); err != nil {
		return sseEvent(errorEventName, typedErrorBody(errorTypeAPI, msgBedrockEventBad)), true
	}

	var probe eventTypeProbe
	if err := json.Unmarshal(data.Bytes(), &probe); err != nil {
		return sseEvent(errorEventName, typedErrorBody(errorTypeAPI, msgBedrockEventBad)), true
	}
	if probe.Type != "" {
		eventType = probe.Type
	}

	// The name is written into the SSE framing itself, so one carrying CR or LF could forge whole events the model never
	// sent. Such a message is refused rather than rendered.
	if !safeEventName(eventType) {
		return sseEvent(errorEventName, typedErrorBody(errorTypeAPI, msgBedrockEventUnsafe)), true
	}

	return sseEvent(eventType, data.Bytes()), false
}

// safeEventName reports whether name can be written as an SSE event name at all: any byte below the printable range
// would end the line, or the event, somewhere the framing did not intend.
func safeEventName(name string) bool {
	for _, char := range []byte(name) {
		if char < firstPrintableByte {
			return false
		}
	}

	return true
}

// sseEvent renders one server-sent event in the shape Anthropic's own stream uses: a named event and its JSON payload,
// terminated by a blank line.
func sseEvent(name string, data []byte) []byte {
	var out bytes.Buffer

	out.WriteString("event: ")
	out.WriteString(name)
	out.WriteString("\ndata: ")
	out.Write(data)
	out.WriteString("\n\n")

	return out.Bytes()
}
