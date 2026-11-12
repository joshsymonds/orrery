package gates

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// resolveEnv returns a getenv that names a fresh HOME and XDG_CONFIG_HOME, so no machine pool file is read.
func resolveEnv(t *testing.T) func(string) string {
	t.Helper()
	vars := map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()}
	return func(name string) string { return vars[name] }
}

// writeFile writes content to path, creating its directory, and returns path.
func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// projectPath is the path of the project file under dir.
func projectPath(dir string) string {
	return filepath.Join(dir, ".orrery", "project.toml")
}

// epicAlpha is a gates file body that defines alpha with the argv, limit and needs from gateKeys.
func epicAlpha() string {
	return file(table("gates.alpha", gateKeys()...))
}

// projectBeta is a project file body that defines beta with its own argv and limit.
func projectBeta() string {
	return file(table("gates.beta", `argv = ["/opt/tools/beta"]`, `limit = 60`))
}

func TestResolveFindsEpicGate(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())

	got, err := Resolve(context.Background(), "alpha", gatesFile, dir, resolveEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	want := Resolved{
		Gate: Gate{
			Name:  "alpha",
			Argv:  []string{"/opt/tools/run", "-c", "check"},
			Limit: 1800 * time.Second,
			Needs: []Need{{Pool: "builds", Count: 2}, {Pool: "gate", Count: 1}},
		},
		Source: SourceEpic,
		File:   gatesFile,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve = %+v, want %+v", got, want)
	}
}

func TestResolveFindsProjectOnlyGateFromProjectFile(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())
	pf := writeFile(t, projectPath(dir), projectBeta())

	got, err := Resolve(context.Background(), "beta", gatesFile, dir, resolveEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	want := Resolved{
		Gate: Gate{
			Name:  "beta",
			Argv:  []string{"/opt/tools/beta"},
			Limit: 60 * time.Second,
			Needs: []Need{{Pool: "gate", Count: 1}},
		},
		Source: SourceProject,
		File:   pf,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve = %+v, want %+v", got, want)
	}
}

func TestResolvePrefersGatesFileWhenBothDefineName(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())
	writeFile(t, projectPath(dir), file(table("gates.alpha", `argv = ["/opt/tools/project"]`, `limit = 60`)))

	got, err := Resolve(context.Background(), "alpha", gatesFile, dir, resolveEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceEpic || got.File != gatesFile {
		t.Fatalf("Source, File = %q, %q; want %q, %q", got.Source, got.File, SourceEpic, gatesFile)
	}
	if want := []string{"/opt/tools/run", "-c", "check"}; !reflect.DeepEqual(got.Argv, want) {
		t.Fatalf("Argv = %q, want the gates file's %q", got.Argv, want)
	}
}

func TestResolveEmptyGatesFileReadsProjectFileAlone(t *testing.T) {
	dir := t.TempDir()
	pf := writeFile(t, projectPath(dir), projectBeta())

	got, err := Resolve(context.Background(), "beta", "", dir, resolveEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceProject || got.File != pf {
		t.Fatalf("Source, File = %q, %q; want %q, %q", got.Source, got.File, SourceProject, pf)
	}
	if got.Name != "beta" {
		t.Fatalf("Name = %q, want beta", got.Name)
	}
}

func TestResolveRefusesNameNeitherFileDefines(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())
	pf := writeFile(t, projectPath(dir), projectBeta())

	got, err := Resolve(context.Background(), "gamma", gatesFile, dir, resolveEnv(t))
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("Resolve error = %v, want one wrapping ErrUnknown", err)
	}
	for _, want := range []string{"gamma", gatesFile, pf} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
	if !reflect.DeepEqual(got, Resolved{}) {
		t.Fatalf("Resolve = %+v on refusal, want the zero value", got)
	}
}

func TestResolveRefusesNameWithNoFilesAtAll(t *testing.T) {
	dir := t.TempDir()

	_, err := Resolve(context.Background(), "gamma", "", dir, resolveEnv(t))
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("Resolve error = %v, want one wrapping ErrUnknown", err)
	}
}

func TestResolveRefusesMissingGatesFileEvenWhenProjectDefinesName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, projectPath(dir), projectBeta())
	missing := filepath.Join(dir, "missing.toml")

	got, err := Resolve(context.Background(), "beta", missing, dir, resolveEnv(t))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Resolve error = %v, want the missing gates file", err)
	}
	if !reflect.DeepEqual(got, Resolved{}) {
		t.Fatalf("Resolve = %+v on refusal, want the zero value", got)
	}
}

// malformedToml is a file body that does not parse.
const malformedToml = "[gates\n"

func TestResolveReturnsGatesFileParseErrorAsIs(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), malformedToml)
	_, want := ParseFile(gatesFile, []byte(malformedToml))
	if want == nil {
		t.Fatal("ParseFile accepted the malformed file")
	}

	_, err := Resolve(context.Background(), "alpha", gatesFile, dir, resolveEnv(t))
	if err == nil || err.Error() != want.Error() {
		t.Fatalf("Resolve error = %v, want the parse error %v", err, want)
	}
	if errors.Is(err, ErrUnknown) {
		t.Fatal("a parse error must not read as an unknown name")
	}
}

func TestResolveReturnsProjectFileParseErrorAsIs(t *testing.T) {
	dir := t.TempDir()
	pf := writeFile(t, projectPath(dir), malformedToml)
	_, want := ParseProject(pf, []byte(malformedToml))
	if want == nil {
		t.Fatal("ParseProject accepted the malformed file")
	}

	_, err := Resolve(context.Background(), "alpha", "", dir, resolveEnv(t))
	if err == nil || err.Error() != want.Error() {
		t.Fatalf("Resolve error = %v, want the parse error %v", err, want)
	}
	if errors.Is(err, ErrUnknown) {
		t.Fatal("a parse error must not read as an unknown name")
	}
}

func TestResolveFromGatesFileDoesNotDiscover(t *testing.T) {
	dir := t.TempDir()
	gatesFile := writeFile(t, filepath.Join(dir, "gates.toml"), epicAlpha())
	noEnv := func(string) string { return "" }

	got, err := Resolve(context.Background(), "alpha", gatesFile, dir, noEnv)
	if err != nil {
		t.Fatalf("Resolve error = %v; Discover ran though the gates file defines the name", err)
	}
	if got.Source != SourceEpic || got.Name != "alpha" {
		t.Fatalf("Resolve = %+v, want alpha from the gates file", got)
	}
}
