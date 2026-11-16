import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicStepSchema, EpicsGetResponseSchema, type EpicStep, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { phaseOutputOf } from "./phaserows";

type StepInit = MessageInitShape<typeof EpicStepSchema>;

// A labelled step of task T1 that runs in stage turn from second 100, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "T1",
    agent: "builder",
    doing: "",
    held: false,
    end: "",
    stage: "turn",
    started: 100n,
    ...init,
  });
}

// A Get response with build tasks T1 and T2, each with its own red run, review and requirements, and the given steps.
function buildOf(steps: EpicStep[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    buildTasks: [
      {
        id: "T1",
        title: "Parser",
        status: "open",
        stage: "green",
        requirements: ["R1"],
        red: { id: "red-1" },
        green: { id: "green-1" },
        reviews: [{ attempt: 1n, gate: "review", step: "rev1", verdict: "holds" }],
      },
      {
        id: "T2",
        title: "Printer",
        status: "open",
        stage: "implement",
        requirements: ["R2", "R3"],
        red: { id: "red-2" },
        reviews: [{ attempt: 1n, gate: "review", step: "rev2", verdict: "fails" }],
      },
    ],
    steps,
  });
}

test("a task's output names only its own steps, runs, reviews and rounds, with two tasks' steps interleaved", () => {
  const detail = buildOf([
    step({
      id: "a1",
      task: "T1",
      end: "done",
      started: 100n,
      ended: 110n,
      summary: "t1 wrote",
      checks: [{ round: 1, name: "checks", ok: true, detail: "t1 round one" }],
    }),
    step({
      id: "b1",
      task: "T2",
      end: "returned",
      started: 115n,
      ended: 120n,
      reason: "lint",
      summary: "t2 wrote",
      checks: [{ round: 2, name: "checks", ok: false, detail: "t2 round two" }],
    }),
    step({
      id: "a2",
      task: "T1",
      end: "stopped",
      held: true,
      started: 125n,
      ended: 128n,
      reason: "owner held",
      summary: "t1 second",
      checks: [{ round: 3, name: "checks", ok: false, detail: "t1 round three" }],
    }),
    step({ id: "r1", task: "T1", readOnly: true, end: "returned", started: 130n, ended: 140n, summary: "t1 reviewed" }),
    step({ id: "b2", task: "T2", readOnly: true, end: "", started: 141n, doing: "t2 reviewing" }),
  ]);

  const first = phaseOutputOf(detail, "T1");
  assert.ok(first);
  assert.equal(first.task, "T1");
  assert.equal(first.title, "Parser");
  assert.equal(first.requirements.join(","), "R1");
  assert.equal(first.attempts.map((attempt) => attempt.step).join(","), "a1,a2");
  assert.equal(first.attempts[1].end, "stopped");
  assert.equal(first.attempts[1].reason, "owner held");
  assert.equal(first.attempts[1].held, true);
  assert.equal(first.reviewers.map((attempt) => attempt.step).join(","), "r1");
  assert.equal(first.reviewers[0].summary, "t1 reviewed");
  assert.equal(first.reviews.map((review) => review.step).join(","), "rev1");
  assert.equal(first.reviews[0].verdict, "holds");
  assert.equal(first.red?.id, "red-1");
  assert.equal(first.green?.id, "green-1");
  assert.equal(first.rounds.map((round) => `${round.step}:${round.round}`).join(","), "a1:1,a2:3");
  assert.equal(first.running === undefined, true);
  assert.equal(first.integrated, false);

  const second = phaseOutputOf(detail, "T2");
  assert.ok(second);
  assert.equal(second.task, "T2");
  assert.equal(second.title, "Printer");
  assert.equal(second.requirements.join(","), "R2,R3");
  assert.equal(second.attempts.map((attempt) => attempt.step).join(","), "b1");
  assert.equal(second.attempts[0].reason, "lint");
  assert.equal(second.reviewers.map((attempt) => attempt.step).join(","), "b2");
  assert.equal(second.reviewers[0].doing, "t2 reviewing");
  assert.equal(second.reviews.map((review) => review.step).join(","), "rev2");
  assert.equal(second.reviews[0].verdict, "fails");
  assert.equal(second.red?.id, "red-2");
  assert.equal(second.green === undefined, true);
  assert.equal(second.rounds.map((round) => `${round.step}:${round.round}`).join(","), "b1:2");
  assert.equal(second.rounds[0].ran.join(","), "checks");
  assert.equal(second.rounds[0].gate === null, true);
  assert.equal(second.running?.id, "b2");
});

test("checks in rounds 1 and 2 with a verdict only in round 2 give each round its own checks, gate, judge and ran", () => {
  const detail = buildOf([
    step({
      id: "c1",
      task: "T1",
      gateName: "make-gate",
      end: "done",
      started: 100n,
      ended: 160n,
      checks: [
        { round: 1, name: "checks", ok: true, detail: "round one checks" },
        { round: 1, name: "gate", ok: false, detail: "round one gate", tailStdout: "one out", tailStderr: "one err" },
        { round: 2, name: "checks", ok: true, detail: "round two checks" },
        { round: 2, name: "gate", ok: true, detail: "round two gate", tailStdout: "two out", tailStderr: "two err" },
      ],
      verdicts: [{ round: 2, verdict: "met", reason: "round two reason" }],
    }),
  ]);

  const output = phaseOutputOf(detail, "T1");
  assert.ok(output);
  assert.equal(String(output.rounds.length), "2");
  const [one, two] = output.rounds;

  assert.equal(one.step, "c1");
  assert.equal(String(one.round), "1");
  assert.equal(one.ran.join(","), "checks,gate");
  assert.equal(String(one.checks.length), "1");
  assert.equal(one.checks[0].name, "checks");
  assert.equal(one.checks[0].ok, true);
  assert.equal(one.checks[0].detail, "round one checks");
  assert.equal(one.gate?.name, "make-gate");
  assert.equal(one.gate?.ok, false);
  assert.equal(one.gate?.detail, "round one gate");
  assert.equal(one.gate?.stdout, "one out");
  assert.equal(one.gate?.stderr, "one err");
  assert.equal(one.judge === null, true);

  assert.equal(two.step, "c1");
  assert.equal(String(two.round), "2");
  assert.equal(two.ran.join(","), "checks,gate,judge");
  assert.equal(String(two.checks.length), "1");
  assert.equal(two.checks[0].detail, "round two checks");
  assert.equal(two.gate?.ok, true);
  assert.equal(two.gate?.detail, "round two gate");
  assert.equal(two.gate?.stdout, "two out");
  assert.equal(two.gate?.stderr, "two err");
  assert.equal(two.judge?.verdict, "met");
  assert.equal(two.judge?.reason, "round two reason");
});

test("a task's rows give each phase its state, its latest session, and the running step's stage start, in phase order", () => {
  const detail = buildOf([
    step({ id: "w1", task: "T1", end: "done", started: 100n, ended: 110n, summary: "wrote" }),
    step({ id: "r1", task: "T1", readOnly: true, end: "returned", started: 115n, ended: 130n, summary: "reviewed" }),
    step({ id: "w2", task: "T1", stage: "checks", started: 140n, stageStarted: 145n, doing: "running checks" }),
  ]);

  const output = phaseOutputOf(detail, "T1");
  assert.ok(output);
  assert.equal(output.rows.map((row) => row.phase).join(","), "implementing,checking,reviewing,integrating");
  assert.equal(output.rows.map((row) => row.state).join(" "), "passed now ahead ahead");
  assert.equal(output.rows.map((row) => row.session).join(","), "w2,w2,r1,");
  assert.equal(output.rows[0].summary, "wrote");
  assert.equal(String(output.rows[0].seconds), "10");
  assert.equal(output.rows[1].summary, "running checks");
  assert.equal(String(output.rows[1].since), "145");
  assert.equal(output.running?.id, "w2");
  assert.equal(output.integrated, false);
});

test("a record task has no red, green or reviews, and its output reads its own steps", () => {
  const detail = create(EpicsGetResponseSchema, {
    tasks: [
      { effort: 1, slug: "first", subject: "First task", status: "done", requirements: ["R2"] },
      { effort: 1, slug: "second", subject: "Second", status: "pending", requirements: [] },
    ],
    steps: [
      step({ id: "x1", task: "first", end: "done", started: 100n, ended: 110n, summary: "shipped" }),
      step({ id: "y1", task: "second", end: "", stage: "turn" }),
    ],
  });

  const output = phaseOutputOf(detail, "first");
  assert.ok(output);
  assert.equal(output.task, "first");
  assert.equal(output.title, "First task");
  assert.equal(output.red === undefined, true);
  assert.equal(output.green === undefined, true);
  assert.equal(String(output.reviews.length), "0");
  assert.equal(String(output.reviewers.length), "0");
  assert.equal(output.attempts.map((attempt) => attempt.step).join(","), "x1");
  assert.equal(output.integrated, true);
  assert.equal(output.running === undefined, true);
  assert.equal(output.rows[3].session, "");
});

test("an unknown task gives no output", () => {
  const detail = buildOf([step({ id: "a1", task: "T1", end: "done", ended: 110n })]);
  assert.equal(phaseOutputOf(detail, "T9") === null, true);
});
