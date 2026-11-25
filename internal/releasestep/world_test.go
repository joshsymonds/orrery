package releasestep

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// gitIn runs git in dir as the tests' own git, with an identity and no commit signing, and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitTry(dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// gitTry runs git in dir as gitIn does and returns its trimmed combined output and its error.
func gitTry(dir string, args ...string) (string, error) {
	full := append([]string{
		"-C", dir,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
	}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// writeFile writes text to file under dir, making its directories.
func writeFile(t *testing.T, dir, file, text string) {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("make directories of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// makeExecutable lets the owner run the file at path, as git needs of a hook.
func makeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

// commitFile commits file, holding text, on the checked-out branch of dir, and returns the commit.
func commitFile(t *testing.T, dir, file, text string) string {
	t.Helper()
	writeFile(t, dir, file, text)
	gitIn(t, dir, "add", file)
	gitIn(t, dir, "commit", "-q", "-m", "change "+file)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// world is the scratch machine a step runs against: a bare origin of the epic's repository, a clone of it the command
// runs in, and the machine fake. A flake world adds a bare flake origin and a clone of it as the owner's checkout.
type world struct {
	t *testing.T
	// origin is the bare origin of the epic's repository.
	origin string
	// repo is the epic's repository, the working directory of the command, with origin as its origin remote.
	repo string
	// base is the first commit of main, on origin.
	base string
	// signers is the allowed signers file that verifies the repository's tag signatures.
	signers string
	// flakeOrigin is the bare origin of the flake repository, in a flake world.
	flakeOrigin string
	// flake is the owner's checkout of the flake repository, in a flake world.
	flake   string
	machine *fakeMachine
}

// newWorld makes the epic's repository on main with one commit pushed to its origin. The repository signs tags with an
// SSH key made for the test, and commits with the test identity.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, origin: t.TempDir(), repo: t.TempDir(), machine: newFakeMachine(t)}
	gitIn(t, w.origin, "init", "-q", "--bare", "-b", "main")
	gitIn(t, w.repo, "init", "-q", "-b", "main")
	gitIn(t, w.repo, "remote", "add", Remote, w.origin)
	keys := t.TempDir()
	key := filepath.Join(keys, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).
		CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	public, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatalf("read the public key: %v", err)
	}
	w.signers = filepath.Join(keys, "allowed_signers")
	writeFile(t, keys, "allowed_signers", "test@example.invalid "+string(public))
	for _, pair := range [][2]string{
		{"user.name", "Test"},
		{"user.email", "test@example.invalid"},
		{"commit.gpgsign", "false"},
		{"gpg.format", "ssh"},
		{"user.signingkey", key},
		{"gpg.ssh.allowedSignersFile", w.signers},
	} {
		gitIn(t, w.repo, "config", pair[0], pair[1])
	}
	w.base = commitFile(t, w.repo, "README", "base\n")
	gitIn(t, w.repo, "push", "-q", Remote, "main")
	return w
}

// commit commits file on the repository's main and returns the commit. It pushes nothing.
func (w *world) commit(file string) string {
	w.t.Helper()
	return commitFile(w.t, w.repo, file, file+"\n")
}

// pushMain pushes the repository's main to origin.
func (w *world) pushMain() {
	w.t.Helper()
	gitIn(w.t, w.repo, "push", "-q", Remote, "main")
}

// originCommit commits file on origin's main from a clone of its own, so the repository does not hold the commit, and
// returns it.
func (w *world) originCommit(file string) string {
	w.t.Helper()
	return cloneCommit(w.t, w.origin, file)
}

// cloneCommit commits file on main of origin from a fresh clone of it and pushes it, and returns the commit.
func cloneCommit(t *testing.T, origin, file string) string {
	t.Helper()
	clone := t.TempDir()
	gitIn(t, clone, "clone", "-q", origin, ".")
	commit := commitFile(t, clone, file, "other "+file+"\n")
	gitIn(t, clone, "push", "-q", Remote, "main")
	return commit
}

// originTag makes the tag name on origin at commit, an annotated one so origin lists its peeled commit.
func (w *world) originTag(name, commit string) {
	w.t.Helper()
	gitIn(w.t, w.origin, "tag", "-a", "-m", name, name, commit)
}

// originMain returns the commit origin's main names.
func (w *world) originMain() string {
	w.t.Helper()
	return gitIn(w.t, w.origin, "rev-parse", "refs/heads/main")
}

// originTags returns the tag refs origin holds with the objects they name, one per line.
func (w *world) originTags() string {
	w.t.Helper()
	return gitIn(w.t, w.origin, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags")
}

// result is what one run of the command gave.
type result struct {
	code   int
	stdout string
	stderr string
}

// run runs the command with args, the arguments after the subcommand, in the repository, against the world's machine.
func (w *world) run(args ...string) result {
	w.t.Helper()
	var stdout, stderr bytes.Buffer
	s := &step{
		git:     hostexec.NewLocal(),
		machine: w.machine,
		repo:    w.repo,
		home:    filepath.Dir(w.flake),
		tempDir: w.t.TempDir(),
		stdout:  &stdout,
		stderr:  &stderr,
	}
	code := s.main(context.Background(), args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// runStep runs a step of kind with params from candidate, with the argv Argv builds for it.
func (w *world) runStep(kind string, params Params, candidate string) result {
	w.t.Helper()
	argv := Argv("orrery", kind, params, candidate, "the change")
	if argv[1] != Command {
		w.t.Fatalf("Argv = %q, want the subcommand %q second", argv, Command)
	}
	return w.run(argv[2:]...)
}

// wantCode fails the test unless res exited with code, and shows its output when it did not.
func wantCode(t *testing.T, res result, code int) {
	t.Helper()
	if res.code != code {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, code, res.stdout, res.stderr)
	}
}

// wantOut fails the test unless text holds each of wants.
func wantOut(t *testing.T, name, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not hold %q:\n%s", name, want, text)
		}
	}
}

// unit is a systemd user unit as the fake machine shows it.
type unit struct {
	state string
	pid   int
}

// fakeMachine is the machine a step runs commands on in a test. systemctl shows the units it holds, the files and
// links it holds stand in for /proc and the store, and nix and the update command run the handlers a test sets. Every
// command it is asked to run is recorded.
type fakeMachine struct {
	t     *testing.T
	mu    sync.Mutex
	calls []call
	units map[string]unit
	files map[string]string
	links map[string]string
	// nix runs `nix ...` in dir, when a test sets it.
	nix func(dir string, argv []string) error
	// update runs a step's run argv in dir, when a test sets it.
	update func(dir string, argv []string) error
}

// call is one command the fake machine was asked to run.
type call struct {
	dir  string
	argv []string
}

// newFakeMachine returns a machine with no unit, file, link or handler.
func newFakeMachine(t *testing.T) *fakeMachine {
	t.Helper()
	return &fakeMachine{t: t, units: map[string]unit{}, files: map[string]string{}, links: map[string]string{}}
}

// Run records the command and runs it: systemctl show prints a unit's properties, nix and anything else run their
// handlers, and a command with no handler fails.
func (m *fakeMachine) Run(_ context.Context, dir string, argv []string, stdout, _ io.Writer) error {
	m.mu.Lock()
	m.calls = append(m.calls, call{dir: dir, argv: slices.Clone(argv)})
	nix, update := m.nix, m.update
	m.mu.Unlock()
	switch {
	case len(argv) > 3 && argv[0] == "systemctl" && argv[1] == "--user" && argv[2] == "show":
		return m.show(argv[len(argv)-1], stdout)
	case argv[0] == "nix" && nix != nil:
		return nix(dir, argv)
	case argv[0] != "nix" && argv[0] != "systemctl" && update != nil:
		return update(dir, argv)
	}
	return fmt.Errorf("the fake machine runs no %q", strings.Join(argv, " "))
}

// show prints the properties of the unit named name, as systemctl show does for a unit it does not know when there is
// none.
func (m *fakeMachine) show(name string, stdout io.Writer) error {
	m.mu.Lock()
	u, found := m.units[name]
	m.mu.Unlock()
	if !found {
		u = unit{state: "inactive"}
	}
	fmt.Fprintf(stdout, "MainPID=%d\nActiveState=%s\n", u.pid, u.state)
	return nil
}

// ReadFile returns the file the machine holds at path.
func (m *fakeMachine) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	text, found := m.files[path]
	if !found {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return []byte(text), nil
}

// Readlink returns the target of the link the machine holds at path.
func (m *fakeMachine) Readlink(path string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, found := m.links[path]
	if !found {
		return "", fmt.Errorf("readlink %s: %w", path, fs.ErrNotExist)
	}
	return target, nil
}

// runs returns the commands the machine ran other than systemctl.
func (m *fakeMachine) runs() []call {
	m.mu.Lock()
	defer m.mu.Unlock()
	var runs []call
	for _, c := range m.calls {
		if c.argv[0] != "systemctl" {
			runs = append(runs, c)
		}
	}
	return runs
}

// runUnit makes name an active unit whose main process pid runs the orreryd of a build from commit, at store path hash.
func (m *fakeMachine) runUnit(name string, pid int, hash, commit string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := "/nix/store/" + hash + "-orrery-0.0.0"
	m.units[name] = unit{state: "active", pid: pid}
	m.links[fmt.Sprintf("/proc/%d/exe", pid)] = out + "/bin/" + name
	m.files[out+"/"+BuildCommitFile] = commit + "\n"
}

// errFake is the failure a fake handler returns.
var errFake = errors.New("fake failure")
