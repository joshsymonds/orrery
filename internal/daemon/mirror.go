package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// mirrorLockWait bounds how long a sync or tidy waits for the store to open and then for the mirror lock, so a hook
// never waits on the mirror for longer. One that waits longer is deferred: it runs once the store is open and the lock
// is free.
const mirrorLockWait = 200 * time.Millisecond

// mirrorSeparator splits the one-command option read. tmux prints it raw, and it cannot appear in a
// single-line option value.
const mirrorSeparator = "\x1f"

// mirrorFieldCount is the number of fields mirrorFormat prints: @orrery_id and the ten mirrored options.
const mirrorFieldCount = 11

// mirrorFormat is the display-message format that prints @orrery_id, then the ten mirrored options in the
// order of store.Fields, separated by mirrorSeparator.
const mirrorFormat = "#{" + protocol.OptionID + "}" + mirrorSeparator +
	"#{" + protocol.OptionState + "}" + mirrorSeparator +
	"#{" + protocol.OptionChanged + "}" + mirrorSeparator +
	"#{" + protocol.OptionSeen + "}" + mirrorSeparator +
	"#{" + protocol.OptionQuestion + "}" + mirrorSeparator +
	"#{" + protocol.OptionSummary + "}" + mirrorSeparator +
	"#{" + protocol.OptionLabel + "}" + mirrorSeparator +
	"#{" + protocol.OptionContext + "}" + mirrorSeparator +
	"#{" + protocol.OptionTool + "}" + mirrorSeparator +
	"#{" + protocol.OptionClaudeSession + "}" + mirrorSeparator +
	"#{" + protocol.OptionTranscript + "}"

// tidyFieldCount is the number of fields tidyFormat prints: the pane id, then mirrorFormat's fields.
const tidyFieldCount = mirrorFieldCount + 1

// tidyFormat is the list-panes format for a tidy. It prints each pane's id, then mirrorFormat's fields, separated by
// mirrorSeparator.
const tidyFormat = "#{pane_id}" + mirrorSeparator + mirrorFormat

// Mirror syncs tmux panes into the store. Each sync holds one lock across its tmux read, its id mint and
// its store write, so concurrent syncs never mint two ids for one pane or write a stale snapshot over a
// newer one. A sync or tidy that cannot start within mirrorLockWait is deferred, and one drain runs the deferred work
// once the store is open and the lock is free.
type Mirror struct {
	// store is set by Attach before opened closes, so it is read only after a receive from opened.
	store  *store.Store
	opened chan struct{}
	now    func() time.Time
	logger *slog.Logger
	lock   chan struct{}
	// lockWait returns the channel that delivers once a sync or tidy has waited mirrorLockWait. It is time.After of
	// mirrorLockWait; a test replaces it.
	lockWait func() <-chan time.Time
	// unreachable holds each server whose last list-panes failed, keyed by its store column. Only the lock
	// holder reads or writes it.
	unreachable map[string]bool

	// deferredMu guards deferredPanes, deferredServers and draining.
	deferredMu sync.Mutex
	// deferredPanes holds each pane whose sync was deferred. A pane deferred again before the drain reaches it is one
	// entry, so its sync runs once and reads tmux as it is then.
	deferredPanes map[paneRef]struct{}
	// deferredServers holds each server whose tidy was deferred.
	deferredServers map[tmuxctl.Server]struct{}
	// draining is true while a drain goroutine runs. At most one runs, and it runs until nothing is deferred.
	draining bool
	// drains counts the running drain, so Wait can wait for it.
	drains sync.WaitGroup
}

// paneRef names one pane on one tmux server.
type paneRef struct {
	server tmuxctl.Server
	pane   string
}

// NewMirror returns a Mirror that writes to st and stamps events with now. A nil logger discards.
func NewMirror(st *store.Store, now func() time.Time, logger *slog.Logger) *Mirror {
	m := NewPendingMirror(now, logger)
	m.Attach(st)
	return m
}

// NewPendingMirror returns a Mirror whose store is still opening. Until Attach, a sync or tidy waits at most
// mirrorLockWait for it, and one that waits longer runs after Attach. A nil logger discards.
func NewPendingMirror(now func() time.Time, logger *slog.Logger) *Mirror {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Mirror{
		opened:      make(chan struct{}),
		now:         now,
		logger:      logger,
		lock:        make(chan struct{}, 1),
		lockWait:    func() <-chan time.Time { return time.After(mirrorLockWait) },
		unreachable: map[string]bool{},

		deferredPanes:   map[paneRef]struct{}{},
		deferredServers: map[tmuxctl.Server]struct{}{},
	}
}

// Attach gives the mirror the store opened for it, or nil when the open failed. It releases every sync and tidy
// waiting for the store, and the drain of those deferred while it opened; a nil store makes all later syncs and tidies
// return at once, silently, and discards the deferred ones. Attach is called once.
func (m *Mirror) Attach(st *store.Store) {
	m.store = st
	close(m.opened)
}

// awaitStore waits at most mirrorLockWait for Attach. It reports false when the wait expires.
func (m *Mirror) awaitStore() bool {
	select {
	case <-m.opened:
		return true
	default:
	}
	// The wait is armed only once the store is found still opening.
	select {
	case <-m.opened:
		return true
	case <-m.lockWait():
		return false
	}
}

// SyncPane syncs pane on server into the store. It reads the pane's @orrery_id and mirrored options,
// mints and stores an id when the pane has none, writes the snapshot to its row, and sets @orrery_id only
// after the store write commits. A sync that waits more than mirrorLockWait for the store to open or for the lock is
// logged and deferred, and SyncPane returns: the drain syncs the pane once the store is open and the lock is free. A
// sync whose tmux read or store write fails is logged and dropped. A sync after a failed open returns at once,
// silently.
func (m *Mirror) SyncPane(ctx context.Context, server tmuxctl.Server, pane string) {
	if !m.awaitStore() {
		m.logger.WarnContext(ctx, "mirror sync deferred", "pane", pane, "reason", "store still opening")
		m.deferSync(ctx, server, pane)
		return
	}
	if m.store == nil {
		return
	}
	if !m.acquire() {
		m.logger.WarnContext(ctx, "mirror sync deferred", "pane", pane, "reason", "lock wait exceeded")
		m.deferSync(ctx, server, pane)
		return
	}
	defer m.release()
	m.syncLocked(ctx, server, pane)
}

// syncLocked runs one pane sync and logs its failure. The caller holds m.lock.
func (m *Mirror) syncLocked(ctx context.Context, server tmuxctl.Server, pane string) {
	if err := m.sync(ctx, server, pane); err != nil {
		m.logger.ErrorContext(ctx, "mirror sync dropped", "pane", pane, "error", err)
	}
}

// deferSync records pane on server for the drain, and starts the drain when none runs.
func (m *Mirror) deferSync(ctx context.Context, server tmuxctl.Server, pane string) {
	m.deferredMu.Lock()
	defer m.deferredMu.Unlock()
	m.deferredPanes[paneRef{server: server, pane: pane}] = struct{}{}
	m.startDrainLocked(ctx)
}

// deferTidy records server for the drain, and starts the drain when none runs.
func (m *Mirror) deferTidy(ctx context.Context, server tmuxctl.Server) {
	m.deferredMu.Lock()
	defer m.deferredMu.Unlock()
	m.deferredServers[server] = struct{}{}
	m.startDrainLocked(ctx)
}

// startDrainLocked starts the drain goroutine unless one runs. A running drain stops only when takeDeferred finds
// nothing recorded, and it clears draining under deferredMu, so every deferral is taken by a drain. The drain keeps
// ctx's values but not its cancel, since the deferring caller's context ends when it returns; orreryd waits for the
// drain with Wait before it closes the store. The caller holds deferredMu.
func (m *Mirror) startDrainLocked(ctx context.Context) {
	if m.draining {
		return
	}
	m.draining = true
	m.drains.Add(1)
	go m.drain(context.WithoutCancel(ctx))
}

// drain runs the deferred tidies and syncs under ctx. It waits, with no bound, for Attach and then for the mirror lock,
// since it runs on its own goroutine and keeps no hook waiting. Under the lock it takes every deferral recorded so far
// and runs it: each tidy, then each sync, each reading tmux as it is then. It repeats until it finds nothing deferred.
// A nil store discards the deferrals.
func (m *Mirror) drain(ctx context.Context) {
	defer m.drains.Done()
	<-m.opened
	for {
		if m.store == nil {
			if _, _, ok := m.takeDeferred(); !ok {
				return
			}
			continue
		}
		m.lock <- struct{}{}
		panes, servers, ok := m.takeDeferred()
		for server := range servers {
			m.tidyLocked(ctx, server)
		}
		for ref := range panes {
			m.syncLocked(ctx, ref.server, ref.pane)
		}
		m.release()
		if !ok {
			return
		}
	}
}

// takeDeferred takes every deferral recorded so far. It reports false when there was none, and then clears draining
// under the same lock, so a later deferral starts a new drain.
func (m *Mirror) takeDeferred() (map[paneRef]struct{}, map[tmuxctl.Server]struct{}, bool) {
	m.deferredMu.Lock()
	defer m.deferredMu.Unlock()
	panes, servers := m.deferredPanes, m.deferredServers
	if len(panes) == 0 && len(servers) == 0 {
		m.draining = false
		return nil, nil, false
	}
	m.deferredPanes = map[paneRef]struct{}{}
	m.deferredServers = map[tmuxctl.Server]struct{}{}
	return panes, servers, true
}

// Wait returns once no drain runs, so every sync and tidy deferred before the call has run. orreryd calls it after
// Serve and the summaries have returned, when nothing defers more, and before it closes the store. Wait never
// returns while the store is still opening and work is deferred: orreryd attaches the store before it serves.
func (m *Mirror) Wait() {
	m.drains.Wait()
}

// acquire takes the mirror lock, waiting at most mirrorLockWait. It reports false when the wait expires.
func (m *Mirror) acquire() bool {
	select {
	case m.lock <- struct{}{}:
		return true
	default:
	}
	// The wait is armed only once the lock is found held.
	select {
	case m.lock <- struct{}{}:
		return true
	case <-m.lockWait():
		return false
	}
}

// release frees the mirror lock that acquire took.
func (m *Mirror) release() { <-m.lock }

// sync runs one pane sync. The caller holds m.lock.
func (m *Mirror) sync(ctx context.Context, server tmuxctl.Server, pane string) error {
	client, err := tmuxctl.New(server)
	if err != nil {
		return fmt.Errorf("resolve tmux server: %w", err)
	}
	out, err := client.Run(ctx, "display-message", "-p", "-t", pane, mirrorFormat)
	if err != nil {
		return fmt.Errorf("read pane %s options: %w", pane, err)
	}
	values := strings.Split(strings.TrimSuffix(out, "\n"), mirrorSeparator)
	if len(values) != mirrorFieldCount {
		return fmt.Errorf("read pane %s options: got %d fields, want %d", pane, len(values), mirrorFieldCount)
	}

	id := values[0]
	minted := id == ""
	if minted {
		if id, err = mintID(); err != nil {
			return err
		}
	}
	if err = m.store.SyncPane(ctx, serverValue(server), store.Pane{
		ID: id, Pane: pane, Fields: fieldsOf(values[1:]),
	}, m.now()); err != nil {
		return fmt.Errorf("sync pane %s: %w", pane, err)
	}
	if minted {
		if err = client.SetPaneOption(ctx, pane, protocol.OptionID, id); err != nil {
			return fmt.Errorf("set pane %s id: %w", pane, err)
		}
	}
	return nil
}

// mintID returns 128 random bits from crypto/rand in lowercase hex.
func mintID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint pane id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// serverValue returns the store's server column for server: "-L <name>", "-S <path>", or "" for the default.
func serverValue(server tmuxctl.Server) string {
	switch {
	case server.Name != "":
		return "-L " + server.Name
	case server.Socket != "":
		return "-S " + server.Socket
	default:
		return ""
	}
}

// fieldsOf maps the ten mirrored option values, in mirrorFormat order after @orrery_id, to store.Fields.
func fieldsOf(values []string) store.Fields {
	return store.Fields{
		State:         values[0],
		Changed:       values[1],
		Seen:          values[2],
		Question:      values[3],
		Summary:       values[4],
		Label:         values[5],
		Context:       values[6],
		Tool:          values[7],
		ClaudeSession: values[8],
		Transcript:    values[9],
	}
}

// Tidy matches the store to server's panes: one list-panes -a reads every pane, and SyncServer replaces the
// server's rows with them. A pane without an @orrery_id gets one minted before the store write, and the option is
// set only after the write commits. A failed read or write changes nothing; it is logged, and the next tidy repairs
// it. A failed list-panes is logged as a server transition instead: see noteListPanes. The lock covers the read and
// the write, so a sync and a tidy never interleave. Like a sync, a tidy that waits more than mirrorLockWait for a store
// still opening or for the lock is logged and deferred to the drain, and a tidy returns at once after a failed open.
func (m *Mirror) Tidy(ctx context.Context, server tmuxctl.Server) {
	if !m.awaitStore() {
		m.logger.WarnContext(ctx, "mirror tidy deferred",
			"server", serverValue(server), "reason", "store still opening")
		m.deferTidy(ctx, server)
		return
	}
	if m.store == nil {
		return
	}
	if !m.acquire() {
		m.logger.WarnContext(ctx, "mirror tidy deferred", "server", serverValue(server), "reason", "lock wait exceeded")
		m.deferTidy(ctx, server)
		return
	}
	defer m.release()
	m.tidyLocked(ctx, server)
}

// tidyLocked runs one tidy of server and logs its failure. The caller holds m.lock.
func (m *Mirror) tidyLocked(ctx context.Context, server tmuxctl.Server) {
	client, err := tmuxctl.New(server)
	if err != nil {
		m.logger.ErrorContext(ctx, "mirror tidy dropped", "server", serverValue(server),
			"error", fmt.Errorf("resolve tmux server: %w", err))
		return
	}
	out, err := client.Run(ctx, "list-panes", "-a", "-F", tidyFormat)
	m.noteListPanes(ctx, server, err)
	if err != nil {
		return
	}
	if err = m.tidy(ctx, server, client, out); err != nil {
		m.logger.ErrorContext(ctx, "mirror tidy dropped", "server", serverValue(server), "error", err)
	}
}

// tidy applies one list-panes listing out to the store. The caller holds m.lock.
func (m *Mirror) tidy(ctx context.Context, server tmuxctl.Server, client *tmuxctl.Client, out string) error {
	panes, err := parseTidyPanes(out)
	if err != nil {
		return err
	}

	var minted []store.Pane
	for i := range panes {
		if panes[i].ID != "" {
			continue
		}
		if panes[i].ID, err = mintID(); err != nil {
			return err
		}
		minted = append(minted, panes[i])
	}
	if err = m.store.SyncServer(ctx, serverValue(server), panes, m.now()); err != nil {
		return fmt.Errorf("sync server %s: %w", serverValue(server), err)
	}

	var errs []error
	for _, pane := range minted {
		if setErr := client.SetPaneOption(ctx, pane.Pane, protocol.OptionID, pane.ID); setErr != nil {
			errs = append(errs, fmt.Errorf("set pane %s id: %w", pane.Pane, setErr))
		}
	}
	return errors.Join(errs...)
}

// noteListPanes logs a server's list-panes outcome when it changes that server's state. A failure logs
// "tmux server unreachable" when the previous list-panes on the server succeeded, and nothing while it keeps
// failing. The first success after a failure logs "tmux server reachable again". The caller holds m.lock.
func (m *Mirror) noteListPanes(ctx context.Context, server tmuxctl.Server, err error) {
	key := serverValue(server)
	switch {
	case err != nil:
		if m.unreachable[key] {
			return
		}
		m.unreachable[key] = true
		m.logger.WarnContext(ctx, "tmux server unreachable", "server", key, "error", err)
	case m.unreachable[key]:
		delete(m.unreachable, key)
		m.logger.InfoContext(ctx, "tmux server reachable again", "server", key)
	}
}

// parseTidyPanes decodes list-panes -a output printed with tidyFormat, one pane per line. The pane's @orrery_id
// is empty when the pane has none.
func parseTidyPanes(out string) ([]store.Pane, error) {
	var panes []store.Pane
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		values := strings.Split(line, mirrorSeparator)
		if len(values) != tidyFieldCount {
			return nil, fmt.Errorf("read panes: got %d fields, want %d", len(values), tidyFieldCount)
		}
		panes = append(panes, store.Pane{ID: values[1], Pane: values[0], Fields: fieldsOf(values[2:])})
	}
	return panes, nil
}
