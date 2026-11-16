package stepmcp

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// CallerInterceptor is a client interceptor that stamps every unary request with the session it runs in, read
// through getenv. A session that orrery started sends its EnvSession as protocol.HeaderSession. Otherwise a session
// in tmux sends its TMUX and TMUX_PANE as protocol.HeaderTmux and protocol.HeaderTmuxPane, when both are set. Any
// other request carries no caller header. Streaming calls pass through unchanged.
func CallerInterceptor(getenv func(string) string) connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			stampCaller(req.Header(), getenv)
			return next(ctx, req)
		}
	})
}

// stampCaller sets on header the caller headers of the session getenv describes.
func stampCaller(header http.Header, getenv func(string) string) {
	if session := getenv(protocol.EnvSession); session != "" {
		header.Set(protocol.HeaderSession, session)
		return
	}
	tmux, pane := getenv("TMUX"), getenv("TMUX_PANE")
	if tmux != "" && pane != "" {
		header.Set(protocol.HeaderTmux, tmux)
		header.Set(protocol.HeaderTmuxPane, pane)
	}
}
