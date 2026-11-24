package gateway

import (
	"context"
	"log/slog"
)

// Run re-reads the route registry every interval until ctx ends, and returns nil when it ends. A read that succeeds
// replaces the snapshot requests are served from, so a changed seat or account applies to the next request. A read that
// fails is logged at warn and leaves the snapshot the gateway served before.
func (g *Gateway) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-g.clock.After(g.interval):
		}
		g.reload(ctx)
	}
}

// reload reads the route registry once and swaps the snapshot requests are served from when the read succeeds. The swap
// is one atomic store, and a request loads the snapshot once when it starts, so a request sees either the snapshot
// before the swap or the one after it, never a mix of the two.
func (g *Gateway) reload(ctx context.Context) {
	snap, err := g.routes.RoutesSnapshot(ctx)
	if err != nil {
		g.logger.WarnContext(ctx, "re-read the route registry; serving the last routes", slog.Any("error", err))
		return
	}
	g.snapshot.Store(&snap)
}
