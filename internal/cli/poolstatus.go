package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// day is the largest unit poolAge reports.
const day = 24 * time.Hour

// PoolStatus writes the pools in use, each with its members and waiters, then the owner holds on members of no pool
// in use. Ages are measured from now.
func PoolStatus(ctx context.Context, pools enginev1connect.PoolsClient, now time.Time, w io.Writer) error {
	resp, err := pools.Status(ctx, connect.NewRequest(&v1.PoolsStatusRequest{}))
	if err != nil {
		return fmt.Errorf("pool status: %w", err)
	}
	if _, err = io.WriteString(w, renderPoolStatus(resp.Msg, now)); err != nil {
		return fmt.Errorf("pool status: write: %w", err)
	}
	return nil
}

// renderPoolStatus returns the text PoolStatus writes for status.
func renderPoolStatus(status *v1.PoolsStatusResponse, now time.Time) string {
	var out strings.Builder
	if len(status.GetPools()) == 0 {
		out.WriteString("no pools in use\n")
	}
	inPool := make(map[string]bool)
	for _, pool := range status.GetPools() {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "%s (cap %d)\n", pool.GetName(), pool.GetCap())
		for _, member := range pool.GetMembers() {
			inPool[member.GetName()] = true
			fmt.Fprintf(&out, "  %-12s %-12s %s\n", member.GetName(), member.GetState(), memberStatus(member, now))
		}
		if len(pool.GetWaiters()) > 0 {
			out.WriteString("  waiters\n")
		}
		for _, waiter := range pool.GetWaiters() {
			fmt.Fprintf(&out, "    %d  %s  %s  count %d  %s\n", waiter.GetPosition(), waiter.GetProject(),
				strings.Join(waiter.GetCommand(), " "), waiter.GetCount(), poolAge(now, waiter.GetSince()))
		}
	}
	var outside []string
	for _, hold := range status.GetHolds() {
		if !inPool[hold.GetMember()] {
			outside = append(outside, fmt.Sprintf("  %-12s held %s\n", hold.GetMember(), poolAge(now, hold.GetSince())))
		}
	}
	if len(outside) > 0 {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString("holds outside pools in use\n")
		for _, line := range outside {
			out.WriteString(line)
		}
	}
	return out.String()
}

// memberStatus describes a member's grant and hold: granted with its holder, held with its age, both when a granted
// member is being stopped, or free.
func memberStatus(member *v1.PoolMemberStatus, now time.Time) string {
	var parts []string
	if holder := member.GetHolder(); holder != nil {
		parts = append(parts, fmt.Sprintf("granted %s pid %d %s %s", holder.GetProject(), holder.GetClientPid(),
			strings.Join(holder.GetCommand(), " "), poolAge(now, holder.GetSince())))
	}
	if member.GetHeldSince() != 0 {
		parts = append(parts, "held "+poolAge(now, member.GetHeldSince()))
	}
	if len(parts) == 0 {
		return "free"
	}
	return strings.Join(parts, "; ")
}

// poolAge returns how long before now since was, in the largest whole unit.
func poolAge(now time.Time, since int64) string {
	elapsed := max(now.Sub(time.Unix(since, 0)), 0)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed/time.Second))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed/time.Minute))
	case elapsed < day:
		return fmt.Sprintf("%dh", int(elapsed/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(elapsed/day))
	}
}
