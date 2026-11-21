import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { isUndoNone, releaseOf, replacementsOf, reportOf, reviewOf } from "./ending";
import {
  type Epic,
  EpicClosureSchema,
  EpicContractSchema,
  EpicLedgerFindingSchema,
  EpicReleaseProofSchema,
  EpicReleaseRunSchema,
  EpicReportSchema,
  EpicReviewVerdictSchema,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";

type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;
type FindingInit = MessageInitShape<typeof EpicLedgerFindingSchema>;

// An orreryd epic named alpha, in the given phase, at the given path. The rest of the response is init.
function epicIn(outcome: string, init: DetailInit = {}, repoPath = ""): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    ...init,
    epic: create(EpicSchema, { id: "repo-a/alpha", slug: "alpha", source: "orreryd", outcome, path: repoPath }),
  });
}

function verdict(reviewer: string, result: string, reason = "") {
  return create(EpicReviewVerdictSchema, { position: 1n, reviewer, step: "", verdict: result, reason });
}

function finding(init: FindingInit) {
  return create(EpicLedgerFindingSchema, init);
}

function closure(round: number, result: string, reason = "") {
  return create(EpicClosureSchema, { round: BigInt(round), step: "", result, reason });
}

// A release step at its position, in the given state, with its undo text.
function proof(position: number, step: string, state: string, undo: string, init: MessageInitShape<typeof EpicReleaseProofSchema> = {}) {
  return create(EpicReleaseProofSchema, { position: BigInt(position), step, state, undo, target: "", ...init });
}

// A run of the given part and position, of the given attempt, that passed or failed.
function run(part: string, position: number, attempt: number, passed: boolean, reason = "") {
  return create(EpicReleaseRunSchema, {
    part,
    position: BigInt(position),
    attempt: BigInt(attempt),
    step: "",
    passed,
    reason,
  });
}

function epicListed(id: string, slug: string, repoPath: string): Epic {
  return create(EpicSchema, { id, slug, path: repoPath, source: "orreryd" });
}

test("EpicReview: reached only in the review phases, never in accepted, built or met_at_base", () => {
  const reviewed: DetailInit = {
    reviews: [verdict("r1", "holds")],
    ledger: [finding({ id: "r1-1", reviewer: "r1", result: "confirmed" })],
  };
  assert.equal(reviewOf(epicIn("accepted", reviewed)), null);
  assert.equal(reviewOf(epicIn("built", reviewed)), null);
  assert.equal(reviewOf(epicIn("met_at_base", reviewed)), null);
  assert.notEqual(reviewOf(epicIn("reviewing")), null);
  assert.notEqual(reviewOf(epicIn("released", reviewed)), null);
});

test("EpicReview: a review phase with no reviews and no findings is drawn only while reviewing or correcting", () => {
  assert.equal(reviewOf(epicIn("clean")), null);
  assert.equal(reviewOf(epicIn("stopped")), null);
  assert.notEqual(reviewOf(epicIn("correcting")), null);
});

test("EpicReview: count words name the reviewers and the findings", () => {
  const two: DetailInit = {
    reviews: [verdict("r1", "holds"), verdict("r2", "gap")],
    ledger: [finding({ id: "r1-1", reviewer: "r1", result: "dropped" })],
  };
  assert.equal(reviewOf(epicIn("reviewing", two))?.words, "2 reviewers · 1 finding");
  assert.equal(reviewOf(epicIn("reviewing", { reviews: [verdict("r1", "holds")] }))?.words, "1 reviewer · no findings");
  assert.equal(
    reviewOf(
      epicIn("reviewing", {
        ledger: [finding({ id: "a", result: "" }), finding({ id: "b", result: "" }), finding({ id: "c", result: "" })],
      }),
    )?.words,
    "no verdict yet · 3 findings",
  );
});

test("EpicReview: each verdict is a reviewer row with its chip word and tone", () => {
  const view = reviewOf(
    epicIn("reviewing", {
      reviews: [
        verdict("r1", "holds", "every claim checks"),
        verdict("r2", "fails", "the test is missing"),
        verdict("r3", "gap", "the release path is unread"),
        verdict("r4", "abstain", "out of scope"),
      ],
    }),
  );
  assert.deepEqual(view?.reviewers, [
    { reviewer: "r1", reason: "every claim checks", verdict: "holds", tone: "ok" },
    { reviewer: "r2", reason: "the test is missing", verdict: "fails", tone: "bad" },
    { reviewer: "r3", reason: "the release path is unread", verdict: "gap", tone: "attention" },
    { reviewer: "r4", reason: "out of scope", verdict: "abstain", tone: "quiet" },
  ]);
});

test("EpicReview: each finding row carries its claim, location and result chip with its tone", () => {
  const view = reviewOf(
    epicIn("reviewing", {
      ledger: [
        finding({ id: "r1-1", claim: "claim one", location: "parse.go:10", result: "confirmed" }),
        finding({ id: "r1-2", claim: "claim two", location: "", result: "dropped" }),
        finding({ id: "r2-1", claim: "claim three", location: "api.go:4", result: "undetermined" }),
        finding({ id: "r2-2", claim: "claim four", result: "", unverified: true, reason: "no verifier ran" }),
        finding({ id: "r2-3", claim: "claim five", result: "", unverified: false }),
        finding({ id: "r2-4", claim: "claim six", result: "stalled" }),
      ],
    }),
  );
  assert.deepEqual(
    view?.findings.map(({ id, what, where, result, tone, reason }) => ({ id, what, where, result, tone, reason })),
    [
      { id: "r1-1", what: "claim one", where: "parse.go:10", result: "confirmed", tone: "bad", reason: "" },
      { id: "r1-2", what: "claim two", where: "", result: "dropped", tone: "quiet", reason: "" },
      { id: "r2-1", what: "claim three", where: "api.go:4", result: "undetermined", tone: "attention", reason: "" },
      { id: "r2-2", what: "claim four", where: "", result: "left unverified", tone: "attention", reason: "no verifier ran" },
      { id: "r2-3", what: "claim five", where: "", result: "being verified", tone: "live", reason: "" },
      { id: "r2-4", what: "claim six", where: "", result: "stalled", tone: "quiet", reason: "" },
    ],
  );
});

test("EpicReview: each finding lists its closures by round, with the reason after the result when there is one", () => {
  const view = reviewOf(
    epicIn("reviewing", {
      ledger: [
        finding({
          id: "r1-1",
          claim: "claim one",
          result: "confirmed",
          closures: [closure(1, "open", "no fix yet"), closure(2, "closed"), closure(3, "stuck", "still broken")],
        }),
      ],
    }),
  );
  assert.deepEqual(view?.findings[0].closures, [
    { text: "round 1 · open · no fix yet", tone: "attention" },
    { text: "round 2 · closed", tone: "ok" },
    { text: "round 3 · stuck · still broken", tone: "attention" },
  ]);
});

test("ReleaseTable: reached only with release steps and a phase or a step that puts it in view", () => {
  const waiting = [proof(1, "tag", "waiting", "none")];
  assert.equal(releaseOf(epicIn("accepted", { releaseSteps: waiting })), null);
  assert.equal(releaseOf(epicIn("reviewing", { releaseSteps: waiting })), null);
  assert.equal(releaseOf(epicIn("clean", { releaseSteps: [] })), null);
  assert.notEqual(releaseOf(epicIn("clean", { releaseSteps: waiting })), null);
  assert.notEqual(releaseOf(epicIn("reviewing", { releaseSteps: [proof(1, "tag", "running", "none")] })), null);
  assert.notEqual(
    releaseOf(epicIn("reviewing", { releaseSteps: waiting, releases: [run("release", 1, 1, true)] })),
    null,
  );
});

test("ReleaseTable: each state has its word and tone, and an unknown state is shown as served", () => {
  const view = releaseOf(
    epicIn("releasing", {
      releaseSteps: [
        proof(1, "a", "waiting", "none"),
        proof(2, "b", "running", "none"),
        proof(3, "c", "passed", "none"),
        proof(4, "d", "failed", "none", { reason: "push refused" }),
        proof(5, "e", "blocked", "none"),
      ],
    }),
  );
  assert.deepEqual(
    view?.rows.map(({ state, tone }) => ({ state, tone })),
    [
      { state: "waiting", tone: "none" },
      { state: "running", tone: "live" },
      { state: "passed", tone: "ok" },
      { state: "failed", tone: "bad" },
      { state: "blocked", tone: "none" },
    ],
  );
});

test("ReleaseTable: the count words name the running step, the stopped step, or the steps passed", () => {
  const steps = (...states: string[]) => states.map((state, index) => proof(index + 1, `s${index + 1}`, state, "none"));
  assert.equal(releaseOf(epicIn("releasing", { releaseSteps: steps("passed", "running", "waiting") }))?.words, "step 2 of 3");
  assert.equal(releaseOf(epicIn("releasing", { releaseSteps: steps("passed", "waiting", "running") }))?.words, "step 3 of 3");
  assert.equal(releaseOf(epicIn("releasing", { releaseSteps: steps("passed", "passed", "passed") }))?.words, "step 3 of 3");
  assert.equal(releaseOf(epicIn("stopped", { releaseSteps: steps("passed", "failed", "waiting") }))?.words, "stopped at step 2 of 3");
  assert.equal(releaseOf(epicIn("released", { releaseSteps: steps("passed", "passed", "waiting") }))?.words, "2 of 3 steps passed");
  assert.equal(releaseOf(epicIn("done", { releaseSteps: steps("passed", "passed") }))?.words, "2 of 2 steps passed");
});

test("ReleaseTable: a step keeps its text and its target apart, its undo is not given when empty, and its reason shows only when it failed", () => {
  const view = releaseOf(
    epicIn("stopped", {
      releaseSteps: [
        proof(1, "tag the commit", "passed", "", { target: "origin" }),
        proof(2, "push the branch", "failed", "delete the branch", { target: "", reason: "push refused", evidence: "remote said no" }),
        proof(3, "announce", "waiting", "none", { reason: "not yet" }),
      ],
    }),
  );
  assert.deepEqual(
    view?.rows.map(({ position, step, target, undo, reason, evidence }) => ({ position, step, target, undo, reason, evidence })),
    [
      { position: 1, step: "tag the commit", target: "origin", undo: "not given", reason: "", evidence: "" },
      {
        position: 2,
        step: "push the branch",
        target: "",
        undo: "delete the branch",
        reason: "push refused",
        evidence: "remote said no",
      },
      { position: 3, step: "announce", target: "", undo: "none", reason: "", evidence: "" },
    ],
  );
});

test("ReleaseTable: each step's runs are its release and named runs at its position, one line per attempt", () => {
  const view = releaseOf(
    epicIn("stopped", {
      releaseSteps: [proof(1, "tag", "passed", "none"), proof(2, "push", "failed", "none")],
      releases: [
        run("candidate", 1, 1, true),
        run("release", 1, 1, true),
        run("named", 2, 1, false, "push refused"),
        run("judge", 2, 1, true),
        run("release", 2, 2, false),
      ],
    }),
  );
  assert.deepEqual(
    view?.rows.map((row) => row.runs),
    [["attempt 1 · passed"], ["attempt 1 · failed · push refused", "attempt 2 · failed"]],
  );
});

test("ReleaseTable: the Then line is drawn only when the contract names one", () => {
  const steps = [proof(1, "tag", "waiting", "none")];
  const withThen = releaseOf(
    epicIn("clean", { releaseSteps: steps, contract: create(EpicContractSchema, { releaseThen: "announce it" }) }),
  );
  assert.equal(withThen?.then, "Then: announce it");
  assert.equal(releaseOf(epicIn("clean", { releaseSteps: steps }))?.then, "");
});

test("isUndoNone: only none, trimmed and ignoring case, is an undo that cannot be taken back", () => {
  assert.equal(isUndoNone("none"), true);
  assert.equal(isUndoNone("  NONE "), true);
  assert.equal(isUndoNone("none once someone has fetched the tag"), false);
  assert.equal(isUndoNone("revert the tag"), false);
  assert.equal(isUndoNone(""), false);
});

test("ReleaseFrom: offered for the first unpassed step when it failed with no undo in a stopped epic", () => {
  const view = releaseOf(
    epicIn("stopped", {
      releaseSteps: [
        proof(1, "tag", "passed", "none"),
        proof(2, "push", "failed", "none", { reason: "push refused" }),
        proof(3, "announce", "waiting", "none"),
      ],
    }),
  );
  assert.equal(view?.offer, 2);
});

test("ReleaseFrom: no offer for a step that has an undo, or whose undo is none once someone has fetched the tag", () => {
  const withUndo = releaseOf(epicIn("stopped", { releaseSteps: [proof(1, "push", "failed", "revert the push")] }));
  assert.equal(withUndo?.offer, 0);
  const fetched = releaseOf(
    epicIn("stopped", { releaseSteps: [proof(1, "push", "failed", "none once someone has fetched the tag")] }),
  );
  assert.equal(fetched?.offer, 0);
});

test("ReleaseFrom: no offer for a waiting or running first unpassed step, and never for a passed step", () => {
  const waiting = releaseOf(epicIn("stopped", { releaseSteps: [proof(1, "push", "waiting", "none")] }));
  assert.equal(waiting?.offer, 0);
  const running = releaseOf(epicIn("stopped", { releaseSteps: [proof(1, "push", "running", "none")] }));
  assert.equal(running?.offer, 0);
  const passedOnly = releaseOf(
    epicIn("stopped", { releaseSteps: [proof(1, "tag", "passed", "none"), proof(2, "push", "waiting", "none")] }),
  );
  assert.equal(passedOnly?.offer, 0);
});

test("ReleaseFrom: no other step is offered, even one that failed with no undo, when the first unpassed step has an undo", () => {
  const view = releaseOf(
    epicIn("stopped", {
      releaseSteps: [proof(1, "push", "failed", "revert the push"), proof(2, "announce", "failed", "none")],
    }),
  );
  assert.equal(view?.offer, 0);
});

test("ReleaseFrom: no offer outside the stopped phase", () => {
  const steps = [proof(1, "push", "failed", "none")];
  assert.equal(releaseOf(epicIn("releasing", { releaseSteps: steps }))?.offer, 0);
  assert.equal(releaseOf(epicIn("released", { releaseSteps: steps }))?.offer, 0);
});

test("EpicReport: each outcome has its sentence when the report is set", () => {
  const sentences: [string, string][] = [
    ["released", "Released."],
    ["done", "Done."],
    ["met_at_base", "Already met at its base."],
    ["ended_with_gaps", "Ended with gaps. It did not release."],
    ["closed_superseded", "Closed as superseded."],
    ["closed_shipped", "Closed as shipped outside orrery."],
    ["closed_abandoned", "Closed as abandoned."],
    ["paused", "Ended: paused."],
  ];
  for (const [outcome, sentence] of sentences) {
    const view = reportOf(epicIn(outcome, { report: create(EpicReportSchema, { outcome }) }));
    assert.equal(view?.sentence, sentence, outcome);
  }
});

test("EpicReport: a closed epic with no report is reached and words its sentence from the phase", () => {
  const view = reportOf(epicIn("closed_abandoned"));
  assert.deepEqual(view, {
    sentence: "Closed as abandoned.",
    why: "",
    shipped: "",
    notShipped: "",
    replacedBy: "",
    gaps: false,
  });
});

test("EpicReport: an epic with no report that is not closed is not reached", () => {
  assert.equal(reportOf(epicIn("released")), null);
  assert.equal(reportOf(epicIn("stopped")), null);
});

test("EpicReport: shipped and not shipped name their requirements, and an empty shipped is none", () => {
  const set = (report: MessageInitShape<typeof EpicReportSchema>) =>
    reportOf(epicIn("released", { report: create(EpicReportSchema, { outcome: "released", ...report }) }));
  const full = set({ shipped: ["R1", "R2"], notShipped: ["R3"], why: "R3 waits on the vendor." });
  assert.equal(full?.shipped, "R1, R2");
  assert.equal(full?.notShipped, "R3");
  assert.equal(full?.why, "R3 waits on the vendor.");
  const none = set({ shipped: [], notShipped: [] });
  assert.equal(none?.shipped, "none");
  assert.equal(none?.notShipped, "");
});

test("EpicReport: replacedBy is the slug of the epic that superseded this one", () => {
  const withId = reportOf(epicIn("closed_superseded", { supersededBy: "repo-b/beta" }));
  assert.equal(withId?.replacedBy, "beta");
  const bare = reportOf(epicIn("closed_superseded", { supersededBy: "gamma" }));
  assert.equal(bare?.replacedBy, "gamma");
  const none = reportOf(epicIn("closed_superseded"));
  assert.equal(none?.replacedBy, "");
});

test("EpicReport: gaps is true only when the phase is ended_with_gaps", () => {
  const gaps = reportOf(epicIn("ended_with_gaps", { report: create(EpicReportSchema, { outcome: "ended_with_gaps" }) }));
  assert.equal(gaps?.gaps, true);
  const released = reportOf(epicIn("released", { report: create(EpicReportSchema, { outcome: "released" }) }));
  assert.equal(released?.gaps, false);
});

test("EpicClose: replacements are the other epics on the same path, in listed order", () => {
  const detail = epicIn("closed_superseded", {}, "/work/alpha");
  const listed = [
    epicListed("repo-a/alpha", "alpha", "/work/alpha"),
    epicListed("repo-a/zeta", "zeta", "/work/alpha"),
    epicListed("repo-b/other", "other", "/work/beta"),
    epicListed("repo-a/beta", "beta", "/work/alpha"),
  ];
  assert.deepEqual(replacementsOf(detail, listed), [
    { id: "repo-a/zeta", slug: "zeta" },
    { id: "repo-a/beta", slug: "beta" },
  ]);
});

test("EpicClose: no replacements when this epic's path is empty", () => {
  const detail = epicIn("closed_superseded", {}, "");
  const listed = [epicListed("repo-a/zeta", "zeta", "")];
  assert.deepEqual(replacementsOf(detail, listed), []);
});
