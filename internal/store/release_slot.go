package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of holder a repository's release slot has: an epic, which holds it until its release frees it, and an
// outside releaser under a label, which holds it until the label gives it back or its deadline passes.
const (
	ReleaseSlotEpic    = "epic"
	ReleaseSlotOutside = "outside"
)

// ReleaseSlotOutsidePrefix begins the holder of a release slot an outside releaser holds, which is the prefix and the
// label of the hold.
const ReleaseSlotOutsidePrefix = "outside:"

// ReleaseSlotHolder is who holds a repository's release slot. An epic holder sets Epic. An outside holder sets Label,
// and Deadline, the instant from which the hold is freed without a give.
type ReleaseSlotHolder struct {
	Kind     string
	Epic     string
	Label    string
	Deadline time.Time
}

// slotHolderOf returns the holder the release slot row names.
func slotHolderOf(row sqlcgen.EpicReleaseSlot) ReleaseSlotHolder {
	if row.HolderKind == ReleaseSlotOutside {
		return ReleaseSlotHolder{Kind: ReleaseSlotOutside, Label: row.Label, Deadline: time.Unix(row.Deadline, 0)}
	}
	return ReleaseSlotHolder{Kind: ReleaseSlotEpic, Epic: row.Epic}
}

// ReleaseSlot returns the holder of the release slot of repository, and whether the slot is held.
func (s *Store) ReleaseSlot(ctx context.Context, repository string) (ReleaseSlotHolder, bool, error) {
	row, err := sqlcgen.New(s.db).GetEpicReleaseSlotHolder(ctx, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseSlotHolder{}, false, nil
	}
	if err != nil {
		return ReleaseSlotHolder{}, false, fmt.Errorf("read release slot of %s: %w", repository, err)
	}
	return slotHolderOf(row), true, nil
}

// HoldReleaseSlot holds the release slot of repository for the outside releaser label until deadline, in one write. A
// free slot is taken, and a slot label already holds keeps its hold with deadline in place of its own. A slot an epic
// or another label holds is left as it is. It returns the holder after the write, and whether label holds the slot. It
// returns an error when repository or label is empty.
func (s *Store) HoldReleaseSlot(
	ctx context.Context, repository, label string, deadline time.Time,
) (ReleaseSlotHolder, bool, error) {
	if repository == "" || label == "" {
		return ReleaseSlotHolder{}, false, errors.New("hold release slot: empty repository or label")
	}
	var holder ReleaseSlotHolder
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.HoldOutsideReleaseSlot(ctx, sqlcgen.HoldOutsideReleaseSlotParams{
			Repository: repository, Label: label, Deadline: deadline.Unix(),
		}); err != nil {
			return fmt.Errorf("hold release slot of %s for %s: %w", repository, label, err)
		}
		row, err := queries.GetEpicReleaseSlotHolder(ctx, repository)
		if err != nil {
			return fmt.Errorf("read release slot of %s: %w", repository, err)
		}
		holder = slotHolderOf(row)
		return nil
	})
	if err != nil {
		return ReleaseSlotHolder{}, false, err
	}
	return holder, holder.Kind == ReleaseSlotOutside && holder.Label == label, nil
}

// GiveReleaseSlot gives back the outside hold label has on the release slot of repository, in one write, and reports
// whether it did. A slot an epic holds, or another label holds, is left as it is.
func (s *Store) GiveReleaseSlot(ctx context.Context, repository, label string) (bool, error) {
	var given bool
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		removed, err := queries.GiveOutsideReleaseSlot(ctx, sqlcgen.GiveOutsideReleaseSlotParams{
			Repository: repository, Label: label,
		})
		if err != nil {
			return fmt.Errorf("give release slot of %s: %w", repository, err)
		}
		given = removed > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return given, nil
}

// FreeExpiredReleaseSlots frees, in one write, each outside hold whose deadline is at or before now, and returns the
// repositories whose slots it freed.
func (s *Store) FreeExpiredReleaseSlots(ctx context.Context, now time.Time) ([]string, error) {
	var freed []string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		repositories, err := queries.FreeExpiredReleaseSlots(ctx, now.Unix())
		if err != nil {
			return fmt.Errorf("free expired release slots: %w", err)
		}
		freed = repositories
		return nil
	})
	if err != nil {
		return nil, err
	}
	return freed, nil
}

// NextReleaseSlotDeadline returns the earliest deadline of an outside hold, and whether there is one.
func (s *Store) NextReleaseSlotDeadline(ctx context.Context) (time.Time, bool, error) {
	deadline, err := sqlcgen.New(s.db).NextOutsideReleaseSlotDeadline(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read next release slot deadline: %w", err)
	}
	return time.Unix(deadline, 0), true, nil
}
