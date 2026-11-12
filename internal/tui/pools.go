package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/palette"
)

// Column widths of a member row, in cells: its name, state, holder, and command. The status column
// holds the word stopping.
const (
	poolNameWidth    = 16
	poolStateWidth   = 11
	poolStatusWidth  = 8
	poolHolderWidth  = 22
	poolCommandWidth = 20
)

// poolsUnavailable is the pools view's only line when the tree has no Pools client.
const poolsUnavailable = "pools unavailable"

// poolRow is one member row of the pools view: a member of a pool in use, or a hold on a member
// of no pool in use. heldSince is 0 when the member is not held.
type poolRow struct {
	name      string
	state     string
	holder    *v1.PoolHolder
	heldSince int64
}

// memberStatus describes what keeps a member from being free: the word that names it, the color
// of the word, and the unix seconds it began. A free member has since 0.
type memberStatus struct {
	word  string
	color palette.Color
	since int64
}

// status returns what keeps the row's member from being free. A hold outranks a grant.
func (r poolRow) status() memberStatus {
	switch {
	case r.heldSince != 0:
		return memberStatus{word: "held", color: palette.Mauve, since: r.heldSince}
	case r.holder == nil:
		return memberStatus{word: "free", color: palette.Overlay0}
	case r.holder.GetStopping():
		return memberStatus{word: "stopping", color: palette.Peach, since: r.holder.GetSince()}
	default:
		return memberStatus{word: "granted", color: palette.Green, since: r.holder.GetSince()}
	}
}

// memberRow returns the row of a member of a pool in use.
func memberRow(member *v1.PoolMemberStatus) poolRow {
	return poolRow{
		name:      member.GetName(),
		state:     member.GetState(),
		holder:    member.GetHolder(),
		heldSince: member.GetHeldSince(),
	}
}

// outsideHolds returns the owner holds on members of no pool in use, in the order Status sends them.
func (m model) outsideHolds() []*v1.PoolHold {
	inPool := make(map[string]bool)
	for _, pool := range m.poolStatus.GetPools() {
		for _, member := range pool.GetMembers() {
			inPool[member.GetName()] = true
		}
	}
	var holds []*v1.PoolHold
	for _, hold := range m.poolStatus.GetHolds() {
		if !inPool[hold.GetMember()] {
			holds = append(holds, hold)
		}
	}
	return holds
}

// poolRows returns the member rows in display order: the members of each pool in use in the
// order their pool declares them, then each hold outside every pool.
func (m model) poolRows() []poolRow {
	var rows []poolRow
	for _, pool := range m.poolStatus.GetPools() {
		for _, member := range pool.GetMembers() {
			rows = append(rows, memberRow(member))
		}
	}
	for _, hold := range m.outsideHolds() {
		rows = append(rows, poolRow{name: hold.GetMember(), heldSince: hold.GetSince()})
	}
	return rows
}

// poolLocate returns the index in rows of the cursor: the row of the cursor's member when that
// member is still shown, else the cursor's index clamped to the last row. rows must not be empty.
func (m model) poolLocate(rows []poolRow) int {
	for i, row := range rows {
		if row.name == m.poolCursorID {
			return i
		}
	}
	return min(m.poolCursor, len(rows)-1)
}

// poolsMove steps the pools cursor by step rows, clamped to the first and last member rows.
func (m model) poolsMove(step int) model {
	rows := m.poolRows()
	if len(rows) == 0 {
		return m
	}
	m.poolCursor = min(max(m.poolLocate(rows)+step, 0), len(rows)-1)
	m.poolCursorID = rows[m.poolCursor].name
	return m
}

// poolsSync records where the pools cursor is after the rows changed, so that a later read can
// find its member again or clamp to the same place.
func (m model) poolsSync() model {
	if len(m.poolRows()) == 0 {
		m.poolCursor, m.poolCursorID = 0, ""
		return m
	}
	return m.poolsMove(0)
}

// loadPools reads the pools in use and the owner holds from the Pools client in a command. The
// read returns as a poolsMsg. It returns nil when the tree has no client.
func (m model) loadPools() tea.Cmd {
	if m.pools == nil {
		return nil
	}
	pools := m.pools
	return func() tea.Msg { return readPools(pools) }
}

// readPools reads the pools in use and the owner holds from pools. The read is a poolsMsg.
func readPools(pools enginev1connect.PoolsClient) poolsMsg {
	resp, err := pools.Status(context.Background(), connect.NewRequest(&v1.PoolsStatusRequest{}))
	if err != nil {
		return poolsMsg{err: fmt.Errorf("pools status: %w", err)}
	}
	return poolsMsg{status: resp.Msg}
}

// holdMember holds the member under the pools cursor, or frees it when it is held. The call runs
// in a command. A successful call reads the pools again, so the view shows the change, and a
// failed call shows its error on the status line. It does nothing without a Pools client or a
// member row.
func (m model) holdMember() (model, tea.Cmd) {
	rows := m.poolRows()
	if m.pools == nil || len(rows) == 0 {
		return m, nil
	}
	row := rows[m.poolLocate(rows)]
	pools := m.pools
	m.actionErr = nil
	return m, func() tea.Msg {
		if err := setHold(pools, row); err != nil {
			return actionMsg{err: err}
		}
		return readPools(pools)
	}
}

// setHold frees row's member when row is held, and holds it otherwise. Its error names the call
// that failed.
func setHold(pools enginev1connect.PoolsClient, row poolRow) error {
	ctx := context.Background()
	if row.heldSince != 0 {
		if _, err := pools.Free(ctx, connect.NewRequest(&v1.PoolsFreeRequest{Member: row.name})); err != nil {
			return fmt.Errorf("pools free: %w", err)
		}
		return nil
	}
	if _, err := pools.Hold(ctx, connect.NewRequest(&v1.PoolsHoldRequest{Member: row.name})); err != nil {
		return fmt.Errorf("pools hold: %w", err)
	}
	return nil
}

// reloadPools reads the pools while the pools view is shown, and nothing otherwise.
func (m model) reloadPools() tea.Cmd {
	if !m.poolsShown {
		return nil
	}
	return m.loadPools()
}

// poolsLoaded applies a read of the pools. A failed read keeps the rows it had and shows its
// error. A read that arrives after the pools view closed is dropped.
func (m model) poolsLoaded(msg poolsMsg) model {
	if !m.poolsShown {
		return m
	}
	if msg.err != nil {
		m.poolsErr = msg.err
		return m
	}
	m.poolStatus, m.poolsErr = msg.status, nil
	return m.poolsSync()
}

// togglePools opens the pools view, or returns to the panes from it. Opening reads the pools.
// Either way the error of an earlier read is cleared.
func (m model) togglePools() (model, tea.Cmd) {
	m.poolsShown = !m.poolsShown
	m.poolsErr = nil
	if !m.poolsShown {
		return m, nil
	}
	return m, m.loadPools()
}

// poolLines renders the pools view: each pool in use with its cap, its members, and its waiters,
// then the holds outside every pool. Without a Pools client it shows only that the view is
// unavailable.
func (m model) poolLines() []string {
	if m.pools == nil {
		return []string{poolsUnavailable}
	}
	if m.poolStatus == nil {
		return nil
	}
	rows := m.poolRows()
	cursor := ""
	if len(rows) > 0 {
		cursor = rows[m.poolLocate(rows)].name
	}
	var lines []string
	for _, pool := range m.poolStatus.GetPools() {
		lines = append(lines, palette.Fg(palette.Mauve, pool.GetName())+"  "+
			palette.Fg(palette.Overlay0, fmt.Sprintf("cap %d", pool.GetCap())))
		for _, member := range pool.GetMembers() {
			row := memberRow(member)
			lines = append(lines, m.poolRowLine(row, row.name == cursor))
		}
		if len(pool.GetWaiters()) > 0 {
			lines = append(lines, palette.Fg(palette.Overlay0, "  waiting"))
			for _, waiter := range pool.GetWaiters() {
				lines = append(lines, m.waiterLine(waiter))
			}
		}
	}
	if holds := m.outsideHolds(); len(holds) > 0 {
		lines = append(lines, palette.Fg(palette.Mauve, "holds"))
		for _, hold := range holds {
			row := poolRow{name: hold.GetMember(), heldSince: hold.GetSince()}
			lines = append(lines, m.poolRowLine(row, row.name == cursor))
		}
	}
	if len(m.poolStatus.GetPools()) == 0 && len(m.outsideHolds()) == 0 {
		lines = append(lines, "No pools in use.")
	}
	return lines
}

// poolRowLine formats one member row: cursor, name, state, status, holder, command, and since.
// The status word is colored from the palette, and the row under the cursor is drawn on the bar.
func (m model) poolRowLine(row poolRow, cursor bool) string {
	mark := "  "
	if cursor {
		mark = "> "
	}
	status := row.status()
	holder, command := unsetMark, unsetMark
	if row.holder != nil {
		holder = row.holder.GetProject()
		if argv := strings.Join(row.holder.GetCommand(), " "); argv != "" {
			command = argv
		}
	}
	line := mark +
		column(row.name, poolNameWidth, "") + " " +
		column(orUnset(row.state), poolStateWidth, palette.Overlay0) + " " +
		column(status.word, poolStatusWidth, status.color) + " " +
		column(holder, poolHolderWidth, "") + " " +
		column(command, poolCommandWidth, palette.Overlay0) + " " +
		m.age(status.since)
	if cursor {
		return m.bar(line)
	}
	return line
}

// waiterLine formats one request waiting for a pool: its place in line, project, command, count,
// and the age of its wait.
func (m model) waiterLine(waiter *v1.PoolWaiter) string {
	command := strings.Join(waiter.GetCommand(), " ")
	return fmt.Sprintf("    %d  %s %s count %d  %s",
		waiter.GetPosition(),
		column(waiter.GetProject(), poolHolderWidth, ""),
		column(orUnset(command), poolCommandWidth, palette.Overlay0),
		waiter.GetCount(),
		m.age(waiter.GetSince()))
}

// orUnset returns text, or unsetMark when text is empty.
func orUnset(text string) string {
	if text == "" {
		return unsetMark
	}
	return text
}
