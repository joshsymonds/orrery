package reviewers

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

func TestProjectThroughHost(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".orrery", "project.toml")
	writeFile(t, file, projectText)
	rec := hostexectest.NewRecorder(hostexec.NewLocal())

	if _, err := Project(context.Background(), rec, dir, noMachineFile(t)); err != nil {
		t.Fatalf("Project(%s): %v", dir, err)
	}
	if calls := rec.Calls(); !slices.ContainsFunc(calls, func(c hostexectest.Call) bool {
		return c.Op == "ReadFile" && c.Path == file
	}) {
		t.Fatalf("Project did not read %s through the host: calls %+v", file, calls)
	}
}
