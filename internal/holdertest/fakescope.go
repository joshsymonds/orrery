package holdertest

import (
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
)

// fakeMode is the mode of the fake programs FakeScope writes: run and read by this user only.
const fakeMode = 0o700

// fakeSystemdRun stands in for systemd-run, as fakesystemdrun.sh describes.
//
//go:embed fakesystemdrun.sh
var fakeSystemdRun []byte

// fakeSystemctl stands in for the systemctl beside it, as fakesystemctl.sh describes.
//
//go:embed fakesystemctl.sh
var fakeSystemctl []byte

// FakeScope returns the absolute path of a fake systemd-run for --holder-scope or LaunchConfig.Scope, with a fake
// systemctl beside it, as the holder's scope stop expects. Each test gets its own pair, in a fake-scope directory in the
// directory that holds the test's t.TempDir directories, so every call of one test returns the same pair, and a
// restarted controller in the test stops the scopes the first one started. Scopes are named within that directory, so
// the scopes of two tests never share a name, even for one session id. The directory goes with the test's temporary
// directories. The fakes record their arguments to $FAKE_SYSTEMD_RUN_ARGS and $FAKE_SYSTEMCTL_ARGS when those are set,
// and $FAKE_SYSTEMD_RUN_FAIL fails systemd-run with its text.
func FakeScope(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(t.TempDir()), "fake-scope")
	path := filepath.Join(dir, "systemd-run")
	if _, err := os.Stat(path); err == nil {
		return path
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("inspect fake systemd-run: %v", err)
	}
	if err := os.Mkdir(dir, fakeMode); err != nil {
		t.Fatalf("make fake scope directory: %v", err)
	}
	exectest.WriteFile(t, filepath.Join(dir, "systemctl"), fakeSystemctl, fakeMode)
	exectest.WriteFile(t, path, fakeSystemdRun, fakeMode)
	return path
}
