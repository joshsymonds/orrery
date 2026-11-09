package hoststatus_test

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

	"github.com/joshsymonds/orrery/internal/hoststatus"
)

const validOutput = `[{"label":"cpu","value":"12%","percent":12.5,"level":"ok"},` +
	`{"label":"uptime","value":"3d","level":"warn"}]`

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func writeFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// stubScript writes a shell script that appends a line to a counter file on each run, prints
// output, then runs tail. It returns the script path and the counter path.
func stubScript(t *testing.T, output, tail string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "output.json")
	counter := filepath.Join(dir, "runs")
	writeFile(t, data, output, 0o600)
	script := filepath.Join(dir, "host-status")
	body := "#!/bin/sh\necho run >> " + counter + "\ncat " + data + "\n" + tail + "\n"
	writeFile(t, script, body, 0o700)
	return script, counter
}

func runCount(t *testing.T, counter string) int {
	t.Helper()
	data, err := os.ReadFile(counter)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func statObjects(count int) string {
	objects := make([]string, count)
	for i := range objects {
		objects[i] = `{"label":"stat","value":"v","level":"ok"}`
	}
	return "[" + strings.Join(objects, ",") + "]"
}

func TestStatsReturnsValidStats(t *testing.T) {
	script, _ := stubScript(t, validOutput, "")
	runner := hoststatus.New(script, time.Now)

	got, err := runner.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	pct := 12.5
	want := []hoststatus.Stat{
		{Label: "cpu", Value: "12%", Percent: &pct, Level: "ok"},
		{Label: "uptime", Value: "3d", Level: "warn"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats = %+v, want %+v", got, want)
	}
}

func TestStatsAcceptsBoundaries(t *testing.T) {
	script, _ := stubScript(t, `[{"label":"low","value":"0","percent":0,"level":"ok"},`+
		`{"label":"high","value":"100","percent":100,"level":"crit"}]`, "")
	got, err := hoststatus.New(script, time.Now).Stats(context.Background())
	if err != nil {
		t.Fatalf("percent 0 and 100: %v", err)
	}
	if len(got) != 2 || got[0].Percent == nil || *got[0].Percent != 0 ||
		got[1].Percent == nil || *got[1].Percent != 100 {
		t.Fatalf("Stats = %+v, want percents 0 and 100", got)
	}

	script, _ = stubScript(t, statObjects(12), "")
	got, err = hoststatus.New(script, time.Now).Stats(context.Background())
	if err != nil {
		t.Fatalf("12 stats: %v", err)
	}
	if len(got) != 12 {
		t.Fatalf("got %d stats, want 12", len(got))
	}
}

func TestStatsRejectsInvalidOutput(t *testing.T) {
	cases := []struct {
		name   string
		output string
	}{
		{"not an array", `{"label":"cpu","value":"1","level":"ok"}`},
		{"null", `null`},
		{"invalid json", `[{`},
		{"trailing data", `[] []`},
		{"13 stats", statObjects(13)},
		{"bad level", `[{"label":"cpu","value":"1","level":"fine"}]`},
		{"percent below zero", `[{"label":"cpu","value":"1","percent":-1,"level":"ok"}]`},
		{"percent above 100", `[{"label":"cpu","value":"1","percent":101,"level":"ok"}]`},
		{"empty label", `[{"label":"","value":"1","level":"ok"}]`},
		{"non-string value", `[{"label":"cpu","value":12,"level":"ok"}]`},
		{"missing value", `[{"label":"cpu","level":"ok"}]`},
		{"unknown field", `[{"label":"cpu","value":"1","level":"ok","extra":true}]`},
		{"over 64 KiB", `[{"label":"cpu","value":"` + strings.Repeat("a", 64<<10) + `","level":"ok"}]`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			script, _ := stubScript(t, testCase.output, "")
			if _, err := hoststatus.New(script, time.Now).Stats(context.Background()); err == nil {
				t.Fatal("Stats succeeded, want an error")
			}
		})
	}
}

func TestStatsReturnsErrorOnNonZeroExit(t *testing.T) {
	script, _ := stubScript(t, validOutput, "exit 3")
	if _, err := hoststatus.New(script, time.Now).Stats(context.Background()); err == nil {
		t.Fatal("Stats succeeded after a non-zero exit, want an error")
	}
}

func TestStatsTimesOut(t *testing.T) {
	script, _ := stubScript(t, validOutput, "sleep 10")
	start := time.Now()
	_, err := hoststatus.New(script, time.Now).Stats(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Stats succeeded past the timeout, want an error")
	}
	if elapsed > 6*time.Second {
		t.Fatalf("Stats took %v after a 2 s timeout", elapsed)
	}
}

func TestStatsCutsLongLabelAndValueByRunes(t *testing.T) {
	label := strings.Repeat("é", 40)
	value := strings.Repeat("v", 70)
	script, _ := stubScript(t, `[{"label":"`+label+`","value":"`+value+`","level":"ok"}]`, "")
	got, err := hoststatus.New(script, time.Now).Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d stats, want 1", len(got))
	}
	if got[0].Label != strings.Repeat("é", 32) {
		t.Fatalf("label = %q, want 32 runes", got[0].Label)
	}
	if got[0].Value != strings.Repeat("v", 64) {
		t.Fatalf("value has %d bytes, want 64", len(got[0].Value))
	}
}

func TestStatsCachesOutcomeForFiveSeconds(t *testing.T) {
	script, counter := stubScript(t, validOutput, "")
	start := time.Unix(1_000_000, 0)
	clk := &fakeClock{now: start}
	runner := hoststatus.New(script, clk.Now)

	first, err := runner.Stats(context.Background())
	if err != nil {
		t.Fatalf("first Stats: %v", err)
	}
	clk.now = start.Add(4 * time.Second)
	second, err := runner.Stats(context.Background())
	if err != nil {
		t.Fatalf("second Stats: %v", err)
	}
	if got := runCount(t, counter); got != 1 {
		t.Fatalf("command ran %d times inside the window, want 1", got)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached Stats = %+v, want %+v", second, first)
	}

	clk.now = start.Add(6 * time.Second)
	if _, err := runner.Stats(context.Background()); err != nil {
		t.Fatalf("Stats after the window: %v", err)
	}
	if got := runCount(t, counter); got != 2 {
		t.Fatalf("command ran %d times after the window, want 2", got)
	}
}

func TestStatsCachesFailure(t *testing.T) {
	script, counter := stubScript(t, "not json", "")
	start := time.Unix(1_000_000, 0)
	clk := &fakeClock{now: start}
	runner := hoststatus.New(script, clk.Now)

	if _, err := runner.Stats(context.Background()); err == nil {
		t.Fatal("first Stats succeeded, want an error")
	}
	clk.now = start.Add(time.Second)
	if _, err := runner.Stats(context.Background()); err == nil {
		t.Fatal("second Stats succeeded, want the cached error")
	}
	if got := runCount(t, counter); got != 1 {
		t.Fatalf("command ran %d times, want 1 with the failure cached", got)
	}
}

func TestStatsDoesNotCacheCancelledCall(t *testing.T) {
	script, _ := stubScript(t, validOutput, "")
	runner := hoststatus.New(script, time.Now)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Stats(canceled); err == nil {
		t.Fatal("Stats with a canceled context succeeded, want an error")
	}
	got, err := runner.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats after a canceled call: %v, want the command to run", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d stats, want 2", len(got))
	}
}
