package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// defaultHistoryLimit is the number of entries a History call answers when its limit is 0, and maxHistoryLimit the most
// it answers.
const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 200
)

// epicHistoryCursor is the fixed message for a History cursor that does not parse.
const epicHistoryCursor = "epic history cursor does not parse"

// The kinds of an epic's history, in the order entries at one time sort by. historyKinds counts them.
const (
	historyTurn = iota
	historyTrigger
	historyStep
	historyEvidence
	historyDecision
	historyTell
	historyAction
	historyKinds
)

// historyCursorFields is the number of fields a history cursor holds: the time, the kind, the seq and the id.
const historyCursorFields = 4

// historyKey orders an entry of an epic's history: by time, then by kind, then by the kind's own position, which is seq for
// a turn, a decision, a tell or an action and id for a trigger, a step or an evidence run. The seq and the id of an entry are
// unique within its kind, so no two entries share a key.
type historyKey struct {
	time int64
	kind int
	seq  int64
	id   string
}

// before reports whether k orders before other.
func (k historyKey) before(other historyKey) bool {
	switch {
	case k.time != other.time:
		return k.time < other.time
	case k.kind != other.kind:
		return k.kind < other.kind
	case k.seq != other.seq:
		return k.seq < other.seq
	default:
		return k.id < other.id
	}
}

// cursor returns the cursor of the entry at k: the key's fields, separated by "|" and encoded as unpadded base64url.
func (k historyKey) cursor() string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d|%d|%d|%s", k.time, k.kind, k.seq, k.id))
}

// parseHistoryCursor returns the key that cursor encodes. It reports false for a cursor that is not unpadded base64url,
// does not hold historyCursorFields fields, has a time, kind or seq that does not parse, or has a kind out of range.
func parseHistoryCursor(cursor string) (historyKey, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return historyKey{}, false
	}
	fields := strings.SplitN(string(raw), "|", historyCursorFields)
	if len(fields) != historyCursorFields {
		return historyKey{}, false
	}
	at, timeErr := strconv.ParseInt(fields[0], 10, 64)
	kind, kindErr := strconv.Atoi(fields[1])
	seq, seqErr := strconv.ParseInt(fields[2], 10, 64)
	if timeErr != nil || kindErr != nil || seqErr != nil || kind < 0 || kind >= historyKinds {
		return historyKey{}, false
	}
	return historyKey{time: at, kind: kind, seq: seq, id: fields[3]}, true
}

// historyEntry is one entry of an epic's history with the key that orders it.
type historyEntry struct {
	key   historyKey
	entry *orreryv1.EpicHistoryEntry
}

// historyEntryOf returns the entry at key that carries entry's row, with its time and cursor set from key.
func historyEntryOf(key historyKey, entry *orreryv1.EpicHistoryEntry) historyEntry {
	entry.Time = key.time
	entry.Cursor = key.cursor()
	return historyEntry{key: key, entry: entry}
}

// History answers one page of an epic's history, oldest first: the turns, triggers, decisions, tells and actions that
// orreryd's epic holds, the epic's labeled steps and its evidence runs. An epic the store holds is asked with its history and its
// runs are listed; a record-only epic holds its labeled steps alone. The page holds the entries strictly after the
// request's cursor, at most 50 for a limit of 0 and at most 200. Its next cursor is the last entry's, and empty when no
// entry follows. A cursor that does not parse answers invalid_argument. An id that neither the store nor the record root
// lists answers not_found, and the id is never joined into a path.
func (e epicsServer) History(
	ctx context.Context,
	req *connect.Request[orreryv1.EpicsHistoryRequest],
) (*connect.Response[orreryv1.EpicsHistoryResponse], error) {
	var after *historyKey
	if cursor := req.Msg.GetAfter(); cursor != "" {
		key, ok := parseHistoryCursor(cursor)
		if !ok {
			return nil, listError(connect.CodeInvalidArgument, epicHistoryCursor)
		}
		after = &key
	}
	id := req.Msg.GetId()
	stored, inStore, err := e.getStoredHistory(ctx, id)
	if err != nil {
		return nil, err
	}
	_, found, err := findEpic(e.root, id, e.heads)
	if err != nil {
		return nil, listError(connect.CodeInternal, epicsFailed)
	}
	if !inStore && !found {
		return nil, listError(connect.CodeNotFound, epicNotFound)
	}
	listed, err := e.listSteps(ctx, &enginev1.ListStepsRequest{Epic: id})
	if err != nil {
		return nil, err
	}
	labeled := epicStepMessages(stepsByEpic(listed.GetSteps())[id])
	var entries []historyEntry
	if inStore {
		runs := e.listEvidence(ctx, id)
		applyStoredRoles(labeled, storedStepRoles(stored, runs))
		entries = append(storedHistoryEntries(stored), runHistoryEntries(runs)...)
	}
	entries = append(entries, stepHistoryEntries(labeled)...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].key.before(entries[j].key) })
	page, next := historyPage(entries, after, req.Msg.GetLimit())
	return connect.NewResponse(&orreryv1.EpicsHistoryResponse{Entries: page, Next: next}), nil
}

// getStoredHistory asks the epic store for the epic of id with its history, which is what getStored asks without. It
// reports false, with no error, when there is no store or the store answers not_found or unimplemented, so that the
// record answers. An unavailable answer is retried as listSteps retries it. Any other failure answers unavailable. Each
// attempt runs under the timeout.
func (e epicsServer) getStoredHistory(ctx context.Context, id string) (*enginev1.StoredEpic, bool, error) {
	if e.store == nil {
		return nil, false, nil
	}
	var resp *connect.Response[enginev1.GetEpicResponse]
	err := retryUnder(ctx, e.clock, e.timeout, rpcretry.Unavailable, func(ctx context.Context) error {
		var err error
		resp, err = e.store.GetEpic(ctx, connect.NewRequest(&enginev1.GetEpicRequest{Id: id, History: true}))
		if err != nil {
			return fmt.Errorf("server: get epic history: %w", err)
		}
		return nil
	})
	if err != nil {
		if code := connect.CodeOf(err); code == connect.CodeNotFound || code == connect.CodeUnimplemented {
			return nil, false, nil
		}
		return nil, false, listError(connect.CodeUnavailable, epicStoreUnavailable)
	}
	return resp.Msg.GetEpic(), true, nil
}

// storedHistoryEntries returns the entries of the turns, triggers, decisions, tells and actions of stored.
func storedHistoryEntries(stored *enginev1.StoredEpic) []historyEntry {
	out := make([]historyEntry, 0, len(stored.GetTurns())+len(stored.GetTriggers())+
		len(stored.GetDecisions())+len(stored.GetTells())+len(stored.GetActions()))
	for _, turn := range stored.GetTurns() {
		out = append(out, historyEntryOf(
			historyKey{time: turn.GetStarted(), kind: historyTurn, seq: turn.GetSeq()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Turn{Turn: epicTurnMessage(turn)}},
		))
	}
	for _, trigger := range stored.GetTriggers() {
		out = append(out, historyEntryOf(
			historyKey{time: trigger.GetCreated(), kind: historyTrigger, id: trigger.GetId()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Trigger{Trigger: &orreryv1.EpicTrigger{
				Id: trigger.GetId(), Cause: trigger.GetCause(), State: trigger.GetState(), Turn: trigger.GetTurn(),
				Created: trigger.GetCreated(),
			}}},
		))
	}
	for _, decision := range stored.GetDecisions() {
		out = append(out, historyEntryOf(
			historyKey{time: decision.GetTime(), kind: historyDecision, seq: decision.GetSeq()},
			&orreryv1.EpicHistoryEntry{
				Item: &orreryv1.EpicHistoryEntry_Decision{Decision: epicDecisionMessage(decision)},
			},
		))
	}
	for _, tell := range stored.GetTells() {
		out = append(out, historyEntryOf(
			historyKey{time: tell.GetAt(), kind: historyTell, seq: tell.GetSeq()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Tell{Tell: &orreryv1.EpicTell{
				Seq: tell.GetSeq(), Text: tell.GetText(), At: tell.GetAt(), Told: tell.GetTold(),
			}}},
		))
	}
	for _, action := range stored.GetActions() {
		out = append(out, historyEntryOf(
			historyKey{time: action.GetTime(), kind: historyAction, seq: action.GetSeq()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Action{Action: &orreryv1.EpicAction{
				Seq: action.GetSeq(), Kind: action.GetKind(), Time: action.GetTime(), By: action.GetBy(),
				Session: action.GetSession(), From: action.GetFrom(), To: action.GetTo(),
			}}},
		))
	}
	return out
}

// epicTurnMessage converts turn to its EpicTurn, with every field the engine gave.
func epicTurnMessage(turn *enginev1.EpicTurn) *orreryv1.EpicTurn {
	return &orreryv1.EpicTurn{
		Seq:       turn.GetSeq(),
		Step:      turn.GetStep(),
		State:     turn.GetState(),
		Reason:    turn.GetReason(),
		Outline:   turn.GetOutline(),
		Outcome:   turn.GetOutcome(),
		Decisions: turn.GetDecisions(),
		Started:   turn.GetStarted(),
		Ended:     turn.GetEnded(),
	}
}

// runHistoryEntries returns the entries of the evidence runs. Each run's item is set, which Get leaves empty.
func runHistoryEntries(runs []*enginev1.EvidenceRun) []historyEntry {
	out := make([]historyEntry, 0, len(runs))
	for _, run := range runs {
		msg := epicRunMessage(run)
		msg.Item = run.GetItem()
		out = append(out, historyEntryOf(
			historyKey{time: run.GetCreated(), kind: historyEvidence, id: run.GetId()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Evidence{Evidence: msg}},
		))
	}
	return out
}

// stepHistoryEntries returns the entries of the labeled steps, each at its start.
func stepHistoryEntries(steps []*orreryv1.EpicStep) []historyEntry {
	out := make([]historyEntry, 0, len(steps))
	for _, step := range steps {
		out = append(out, historyEntryOf(
			historyKey{time: step.GetStarted(), kind: historyStep, id: step.GetId()},
			&orreryv1.EpicHistoryEntry{Item: &orreryv1.EpicHistoryEntry_Step{Step: step}},
		))
	}
	return out
}

// historyPage returns the page of entries, sorted oldest first, that follows the key after: the entries strictly after
// it, or all of them when after is nil. It holds at most historyLimit(limit) entries. Its cursor is the page's last
// entry's when entries follow the page, and empty otherwise.
func historyPage(entries []historyEntry, after *historyKey, limit int32) ([]*orreryv1.EpicHistoryEntry, string) {
	start := 0
	if after != nil {
		start = sort.Search(len(entries), func(i int) bool { return after.before(entries[i].key) })
	}
	end := min(start+historyLimit(limit), len(entries))
	page := make([]*orreryv1.EpicHistoryEntry, 0, end-start)
	for _, entry := range entries[start:end] {
		page = append(page, entry.entry)
	}
	next := ""
	if end < len(entries) {
		next = entries[end-1].key.cursor()
	}
	return page, next
}

// historyLimit returns the number of entries a page holds: defaultHistoryLimit for a limit of 0 or less, and
// maxHistoryLimit for a larger limit.
func historyLimit(limit int32) int {
	switch {
	case limit <= 0:
		return defaultHistoryLimit
	case limit > maxHistoryLimit:
		return maxHistoryLimit
	default:
		return int(limit)
	}
}
