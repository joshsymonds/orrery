package enginehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cron"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cronLifetime is how long a recurring job lives: it fires at each match until its Created plus this, then fires a final
// time and ends.
const cronLifetime = 7 * 24 * time.Hour

// cronTimeLayout is how the cron answers show a time, such as Wed 2026-11-18 10:05 JST.
const cronTimeLayout = "Mon 2006-01-02 15:04 MST"

// The tools the cron answers name.
const (
	cronCreateTool = "CronCreate"
	cronDeleteTool = "CronDelete"
	cronListTool   = "CronList"
)

// cronRefusedPrefix begins every refusal. It tells the agent that the call changed no job.
const cronRefusedPrefix = "orrery refused this call, so nothing was scheduled or canceled: "

// notObjectReason is the reason a call is refused when its tool input is not a JSON object with the fields its tool takes.
const notObjectReason = "the tool input is not a JSON object with the fields the tool takes."

// cronIDBytes is how many random bytes a job id takes. Its id shows them as eight lowercase hex digits.
const cronIDBytes = 4

// CronAnswerStore is the set of cron job operations the cron answers keep their jobs in. *store.Store satisfies it.
type CronAnswerStore interface {
	CreateCronJob(ctx context.Context, job store.CronJob) (store.CronJob, bool, error)
	DeleteCronJob(ctx context.Context, session, id string) (bool, error)
	CronJobs(ctx context.Context, session string) ([]store.CronJob, error)
}

// CronAnswersConfig configures NewCronAnswers. A nil Now reads the time with time.Now. Changed runs once after each call
// that stores a new job or deletes one, and a nil Changed runs nothing.
type CronAnswersConfig struct {
	Store   CronAnswerStore
	Now     func() time.Time
	Changed func()
}

// CronAnswers answers the cron tools of a headless session from the jobs in its store. It is the record of the jobs.
type CronAnswers struct {
	jobs    CronAnswerStore
	now     func() time.Time
	changed func()
}

// CronCommands is the set of cron operations the Crons service answers with. *CronAnswers satisfies it.
type CronCommands interface {
	CronCall(ctx context.Context, session, toolName, toolUseID, toolInput string) (string, error)
}

// NewCronAnswers returns the CronAnswers that cfg describes.
func NewCronAnswers(cfg CronAnswersConfig) *CronAnswers {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	changed := cfg.Changed
	if changed == nil {
		changed = func() {}
	}
	return &CronAnswers{jobs: cfg.Store, now: now, changed: changed}
}

// CronCall answers one cron tool call of session and returns the text the agent reads. It returns an error only when the
// store fails. A call that orrery refuses gets its refusal as the text, with a nil error.
func (c *CronAnswers) CronCall(ctx context.Context, session, toolName, toolUseID, toolInput string) (string, error) {
	switch toolName {
	case cronCreateTool:
		return c.createJob(ctx, session, toolUseID, toolInput)
	case cronDeleteTool:
		return c.cancelJob(ctx, session, toolInput)
	case cronListTool:
		return c.listJobs(ctx, session)
	default:
		return cronRefusal(toolName + " is not a cron tool orrery answers."), nil
	}
}

// createJob answers a CronCreate call. It stores the job the input describes, or refuses the input. A tool_use_id that
// is already stored answers for the stored job and changes nothing.
func (c *CronAnswers) createJob(ctx context.Context, session, toolUseID, toolInput string) (string, error) {
	var in struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring *bool  `json:"recurring"`
	}
	if !decodeToolInput(toolInput, &in) {
		return cronRefusal(notObjectReason), nil
	}
	if in.Prompt == "" {
		return cronRefusal("the prompt is empty."), nil
	}
	if toolUseID == "" {
		return cronRefusal("the call has no tool_use_id."), nil
	}
	sched, err := cron.Parse(in.Cron)
	if err != nil {
		return cronRefusal(fmt.Sprintf("cron %q is not valid: %v.", in.Cron, err)), nil
	}
	now := c.now()
	id, err := newCronJobID()
	if err != nil {
		return "", err
	}
	job := store.CronJob{
		ID: id, Session: session, ToolUseID: toolUseID, Cron: in.Cron, Prompt: in.Prompt,
		Recurring: in.Recurring == nil || *in.Recurring, Created: now.Truncate(time.Second),
	}
	// Checked on the cron itself: a recurring job with no match ends at its end rather than having a zero due, so
	// cronNextFire cannot tell that the cron never fires.
	if sched.Next(job.Created).IsZero() {
		return cronRefusal(fmt.Sprintf("cron %q never fires.", in.Cron)), nil
	}
	stored, created, err := c.jobs.CreateCronJob(ctx, job)
	if err != nil {
		return "", fmt.Errorf("create cron job: %w", err)
	}
	if created {
		c.changed()
	}
	return scheduledAnswer(stored, now.Location()), nil
}

// cancelJob answers a CronDelete call. It deletes the job that the input names from session, or refuses the call.
func (c *CronAnswers) cancelJob(ctx context.Context, session, toolInput string) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	if !decodeToolInput(toolInput, &in) {
		return cronRefusal(notObjectReason), nil
	}
	if in.ID == "" {
		return cronRefusal("the id is empty."), nil
	}
	deleted, err := c.jobs.DeleteCronJob(ctx, session, in.ID)
	if err != nil {
		return "", fmt.Errorf("delete cron job: %w", err)
	}
	if !deleted {
		return cronRefusal(fmt.Sprintf(
			"orrery has no job %s in this session, so nothing was canceled. "+
				"The orrery MCP tool cron_list, or `orrery cron list`, lists this session's jobs.",
			in.ID,
		)), nil
	}
	c.changed()
	return fmt.Sprintf("orrery canceled job %s; it will not fire again.", in.ID), nil
}

// listJobs answers a CronList call with the jobs of session, oldest first.
func (c *CronAnswers) listJobs(ctx context.Context, session string) (string, error) {
	jobs, err := c.jobs.CronJobs(ctx, session)
	if err != nil {
		return "", fmt.Errorf("list cron jobs: %w", err)
	}
	if len(jobs) == 0 {
		return "This session has no orrery cron jobs.", nil
	}
	loc := c.now().Location()
	lines := make([]string, 0, len(jobs)+1)
	lines = append(lines, "This session's orrery cron jobs:")
	for _, job := range jobs {
		due, _ := cronNextFire(job, loc)
		kind := "one-shot"
		if job.Recurring {
			kind = "recurring until " + cronTime(cronEnd(job, loc), loc)
		}
		lines = append(lines, fmt.Sprintf(
			"%s  cron %s  %s  next run %s  prompt %q", job.ID, job.Cron, kind, cronTime(due, loc), job.Prompt,
		))
	}
	return strings.Join(lines, "\n"), nil
}

// scheduledAnswer is the answer to a create whose job is stored, with the job's id, cron, prompt, next run and end.
func scheduledAnswer(job store.CronJob, loc *time.Location) string {
	due, _ := cronNextFire(job, loc)
	fires := "It fires once and then ends."
	if job.Recurring {
		fires = fmt.Sprintf(
			"It fires at each match until %s, when it fires a final time and ends.", cronTime(cronEnd(job, loc), loc),
		)
	}
	return fmt.Sprintf(
		"orrery scheduled this job as %s. Cron %s, prompt %q, next run %s. %s orreryd fires it into this session, "+
			"resuming the session if it is asleep. The job is scheduled; this is not an error, so do not call CronCreate "+
			"again for it. Cancel it with the orrery MCP tool cron_delete and id %s, or with `orrery cron delete %s`.",
		job.ID, job.Cron, job.Prompt, cronTime(due, loc), fires, job.ID, job.ID,
	)
}

// cronNextFire returns when job next fires, in loc, and whether that fire is the job's last. A job with no fire left
// returns the zero time, and its fire is final. A job whose cron does not parse has no fire left.
//
// A one-shot job fires at the first match after its Created, and ends with that fire. A recurring job fires at each
// match until its end, Created plus cronLifetime. When its next match is at or after the end, or there is none, its due
// is the end itself, and that fire is its final one.
func cronNextFire(job store.CronJob, loc *time.Location) (due time.Time, final bool) {
	sched, err := cron.Parse(job.Cron)
	if err != nil {
		return time.Time{}, true
	}
	fired := !job.LastFire.IsZero()
	base := job.Created.In(loc)
	if fired {
		base = job.LastFire.In(loc)
	}
	if !job.Recurring {
		if fired {
			return time.Time{}, true
		}
		return sched.Next(base), true
	}
	end := cronEnd(job, loc)
	if fired && !job.LastFire.Before(end) {
		return time.Time{}, true
	}
	next := sched.Next(base)
	if next.IsZero() || !next.Before(end) {
		return end, true
	}
	return next, false
}

// cronEnd returns when the recurring job ends, in loc: cronLifetime after its Created.
func cronEnd(job store.CronJob, loc *time.Location) time.Time {
	return job.Created.Add(cronLifetime).In(loc)
}

// cronTime returns t, in loc, in the form the cron answers show a time.
func cronTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(cronTimeLayout)
}

// cronRefusal returns the answer that refuses a call for reason.
func cronRefusal(reason string) string {
	return cronRefusedPrefix + reason
}

// newCronJobID returns a new job id: "cron-" and cronIDBytes random bytes as lowercase hex.
func newCronJobID() (string, error) {
	var raw [cronIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read job id: %w", err)
	}
	return "cron-" + hex.EncodeToString(raw[:]), nil
}

// decodeToolInput decodes the tool input text into v. It reports whether text is a JSON object whose fields v takes, and
// so false for a JSON null.
func decodeToolInput(text string, v any) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
		return false
	}
	return json.Unmarshal([]byte(text), v) == nil
}

// cronService implements the Crons RPCs over crons. A nil crons refuses every RPC as unavailable.
type cronService struct {
	crons CronCommands
}

// CronCall answers one cron tool call. The session is the one in the Orrery-Session header, never one the tool input
// names.
func (s cronService) CronCall(
	ctx context.Context,
	req *connect.Request[v1.CronCallRequest],
) (*connect.Response[v1.CronCallResponse], error) {
	if s.crons == nil {
		return nil, cronsUnavailable()
	}
	session := req.Header().Get(protocol.HeaderSession)
	if session == "" {
		return nil, errors.Join(connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("the %s header names no session", protocol.HeaderSession),
		))
	}
	reason, err := s.crons.CronCall(ctx, session, req.Msg.GetToolName(), req.Msg.GetToolUseId(), req.Msg.GetToolInput())
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeInternal, err))
	}
	return connect.NewResponse(&v1.CronCallResponse{Reason: reason}), nil
}

// cronsUnavailable returns the refusal every Crons RPC gets when there are no cron answers to serve.
func cronsUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}
