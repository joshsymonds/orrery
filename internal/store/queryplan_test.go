package store

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// queryPlanSource is the sqlc output that holds the SQL text of every query the Reader issues.
const queryPlanSource = "sqlcgen/queries.sql.go"

// TestReaderQueryPlans fails when a Reader query scans the events table without a partial index. It takes each
// query's text from queryPlanSource, runs EXPLAIN QUERY PLAN on it over a migrated store, and rejects any plan row
// that scans events, under its name or any alias, by a full scan or by an index that is not partial. A search of
// events is not a scan and passes.
// An exported *Reader method with no entry in the list below fails too, so a new reader query cannot skip the check.
func TestReaderQueryPlans(t *testing.T) {
	t.Parallel()
	// readerQueries names the sqlcgen queries that each exported *Reader method issues.
	readerQueries := map[string][]string{
		"Events":             {"EventPageAfter", "OldestEventSeq", "NewestEventSeq", "ListUnfinishedEngineText"},
		"Snapshot":           {"ListSessions", "NewestEventSeq"},
		"EngineHistory":      {"GetEngineSession", "GetEngineFirstPrompt", "SessionEventsAfter", "ListEngineText"},
		"EngineEventsBefore": {"GetEngineSession", "SessionEventsBefore"},
		"HeadlessSessions":   {"ListHeadlessSessions"},
		"LastTurnEnds":       {"LastTurnEnds"},
		"Dirs":               {"ListKnownDirs"},
		"EpicSessions":       {"ListEpicSessions"},
		"PromptStates":       {"ListPromptStates"},
		"ArchivedRows":       {"ListArchivedRows"},
		"ClosureVerdicts":    {"ListClosureVerdicts"},
		"PutOffQuestions":    {"ListPutOffQuestions"},
		"PaneSends":          {"ListPaneSends"},
		"PendingCallerEvents": {
			"ListPendingCallerSteps", "ListSteeredEpics", "ListOpenEpicQuestions", "CountCallerEvent",
		},
		"StepFacts": {"ListStepFacts"},
		"Contract":  {"GetContractListing", "ListContractVersions", "ListContractStates"},
		"Contracts": {"ListContractListings"},
		"Accounts":  {"ListLLMAccounts"},
		"Routes":    {"GetRouteRegistry", "ListRouteSeats", "ListRouteContexts", "ListRouteBindings"},
	}
	checkReaderMethodsListed(t, readerQueries)

	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "events")
	for method, names := range readerQueries {
		for _, name := range names {
			text, ok := texts[name]
			if !ok {
				t.Fatalf("Reader.%s issues query %s, which %s does not define", method, name, queryPlanSource)
			}
			for _, detail := range unpartitionedEventsScans(t, s.db.DB, partial, name, text) {
				t.Errorf("Reader.%s query %s: plan row %q scans events without a partial index",
					method, name, detail)
			}
		}
	}
}

// TestDueResumeQueryPlans fails when a query of the due pass or of a limit's write scans due_resumes without a
// partial index. The table keeps every row it closes, so a full scan grows with each reset the store has seen. A
// search of due_resumes is not a scan and passes.
func TestDueResumeQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "due_resumes")
	names := map[string]bool{"due_resumes": true}
	for _, name := range []string{"InsertDueResume", "GetDueResume", "ListDueResumes", "CloseDueResume", "CloseDueResumesOf"} {
		text, ok := texts[name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", name, queryPlanSource)
		}
		for _, detail := range planDetails(t, s.db.DB, name, text) {
			if index, scans := eventsScan(detail, names); scans && !partial[index] {
				t.Errorf("query %s: plan row %q scans due_resumes without a partial index", name, detail)
			}
		}
	}
}

// TestHolderPromptQueryPlans fails when a query that writes a turn end's answered uuids, or counts the holder prompts
// no later turn end answers, scans events without a partial index. Re-adoption counts the owed prompts of each holder it
// attaches, and events holds every session's log, so a full scan of it grows with the whole store. A search of events
// is not a scan and passes.
func TestHolderPromptQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "events")
	for _, name := range []string{"InsertEngineTurnEndedEvent", "CountHolderPromptsUnanswered"} {
		text, ok := texts[name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", name, queryPlanSource)
		}
		for _, detail := range unpartitionedEventsScans(t, s.db.DB, partial, name, text) {
			t.Errorf("query %s: plan row %q scans events without a partial index", name, detail)
		}
	}
}

// TestSessionHeadlineQueryPlans fails when a headline query scans events without a partial index. The headline pass
// lists the due sessions on every pass and reads each due session's recent turns, so each of these must search events
// by session rather than read the whole table. A search of events is not a scan and passes.
func TestSessionHeadlineQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "events")
	for _, name := range []string{"ListHeadlineDueSessions", "GetHeadlineDueSession", "ListRecentTurns", "SetSessionHeadline"} {
		text, ok := texts[name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", name, queryPlanSource)
		}
		for _, detail := range unpartitionedEventsScans(t, s.db.DB, partial, name, text) {
			t.Errorf("query %s: plan row %q scans events without a partial index", name, detail)
		}
	}
}

// TestToldLimitQueryPlans checks the told-limit queries against told_limit_steps. Deleting a limit's marks searches its
// primary key. Listing reads every mark, as the check at start needs them all, so it may scan, but only in primary-key
// order: a bare scan of the table or a temporary sort would mean the key no longer serves the query.
func TestToldLimitQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	names := map[string]bool{"told_limit_steps": true}
	for _, tc := range []struct {
		name     string
		scanning bool
	}{
		{"InsertToldLimitStep", false},
		{"DeleteToldLimit", false},
		{"ListToldLimitSteps", true},
	} {
		text, ok := texts[tc.name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", tc.name, queryPlanSource)
		}
		for _, detail := range planDetails(t, s.db.DB, tc.name, text) {
			if strings.Contains(detail, "TEMP B-TREE") {
				t.Errorf("query %s: plan row %q sorts told_limit_steps in a temporary b-tree", tc.name, detail)
			}
			index, scans := eventsScan(detail, names)
			switch {
			case scans && !tc.scanning:
				t.Errorf("query %s: plan row %q scans told_limit_steps, want a search of its key", tc.name, detail)
			case scans && index == "":
				t.Errorf("query %s: plan row %q scans told_limit_steps without its key", tc.name, detail)
			}
		}
	}
}

// TestStepQueryPlans fails when a query that reads steps by an epic's labels, lists the open steps or lists the step
// ends a caller has not read scans steps, or scans it through an index that is not partial. steps holds every step's
// prompt, result, checks and verdicts, so a scan of it reads the whole history: the interpreter reads an epic's labeled
// steps on every decision, the limit notices and caller events list theirs on every pass, and serve lists the running
// steps' listings for every epic list and an epic's listings for every epic page. A search of steps is not a scan and
// passes. ListLabelledSteps and ListLabelledStepListings list the labeled steps of every epic and so read the whole
// table; no pass reads them, and they are not listed. A read of one task's steps of an epic searches both labels, in
// the order it returns them. The count of the labeled steps by label, which an epic list takes its ended counts from,
// reads the label index alone and no step's row.
func TestStepQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "steps")
	names := map[string]bool{"steps": true}
	for _, name := range []string{
		"ListEpicTaskSteps", "ListEpicSteps", "ListOpenEpicSteps", "ListOpenEpicTaskSteps", "ListOpenSteps",
		"ListLimitedSteps", "ListCallerSteps", "ListPendingCallerSteps", "ListEpicStepListings",
		"ListEpicTaskStepListings", "ListOpenStepListings",
	} {
		text, ok := texts[name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", name, queryPlanSource)
		}
		for _, detail := range planDetails(t, s.db.DB, name, text) {
			if index, scans := eventsScan(detail, names); scans && !partial[index] {
				t.Errorf("query %s: plan row %q scans steps without a partial index", name, detail)
			}
		}
	}
	want := []string{"SEARCH steps USING INDEX steps_epic_task (epic=? AND task=?)"}
	if got := planDetails(t, s.db.DB, "ListEpicTaskSteps", texts["ListEpicTaskSteps"]); !reflect.DeepEqual(got, want) {
		t.Errorf("ListEpicTaskSteps plan = %q, want %q", got, want)
	}
	for name, first := range map[string]string{
		"ListEpicStepListings":     "SEARCH steps USING INDEX steps_epic_task (epic=?)",
		"ListEpicTaskStepListings": "SEARCH steps USING INDEX steps_epic_task (epic=? AND task=?)",
		"ListOpenStepListings":     "SCAN steps USING INDEX steps_open",
	} {
		if got := planDetails(t, s.db.DB, name, texts[name]); len(got) == 0 || got[0] != first {
			t.Errorf("%s plan = %q, want it to read steps by %q", name, got, first)
		}
	}
	want = []string{"SCAN steps USING COVERING INDEX steps_epic_task"}
	if got := planDetails(
		t,
		s.db.DB,
		"CountLabelledSteps",
		texts["CountLabelledSteps"],
	); !reflect.DeepEqual(
		got,
		want,
	) {
		t.Errorf("CountLabelledSteps plan = %q, want %q", got, want)
	}
}

// TestEvidenceArtifactQueryPlans checks that the read of every artifact of an epic's runs searches the epic's runs by
// their epic index and each run's artifacts by its key, in key order, so one query serves every run of the epic. The
// read of the open runs' artifacts searches each open run's artifacts by its key too.
func TestEvidenceArtifactQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	for name, want := range map[string][]string{
		"ListEpicEvidenceArtifacts": {
			"SEARCH evidence_artifacts USING INDEX sqlite_autoindex_evidence_artifacts_1 (run=?)",
			"LIST SUBQUERY 1",
			"SEARCH evidence_runs USING INDEX evidence_runs_epic_created (epic=?)",
		},
		"ListOpenEvidenceArtifacts": {
			"SEARCH evidence_artifacts USING INDEX sqlite_autoindex_evidence_artifacts_1 (run=?)",
			"LIST SUBQUERY 1",
			"SCAN evidence_runs",
		},
	} {
		if got := planDetails(t, s.db.DB, name, texts[name]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s plan = %q, want %q", name, got, want)
		}
	}
}

// TestPassQueryPlans fails when the read of every open epic's node run states scans epic_node_runs. The interpreter
// reads it on every pass, and the table keeps the runs of every epic the store has held, so it must search each open
// epic's runs by key. It may scan epics, one row per epic, which the pass lists anyway.
func TestPassQueryPlans(t *testing.T) {
	t.Parallel()
	texts := sqlcQueryTexts(t)
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	name := "ListOpenEpicNodeRunStates"
	details := planDetails(t, s.db.DB, name, texts[name])
	searched := false
	for _, detail := range details {
		fields := strings.Fields(detail)
		if len(fields) > 1 && fields[1] == "epic_node_runs" {
			searched = searched || fields[0] == "SEARCH"
			if fields[0] == "SCAN" {
				t.Errorf("query %s: plan row %q scans epic_node_runs, want a search of each epic's runs", name, detail)
			}
		}
	}
	if !searched {
		t.Errorf("query %s: plan %q does not search epic_node_runs", name, details)
	}
}

// TestReaderQueryPlansAliases checks that the guard sees a scan of events under an alias, with or without AS, and
// passes a search of events under an alias. EXPLAIN QUERY PLAN names an aliased table by its alias, so each plan row
// of a query that reads events through an alias must be matched against the names that the query gives events.
func TestReaderQueryPlansAliases(t *testing.T) {
	t.Parallel()
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	partial := partialIndexesOf(t, s.db.DB, "events")
	for _, tc := range []struct {
		name    string
		text    string
		flagged bool
	}{
		{"aliased scan", `SELECT p.payload FROM events AS p WHERE p.kind = 'x'`, true},
		{"aliased scan without AS", `SELECT p.payload FROM events p WHERE p.kind = 'x'`, true},
		{
			"aliased scan through a non-partial index",
			`SELECT p.session_id, MAX(p.at) FROM events AS p WHERE p.kind = 'session_changed' GROUP BY p.session_id`,
			true,
		},
		{"aliased search", `SELECT p.payload FROM events AS p WHERE p.seq = ?`, false},
	} {
		rows := unpartitionedEventsScans(t, s.db.DB, partial, tc.name, tc.text)
		if got := len(rows) > 0; got != tc.flagged {
			t.Errorf("%s: flagged = %v (plan rows flagged %q), want %v", tc.name, got, rows, tc.flagged)
		}
	}
}

// unpartitionedEventsScans returns the plan rows of the query name, whose text is text, that scan events without a
// partial index.
func unpartitionedEventsScans(t *testing.T, db *sql.DB, partial map[string]bool, name, text string) []string {
	t.Helper()
	names := eventsNames(text)
	var rows []string
	for _, detail := range planDetails(t, db, name, text) {
		index, scans := eventsScan(detail, names)
		if scans && !partial[index] {
			rows = append(rows, detail)
		}
	}
	return rows
}

// checkReaderMethodsListed fails for each exported *Reader method that has no entry in listed.
func checkReaderMethodsListed(t *testing.T, listed map[string][]string) {
	t.Helper()
	for method := range reflect.TypeFor[*Reader]().Methods() {
		if _, ok := listed[method.Name]; !ok {
			t.Errorf(
				"exported Reader method %s has no entry in TestReaderQueryPlans; list the sqlcgen queries it issues",
				method.Name,
			)
		}
	}
}

// sqlcQueryTexts returns the SQL text of each query in queryPlanSource, keyed by the query name in its "-- name:"
// header line.
func sqlcQueryTexts(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), queryPlanSource, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", queryPlanSource, err)
	}
	texts := make(map[string]string)
	for _, text := range stringConsts(t, file) {
		if name, ok := queryName(text); ok {
			texts[name] = text
		}
	}
	return texts
}

// stringConsts returns the value of each single-valued string declaration in file, in source order.
func stringConsts(t *testing.T, file *ast.File) []string {
	t.Helper()
	var values []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Values) != 1 {
			return true
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote %s in %s: %v", spec.Names[0].Name, queryPlanSource, err)
		}
		values = append(values, value)
		return true
	})
	return values
}

// queryName returns the name in the "-- name: NAME :many" header line that opens a sqlc query, and false when text
// has no such line.
func queryName(text string) (string, bool) {
	header, _, _ := strings.Cut(text, "\n")
	rest, ok := strings.CutPrefix(header, "-- name: ")
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(rest, " ")
	return name, true
}

// partialIndexesOf maps each index on table to whether it is a partial index.
func partialIndexesOf(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name, partial FROM pragma_index_list(?)`, table)
	if err != nil {
		t.Fatalf("index list of %s: %v", table, err)
	}
	defer rows.Close()
	partial := make(map[string]bool)
	for rows.Next() {
		var name string
		var isPartial int
		if err := rows.Scan(&name, &isPartial); err != nil {
			t.Fatalf("scan index list of %s: %v", table, err)
		}
		partial[name] = isPartial == 1
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read index list of %s: %v", table, err)
	}
	return partial
}

// planDetails returns the detail column of each EXPLAIN QUERY PLAN row of the query name, whose text is text. Every ?
// placeholder is bound to NULL, because the plan does not depend on the values. A ?NNN placeholder that the count
// misses makes the query fail with a missing-argument error, so it cannot pass unexamined.
func planDetails(t *testing.T, db *sql.DB, name, text string) []string {
	t.Helper()
	args := make([]any, strings.Count(text, "?"))
	rows, err := db.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+text, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN of %s: %v", name, err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan EXPLAIN QUERY PLAN of %s: %v", name, err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read EXPLAIN QUERY PLAN of %s: %v", name, err)
	}
	return details
}

// eventsNames returns the names that text refers to the events table by: "events" itself, and each alias that text
// gives it, with or without AS. A keyword that follows events, such as WHERE, matches too; no plan row is named after a
// keyword, so that name matches nothing.
func eventsNames(text string) map[string]bool {
	names := map[string]bool{"events": true}
	for _, match := range regexp.MustCompile(`(?i)\bevents(?:\s+AS)?\s+(\w+)`).FindAllStringSubmatch(text, -1) {
		names[match[1]] = true
	}
	return names
}

// eventsScan reports whether a plan row is a full scan of the events table, and the index that scan reads through,
// which is "" when the row names none. The row names events by one of names, the names its query gives it (see
// eventsNames). A SEARCH row is not a scan and reports false.
func eventsScan(detail string, names map[string]bool) (string, bool) {
	fields := strings.Fields(detail)
	if len(fields) < 2 || fields[0] != "SCAN" || !names[fields[1]] {
		return "", false
	}
	for i := 2; i+1 < len(fields); i++ {
		if fields[i] == "INDEX" {
			return fields[i+1], true
		}
	}
	return "", true
}

// TestNodeRunQueryPlans fails when a query that EpicFlow issues, or a node-run write, scans a table without an index.
// The interpreter calls EpicFlow for every epic it decides on every pass, so each of its queries must search an epic's
// rows by key rather than read a whole table. The EpicFlow queries are recorded from a real read of a seeded epic with
// a planned task, a plan run and a review run, so a query EpicFlow later adds is checked too, and the read of the
// review run's labeled steps among them.
func TestNodeRunQueryPlans(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	seedFlowPlanEpic(t, s)
	recorder := &planRecorder{db: s.db.DB}
	if _, err := readEpicFlow(ctx, sqlcgen.New(recorder), queryPlanEpic); err != nil {
		t.Fatalf("readEpicFlow: %v", err)
	}
	texts := sqlcQueryTexts(t)
	byName := map[string]string{}
	for _, text := range recorder.texts {
		if name, ok := queryName(text); ok {
			byName[name] = text
		}
	}
	if _, ok := byName["ListEpicTaskSteps"]; !ok {
		t.Fatalf("EpicFlow issued no ListEpicTaskSteps for the review run, want its labeled steps read")
	}
	for _, name := range []string{
		"ListEpicNodeRuns", "CountPendingEpicTriggers", "InsertEpicNodeRun", "EndEpicNodeRun",
		"MoveEpicLoopRound", "ParkEpicLoopRun", "GrantEpicParkedLoops", "InsertEpicTriggerOnce",
		"ListEpicWorkflowNodes",
	} {
		text, ok := texts[name]
		if !ok {
			t.Fatalf("query %s is not defined in %s", name, queryPlanSource)
		}
		byName[name] = text
	}
	if len(byName) < 12 {
		t.Fatalf("recorded %d queries, want EpicFlow's reads and the node-run writes", len(byName))
	}
	for name, text := range byName {
		for _, detail := range planDetails(t, s.db.DB, name, text) {
			if fields := strings.Fields(detail); len(fields) > 1 && fields[0] == "SCAN" {
				t.Errorf("query %s: plan row %q scans a table, want a search of the epic's key", name, detail)
			}
		}
	}
}

// queryPlanEpic is the synthetic epic TestNodeRunQueryPlans reads.
const queryPlanEpic = "orrery-0123456/query-plan"

// seedFlowPlanEpic stores queryPlanEpic with one planned task, a running plan run and a running review run, so a read
// of it issues every query EpicFlow has, the per-task ones and the labeled steps of a review run included.
func seedFlowPlanEpic(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	at := time.Unix(1700000000, 0)
	if err := s.CreateEpic(ctx, EpicRow{
		ID: queryPlanEpic, Repository: "/work/synthetic", Base: "base", Title: "Plan the query plans",
		Phase: "accepted", Created: at, Changed: at,
		Requirements: []EpicRequirement{{ID: "R1", Text: "plans search", Evidence: "plan test", Kind: "test"}},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if err := s.StartNodes(ctx, queryPlanEpic, []NodeRun{{
		Path: "plan", ID: NodeRunID(queryPlanEpic, "plan", 0, "start"), Node: "plan", Kind: "plan",
	}, {
		Path: "review", ID: NodeRunID(queryPlanEpic, "review", 0, "start"), Node: "review", Kind: flowReviewKind,
	}}, nil, at); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, queryPlanEpic, at); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, queryPlanEpic, 1, EpicPlan{
		Tasks: []EpicTaskInput{{
			Title: "Search by key", Deliverable: "every query searches", Files: []string{"plan.go"},
			Requirements: []string{"R1"}, Gate: "check",
		}},
		Outline: "One task", Outcome: "work",
	}, at); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// planRecorder is a sqlcgen.DBTX that keeps the text of each query it runs on db, so a test can read the plan of
// every query a store read issues.
type planRecorder struct {
	db    *sql.DB
	texts []string
}

func (r *planRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r.texts = append(r.texts, query)
	return r.db.ExecContext(ctx, query, args...)
}

func (r *planRecorder) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	r.texts = append(r.texts, query)
	return r.db.PrepareContext(ctx, query)
}

func (r *planRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.texts = append(r.texts, query)
	return r.db.QueryContext(ctx, query, args...)
}

func (r *planRecorder) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.texts = append(r.texts, query)
	return r.db.QueryRowContext(ctx, query, args...)
}
