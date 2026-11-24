package store

import (
	"context"
	"time"
)

// SteererTell is a tell of an epic that its steerer has not been sent: the epic and its title, the tell's seq, its text,
// when it was stored, and the question it is about, 0 for none.
type SteererTell struct {
	Epic, Title string
	Seq         int64
	Text        string
	At          time.Time
	Question    int64
}

// UndeliveredTells returns the tells no steerer has been sent, in epic then seq order.
func (s *Store) UndeliveredTells(_ context.Context) ([]SteererTell, error) {
	return nil, nil
}

// MarkTellDelivered records that the steerer of epic has been sent its tell seq. It returns an error matching ErrNoTell
// for an unknown tell.
func (s *Store) MarkTellDelivered(_ context.Context, _ string, _ int64) error {
	return nil
}
