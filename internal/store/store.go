// Package store owns orreryd's SQLite store: the file and its connection (its path, its private file modes, WAL mode
// and embedded migrations), and the reads and writes of its tables, among them epics, node runs, reviews, steps and
// notices.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrTooNew reports a database whose recorded migration version is above the newest migration embedded in
// this binary. Open returns it without migrating the database.
var ErrTooNew = errors.New("store schema is newer than this binary")

// readConns is how many connections the store's read pool opens at most. Reads run on that pool, so a read waits for
// one of these connections and never for the writer.
const readConns = 4

// Store is an open orrery SQLite store.
type Store struct {
	// db is the writer: the one connection every write runs on, in turn (writeDB).
	db        *pool
	connector *trackedConnector
	// read is the read pool every read runs on. Its connections open the file read-only, so a read sees each committed
	// write and never waits for the writer's lock.
	read          *pool
	readConnector *trackedConnector

	liveMu     sync.Mutex
	engineLive func() []string

	// callerEvent is the hook OnCallerEvent sets, nil when none is set.
	callerEvent atomic.Pointer[func()]
	// epicsChanged is the hook OnEpicsChanged sets, nil when none is set.
	epicsChanged atomic.Pointer[func(epics []string)]
	// sessionEvents is the hook OnSessionEvents sets, nil when none is set.
	sessionEvents atomic.Pointer[func(seq int64, sessions []string)]
	// epicStored, epicEnded, firstPrompt and turnEnded are the hooks OnEpicStored, OnEpicEnded, OnFirstPrompt and
	// OnTurnEnded set, nil when none is set.
	epicStored  atomic.Pointer[func(epic string)]
	epicEnded   atomic.Pointer[func(epic string)]
	firstPrompt atomic.Pointer[func(session string)]
	turnEnded   atomic.Pointer[func(session string)]
}

// SetEngineLive sets the function that SyncServer's prune calls, once per run, for the ids of the engine
// sessions that are live. The prune keeps those sessions and their engine text. It is safe to call while
// SyncServer runs on other goroutines.
func (s *Store) SetEngineLive(live func() []string) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	s.engineLive = live
}

// engineLiveIDs calls the function set by SetEngineLive, outside the lock, and returns its ids. It returns a
// non-nil empty slice when no function is set or it returns none, so the ids always encode as a JSON array.
func (s *Store) engineLiveIDs() []string {
	s.liveMu.Lock()
	live := s.engineLive
	s.liveMu.Unlock()
	ids := []string{}
	if live != nil {
		ids = append(ids, live()...)
	}
	return ids
}

// OnCallerEvent sets fn as the hook that a store write creating an epic or contract caller event calls. Such a write
// inserts an epic question, sets an epic's phase, stops a release, or records an accept, a set aside or a reopen of a
// contract, and it calls the hook once, synchronously, right after it commits. A write that rolls back or fails to
// commit calls nothing. A nil fn clears the hook. It is safe to call while writes run.
func (s *Store) OnCallerEvent(fn func()) {
	if fn == nil {
		s.callerEvent.Store(nil)
		return
	}
	s.callerEvent.Store(&fn)
}

// fireCallerEvent calls the hook OnCallerEvent set, when one is set. It takes no store lock.
func (s *Store) fireCallerEvent() {
	if fn := s.callerEvent.Load(); fn != nil {
		(*fn)()
	}
}

// OnEpicStored sets fn as the hook that a store write creating an epic calls, once, with the epic's id, synchronously,
// right after it commits and its connection is free again. A write that rolls back or fails to commit calls nothing. A
// nil fn clears the hook. It is safe to call while writes run.
func (s *Store) OnEpicStored(fn func(epic string)) {
	if fn == nil {
		s.epicStored.Store(nil)
		return
	}
	s.epicStored.Store(&fn)
}

// OnEpicEnded sets fn as the hook that a store write setting an epic to a phase ListUnreportedEpics reports calls, once
// for the epic, with its id, synchronously, right after it commits and its connection is free again. A write of any
// other phase, and a write that rolls back or fails to commit, calls nothing. A nil fn clears the hook. It is safe to
// call while writes run.
func (s *Store) OnEpicEnded(fn func(epic string)) {
	if fn == nil {
		s.epicEnded.Store(nil)
		return
	}
	s.epicEnded.Store(&fn)
}

// OnFirstPrompt sets fn as the hook that a store write appending an engine session's first prompt calls, once, with
// the session's id, synchronously, right after it commits and its connection is free again. A later prompt, a prompt of
// a step session, and a write that rolls back or fails to commit call nothing. A nil fn clears the hook. It is safe to
// call while writes run.
func (s *Store) OnFirstPrompt(fn func(session string)) {
	if fn == nil {
		s.firstPrompt.Store(nil)
		return
	}
	s.firstPrompt.Store(&fn)
}

// OnTurnEnded sets fn as the hook that a store write appending an engine session's turn end calls, once for each
// session it ends a turn of, with the session's id, synchronously, right after it commits and its connection is free
// again. A step session's turn end, and a write that rolls back or fails to commit, call nothing. A nil fn clears the
// hook. It is safe to call while writes run.
func (s *Store) OnTurnEnded(fn func(session string)) {
	if fn == nil {
		s.turnEnded.Store(nil)
		return
	}
	s.turnEnded.Store(&fn)
}

// fireHousekeep calls, in order, the hook each mark names with the mark's id, when that hook is set. It takes no store
// lock.
func (s *Store) fireHousekeep(marks []housekeepMark) {
	for _, mark := range marks {
		switch mark.hook {
		case hookEpicStored:
			if fn := s.epicStored.Load(); fn != nil {
				(*fn)(mark.id)
			}
		case hookEpicEnded:
			if fn := s.epicEnded.Load(); fn != nil {
				(*fn)(mark.id)
			}
		case hookFirstPrompt:
			if fn := s.firstPrompt.Load(); fn != nil {
				(*fn)(mark.id)
			}
		case hookTurnEnded:
			if fn := s.turnEnded.Load(); fn != nil {
				(*fn)(mark.id)
			}
		}
	}
}

// Path resolves the store file: $XDG_STATE_HOME/orrery/orrery.db when XDG_STATE_HOME is set,
// else $HOME/.local/state/orrery/orrery.db. It returns an error when both are unset.
func Path(getenv func(string) string) (string, error) {
	if stateHome := getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "orrery", "orrery.db"), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", "orrery", "orrery.db"), nil
	}
	return "", fmt.Errorf("no store path: set XDG_STATE_HOME or HOME")
}

// Synchronous is an SQLite synchronous mode for the store's connection.
type Synchronous string

// The synchronous modes WithSynchronous accepts.
const (
	SynchronousOff    Synchronous = "OFF"
	SynchronousNormal Synchronous = "NORMAL"
	SynchronousFull   Synchronous = "FULL"
)

// BusyTimeout is how long a store write waits for SQLite's write lock while another connection holds it, before it
// fails with SQLITE_BUSY. The other writer of orreryd's store is Litestream, which takes the write lock for its
// checkpoints. Over a large store those holds outlast 200 ms often enough to fail writes, and Litestream recommends a
// 5 s busy timeout for the processes that share its database. Writes run on the store's one writer in turn, so the
// writes behind one wait out Litestream's hold whatever the timeout is: the timeout decides only whether the write at
// the front fails. Reads run on the read pool, so a write queued on the writer does not hold them up.
const BusyTimeout = 5 * time.Second

// Option changes how Open opens the store.
type Option func(*openConfig)

type openConfig struct {
	synchronous Synchronous
	// busyTimeout is how long a statement waits for a lock another connection holds. It is BusyTimeout unless
	// WithBusyTimeout set another.
	busyTimeout time.Duration
	logger      *slog.Logger
	slowCounted func(context.Context)
	// connectGate and closeWaiting are the trackedConnector's gate and waiting. They are for tests and nil otherwise.
	connectGate  func(context.Context)
	closeWaiting func()
	// callClock is the clock store calls are timed on. It is for tests, and time.Now otherwise.
	callClock func() time.Time
}

// WithLogger sets the logger the store warns on for each call that takes longer than 100 ms, from the call until the
// connection it runs on is free again: the writer's for a write, one of the read pool's for a read. The warning names
// the call's queries and splits its time into the wait for the connection and the run on it. Without WithLogger the
// warnings are discarded.
func WithLogger(logger *slog.Logger) Option {
	return func(c *openConfig) { c.logger = logger }
}

// WithSlowCallCounter sets counted, which the store runs once for each call that takes longer than 100 ms, after its
// warning (see WithLogger).
func WithSlowCallCounter(counted func(context.Context)) Option {
	return func(c *openConfig) { c.slowCounted = counted }
}

// withCallClock sets the clock store calls are timed on, in place of time.Now. It is for tests.
func withCallClock(now func() time.Time) Option {
	return func(c *openConfig) { c.callClock = now }
}

// WithBusyTimeout sets how long a store statement waits for a lock another connection holds, in place of BusyTimeout.
// It is for tests: 0 fails a write at once with SQLITE_BUSY while a test's second connection holds the write lock, so
// the test waits out no timeout.
func WithBusyTimeout(d time.Duration) Option {
	return func(c *openConfig) { c.busyTimeout = d }
}

// WithSynchronous sets the connection's synchronous mode in place of SQLite's default, FULL. It is for tests: OFF
// skips the fsync each commit and checkpoint costs, which a test's throwaway store does not need. orreryd never
// passes it, because restart and re-adoption rely on committed rows surviving an OS crash or power loss.
func WithSynchronous(mode Synchronous) Option {
	return func(c *openConfig) { c.synchronous = mode }
}

// Open creates the store's directory (0700) and file (0600) when absent, opens the database in WAL mode with a
// BusyTimeout busy timeout, and applies the embedded migrations. Its writer is the one connection writes run on, and
// its read pool opens the file read-only, with the same busy timeout, for every read. It returns ErrTooNew, without
// migrating, when the database records a newer migration than this binary embeds. A file it cannot use is left
// untouched.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	cfg := openConfig{busyTimeout: BusyTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.busyTimeout < 0 {
		return nil, fmt.Errorf("open store: negative busy timeout %v", cfg.busyTimeout)
	}
	query := "_busy_timeout=" + strconv.FormatInt(cfg.busyTimeout.Milliseconds(), 10) + "&_journal_mode=WAL"
	switch cfg.synchronous {
	case "":
	case SynchronousOff, SynchronousNormal, SynchronousFull:
		query += "&_synchronous=" + string(cfg.synchronous)
	default:
		return nil, fmt.Errorf("open store: unknown synchronous mode %q", cfg.synchronous)
	}

	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	file, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close store file: %w", err)
	}

	dsn := (&url.URL{Scheme: "file", Path: clean, RawQuery: query}).String()
	inner, err := sqlite.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	readInner, err := sqlite.NewConnector(readOnlyDSNBusy(clean, cfg.busyTimeout.Milliseconds()))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	connector := &trackedConnector{inner: inner, gate: cfg.connectGate, waiting: cfg.closeWaiting}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	readConnector := &trackedConnector{inner: readInner, readPool: true}
	readDB := sql.OpenDB(readConnector)
	readDB.SetMaxOpenConns(readConns)
	readDB.SetMaxIdleConns(readConns)
	slow := &slowCalls{now: cfg.callClock, logger: cfg.logger, counted: cfg.slowCounted}
	if slow.now == nil {
		slow.now = time.Now
	}
	if slow.logger == nil {
		slow.logger = slog.New(slog.DiscardHandler)
	}
	opened := &Store{
		db:            &pool{DB: db, slow: slow},
		connector:     connector,
		read:          &pool{DB: readDB, slow: slow},
		readConnector: readConnector,
	}
	if err = opened.migrate(ctx); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	connector.counting.Store(true)
	return opened, nil
}

// Close closes the store and returns once every connection it opened is closed, so the database's files are settled
// when it returns. database/sql opens a connection on its own goroutine when one is discarded, an interrupted one for
// instance, while another call waits for it, and DB.Close does not wait for that open. Close refuses new connections on
// both pools first, then closes the read pool and waits for its connections, then closes the writer and waits for its
// connection, which stays the store's last one, so its close removes the -wal and -shm files. A connection still open
// closes as its call returns it. It returns an error matching ErrEpicWriteOutsideWrite, naming the tables, when a row
// was written on one of the store's connections outside a store write to a table that names an epic or that no store
// write had touched (epicMoves), since such a write may have moved no epic's revision.
func (s *Store) Close() error {
	outside := s.outsideWrites()
	s.readConnector.beginClose()
	s.connector.beginClose()
	readErr := s.read.Close()
	s.readConnector.wait()
	err := s.db.Close()
	s.connector.wait()
	if readErr != nil {
		readErr = fmt.Errorf("close store reads: %w", readErr)
	}
	if err != nil {
		err = fmt.Errorf("close store: %w", err)
	}
	err = errors.Join(readErr, err)
	if outside != "" {
		err = errors.Join(err, fmt.Errorf("%w: %s", ErrEpicWriteOutsideWrite, outside))
	}
	return err
}

func (s *Store) migrate(ctx context.Context) error {
	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}
	// The provider does not own s.db: Provider.Close would close the store, so it is never called.
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db.DB, migrationsFS)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	var newest int64
	for _, src := range provider.ListSources() {
		newest = max(newest, src.Version)
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read store version: %w", err)
	}
	if current > newest {
		return fmt.Errorf("%w: store version %d, newest migration %d", ErrTooNew, current, newest)
	}

	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("run store migrations: %w", err)
	}
	return nil
}
