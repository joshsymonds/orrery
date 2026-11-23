package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// stageStoreUp returns a store at version 105 with 00106_contract_stage.sql applied: the provider's Up runs the one
// migration, and the store is moved no further.
func stageStoreUp(t *testing.T) *Store {
	t.Helper()
	s, provider := storeAtVersion(t, 105)
	if _, err := provider.UpByOne(t.Context()); err != nil {
		t.Fatalf("Up of 00106: %v", err)
	}
	return s
}

// TestStageSchema checks 00106_contract_stage.sql, which adds the staged column to contracts and the tables the contract
// stage writes. Each subtest opens a store at version 105, before the migration, and steps the migration Up. The subtests
// check the staged column on a contract stored before and after the migration, that each new table takes a row of its
// shape and reads it back through its queries, and that the CHECK constraints refuse an unknown kind, disposition and
// maker. Each subtest logs what it saw.
func TestStageSchema(t *testing.T) {
	t.Parallel()

	t.Run("contracts staged column", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		s, provider := storeAtVersion(t, 105)
		if _, err := s.fixtureExec(ctx,
			`INSERT INTO contracts (id, created) VALUES ('contract-before', 1700000100)`); err != nil {
			t.Fatalf("insert contract before 00106: %v", err)
		}
		if _, err := provider.UpByOne(ctx); err != nil {
			t.Fatalf("Up of 00106: %v", err)
		}
		before, err := s.fixtureQueries().GetContractStaged(ctx, "contract-before")
		if err != nil {
			t.Fatalf("read staged of the contract stored before 00106: %v", err)
		}
		if err := s.fixtureQueries().InsertContract(ctx,
			sqlcgen.InsertContractParams{ID: "contract-after", Created: 1700000200}); err != nil {
			t.Fatalf("propose a contract after 00106: %v", err)
		}
		after, err := s.fixtureQueries().GetContractStaged(ctx, "contract-after")
		if err != nil {
			t.Fatalf("read staged of the contract proposed after 00106: %v", err)
		}
		t.Logf("contract stored before 00106 reads staged %d; contract proposed after it reads staged %d", before, after)
		if before != 0 {
			t.Fatalf("staged of the contract stored before 00106 = %d, want 0", before)
		}
		if after != 1 {
			t.Fatalf("staged of the contract proposed after 00106 = %d, want 1", after)
		}
	})

	t.Run("contract_drafts", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		draft := sqlcgen.UpsertContractDraftParams{
			ID: "draft-one", Design: "", Repository: "/work/repo", Title: "Draft one", Body: "{}", Decisions: "[]",
			Words: "Store the draft", Writer: "claude-writer", Created: 1700000300, Updated: 1700000300,
		}
		if err := q.UpsertContractDraft(ctx, draft); err != nil {
			t.Fatalf("insert draft: %v", err)
		}
		draft.Title, draft.Updated = "Draft one, revised", 1700000400
		if err := q.UpsertContractDraft(ctx, draft); err != nil {
			t.Fatalf("upsert draft: %v", err)
		}
		got, err := q.GetContractDraft(ctx, "draft-one")
		if err != nil {
			t.Fatalf("read draft: %v", err)
		}
		t.Logf("draft read back: title %q, created %d, updated %d", got.Title, got.Created, got.Updated)
		if got.Title != "Draft one, revised" || got.Created != 1700000300 || got.Updated != 1700000400 {
			t.Fatalf("draft after upsert = %+v, want the revised title, created 1700000300 and updated 1700000400", got)
		}
		listed, err := q.ListDesignDrafts(ctx, "")
		if err != nil {
			t.Fatalf("list drafts of no design: %v", err)
		}
		if len(listed) != 1 || listed[0].ID != "draft-one" {
			t.Fatalf("drafts of no design = %v, want draft-one", listed)
		}
		if err := q.DeleteContractDraft(ctx, "draft-one"); err != nil {
			t.Fatalf("delete draft: %v", err)
		}
		if _, err := q.GetContractDraft(ctx, "draft-one"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("read draft after delete: err = %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("design_steps", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		if err := q.InsertDesignStep(ctx, sqlcgen.InsertDesignStepParams{
			Step: "step-research", Design: "design-one", Kind: StepResearch, Subject: "Does the store keep rows?",
			Repository: "/work/repo", Revision: "abc123", Delta: "", Created: 1700000500,
		}); err != nil {
			t.Fatalf("insert research step: %v", err)
		}
		// The reproduce step is written second, with the earlier created, so the listing takes it first.
		if err := q.InsertDesignStep(ctx, sqlcgen.InsertDesignStepParams{
			Step: "step-reproduce", Design: "design-one", Kind: StepReproduce, Subject: "go test ./internal/store/",
			Repository: "/work/repo", Revision: "abc123", Delta: "", Created: 1700000400,
		}); err != nil {
			t.Fatalf("insert reproduce step: %v", err)
		}
		listed, err := q.ListDesignSteps(ctx, "design-one")
		if err != nil {
			t.Fatalf("list steps: %v", err)
		}
		for _, step := range listed {
			t.Logf("step %s: kind %s, subject %q, created %d", step.Step, step.Kind, step.Subject, step.Created)
		}
		if len(listed) != 2 || listed[0].Step != "step-reproduce" || listed[1].Step != "step-research" {
			t.Fatalf("steps = %v, want step-reproduce then step-research", listed)
		}
	})

	t.Run("design_questions", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		if err := q.InsertDesignQuestion(ctx, sqlcgen.InsertDesignQuestionParams{
			Design: "design-one", Seq: 1, Contract: "", Text: "Which store?", Options: `["sqlite","postgres"]`,
			Recommendation: "sqlite", Asker: "claude-writer", Asked: 1700000600,
		}); err != nil {
			t.Fatalf("insert question: %v", err)
		}
		next, err := q.NextDesignQuestionSeq(ctx, "design-one")
		if err != nil {
			t.Fatalf("next question seq: %v", err)
		}
		if next != 2 {
			t.Fatalf("next question seq after seq 1 = %d, want 2", next)
		}
		answer := sqlcgen.AnswerDesignQuestionParams{
			Answer: "sqlite", Accepted: 1, Maker: StageMakerOwnerServe, Quote: "sqlite", MakerReason: "",
			Delegation: "", Answerer: "", Answered: 1700000700, Design: "design-one", Seq: 1,
		}
		changed, err := q.AnswerDesignQuestion(ctx, answer)
		if err != nil {
			t.Fatalf("answer question: %v", err)
		}
		again, err := q.AnswerDesignQuestion(ctx, answer)
		if err != nil {
			t.Fatalf("answer answered question: %v", err)
		}
		t.Logf("answering an unanswered question changed %d row(s); answering it again changed %d", changed, again)
		if changed != 1 || again != 0 {
			t.Fatalf("answer changed %d then %d rows, want 1 then 0", changed, again)
		}
		got, err := q.GetDesignQuestion(ctx, sqlcgen.GetDesignQuestionParams{Design: "design-one", Seq: 1})
		if err != nil {
			t.Fatalf("read question: %v", err)
		}
		if got.Answer != "sqlite" || got.Accepted != 1 || got.Maker != StageMakerOwnerServe || got.Answered != 1700000700 {
			t.Fatalf("question after answer = %+v, want answer sqlite, accepted, owner_serve, answered 1700000700", got)
		}
		listed, err := q.ListDesignQuestions(ctx, "design-one")
		if err != nil {
			t.Fatalf("list questions: %v", err)
		}
		if len(listed) != 1 {
			t.Fatalf("questions = %v, want one", listed)
		}
	})

	t.Run("design_dispositions", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		if err := q.InsertDesignDisposition(ctx, sqlcgen.InsertDesignDispositionParams{
			Design: "design-one", Seq: 1, Item: "R1", Disposition: DispositionAdopted, Reason: "The store keeps it",
			Boundary: "the store", Maker: StageMakerConversation, Quote: "adopt R1", MakerReason: "it holds",
			Delegation: "", Session: "claude-writer", At: 1700000800,
		}); err != nil {
			t.Fatalf("insert disposition: %v", err)
		}
		next, err := q.NextDesignDispositionSeq(ctx, "design-one")
		if err != nil {
			t.Fatalf("next disposition seq: %v", err)
		}
		listed, err := q.ListDesignDispositions(ctx, "design-one")
		if err != nil {
			t.Fatalf("list dispositions: %v", err)
		}
		t.Logf("dispositions %v; next seq %d", listed, next)
		if next != 2 || len(listed) != 1 || listed[0].Disposition != DispositionAdopted {
			t.Fatalf("dispositions = %v, next seq %d; want one adopted disposition and next seq 2", listed, next)
		}
	})

	t.Run("design_closure_skips", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		if err := q.InsertDesignClosureSkip(ctx, sqlcgen.InsertDesignClosureSkipParams{
			Design: "design-one", Reason: "Nothing to close", Maker: StageMakerOwnerConversation, Quote: "skip the closure",
			MakerReason: "no evidence", Delegation: "", Session: "claude-writer", At: 1700000900,
		}); err != nil {
			t.Fatalf("insert closure skip: %v", err)
		}
		got, err := q.GetDesignClosureSkip(ctx, "design-one")
		if err != nil {
			t.Fatalf("read closure skip: %v", err)
		}
		t.Logf("closure skip read back: reason %q, maker %s, at %d", got.Reason, got.Maker, got.At)
		if got.Reason != "Nothing to close" || got.Maker != StageMakerOwnerConversation || got.At != 1700000900 {
			t.Fatalf("closure skip = %+v, want the reason, maker and time it was written with", got)
		}
	})

	t.Run("CHECKs", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		q := stageStoreUp(t).fixtureQueries()
		if err := q.InsertDesignQuestion(ctx, sqlcgen.InsertDesignQuestionParams{
			Design: "design-one", Seq: 1, Contract: "", Text: "Which store?", Options: "[]", Recommendation: "sqlite",
			Asker: "claude-writer", Asked: 1700000600,
		}); err != nil {
			t.Fatalf("insert question: %v", err)
		}
		refusals := []struct {
			name  string
			write func() error
		}{
			{"design_steps kind", func() error {
				return q.InsertDesignStep(ctx, sqlcgen.InsertDesignStepParams{
					Step: "step-bad", Design: "design-one", Kind: "bogus", Subject: "", Repository: "", Revision: "",
					Delta: "", Created: 1700000500,
				})
			}},
			{"design_dispositions disposition", func() error {
				return q.InsertDesignDisposition(ctx, sqlcgen.InsertDesignDispositionParams{
					Design: "design-one", Seq: 1, Item: "R1", Disposition: "bogus", Maker: StageMakerConversation,
					Session: "claude-writer", At: 1700000800,
				})
			}},
			{"design_dispositions maker", func() error {
				return q.InsertDesignDisposition(ctx, sqlcgen.InsertDesignDispositionParams{
					Design: "design-one", Seq: 1, Item: "R1", Disposition: DispositionAdopted, Maker: "bogus",
					Session: "claude-writer", At: 1700000800,
				})
			}},
			{"design_closure_skips maker", func() error {
				return q.InsertDesignClosureSkip(ctx, sqlcgen.InsertDesignClosureSkipParams{
					Design: "design-one", Reason: "", Maker: "bogus", Session: "claude-writer", At: 1700000900,
				})
			}},
			{"design_questions maker", func() error {
				_, err := q.AnswerDesignQuestion(ctx, sqlcgen.AnswerDesignQuestionParams{
					Answer: "sqlite", Accepted: 1, Maker: "bogus", Answered: 1700000700, Design: "design-one", Seq: 1,
				})
				return err
			}},
		}
		for _, refusal := range refusals {
			err := refusal.write()
			t.Logf("%s: %v", refusal.name, err)
			if err == nil {
				t.Errorf("%s accepted an unknown value, want a CHECK refusal", refusal.name)
			} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
				t.Errorf("%s: err = %v, want a CHECK refusal", refusal.name, err)
			}
		}
	})
}
