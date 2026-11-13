package holdertest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(Main(m))
}

func TestOrreryResolvesToBuiltBinaryFirstOnPath(t *testing.T) {
	got, err := exec.LookPath("orrery")
	if err != nil {
		t.Fatalf("LookPath(orrery): %v", err)
	}
	first, _, _ := strings.Cut(os.Getenv("PATH"), string(os.PathListSeparator))
	if dir := filepath.Dir(got); dir != first {
		t.Fatalf("orrery resolves to %s, want it in the first PATH entry %s", got, first)
	}
	if !strings.HasPrefix(filepath.Base(first), "orrery-holder-bin-") {
		t.Fatalf("first PATH entry %s is not the directory Main built orrery into", first)
	}
}

func TestBuiltOrreryHolderWithoutArgumentsExitsTwo(t *testing.T) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(t.Context(), "orrery", "holder")
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("orrery holder: want an exit status, got %v", err)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Fatalf("orrery holder exit code = %d, want 2; stderr: %s", code, stderr.String())
	}
}
