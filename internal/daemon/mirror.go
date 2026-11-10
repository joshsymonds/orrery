package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// mirrorLockWait bounds how long a sync waits for the mirror lock. A sync that waits longer is dropped.
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
// newer one.
type Mirror struct {
	store  *store.Store
	now    func() time.Time
	logger *slog.Logger
	lock   chan struct{}
}

// NewMirror returns a Mirror that writes to st and stamps events with now. A nil logger discards.
func NewMirror(st *store.Store, now func() time.Time, logger *slog.Logger) *Mirror {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Mirror{store: st, now: now, logger: logger, lock: make(chan struct{}, 1)}
}

// SyncPane syncs pane on server into the store. It reads the pane's @orrery_id and mirrored options,
// mints and stores an id when the pane has none, writes the snapshot to its row, and sets @orrery_id only
// after the store write commits. A sync that waits more than 200 ms for the lock, or whose store write
// fails, is logged and dropped.
func (m *Mirror) SyncPane(ctx context.Context, server tmuxctl.Server, pane string) {
	if !m.acquire() {
		m.logger.WarnContext(ctx, "mirror sync dropped", "pane", pane, "reason", "lock wait exceeded")
		return
	}
	defer m.release()

	if err := m.sync(ctx, server, pane); err != nil {
		m.logger.ErrorContext(ctx, "mirror sync dropped", "pane", pane, "error", err)
	}
}

// acquire takes the mirror lock, waiting at most mirrorLockWait. It reports false when the wait expires.
func (m *Mirror) acquire() bool {
	timer := time.NewTimer(mirrorLockWait)
	defer timer.Stop()
	select {
	case m.lock <- struct{}{}:
		return true
	case <-timer.C:
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
// it. The lock covers the read and the write, so a sync and a tidy never interleave.
func (m *Mirror) Tidy(ctx context.Context, server tmuxctl.Server) {
	if !m.acquire() {
		m.logger.WarnContext(ctx, "mirror tidy dropped", "server", serverValue(server), "reason", "lock wait exceeded")
		return
	}
	defer m.release()

	if err := m.tidy(ctx, server); err != nil {
		m.logger.ErrorContext(ctx, "mirror tidy dropped", "server", serverValue(server), "error", err)
	}
}

// tidy runs one tidy. The caller holds m.lock.
func (m *Mirror) tidy(ctx context.Context, server tmuxctl.Server) error {
	client, err := tmuxctl.New(server)
	if err != nil {
		return fmt.Errorf("resolve tmux server: %w", err)
	}
	out, err := client.Run(ctx, "list-panes", "-a", "-F", tidyFormat)
	if err != nil {
		return fmt.Errorf("list panes: %w", err)
	}
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
