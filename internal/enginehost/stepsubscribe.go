package enginehost

import (
	"context"
	"fmt"
	"sync"

	"github.com/joshsymonds/orrery/internal/store"
)

// StepSubscription reports the rows of the steps it was subscribed to when each of them ends or is held by a usage limit.
// Each report is queued once: an end, or the hold a limit began, is queued either by Subscribe when the row is already
// stored, or by the publish that follows the write that stores it, and never by both. Queued rows wait until Take returns
// them, and Ready is signaled whenever rows are pending. Close unregisters the subscription, after which nothing is queued
// for it.
type StepSubscription struct {
	steps *Steps
	ids   map[string]bool
	ready chan struct{}

	mu       sync.Mutex
	pending  []store.StepRow
	reported map[stepReport]bool
}

// stepReport identifies one report of a step row: the end of the step with id, or the usage-limit hold of that step that
// began at since, the time its stage started. An id's end is reported once, and each hold once.
type stepReport struct {
	id    string
	end   bool
	since int64
}

// NewStepSubscription returns an unregistered subscription to the ends and holds of the steps with ids. No row reaches
// it except through Offer, and its Close does nothing, since it has no Steps. It lets a fake feed a subscription.
func NewStepSubscription(ids []string) *StepSubscription {
	sub := &StepSubscription{
		ids: make(map[string]bool, len(ids)), ready: make(chan struct{}, 1), reported: map[stepReport]bool{},
	}
	for _, id := range ids {
		sub.ids[id] = true
	}
	return sub
}

// Subscribe returns a subscription to the ends and holds of the steps with ids. It registers the subscription before it
// reads each id's row, so an end or hold stored at any time is reported: one stored before the read is queued by it, and
// one stored after the registration is published to the subscription by the write's publish. An id with no row returns an
// error matching store.ErrNoStep, and nothing is left registered. The caller Closes the subscription.
func (s *Steps) Subscribe(ctx context.Context, ids []string) (*StepSubscription, error) {
	sub := NewStepSubscription(ids)
	sub.steps = s
	s.mu.Lock()
	s.subs[sub] = true
	s.mu.Unlock()
	for _, id := range ids {
		row, err := s.Status(ctx, id)
		if err != nil {
			sub.Close()
			return nil, fmt.Errorf("subscribe to steps: %w", err)
		}
		sub.Offer(row)
	}
	return sub, nil
}

// Ready returns the channel signaled whenever rows are pending. It holds one signal, so a received signal stands for every
// row pending at the receive, and Take returns them all.
func (sub *StepSubscription) Ready() <-chan struct{} {
	return sub.ready
}

// Take returns the rows pending in the order they were published, and clears them. It returns nil when none is pending.
func (sub *StepSubscription) Take() []store.StepRow {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	rows := sub.pending
	sub.pending = nil
	return rows
}

// Close unregisters the subscription, so no row is queued for it once Close returns. Rows already pending stay for Take.
// Close may be called more than once, and on a subscription NewStepSubscription returned it does nothing.
func (sub *StepSubscription) Close() {
	if sub.steps == nil {
		return
	}
	sub.steps.mu.Lock()
	defer sub.steps.mu.Unlock()
	delete(sub.steps.subs, sub)
}

// Offer queues row for the subscription when it is an end or a hold of an id the subscription holds, and that report has
// not been queued yet. It signals Ready when it queues a row. A row that is neither an end nor a hold is not queued.
func (sub *StepSubscription) Offer(row store.StepRow) {
	if !sub.ids[row.ID] {
		return
	}
	report, ok := stepReportOf(row)
	if !ok {
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.reported[report] {
		return
	}
	sub.reported[report] = true
	sub.pending = append(sub.pending, row)
	select {
	case sub.ready <- struct{}{}:
	default:
	}
}

// stepReportOf returns the report row is: its end when it has one, else its hold when it is limited. A row with neither has
// no report, and ok is false.
func stepReportOf(row store.StepRow) (report stepReport, ok bool) {
	switch {
	case row.End != "":
		return stepReport{id: row.ID, end: true}, true
	case row.LoopLimited:
		return stepReport{id: row.ID, since: row.LoopStageFrom.Unix()}, true
	}
	return stepReport{}, false
}

// publish reports the row of the step with id, as the store now holds it, to every subscription, then calls Stored and,
// when the row has an epic, EpicWake with that epic. It is called once a write that ends or holds the step is stored. The
// row is read under a context that has not been canceled, so a stored write is reported whatever ctx has done. A read
// error is logged, and the row is then not reported by this publish; a subscription opened later still reads the stored
// row and reports it.
func (s *Steps) publish(ctx context.Context, id string) {
	row, err := s.store.Step(context.WithoutCancel(ctx), id)
	if err != nil {
		s.logger.ErrorContext(ctx, "read published step", "step", id, "err", err)
		return
	}
	s.mu.Lock()
	for sub := range s.subs {
		sub.Offer(row)
	}
	s.mu.Unlock()
	if s.stored != nil {
		s.stored()
	}
	if s.epicWake != nil && row.Epic != "" {
		s.epicWake(row.Epic)
	}
}
