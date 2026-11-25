package enginehost

import (
	"slices"
	"strings"
	"testing"
)

// nodeCheckLoadLine is the line a long check prints as it starts, as scripts/gate-remote.sh prints it.
const nodeCheckLoadLine = "gate-remote: running just check here (no box; load 139.20 121.48 131.33, nproc 32)"

// nodeCheckLongScript prints the load line on stderr and a failing test on stdout, then 400 lines of filler on each,
// some 10 KiB a stream, and then the end of a test run cut short by its budget.
const nodeCheckLongScript = `echo '` + nodeCheckLoadLine + `' >&2
echo '--- FAIL: TestSlow (3.46s)'
i=0
while [ "$i" -lt 400 ]; do
	echo 'ok  	github.com/x/filler	0.01s'
	echo 'building the next package' >&2
	i=$((i + 1))
done
echo 'tests exceeded 360s budget' >&2
echo FAIL
exit 1
`

// TestNodeChecksKeepTheKeyLinesTheTailsCutOff runs a check whose output is far longer than the tails the store keeps,
// with its load line and its failing test at the start. The stored tails, the newest store.NodeOutputMax bytes of each
// stream, have lost both, and the stored key lines, read from the whole output by the check's holder, keep them with
// the lines at the end.
func TestNodeChecksKeepTheKeyLinesTheTailsCutOff(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", nodeCheckLongScript)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	nodeCheckEnded(t, f.ended)

	got := nodeCheckStored(t, f.st)
	if got.Exit != 1 || got.NotRun != "" {
		t.Fatalf("stored check = %+v, want ended with exit 1", got)
	}
	if strings.Contains(got.Stderr, nodeCheckLoadLine) || strings.Contains(got.Stdout, "--- FAIL") {
		t.Errorf("stored tails hold the early lines, want them cut off:\nstdout %q\nstderr %q", got.Stdout, got.Stderr)
	}
	if !strings.HasSuffix(got.Stderr, "tests exceeded 360s budget\n") || !strings.HasSuffix(got.Stdout, "FAIL\n") {
		t.Errorf("stored tails do not end as the check did:\nstdout %q\nstderr %q", got.Stdout, got.Stderr)
	}
	for _, want := range []string{
		nodeCheckLoadLine, "--- FAIL: TestSlow (3.46s)", "tests exceeded 360s budget", "FAIL",
	} {
		if !slices.Contains(got.KeyLines, want) {
			t.Errorf("stored key lines = %q, want them to hold %q", got.KeyLines, want)
		}
	}
	if len(got.KeyLines) != 4 {
		t.Errorf("stored key lines = %q, want only the four key lines", got.KeyLines)
	}
}

// TestGateRedactsItsKeyLines runs a gate whose key lines each hold a listed credential, before more output than its
// tail keeps. Its result's key lines keep each line with the credential replaced, and no secret.
func TestGateRedactsItsKeyLines(t *testing.T) {
	t.Parallel()
	var in strings.Builder
	var want []string
	for _, row := range gateRedactRows() {
		if strings.Contains(row.in, "\n") {
			continue
		}
		in.WriteString("FAIL: " + row.in + "\n")
		want = append(want, "FAIL: "+row.want)
	}
	in.WriteString(gateTailLines(0, 1000))
	result := runGateScript(t, gateRedactHeredoc(in.String(), true), 0)
	if !slices.Equal(result.KeyLines, want) {
		t.Errorf("KeyLines = %q, want %q", result.KeyLines, want)
	}
	gateRedactAssertNoSecret(t, "key lines", strings.Join(result.KeyLines, "\n"))
}
