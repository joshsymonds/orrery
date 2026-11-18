package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// mustCreateCronJob stores job, failing the test unless it is new.
func mustCreateCronJob(t *testing.T, s *store.Store, job store.CronJob) {
	t.Helper()
	got, created, err := s.CreateCronJob(context.Background(), job)
	if err != nil {
		t.Fatalf("CreateCronJob %s: %v", job.ID, err)
	}
	if !created || !sameCronJob(got, job) {
		t.Fatalf("CreateCronJob %s = %+v, %t; want %+v, true", job.ID, got, created, job)
	}
}

// sameCronJob reports whether a and b hold the same fields, with times compared as instants.
func sameCronJob(a, b store.CronJob) bool {
	return a.ID == b.ID && a.Session == b.Session && a.ToolUseID == b.ToolUseID && a.Cron == b.Cron &&
		a.Prompt == b.Prompt && a.Recurring == b.Recurring && a.Created.Equal(b.Created) && a.LastFire.Equal(b.LastFire)
}

// assertCronJobs fails unless got holds the jobs of want, in order, with every field equal.
func assertCronJobs(t *testing.T, got, want []store.CronJob) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cron jobs = %+v, want %+v", got, want)
	}
	for i := range want {
		if !sameCronJob(got[i], want[i]) {
			t.Errorf("cron job %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCronJobsCreateIsIdempotentByToolUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	first := store.CronJob{
		ID: "job-1", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "check the build",
		Recurring: true, Created: time.Unix(1700000000, 0),
	}

	got, created, err := s.CreateCronJob(ctx, first)
	if err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}
	if !created || !sameCronJob(got, first) {
		t.Fatalf("CreateCronJob = %+v, %t; want %+v, true", got, created, first)
	}

	// A retried tool call names a new job, but its session already holds the job of that tool use.
	retry := store.CronJob{
		ID: "job-2", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "0 9 * * *", Prompt: "something else",
		Created: time.Unix(1700000100, 0),
	}
	got, created, err = s.CreateCronJob(ctx, retry)
	if err != nil {
		t.Fatalf("CreateCronJob retry: %v", err)
	}
	if created || !sameCronJob(got, first) {
		t.Fatalf("CreateCronJob retry = %+v, %t; want the stored %+v, false", got, created, first)
	}

	// The same tool use in another session is a job of that session.
	other := store.CronJob{
		ID: "job-3", Session: "orrery-2", ToolUseID: "toolu_1", Cron: "0 9 * * *", Prompt: "something else",
		Created: time.Unix(1700000200, 0),
	}
	if _, created, err = s.CreateCronJob(ctx, other); err != nil || !created {
		t.Fatalf("CreateCronJob in another session = %t, %v; want true, nil", created, err)
	}

	jobs, err := s.CronJobs(ctx, "orrery-1")
	if err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{first})
}

func TestCronJobsDeleteOnlyInSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	mine := store.CronJob{ID: "job-1", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "a",
		Created: time.Unix(1700000000, 0)}
	theirs := store.CronJob{ID: "job-2", Session: "orrery-2", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "b",
		Created: time.Unix(1700000000, 0)}
	mustCreateCronJob(t, s, mine)
	mustCreateCronJob(t, s, theirs)

	deleted, err := s.DeleteCronJob(ctx, "orrery-2", mine.ID)
	if err != nil {
		t.Fatalf("DeleteCronJob from another session: %v", err)
	}
	if deleted {
		t.Fatalf("DeleteCronJob from orrery-2 deleted job-1 of orrery-1")
	}
	jobs, err := s.CronJobs(ctx, "orrery-1")
	if err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{mine})

	deleted, err = s.DeleteCronJob(ctx, "orrery-1", mine.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteCronJob in its session = %t, %v; want true, nil", deleted, err)
	}
	deleted, err = s.DeleteCronJob(ctx, "orrery-1", mine.ID)
	if err != nil || deleted {
		t.Fatalf("DeleteCronJob of a deleted job = %t, %v; want false, nil", deleted, err)
	}
	if jobs, err = s.CronJobs(ctx, "orrery-1"); err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, nil)
	if jobs, err = s.CronJobs(ctx, "orrery-2"); err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{theirs})
}

func TestCronJobsListBySessionAndAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	// Stored newest first, so the lists must order by creation time, not by insertion.
	a2 := store.CronJob{ID: "job-a2", Session: "orrery-a", ToolUseID: "toolu_2", Cron: "0 9 * * *", Prompt: "two",
		Created: time.Unix(1700000020, 0)}
	a1 := store.CronJob{ID: "job-a1", Session: "orrery-a", ToolUseID: "toolu_1", Cron: "0 8 * * *", Prompt: "one",
		Recurring: true, Created: time.Unix(1700000010, 0)}
	b1 := store.CronJob{ID: "job-b1", Session: "orrery-b", ToolUseID: "toolu_1", Cron: "0 7 * * *", Prompt: "bee",
		Created: time.Unix(1700000005, 0)}
	mustCreateCronJob(t, s, a2)
	mustCreateCronJob(t, s, a1)
	mustCreateCronJob(t, s, b1)

	jobs, err := s.CronJobs(ctx, "orrery-a")
	if err != nil {
		t.Fatalf("CronJobs orrery-a: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{a1, a2})

	jobs, err = s.CronJobs(ctx, "orrery-b")
	if err != nil {
		t.Fatalf("CronJobs orrery-b: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{b1})

	jobs, err = s.CronJobs(ctx, "orrery-none")
	if err != nil {
		t.Fatalf("CronJobs orrery-none: %v", err)
	}
	assertCronJobs(t, jobs, nil)

	all, err := s.AllCronJobs(ctx)
	if err != nil {
		t.Fatalf("AllCronJobs: %v", err)
	}
	assertCronJobs(t, all, []store.CronJob{b1, a1, a2})
}

func TestCronJobsFiredSetsLastFire(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	target := store.CronJob{
		ID: "job-1", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "check",
		Recurring: true, Created: time.Unix(1700000000, 0),
	}
	bystander := store.CronJob{
		ID: "job-2", Session: "orrery-1", ToolUseID: "toolu_2", Cron: "0 9 * * *", Prompt: "other",
		Created: time.Unix(1700000001, 0),
	}
	mustCreateCronJob(t, s, target)
	mustCreateCronJob(t, s, bystander)

	fired := time.Unix(1700000600, 0)
	if err := s.SetCronFired(ctx, target.ID, fired); err != nil {
		t.Fatalf("SetCronFired: %v", err)
	}

	jobs, err := s.CronJobs(ctx, "orrery-1")
	if err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	firedTarget := target
	firedTarget.LastFire = fired
	assertCronJobs(t, jobs, []store.CronJob{firedTarget, bystander})
}

func TestCronJobsDeleteSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	a1 := store.CronJob{ID: "job-a1", Session: "orrery-a", ToolUseID: "toolu_1", Cron: "0 8 * * *", Prompt: "one",
		Created: time.Unix(1700000010, 0)}
	a2 := store.CronJob{ID: "job-a2", Session: "orrery-a", ToolUseID: "toolu_2", Cron: "0 9 * * *", Prompt: "two",
		Created: time.Unix(1700000020, 0)}
	b1 := store.CronJob{ID: "job-b1", Session: "orrery-b", ToolUseID: "toolu_1", Cron: "0 7 * * *", Prompt: "bee",
		Created: time.Unix(1700000005, 0)}
	mustCreateCronJob(t, s, a1)
	mustCreateCronJob(t, s, a2)
	mustCreateCronJob(t, s, b1)

	n, err := s.DeleteSessionCronJobs(ctx, "orrery-a")
	if err != nil {
		t.Fatalf("DeleteSessionCronJobs: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteSessionCronJobs = %d, want 2", n)
	}
	jobs, err := s.CronJobs(ctx, "orrery-a")
	if err != nil {
		t.Fatalf("CronJobs orrery-a: %v", err)
	}
	assertCronJobs(t, jobs, nil)
	jobs, err = s.CronJobs(ctx, "orrery-b")
	if err != nil {
		t.Fatalf("CronJobs orrery-b: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{b1})

	if n, err = s.DeleteSessionCronJobs(ctx, "orrery-a"); err != nil || n != 0 {
		t.Fatalf("DeleteSessionCronJobs of an emptied session = %d, %v; want 0, nil", n, err)
	}
}

func TestCronJobsArchiveHeadlessRemovesJobs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openArchiveStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	mine := store.CronJob{ID: "job-1", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "a",
		Created: time.Unix(1700000000, 0)}
	other := store.CronJob{ID: "job-2", Session: "orrery-2", ToolUseID: "toolu_1", Cron: "*/5 * * * *", Prompt: "b",
		Created: time.Unix(1700000000, 0)}
	mustCreateCronJob(t, s, mine)
	mustCreateCronJob(t, s, other)
	at := time.Unix(1700000500, 0)

	// A session or an epic archive with the same id keeps the session's jobs.
	if err := s.ArchiveRow(ctx, "session", "orrery-1", at); err != nil {
		t.Fatalf("ArchiveRow session: %v", err)
	}
	if err := s.ArchiveRow(ctx, "epic", "orrery-1", at); err != nil {
		t.Fatalf("ArchiveRow epic: %v", err)
	}
	jobs, err := s.CronJobs(ctx, "orrery-1")
	if err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{mine})

	// A headless archive removes that session's jobs and no other session's.
	if err := s.ArchiveRow(ctx, "headless", "orrery-1", at); err != nil {
		t.Fatalf("ArchiveRow headless: %v", err)
	}
	if jobs, err = s.CronJobs(ctx, "orrery-1"); err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, nil)
	if jobs, err = s.CronJobs(ctx, "orrery-2"); err != nil {
		t.Fatalf("CronJobs: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{other})
}

func TestCronJobsSurviveReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	job := store.CronJob{
		ID: "job-1", Session: "orrery-1", ToolUseID: "toolu_1", Cron: "0 9 * * 1-5", Prompt: "check the deploy",
		Recurring: true, Created: time.Unix(1700000000, 0), LastFire: time.Unix(1700000600, 0),
	}

	s, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreateCronJob(t, s, store.CronJob{
		ID: job.ID, Session: job.Session, ToolUseID: job.ToolUseID, Cron: job.Cron, Prompt: job.Prompt,
		Recurring: job.Recurring, Created: job.Created,
	})
	if err = s.SetCronFired(ctx, job.ID, job.LastFire); err != nil {
		t.Fatalf("SetCronFired: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close after reopen: %v", err)
		}
	})
	jobs, err := reopened.CronJobs(ctx, job.Session)
	if err != nil {
		t.Fatalf("CronJobs after reopen: %v", err)
	}
	assertCronJobs(t, jobs, []store.CronJob{job})
}
