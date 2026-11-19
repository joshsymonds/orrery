package enginehost

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// callerListings is the optional answer a StepCommands gives to a caller listing: the listings of the steps the
// caller's session started. Steps satisfies it. A StepCommands that does not is refused a caller listing as
// unimplemented, so the interface StepCommands itself does not grow.
type callerListings interface {
	CallerListings(ctx context.Context, caller store.EpicCaller, epic string) ([]store.StepListing, error)
}

// The compile-time check that *Steps answers caller listings, which the service asserts at run time.
var _ callerListings = (*Steps)(nil)

// CallerListings returns the listings of the steps the session caller names started, narrowed to epic when it is
// not empty, as StepStore.CallerStepListings returns them. A caller that resolves to no sessions row started no
// steps, so it has no listings and no error. A read error is returned wrapped.
func (s *Steps) CallerListings(ctx context.Context, caller store.EpicCaller, epic string) ([]store.StepListing, error) {
	id, found, err := s.store.CallerSession(ctx, caller)
	if err != nil {
		return nil, fmt.Errorf("resolve caller of step listing: %w", err)
	}
	if !found {
		return nil, nil
	}
	listings, err := s.store.CallerStepListings(ctx, id, epic)
	if err != nil {
		return nil, fmt.Errorf("list caller steps: %w", err)
	}
	return listings, nil
}
