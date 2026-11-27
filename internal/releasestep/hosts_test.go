package releasestep

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// orreryNode is the unit a hosts step checks on each host.
const orreryNode = "orrery-node"

// twoHostsFile is a hosts file that lists alpha and beta, which the controller reaches at alpha.example and
// beta.example.
const twoHostsFile = `name = "controller"

[[hosts]]
name = "alpha"
address = "alpha.example:7443"

[[hosts]]
name = "beta"
address = "beta.example:7443"
`

// fleetHost is a host of the test fleet: whether it is down, and the units, links and files its commands read.
type fleetHost struct {
	down  bool
	units map[string]unit
	links map[string]string
	files map[string]string
}

// runBuild makes the host's orrery node unit an active unit whose main process, pid, runs the build of commit.
func (h *fleetHost) runBuild(pid int, commit string) {
	store := "/nix/store/" + commit[:8] + "-orrery-0.0.0"
	h.units[orreryNode] = unit{state: "active", pid: pid}
	h.links[fmt.Sprintf("/proc/%d/exe", pid)] = store + "/bin/" + orreryNode
	h.files[store+"/"+BuildCommitFile] = commit + "\n"
}

// fleet is the machine a hosts step runs on in a test. The controller's own files and links are the embedded fake
// machine's. Its commands are answered here, and ssh reaches each of the fleet's hosts by its target. A nixos-rebuild
// that names a host with --target-host leaves that host's orrery node unit running the fleet's build. Every command the
// fleet is asked to run is recorded in the fake machine's calls.
type fleet struct {
	*fakeMachine
	hosts map[string]*fleetHost
	// build is the commit a nixos-rebuild leaves the host it switches running.
	build string
}

// newFleet returns a fleet with no host.
func newFleet(t *testing.T) *fleet {
	t.Helper()
	return &fleet{fakeMachine: newFakeMachine(t), hosts: map[string]*fleetHost{}}
}

// addHost adds a host reached at target. It is down, or runs its orrery node unit from a build of commit.
func (f *fleet) addHost(target string, down bool, commit string) {
	h := &fleetHost{down: down, units: map[string]unit{}, links: map[string]string{}, files: map[string]string{}}
	f.hosts[target] = h
	h.runBuild(100+len(f.hosts), commit)
}

// Run answers a command: ssh runs its words on a host, and nixos-rebuild switches the host its --target-host names.
// Anything else fails.
func (f *fleet) Run(_ context.Context, dir string, argv []string, stdout, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{dir: dir, argv: slices.Clone(argv)})
	switch argv[0] {
	case "ssh":
		return f.ssh(argv, stdout)
	case "nixos-rebuild":
		h := f.hosts[flagValue(argv, "--target-host")]
		if h == nil || h.down {
			return errFake
		}
		h.runBuild(h.units[orreryNode].pid, f.build)
		return nil
	}
	return fmt.Errorf("the fleet runs no %q", strings.Join(argv, " "))
}

// ssh answers an ssh argv, which is ssh, four option words, the target and the words to run there. A host that is down
// fails. The probe, true, succeeds; systemctl, readlink and cat answer from the host's units, links and files.
func (f *fleet) ssh(argv []string, stdout io.Writer) error {
	const targetAt = 5
	h := f.hosts[argv[targetAt]]
	if h == nil || h.down {
		return errFake
	}
	var words []string
	for _, word := range argv[targetAt+1:] {
		words = append(words, strings.Trim(word, "'"))
	}
	switch {
	case slices.Equal(words, []string{"true"}):
		return nil
	case words[0] == "systemctl":
		u := h.units[words[len(words)-1]]
		fmt.Fprintf(stdout, "MainPID=%d\nActiveState=%s\n", u.pid, u.state)
		return nil
	case words[0] == "readlink":
		link, found := h.links[words[1]]
		if !found {
			return fmt.Errorf("readlink %s: %w", words[1], fs.ErrNotExist)
		}
		fmt.Fprintln(stdout, link)
		return nil
	case words[0] == "cat":
		text, found := h.files[words[1]]
		if !found {
			return fmt.Errorf("read %s: %w", words[1], fs.ErrNotExist)
		}
		fmt.Fprint(stdout, text)
		return nil
	}
	return fmt.Errorf("the fleet runs no %q on %s", strings.Join(words, " "), argv[targetAt])
}

// commands returns the commands the fleet was asked to run, in order.
func (f *fleet) commands() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// rebuilt returns the --target-host of each nixos-rebuild the fleet ran, in order.
func (f *fleet) rebuilt() []string {
	var targets []string
	for _, c := range f.commands() {
		if c.argv[0] == "nixos-rebuild" {
			targets = append(targets, flagValue(c.argv, "--target-host"))
		}
	}
	return targets
}

// ran reports whether the fleet was asked to run argv in dir.
func (f *fleet) ran(dir string, argv []string) bool {
	return slices.ContainsFunc(f.commands(), func(c call) bool {
		return c.dir == dir && slices.Equal(c.argv, argv)
	})
}

// flagValue returns the word after flag in argv, or "" when argv names no such flag.
func flagValue(argv []string, flag string) string {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return ""
	}
	return argv[i+1]
}

// writeHostsFile writes text as the hosts file under a fresh XDG_CONFIG_HOME, and returns that config directory.
func writeHostsFile(t *testing.T, text string) string {
	t.Helper()
	config := t.TempDir()
	writeFile(t, config, "orrery/hosts.toml", text)
	return config
}

// envWithConfig returns the environment that names config as XDG_CONFIG_HOME.
func envWithConfig(config string) func(string) string {
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// runHosts runs `release-step hosts` for candidate, with the hosts file under config, flake as the flake repository,
// and the fleet as the machine. It returns the command's result.
func runHosts(t *testing.T, w *world, f *fleet, config, candidate, flake string) result {
	t.Helper()
	params := hostsParams()
	params.Flake = flake
	argv := Argv("orrery", KindHosts, params, candidate, "the change")
	var stdout, stderr bytes.Buffer
	s := &step{
		git:     hostexec.NewLocal(),
		machine: f,
		repo:    w.repo,
		getenv:  envWithConfig(config),
		stdout:  &stdout,
		stderr:  &stderr,
	}
	code := s.main(context.Background(), argv[2:])
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// hostsSwitchArgv is the argv the run of hostsParams switches the host named name, reached at target, with.
func hostsSwitchArgv(name, target string) []string {
	return []string{"nixos-rebuild", "switch", "--flake", ".#" + name, "--target-host", target, "--sudo"}
}

// TestHostsStepSwitchesEachHost: both hosts answer and run an older build, so the step runs the run argv in the
// flake once for each host, with the host's name and target, and each host's orrery node unit then runs the
// candidate's build.
func TestHostsStepSwitchesEachHost(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("candidate")
	f := newFleet(t)
	f.addHost("alpha.example", false, w.base)
	f.addHost("beta.example", false, w.base)
	f.build = candidate
	flake := t.TempDir()
	res := runHosts(t, w, f, writeHostsFile(t, twoHostsFile), candidate, flake)
	wantCode(t, res, exitHeld)
	if got, want := f.rebuilt(), []string{"alpha.example", "beta.example"}; !slices.Equal(got, want) {
		t.Errorf("switched hosts = %q, want %q", got, want)
	}
	for _, host := range [][2]string{{"alpha", "alpha.example"}, {"beta", "beta.example"}} {
		if want := hostsSwitchArgv(host[0], host[1]); !f.ran(flake, want) {
			t.Errorf("no run of %q in %s", want, flake)
		}
	}
	wantOut(t, "stdout", res.stdout, "host alpha: unit orrery-node is active", "host beta: unit orrery-node is active",
		"which contains candidate "+candidate)
}

// TestHostsStepSkipsHostThatDoesNotAnswer: beta answers and is switched, while alpha does not answer. The step names
// alpha as skipped, upgraded on the next release, and runs nothing on alpha.
func TestHostsStepSkipsHostThatDoesNotAnswer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("candidate")
	f := newFleet(t)
	f.addHost("alpha.example", true, w.base)
	f.addHost("beta.example", false, w.base)
	f.build = candidate
	flake := t.TempDir()
	res := runHosts(t, w, f, writeHostsFile(t, twoHostsFile), candidate, flake)
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout,
		"host alpha did not answer: fake failure; skipped, upgraded on the next release",
		"host beta: unit orrery-node is active")
	if got, want := f.rebuilt(), []string{"beta.example"}; !slices.Equal(got, want) {
		t.Errorf("switched hosts = %q, want %q", got, want)
	}
	if want := hostsSwitchArgv("beta", "beta.example"); !f.ran(flake, want) {
		t.Errorf("no run of %q in %s", want, flake)
	}
}

// TestHostsStepNoHostsFile: with no hosts file, or one that lists no host, the step holds with the fact that nothing is
// switched, and runs no command, not even a probe.
func TestHostsStepNoHostsFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, text string
	}{
		{name: "no hosts file"},
		{name: "a hosts file that lists no host", text: "name = \"controller\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			candidate := w.commit("candidate")
			f := newFleet(t)
			f.addHost("alpha.example", false, w.base)
			f.build = candidate
			config := t.TempDir()
			if c.text != "" {
				writeFile(t, config, "orrery/hosts.toml", c.text)
			}
			res := runHosts(t, w, f, config, candidate, t.TempDir())
			wantCode(t, res, exitHeld)
			wantOut(t, "stdout", res.stdout, "no hosts.toml hosts: nothing to switch")
			if cmds := f.commands(); len(cmds) != 0 {
				t.Errorf("the step ran %v, want no command", cmds)
			}
		})
	}
}

// TestHostsStepHeldWhenEveryHostRunsCandidate: both hosts answer and already run a build that contains the
// candidate, so the postcondition held before the step and nothing is switched.
func TestHostsStepHeldWhenEveryHostRunsCandidate(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("candidate")
	later := w.commit("later")
	f := newFleet(t)
	f.addHost("alpha.example", false, later)
	f.addHost("beta.example", false, later)
	f.build = candidate
	res := runHosts(t, w, f, writeHostsFile(t, twoHostsFile), candidate, t.TempDir())
	wantCode(t, res, exitHeld)
	wantOut(t, "stdout", res.stdout, "the postcondition held before the step, so nothing was done",
		"which contains candidate "+candidate)
	if got := f.rebuilt(); len(got) != 0 {
		t.Errorf("switched hosts = %q, want none", got)
	}
}

// TestHostsStepRefusesHostThatReadsAsOption: a host whose address starts its host part with a dash would be read as an
// option of ssh, so the step fails before it reaches any host.
func TestHostsStepRefusesHostThatReadsAsOption(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	candidate := w.commit("candidate")
	f := newFleet(t)
	f.addHost("alpha.example", false, w.base)
	f.build = candidate
	text := "[[hosts]]\nname = \"alpha\"\naddress = \"-oProxyCommand=sh:7443\"\n"
	res := runHosts(t, w, f, writeHostsFile(t, text), candidate, t.TempDir())
	if res.code != exitFailed {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, exitFailed, res.stdout, res.stderr)
	}
	wantOut(t, "stderr", res.stderr, "starts with a dash")
	if cmds := f.commands(); len(cmds) != 0 {
		t.Errorf("the step ran %v, want no command", cmds)
	}
}
