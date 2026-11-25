package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// modelsPath is the path the model listing answers, and the prefix of the path one model answers, beneath any
// /ctx/<name> prefix.
const modelsPath = "/v1/models"

// modelsFetchTimeout bounds one upstream models call. Nothing on this surface streams: a listing is a small document
// that either arrives promptly or is not worth waiting on.
const modelsFetchTimeout = 5 * time.Second

// modelsResponseLimitBytes caps the upstream body the gateway holds for a models call. A model listing is a few
// kilobytes; anything past this ceiling is not one.
const modelsResponseLimitBytes = 4 << 20

// epochCreatedAt is the created_at every entry the gateway describes itself carries. The gateway does not know when a
// binding came into existence and will not invent a date. The field is not optional to a strict SDK decoder, and
// Anthropic's contract allows an epoch value when the release date is unknown.
const epochCreatedAt = "1970-01-01T00:00:00Z"

// contentTypeJSON is the content type of every answer the model endpoints write themselves, and the default of an
// upstream answer that declares none.
const contentTypeJSON = "application/json"

// Header names the models calls carry upstream beside the caller's credentials. They shape the answer, so they travel
// with every such request.
const (
	headerVersion = "Anthropic-Version"
	headerBeta    = "Anthropic-Beta"
)

// The messages a model endpoint's refusal carries. Like every message the gateway writes, they are fixed, and the
// detail of a failure goes to the log.
const (
	msgModelUnknown         = "gateway: the context serves no such model"
	msgCallerKeyUnavailable = "gateway: the caller key the named context requires is not loaded"
	msgCallerKeyMismatch    = "gateway: the request does not present the caller key the named context requires"
	msgUpstreamOversized    = "gateway: the upstream response exceeds the size the gateway holds"
	msgResponseEncode       = "gateway: the answer could not be encoded"
)

// modelRoute is a request the model endpoints answer: the listing, or one model, beneath an optional /ctx/<name>.
type modelRoute struct {
	// name is the context the path names, or empty for the default context.
	name string

	// rest is the path after the context prefix, escaped as it arrived.
	rest string

	// id is the model a single-model request names, unescaped. It is empty for the listing.
	id string

	// single is whether the request names one model rather than the listing.
	single bool
}

// upstreamListing is the little of an upstream listing the merge reads: the entries, and the pagination the merged
// envelope has to keep telling the truth about. Both cursors are read, because Anthropic pages forward from last_id and
// back from first_id, and the merged envelope owes a client both directions. Each entry stays raw so nothing about it
// is lost in the round trip.
type upstreamListing struct {
	Data    []json.RawMessage
	HasMore bool
	FirstID *string
	LastID  *string
}

// parseUpstreamListing reads the members of an upstream listing body that the merge reads, each as its type. A member
// that is absent is left at its zero value, and one that does not decode as its type is an error naming it.
func parseUpstreamListing(body []byte) (upstreamListing, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		return upstreamListing{}, fmt.Errorf("decode the listing object: %w", err)
	}

	var listing upstreamListing
	for _, member := range []struct {
		name   string
		target any
	}{
		{name: "data", target: &listing.Data},
		{name: "has_more", target: &listing.HasMore},
		{name: "first_id", target: &listing.FirstID},
		{name: "last_id", target: &listing.LastID},
	} {
		raw, ok := members[member.name]
		if !ok {
			continue
		}

		if err := json.Unmarshal(raw, member.target); err != nil {
			return upstreamListing{}, fmt.Errorf("member %q: %w", member.name, err)
		}
	}

	return listing, nil
}

// upstreamEntry is the one field of an upstream model entry the merge reads: the id a binding may shadow.
type upstreamEntry struct {
	ID string `json:"id"`
}

// publicBinding is one selector a context serves publicly, with the seat it routes to.
type publicBinding struct {
	selector string
	seat     llmroutes.Seat
}

// modelsRouteOf reads a request as one of the model endpoints: GET /v1/models, or GET /v1/models/{id}, with or without a
// /ctx/<name> prefix. It reports false for any other request, which the proxy path serves.
func modelsRouteOf(r *http.Request) (modelRoute, bool) {
	if r.Method != http.MethodGet {
		return modelRoute{}, false
	}

	name, rest := SplitContext(r.URL.EscapedPath())
	if rest == modelsPath {
		return modelRoute{name: name, rest: rest}, true
	}

	escapedID, found := strings.CutPrefix(rest, modelsPath+"/")
	if !found || escapedID == "" || strings.Contains(escapedID, "/") {
		return modelRoute{}, false
	}

	id, err := url.PathUnescape(escapedID)
	if err != nil {
		return modelRoute{}, false
	}

	return modelRoute{name: name, rest: rest, id: id, single: true}, true
}

// serveModels answers one model endpoint from the route registry the gateway has loaded. The context the route names
// is resolved the way the proxy resolves it, and then the listing or the one model is answered from that context.
func (g *Gateway) serveModels(w http.ResponseWriter, r *http.Request, route modelRoute) {
	loaded := g.snapshot.Load()
	if loaded == nil {
		g.abort(r.Context(), w, http.StatusServiceUnavailable, errorTypeAPI, msgRegistryNotLoaded, errRegistryNotLoaded)
		return
	}
	snap := *loaded

	res, err := Resolve(snap.Routes, route.name, "", nil)
	if err != nil {
		g.refuseResolution(r.Context(), w, err)
		return
	}

	// Naming a context in the path asks what that context serves, which the caller key exists to protect. The bare path
	// is not gated: it is what a client sends with no idea the gateway is there, and it describes only the default
	// context.
	if route.name != "" && !g.checkModelsKey(r.Context(), w, r) {
		return
	}

	bindings, err := publicBindings(snap.Routes, res.Context)
	if err != nil {
		g.abort(r.Context(), w, http.StatusInternalServerError, errorTypeAPI, msgRegistryInvalid, err)
		return
	}

	if route.single {
		g.showModel(w, r, route, snap, res, bindings)
		return
	}

	g.listModels(w, r, snap, res, bindings)
}

// checkModelsKey refuses a request to a named context's model endpoints that does not present the caller key. With no
// key loaded the answer is 500, since that is the server's own misconfiguration; a key that is missing or wrong is 403.
// It reports whether the request may go on.
func (g *Gateway) checkModelsKey(ctx context.Context, w http.ResponseWriter, r *http.Request) bool {
	err := g.checkCallerKey(r)
	if err == nil {
		return true
	}

	if errors.Is(err, errNoCallerKey) {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgCallerKeyUnavailable, err)
		return false
	}

	g.abort(ctx, w, http.StatusForbidden, errorTypeAPI, msgCallerKeyMismatch, err)

	return false
}

// publicBindings returns the selectors a context serves publicly, in selector order, each with the seat it routes to.
// A binding to a seat the registry does not define is an invalid registry.
func publicBindings(reg llmroutes.Registry, ctx llmroutes.Context) ([]publicBinding, error) {
	selectors := slices.Sorted(maps.Keys(ctx.Models))
	bindings := make([]publicBinding, 0, len(selectors))

	for _, selector := range selectors {
		seatID := ctx.Models[selector]

		seat, ok := findSeat(reg.Seats, seatID)
		if !ok {
			return nil, fmt.Errorf(
				"gateway: context %q binds %q to seat %q, which is not defined",
				ctx.ID,
				selector,
				seatID,
			)
		}

		bindings = append(bindings, publicBinding{selector: selector, seat: seat})
	}

	return bindings, nil
}

// answersFromAnthropic reports whether a context's default seat still reaches Anthropic on the caller's own credentials
// or on the account's token. A forward seat may target no other upstream, so the auth mode is the whole test.
func answersFromAnthropic(seat llmroutes.Seat) bool {
	return seat.AuthMode == llmroutes.AuthForward
}

// listModels answers the listing of the context res resolved. A context whose default seat still answers from Anthropic
// lists its public bindings, followed by the models the account or the caller's own credentials see there and no
// binding already claims. Any other context is listed whole from the registry, since Anthropic has nothing to say about
// a context that never reaches it.
func (g *Gateway) listModels(
	w http.ResponseWriter,
	r *http.Request,
	snap store.RoutesSnapshot,
	res Resolution,
	bindings []publicBinding,
) {
	if !answersFromAnthropic(res.Seat) {
		g.writeModelsJSON(w, r, g.synthesizedList(r.Context(), bindings, res.Seat))
		return
	}

	// The public bindings are a page of their own, in front of the upstream's first page, so they are emitted once and
	// never repeated on a later page. Anthropic pages forward from last_id and back from first_id, so a request carrying
	// either cursor is asking for a page of the upstream's own listing and gets no bindings.
	//
	// What every page still owes is the shadowing: a selector this context serves is what that name means here, whichever
	// upstream page the colliding id happens to arrive on.
	query := r.URL.Query()
	firstPage := !query.Has("after_id") && !query.Has("before_id")

	data := make([]any, 0, len(bindings))
	ids := make([]string, 0, len(bindings))
	claimed := make(map[string]bool, len(bindings))

	for _, binding := range bindings {
		claimed[binding.selector] = true

		if firstPage {
			data = append(data, seatEntry(binding.seat, binding.selector))
			ids = append(ids, binding.selector)
		}
	}

	listing := g.upstreamModels(r, res.Context.Account, snap.Tokens)

	for _, entry := range listing.Data {
		id := entryID(entry)
		if claimed[id] {
			continue
		}

		data = append(data, entry)
		ids = append(ids, id)
	}

	g.writeModelsJSON(w, r, mergedList(data, ids, listing, firstPage))
}

// synthesizedList is the listing of a context whose default seat never reaches Anthropic: its public bindings, and the
// model its default seat serves. A synthesized listing is patchbay's own from end to end and is never paginated, so it
// is always its own first and only page.
func (g *Gateway) synthesizedList(ctx context.Context, bindings []publicBinding, seat llmroutes.Seat) map[string]any {
	data := make([]any, 0, len(bindings)+1)
	ids := make([]string, 0, len(bindings)+1)
	claimed := make(map[string]bool, len(bindings))

	for _, binding := range bindings {
		data = append(data, seatEntry(binding.seat, binding.selector))
		ids = append(ids, binding.selector)
		claimed[binding.selector] = true
	}

	for _, model := range g.defaultModels(ctx, seat) {
		if claimed[model] {
			continue
		}

		data = append(data, seatEntry(seat, model))
		ids = append(ids, model)
		claimed[model] = true
	}

	return mergedList(data, ids, upstreamListing{}, true)
}

// showModel answers one model of GET /v1/models/{id}. A binding the context serves is described from the registry.
// Anything else depends on where the default seat rides: a context that still answers from Anthropic relays the
// upstream's own answer, and any other context answers with the model its default seat rewrites to, or not at all.
func (g *Gateway) showModel(
	w http.ResponseWriter,
	r *http.Request,
	route modelRoute,
	snap store.RoutesSnapshot,
	res Resolution,
	bindings []publicBinding,
) {
	for _, binding := range bindings {
		if binding.selector == route.id {
			g.writeModelsJSON(w, r, seatEntry(binding.seat, binding.selector))
			return
		}
	}

	if answersFromAnthropic(res.Seat) {
		g.relayModel(w, r, snap.Tokens, res.Context.Account, route.rest)
		return
	}

	if slices.Contains(g.defaultModels(r.Context(), res.Seat), route.id) {
		g.writeModelsJSON(w, r, seatEntry(res.Seat, route.id))
		return
	}

	g.abort(r.Context(), w, http.StatusNotFound, errorTypeNotFound, msgModelUnknown,
		fmt.Errorf("context %q serves no model %q", res.Context.ID, route.id))
}

// defaultModels is every model id a caller may name and reach a context's default seat by, in order.
//
// A seat that rewrites to one model advertises that one; a seat that rewrites nothing has no model id to advertise and
// contributes none, because the gateway will not invent one.
//
// A sigv4 seat serves exactly the models its model map has entries for, and the ids a caller may name are that map's
// keys. The values are Bedrock model ids, the one thing the map file exists to keep out of sight, so they are never
// listed. A map that cannot be read costs the listing nothing but those entries: the public bindings still stand.
func (g *Gateway) defaultModels(ctx context.Context, seat llmroutes.Seat) []string {
	if seat.AuthMode != llmroutes.AuthSigV4 {
		if seat.Model == "" {
			return nil
		}

		return []string{seat.Model}
	}

	mapping, err := g.readModelMap(seat.ModelMapEnvFile)
	if err != nil {
		g.logger.WarnContext(
			ctx,
			"model map unavailable; a sigv4 context lists only its public bindings",
			slog.Any("error", err),
		)

		return nil
	}

	models := make([]string, 0, len(mapping))

	for model, target := range mapping {
		// An entry mapping to nothing is one the pipeline refuses, so listing it would advertise a model this context
		// cannot actually reach.
		if target != "" {
			models = append(models, model)
		}
	}

	slices.Sort(models)

	return models
}

// readModelMap resolves the double indirection a sigv4 seat names its model map with: envName is the name of an
// environment variable whose value is the path of a JSON file mapping the model ids a caller may name to the Bedrock
// ones they ride. The variable is read through the gateway's Getenv, and the file is read per request, so a listing
// describes the file as it stands now.
func (g *Gateway) readModelMap(envName string) (map[string]string, error) {
	path := g.getenv(envName)
	if path == "" {
		return nil, fmt.Errorf("environment variable %s is not set", envName)
	}

	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read the model map named by %s: %w", envName, err)
	}

	var mapping map[string]string
	if decodeErr := json.Unmarshal(contents, &mapping); decodeErr != nil {
		return nil, fmt.Errorf("decode the model map named by %s: %w", envName, decodeErr)
	}

	return mapping, nil
}

// relayModel asks the upstream about one model with the credentials the context's account grants, or the caller's own
// when it names none, and relays the upstream's status, content type and body untouched: the upstream's own answer
// about its own models is the honest one. An upstream that cannot be reached, or whose body is too large to hold, is
// answered by the gateway in Anthropic's error shape.
func (g *Gateway) relayModel(
	w http.ResponseWriter,
	r *http.Request,
	tokens map[string]string,
	account, rest string,
) {
	ctx, cancel := context.WithTimeout(r.Context(), modelsFetchTimeout)
	defer cancel()

	bearer, err := accountToken(account, tokens)
	if err != nil {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgAccountNoToken, err)
		return
	}

	resp, err := g.fetchModels(ctx, r, bearer, rest)
	if errors.Is(err, errBuildRequest) {
		g.abort(ctx, w, http.StatusInternalServerError, errorTypeAPI, msgRequestBuild, err)
		return
	}

	if err != nil {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgUpstreamFailed, err)
		return
	}

	defer resp.Body.Close()

	// Read one byte past the ceiling so an upstream that overruns it is caught rather than silently truncated and relayed
	// as a fragment under the upstream's own 2xx.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, modelsResponseLimitBytes+1))
	if readErr != nil {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgUpstreamFailed,
			fmt.Errorf("read upstream response: %w", readErr))

		return
	}

	if len(body) > modelsResponseLimitBytes {
		g.abort(ctx, w, http.StatusBadGateway, errorTypeAPI, msgUpstreamOversized,
			fmt.Errorf("upstream response exceeds the %d-byte ceiling", modelsResponseLimitBytes))

		return
	}

	w.Header().Set(headerContentType, upstreamContentType(resp))
	w.WriteHeader(resp.StatusCode)
	g.writeBody(ctx, w, body)
}

// upstreamModels fetches the listing the context's credentials can see: the account's token when the context names an
// account, and the caller's own credentials otherwise. Every way it can go wrong returns no entries and logs why. The
// caller is never told: a gateway that cannot reach Anthropic still knows its own public bindings.
func (g *Gateway) upstreamModels(r *http.Request, account string, tokens map[string]string) upstreamListing {
	ctx, cancel := context.WithTimeout(r.Context(), modelsFetchTimeout)
	defer cancel()

	bearer, err := accountToken(account, tokens)
	if err != nil {
		g.warnModels(ctx, err)
		return upstreamListing{}
	}

	resp, err := g.fetchModels(ctx, r, bearer, modelsPath)
	if err != nil {
		g.warnModels(ctx, err)
		return upstreamListing{}
	}

	defer resp.Body.Close()

	// Read one byte past the ceiling to tell a listing that fits from one that overruns it, so an oversized body is refused
	// explicitly rather than left to fail an opaque json.Unmarshal.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, modelsResponseLimitBytes+1))
	if readErr != nil {
		g.warnModels(ctx, fmt.Errorf("read upstream response: %w", readErr))
		return upstreamListing{}
	}

	if len(body) > modelsResponseLimitBytes {
		g.warnModels(ctx, fmt.Errorf("upstream response exceeds the %d-byte ceiling", modelsResponseLimitBytes))
		return upstreamListing{}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		g.warnModels(ctx, fmt.Errorf("upstream answered %d", resp.StatusCode))
		return upstreamListing{}
	}

	listing, parseErr := parseUpstreamListing(body)
	if parseErr != nil {
		g.warnModels(ctx, fmt.Errorf("upstream body is not a model listing: %w", parseErr))
		return upstreamListing{}
	}

	return listing
}

// fetchModels sends one models request to the upstream the gateway was configured with, at the path rest names and with
// the query the caller sent. The request carries the credentials modelsHeaders chooses for bearer. The base URL is the
// configured one and only the path and query are the caller's, so no part of the caller's request moves the host.
func (g *Gateway) fetchModels(ctx context.Context, r *http.Request, bearer, rest string) (*http.Response, error) {
	path, err := url.PathUnescape(rest)
	if err != nil {
		return nil, fmt.Errorf("%w: unescape the request path: %w", errBuildRequest, err)
	}

	target := *g.upstream
	target.Path = strings.TrimSuffix(g.upstream.Path, "/") + path
	target.RawPath = strings.TrimSuffix(g.upstream.EscapedPath(), "/") + rest
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.upstream.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBuildRequest, err)
	}

	req.URL = &target
	req.Header = modelsHeaders(r.Header, bearer)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway: upstream request failed: %w", err)
	}

	return resp, nil
}

// modelsHeaders returns the headers a models request carries upstream. The API version and beta flags travel with every
// such request. The credential is the account's token when bearer is set, and the caller's own Authorization and
// X-Api-Key otherwise. The caller's credentials never travel beside an account token, and nothing else the caller sent
// travels at all.
func modelsHeaders(src http.Header, bearer string) http.Header {
	dst := make(http.Header)

	for _, name := range []string{headerVersion, headerBeta} {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}

	if bearer != "" {
		dst.Set(headerAuthorization, "Bearer "+bearer)
		return dst
	}

	for _, name := range []string{headerAuthorization, headerClientKey} {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}

	return dst
}

// warnModels reports a degraded model listing. It is a warning rather than an error because the caller is still served.
func (g *Gateway) warnModels(ctx context.Context, err error) {
	g.logger.WarnContext(ctx, "upstream model list unavailable", slog.Any("error", err))
}

// writeModelsJSON answers with value encoded as JSON. Encoding it whole before anything is written keeps a failure from
// reaching the caller as a truncated 200.
func (g *Gateway) writeModelsJSON(w http.ResponseWriter, r *http.Request, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		g.abort(r.Context(), w, http.StatusInternalServerError, errorTypeAPI, msgResponseEncode,
			fmt.Errorf("encode response: %w", err))

		return
	}

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	g.writeBody(r.Context(), w, body)
}

// writeBody hands body to the caller, noting a caller that has already gone.
func (g *Gateway) writeBody(ctx context.Context, w http.ResponseWriter, body []byte) {
	if _, err := w.Write(body); err != nil {
		g.logger.WarnContext(ctx, "client went away before the answer reached it", slog.Any("error", err))
	}
}

// upstreamContentType reports the content type to relay, defaulting to JSON when the upstream named none. Every answer
// these endpoints relay is a JSON document.
func upstreamContentType(resp *http.Response) string {
	if declared := resp.Header.Get(headerContentType); declared != "" {
		return declared
	}

	return contentTypeJSON
}

// mergedList wraps the merged entries in the /v1/models envelope: data, has_more, first_id and last_id.
//
// has_more is the upstream's own. The gateway puts a page of public bindings in front of the upstream's first page, and
// whether another page follows is the upstream's fact to report, not the gateway's to overwrite.
//
// The cursors are the merged listing's own ends only where the merged listing really does end there. A cursor-qualified
// page is none of the gateway's own from end to end, so both of its cursors are the upstream's, whichever way the caller
// was paging. Only the first page begins where the merged listing begins, with the gateway's own bindings, and only there
// does the merged listing's own final id close it. When has_more is true the next page is the upstream's, so last_id is
// the cursor Anthropic can page from rather than a binding id that would mean nothing to it.
func mergedList(data []any, ids []string, listing upstreamListing, firstPage bool) map[string]any {
	var firstID, lastID *string
	if len(ids) > 0 {
		firstID = &ids[0]
		lastID = &ids[len(ids)-1]
	}

	if firstPage {
		if listing.HasMore {
			lastID = listing.LastID
		}
	} else {
		if listing.FirstID != nil {
			firstID = listing.FirstID
		}

		if listing.LastID != nil {
			lastID = listing.LastID
		}
	}

	return map[string]any{"data": data, "has_more": listing.HasMore, "first_id": firstID, "last_id": lastID}
}

// seatEntry renders one public selector, or one model a context's default seat serves, as a listing entry. The seat
// supplies the token ceiling and nothing else: its id is private, and a listing that named it would publish the
// registry's own vocabulary to every caller. The ceiling is omitted when the seat sets none.
func seatEntry(seat llmroutes.Seat, model string) map[string]any {
	entry := map[string]any{
		"type":         "model",
		"id":           model,
		"display_name": model,
		"created_at":   epochCreatedAt,
	}
	if seat.MaxInputTokens != 0 {
		entry["max_input_tokens"] = seat.MaxInputTokens
	}

	return entry
}

// entryID reads the id of an upstream listing entry. An entry that is not an object, or carries no id, has no id a
// binding could shadow, so it is listed as it arrived.
func entryID(raw json.RawMessage) string {
	var entry upstreamEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ""
	}

	return entry.ID
}
