package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// Synthetic names only. upgradeHost is the controller's host name, which --host names and the store stamps on the rows
// the controller runs. upgradeEpic is the controller-only epic in flight across the upgrade, and upgradeStepID the step
// of that epic that runs on the controller's Local before the upgrade. upgradeNextID is the step the epic starts after
// the upgrade, and upgradeGrantID the grant the controller holds before it.
const (
	upgradeHost    = "synthetic-upgrade-host"
	upgradeEpic    = "synthetic-repo-7c1d2e3/epic-in-flight"
	upgradeStepID  = "2f6a9c4e1b8d4037a5e2c9f1b7d30a68"
	upgradeNextID  = "8e3b0d5a7c2f41e9b6a4d1c8f2e70b35"
	upgradeGrantID = "c4d9e2b7a1f84a6e9d3c0b5a8f7e2d14"
	upgradePool    = "upgrade-pool"
	upgradeMember  = "m1"
	upgradeProject = "synthetic-upgrade-project"
	// upgradePrompt is the prompt of each step. The fake claude streams a turn it never finishes, so each step runs until
	// the test stops it.
	upgradePrompt = "STALL the step in flight across the upgrade"
	// upgradeStamp is the time the rows of the test are stored at, in seconds since the epoch.
	upgradeStamp = 1_700_000_000
	// upgradeStatFieldIndex is the index, among the fields of a /proc stat line after its command name, of the start time,
	// which is field 22 of the line. The fields after the command name begin at field 3.
	upgradeStatFieldIndex = 22 - 3
)

// upgradeStoredPool is a pool definition as a grant row records it, with the JSON names the broker reads back on
// restore.
type upgradeStoredPool struct {
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	Cap         int           `json:"cap"`
	Members     []string      `json:"members"`
	Wake        []string      `json:"wake"`
	WakeLimit   time.Duration `json:"wakeLimit"`
	Status      []string      `json:"status"`
	StatusLimit time.Duration `json:"statusLimit"`
	Admit       []string      `json:"admit"`
	AdmitLimit  time.Duration `json:"admitLimit"`
}

// upgradeEnv sets the environment of the test's controller: a state directory, a config directory that holds no hosts
// file, and the home and claude directories. It returns the getenv that names them, and the root that holds them, which
// is removed when the test ends.
func upgradeEnv(t *testing.T) (func(string) string, string) {
	t.Helper()
	root, err := shortStateRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	home := filepath.Join(root, "home")
	env := map[string]string{
		"XDG_STATE_HOME":      filepath.Join(root, "state"),
		"XDG_CONFIG_HOME":     filepath.Join(root, "config"),
		"HOME":                home,
		"CLAUDE_CONFIG_DIR":   filepath.Join(home, ".claude"),
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	return func(key string) string { return env[key] }, root
}

// upgradeSystemdRun is the fake systemd-run the controller's holders run under, as --holder-scope names it. It skips
// the scope's own arguments up to --unit and the unit's name, and runs the holder's argument vector in place. So each
// holder is a process of its own that survives the controller's shutdown, as a holder in a user scope does.
const upgradeSystemdRun = "#!/bin/sh\nwhile [ \"$1\" != \"--unit\" ]; do shift; done\nshift 2\nexec \"$@\"\n"

// upgradeSystemctl is the fake systemctl beside upgradeSystemdRun. Its stop exits with the status of a unit that is not
// loaded, since the fake systemd-run loads no scope, so the holders' scope stops are no errors.
const upgradeSystemctl = "#!/bin/sh\nexit 5\n"

// upgradeScope writes the fake systemd-run and systemctl under root, and returns the absolute path of the systemd-run,
// which the controller's --holder-scope names.
func upgradeScope(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "scope")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make scope directory: %v", err)
	}
	for name, text := range map[string]string{"systemd-run": upgradeSystemdRun, "systemctl": upgradeSystemctl} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return filepath.Join(dir, "systemd-run")
}

// upgradeClientStart returns the start time of this test process, as /proc records it. A grant row stores its client's
// start time, which tells the client's process from another that later takes its pid.
func upgradeClientStart(t *testing.T) int64 {
	t.Helper()
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatalf("read stat of this process: %v", err)
	}
	text := string(stat)
	fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
	if len(fields) <= upgradeStatFieldIndex {
		t.Fatalf("stat of this process has %d fields after its command, want more than %d",
			len(fields), upgradeStatFieldIndex)
	}
	start, err := strconv.ParseInt(fields[upgradeStatFieldIndex], 10, 64)
	if err != nil {
		t.Fatalf("parse start time of this process: %v", err)
	}
	return start
}

// upgradeGrantRow returns the grant of the controller that the store holds before the upgrade: a grant with no host,
// which is the controller's own, whose client is this test process and which holds the one member of the pool it names.
// The pool's commands succeed at once.
func upgradeGrantRow(t *testing.T, sh string) store.GrantRow {
	t.Helper()
	argv := []string{sh, "-c", "exit 0"}
	pool := upgradeStoredPool{
		Name: upgradePool, Host: upgradeHost, Cap: 1, Members: []string{upgradeMember},
		Wake: argv, WakeLimit: time.Minute, Status: argv, StatusLimit: time.Minute, Admit: argv, AdmitLimit: time.Minute,
	}
	encoded, err := json.Marshal([]upgradeStoredPool{pool})
	if err != nil {
		t.Fatalf("encode pool of grant: %v", err)
	}
	return store.GrantRow{
		ID: upgradeGrantID, Project: upgradeProject, Host: "", ClientPID: os.Getpid(),
		ClientStart: upgradeClientStart(t), Pools: string(encoded),
		Members: []store.GrantMember{{Pool: upgradePool, Member: upgradeMember}},
		Granted: time.Unix(upgradeStamp, 0),
	}
}

// openUpgradeStore opens the store at path, and returns it with the func that closes it, which may be called more than
// once. The test's cleanup closes it too, if it is still open.
func openUpgradeStore(t *testing.T, path string) (*store.Store, func() error) {
	t.Helper()
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	closeOnce := sync.OnceValue(st.Close)
	t.Cleanup(func() {
		if err := closeOnce(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st, closeOnce
}

// upgradeController is one orreryd --sessions of the test's controller over the store. local is the recorder of the
// calls its Local makes, and wrapped names each host its hostexec.Map was given, so the test sees that no hosts.toml
// host is among them.
type upgradeController struct {
	host    *sessionHost
	stop    func() error
	local   *hostexectest.Recorder
	wrapped []string
}

// startUpgradeController starts the controller over st, with the flags of an orreryd whose hosts file names no host,
// and its socket under root. Its holders run under the fake systemd-run at scope, as an orreryd run under
// --holder-scope has them, so its shutdown detaches each session rather than stopping it. Its stop ends it, once, and
// the test's cleanup ends it too if it still runs.
func startUpgradeController(
	t *testing.T, getenv func(string) string, st *store.Store, root, scope string,
) *upgradeController {
	t.Helper()
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", eventWakeShell(t), "--claude", eventWakeClaude(t),
		"--socket", filepath.Join(root, "orreryd.sock"), "--host", upgradeHost, "--holder-scope", scope,
	}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.orrery = upgradeHost
	c := &upgradeController{}
	opts.hostWrap = func(name string, host hostexec.Host) hostexec.Host {
		c.wrapped = append(c.wrapped, name)
		rec := hostexectest.NewRecorder(host)
		if name == upgradeHost {
			c.local = rec
		}
		return rec
	}
	host, err := startSessions(t.Context(), opts, upgradeHost, st, &recordingRouter{}, nil,
		slog.New(slog.DiscardHandler), getenv)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	c.host = host
	c.stop = sync.OnceValue(host.stop)
	t.Cleanup(func() {
		if err := c.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	})
	return c
}

// upgradeStart returns the start of a read-only step of the epic on the controller, in repo at base, whose id is id.
func upgradeStart(id, repo, base string) enginehost.StepStart {
	return enginehost.StepStart{
		ID: id, Agent: routingImplementer, Prompt: upgradePrompt, Workspace: repo, Revision: base,
		ReadOnly: true, Epic: upgradeEpic, Schema: routingStatusSchema,
	}
}

// upgradeHolderSessions returns the sessions whose holders lie under the controller's state directory stateDir.
func upgradeHolderSessions(t *testing.T, stateDir string) []string {
	t.Helper()
	local := holderhost.NewLocal(holderhost.LocalConfig{StateDir: stateDir})
	ids, err := local.Sessions(t.Context())
	if err != nil {
		t.Fatalf("list holders under %s: %v", stateDir, err)
	}
	return ids
}

// TestUpgradeKeepsLocalWork runs a controller-only epic in flight across the upgrade. The store holds the epic and its
// step rows stamped with the controller's host, and a grant row written without a host. The controller stops, and
// restarts under the upgraded orreryd, whose hosts file names no host. The step re-adopts on the controller's Local,
// the grant restores as the controller's, and the epic's next git call and step session run on the Local.
func TestUpgradeKeepsLocalWork(t *testing.T) {
	getenv, root := upgradeEnv(t)
	writeRoutingFile(t, filepath.Join(root, "home", ".claude", "agents", routingImplementer+".md"),
		"---\nname: "+routingImplementer+"\ndescription: A synthetic agent\n---\n")
	repo, base := restartRepo(t, filepath.Join(root, "repo"))
	path := mustStorePath(t, getenv)
	stateDir, err := stateDirOf(getenv)
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	storetest.Seed(t, path)
	scope := upgradeScope(t, root)
	ctx := t.Context()

	// Before the upgrade, the controller runs the epic's step, and holds the grant.
	st, closeStore := openUpgradeStore(t, path)
	created := time.Unix(upgradeStamp, 0)
	if err := st.CreateEpic(ctx, store.EpicRow{
		ID: upgradeEpic, Repository: repo, Host: upgradeHost, Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if err := st.AddGrant(ctx, upgradeGrantRow(t, eventWakeShell(t))); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	before := startUpgradeController(t, getenv, st, root, scope)
	if _, err := before.host.steps.Start(ctx, upgradeStart(upgradeStepID, repo, base)); err != nil {
		t.Fatalf("start step %s before the upgrade: %v", upgradeStepID, err)
	}
	if engine, err := st.EngineSession(ctx, upgradeStepID); err != nil || engine.Host != upgradeHost {
		t.Fatalf("step %s before the upgrade is stored as host %q, err %v, want %q", upgradeStepID, engine.Host, err,
			upgradeHost)
	}
	if err := before.stop(); err != nil {
		t.Fatalf("stop controller before the upgrade: %v", err)
	}
	if err := closeStore(); err != nil {
		t.Fatalf("close store before the upgrade: %v", err)
	}

	// The upgraded orreryd starts over the same store and state directory, with no hosts file, so the only host is the
	// controller's own.
	st, _ = openUpgradeStore(t, path)
	after := startUpgradeController(t, getenv, st, root, scope)
	if !slices.Equal(after.wrapped, []string{upgradeHost}) {
		t.Fatalf("the upgraded controller reaches hosts %v, want only %q", after.wrapped, upgradeHost)
	}
	// The step stops before the controller does, so its loop ends while the sessions still run.
	t.Cleanup(func() {
		if err := after.host.steps.Stop(context.Background(), upgradeStepID); err != nil {
			t.Errorf("stop step %s: %v", upgradeStepID, err)
		}
	})

	t.Run("the step re-adopts on the controller's Local", func(t *testing.T) {
		step, err := st.EngineSession(ctx, upgradeStepID)
		if err != nil {
			t.Fatalf("read engine row of step %s: %v", upgradeStepID, err)
		}
		if step.Host != upgradeHost || step.State != "working" {
			t.Errorf("the upgraded controller stores step %s as host %q in state %q, want host %q working",
				upgradeStepID, step.Host, step.State, upgradeHost)
		}
		row, err := after.host.steps.Status(ctx, upgradeStepID)
		if err != nil {
			t.Fatalf("read step row of step %s: %v", upgradeStepID, err)
		}
		if row.End != "" {
			t.Errorf("the upgrade ended step %s: %q", upgradeStepID, row.End)
		}
		if !after.host.manager.StepLive(upgradeStepID) {
			t.Errorf("the upgraded controller holds no live session for step %s", upgradeStepID)
		}
		if !slices.Contains(upgradeHolderSessions(t, stateDir), upgradeStepID) {
			t.Errorf("the controller's state directory holds no holder of step %s", upgradeStepID)
		}
	})
	t.Run("the grant restores as the controller's", func(t *testing.T) {
		rows, err := st.Grants(ctx)
		if err != nil {
			t.Fatalf("read grants: %v", err)
		}
		i := slices.IndexFunc(rows, func(row store.GrantRow) bool { return row.ID == upgradeGrantID })
		if i < 0 {
			t.Fatalf("grant %s is not stored after the upgrade", upgradeGrantID)
		}
		if rows[i].Host != "" {
			t.Errorf("grant %s is stored with host %q after the upgrade, want the controller's empty host",
				upgradeGrantID, rows[i].Host)
		}
		held := false
		for _, pool := range after.host.broker.Status().Pools {
			if pool.Name != upgradePool {
				continue
			}
			for _, member := range pool.Members {
				if member.Name == upgradeMember && member.Holder != nil && member.Holder.GrantID == upgradeGrantID {
					held = true
				}
			}
		}
		if !held {
			t.Errorf("the restored broker holds no member %s of pool %s for grant %s", upgradeMember, upgradePool,
				upgradeGrantID)
		}
	})
	t.Run("the epic's next git call and step session run on the Local", func(t *testing.T) {
		epicHost, err := st.EpicHost(ctx, upgradeEpic)
		if err != nil {
			t.Fatalf("read host of epic %s: %v", upgradeEpic, err)
		}
		if epicHost != upgradeHost {
			t.Errorf("epic %s is stored with host %q after the upgrade, want %q", upgradeEpic, epicHost, upgradeHost)
		}
		callsBefore := len(after.local.Calls())
		id, err := after.host.steps.Start(ctx, upgradeStart(upgradeNextID, repo, base))
		if err != nil {
			t.Fatalf("start the epic's next step: %v", err)
		}
		t.Cleanup(func() {
			if err := after.host.steps.Stop(context.Background(), id); err != nil {
				t.Errorf("stop step %s: %v", id, err)
			}
		})
		calls := after.local.Calls()[callsBefore:]
		if !routingGitCall(calls, repo, "rev-parse") {
			t.Errorf("the controller's Local recorded no git rev-parse in %s for the epic's next step: %v", repo, calls)
		}
		if !routingGitCall(calls, repo, "worktree", "add") {
			t.Errorf("the controller's Local recorded no worktree add in %s for the epic's next step: %v", repo, calls)
		}
		engine, err := st.EngineSession(ctx, id)
		if err != nil {
			t.Fatalf("read engine row of step %s: %v", id, err)
		}
		if engine.Host != upgradeHost {
			t.Errorf("the epic's next step %s is stored as host %q, want %q", id, engine.Host, upgradeHost)
		}
		if !slices.Contains(upgradeHolderSessions(t, stateDir), id) {
			t.Errorf("the controller's state directory holds no holder of the epic's next step %s", id)
		}
	})
}
