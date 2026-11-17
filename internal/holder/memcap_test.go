package holder

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeScopeGroup is the cgroup a fake memory cap names for the holder: a holder scope, as Launch names one.
const fakeScopeGroup = "/user.slice/app.slice/orrery-hold-" + testSession + "-0123456789abcdef.scope"

// fakeMemoryCap returns a memoryCap over a fake /proc/self/cgroup that names group, and a fake cgroup root that holds
// group's directory with memory.events giving oomKills and memory.max giving limit. It returns the memory.events path
// so a test can raise the count.
func fakeMemoryCap(t *testing.T, group, oomKills, limit string) (memoryCap, string) {
	t.Helper()
	dir := t.TempDir()
	self := filepath.Join(dir, "cgroup")
	if err := os.WriteFile(self, []byte("0::"+group+"\n"), 0o600); err != nil {
		t.Fatalf("write fake cgroup file: %v", err)
	}
	root := filepath.Join(dir, "root")
	groupDir := filepath.Join(root, filepath.FromSlash(group))
	if err := os.MkdirAll(groupDir, 0o700); err != nil {
		t.Fatalf("make fake cgroup: %v", err)
	}
	events := filepath.Join(groupDir, "memory.events")
	writeMemoryEvents(t, events, oomKills)
	if err := os.WriteFile(filepath.Join(groupDir, "memory.max"), []byte(limit+"\n"), 0o600); err != nil {
		t.Fatalf("write fake memory.max: %v", err)
	}
	return memoryCap{self: self, root: root}, events
}

// writeMemoryEvents writes a memory.events file at path whose oom_kill count is oomKills, as cgroup v2 lays it out.
func writeMemoryEvents(t *testing.T, path, oomKills string) {
	t.Helper()
	body := "low 0\nhigh 0\nmax 4\noom 1\noom_kill " + oomKills + "\noom_group_kill 0\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fake memory.events: %v", err)
	}
}

func TestHolderCommandCapsTheScopeAtScopeMemoryWithNoSwap(t *testing.T) {
	t.Parallel()
	child := LaunchConfig{Scope: "/run/systemd-run", Session: testSession}
	for _, tc := range []struct {
		name   string
		memory int64
		want   string
	}{
		{name: "default", memory: 0, want: "MemoryMax=8589934592"},
		{name: "configured", memory: 512 << 20, want: "MemoryMax=536870912"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := child
			cfg.ScopeMemory = tc.memory
			_, argv := holderCommand(cfg, "/bin/orrery", "/state", "orrery-hold-unit")
			want := []string{
				"--user", "--scope", "--collect", "-p", "OOMPolicy=continue",
				"-p", tc.want, "-p", "MemorySwapMax=0", "--unit", "orrery-hold-unit", "/bin/orrery",
			}
			if len(argv) < len(want) || !slices.Equal(argv[:len(want)], want) {
				t.Fatalf("scope argv %q, want it to start with %q", argv, want)
			}
		})
	}
}

func TestHolderCommandWithoutScopeSetsNoCap(t *testing.T) {
	t.Parallel()
	program, argv := holderCommand(LaunchConfig{Session: testSession, ScopeMemory: 1 << 30}, "/bin/orrery", "/s", "")
	if program != "/bin/orrery" {
		t.Fatalf("program %q, want orrery itself", program)
	}
	for _, arg := range argv {
		if strings.HasPrefix(arg, "Memory") {
			t.Fatalf("plain holder argv %q carries a memory property", argv)
		}
	}
}

func TestMemoryCapReadsTheScopesKillsAndLimit(t *testing.T) {
	t.Parallel()
	mem, _ := fakeMemoryCap(t, fakeScopeGroup, "2", "8589934592")
	kills, ok := mem.kills()
	if !ok || kills != 2 {
		t.Fatalf("kills = %d, %v; want 2, true", kills, ok)
	}
	if got := mem.limit(); got != "8589934592" {
		t.Fatalf("limit = %q, want 8589934592", got)
	}
}

func TestMemoryCapReadsNothingOutsideAHolderScope(t *testing.T) {
	t.Parallel()
	mem, _ := fakeMemoryCap(t, "/user.slice/app.slice/orreryd.service", "2", "max")
	if kills, ok := mem.kills(); ok {
		t.Fatalf("kills = %d, true; want none read outside a holder scope", kills)
	}
	if got := mem.limit(); got != "unknown" {
		t.Fatalf("limit = %q, want unknown", got)
	}
}

func TestMemoryCapReadsNothingWithoutItsFiles(t *testing.T) {
	t.Parallel()
	missing := memoryCap{self: filepath.Join(t.TempDir(), "cgroup"), root: t.TempDir()}
	if _, ok := missing.kills(); ok {
		t.Fatal("kills read with no cgroup file")
	}
	mem, events := fakeMemoryCap(t, fakeScopeGroup, "x", "max")
	if _, ok := mem.kills(); ok {
		t.Fatal("kills read from an oom_kill that is no number")
	}
	if err := os.Remove(events); err != nil {
		t.Fatalf("remove memory.events: %v", err)
	}
	if _, ok := mem.kills(); ok {
		t.Fatal("kills read with no memory.events")
	}
}

func TestHolderRecordsTheKillsOfTheScopesMemoryCap(t *testing.T) {
	t.Parallel()
	cfg := newHolderConfig(t)
	mem, events := fakeMemoryCap(t, fakeScopeGroup, "1", "8589934592")
	cfg.memory = mem
	// The child stands in for the kernel: it raises the scope's count by two, as two kills at the cap would, then ends
	// as a shell whose test process was killed does.
	script := "printf 'oom_kill 3\\n' > \"$1\"; echo partial >&2; exit 137"
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", script, "sh", events}, Dir: t.TempDir()}
	run := startHolder(t.Context(), t, cfg)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	want := Exit{
		Reason:      ReasonExited,
		Code:        137,
		Stderr:      "partial\norrery holder: the scope's memory cap (memory.max 8589934592) killed 2 processes",
		MemoryKills: 2,
	}
	if got := readExitFile(t, cfg.Dir); got != want {
		t.Errorf("exit file = %+v, want %+v", got, want)
	}
	if run.exit != want {
		t.Errorf("Run's exit = %+v, want %+v", run.exit, want)
	}
}

func TestHolderRecordsNoKillsWhenTheCountHolds(t *testing.T) {
	t.Parallel()
	cfg := newHolderConfig(t)
	cfg.memory, _ = fakeMemoryCap(t, fakeScopeGroup, "1", "8589934592")
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", "echo err >&2; exit 3"}, Dir: t.TempDir()}
	run := startHolder(t.Context(), t, cfg)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	want := Exit{Reason: ReasonExited, Code: 3, Stderr: "err\n"}
	if run.exit != want {
		t.Errorf("Run's exit = %+v, want %+v", run.exit, want)
	}
}

func TestMemoryCapLineNamesOneProcess(t *testing.T) {
	t.Parallel()
	want := "orrery holder: the scope's memory cap (memory.max max) killed 1 process"
	if got := memoryCapLine(1, "max"); got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}
