package node

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/holder"
)

// keptMessage is the log record an agent writes once a keeper has attached to a holder. Its session attribute names the
// session.
const keptMessage = "node: keeping holder"

// keeper is a keeper that runs: cancel ends it, and done closes once it has ended.
type keeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// keepHolders starts a keeper on each holder under the state directory's holders directory, as Serve starts. A holder that
// is not live has no keeper, and its keeper ends at once.
func (a *Agent) keepHolders(ctx context.Context) {
	if a.cfg.StateDir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(a.cfg.StateDir, holdersName))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: read the holders directory", "err", err)
		return
	}
	for _, entry := range entries {
		a.keep(ctx, entry.Name())
	}
}

// keep starts the keeper of session id, unless a keeper already runs on it or a splice is open on it. The keeper runs until
// ctx ends, its holder ends, or a splice takes the session.
func (a *Agent) keep(ctx context.Context, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.splices[id] > 0 || a.keepers[id] != nil {
		return
	}
	dir, err := a.holderOf(id)
	if err != nil {
		if !errors.Is(err, holder.ErrGone) {
			a.cfg.Logger.WarnContext(ctx, "node: find a holder to keep", "session", id, "err", err)
		}
		return
	}
	kctx, cancel := context.WithCancel(ctx)
	k := &keeper{cancel: cancel, done: make(chan struct{})}
	a.keepers[id] = k
	a.keeping.Add(1)
	go a.runKeeper(kctx, id, dir, k)
}

// runKeeper keeps the holder in dir attached until k's context ends, the holder ends, or a later attach replaces the keeper.
func (a *Agent) runKeeper(ctx context.Context, id, dir string, k *keeper) {
	defer a.keeping.Done()
	defer close(k.done)
	defer k.cancel()
	defer a.forget(id, k)
	kept, err := holder.Keep(ctx, dir, a.cfg.Scope)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, holder.ErrGone) {
			a.cfg.Logger.WarnContext(ctx, "node: keep a holder", "session", id, "err", err)
		}
		return
	}
	a.cfg.Logger.InfoContext(ctx, keptMessage, "session", id)
	<-kept.Done()
	if err = kept.Err(); err != nil {
		a.cfg.Logger.WarnContext(ctx, "node: keeper ended", "session", id, "err", err)
	}
}

// forget drops k as the keeper of session id, if it still is one.
func (a *Agent) forget(id string, k *keeper) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keepers[id] == k {
		delete(a.keepers, id)
	}
}

// beginSplice opens a splice on session id. It ends the session's keeper, if one runs, and returns once that keeper has
// ended, so that the keeper's attach never replaces the splice's.
func (a *Agent) beginSplice(id string) {
	a.mu.Lock()
	a.splices[id]++
	k := a.keepers[id]
	delete(a.keepers, id)
	a.mu.Unlock()
	if k != nil {
		k.cancel()
		<-k.done
	}
}

// endSplice closes a splice on session id, and then keeps the session's holder attached again.
func (a *Agent) endSplice(ctx context.Context, id string) {
	a.mu.Lock()
	a.splices[id]--
	if a.splices[id] == 0 {
		delete(a.splices, id)
	}
	a.mu.Unlock()
	a.keep(ctx, id)
}
