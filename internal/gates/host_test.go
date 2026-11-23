package gates

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

func TestResolveThroughHost(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())
	rec := hostexectest.NewRecorder(hostexec.NewLocal())

	got, err := Resolve(context.Background(), rec, "alpha", gatesFile, dir, resolveEnv(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.File != gatesFile {
		t.Fatalf("Resolve came from %q, want %q", got.File, gatesFile)
	}
	if calls := rec.Calls(); !slices.ContainsFunc(calls, func(c hostexectest.Call) bool {
		return c.Op == "ReadFile" && c.Path == gatesFile
	}) {
		t.Fatalf("Resolve did not read %s through the host: calls %+v", gatesFile, calls)
	}
}
