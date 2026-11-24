package enginehost

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

// TestGradleEnvironThroughHost makes the Gradle home through a Host that records its calls. The home is made with
// MkdirAll under the state directory, its gradle.properties is written with WriteFile, and the user's caches are linked
// in with Symlink. Those are the only calls that change the file system, so they are the only ones checked.
func TestGradleEnvironThroughHost(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	user := t.TempDir()
	if err := os.Mkdir(filepath.Join(user, "caches"), 0o700); err != nil {
		t.Fatalf("make the user's caches: %v", err)
	}
	recorder := hostexectest.NewRecorder(hostexec.NewLocal())
	if _, err := GradleEnviron(t.Context(), recorder, []string{"KEEP=1"}, stateDir, user); err != nil {
		t.Fatalf("GradleEnviron: %v", err)
	}
	home := filepath.Join(stateDir, gradleHomeName)
	var changes []string
	for _, call := range recorder.Calls() {
		switch call.Op {
		case "MkdirAll", "WriteFile", "Symlink":
			changes = append(changes, call.Op+" "+call.Path)
		}
	}
	want := []string{
		"MkdirAll " + home,
		"WriteFile " + filepath.Join(home, "gradle.properties"),
		"Symlink " + filepath.Join(home, "caches"),
	}
	if !slices.Equal(changes, want) {
		t.Fatalf("recorded changes = %q, want %q", changes, want)
	}
}
