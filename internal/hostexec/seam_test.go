package hostexec

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// seamHolderPath is the import path of the holder package, which only the session manager and its drain may import.
const seamHolderPath = "github.com/joshsymonds/orrery/internal/holder"

// seamPackages returns the engine packages the scan covers, each a directory under internal/.
func seamPackages() []string {
	return []string{"enginehost", "build", "compose", "interpret", "guard", "pools", "gates", "reviewers", "agents"}
}

// seamExemptFiles returns the files the scan skips, by their path under internal/. Each is controller-side or
// session-manager code, and the reason it may reach git, files or holders directly is written beside it.
func seamExemptFiles() map[string]bool {
	return map[string]bool{
		// The session manager: it resolves claude with exec.LookPath and stats a session directory on the controller.
		"enginehost/manager.go": true,
		// The session manager's reconcile: it lists the controller's holders directory and re-adopts each holder.
		"enginehost/reconcile.go": true,
		// The session manager's drain: it settles an ended holder into the store, so it imports the holder package.
		"enginehost/drain.go": true,
		// Haiku's own call: it runs claude as a process group of its own and kills that group with syscall.Kill.
		"enginehost/haiku.go": true,
		// The limit prober: it runs claude as a process group of its own and kills that group with syscall.Kill, like Haiku,
		// and its Exec seam takes *exec.Cmd, so it imports os/exec.
		"enginehost/limitprobe.go": true,
		// Controller-side blobs: evidence bytes are stored under the controller's state directory, not on a host.
		"enginehost/evidenceblob.go": true,
	}
}

// seamForbiddenCalls returns the calls the scan forbids, each as its import path, a dot and the function's name.
// os.TempDir and os.Getenv are allowed, so they are not among them.
func seamForbiddenCalls() []string {
	return []string{
		"os.Open", "os.OpenFile", "os.OpenRoot", "os.OpenInRoot", "os.Create", "os.CreateTemp", "os.ReadFile",
		"os.WriteFile", "os.ReadDir", "os.Stat", "os.Lstat", "os.Mkdir", "os.MkdirAll", "os.MkdirTemp", "os.Remove",
		"os.RemoveAll", "os.Rename", "os.Symlink", "os.Readlink", "os.Link", "os.Chmod", "os.Chtimes", "os.Truncate",
		"os.DirFS", "path/filepath.EvalSymlinks", "path/filepath.Walk", "path/filepath.WalkDir", "path/filepath.Glob",
		"syscall.Kill",
	}
}

// seamFixture is a Go source file holding one of each form the scan forbids. Each forbidden line ends in a // want
// comment that names the call the scan reports on that line. The allowed calls carry none.
const seamFixture = `package fixture

import (
	"os"
	"os/exec" // want import "os/exec"
	fp "path/filepath"
	"syscall"

	h "github.com/joshsymonds/orrery/internal/holder" // want import "github.com/joshsymonds/orrery/internal/holder"
)

var (
	_ = os.TempDir
	_ = os.Getenv
	_ = "/proc/self/stat" // want "/proc/self/stat"
	_ = syscall.Kill // want syscall.Kill
	_ = fp.EvalSymlinks // want fp.EvalSymlinks
	_ = fp.Walk // want fp.Walk
	_ = fp.WalkDir // want fp.WalkDir
	_ = fp.Glob // want fp.Glob
	_ = os.Open // want os.Open
	_ = os.OpenFile // want os.OpenFile
	_ = os.OpenRoot // want os.OpenRoot
	_ = os.OpenInRoot // want os.OpenInRoot
	_ = os.Create // want os.Create
	_ = os.CreateTemp // want os.CreateTemp
	_ = os.ReadFile // want os.ReadFile
	_ = os.WriteFile // want os.WriteFile
	_ = os.ReadDir // want os.ReadDir
	_ = os.Stat // want os.Stat
	_ = os.Lstat // want os.Lstat
	_ = os.Mkdir // want os.Mkdir
	_ = os.MkdirAll // want os.MkdirAll
	_ = os.MkdirTemp // want os.MkdirTemp
	_ = os.Remove // want os.Remove
	_ = os.RemoveAll // want os.RemoveAll
	_ = os.Rename // want os.Rename
	_ = os.Symlink // want os.Symlink
	_ = os.Readlink // want os.Readlink
	_ = os.Link // want os.Link
	_ = os.Chmod // want os.Chmod
	_ = os.Chtimes // want os.Chtimes
	_ = os.Truncate // want os.Truncate
	_ = os.DirFS // want os.DirFS
)
`

// seamFinding is one forbidden form the scan found: the file and line it is on, and the call as the source writes it.
type seamFinding struct {
	file string
	line int
	call string
}

// seamForbiddenImport reports whether an engine package may not import importPath: os/exec, and the holder package
// under any alias.
func seamForbiddenImport(importPath string) bool {
	return importPath == "os/exec" || importPath == seamHolderPath
}

// seamCalls parses the Go source of file and returns each forbidden form in it, in source order. A selector counts
// when its qualifier names an import of the forbidden package in this file, under whatever alias the file gives it.
// A string literal counts when its value begins with /proc.
func seamCalls(file string, src []byte) ([]seamFinding, error) {
	fset := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fset, file, src, 0)
	if parseErr != nil {
		return nil, fmt.Errorf("parse %s: %w", file, parseErr)
	}
	var found []seamFinding
	imported := make(map[string]string, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		importPath, quoteErr := strconv.Unquote(spec.Path.Value)
		if quoteErr != nil {
			return nil, fmt.Errorf("import in %s: %w", file, quoteErr)
		}
		if seamForbiddenImport(importPath) {
			line := fset.Position(spec.Pos()).Line
			call := "import " + strconv.Quote(importPath)
			found = append(found, seamFinding{file: file, line: line, call: call})
		}
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imported[local] = importPath
	}
	var walkErr error
	forbidden := seamForbiddenCalls()
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.BasicLit:
			if typed.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(typed.Value)
			if unquoteErr != nil {
				walkErr = fmt.Errorf("string in %s: %w", file, unquoteErr)
				return false
			}
			if strings.HasPrefix(value, "/proc") {
				line := fset.Position(typed.Pos()).Line
				found = append(found, seamFinding{file: file, line: line, call: strconv.Quote(value)})
			}
		case *ast.SelectorExpr:
			qualifier, isIdent := typed.X.(*ast.Ident)
			if !isIdent {
				return true
			}
			importPath, isImport := imported[qualifier.Name]
			if isImport && slices.Contains(forbidden, importPath+"."+typed.Sel.Name) {
				line := fset.Position(typed.Pos()).Line
				call := qualifier.Name + "." + typed.Sel.Name
				found = append(found, seamFinding{file: file, line: line, call: call})
			}
		}
		return true
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return found, nil
}

// seamScanPackage scans the non-test .go files of the engine package pkg, except the exempt ones, and returns each
// forbidden form in them. It fails when the package holds no non-test .go file, so a wrong directory cannot pass.
func seamScanPackage(pkg string) ([]seamFinding, error) {
	dir := filepath.Join("..", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	exempt := seamExemptFiles()
	var found []seamFinding
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		if exempt[filepath.Join(pkg, name)] {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, name), readErr)
		}
		hits, scanErr := seamCalls(filepath.Join("internal", pkg, name), src)
		if scanErr != nil {
			return nil, scanErr
		}
		found = append(found, hits...)
	}
	if scanned == 0 {
		return nil, fmt.Errorf("no non-test .go file in %s to scan", dir)
	}
	return found, nil
}

// seamWants returns "line call" for each line of src that a // want comment marks, in line order.
func seamWants(src string) []string {
	var wants []string
	for index, text := range strings.Split(src, "\n") {
		_, call, marked := strings.Cut(text, "// want ")
		if marked {
			wants = append(wants, fmt.Sprintf("%d %s", index+1, call))
		}
	}
	return wants
}

// TestHostSeam holds the engine packages to the seam: their non-test code reaches git, files and holders only through
// a Host, so the scan fails on a forbidden call in any of them. Its last subtest proves the scan finds each form it
// forbids, so a scan that finds nothing cannot pass.
func TestHostSeam(t *testing.T) {
	t.Parallel()
	for _, pkg := range seamPackages() {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()
			found, err := seamScanPackage(pkg)
			if err != nil {
				t.Fatalf("scan %s: %v", pkg, err)
			}
			for _, finding := range found {
				t.Errorf("%s:%d calls %s: reach it through the Host instead",
					finding.file, finding.line, finding.call)
			}
		})
	}
	t.Run("the scan finds each forbidden call", func(t *testing.T) {
		t.Parallel()
		got, err := seamCalls("fixture.go", []byte(seamFixture))
		if err != nil {
			t.Fatalf("scan fixture: %v", err)
		}
		gotLines := make([]string, 0, len(got))
		for _, finding := range got {
			gotLines = append(gotLines, fmt.Sprintf("%d %s", finding.line, finding.call))
		}
		if wantLines := seamWants(seamFixture); !slices.Equal(gotLines, wantLines) {
			t.Errorf("forbidden forms found %q, want %q", gotLines, wantLines)
		}
	})
}
