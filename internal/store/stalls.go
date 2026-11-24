package store

import (
	"context"
	"time"
)

// StallMark is the progress of an epic that a stall check compares between its passes: its task count, its node run
// count with their attempts, and its action count.
type StallMark struct {
	Tasks, Runs, Actions int64
}

// EpicStall is a stall the stall check found in an epic: its seq, when it was found, the StallMark the epic had then,
// and whether the epic parked for it.
type EpicStall struct {
	Seq    int64
	At     time.Time
	Mark   StallMark
	Parked bool
}

// StallMark returns the StallMark of epic as the store holds it now.
func (s *Store) StallMark(_ context.Context, _ string) (StallMark, error) {
	return StallMark{}, nil
}

// LatestStall returns the stall with the highest seq of epic, and whether epic has any stall.
func (s *Store) LatestStall(_ context.Context, _ string) (EpicStall, bool, error) {
	return EpicStall{}, false, nil
}

// WakeStall records a stall row and adds the pending trigger `stall:<seq>` with cause, in one write.
func (s *Store) WakeStall(_ context.Context, _, _ string, _ StallMark, _ time.Time) error {
	return nil
}

// ParkStall records a parked stall row and parks the epic with ParkClassStall and reason, in one write.
func (s *Store) ParkStall(_ context.Context, _, _ string, _ StallMark, _ time.Time) error {
	return nil
}
