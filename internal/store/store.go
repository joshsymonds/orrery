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

// Store is an open orrery SQLite store.
type Store struct {
	db        *pool
	connector *trackedConnector

	liveMu     sync.Mutex
	engineLive func() []string

	// callerEvent is the hook OnCallerEvent sets, nil when none is set.
	callerEvent atomic.Pointer[func()]
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

// OnCallerEvent sets fn as the hook that a store write creating an epic caller event calls. Such a write inserts an
// epic question, sets an epic's phase, or stops a release, and it calls the hook once, synchronously, right after it
// commits. A write that rolls back or fails to commit calls nothing. A nil fn clears the hook. It is safe to call while
// writes run.
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

// Option changes how Open opens the store.
type Option func(*openConfig)

type openConfig struct {
	synchronous Synchronous
	logger      *slog.Logger
	slowCounted func(context.Context)
	// connectGate and closeWaiting are the trackedConnector's gate and waiting. They are for tests and nil otherwise.
	connectGate  func(context.Context)
	closeWaiting func()
	// callClock is the clock store calls are timed on. It is for tests, and time.Now otherwise.
	callClock func() time.Time
}

// WithLogger sets the logger the store warns on for each call that takes longer than 100 ms, from the call until the
// store's one connection is free again. The warning names the call's queries and splits its time into the wait for the
// connection and the run on it. Without WithLogger the warnings are discarded.
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

// WithSynchronous sets the connection's synchronous mode in place of SQLite's default, FULL. It is for tests: OFF
// skips the fsync each commit and checkpoint costs, which a test's throwaway store does not need. orreryd never
// passes it, because restart and re-adoption rely on committed rows surviving an OS crash or power loss.
func WithSynchronous(mode Synchronous) Option {
	return func(c *openConfig) { c.synchronous = mode }
}

// Open creates the store's directory (0700) and file (0600) when absent, opens the database in WAL mode with a
// 200 ms busy timeout, and applies the embedded migrations. It returns ErrTooNew, without migrating, when the
// database records a newer migration than this binary embeds. A file it cannot use is left untouched.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	var cfg openConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	query := "_busy_timeout=200&_journal_mode=WAL"
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
	connector := &trackedConnector{inner: inner, gate: cfg.connectGate, waiting: cfg.closeWaiting}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	slow := &slowCalls{now: cfg.callClock, logger: cfg.logger, counted: cfg.slowCounted}
	if slow.now == nil {
		slow.now = time.Now
	}
	if slow.logger == nil {
		slow.logger = slog.New(slog.DiscardHandler)
	}
	s := &Store{db: &pool{DB: db, slow: slow}, connector: connector}
	if err = s.migrate(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// Close closes the store and returns once every connection it opened is closed, so the database's files are settled
// when it returns. database/sql opens a connection on its own goroutine when one is discarded, an interrupted one for
// instance, while another call waits for it, and DB.Close does not wait for that open. Close refuses new connections
// first, then closes the pool, then waits for the connections still open, which close as their calls return them.
func (s *Store) Close() error {
	s.connector.beginClose()
	err := s.db.Close()
	s.connector.wait()
	if err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
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
