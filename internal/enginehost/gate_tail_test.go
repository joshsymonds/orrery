package enginehost

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// gateTailMarker ends the output of the tail gates, after more bytes than the default tail keeps.
const gateTailMarker = "END"

// gateTailLines returns count lines of ten bytes each, "line" and a five-digit number from start on, each ending in a
// newline.
func gateTailLines(start, count int) string {
	var b strings.Builder
	for i := start; i < start+count; i++ {
		fmt.Fprintf(&b, "line%05d\n", i)
	}
	return b.String()
}

// gateDefaultTailScript is the script of the default-tail gates: 1000 lines of ten bytes, 10000 bytes in all.
func gateDefaultTailScript() string {
	return gateRedactHeredoc(gateTailLines(0, 1000), false)
}

// gateDefaultTailWant is the stdout the default 8 KiB tail gives gateDefaultTailScript. The newest 8 KiB start at byte
// 1808, inside line 180, so that line is cut and the placeholder stands for it. Lines 181 to 999 are kept whole.
func gateDefaultTailWant() string {
	return "[redacted]\n" + gateTailLines(181, 819)
}

// runGateScript runs script as a gate with tail, and returns the result the gate ends with.
func runGateScript(t *testing.T, script string, tail int) gateResult {
	t.Helper()
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:    gateTestHost(),
		Argv:    []string{"/bin/sh", "-c", script},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv(),
		Tail:    tail,
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	return result
}

// runTailGate runs a gate that prints 64 KiB of a's and then gateTailMarker, with the tail it is given, and returns the
// stdout the gate's result holds.
func runTailGate(t *testing.T, tail int) string {
	t.Helper()
	return runGateScript(t, `head -c 65536 /dev/zero | tr '\0' a; printf END`, tail).Stdout
}

// TestGateTailKeepsTheRequestedOutput runs a gate whose output is longer than the default tail with a tail of 1 MiB.
// The result holds all 64 KiB and the marker.
func TestGateTailKeepsTheRequestedOutput(t *testing.T) {
	t.Parallel()
	got := runTailGate(t, 1<<20)
	want := strings.Repeat("a", 64<<10) + gateTailMarker
	if got != want {
		t.Fatalf("len(Stdout) = %d, want the 64 KiB and the marker (len %d)", len(got), len(want))
	}
}

// TestGateTailZeroKeepsTheDefaultTail runs the default-tail gate with a tail of 0. The result holds the newest 8 KiB,
// cut at its start, and the lines after the cut.
func TestGateTailZeroKeepsTheDefaultTail(t *testing.T) {
	t.Parallel()
	got := runGateScript(t, gateDefaultTailScript(), 0).Stdout
	want := gateDefaultTailWant()
	if got != want {
		t.Fatalf("len(Stdout) = %d, want the placeholder and lines 181 to 999 (len %d)", len(got), len(want))
	}
}
