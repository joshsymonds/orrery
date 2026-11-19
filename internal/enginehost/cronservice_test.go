package enginehost

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// cronZoneOffset is the offset, in seconds, of the zone the cron tests read times in: UTC+9, never UTC.
const cronZoneOffset = 9 * 3600

// cronTestSession is the session the cron tests call from.
const cronTestSession = "session-1"

// cronRefusalPrefix begins every refusal the cron answers give.
const cronRefusalPrefix = "orrery refused this call, so nothing was scheduled or canceled: "

// cronTestZone returns the zone the cron tests read times in: a fixed zone that is not UTC.
func cronTestZone() *time.Location {
	return time.FixedZone("JST", cronZoneOffset)
}

// cronTestNow is the fixed time the cron tests read as now: 2026-11-18 10:02:30 in cronTestZone.
func cronTestNow() time.Time {
	return time.Date(2026, time.November, 18, 10, 2, 30, 0, cronTestZone())
}

// cronCall calls answers as cronTestSession and returns the answer. A call that returns an error fails the test.
func cronCall(t *testing.T, answers *CronAnswers, toolName, toolUseID, toolInput string) string {
	t.Helper()
	got, err := answers.CronCall(t.Context(), cronTestSession, toolName, toolUseID, toolInput)
	if err != nil {
		t.Fatalf("CronCall %s: %v", toolName, err)
	}
	return got
}

// startCronsServer serves crons on a sessions socket in a temporary directory and returns the socket's path. The server
// stops at cleanup.
func startCronsServer(t *testing.T, crons CronCommands) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, nil, nil, nil, nil, nil, crons) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path
}

// TestCronTakeoverNextFire checks the due time and final fire of each kind of job.
func TestCronTakeoverNextFire(t *testing.T) {
	t.Parallel()
	zone := cronTestZone()
	created := cronTestNow()
	fireAt := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.November, day, hour, minute, 0, 0, zone)
	}
	end := created.Add(cronLifetime)
	tests := []struct {
		name      string
		job       store.CronJob
		wantDue   time.Time
		wantFinal bool
	}{
		{
			name:      "one-shot",
			job:       store.CronJob{Cron: "0 12 * * *", Created: created},
			wantDue:   fireAt(18, 12, 0),
			wantFinal: true,
		},
		{
			name:      "recurring before its end",
			job:       store.CronJob{Cron: "*/5 * * * *", Recurring: true, Created: created},
			wantDue:   fireAt(18, 10, 5),
			wantFinal: false,
		},
		{
			name: "recurring after a fire",
			job: store.CronJob{
				Cron: "*/5 * * * *", Recurring: true, Created: created, LastFire: fireAt(18, 10, 5),
			},
			wantDue:   fireAt(18, 10, 10),
			wantFinal: false,
		},
		{
			name:      "recurring whose next match is past its end",
			job:       store.CronJob{Cron: "0 0 1 * *", Recurring: true, Created: created},
			wantDue:   end,
			wantFinal: true,
		},
		{
			name:      "fired one-shot",
			job:       store.CronJob{Cron: "0 12 * * *", Created: created, LastFire: fireAt(18, 12, 0)},
			wantDue:   time.Time{},
			wantFinal: true,
		},
		{
			name:      "spent recurring",
			job:       store.CronJob{Cron: "*/5 * * * *", Recurring: true, Created: created, LastFire: end},
			wantDue:   time.Time{},
			wantFinal: true,
		},
		{
			name:      "unparsable cron",
			job:       store.CronJob{Cron: "every minute", Recurring: true, Created: created},
			wantDue:   time.Time{},
			wantFinal: true,
		},
	}
	for _, tc := range tests {
		due, final := cronNextFire(tc.job, zone)
		if !due.Equal(tc.wantDue) || final != tc.wantFinal {
			t.Errorf("%s: cronNextFire = %v, %t; want %v, %t", tc.name, due, final, tc.wantDue, tc.wantFinal)
		}
	}
}

// TestCronTakeoverServiceRefusals checks that each call the answers refuse gets a refusal, and that no job is stored.
func TestCronTakeoverServiceRefusals(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	answers := NewCronAnswers(CronAnswersConfig{Store: st, Now: cronTestNow})
	// February has no 30th day, so the cron never fires, and the job is refused even though it is recurring by default.
	tests := []struct {
		name, toolName, toolUseID, toolInput string
	}{
		{"malformed input", "CronCreate", "toolu_01", `{"cron":`},
		{"empty prompt", "CronCreate", "toolu_01", `{"cron":"*/5 * * * *","prompt":""}`},
		{"no tool_use_id", "CronCreate", "", `{"cron":"*/5 * * * *","prompt":"check"}`},
		{"unknown tool", "ScheduleWakeup", "toolu_01", `{"cron":"*/5 * * * *","prompt":"check"}`},
		{"cron that never fires", "CronCreate", "toolu_01", `{"cron":"0 0 30 2 *","prompt":"check"}`},
	}
	for _, tc := range tests {
		got, err := answers.CronCall(t.Context(), cronTestSession, tc.toolName, tc.toolUseID, tc.toolInput)
		if err != nil {
			t.Fatalf("%s: CronCall: %v", tc.name, err)
		}
		if !strings.HasPrefix(got, cronRefusalPrefix) {
			t.Errorf("%s: answer %q, want a refusal", tc.name, got)
		}
	}
	jobs, err := st.AllCronJobs(t.Context())
	if err != nil {
		t.Fatalf("AllCronJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("refused calls stored %d jobs, want none", len(jobs))
	}
}

// TestCronTakeoverServiceChanged checks that Changed runs once for a new job and once for a delete that removes one, and
// not for a repeated tool_use_id or a delete of an unknown id.
func TestCronTakeoverServiceChanged(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	changes := 0
	answers := NewCronAnswers(CronAnswersConfig{Store: st, Now: cronTestNow, Changed: func() { changes++ }})
	wantChanges := func(step string, want int) {
		t.Helper()
		if changes != want {
			t.Fatalf("after %s: Changed ran %d times, want %d", step, changes, want)
		}
	}
	const create = `{"cron":"*/5 * * * *","prompt":"check the build"}`
	cronCall(t, answers, "CronCreate", "toolu_01", create)
	wantChanges("a new create", 1)
	cronCall(t, answers, "CronCreate", "toolu_01", create)
	wantChanges("a repeated tool_use_id", 1)
	cronCall(t, answers, "CronDelete", "toolu_02", `{"id":"cron-00000000"}`)
	wantChanges("a delete of an unknown id", 1)
	jobs, err := st.CronJobs(t.Context(), cronTestSession)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("session jobs = %d, err %v; want the one job", len(jobs), err)
	}
	cronCall(t, answers, "CronDelete", "toolu_03", fmt.Sprintf(`{"id":%q}`, jobs[0].ID))
	wantChanges("a delete that removed the job", 2)
}

// TestCronTakeoverServiceNeedsSession checks, through Serve on a session socket, that a call with no Orrery-Session header
// is refused as InvalidArgument, and that a call to a Crons service with no cron answers is refused as Unavailable.
func TestCronTakeoverServiceNeedsSession(t *testing.T) {
	t.Parallel()
	answers := NewCronAnswers(CronAnswersConfig{Store: epicTestStore(t), Now: cronTestNow})
	tests := []struct {
		name  string
		crons CronCommands
		want  connect.Code
	}{
		{name: "no session header", crons: answers, want: connect.CodeInvalidArgument},
		{name: "no cron answers", crons: nil, want: connect.CodeUnavailable},
	}
	for _, tc := range tests {
		path := startCronsServer(t, tc.crons)
		client := enginev1connect.NewCronsClient(unixHTTPClient(t, path), "http://crons.invalid")
		_, err := client.CronCall(t.Context(), connect.NewRequest(&v1.CronCallRequest{ToolName: "CronList"}))
		if got := connect.CodeOf(err); got != tc.want {
			t.Errorf("%s: code %v (err %v), want %v", tc.name, got, err, tc.want)
		}
	}
}
