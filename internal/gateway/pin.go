package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/joshsymonds/orrery/internal/llmroutes"
)

// accountPinHeader is the header a loopback caller names a stored account with, to pin the account its forward request
// is sent with. It is the gateway's own header and never the upstream's, so it is removed from every upstream request.
const accountPinHeader = "X-Orrery-Account"

// The messages a refused account pin's body carries.
const (
	msgPinPeer    = "gateway: the account pin is honored only from a loopback peer"
	msgPinAccount = "gateway: the account pin names no stored account"
	msgPinSeat    = "gateway: the account pin needs a forward seat"
)

// pinFailure is a request whose account pin is refused: the status and error type it is answered with, the message its
// body carries, and the error the log records.
type pinFailure struct {
	status  int
	errType string
	message string
	err     error
}

// pinnedToken returns the token of the account the request pins with its X-Orrery-Account header, or the empty string
// when the request pins none. A pin is honored only from a loopback peer, only for an account the store holds, and only
// on a forward seat. Any other pin is refused, so that nothing is sent upstream for it.
func pinnedToken(r *http.Request, seat llmroutes.Seat, accountTokens map[string]string) (string, *pinFailure) {
	values := r.Header.Values(accountPinHeader)
	if len(values) == 0 {
		return "", nil
	}

	if !isLoopbackPeer(r.RemoteAddr) {
		return "", &pinFailure{
			status:  http.StatusForbidden,
			errType: errorTypePermission,
			message: msgPinPeer,
			err:     fmt.Errorf("gateway: account pin from non-loopback peer %s", r.RemoteAddr),
		}
	}

	var token string
	var known bool
	if len(values) == 1 {
		token, known = accountTokens[values[0]]
	}
	if !known || token == "" {
		return "", &pinFailure{
			status:  http.StatusBadRequest,
			errType: errorTypeInvalidRequest,
			message: msgPinAccount,
			err:     errors.New("gateway: account pin names no stored account"),
		}
	}

	if seat.AuthMode != llmroutes.AuthForward {
		return "", &pinFailure{
			status:  http.StatusBadRequest,
			errType: errorTypeInvalidRequest,
			message: msgPinSeat,
			err:     fmt.Errorf("gateway: account pin on seat %q, which authenticates with %s", seat.ID, seat.AuthMode),
		}
	}

	return token, nil
}

// isLoopbackPeer reports whether the peer at remoteAddr, a host and port, is a loopback IP. An address that does not
// split into a host and a port, or whose host is not an IP, is not loopback.
func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
