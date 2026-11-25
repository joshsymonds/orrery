package holderhost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/stamp"
)

// PermissionMode is the permission mode every session runs in.
const PermissionMode = "bypassPermissions"

// holdersName is the subdirectory of the state directory that holds each session's holder directory.
const holdersName = "holders"

// LocalConfig configures a Local host: the programs and environment its sessions run with, where their holders live,
// and how they are started and attached.
type LocalConfig struct {
	// Shell is the owner's login shell, an absolute path, that each session's claude runs through.
	Shell string
	// Claude is the claude binary, an absolute path.
	Claude string
	// Environ is the environment each session inherits, as session.Config takes it.
	Environ []string
	// StateDir is the state directory. Each session's holder directory is under StateDir/holders, as holder.Launch and
	// the engine host's holders subdirectory have it.
	StateDir string
	// Scope is the absolute path of systemd-run, which runs each holder in its own user scope. Empty starts the holders
	// plainly.
	Scope string
	// CronHook makes a session that is not a step take the settings that carry its cron tools to the orrery hook. A step
	// never takes them. Another epic removes the hook, and with it this field.
	CronHook bool
	// AttachTimeout bounds the hello and the attach of each Reattach. Zero takes holder.AttachTimeout.
	AttachTimeout time.Duration
}

// Local is the host this machine runs its sessions on. Each session's holder is the one holder.Launch starts under
// StateDir/holders, and the host attaches to it with holder.Reattach.
type Local struct {
	cfg LocalConfig
	// attachTimeout is the bound Reattach uses in place of cfg.AttachTimeout, as SetAttachTimeout sets it.
	attachTimeout atomic.Int64
}

// NewLocal returns the Local host that cfg describes.
func NewLocal(cfg LocalConfig) *Local {
	l := &Local{cfg: cfg}
	l.attachTimeout.Store(int64(cfg.AttachTimeout))
	return l
}

var _ Host = (*Local)(nil)

// SetAttachTimeout sets the bound Reattach uses for its hello and attach, in place of the one LocalConfig gave. Zero
// takes holder.AttachTimeout. Reattach reads the bound when it is called, so a call of SetAttachTimeout takes effect for
// each later Reattach.
func (l *Local) SetAttachTimeout(d time.Duration) {
	l.attachTimeout.Store(int64(d))
}

// Launch starts the session that spec describes on this machine under a holder in StateDir/holders, as session.Start
// does with the claude, shell, environment and state directory of the Local's configuration. claude is resolved with
// exec.LookPath at each launch. A step's settings carry the stamp's env and exclude the owner's CLAUDE.md; any other
// session's settings carry the cron hook only when the Local's CronHook is set. Settings that cannot be read fail the
// launch before claude runs.
func (l *Local) Launch(ctx context.Context, spec Spec) (*session.Session, error) {
	claude, err := exec.LookPath(l.cfg.Claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	settings, err := l.settings(spec)
	if err != nil {
		return nil, err
	}
	sess, err := session.Start(ctx, session.Config{
		Shell:          l.cfg.Shell,
		Claude:         claude,
		Dir:            spec.Dir,
		PermissionMode: PermissionMode,
		Settings:       settings,
		Resume:         spec.Resume,
		Model:          spec.Model,
		Agent:          spec.Agent,
		Agents:         spec.Agents,
		Schema:         spec.Schema,
		Name:           spec.Session,
		HolderScope:    l.cfg.Scope,
		StateDir:       l.cfg.StateDir,
		Environ:        l.cfg.Environ,
	})
	if err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}
	return sess, nil
}

// settings returns the --settings JSON of a launch of spec in spec.Dir. The stamp is looked up under the HOME entry of
// the Local's environment, which must be an absolute path. A step takes settings with the stamp's env and the owner's
// CLAUDE.md excluded. Any other session takes the stamp's env and the PreToolUse hook on the cron tools when CronHook is
// set, which runs holder.Binary(""), and one that cannot be resolved fails here, before claude runs.
func (l *Local) settings(spec Spec) (string, error) {
	home, err := StampHome(l.cfg.Environ)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", spec.Dir, err)
	}
	found, err := stamp.Find(home, spec.Dir)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", spec.Dir, err)
	}
	if spec.Step {
		var memory string
		memory, err = UserMemoryOf(l.cfg.Environ)
		if err != nil {
			return "", fmt.Errorf("step in %s: %w", spec.Dir, err)
		}
		return stamp.Settings(false, found.Env, []string{memory}), nil
	}
	if !l.cfg.CronHook {
		return stamp.Settings(false, found.Env, nil), nil
	}
	orrery, err := holder.Binary("")
	if err != nil {
		return "", fmt.Errorf("cron hook for %s: %w", spec.Dir, err)
	}
	return stamp.HeadlessSettings(found.Env, orrery), nil
}

// Reattach takes over the holder of session id on this machine at replayFrom, speaking version, as holder.Reattach does
// on the session's holder directory, scoped as the Local's Scope names. Its attach is bounded by the Local's attach
// timeout when that is not zero.
func (l *Local) Reattach(
	ctx context.Context, id string, replayFrom int64, version int,
) (*holder.Process, []string, error) {
	opts := []holder.ReattachOption{holder.WithScope(l.cfg.Scope)}
	if timeout := time.Duration(l.attachTimeout.Load()); timeout != 0 {
		opts = append(opts, holder.WithAttachTimeout(timeout))
	}
	process, written, err := holder.Reattach(ctx, l.dirOf(id), replayFrom, version, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("reattach holder: %w", err)
	}
	return process, written, nil
}

// Stop ends the holder of session id on this machine and leaves its directory in place, as holder.Stop does.
func (l *Local) Stop(ctx context.Context, id string) error {
	if err := holder.Stop(ctx, l.dirOf(id)); err != nil {
		return fmt.Errorf("stop holder: %w", err)
	}
	return nil
}

// Kill ends the holder of session id on this machine without asking it, as holder.Kill does.
func (l *Local) Kill(ctx context.Context, id string) error {
	if err := holder.Kill(ctx, l.dirOf(id)); err != nil {
		return fmt.Errorf("kill holder: %w", err)
	}
	return nil
}

// Clear empties the holder directory of session id on this machine, as holder.Clear does.
func (l *Local) Clear(ctx context.Context, id string) error {
	if err := holder.Clear(ctx, l.dirOf(id)); err != nil {
		return fmt.Errorf("clear holder: %w", err)
	}
	return nil
}

// StopOrphan stops the child the gone holder of session id recorded on this machine, as holder.StopOrphan does.
func (l *Local) StopOrphan(ctx context.Context, id string) error {
	if err := holder.StopOrphan(ctx, l.dirOf(id)); err != nil {
		return fmt.Errorf("stop orphan: %w", err)
	}
	return nil
}

// OpenGone returns the child of session id on this machine as its gone holder left it, with its stdout from offset from,
// as holder.OpenGone does. It returns a nil Gone when it returns an error.
func (l *Local) OpenGone(_ context.Context, id string, from int64) (Gone, error) {
	gone, err := holder.OpenGone(l.dirOf(id), from)
	if err != nil {
		return nil, fmt.Errorf("open gone holder: %w", err)
	}
	return gone, nil
}

// Sessions returns the ids that have a holder directory under StateDir/holders on this machine, in the order their
// names sort. A missing directory has none, and so does a Local with no StateDir.
func (l *Local) Sessions(_ context.Context) ([]string, error) {
	if l.cfg.StateDir == "" {
		return nil, nil
	}
	dir := filepath.Join(l.cfg.StateDir, holdersName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read holders dir %s: %w", dir, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.Name())
	}
	return ids, nil
}

// Reach reports that this machine is reachable, and returns a channel that never closes.
func (*Local) Reach() (bool, <-chan struct{}) {
	return true, make(chan struct{})
}

// dirOf returns the holder directory of session id under StateDir/holders.
func (l *Local) dirOf(id string) string {
	return filepath.Join(l.cfg.StateDir, holdersName, id)
}

// EnvironValue returns the value of name in environ, and whether environ has it. The last entry for name wins, as it
// does for a child process that exec starts with environ.
func EnvironValue(environ []string, name string) (string, bool) {
	value, found := "", false
	for _, entry := range environ {
		key, val, ok := strings.Cut(entry, "=")
		if ok && key == name {
			value, found = val, true
		}
	}
	return value, found
}

// StampHome returns the HOME entry of environ, the home every stamp is looked up under. It refuses a HOME that is absent,
// empty, or not an absolute path, so no stamp is ever read below a home the lookup cannot resolve.
func StampHome(environ []string) (string, error) {
	home, ok := EnvironValue(environ, "HOME")
	if !ok {
		return "", errors.New("HOME is not set")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("HOME %q is not an absolute path", home)
	}
	return home, nil
}

// UserMemoryOf returns the path of the user's CLAUDE.md that the claude running with environ loads: its HOME and
// CLAUDE_CONFIG_DIR entries, read as stamp.UserMemory reads them. It fails when that path cannot be resolved.
func UserMemoryOf(environ []string) (string, error) {
	home, _ := EnvironValue(environ, "HOME")
	configDir, _ := EnvironValue(environ, "CLAUDE_CONFIG_DIR")
	memory, err := stamp.UserMemory(home, configDir)
	if err != nil {
		return "", fmt.Errorf("user memory: %w", err)
	}
	return memory, nil
}
