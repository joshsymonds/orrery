package gateway

import (
	"net/http"
	"net/textproto"
	"strings"
)

// Header names the gateway reads or sets itself rather than copying as ordinary request metadata.
const (
	headerAuthorization   = "Authorization"
	headerClientKey       = "X-Api-Key"
	headerContentLength   = "Content-Length"
	headerContentType     = "Content-Type"
	headerClaudeAgentID   = "X-Claude-Code-Agent-Id"
	headerClaudeAgentType = "X-Claude-Code-Agent-Type"
)

// hopByHopHeaders are the connection-scoped headers RFC 9110 forbids a proxy from forwarding.
func hopByHopHeaders() []string {
	return []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	}
}

// droppedHeaders returns the canonical names that must not cross the proxy: the fixed hop-by-hop set, every name the
// header's own Connection header names, and any extra names the caller adds.
func droppedHeaders(header http.Header, extra ...string) map[string]bool {
	dropped := make(map[string]bool)

	for _, name := range hopByHopHeaders() {
		dropped[textproto.CanonicalMIMEHeaderKey(name)] = true
	}

	for _, name := range extra {
		dropped[textproto.CanonicalMIMEHeaderKey(name)] = true
	}

	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				dropped[textproto.CanonicalMIMEHeaderKey(token)] = true
			}
		}
	}

	return dropped
}

// copyHeader copies src into dst, skipping the dropped names and keeping every value of a repeated header in order.
func copyHeader(dst, src http.Header, dropped map[string]bool) {
	for name, values := range src {
		if dropped[name] {
			continue
		}

		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

// observeSubagent returns the identity a marked subagent's request reports, or nil for any other request. Claude Code
// marks a subagent's request with its agent id, which must appear exactly once; its agent type is optional.
func observeSubagent(header http.Header) *Observation {
	agentIDs := header.Values(headerClaudeAgentID)
	if len(agentIDs) != 1 || agentIDs[0] == "" {
		return nil
	}

	observation := &Observation{AgentID: agentIDs[0]}
	if agentTypes := header.Values(headerClaudeAgentType); len(agentTypes) == 1 {
		observation.AgentType = agentTypes[0]
	}

	return observation
}
