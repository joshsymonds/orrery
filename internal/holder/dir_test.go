package holder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envOf returns a getenv function over a fixed map, so a test never touches the process environment.
func envOf(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestHoldersDirUnderXDGStateHome(t *testing.T) {
	base := t.TempDir()
	got, err := HoldersDir(envOf(map[string]string{"XDG_STATE_HOME": base}))
	if err != nil {
		t.Fatalf("HoldersDir: %v", err)
	}
	want := filepath.Join(base, "orrery", "holders")
	if got != want {
		t.Fatalf("HoldersDir = %q, want %q", got, want)
	}
}

func TestHoldersDirUnderHome(t *testing.T) {
	home := t.TempDir()
	got, err := HoldersDir(envOf(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("HoldersDir: %v", err)
	}
	want := filepath.Join(home, ".local", "state", "orrery", "holders")
	if got != want {
		t.Fatalf("HoldersDir = %q, want %q", got, want)
	}
}

func TestHoldersDirNeedsStateHomeOrHome(t *testing.T) {
	got, err := HoldersDir(envOf(map[string]string{}))
	if err == nil {
		t.Fatalf("HoldersDir with neither variable = %q, want an error", got)
	}
	if got != "" {
		t.Fatalf("HoldersDir returned %q alongside its error", got)
	}
}

func TestSessionDirAcceptsSessionID(t *testing.T) {
	holders := t.TempDir()
	const id = "0123456789abcdef0123456789abcdef"
	got, err := SessionDir(holders, id)
	if err != nil {
		t.Fatalf("SessionDir(%q): %v", id, err)
	}
	if want := filepath.Join(holders, id); got != want {
		t.Fatalf("SessionDir = %q, want %q", got, want)
	}
}

func TestSessionDirAcceptsLongestName(t *testing.T) {
	holders := t.TempDir()
	name := "a" + strings.Repeat("b", 63)
	if _, err := SessionDir(holders, name); err != nil {
		t.Fatalf("SessionDir with a 64-byte name: %v", err)
	}
}

func TestSessionDirRefusesBadNames(t *testing.T) {
	holders := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", strings.Repeat("a", 65)} {
		got, err := SessionDir(holders, name)
		if !errors.Is(err, ErrBadSession) {
			t.Errorf("SessionDir(%q) error = %v, want ErrBadSession", name, err)
		}
		if got != "" {
			t.Errorf("SessionDir(%q) returned %q alongside its error", name, got)
		}
	}
}

// pathOfLen returns a clean absolute directory whose SocketPath is exactly total bytes long.
func pathOfLen(t *testing.T, total int) string {
	t.Helper()
	dir := "/" + strings.Repeat("d", total-len(SocketName)-2)
	if got := len(filepath.Join(dir, SocketName)); got != total {
		t.Fatalf("fixture path is %d bytes, want %d", got, total)
	}
	return dir
}

func TestSocketPathUnder108BytesPasses(t *testing.T) {
	dir := pathOfLen(t, 107)
	got, err := SocketPath(dir)
	if err != nil {
		t.Fatalf("SocketPath at 107 bytes: %v", err)
	}
	if want := filepath.Join(dir, SocketName); got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
}

func TestSocketPathAt108BytesFails(t *testing.T) {
	dir := pathOfLen(t, 108)
	got, err := SocketPath(dir)
	if !errors.Is(err, ErrSocketPathTooLong) {
		t.Fatalf("SocketPath at 108 bytes error = %v, want ErrSocketPathTooLong", err)
	}
	if got != "" {
		t.Fatalf("SocketPath at 108 bytes returned %q alongside its error", got)
	}
	path := filepath.Join(dir, SocketName)
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "108") {
		t.Fatalf("error %q does not name the path %q and its length 108", err, path)
	}
}

func TestPrepareDirCreatesPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orrery", "holders", "0123456789abcdef0123456789abcdef")
	if err := PrepareDir(dir); err != nil {
		t.Fatalf("PrepareDir: %v", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("mode = %o, want 0700", perm)
	}
}

func TestPrepareDirAcceptsOwnPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "holders", "sess")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := PrepareDir(dir); err != nil {
		t.Fatalf("PrepareDir on its own 0700 dir: %v", err)
	}
}

func TestPrepareDirRefusesWrongMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "holders")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := PrepareDir(dir); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("PrepareDir on a 0755 dir error = %v, want ErrUnsafeDir", err)
	}
}

func TestPrepareDirRefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "holders")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := PrepareDir(path); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("PrepareDir on a regular file error = %v, want ErrUnsafeDir", err)
	}
}

func TestPrepareDirRefusesSymlinkToPrivateDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	link := filepath.Join(root, "holders")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := PrepareDir(link); !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("PrepareDir on a symlink to a 0700 dir error = %v, want ErrUnsafeDir", err)
	}
}
