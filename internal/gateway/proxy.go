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

// fallbackErrorBody is returned when the error envelope itself fails to encode, which keeps the caller's contract of
// Anthropic-shaped JSON even then.
const fallbackErrorBody = `{"type":"error","error":{"type":"api_error","message":"gateway: internal error"}}`

// errRegistryNotLoaded is what a request is refused with before Load has read the route registry.
var errRegistryNotLoaded = errors.New("gateway: the route registry has not been read yet")

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
		g.abort(r.Context(), w, http.StatusServiceUnavailable, errorTypeAPI, errRegistryNotLoaded)
		return
	}

	name, rest := SplitContext(r.URL.EscapedPath())
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimitBytes))
	if err != nil {
		g.abort(r.Context(), w, readFailureStatus(err), errorTypeAPI,
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
		g.abort(r.Context(), w, http.StatusNotImplemented, errorTypeAPI,
			fmt.Errorf("%w: seat %q authenticates with %s", errNotImplemented, res.SeatID, res.Seat.AuthMode))
		return
	}

	// Hook points for the later steps. The inject and sigv4 seats take the refusal above as their branch. The request
	// rewrite changes body before forward reads it. The pin header is set in forwardRequest, beside the credential. The
	// ledger record is started before forward and finished after its relay, through g.ledger.
	g.forward(w, r, res.Context.Account, snap.Tokens, rest, body)
}

// forward sends the request to the upstream a forward seat reaches and relays the upstream's answer. The caller's own
// credential passes through untouched unless the context names an account, whose token replaces it.
func (g *Gateway) forward(
	w http.ResponseWriter,
	r *http.Request,
	account string,
	tokens map[string]string,
	rest string,
	body []byte,
) {
	req, err := g.forwardRequest(r, account, tokens, rest, body)
	if err != nil {
		g.abort(r.Context(), w, http.StatusInternalServerError, errorTypeAPI, err)
		return
	}

	resp, err := g.client.Do(req)
	if err != nil {
		g.abort(r.Context(), w, http.StatusBadGateway, errorTypeAPI,
			fmt.Errorf("gateway: upstream request failed: %w", err))
		return
	}
	defer resp.Body.Close()

	g.relay(r.Context(), w, resp)
}

// forwardRequest builds the request a forward seat sends upstream: the upstream URL joined with the path the caller
// named after its context, the caller's query and body, and the caller's headers. When account is set, the caller's
// Authorization and x-api-key are dropped and the account's token from tokens is sent as the bearer credential. It
// refuses an account with no token, rather than falling back to the caller's credential.
func (g *Gateway) forwardRequest(
	r *http.Request,
	account string,
	tokens map[string]string,
	rest string,
	body []byte,
) (*http.Request, error) {
	dropped := []string{headerContentLength}
	if account != "" {
		dropped = append(dropped, headerAuthorization, headerClientKey)
	}

	target := g.upstream + "/" + strings.TrimPrefix(rest, "/")
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gateway: build upstream request: %w", err)
	}

	copyHeader(req.Header, r.Header, droppedHeaders(r.Header, dropped...))
	if account == "" {
		return req, nil
	}

	token, ok := tokens[account]
	if !ok || token == "" {
		return nil, fmt.Errorf("gateway: account %q has no token in the route registry", account)
	}
	req.Header.Set(headerAuthorization, "Bearer "+token)

	return req, nil
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
		g.abort(ctx, w, http.StatusNotFound, errorTypeNotFound, err)
		return
	}

	g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, err)
}

// abort answers a failure the gateway originated, in Anthropic's error shape, and logs it. An upstream's own error is
// never answered here: it is relayed untouched.
func (g *Gateway) abort(ctx context.Context, w http.ResponseWriter, status int, errType string, err error) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)

	if _, writeErr := w.Write(typedErrorBody(errType, err)); writeErr != nil {
		g.logger.WarnContext(ctx, "client went away before the error reached it", slog.Any("error", writeErr))
	}
	g.logger.WarnContext(ctx, "gateway error", slog.Int("status", status), slog.Any("error", err))
}

// typedErrorBody renders a failure the gateway originated as Anthropic-format error JSON.
func typedErrorBody(errType string, err error) []byte {
	body, marshalErr := json.Marshal(errorEnvelope{
		Type:  "error",
		Error: errorPayload{Type: errType, Message: err.Error()},
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
