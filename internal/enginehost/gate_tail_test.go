package enginehost

import (
	"strings"
	"testing"
	"time"
)

// gateTailMarker ends the output of the tail gates, after more bytes than the default tail keeps.
const gateTailMarker = "END"

// runTailGate runs a gate that prints 64 KiB of a's and then gateTailMarker, with the tail it is given, and returns the
// output the gate's result holds.
func runTailGate(t *testing.T, tail int) string {
	t.Helper()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `head -c 65536 /dev/zero | tr '\0' a; printf END`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv(),
		Tail:    tail,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	return result.Output
}

// TestGateTailKeepsTheRequestedOutput runs a gate whose output is longer than the default tail with a tail of 1 MiB.
// The result holds all 64 KiB and the marker.
func TestGateTailKeepsTheRequestedOutput(t *testing.T) {
	t.Parallel()
	got := runTailGate(t, 1<<20)
	want := strings.Repeat("a", 64<<10) + gateTailMarker
	if got != want {
		t.Fatalf("len(Output) = %d, want the 64 KiB and the marker (len %d)", len(got), len(want))
	}
}

// TestGateTailZeroKeepsTheDefaultTail runs the same gate with a tail of 0. The result holds the newest 8 KiB, which
// ends in the marker.
func TestGateTailZeroKeepsTheDefaultTail(t *testing.T) {
	t.Parallel()
	got := runTailGate(t, 0)
	want := strings.Repeat("a", 8<<10-len(gateTailMarker)) + gateTailMarker
	if got != want {
		t.Fatalf("len(Output) = %d, want the newest 8 KiB ending in the marker (len %d)", len(got), len(want))
	}
}
