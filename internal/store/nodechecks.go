package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoNodeCheck is matched by the error NodeCheck returns for an id no check has.
var ErrNoNodeCheck = errors.New("no node check")

// EnvironmentExit is the exit status that says a gate, command or evidence capture failed in its environment, not on its
// judgement, as cli.ExitNoGrant gives it when the command it wraps never got its grant. Such a run runs again, up to
// EnvironmentReruns times, and a run whose last rerun exits it too is parked, or fails its step.
const EnvironmentExit = 75

// EnvironmentReruns is how many times a run that exited EnvironmentExit runs again after its first run.
const EnvironmentReruns = 2

// EnvironmentExited reports whether a run that ended with exit, and timedOut, exited EnvironmentExit without timing out.
func EnvironmentExited(exit int, timedOut bool) bool {
	return exit == EnvironmentExit && !timedOut
}

// NodeCheck is one holder run of a gate or command node: a check that runs under a holder, under the grant of its
// needs. ID is the id NodeWorkID derives for the check, Epic and Path the epic and node run it belongs to, and Revision
// the revision it runs at. Argv and Limit are its command and its limit in seconds, and Needs the pools it draws slots
// from. State is running until the check ends, and Grant and Holder are the grant and holder it runs under. Created is
// the second it was added and Ended the second it ended, zero while it runs. Exit, TimedOut, NotRun, Stdout and Stderr
// are how it ended, and NotRun is the reason it did not run.
type NodeCheck struct {
	ID, Epic, Path, Revision string
	Argv                     []string
	Limit                    int64
	Needs                    []EpicGateNeed
	State, Grant, Holder     string
	Exit                     int
	TimedOut                 bool
	NotRun, Stdout, Stderr   string
	Created, Ended           time.Time
}

// NodeCheckEnd is how a running check ended: its exit, whether it timed out, the reason it did not run, and its stdout
// and stderr.
type NodeCheckEnd struct {
	Exit     int
	TimedOut bool
	NotRun   string
	Stdout   string
	Stderr   string
}

// AddNodeCheck stores check as a running check created at at, or, when check.NotRun is set, as a check ended at at for
// that reason. Argv and Needs are stored as JSON. Grant, holder and end are set by SetNodeCheckGrant and EndNodeCheck,
// so check's values for them are not stored. A check whose id is stored already is left as it is, so a repeat changes
// nothing.
func (s *Store) AddNodeCheck(ctx context.Context, check NodeCheck, at time.Time) error {
	argv, err := encodeList(check.Argv)
	if err != nil {
		return fmt.Errorf("argv of node check %s: %w", check.ID, err)
	}
	needs, err := encodeList(check.Needs)
	if err != nil {
		return fmt.Errorf("needs of node check %s: %w", check.ID, err)
	}
	state, ended := NodeRunning, int64(0)
	if check.NotRun != "" {
		state, ended = NodeEnded, at.Unix()
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if insertErr := queries.InsertEpicNodeCheck(ctx, sqlcgen.InsertEpicNodeCheckParams{
			ID: check.ID, Epic: check.Epic, Path: check.Path, Revision: check.Revision, Argv: argv,
			LimitSeconds: check.Limit, Needs: needs, State: state, NotRun: check.NotRun,
			Created: at.Unix(), Ended: ended,
		}); insertErr != nil {
			return fmt.Errorf("insert node check %s: %w", check.ID, insertErr)
		}
		return nil
	})
}

// SetNodeCheckGrant records the pool grant and holder that the running check with id runs under, in one write. A
// check that is not running is left as it is.
func (s *Store) SetNodeCheckGrant(ctx context.Context, id, grant, holder string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.SetEpicNodeCheckGrant(ctx, sqlcgen.SetEpicNodeCheckGrantParams{
			GrantID: grant, Holder: holder, ID: id,
		}); err != nil {
			return fmt.Errorf("set grant of node check %s: %w", id, err)
		}
		return nil
	})
}

// EndNodeCheck ends the running check with id at the second at, with end, in one write. Stdout and stderr are cut to
// their newest NodeOutputMax bytes. A check that is not running is left as it is, so a repeat changes nothing.
func (s *Store) EndNodeCheck(ctx context.Context, id string, end NodeCheckEnd, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.EndEpicNodeCheck(ctx, sqlcgen.EndEpicNodeCheckParams{
			Exit: int64(end.Exit), TimedOut: boolInt(end.TimedOut), NotRun: end.NotRun,
			Stdout: tailBytes(end.Stdout, NodeOutputMax), Stderr: tailBytes(end.Stderr, NodeOutputMax),
			Ended: at.Unix(), ID: id,
		}); err != nil {
			return fmt.Errorf("end node check %s: %w", id, err)
		}
		return nil
	})
}

// NodeCheck returns the stored check with id. It returns an error matching ErrNoNodeCheck when no check has that id.
func (s *Store) NodeCheck(ctx context.Context, id string) (NodeCheck, error) {
	stored, err := sqlcgen.New(s.read).GetEpicNodeCheck(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeCheck{}, fmt.Errorf("node check %s: %w", id, ErrNoNodeCheck)
	}
	if err != nil {
		return NodeCheck{}, fmt.Errorf("get node check %s: %w", id, err)
	}
	return nodeCheckOf(stored)
}

// NodeChecks returns the checks of epic ordered by created, then id.
func (s *Store) NodeChecks(ctx context.Context, epic string) ([]NodeCheck, error) {
	return readNodeChecks(ctx, sqlcgen.New(s.read), epic)
}

// OpenNodeChecks returns the running checks of every epic ordered by created, then id.
func (s *Store) OpenNodeChecks(ctx context.Context) ([]NodeCheck, error) {
	stored, err := sqlcgen.New(s.read).ListOpenEpicNodeChecks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list open node checks: %w", err)
	}
	return convertInOrder(stored, nodeCheckOf)
}

// readNodeChecks returns the checks of epic as queries read them, ordered by created, then id.
func readNodeChecks(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]NodeCheck, error) {
	stored, err := queries.ListEpicNodeChecks(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list node checks of epic %s: %w", epic, err)
	}
	return convertInOrder(stored, nodeCheckOf)
}

// nodeCheckOf returns the check a stored row holds, its argv and needs decoded. Ended is zero while the check runs.
func nodeCheckOf(stored sqlcgen.EpicNodeCheck) (NodeCheck, error) {
	argv, err := decodeList[string](stored.Argv)
	if err != nil {
		return NodeCheck{}, fmt.Errorf("argv of node check %s: %w", stored.ID, err)
	}
	needs, err := decodeList[EpicGateNeed](stored.Needs)
	if err != nil {
		return NodeCheck{}, fmt.Errorf("needs of node check %s: %w", stored.ID, err)
	}
	check := NodeCheck{
		ID: stored.ID, Epic: stored.Epic, Path: stored.Path, Revision: stored.Revision, Argv: argv,
		Limit: stored.LimitSeconds, Needs: needs, State: stored.State, Grant: stored.GrantID, Holder: stored.Holder,
		Exit: int(stored.Exit), TimedOut: stored.TimedOut != 0, NotRun: stored.NotRun, Stdout: stored.Stdout,
		Stderr: stored.Stderr, Created: time.Unix(stored.Created, 0),
	}
	if stored.Ended != 0 {
		check.Ended = time.Unix(stored.Ended, 0)
	}
	return check, nil
}

// tailBytes returns the newest limit bytes of text. When that cut falls inside a UTF-8 rune, the tail starts at the
// next rune start, so it is shorter than limit by up to the rune's remaining bytes.
func tailBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := len(text) - limit
	for cut < len(text) && !utf8.RuneStart(text[cut]) {
		cut++
	}
	return text[cut:]
}
