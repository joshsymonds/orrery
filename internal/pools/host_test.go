package pools

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

func TestDiscoverThroughHost(t *testing.T) {
	home := isolate(t)
	root := newRepo(t)
	projectFile := filepath.Join(root, ".orrery", "project.toml")
	writeFile(t, projectFile, poolText("alpha", "box-a", "m1"))
	rec := hostexectest.NewRecorder(hostexec.NewLocal())

	if _, _, err := Load(context.Background(), rec, root, fakeEnv(map[string]string{"HOME": home})); err != nil {
		t.Fatalf("Load(%s): %v", root, err)
	}
	calls := rec.Calls()
	if !slices.ContainsFunc(calls, func(c hostexectest.Call) bool {
		return c.Op == "ReadFile" && c.Path == projectFile
	}) {
		t.Fatalf("Load did not read %s through the host: calls %+v", projectFile, calls)
	}
	if !slices.ContainsFunc(calls, func(c hostexectest.Call) bool {
		return c.Op == "Git" && c.Path == root && len(c.Args) > 0 && c.Args[0] == "rev-parse"
	}) {
		t.Fatalf("Load did not run rev-parse in %s through the host: calls %+v", root, calls)
	}
}
