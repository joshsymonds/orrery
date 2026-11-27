package enginehost

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// stepStampRemoteHome is the HOME of the step's host in TestStepStampIsReadOnTheStepsHost. No such path exists on
// this machine, so a stamp found under it was read through the host's files.
const stepStampRemoteHome = "/orrery-test-remote-home"

// stepStampHostFiles is the file system of another host: each path under stepStampRemoteHome is read from the same
// path under root on this machine, and every other path is missing.
type stepStampHostFiles struct {
	hostexec.Files
	root string
}

// local maps path to this machine, or reports false for a path outside the host's home.
func (f stepStampHostFiles) local(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, stepStampRemoteHome)
	if !ok {
		return "", false
	}
	return filepath.Join(f.root, rest), true
}

// EvalSymlinks resolves path on the host.
func (f stepStampHostFiles) EvalSymlinks(_ context.Context, path string) (string, error) {
	local, ok := f.local(path)
	if !ok {
		return "", &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
	}
	resolved, err := filepath.EvalSymlinks(local)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		return "", err
	}
	return stepStampRemoteHome + strings.TrimPrefix(resolved, root), nil
}

// Lstat describes path on the host.
func (f stepStampHostFiles) Lstat(_ context.Context, path string) (fs.FileInfo, error) {
	local, ok := f.local(path)
	if !ok {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
	}
	return os.Lstat(local)
}

// Stat describes path on the host.
func (f stepStampHostFiles) Stat(_ context.Context, path string) (fs.FileInfo, error) {
	local, ok := f.local(path)
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	return os.Stat(local)
}

// ReadFile reads path on the host.
func (f stepStampHostFiles) ReadFile(_ context.Context, path string) ([]byte, error) {
	local, ok := f.local(path)
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return os.ReadFile(filepath.Clean(local))
}

// TestStepStampIsReadOnTheStepsHost binds the Haiku caller of a step whose folder is on another host. The folder and
// its stamp exist only on that host, under a HOME this machine does not have, so the bound caller carries the stamp's
// env only when the stamp was read through the step host's files, and a folder the host lacks fails naming it.
func TestStepStampIsReadOnTheStepsHost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stepGitTestWrite(t, filepath.Join(root, "stamped"), filepath.Join(".claude", "settings.local.json"), stepStampValid)
	if err := os.MkdirAll(filepath.Join(root, "stamped", "repo"), 0o750); err != nil {
		t.Fatal(err)
	}
	host := stepStampHostFiles{root: root}
	s := &Steps{haiku: NewHaiku(HaikuConfig{}), environ: []string{"HOME=" + stepStampRemoteHome}}

	var cfg stepRunConfig
	if err := s.bindHaiku(t.Context(), host, &cfg, stepStampRemoteHome+"/stamped/repo"); err != nil {
		t.Fatalf("bindHaiku on the step's host: %v", err)
	}
	bound, ok := cfg.haiku.(haikuBound)
	if !ok {
		t.Fatalf("haiku = %T, want the caller bound to the host folder's stamp", cfg.haiku)
	}
	if bound.env["ORRERY_STAMP_MARK"] != "kept" {
		t.Errorf("bound env = %v, want the stamp's env read from the step's host", bound.env)
	}

	err := s.bindHaiku(t.Context(), host, &cfg, stepStampRemoteHome+"/missing")
	if err == nil || !strings.Contains(err.Error(), stepStampRemoteHome+"/missing") {
		t.Errorf("bindHaiku of a folder the host lacks = %v, want an error naming it", err)
	}
}

// stepStampCountingFiles counts the reads it passes to its host's files.
type stepStampCountingFiles struct {
	stepStampHostFiles
	reads *int
}

// EvalSymlinks counts the read and resolves path on the host.
func (f stepStampCountingFiles) EvalSymlinks(ctx context.Context, path string) (string, error) {
	*f.reads++
	return f.stepStampHostFiles.EvalSymlinks(ctx, path)
}

// TestReadoptStepStampIsReadAtFirstCall binds a re-adopted step's Haiku caller: the binding reads nothing from the
// step's host, so a host that does not answer cannot hold the re-adoption up, and the caller's first call reads the
// stamp through that host and binds it, failing with the host's error when the folder is missing there.
func TestReadoptStepStampIsReadAtFirstCall(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stepGitTestWrite(t, filepath.Join(root, "stamped"), filepath.Join(".claude", "settings.local.json"), stepStampValid)
	if err := os.MkdirAll(filepath.Join(root, "stamped", "repo"), 0o750); err != nil {
		t.Fatal(err)
	}
	var reads int
	host := stepStampCountingFiles{stepStampHostFiles: stepStampHostFiles{root: root}, reads: &reads}
	s := &Steps{haiku: NewHaiku(HaikuConfig{}), environ: []string{"HOME=" + stepStampRemoteHome}}

	var cfg stepRunConfig
	if err := s.bindHaikuLazily(host, &cfg, stepStampRemoteHome+"/missing"); err != nil {
		t.Fatalf("bindHaikuLazily: %v", err)
	}
	if reads != 0 {
		t.Fatalf("binding read the host %d times, want none before the first call", reads)
	}
	lazy, ok := cfg.haiku.(*stampedHaiku)
	if !ok {
		t.Fatalf("haiku = %T, want the caller that binds at its first call", cfg.haiku)
	}
	if _, err := lazy.Call(t.Context(), "{}", "input"); err == nil ||
		!strings.Contains(err.Error(), stepStampRemoteHome+"/missing") {
		t.Errorf("first call for a folder the host lacks = %v, want an error naming it", err)
	}
	if reads == 0 {
		t.Errorf("the first call read nothing from the host")
	}
	if lazy.bound != nil {
		t.Errorf("a failed first call bound %T, want nothing bound so the next call reads again", lazy.bound)
	}

	relative := &Steps{haiku: NewHaiku(HaikuConfig{}), environ: []string{"HOME=relative-home"}}
	if err := relative.bindHaikuLazily(host, &cfg, stepStampRemoteHome+"/stamped/repo"); err == nil {
		t.Errorf("bindHaikuLazily with a relative HOME = nil, want the HOME refused at once")
	}
}
