package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoStepWaitVerb refuses `orrery step wait` with and without --all as a usage error that carries stepUsage. Each
// exits 2 and sends orreryd nothing.
func TestNoStepWaitVerb(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"wait", []string{"step", "wait", "s1"}},
		{"wait with --all", []string{"step", "wait", "--all", "s1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &stepsFake{}
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			serveStepsFake(t, daemon, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: "+stepUsage+"\n") {
				t.Errorf("stderr %q, want a usage error carrying %q", res.stderr, stepUsage)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("orreryd received %q, want nothing", got)
			}
		})
	}
}
