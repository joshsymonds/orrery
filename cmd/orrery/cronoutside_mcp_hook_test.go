package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestCronOutsideMCPHookCreateStillStored runs `orrery hook cron` on a CronCreate call and checks that the job is
// stored under ORRERY_SESSION, and that the deny reason names the orrery MCP tool cron_delete and `orrery cron delete`
// with the job's id, and does not name CronDelete.
func TestCronOutsideMCPHookCreateStillStored(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	reason := runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	jobs := cronStoreJobs(t, st, cronStoreSession)
	if len(jobs) != 1 {
		t.Fatalf("session has %d jobs, want 1", len(jobs))
	}
	id := jobs[0].ID
	if !strings.HasPrefix(reason, "orrery scheduled this job as "+id+".") {
		t.Fatalf("reason = %q, want it to start with the scheduled job %s", reason, id)
	}
	want := fmt.Sprintf(
		"Cancel it with the orrery MCP tool cron_delete and id %s, or with `orrery cron delete %s`.", id, id,
	)
	if !strings.Contains(reason, want) {
		t.Errorf("reason = %q, want it to contain %q", reason, want)
	}
	if strings.Contains(reason, "CronDelete") {
		t.Errorf("reason = %q names CronDelete", reason)
	}
}
