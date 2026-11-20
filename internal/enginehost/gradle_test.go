package enginehost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// fakeGradleScript stands in for gradle. Its build runs until gradle.release exists in its working directory. As Gradle
// does, it reads org.gradle.daemon from $GRADLE_USER_HOME/gradle.properties, or from ~/.gradle's without one. With
// false the build runs in this process. Otherwise it runs in the user's shared daemon, registered in
// $FAKE_GRADLE_REGISTRY: the first build starts the daemon, in a session of its own with its stdio closed, and later
// builds reuse it. A build whose daemon has died fails. It writes gradle.started once its build runs.
const fakeGradleScript = `#!/bin/sh
props=${GRADLE_USER_HOME:-$HOME/.gradle}/gradle.properties
if grep -qx 'org.gradle.daemon=false' "$props" 2>/dev/null; then
	: > gradle.started
	until [ -e gradle.release ]; do sleep 0.01; done
	exit 0
fi
reg=$FAKE_GRADLE_REGISTRY
if ! { [ -f "$reg/daemon.pid" ] && kill -0 "$(cat "$reg/daemon.pid")" 2>/dev/null; }; then
	(setsid sh -c 'echo $$ > "$1/daemon.tmp" && mv "$1/daemon.tmp" "$1/daemon.pid"; exec sleep 3600' sh "$reg" \
		</dev/null >/dev/null 2>&1 &)
	until [ -f "$reg/daemon.pid" ]; do sleep 0.01; done
fi
daemon=$(cat "$reg/daemon.pid")
: > gradle.started
until [ -e gradle.release ]; do
	kill -0 "$daemon" 2>/dev/null || exit 1
	sleep 0.01
done
kill -0 "$daemon" 2>/dev/null
`

// gradleEnviron returns the environment of the sessions of a Gradle test: the fake gradle as FAKE_GRADLE, a HOME of their
// own, and a daemon registry both sessions share, as one user's builds share Gradle's. daemonless runs it through
// GradleEnviron, as orreryd does. The registry's daemon is killed at cleanup, should a test leave it running.
func gradleEnviron(t *testing.T, stateDir string, daemonless bool) []string {
	t.Helper()
	bin := t.TempDir()
	exectest.WriteFile(t, filepath.Join(bin, "gradle"), []byte(fakeGradleScript), 0o700)
	registry := t.TempDir()
	t.Cleanup(func() {
		if data, err := os.ReadFile(filepath.Join(registry, "daemon.pid")); err == nil {
			_ = exec.Command("kill", "-KILL", strings.TrimSpace(string(data))).Run()
		}
	})
	environ := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"FAKE_GRADLE=" + filepath.Join(bin, "gradle"),
		"FAKE_GRADLE_REGISTRY=" + registry,
	}
	if !daemonless {
		return environ
	}
	environ, err := GradleEnviron(environ, stateDir, "")
	if err != nil {
		t.Fatalf("GradleEnviron: %v", err)
	}
	return environ
}

// newGradleManager returns a manager with two slots whose sessions run their holders under the test's fake scope with
// state dir root, in environ.
func newGradleManager(t *testing.T, root string, environ []string) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     environ,
		Cap:         2,
		Writer:      writer,
		Now:         fixedClock,
		Store:       s,
		HolderScope: fakeSystemdRunPath(t),
		StateDir:    root,
	})
	t.Cleanup(m.Close)
	return m, path
}

// startGradle starts a session in a fresh directory whose first prompt runs a gradle build, and returns its id and
// directory once the build runs.
func startGradle(t *testing.T, m *Manager, root string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "GRADLE"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitUntil(t, engineOf(m), "the session's gradle build to run", func() bool {
		_, err := os.Stat(filepath.Join(dir, "gradle.started"))
		return err == nil
	})
	return id, dir
}

// release lets the gradle build in dir finish.
func release(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "gradle.release"), nil, 0o600); err != nil {
		t.Fatalf("release the gradle build: %v", err)
	}
}

// TestOneStepsEndLeavesAnotherStepsGradleBuildRunning runs a gradle build in each of two sessions, the second while the
// first's runs. The first session's build ends and its session is stopped, which stops its holder's scope. The second
// build then finishes. Under GradleEnviron no build runs in a daemon, so the stop ends nothing the second build needs.
// Without it, the first build starts the user's shared daemon in its own scope, the second build runs in that daemon,
// and the stop ends it under the second build.
func TestOneStepsEndLeavesAnotherStepsGradleBuildRunning(t *testing.T) {
	t.Parallel()
	root := holderRoot(t)
	m, path := newGradleManager(t, root, gradleEnviron(t, root, true))
	first, firstDir := startGradle(t, m, root)
	second, secondDir := startGradle(t, m, root)

	release(t, firstDir)
	waitHistory(t, engineOf(m), path, first, "the first build's reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake gradle ok 1")
	})
	if err := m.Stop(t.Context(), first); err != nil {
		t.Fatalf("Stop the first session: %v", err)
	}

	release(t, secondDir)
	history := waitHistory(t, engineOf(m), path, second, "the second build's reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake gradle ok 1") || hasBlock(t, h, "fake gradle failed 1")
	})
	if !hasBlock(t, history, "fake gradle ok 1") {
		t.Fatal("the second session's build failed once the first session was stopped")
	}
}

func TestGradleEnvironMakesADaemonlessHomeSharingTheUsersCaches(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	user := t.TempDir()
	for _, name := range []string{"caches", "wrapper"} {
		if err := os.Mkdir(filepath.Join(user, name), 0o700); err != nil {
			t.Fatalf("make the user's %s: %v", name, err)
		}
	}
	environ, err := GradleEnviron(
		[]string{"HOME=/synthetic/home", "GRADLE_USER_HOME=" + user, "KEEP=1"}, stateDir, user,
	)
	if err != nil {
		t.Fatalf("GradleEnviron: %v", err)
	}
	home := filepath.Join(stateDir, "gradle")
	if want := []string{"HOME=/synthetic/home", "KEEP=1", "GRADLE_USER_HOME=" + home}; !slices.Equal(environ, want) {
		t.Fatalf("environ = %q, want %q", environ, want)
	}
	props, err := os.ReadFile(filepath.Join(home, "gradle.properties"))
	if err != nil {
		t.Fatalf("read gradle.properties: %v", err)
	}
	if string(props) != "org.gradle.daemon=false\nkotlin.compiler.execution.strategy=in-process\n" {
		t.Fatalf("gradle.properties = %q", props)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read the gradle home: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{"caches", "gradle.properties", "wrapper"}; !slices.Equal(names, want) {
		t.Fatalf("gradle home holds %q, want %q", names, want)
	}
	for _, name := range []string{"caches", "wrapper"} {
		target, linkErr := os.Readlink(filepath.Join(home, name))
		if linkErr != nil || target != filepath.Join(user, name) {
			t.Fatalf("%s links to %q (%v), want the user's", name, target, linkErr)
		}
	}
	// A second start keeps the links and rewrites the properties.
	if _, err = GradleEnviron([]string{"GRADLE_USER_HOME=" + user}, stateDir, user); err != nil {
		t.Fatalf("GradleEnviron again: %v", err)
	}
}
