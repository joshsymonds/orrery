package paneregistry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/inotify"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// watchMask is the set of events on the registry directory that can change an entry: its creation, a write in place, a
// move into or out of the directory, and its deletion.
const watchMask = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE

// entryExt is the extension of an entry's name. The <pid>.<hash>.key files beside each entry do not have it.
const entryExt = ".json"

// Event is a change in the panes the registry binds. An event with Unbound false binds Binding.Pane to the session of
// Binding. An event with Unbound true unbinds Binding.Pane, and Binding is the binding it ends.
type Event struct {
	Binding Binding
	Unbound bool
}

// Config is what Watch watches and where it reports. Proc, Events and Unreadable must be set.
type Config struct {
	// Dir is the session registry directory, ~/.claude/sessions.
	Dir string
	// Home is the home directory whose .claude/projects holds the transcripts.
	Home string
	// Proc tells whether a pid is alive, and reads the environment of a process.
	Proc Proc
	// Open creates the inotify instance Watch reads; nil means inotify.New.
	Open func() (*inotify.Inotify, error)
	// Events receives each change to the bound panes, in the order the changes happen.
	Events func(Event)
	// Unreadable receives the path of an entry that Watch could not read, and why. Watch emits nothing for that entry.
	Unreadable func(path string, err error)
	// Logger receives what Watch does beyond its events; nil discards it.
	Logger *slog.Logger
}

// Watch binds the panes of the session registry in cfg.Dir and reports each change through cfg.Events, until ctx is
// done. It reads every entry when it starts, reads an entry again on each event that names it, and rereads the whole
// directory when the kernel reports that it dropped events. Once ctx is done it returns an error that wraps ctx.Err().
// It returns an error too when it cannot create its inotify instance, watch cfg.Dir or read it.
func Watch(ctx context.Context, cfg Config) error {
	open := cfg.Open
	if open == nil {
		open = inotify.New
	}
	ino, err := open()
	if err != nil {
		return fmt.Errorf("paneregistry: open inotify: %w", err)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	watch := &watcher{
		cfg:      cfg,
		ino:      ino,
		logger:   logger,
		files:    os.DirFS(cfg.Dir),
		bindings: make(map[string]bound),
	}
	// Closing the instance ends the read that run waits in. Once ctx is done the callback closes it, and otherwise this
	// function does. Either way the close is run once, and its error reaches this function through closed.
	closed := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { closed <- ino.Close() })
	err = watch.run()
	if stop() {
		closed <- ino.Close()
	}
	if closeErr := <-closed; closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("paneregistry: watch ended: %w", ctx.Err())
	}
	return err
}

// watcher is the state of one Watch: the directory it reads, and the binding each entry holds.
type watcher struct {
	cfg      Config
	ino      *inotify.Inotify
	logger   *slog.Logger
	files    fs.FS
	bindings map[string]bound
	reads    uint64
}

// bound is the binding an entry holds, and the read that made it. A larger seq is a more recent read.
type bound struct {
	binding Binding
	seq     uint64
}

// paneID identifies a tmux pane. A pane id is unique only within its tmux server, so the identity pairs the id with the
// socket of the server: the part of TMUX before its first ','.
type paneID struct {
	socket string
	id     string
}

// paneOf returns the identity of the pane b binds.
func paneOf(binding Binding) paneID {
	socket, _, _ := strings.Cut(binding.Tmux, ",")
	return paneID{socket: socket, id: binding.Pane}
}

// run watches the directory, reads its entries, and then handles events until a read fails.
func (w *watcher) run() error {
	if _, err := w.ino.Add(w.cfg.Dir, watchMask); err != nil {
		return fmt.Errorf("paneregistry: %w", err)
	}
	if err := w.rescan(); err != nil {
		return err
	}
	for {
		batch, readErr := w.ino.Read()
		if readErr != nil {
			return fmt.Errorf("paneregistry: %w", readErr)
		}
		if err := w.apply(batch); err != nil {
			return err
		}
	}
}

// apply handles the events of one read, in order.
func (w *watcher) apply(events []inotify.Event) error {
	for _, event := range events {
		if err := w.handle(event); err != nil {
			return err
		}
	}
	return nil
}

// handle applies one event. An overflow rereads the whole directory, and an event that names an entry rereads it.
func (w *watcher) handle(event inotify.Event) error {
	if event.Mask&unix.IN_Q_OVERFLOW != 0 {
		w.logger.Warn("session registry events were dropped; rereading the directory", "dir", w.cfg.Dir)
		return w.rescan()
	}
	if isEntry(event.Name) {
		w.refresh(event.Name)
	}
	return nil
}

// isEntry reports whether name is the name of a registry entry.
func isEntry(name string) bool {
	return filepath.Ext(name) == entryExt
}

// rescan reads every entry in the directory, as refresh does, and unbinds each bound entry that is no longer there.
func (w *watcher) rescan() error {
	dirents, err := os.ReadDir(w.cfg.Dir)
	if err != nil {
		return fmt.Errorf("paneregistry: read %s: %w", w.cfg.Dir, err)
	}
	present := make(map[string]bool, len(dirents))
	for _, dirent := range dirents {
		if isEntry(dirent.Name()) {
			present[dirent.Name()] = true
		}
	}
	gone := make([]string, 0, len(w.bindings))
	for name := range w.bindings {
		if !present[name] {
			gone = append(gone, name)
		}
	}
	for _, name := range gone {
		w.unbind(name)
	}
	for _, dirent := range dirents {
		if isEntry(dirent.Name()) {
			w.refresh(dirent.Name())
		}
	}
	return nil
}

// refresh reads the entry name, and binds, rebinds or unbinds its pane as the entry and its process now say. An entry
// that cannot be read, or does not parse, emits nothing and keeps the binding it holds.
func (w *watcher) refresh(name string) {
	path := filepath.Join(w.cfg.Dir, name)
	data, err := fs.ReadFile(w.files, name)
	if errors.Is(err, fs.ErrNotExist) {
		w.unbind(name)
		return
	}
	if err != nil {
		w.cfg.Unreadable(path, err)
		return
	}
	entry, err := Parse(data)
	if err != nil {
		w.cfg.Unreadable(path, err)
		return
	}
	binding, binds, err := w.bindingOf(path, entry)
	switch {
	case err != nil:
		w.cfg.Unreadable(path, err)
	case binds:
		w.bind(name, binding)
	default:
		w.unbind(name)
	}
}

// bindingOf returns the binding that the entry at path, parsed as entry, makes, and whether it binds at all. The entry
// binds when its process is alive, the entry is a cli session that names a pane, and the process is neither a session
// orrery started headless nor a summarizer. It fails when the environment of the process cannot be read.
func (w *watcher) bindingOf(path string, entry Entry) (Binding, bool, error) {
	if entry.Entrypoint != EntrypointCLI || !w.cfg.Proc.Alive(entry.PID) {
		return Binding{}, false, nil
	}
	env, err := w.cfg.Proc.Environ(entry.PID)
	if err != nil {
		return Binding{}, false, fmt.Errorf("paneregistry: environment of pid %d: %w", entry.PID, err)
	}
	if env[protocol.EnvSession] != "" || protocol.IsSummarizer(func(key string) string { return env[key] }) {
		return Binding{}, false, nil
	}
	pane := entry.Pane(env)
	if pane == "" {
		return Binding{}, false, nil
	}
	return Binding{
		File:       path,
		PID:        entry.PID,
		Pane:       pane,
		Tmux:       env["TMUX"],
		SessionID:  entry.SessionID,
		Cwd:        entry.Cwd,
		Transcript: TranscriptPath(w.cfg.Home, entry.Cwd, entry.SessionID),
		BaseURL:    env["ANTHROPIC_BASE_URL"],
		Status:     entry.Status,
		WaitingFor: entry.WaitingFor,
	}, true, nil
}

// bind records that the entry name binds binding, and emits it. An entry that moves from another pane releases that
// pane first.
func (w *watcher) bind(name string, binding Binding) {
	old, wasBound := w.bindings[name]
	w.reads++
	w.bindings[name] = bound{binding: binding, seq: w.reads}
	if wasBound && paneOf(old.binding) != paneOf(binding) {
		w.release(old.binding)
	}
	w.cfg.Events(Event{Binding: binding})
}

// unbind drops the binding of the entry name, which is gone or no longer binds, and releases its pane.
func (w *watcher) unbind(name string) {
	old, wasBound := w.bindings[name]
	if !wasBound {
		return
	}
	delete(w.bindings, name)
	w.release(old.binding)
}

// release emits what happens to the pane of gone once an entry stops binding it: the binding of the most recently read
// entry that still binds the pane, or else an unbinding of the pane.
func (w *watcher) release(gone Binding) {
	target := paneOf(gone)
	var next bound
	found := false
	for _, other := range w.bindings {
		if paneOf(other.binding) == target && (!found || other.seq > next.seq) {
			next, found = other, true
		}
	}
	if found {
		w.cfg.Events(Event{Binding: next.binding})
		return
	}
	w.cfg.Events(Event{Binding: gone, Unbound: true})
}
