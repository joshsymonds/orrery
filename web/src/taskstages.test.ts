import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicBuildTaskSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  EpicTaskFindingSchema,
  EpicTaskReviewSchema,
  EvidenceRunSchema,
  type EpicsGetResponse,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import { recordTasksOf } from "./record";
import { stagesOf, type StageRow, type TaskStages } from "./taskstages";

type StepInit = MessageInitShape<typeof EpicStepSchema>;
type RunInit = MessageInitShape<typeof EvidenceRunSchema>;
type ReviewInit = MessageInitShape<typeof EpicTaskReviewSchema>;
type BuildInit = MessageInitShape<typeof EpicBuildTaskSchema>;

// The instant every test reads the clock at, in unix seconds.
const now = 2000;

// A labelled step of task T1 that runs as an implementer in stage turn from second 100, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "T1",
    agent: "builder",
    role: "implementer",
    readOnly: false,
    doing: "",
    held: false,
    end: "",
    stage: "turn",
    started: 100n,
    ...init,
  });
}

// A read-only review step of task T1 that runs from its start to its end.
function reviewStep(started: bigint, ended: bigint): EpicStep {
  return step({ role: "reviewer", readOnly: true, end: "done", started, ended });
}

// The ring the server stands an open task at for each build stage, as the fixtures here give it.
const stagePlaces: Record<string, string> = {
  red: "red",
  implement: "implement",
  orchestrate: "implement",
  green: "green",
  review: "review",
  verify: "review",
};

// The stages of task T1 in a Get response with the build task at the given status and stage, standing where place and
// tone say (by default at its stage's ring, running, or planned with no stage), and the given steps and build records,
// read at now.
function stagesFor(
  init: {
    status?: string;
    stage?: string;
    place?: string;
    tone?: string;
    steps?: StepInit[];
    red?: RunInit;
    green?: RunInit;
    reviews?: ReviewInit[];
  } = {},
): TaskStages {
  const place = init.place ?? stagePlaces[init.stage ?? ""] ?? "planned";
  const build: BuildInit = {
    id: "T1",
    title: "Parser",
    status: init.status ?? "open",
    stage: init.stage ?? "",
    place,
    tone: init.tone ?? (place === "planned" ? "plan" : "run"),
    requirements: ["R1"],
    red: init.red,
    green: init.green,
    reviews: init.reviews ?? [],
  };
  const detail: EpicsGetResponse = create(EpicsGetResponseSchema, {
    buildTasks: [build],
    steps: (init.steps ?? []).map(step),
  });
  const [task] = recordTasksOf(detail);
  return stagesOf(detail, task, now);
}

// The summaries of the rows, in row order.
function summariesOf(rows: StageRow[]): string[] {
  return rows.map((row) => row.summary);
}

// The states of the rows, in row order, as words.
function statesOf(stages: TaskStages): string {
  return stages.rows.map((row) => row.state).join(" ");
}

// The summary of the one row of a stage.
function summaryOfStage(stages: TaskStages, stage: string): string {
  const row = stages.rows.find((candidate) => candidate.stage === stage);
  assert.notEqual(row, undefined, stage);
  return row?.summary ?? "";
}

test("TaskStages: the five rows are red, implement, green, review and integrate, in that order", () => {
  const stages = stagesFor();
  assert.equal(stages.rows.map((row) => row.stage).join(" "), "red implement green review integrate");
});

test("TaskStages: a planned task has five ahead rows that read not reached, and opens no row", () => {
  const stages = stagesFor();
  assert.equal(statesOf(stages), "ahead ahead ahead ahead ahead");
  assert.deepEqual(summariesOf(stages.rows), Array(5).fill("not reached"));
  assert.ok(stages.rows.every((row) => row.end === "" && row.tone === undefined));
  assert.equal(stages.open, undefined);
});

test("TaskStages: an integrated task has five passed rows, opens integrate, and its integrate row reads merged", () => {
  const stages = stagesFor({ status: "done", place: "integrated", tone: "ok" });
  assert.equal(statesOf(stages), "passed passed passed passed passed");
  assert.equal(summaryOfStage(stages, "integrate"), "merged");
  assert.equal(stages.open, "integrate");
});

test("TaskStages: at each of the five places the rows before it are passed, it is now, and the rows after are ahead", () => {
  const cases: [Parameters<typeof stagesFor>[0], string, string | undefined][] = [
    [{ stage: "red" }, "now ahead ahead ahead ahead", "red"],
    [{ stage: "implement" }, "passed now ahead ahead ahead", "implement"],
    [{ stage: "green" }, "passed passed now ahead ahead", "green"],
    [{ stage: "review" }, "passed passed passed now ahead", "review"],
    [{ status: "done", place: "integrate", tone: "acc" }, "passed passed passed passed now", "integrate"],
  ];
  for (const [init, states, open] of cases) {
    const stages = stagesFor(init);
    assert.equal(statesOf(stages), states, JSON.stringify(init));
    assert.equal(stages.open, open, JSON.stringify(init));
  }
});

test("TaskStages: the now row takes the task's tone: run live, mine held, limited grey, bad failed, acc accepted", () => {
  const cases: [Parameters<typeof stagesFor>[0], string][] = [
    [{ place: "implement", tone: "run", steps: [step({})] }, "run"],
    [{ place: "implement", tone: "mine", steps: [step({ held: true, stage: "held" })] }, "mine"],
    [{ place: "implement", tone: "limited", steps: [step({ limited: true, stage: "limited" })] }, "limited"],
    [{ status: "gap", place: "implement", tone: "bad", steps: [step({ end: "gate_failed", ended: 200n })] }, "bad"],
    [{ status: "done", place: "integrate", tone: "acc" }, "acc"],
  ];
  for (const [init, tone] of cases) {
    const stages = stagesFor(init);
    const current = stages.rows.find((row) => row.state === "now");
    assert.equal(current?.tone, tone);
    assert.ok(stages.rows.filter((row) => row.state !== "now").every((row) => row.tone === undefined));
  }
});

test("TaskStages: the now row's clock is the running step's elapsed time since its stage start, else since its start", () => {
  const withStage = stagesFor({ stage: "implement", steps: [step({ started: 1000n, stageStarted: 1800n })] });
  assert.equal(withStage.rows[1].end, "3:20");

  const withoutStage = stagesFor({ stage: "implement", steps: [step({ started: 1500n })] });
  assert.equal(withoutStage.rows[1].end, "8:20");
});

test("TaskStages: a now row with no running step has no clock, as a task between attempts has none", () => {
  const stages = stagesFor({ stage: "implement", steps: [step({ end: "gate_failed", started: 100n, ended: 200n })] });
  const now = stages.rows.find((row) => row.state === "now");
  assert.equal(now?.stage, "implement");
  assert.equal(now?.end, "");
});

test("TaskStages: a passed row shows its run's or its steps' duration, and a zero or missing duration shows none", () => {
  const stages = stagesFor({
    status: "done",
    place: "integrated",
    tone: "ok",
    red: { id: "red-1", phase: "red", state: "ended", passed: true, verdict: "holds", created: 100n, ended: 220n },
    green: { id: "green-1", phase: "green", state: "ended", passed: true, verdict: "holds", created: 500n, ended: 500n },
    steps: [
      step({ started: 400n, end: "done", ended: 700n }),
      step({ started: 800n, end: "done", ended: 1000n }),
      reviewStep(1100n, 1130n),
    ],
  });
  assert.equal(stages.rows[0].end, "2 min");
  assert.equal(stages.rows[1].end, "10 min");
  assert.equal(stages.rows[2].end, "");
  assert.equal(stages.rows[3].end, "30 s");
  assert.equal(stages.rows[4].end, "");
});

test("TaskStages: the red row says what the red run is doing, and judged to hold follows only an ended run with a holds verdict", () => {
  const cases: [RunInit | undefined, string][] = [
    [undefined, "capturing the failing test"],
    [{ id: "r", phase: "red", state: "capturing" }, "capturing the failing test"],
    [{ id: "r", phase: "red", state: "judging" }, "a judge is reading the red run"],
    [
      { id: "r", phase: "red", state: "ended", passed: true, verdict: "holds", created: 100n, ended: 150n },
      "the test fails before any change · judged to hold",
    ],
    [
      { id: "r", phase: "red", state: "ended", passed: true, verdict: "fails", created: 100n, ended: 150n },
      "the test fails before any change",
    ],
    [
      { id: "r", phase: "red", state: "ended", passed: false, verdict: "", created: 100n, ended: 150n },
      "the test passes before any change",
    ],
  ];
  for (const [red, summary] of cases) {
    assert.equal(summaryOfStage(stagesFor({ stage: "green", red }), "red"), summary);
  }
});

test("TaskStages: the green row says what the green run is doing, and judged to hold follows only an ended run with a holds verdict", () => {
  const cases: [RunInit | undefined, string][] = [
    [undefined, "capturing the green run"],
    [{ id: "g", phase: "green", state: "judging" }, "a judge is reading the green run"],
    [
      { id: "g", phase: "green", state: "ended", passed: true, verdict: "holds", created: 300n, ended: 330n },
      "the test passes on the change · judged to hold",
    ],
    [
      { id: "g", phase: "green", state: "ended", passed: false, verdict: "fails", created: 300n, ended: 330n },
      "the test still fails on the change",
    ],
  ];
  for (const [green, summary] of cases) {
    assert.equal(summaryOfStage(stagesFor({ stage: "review", green }), "green"), summary);
  }
});

test("TaskStages: the implement row reads not started, or an orchestrator's decision, before any attempt runs", () => {
  assert.equal(summaryOfStage(stagesFor({ stage: "implement" }), "implement"), "not started");
  assert.equal(
    summaryOfStage(stagesFor({ stage: "orchestrate" }), "implement"),
    "an orchestrator is deciding on the implementer’s request",
  );
});

test("TaskStages: the implement row counts its attempts and says what the latest one is doing", () => {
  const cases: [StepInit[], string][] = [
    [[step({ stage: "turn" })], "attempt 1 · the worker is writing"],
    [[step({ stage: "turn" }), step({ stage: "turn", started: 200n })], "attempt 2 · the worker is writing"],
    [
      [step({ stage: "checks", checks: [{ round: 1, name: "vet", ok: true }, { round: 1, name: "lint", ok: false }] })],
      "attempt 1 · round 1 failed · the checks are running",
    ],
    [
      [step({ stage: "gate", checks: [{ round: 2, name: "vet", ok: true }, { round: 1, name: "lint", ok: false }] })],
      "attempt 1 · round 2 passed · the gate is running",
    ],
    [[step({ stage: "judge" })], "attempt 1 · a judge is reading the change"],
    [[step({ stage: "held", held: true })], "attempt 1 · held"],
    [[step({ stage: "limited", limited: true })], "attempt 1 · waits on a usage limit"],
  ];
  for (const [steps, summary] of cases) {
    assert.equal(summaryOfStage(stagesFor({ stage: "implement", steps }), "implement"), summary, summary);
  }
});

test("TaskStages: a finished implement row says whether its gate passed and whether the judge met the goal, else how it ended", () => {
  const met = stagesFor({
    stage: "review",
    steps: [
      step({
        end: "done",
        ended: 300n,
        checks: [{ round: 1, name: "vet", ok: false }, { round: 1, name: "gate", ok: true }],
        verdicts: [{ round: 1, verdict: "met" }],
      }),
    ],
  });
  assert.equal(summaryOfStage(met, "implement"), "attempt 1 · the gate passed · judged met");

  const unmet = stagesFor({
    stage: "review",
    steps: [
      step({ end: "done", ended: 300n, checks: [{ round: 1, name: "gate", ok: false }], verdicts: [{ round: 1, verdict: "unmet" }] }),
    ],
  });
  assert.equal(summaryOfStage(unmet, "implement"), "attempt 1");

  const ended = stagesFor({ stage: "review", steps: [step({ end: "gate_failed", ended: 300n })] });
  assert.equal(summaryOfStage(ended, "implement"), "attempt 1 · ended: gate_failed");
});

test("TaskStages: the implement row's time runs from its first attempt's start to its last attempt's end", () => {
  const stages = stagesFor({
    status: "done",
    place: "integrated",
    tone: "ok",
    steps: [step({ started: 400n, end: "failed", ended: 500n }), step({ started: 800n, end: "done", ended: 1000n })],
  });
  assert.equal(stages.rows[1].end, "10 min");
});

test("TaskStages: the review row names a verifier's open findings in verify, else each review's gate and its findings", () => {
  const finding = (result: string) => create(EpicTaskFindingSchema, { id: "F", claim: "c", location: "l", result });

  const open = stagesFor({
    stage: "verify",
    reviews: [{ attempt: 1n, gate: "review", verdict: "fails", findings: [finding(""), finding("")] }],
  });
  assert.equal(summaryOfStage(open, "review"), "a verifier is checking 2 findings");

  const single = stagesFor({
    stage: "verify",
    reviews: [{ attempt: 1n, gate: "review", verdict: "fails", findings: [finding("")] }],
  });
  assert.equal(summaryOfStage(single, "review"), "a verifier is checking 1 finding");

  const settled = stagesFor({
    stage: "verify",
    reviews: [{ attempt: 1n, gate: "review", verdict: "fails", findings: [finding("confirmed")] }],
  });
  assert.equal(summaryOfStage(settled, "review"), "a verifier is checking its findings");

  const reviewed = stagesFor({
    stage: "review",
    reviews: [
      { attempt: 1n, gate: "review", verdict: "holds", findings: [] },
      { attempt: 2n, gate: "review", verdict: "fails", findings: [finding("confirmed"), finding("dropped")] },
    ],
  });
  assert.equal(summaryOfStage(reviewed, "review"), "review holds · review: 2 findings, 1 confirmed");

  const singleFinding = stagesFor({
    stage: "review",
    reviews: [{ attempt: 1n, gate: "review", verdict: "fails", findings: [finding("dropped")] }],
  });
  assert.equal(summaryOfStage(singleFinding, "review"), "review: 1 finding");

  const none = stagesFor({ stage: "review" });
  assert.equal(summaryOfStage(none, "review"), "the review gates are reading the change");
});

test("TaskStages: the review row's time runs from its first read-only step's start to its last one's end", () => {
  const stages = stagesFor({
    status: "done",
    place: "integrated",
    tone: "ok",
    steps: [reviewStep(1100n, 1130n), reviewStep(1200n, 1240n)],
  });
  assert.equal(stages.rows[3].end, "2 min");
});

test("TaskStages: the integrate row reads accepted and waiting for its merge while the task is accepted", () => {
  const stages = stagesFor({ status: "done", stage: "review", place: "integrate", tone: "acc" });
  assert.equal(summaryOfStage(stages, "integrate"), "accepted · waits for its merge");
  assert.equal(stages.open, "integrate");
});

test("TaskStages: an ahead row reads not reached, whichever stage the task is at", () => {
  const stages = stagesFor({ stage: "green" });
  assert.equal(summaryOfStage(stages, "review"), "not reached");
  assert.equal(summaryOfStage(stages, "integrate"), "not reached");
});
