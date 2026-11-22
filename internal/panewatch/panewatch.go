// Package panewatch binds the panes of Claude Code's session registry to their transcripts. Run watches the transcript of
// each bound pane with inotify and sends the daemon a pane update for each change of the pane's registry entry or its
// transcript, so a pane's running tool, turn start and context usage come from the transcript with no client involved.
package panewatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/inotify"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/panetranscript"
)

// dirEvents are the inotify events of a transcript's directory that concern the transcript's name: its creation or a
// move into place, which replace the transcript, and its removal or a move out, which leave no transcript to read.
const dirEvents = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_ONLYDIR

// fileEvents are the inotify events of the transcript file: a write, and its removal or move, after which the watch no
// longer follows the path.
const fileEvents = unix.IN_MODIFY | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// noWatch is the watch descriptor of a transcript file that had no watch, because the file did not exist when its
// watch was made.
const noWatch = -1

// Config is what Run watches and where it reports. Proc, Updates and Unreadable must be set.
type Config struct {
	// Dir is the session registry directory, ~/.claude/sessions.
	Dir string
	// Home is the home directory whose .claude/projects holds the transcripts.
	Home string
	// Proc tells whether a pid is alive, and reads the environment of a process.
	Proc paneregistry.Proc
	// Open creates each inotify instance Run reads: the registry's, and one for each bound pane's transcript. Nil means
	// inotify.New.
	Open func() (*inotify.Inotify, error)
	// Updates receives each pane update, in the order the changes happen. Run drops an update it has not handed over once
	// its context is done.
	Updates chan<- daemon.PaneUpdate
	// Unreadable receives the path of a registry entry or a transcript that Run could not read, and why. A transcript
	// read that fails sends no update.
	Unreadable func(path string, err error)
	// Logger receives what Run does beyond its updates, such as a transcript watch it could not open. Nil discards it.
	Logger *slog.Logger
}

// Run binds the panes of the session registry in cfg.Dir, as paneregistry.Watch does, and watches the transcript of each
// bound pane with inotify. It sends a daemon.PaneUpdate on cfg.Updates for each change of a bound pane's registry entry
// or transcript: the entry's fields and the turn the transcript shows. An unbinding sends an update with Unbound set, and
// drops the pane's watch. A transcript that does not exist yet is watched for its creation through its directory. Run
// returns once ctx is done, after it has closed every inotify instance it opened, with the error paneregistry.Watch
// returned.
func Run(ctx context.Context, cfg Config) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	open := cfg.Open
	if open == nil {
		open = inotify.New
	}
	run := &watcher{
		cfg:    cfg,
		logger: logger,
		open:   open,
		events: make(chan paneregistry.Event),
		woke:   make(chan wake),
		panes:  make(map[paneKey]*paneState),
	}
	registry := make(chan error, 1)
	go func() {
		registry <- paneregistry.Watch(ctx, paneregistry.Config{
			Dir:        cfg.Dir,
			Home:       cfg.Home,
			Proc:       cfg.Proc,
			Open:       open,
			Events:     run.forward(ctx),
			Unreadable: cfg.Unreadable,
			Logger:     logger,
		})
	}()
	err := run.loop(ctx, registry)
	return errors.Join(err, run.closeAll())
}

// watcher is the state of one Run: its configuration, the function that opens its inotify instances, the registry events
// and transcript wakes it takes in, and the bound panes it holds. Only the loop goroutine reads or writes panes.
type watcher struct {
	cfg    Config
	logger *slog.Logger
	open   func() (*inotify.Inotify, error)
	events chan paneregistry.Event
	woke   chan wake
	panes  map[paneKey]*paneState
}

// paneKey identifies a pane. A tmux pane id is unique only within its server, so the key pairs the id with the socket of
// the server: the part of TMUX before its first ','.
type paneKey struct {
	socket string
	id     string
}

// keyOf returns the key of the pane that binding binds.
func keyOf(binding paneregistry.Binding) paneKey {
	socket, _, _ := strings.Cut(binding.Tmux, ",")
	return paneKey{socket: socket, id: binding.Pane}
}

// paneState is what Run holds for one bound pane: the binding its registry entry last made, the reader of its transcript,
// and the watch on that transcript while one is up.
type paneState struct {
	binding paneregistry.Binding
	reader  *panetranscript.Reader // nil when the binding names no transcript
	watch   *transcriptWatch       // nil when none is up
}

// wake is what a transcript watch reports to the loop: the watch, and whether its transcript was replaced, so that it
// must be read from its start, rather than changed.
type wake struct {
	watch    *transcriptWatch
	replaced bool
}

// transcriptWatch is the inotify watch of one pane's transcript: its instance, the watches on the transcript's directory
// and on the transcript file, and the goroutine that reports the watch's changes. A watch is made once and never
// re-armed: a transcript that is replaced gets a new watch.
type transcriptWatch struct {
	ino    *inotify.Inotify
	state  *paneState // the pane the watch belongs to, read only by the loop
	pane   string     // the pane id, which the goroutine logs with
	name   string     // the base name of the transcript
	dirWD  int
	fileWD int           // noWatch when the transcript did not exist when the watch was made
	halt   chan struct{} // closed by release before the instance is closed
	ended  chan struct{} // closed when the goroutine has returned
}

// forward returns the Events callback of the registry watch: it hands each event to the loop, or drops it once ctx is done.
func (w *watcher) forward(ctx context.Context) func(paneregistry.Event) {
	return func(event paneregistry.Event) {
		select {
		case w.events <- event:
		case <-ctx.Done():
		}
	}
}

// loop applies the registry's events and the transcript wakes in the order they arrive, until the registry watch ends,
// and returns the error the watch ended with.
func (w *watcher) loop(ctx context.Context, registry <-chan error) error {
	for {
		select {
		case err := <-registry:
			return err
		case event := <-w.events:
			if event.Unbound {
				w.unbind(ctx, event.Binding)
			} else {
				w.bind(ctx, event.Binding)
			}
		case woke := <-w.woke:
			w.onWake(ctx, woke)
		}
	}
}

// bind applies a binding of the registry: the pane follows the transcript the binding names, and its update is sent. A
// pane that is bound to another transcript drops the reader and watch of the one it followed.
func (w *watcher) bind(ctx context.Context, binding paneregistry.Binding) {
	key := keyOf(binding)
	state, known := w.panes[key]
	if !known {
		state = &paneState{}
		w.panes[key] = state
	}
	if !known || state.binding.Transcript != binding.Transcript {
		w.dropWatch(state)
		state.reader = readerFor(binding.Transcript)
	}
	state.binding = binding
	w.arm(state)
	w.publish(ctx, state)
}

// unbind drops the watch of the pane that binding names, and sends the pane's update with Unbound set.
func (w *watcher) unbind(ctx context.Context, binding paneregistry.Binding) {
	key := keyOf(binding)
	if state, ok := w.panes[key]; ok {
		w.dropWatch(state)
		delete(w.panes, key)
	}
	update := updateOf(binding, panetranscript.Turn{})
	update.Unbound = true
	w.send(ctx, update)
}

// onWake handles a change to a pane's transcript that its watch reported. A wake from a watch the pane no longer holds
// is stale, and is ignored. A replaced transcript is read from its start, through a new watch and a new reader.
func (w *watcher) onWake(ctx context.Context, woke wake) {
	state := woke.watch.state
	if state.watch != woke.watch {
		return
	}
	if woke.replaced {
		w.dropWatch(state)
		state.reader = readerFor(state.binding.Transcript)
		w.arm(state)
	}
	w.publish(ctx, state)
}

// arm opens the transcript watch of state when state reads a transcript and holds no watch. A watch that cannot be opened
// is logged, and the pane then reads with no watch until its next registry event arms one.
func (w *watcher) arm(state *paneState) {
	if state.reader == nil || state.watch != nil {
		return
	}
	watch, err := w.watchTranscript(state)
	if err != nil {
		w.logger.Warn(
			"watch transcript",
			"pane",
			state.binding.Pane,
			"transcript",
			state.binding.Transcript,
			"error",
			err,
		)
		return
	}
	state.watch = watch
}

// publish reads the transcript of state and sends the pane's update: the binding's fields and the turn the transcript
// shows. A transcript that does not exist shows no turn. A transcript that cannot be read is reported to Unreadable, and
// no update is sent.
func (w *watcher) publish(ctx context.Context, state *paneState) {
	var turn panetranscript.Turn
	var err error
	if state.reader != nil {
		turn, err = state.reader.Read()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.cfg.Unreadable(state.binding.Transcript, err)
		return
	}
	w.send(ctx, updateOf(state.binding, turn))
}

// send hands update to cfg.Updates, or drops it once ctx is done.
func (w *watcher) send(ctx context.Context, update daemon.PaneUpdate) {
	select {
	case w.cfg.Updates <- update:
	case <-ctx.Done():
	}
}

// dropWatch releases the transcript watch of state, if it has one, and logs a failure to close its instance.
func (w *watcher) dropWatch(state *paneState) {
	if err := w.releaseWatch(state); err != nil {
		w.logger.Warn("close transcript watch", "pane", state.binding.Pane, "error", err)
	}
}

// releaseWatch releases the transcript watch of state, if it has one, and returns the error closing its instance gave.
func (w *watcher) releaseWatch(state *paneState) error {
	if state.watch == nil {
		return nil
	}
	err := state.watch.release()
	state.watch = nil
	return err
}

// closeAll releases the transcript watch of every pane, and returns the errors closing their instances gave.
func (w *watcher) closeAll() error {
	var err error
	for _, state := range w.panes {
		err = errors.Join(err, w.releaseWatch(state))
	}
	return err
}

// watchTranscript makes the watch of the transcript of state: an inotify instance with a watch on the transcript's
// directory, and one on the transcript file when the file exists. It starts the goroutine that reports the watch's
// changes.
func (w *watcher) watchTranscript(state *paneState) (*transcriptWatch, error) {
	path := state.binding.Transcript
	ino, err := w.open()
	if err != nil {
		return nil, fmt.Errorf("panewatch: open inotify: %w", err)
	}
	dirWD, err := ino.Add(filepath.Dir(path), dirEvents)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("panewatch: watch transcript directory: %w", err), ino.Close())
	}
	fileWD, err := ino.Add(path, fileEvents)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// The transcript does not exist yet. Its creation shows in the directory's watch, which replaces this watch.
		fileWD = noWatch
	case err != nil:
		return nil, errors.Join(fmt.Errorf("panewatch: watch transcript: %w", err), ino.Close())
	}
	watch := &transcriptWatch{
		ino:    ino,
		state:  state,
		pane:   state.binding.Pane,
		name:   filepath.Base(path),
		dirWD:  dirWD,
		fileWD: fileWD,
		halt:   make(chan struct{}),
		ended:  make(chan struct{}),
	}
	go w.readEvents(watch)
	return watch, nil
}

// readEvents takes the events of watch's instance until a read fails, and sends a wake to the loop for each batch that
// changes the transcript. Releasing the watch fails the read. Any other read failure is logged, and then the watch reports
// no more changes.
func (w *watcher) readEvents(watch *transcriptWatch) {
	defer close(watch.ended)
	for {
		events, err := watch.ino.Read()
		if err != nil {
			select {
			case <-watch.halt:
			default:
				w.logger.Warn("read transcript watch", "pane", watch.pane, "error", err)
			}
			return
		}
		replaced, changed := watch.classify(events)
		if !replaced && !changed {
			continue
		}
		select {
		case w.woke <- wake{watch: watch, replaced: replaced}:
		case <-watch.halt:
			return
		}
	}
}

// classify reports whether events replace the transcript, so that it must be read from its start, and whether they change
// it at all. A queue overflow may have lost any change, so it replaces the transcript.
func (t *transcriptWatch) classify(events []inotify.Event) (replaced, changed bool) {
	for _, event := range events {
		switch {
		case event.Mask&unix.IN_Q_OVERFLOW != 0:
			replaced, changed = true, true
		case event.WD == t.dirWD && event.Name == t.name && event.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0:
			replaced, changed = true, true
		case event.WD == t.dirWD && event.Name == t.name:
			changed = true
		case event.WD == t.fileWD:
			changed = true
		}
	}
	return replaced, changed
}

// release stops the goroutine's wakes, closes the instance, which ends its read, and returns once the goroutine has
// returned. It returns the error closing the instance gave.
func (t *transcriptWatch) release() error {
	close(t.halt)
	err := t.ino.Close()
	<-t.ended
	if err != nil {
		return fmt.Errorf("panewatch: release transcript watch: %w", err)
	}
	return nil
}

// readerFor returns a reader of the transcript at path, or nil when path is empty, since a binding that names no
// transcript has none to read.
func readerFor(path string) *panetranscript.Reader {
	if path == "" {
		return nil
	}
	return panetranscript.NewReader(path)
}

// updateOf returns the pane update of binding, with turn as its turn.
func updateOf(binding paneregistry.Binding, turn panetranscript.Turn) daemon.PaneUpdate {
	return daemon.PaneUpdate{
		PaneID:     binding.Pane,
		Tmux:       binding.Tmux,
		SessionID:  binding.SessionID,
		Cwd:        binding.Cwd,
		BaseURL:    binding.BaseURL,
		Transcript: binding.Transcript,
		Status:     binding.Status,
		WaitingFor: binding.WaitingFor,
		Turn:       turn,
	}
}
