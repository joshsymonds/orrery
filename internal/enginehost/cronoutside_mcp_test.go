package enginehost

import (
	"fmt"
	"strings"
	"testing"
)

// TestCronOutsideMCPCreateAnswerNamesOutsideTools checks that the answer to a CronCreate names the orrery MCP tool
// cron_delete and `orrery cron delete` with the stored job's id, and does not name CronDelete, which Claude Code
// never reaches orrery with.
func TestCronOutsideMCPCreateAnswerNamesOutsideTools(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	answers := NewCronAnswers(CronAnswersConfig{Store: st, Now: cronTestNow})
	got := cronCall(t, answers, "CronCreate", "toolu_01", `{"cron":"*/5 * * * *","prompt":"check the build"}`)
	jobs, err := st.CronJobs(t.Context(), cronTestSession)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("session jobs = %d, err %v; want the one job", len(jobs), err)
	}
	id := jobs[0].ID
	want := fmt.Sprintf(
		"Cancel it with the orrery MCP tool cron_delete and id %s, or with `orrery cron delete %s`.", id, id,
	)
	if !strings.Contains(got, want) {
		t.Errorf("answer %q does not contain %q", got, want)
	}
	if strings.Contains(got, "CronDelete") {
		t.Errorf("answer %q names CronDelete", got)
	}
}

// TestCronOutsideMCPDeleteRefusalNamesOutsideTools checks that the refusal to a CronDelete of an unknown id begins
// with the refusal prefix, names the orrery MCP tool cron_list and `orrery cron list`, and does not name CronList.
func TestCronOutsideMCPDeleteRefusalNamesOutsideTools(t *testing.T) {
	t.Parallel()
	answers := NewCronAnswers(CronAnswersConfig{Store: epicTestStore(t), Now: cronTestNow})
	got := cronCall(t, answers, "CronDelete", "toolu_01", `{"id":"cron-00000000"}`)
	if !strings.HasPrefix(got, cronRefusalPrefix) {
		t.Fatalf("answer %q, want a refusal", got)
	}
	for _, want := range []string{"cron_list", "`orrery cron list`"} {
		if !strings.Contains(got, want) {
			t.Errorf("answer %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "CronList") {
		t.Errorf("answer %q names CronList", got)
	}
}
