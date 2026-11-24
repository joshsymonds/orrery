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
	"net/url"
	"strings"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// requestLimitBytes caps the request body the gateway holds in memory: 64 MiB. The body is buffered whole so it can be
// replayed upstream, so an unbounded read would be an out-of-memory kill any caller could ask for.
const requestLimitBytes = 64 << 20

// streamChunkBytes is the read size of the response copy loop. Every chunk is written and flushed as it arrives, so
// this bounds latency, not throughput.
const streamChunkBytes = 32 << 10

// Error types from Anthropic's own taxonomy. A failure the gateway originates is described in the vocabulary a client
// already knows, so that it never has to learn a type of the gateway's invention.
const (
	errorTypeAPI      = "api_error"
	errorTypeNotFound = "not_found_error"
)

// The messages a refusal's body carries. They are fixed strings, so a body never echoes what the caller or the upstream
// sent. The detail of a failure goes to the gateway's log instead.
const (
	msgCapacityExhausted = "gateway: request capacity exhausted"
	msgRegistryNotLoaded = "gateway: the route registry has not been read yet"
	msgBodyUnreadable    = "gateway: the request body could not be read"
	msgContextUnknown    = "gateway: the request names a context the route registry does not define"
	msgRegistryInvalid   = "gateway: the route registry names a seat it does not define"
	msgSeatNotForwarded  = "gateway: the seat the request resolved to is not served yet"
	msgAccountNoToken    = "gateway: the account the context names has no token"
	msgRequestBuild      = "gateway: the upstream request could not be built"
	msgUpstreamFailed    = "gateway: the upstream request failed"
)

// fallbackErrorBody is returned when the error envelope itself fails to encode, which keeps the caller's contract of
// Anthropic-shaped JSON even then.
const fallbackErrorBody = `{"type":"error","error":{"type":"api_error","message":"gateway: internal error"}}`

// errRegistryNotLoaded is what a request is refused with before Load has read the route registry.
var errRegistryNotLoaded = errors.New("gateway: the route registry has not been read yet")

// errBuildRequest marks a failure to build the upstream request, which is the gateway's own fault rather than the
// upstream's.
var errBuildRequest = errors.New("gateway: build upstream request")

// errorEnvelope is the Anthropic error wire shape.
type errorEnvelope struct {
	Type  string       `json:"type"`
	Error errorPayload `json:"error"`
}

// errorPayload is the detail object inside an Anthropic error.
type errorPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ServeHTTP serves one request to the gateway. The request is admitted first, so that no more than
// requestAdmissionCapacity are served at once. It is then served from the route registry Load read: the context its
// path names and the seat that context chooses for its model and subagent identity answer it.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.tryAdmission() {
		g.refuseOverload(w, r)
		return
	}
	defer g.releaseAdmission()

	snap := g.snapshot.Load()
	if snap == nil {
		g.abort(r.Context(), w, http.StatusServiceUnavailable, errorTypeAPI, msgRegistryNotLoaded, errRegistryNotLoaded)
		return
	}

	name, _ := SplitContext(r.URL.EscapedPath())
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimitBytes))
	if err != nil {
		g.abort(r.Context(), w, readFailureStatus(err), errorTypeAPI, msgBodyUnreadable,
			fmt.Errorf("gateway: read request body: %w", err))
		return
	}

	res, err := Resolve(snap.Routes, name, requestModel(body), observeSubagent(r.Header))
	if err != nil {
		g.refuseResolution(r.Context(), w, err)
		return
	}

	// Only forward seats are served so far. Any other seat is refused rather than sent on, so the caller's credential
	// never reaches an upstream its seat does not name. The inject and sigv4 seats replace this refusal with their own
	// paths.
	if res.Seat.AuthMode != llmroutes.AuthForward {
		g.abort(r.Context(), w, http.StatusNotImplemented, errorTypeAPI, msgSeatNotForwarded,
			fmt.Errorf("%w: seat %q authenticates with %s", errNotImplemented, res.SeatID, res.Seat.AuthMode))
		return
	}

	// Hook points for the later steps. The inject and sigv4 seats take the refusal above as their branch. The request
	// rewrite changes body before forward reads it. The pin header is set in send, beside the credential. The
	// ledger record is started before forward and finished after its relay, through g.ledger.
	g.forward(w, r, res.Context.Account, snap.Tokens, body)
}

// forward sends the request to the upstream a forward seat reaches and relays the upstream's answer. The caller's own
// credential passes through untouched unless the context names an account, whose token replaces it.
func (g *Gateway) forward(
	w http.ResponseWriter,
	r *http.Request,
	account string,
	tokens map[string]string,
	body []byte,
) {
	token, err := accountToken(account, tokens)
	if err != nil {
		g.abort(r.Context(), w, http.StatusInternalServerError, errorTypeAPI, msgAccountNoToken, err)
		return
	}

	resp, err := g.send(r, account, token, body)
	if errors.Is(err, errBuildRequest) {
		g.abort(r.Context(), w, http.StatusInternalServerError, errorTypeAPI, msgRequestBuild, err)
		return
	}
	if err != nil {
		g.abort(r.Context(), w, http.StatusBadGateway, errorTypeAPI, msgUpstreamFailed, err)
		return
	}
	defer resp.Body.Close()

	g.relay(r.Context(), w, resp)
}

// accountToken returns the token of the account a context names, or the empty string when the context names none. It
// refuses an account with no token, rather than falling back to the caller's credential.
func accountToken(account string, tokens map[string]string) (string, error) {
	if account == "" {
		return "", nil
	}

	token, ok := tokens[account]
	if !ok || token == "" {
		return "", fmt.Errorf("gateway: account %q has no token in the route registry", account)
	}

	return token, nil
}

// send builds the request a forward seat sends upstream and sends it: the upstream's own scheme and host, the path the
// caller named after its context, the caller's method, query and body, and the caller's headers. When account is set,
// the caller's Authorization and x-api-key are dropped and token is sent as the bearer credential.
//
// The target is a copy of the configured upstream, so no part of the caller's request can change its scheme or host.
// The path after the context is set as both the decoded path and the escaped path, so an escaped byte reaches the
// upstream as it arrived. The request is built on the upstream's own URL and a fixed method, and the caller's method
// and the target are assigned to it, so the caller's method is never handed to the constructor.
//
// The request is built and sent in one function, so that the request the client sends is the one this function built.
// It returns an error wrapping errBuildRequest when the request cannot be built, and any other error is the upstream's.
func (g *Gateway) send(r *http.Request, account, token string, body []byte) (*http.Response, error) {
	_, rest := SplitContext(r.URL.EscapedPath())
	path, err := url.PathUnescape(rest)
	if err != nil {
		return nil, fmt.Errorf("%w: unescape the request path: %w", errBuildRequest, err)
	}

	target := *g.upstream
	target.Path = strings.TrimSuffix(g.upstream.Path, "/") + path
	target.RawPath = strings.TrimSuffix(g.upstream.EscapedPath(), "/") + rest
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.upstream.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBuildRequest, err)
	}
	req.Method = r.Method
	req.URL = &target

	dropped := []string{headerContentLength}
	if account != "" {
		dropped = append(dropped, headerAuthorization, headerClientKey)
	}
	copyHeader(req.Header, r.Header, droppedHeaders(r.Header, dropped...))
	if account != "" {
		req.Header.Set(headerAuthorization, "Bearer "+token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway: upstream request failed: %w", err)
	}

	return resp, nil
}

// relay copies the upstream's answer to the caller, flushing each chunk as it arrives so that a streamed turn reaches
// the caller as it is produced.
func (g *Gateway) relay(ctx context.Context, w http.ResponseWriter, resp *http.Response) {
	copyHeader(w.Header(), resp.Header, droppedHeaders(resp.Header))
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, streamChunkBytes)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				g.logger.WarnContext(ctx, "client went away mid-stream", slog.Any("error", err))
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				g.logger.WarnContext(ctx, "upstream stream ended early", slog.Any("error", readErr))
			}
			return
		}
	}
}

// refuseResolution answers a request whose context or seat did not resolve. A context the registry does not define is
// a 404, as Anthropic answers an unknown resource. A registry that names a seat it does not define is the gateway's own
// fault, and is a 500.
func (g *Gateway) refuseResolution(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNoContext) {
		g.abort(ctx, w, http.StatusNotFound, errorTypeNotFound, msgContextUnknown, err)
		return
	}

	g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgRegistryInvalid, err)
}

// abort answers a failure the gateway originated, in Anthropic's error shape, and logs it. The body carries only the
// fixed message, and err is logged for the operator. An upstream's own error is never answered here: it is relayed
// untouched.
func (g *Gateway) abort(
	ctx context.Context,
	w http.ResponseWriter,
	status int,
	errType, message string,
	err error,
) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)

	if _, writeErr := w.Write(typedErrorBody(errType, message)); writeErr != nil {
		g.logger.WarnContext(ctx, "client went away before the error reached it", slog.Any("error", writeErr))
	}
	g.logger.WarnContext(ctx, "gateway error", slog.Int("status", status), slog.Any("error", err))
}

// typedErrorBody renders a failure the gateway originated as Anthropic-format error JSON.
func typedErrorBody(errType, message string) []byte {
	body, marshalErr := json.Marshal(errorEnvelope{
		Type:  "error",
		Error: errorPayload{Type: errType, Message: message},
	})
	if marshalErr != nil {
		return []byte(fallbackErrorBody)
	}

	return body
}

// readFailureStatus reports the status a failed body read deserves. A caller that sent more than the gateway holds is
// told exactly that; any other read failure is a request the caller cut short.
func readFailureStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}

	return http.StatusBadRequest
}

// requestModel is the model a request names in its body. A body that names none, or is not a JSON object, names no
// model, so its request takes its context's default seat and the upstream judges the body itself.
func requestModel(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}

	return probe.Model
}
