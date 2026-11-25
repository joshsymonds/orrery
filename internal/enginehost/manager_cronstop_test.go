package enginehost

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cronStopOther is the session id of a cron job that the Stop of another session must keep.
const cronStopOther = "other-session"

// cronStopJob returns a recurring cron job of session with the job id id.
func cronStopJob(session, id string) store.CronJob {
	return store.CronJob{
		ID: id, Session: session, ToolUseID: "tool-" + id, Cron: "*/5 * * * *", Prompt: "tick", Recurring: true,
		Created: fixedClock(),
	}
}

// createCronStopJob stores job, failing the test unless it is new.
func createCronStopJob(t *testing.T, s *store.Store, job store.CronJob) {
	t.Helper()
	if _, created, err := s.CreateCronJob(t.Context(), job); err != nil || !created {
		t.Fatalf("CreateCronJob %s = %t, %v; want true, nil", job.ID, created, err)
	}
}

// requireCronJobs checks that the cron jobs of session have the job ids want, oldest first.
func requireCronJobs(t *testing.T, s *store.Store, session string, want []string) {
	t.Helper()
	jobs, err := s.CronJobs(t.Context(), session)
	if err != nil {
		t.Fatalf("CronJobs of %s: %v", session, err)
	}
	got := make([]string, 0, len(jobs))
	for _, job := range jobs {
		got = append(got, job.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cron jobs of %s = %q, want %q", session, got, want)
	}
}

func TestCronStopRemovesLiveSessionJobs(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	s := storeOf(t, m)
	createCronStopJob(t, s, cronStopJob(id, "job-own"))
	createCronStopJob(t, s, cronStopJob(cronStopOther, "job-other"))

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of a live session: %v", err)
	}
	requireCronJobs(t, s, id, nil)
	requireCronJobs(t, s, cronStopOther, []string{"job-other"})
	if got := historyOf(t, path, id).State; got != stateStopped {
		t.Fatalf("state after Stop = %q, want %q", got, stateStopped)
	}
}
