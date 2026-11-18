package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// CronFireStore is the set of cron job operations the cron scheduler fires jobs from. *store.Store satisfies it.
type CronFireStore interface {
	AllCronJobs(ctx context.Context) ([]store.CronJob, error)
	SetCronFired(ctx context.Context, id string, at time.Time) error
	DeleteCronJob(ctx context.Context, session, id string) (bool, error)
}

// CronTimer is the timer the cron scheduler waits on for the next due fire. Its C receives when it fires, and Stop
// stops it, as a time.Timer's do.
type CronTimer interface {
	C() <-chan time.Time
	Stop() bool
}

// CronSchedulerConfig configures NewCronScheduler. Store holds the jobs. Deliver sends a due job's prompt to its
// session, and *Manager's DeliverCron fits it. Now is the clock, and NewTimer arms a timer for a duration. Logger logs
// each fire that failed and each store error. A nil Now is time.Now, a nil NewTimer wraps time.NewTimer, and a nil
// Logger discards.
type CronSchedulerConfig struct {
	Store    CronFireStore
	Deliver  func(context.Context, DeliverRequest) error
	Now      func() time.Time
	NewTimer func(time.Duration) CronTimer
	Logger   *slog.Logger
}

// CronScheduler fires the cron jobs of every session when each is due, from one timer for the next due fire across
// all of them. It keeps no record of its own: each pass reads every job from the store, so the store is the record.
// There is no ticker and no poll. A pass runs once when Run starts, once on each kick (Changed and Wake), and once when
// the timer fires. Each pass stops the armed timer and arms a new one for the earliest due still ahead of the clock,
// and arms none when nothing is ahead.
//
// A job with no fire left is deleted. A job that is due is fired: its prompt goes to its session through Deliver. A
// failed delivery leaves the job as it is, still due, so the next pass tries it again. A delivered fire of a one-shot
// job, or the last fire of a recurring job, deletes the job. Any other delivered fire is recorded in the store as the
// job's last fire, so a job never has more than one fire waiting: the matches that pass while a fire waits are not
// queued.
type CronScheduler struct {
	store    CronFireStore
	deliver  func(context.Context, DeliverRequest) error
	now      func() time.Time
	newTimer func(time.Duration) CronTimer
	logger   *slog.Logger
	// kick holds one pending kick, so a kick that comes while one is pending is absorbed.
	kick chan struct{}
	// timer is the armed timer, or nil when none is armed. Only the passes touch it, and they run on one goroutine.
	timer CronTimer
}

// NewCronScheduler returns the CronScheduler that cfg describes.
func NewCronScheduler(cfg CronSchedulerConfig) *CronScheduler {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newTimer := cfg.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration) CronTimer { return stdCronTimer{time.NewTimer(d)} }
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &CronScheduler{
		store: cfg.Store, deliver: cfg.Deliver, now: now, newTimer: newTimer, logger: logger,
		kick: make(chan struct{}, 1),
	}
}

// stdCronTimer is the CronTimer of a time.Timer.
type stdCronTimer struct{ *time.Timer }

// C returns the channel the timer fires on.
func (t stdCronTimer) C() <-chan time.Time { return t.Timer.C }

// Run runs one pass at once, then one pass on each kick and each fire of its timer, until ctx ends. It stops its timer
// when it returns. A pass under way when ctx ends finishes first.
func (s *CronScheduler) Run(ctx context.Context) {
	defer s.stopTimer()
	s.pass(ctx)
	for {
		var fire <-chan time.Time
		if s.timer != nil {
			fire = s.timer.C()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-fire:
		}
		s.pass(ctx)
	}
}

// Changed kicks Run to run a pass, without blocking, and a kick already pending absorbs this one. The cron answers call
// it after they store a job or delete one.
func (s *CronScheduler) Changed() { s.kickRun() }

// Wake kicks Run to run a pass, without blocking, as Changed does. It ignores the session, so the pass tries every due
// job, and a job whose session was busy is tried again.
func (s *CronScheduler) Wake(_ string) { s.kickRun() }

// kickRun puts one kick in the kick channel unless one is pending already.
func (s *CronScheduler) kickRun() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// pass makes one pass over every job at the clock's now. A job with no fire left is deleted. A job due after now counts
// toward the earliest due still ahead. A job due at or before now is fired (see fire), and the job it leaves due ahead,
// if any, counts too. Then the armed timer is stopped and one is armed for the earliest due, if any is ahead. A failed
// list leaves the timer as it is: the jobs are not read, so the next kick or timer fire runs the pass again.
func (s *CronScheduler) pass(ctx context.Context) {
	now := s.now()
	loc := now.Location()
	jobs, err := s.store.AllCronJobs(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "list cron jobs", "err", err)
		return
	}
	var earliest time.Time
	for _, job := range jobs {
		due, final := cronNextFire(job, loc)
		switch {
		case due.IsZero():
			s.remove(ctx, job)
		case due.After(now):
			earliest = soonerOf(earliest, due)
		default:
			earliest = soonerOf(earliest, s.fire(ctx, job, due, final, now))
		}
	}
	s.stopTimer()
	if !earliest.IsZero() {
		s.timer = s.newTimer(earliest.Sub(now))
	}
}

// fire delivers the fire of job due at due to its session, at now, which is at or after due. It returns the job's next
// due, which is ahead of now, or the zero time when the job has no fire left. A delivery that fails leaves job as it
// is, and returns the zero time, so the next pass tries the same fire again.
//
// A delivery that the session cannot take now is logged at debug level, and any other failure at warn. A delivered fire
// of a one-shot job, or the last fire of a recurring job, deletes the job. Any other delivered fire records now as the
// job's last fire, and deletes the job when no match is left after it.
func (s *CronScheduler) fire(
	ctx context.Context, job store.CronJob, due time.Time, final bool, now time.Time,
) time.Time {
	req := DeliverRequest{
		ID: job.Session, Text: job.Prompt, PromptID: "cron:" + job.ID + ":" + strconv.FormatInt(due.Unix(), 10),
	}
	if err := s.deliver(ctx, req); err != nil {
		if cronWaiting(err) {
			s.logger.DebugContext(ctx, "cron fire waits", "job", job.ID, "session", job.Session, "err", err)
		} else {
			s.logger.WarnContext(ctx, "cron fire failed", "job", job.ID, "session", job.Session, "err", err)
		}
		return time.Time{}
	}
	if !job.Recurring || final {
		s.remove(ctx, job)
		return time.Time{}
	}
	job.LastFire = now.Truncate(time.Second)
	if err := s.store.SetCronFired(ctx, job.ID, job.LastFire); err != nil {
		s.logger.WarnContext(ctx, "set cron job fired", "job", job.ID, "err", err)
	}
	next, _ := cronNextFire(job, now.Location())
	if next.IsZero() {
		s.remove(ctx, job)
	}
	return next
}

// remove deletes job from the store, and logs a store error.
func (s *CronScheduler) remove(ctx context.Context, job store.CronJob) {
	if _, err := s.store.DeleteCronJob(ctx, job.Session, job.ID); err != nil {
		s.logger.WarnContext(ctx, "delete cron job", "job", job.ID, "err", err)
	}
}

// stopTimer stops the armed timer, if one is armed, and forgets it.
func (s *CronScheduler) stopTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// cronWaiting reports whether err is a delivery error that means the session cannot take the fire now.
func cronWaiting(err error) bool {
	return errors.Is(err, ErrSessionBusy) || errors.Is(err, ErrCallerQuiet) || errors.Is(err, ErrCallerFailed) ||
		errors.Is(err, ErrCallerStopped) || errors.Is(err, ErrCapReached)
}

// soonerOf returns the earlier of held and next. The zero time means no time, so a zero argument yields the other.
func soonerOf(held, next time.Time) time.Time {
	switch {
	case next.IsZero():
		return held
	case held.IsZero(), next.Before(held):
		return next
	}
	return held
}
