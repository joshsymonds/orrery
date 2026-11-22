package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// CronJob is a cron job of a headless session, which orreryd keeps so the session can sleep between fires. ID is the
// job's id and Session the headless session's id. ToolUseID is the id of the tool call that created the job, and a
// session has at most one job per ToolUseID. Recurring is false for a one-shot job. Created is when the job was made,
// and LastFire is when it last fired, zero when it never has. Times are stored to the second.
type CronJob struct {
	ID, Session, ToolUseID, Cron, Prompt string
	Recurring                            bool
	Created, LastFire                    time.Time
}

// CreateCronJob stores job and returns it with true. When its session already has a job with job's ToolUseID, it stores
// nothing and returns the stored job with false, so a tool call that is retried creates no second job.
func (s *Store) CreateCronJob(ctx context.Context, job CronJob) (CronJob, bool, error) {
	var stored CronJob
	created := false
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		inserted, err := queries.InsertCronJob(ctx, sqlcgen.InsertCronJobParams{
			JobID: job.ID, Session: job.Session, ToolUseID: job.ToolUseID, Cron: job.Cron, Prompt: job.Prompt,
			Recurring: boolInt(job.Recurring), CreatedAt: job.Created.Unix(), LastFireAt: unixOrZero(job.LastFire),
		})
		if err != nil {
			return fmt.Errorf("insert cron job %s: %w", job.ID, err)
		}
		if inserted > 0 {
			stored, created = job, true
			return nil
		}
		row, err := queries.GetCronJobByToolUse(ctx, sqlcgen.GetCronJobByToolUseParams{
			Session: job.Session, ToolUseID: job.ToolUseID,
		})
		if err != nil {
			return fmt.Errorf("read cron job of tool use %s of %s: %w", job.ToolUseID, job.Session, err)
		}
		stored = cronJobOf(row)
		return nil
	})
	if err != nil {
		return CronJob{}, false, err
	}
	return stored, created, nil
}

// DeleteCronJob deletes the job id only when it belongs to session, and reports whether it deleted one.
func (s *Store) DeleteCronJob(ctx context.Context, session, id string) (bool, error) {
	var deleted int64
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		n, err := queries.DeleteCronJob(ctx, sqlcgen.DeleteCronJobParams{Session: session, JobID: id})
		if err != nil {
			return fmt.Errorf("delete cron job %s: %w", id, err)
		}
		deleted = n
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

// CronJobs returns the cron jobs of session, oldest first.
func (s *Store) CronJobs(ctx context.Context, session string) ([]CronJob, error) {
	rows, err := sqlcgen.New(s.read).ListSessionCronJobs(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs of %s: %w", session, err)
	}
	return cronJobsOf(rows), nil
}

// AllCronJobs returns the cron jobs of every session, oldest first.
func (s *Store) AllCronJobs(ctx context.Context) ([]CronJob, error) {
	rows, err := sqlcgen.New(s.read).ListCronJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	return cronJobsOf(rows), nil
}

// SetCronFired sets the last fire of job id to at.
func (s *Store) SetCronFired(ctx context.Context, id string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.SetCronJobFired(ctx, sqlcgen.SetCronJobFiredParams{
			LastFireAt: unixOrZero(at), JobID: id,
		}); err != nil {
			return fmt.Errorf("set cron job %s fired: %w", id, err)
		}
		return nil
	})
}

// DeleteSessionCronJobs deletes every cron job of session and returns how many it deleted.
func (s *Store) DeleteSessionCronJobs(ctx context.Context, session string) (int, error) {
	var deleted int64
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		n, err := queries.DeleteSessionCronJobs(ctx, session)
		if err != nil {
			return fmt.Errorf("delete cron jobs of %s: %w", session, err)
		}
		deleted = n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(deleted), nil
}

// unixOrZero is the unix seconds of t, or 0 when t is zero, which a stored time reads back as zero.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// cronJobOf returns the job a stored row holds. A last fire of 0 reads back as the zero time.
func cronJobOf(row sqlcgen.CronJob) CronJob {
	job := CronJob{
		ID: row.JobID, Session: row.Session, ToolUseID: row.ToolUseID, Cron: row.Cron, Prompt: row.Prompt,
		Recurring: row.Recurring != 0, Created: time.Unix(row.CreatedAt, 0),
	}
	if row.LastFireAt != 0 {
		job.LastFire = time.Unix(row.LastFireAt, 0)
	}
	return job
}

// cronJobsOf returns the jobs the stored rows hold, in the rows' order.
func cronJobsOf(rows []sqlcgen.CronJob) []CronJob {
	jobs := make([]CronJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, cronJobOf(row))
	}
	return jobs
}
