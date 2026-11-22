package server

import (
	"net/http"

	"connectrpc.com/connect"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/store"
)

// usageUnavailable is the fixed message for a usage read when serve has no ledger reader.
const usageUnavailable = "usage ledger unavailable"

// usageNotImplemented is the fixed message for a usage read whose answer is not written yet.
const usageNotImplemented = "usage read not implemented"

// usageServer answers orrery.v1.Usage from the ledger reader. A nil ledger answers each read unavailable.
type usageServer struct {
	ledger   *ledger.Reader
	sessions *store.Reader
}

// newUsageHandler returns the path and handler of the Usage service, reading the ledger from ledger and the sessions
// from sessions, as usageServer does. Each request reads at most maxControlBody bytes.
func newUsageHandler(
	ledger *ledger.Reader,
	sessions *store.Reader,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewUsageHandler(usageServer{ledger: ledger, sessions: sessions}, options...)
}
