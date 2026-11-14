package gates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/pools"
)

// SourceEpic and SourceProject name the file a gate resolved from.
const (
	SourceEpic    = "epic"
	SourceProject = "project"
)

// ErrUnknown is wrapped by the error Resolve returns when neither file defines the name.
var ErrUnknown = errors.New("unknown gate")

// Resolved is a gate with the file that defines it. Source is SourceEpic or SourceProject, and File is that file's
// path.
type Resolved struct {
	Gate
	Source string
	File   string
}

// Resolve finds the gate named name. The epic's gatesFile is read first when it is not empty, and a missing or
// unreadable file is an error. Only when that file does not define name does Resolve discover the project for dir
// and read its project file. A name neither file defines is an error wrapping ErrUnknown that names every file
// searched. getenv reads the environment for the machine pool file that discovery consults.
func Resolve(ctx context.Context, name, gatesFile, dir string, getenv func(string) string) (Resolved, error) {
	var searched []string
	if gatesFile != "" {
		searched = append(searched, gatesFile)
		gates, err := readGates(gatesFile, ParseFile)
		if err != nil {
			return Resolved{}, err
		}
		if g, ok := findGate(gates, name); ok {
			return Resolved{Gate: g, Source: SourceEpic, File: gatesFile}, nil
		}
	}
	return resolveProject(ctx, name, dir, getenv, searched)
}

// ResolveDefined finds the gate named name in defined, the gate data an epic carries. A name defined there resolves
// from it with no file read. Otherwise the project file for dir is read as Resolve reads it. A name neither defines
// is an error wrapping ErrUnknown that names the epic's gates when defined is not empty, and the project file when
// one exists.
func ResolveDefined(
	ctx context.Context,
	name string,
	defined []Gate,
	dir string,
	getenv func(string) string,
) (Resolved, error) {
	if g, ok := findGate(defined, name); ok {
		return Resolved{Gate: g, Source: SourceEpic}, nil
	}
	var searched []string
	if len(defined) > 0 {
		searched = append(searched, "the epic's gates")
	}
	return resolveProject(ctx, name, dir, getenv, searched)
}

// resolveProject finds name in the project file that pools.Discover finds for dir. searched names the sources the
// caller consulted before the project file. A name the project file does not define is an error wrapping ErrUnknown
// that names searched and the project file.
func resolveProject(
	ctx context.Context,
	name, dir string,
	getenv func(string) string,
	searched []string,
) (Resolved, error) {
	files, err := pools.Discover(ctx, dir, getenv)
	if err != nil {
		return Resolved{}, fmt.Errorf("find the project for %s: %w", dir, err)
	}
	if files.ProjectFile != "" {
		searched = append(searched, files.ProjectFile)
		projectGates, readErr := readGates(files.ProjectFile, ParseProject)
		if readErr != nil {
			return Resolved{}, readErr
		}
		if g, ok := findGate(projectGates, name); ok {
			return Resolved{Gate: g, Source: SourceProject, File: files.ProjectFile}, nil
		}
	}
	if len(searched) == 0 {
		return Resolved{}, fmt.Errorf("gate %q: %w: no gates file or project file for %s", name, ErrUnknown, dir)
	}
	return Resolved{}, fmt.Errorf("gate %q: %w: searched %s", name, ErrUnknown, strings.Join(searched, ", "))
}

// readGates reads the file at path and parses it with parse. A parse error is returned as parse made it.
func readGates(path string, parse func(string, []byte) ([]Gate, error)) ([]Gate, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parse(path, data)
}

// findGate returns the gate named name from gates and whether one exists.
func findGate(gates []Gate, name string) (Gate, bool) {
	i := slices.IndexFunc(gates, func(g Gate) bool { return g.Name == name })
	if i < 0 {
		return Gate{}, false
	}
	return gates[i], true
}
