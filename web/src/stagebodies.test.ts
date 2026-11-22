import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicBuildTaskSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  type EpicStep,
  type EpicsGetResponse,
} from "./gen/orrery/v1/epics_pb";
import { stageBodiesOf } from "./stagebodies";

type StepInit = MessageInitShape<typeof EpicStepSchema>;
type BuildInit = MessageInitShape<typeof EpicBuildTaskSchema>;

// An implementer step of task T1 that runs in stage turn from second 100, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "T1",
    agent: "sonnet",
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

// A Get answer with build task T1, "Parser", at stage implement, unless the build init says otherwise, and the steps.
function detailOf(steps: EpicStep[], build: Partial<Omit<BuildInit, "$typeName">> = {}): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "implement", requirements: ["R1"], ...build }],
    steps,
  });
}

test("TaskStages: red shows the task's red run, and a quiet line when the build names none", () => {
  const withRed = detailOf([], { red: { id: "red-1", phase: "red", state: "ended", passed: true, verdict: "holds" } });
  assert.deepEqual(stageBodiesOf(withRed, "T1")?.red, { kind: "run", run: withRed.buildTasks[0].red });
  assert.deepEqual(stageBodiesOf(detailOf([]), "T1")?.red, {
    kind: "none",
    line: { text: "No red run yet.", tone: "quiet" },
  });
});

test("TaskStages: implement lists the task's implementer steps in order with each chip, and no other task's or reviewer's", () => {
  const detail = detailOf([
    step({ id: "a1", end: "done", summary: "Wrote the parser.", started: 100n, ended: 110n }),
    step({ id: "r1", agent: "opus", role: "reviewer", readOnly: true, end: "done", started: 112n, ended: 113n }),
    step({ id: "a2", end: "looping", reason: "Looped on one edit.", started: 115n, ended: 120n }),
    step({ id: "a3", end: "", held: true, reason: "Paused for a question.", doing: "asks", started: 121n }),
    step({ id: "a4", end: "", doing: "Editing the parser.", started: 122n }),
    step({ id: "t2", task: "T2", end: "done", summary: "Other task.", started: 123n, ended: 124n }),
  ]);
  assert.deepEqual(stageBodiesOf(detail, "T1")?.implement.attempts, [
    { step: "a1", attempt: 1, words: "Wrote the parser.", chip: { text: "done", tone: "ok" } },
    { step: "a2", attempt: 2, words: "Looped on one edit.", chip: { text: "spent", tone: "bad" } },
    { step: "a3", attempt: 3, words: "Paused for a question.", chip: { text: "held", tone: "attention" } },
    { step: "a4", attempt: 4, words: "Editing the parser.", chip: { text: "running", tone: "live" } },
  ]);
});

test("TaskStages: an attempt's words are its summary once it ended, else its reason, else its doing", () => {
  const detail = detailOf([
    step({ id: "ended-summary", end: "done", summary: "Done words.", reason: "ignored", doing: "ignored", started: 100n }),
    step({ id: "ended-reason", end: "looping", summary: "", reason: "Reason words.", doing: "ignored", started: 101n }),
    step({ id: "running-summary", end: "", summary: "ignored while running", doing: "Doing words.", started: 102n }),
  ]);
  assert.deepEqual(
    stageBodiesOf(detail, "T1")?.implement.attempts.map((attempt) => attempt.words),
    ["Done words.", "Reason words.", "Doing words."],
  );
});

test("TaskStages: the latest attempt's rounds show each round's checks and its failed-check text, or All checks pass.", () => {
  const detail = detailOf([
    step({
      id: "a1",
      end: "looping",
      started: 100n,
      checks: [{ round: 9, name: "lint", ok: true }],
    }),
    step({
      id: "a2",
      end: "",
      started: 110n,
      checks: [
        { round: 1, name: "lint", ok: false, detail: "unused import" },
        { round: 1, name: "tests", ok: false, detail: "" },
        { round: 1, name: "gate", ok: false, detail: "exit 1" },
        { round: 2, name: "lint", ok: true, detail: "" },
        { round: 2, name: "tests", ok: true, detail: "" },
        { round: 2, name: "gate", ok: true, detail: "" },
      ],
    }),
  ]);
  assert.deepEqual(stageBodiesOf(detail, "T1")?.implement.latest?.rounds, [
    {
      round: 1,
      checks: [
        { name: "lint", ok: false },
        { name: "tests", ok: false },
        { name: "gate", ok: false },
      ],
      text: "lint: unused import\ntests failed\ngate: exit 1",
    },
    {
      round: 2,
      checks: [
        { name: "lint", ok: true },
        { name: "tests", ok: true },
        { name: "gate", ok: true },
      ],
      text: "All checks pass.",
    },
  ]);
});

test("TaskStages: the latest attempt's gate names the gate and logs its last round's tails, stdout then stderr, when set", () => {
  const both = detailOf([
    step({
      id: "a1",
      end: "",
      gateName: "just check",
      started: 100n,
      checks: [
        { round: 1, name: "gate", ok: false, detail: "exit 1", tailStdout: "FAIL", tailStderr: "" },
        { round: 2, name: "gate", ok: true, detail: "", tailStdout: "ok", tailStderr: "warn" },
      ],
    }),
  ]);
  assert.deepEqual(stageBodiesOf(both, "T1")?.implement.latest?.gate, { name: "just check", log: "ok\nwarn" });

  const stderrOnly = detailOf([
    step({
      id: "a1",
      end: "",
      gateName: "just check",
      started: 100n,
      checks: [{ round: 1, name: "gate", ok: false, detail: "exit 1", tailStdout: "", tailStderr: "error" }],
    }),
  ]);
  assert.deepEqual(stageBodiesOf(stderrOnly, "T1")?.implement.latest?.gate, { name: "just check", log: "error" });

  const noGate = detailOf([
    step({ id: "a1", end: "", gateName: "just check", started: 100n, checks: [{ round: 1, name: "lint", ok: true }] }),
  ]);
  assert.equal(stageBodiesOf(noGate, "T1")?.implement.latest?.gate, null);
});

test("TaskStages: the judge line is the latest attempt's last verdict, met, unmet with or without its reason, or quiet", () => {
  const judged = (verdicts: StepInit["verdicts"]) =>
    stageBodiesOf(detailOf([step({ id: "a1", end: "done", started: 100n, verdicts })]), "T1")?.implement.latest?.judge;

  assert.deepEqual(judged([{ round: 1, verdict: "met", reason: "" }]), {
    text: "Judged: the goal is met.",
    tone: "ok",
  });
  assert.deepEqual(judged([{ round: 1, verdict: "unmet", reason: "Parser ignores tabs." }]), {
    text: "Judged: unmet. Parser ignores tabs.",
    tone: "bad",
  });
  assert.deepEqual(judged([{ round: 1, verdict: "unmet", reason: "" }]), { text: "Judged: unmet.", tone: "bad" });
  assert.deepEqual(
    judged([
      { round: 1, verdict: "unmet", reason: "First." },
      { round: 2, verdict: "met", reason: "" },
    ]),
    { text: "Judged: the goal is met.", tone: "ok" },
  );
  assert.deepEqual(judged([]), { text: "The judge reads the change once the gate passes.", tone: "quiet" });
});

test("TaskStages: green's reproduction line says what the red test does on the change, and its run is the green run or a quiet line", () => {
  const greenOf = (green: BuildInit["green"]) => stageBodiesOf(detailOf([], { green }), "T1")?.green;

  assert.deepEqual(greenOf(undefined), {
    reproduction: { text: "The red test has not run on the change yet.", tone: "quiet" },
    run: { kind: "none", line: { text: "No green run yet.", tone: "quiet" } },
  });
  assert.deepEqual(stageBodiesOf(detailOf([], { green: { id: "g1", phase: "green", state: "capturing" } }), "T1")?.green.reproduction, {
    text: "The red test is running on the change.",
    tone: "quiet",
  });
  const passing = detailOf([], { green: { id: "g2", phase: "green", state: "ended", passed: true } });
  assert.deepEqual(stageBodiesOf(passing, "T1")?.green.reproduction, {
    text: "The red test, run on the change, now passes.",
    tone: "ok",
  });
  assert.deepEqual(stageBodiesOf(passing, "T1")?.green.run, { kind: "run", run: passing.buildTasks[0].green });
  const failing = detailOf([], { green: { id: "g3", phase: "green", state: "ended", passed: false } });
  assert.deepEqual(stageBodiesOf(failing, "T1")?.green.reproduction, {
    text: "The red test, run on the change, still fails.",
    tone: "bad",
  });
});

test("TaskStages: review rows give a no-findings review its verdict chip, with an undetermined review's reason after it", () => {
  const detail = detailOf(
    [
      step({ id: "rev1", role: "reviewer", readOnly: true, end: "done", started: 100n, ended: 105n }),
      step({ id: "rev2", role: "reviewer", readOnly: true, end: "done", started: 106n, ended: 110n }),
    ],
    {
      reviews: [
        { attempt: 1n, gate: "review", step: "rev1", verdict: "holds", findings: [] },
        {
          attempt: 1n,
          gate: "security",
          step: "rev2",
          verdict: "undetermined",
          reason: "The reviewer gave no verdict twice.",
          findings: [],
        },
      ],
    },
  );
  assert.deepEqual(stageBodiesOf(detail, "T1")?.review, {
    rows: [
      { gate: "review", what: "no findings", where: "", result: { text: "holds", tone: "ok" }, reason: "" },
      {
        gate: "security",
        what: "no findings",
        where: "",
        result: { text: "undetermined", tone: "neutral" },
        reason: "The reviewer gave no verdict twice.",
      },
    ],
    empty: null,
    session: "rev2",
  });
});

test("TaskStages: review rows give each finding its claim, location and result chip, the gate on its first row only", () => {
  const detail = detailOf([], {
    reviews: [
      {
        attempt: 1n,
        gate: "review",
        step: "rev1",
        verdict: "fails",
        findings: [
          { id: "F1", claim: "Export overwrites a file.", location: "cli/export.go:57", result: "confirmed", reason: "Ran it twice." },
          { id: "F2", claim: "Unused helper.", location: "cli/util.go:9", result: "dropped", reason: "A test uses it." },
          { id: "F3", claim: "Off by one.", location: "rota.go:12", result: "undecided", reason: "The verifier ran out of time." },
          { id: "F4", claim: "Race.", location: "rota.go:40", result: "undetermined", reason: "No reason." },
          { id: "F5", claim: "Leak.", location: "rota.go:77", result: "", reason: "" },
        ],
      },
    ],
  });
  assert.deepEqual(stageBodiesOf(detail, "T1")?.review.rows, [
    {
      gate: "review",
      what: "Export overwrites a file.",
      where: "cli/export.go:57",
      result: { text: "confirmed", tone: "bad" },
      reason: "Ran it twice.",
    },
    {
      gate: "",
      what: "Unused helper.",
      where: "cli/util.go:9",
      result: { text: "dropped", tone: "neutral" },
      reason: "A test uses it.",
    },
    {
      gate: "",
      what: "Off by one.",
      where: "rota.go:12",
      result: { text: "undecided", tone: "neutral" },
      reason: "The verifier ran out of time.",
    },
    {
      gate: "",
      what: "Race.",
      where: "rota.go:40",
      result: { text: "undecided", tone: "neutral" },
      reason: "No reason.",
    },
    {
      gate: "",
      what: "Leak.",
      where: "rota.go:77",
      result: { text: "verifying", tone: "live" },
      reason: "a verifier is checking it",
    },
  ]);
});

test("TaskStages: review rows read the gate with its attempt when the reviews span more than one attempt", () => {
  const detail = detailOf([], {
    reviews: [
      { attempt: 1n, gate: "review", step: "rev1", verdict: "holds", findings: [] },
      {
        attempt: 2n,
        gate: "review",
        step: "rev2",
        verdict: "fails",
        findings: [{ id: "F1", claim: "Claim.", location: "loc", step: "", result: "confirmed", reason: "Why." }],
      },
    ],
  });
  assert.deepEqual(stageBodiesOf(detail, "T1")?.review.rows, [
    { gate: "review · attempt 1", what: "no findings", where: "", result: { text: "holds", tone: "ok" }, reason: "" },
    {
      gate: "review · attempt 2",
      what: "Claim.",
      where: "loc",
      result: { text: "confirmed", tone: "bad" },
      reason: "Why.",
    },
  ]);
});

test("TaskStages: review says no review has run yet when the build task has none", () => {
  const body = stageBodiesOf(detailOf([]), "T1")?.review;
  assert.deepEqual(body, {
    rows: [],
    empty: { text: "No review has run yet.", tone: "quiet" },
    session: null,
  });
});

test("TaskStages: integrate says Merged. once the task is integrated, else that every gate took it and it waits", () => {
  assert.deepEqual(stageBodiesOf(detailOf([], { status: "done", place: "integrated", tone: "ok" }), "T1")?.integrate, {
    text: "Merged.",
    tone: "ok",
  });
  assert.deepEqual(stageBodiesOf(detailOf([], { status: "done", place: "integrate", tone: "acc" }), "T1")?.integrate, {
    text: "Every gate took it. It waits for its merge.",
    tone: "accepted",
  });
});

test("TaskStages: the implement and review doors open the latest implementer step and the latest read-only step of the task", () => {
  const detail = detailOf([
    step({ id: "a1", end: "done", started: 100n, ended: 110n }),
    step({ id: "r1", agent: "opus", role: "reviewer", readOnly: true, end: "done", started: 112n, ended: 113n }),
    step({ id: "a2", end: "done", started: 115n, ended: 120n }),
    step({ id: "r2", task: "T2", agent: "opus", role: "reviewer", readOnly: true, end: "done", started: 125n, ended: 126n }),
    step({ id: "a9", task: "T2", end: "done", started: 130n, ended: 131n }),
  ]);
  const bodies = stageBodiesOf(detail, "T1");
  assert.equal(bodies?.implement.session, "a2");
  assert.equal(bodies?.review.session, "r1");
});

test("TaskStages: a task with no implementer step has no latest attempt and no door", () => {
  const bodies = stageBodiesOf(detailOf([]), "T1");
  assert.equal(bodies?.implement.latest, null);
  assert.equal(bodies?.implement.session, null);
});

test("TaskStages: a task the epic does not have has no stage bodies", () => {
  assert.equal(stageBodiesOf(detailOf([]), "T9"), null);
});
