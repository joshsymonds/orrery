package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cronCLIRefusedPrefix begins the stderr of a refused `orrery cron`. The tests assert this prefix and the id, and not the
// sentence after it.
const cronCLIRefusedPrefix = "orrery: orrery refused this call"

// cronCLIRun runs `orrery --socket socket cron ARGS...` with ORRERY_SESSION set to cronStoreSession, and returns its exit
// code, stdout and stderr.
func cronCLIRun(t *testing.T, socket string, args ...string) (int, string, string) {
	t.Helper()
	getenv := envFrom(map[string]string{protocol.EnvSession: cronStoreSession})
	var stdout, stderr bytes.Buffer
	argv := append([]string{"--socket", socket, "cron"}, args...)
	code := run(argv, nil, &stdout, &stderr, getenv)
	return code, stdout.String(), stderr.String()
}

// cronCLIStoreJob stores a recurring job with id under session, and returns the stored job.
func cronCLIStoreJob(t *testing.T, st *store.Store, session, id string) store.CronJob {
	t.Helper()
	job, _, err := st.CreateCronJob(t.Context(), store.CronJob{
		ID: id, Session: session, ToolUseID: "toolu_" + id, Cron: "*/5 * * * *", Prompt: "check the build",
		Recurring: true, Created: cronStoreNow(),
	})
	if err != nil {
		t.Fatalf("CreateCronJob %s: %v", id, err)
	}
	return job
}

// TestCronOutsideCLIListsSessionJobs stores two jobs of the session and one of session-9, runs `orrery cron list`, and
// checks that it exits 0 and prints the session's two ids and not session-9's.
func TestCronOutsideCLIListsSessionJobs(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	first := cronCLIStoreJob(t, st, cronStoreSession, "cron-00000001")
	second := cronCLIStoreJob(t, st, cronStoreSession, "cron-00000002")
	other := cronCLIStoreJob(t, st, "session-9", "cron-00000003")
	code, stdout, stderr := cronCLIRun(t, socket, "list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr)
	}
	for _, job := range []store.CronJob{first, second} {
		if !strings.Contains(stdout, job.ID) {
			t.Errorf("stdout %q does not list job %s", stdout, job.ID)
		}
	}
	if strings.Contains(stdout, other.ID) {
		t.Errorf("stdout %q lists job %s of another session", stdout, other.ID)
	}
}

// TestCronOutsideCLIDeleteCancels stores two jobs of the session, runs `orrery cron delete` on one of them, and checks
// that it exits 0 and says the job was canceled, and that the next list lacks that id and still lists the other.
func TestCronOutsideCLIDeleteCancels(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	canceled := cronCLIStoreJob(t, st, cronStoreSession, "cron-00000001")
	kept := cronCLIStoreJob(t, st, cronStoreSession, "cron-00000002")
	code, stdout, stderr := cronCLIRun(t, socket, "delete", canceled.ID)
	if code != 0 {
		t.Fatalf("delete exit code = %d, want 0; stderr %q", code, stderr)
	}
	if want := "orrery canceled job " + canceled.ID; !strings.Contains(stdout, want) {
		t.Fatalf("stdout = %q, want it to say %q", stdout, want)
	}
	code, stdout, stderr = cronCLIRun(t, socket, "list")
	if code != 0 {
		t.Fatalf("list exit code = %d, want 0; stderr %q", code, stderr)
	}
	if strings.Contains(stdout, canceled.ID) {
		t.Errorf("list %q still lists canceled job %s", stdout, canceled.ID)
	}
	if !strings.Contains(stdout, kept.ID) {
		t.Errorf("list %q does not list job %s", stdout, kept.ID)
	}
}

// TestCronOutsideCLIDeleteOtherSessionRefused deletes a job that session-9 stores, and checks that the command exits 1
// with a refusal that names the id, and that the job is still stored.
func TestCronOutsideCLIDeleteOtherSessionRefused(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	other := cronCLIStoreJob(t, st, "session-9", "cron-00000001")
	code, _, stderr := cronCLIRun(t, socket, "delete", other.ID)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stderr, cronCLIRefusedPrefix) || !strings.Contains(stderr, other.ID) {
		t.Fatalf("stderr = %q, want a refusal that names job %s", stderr, other.ID)
	}
	if jobs := cronStoreJobs(t, st, "session-9"); len(jobs) != 1 || jobs[0].ID != other.ID {
		t.Fatalf("session-9 has %d jobs after the refused delete, want job %s", len(jobs), other.ID)
	}
}

// TestCronOutsideCLIDeleteUnknownRefused deletes an id that no session stores, and checks that the command exits 1 with
// a refusal that names the id.
func TestCronOutsideCLIDeleteUnknownRefused(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	const id = "cron-00000000"
	code, _, stderr := cronCLIRun(t, socket, "delete", id)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stderr, cronCLIRefusedPrefix) || !strings.Contains(stderr, id) {
		t.Fatalf("stderr = %q, want a refusal that names job %s", stderr, id)
	}
}
