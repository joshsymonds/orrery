import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicsGetResponseSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { blockerOf, controlsOf, stateOf } from "./status";

type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;

// A response as serve sends it. What the init leaves out is false, empty or zero.
function detail(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// Synthetic epic identity shared by the fixtures.
const alpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha" };

test("a missing epic gives an empty state", () => {
  assert.equal(stateOf(detail({ paused: true, parked: "budget" })), "");
});

test("a phase off the track is the state as written", () => {
  assert.equal(stateOf(detail({ epic: { ...alpha, outcome: "released", live: true } })), "released");
});

test("paused is ahead of parked and live", () => {
  const d = detail({ epic: { ...alpha, live: true }, paused: true, parked: "budget" });
  assert.equal(stateOf(d), "paused");
});

test("parked is ahead of live", () => {
  const d = detail({ epic: { ...alpha, live: true }, parked: "budget" });
  assert.equal(stateOf(d), "parked");
});

test("an epic that is not live and has no other state is empty", () => {
  assert.equal(stateOf(detail({ epic: { ...alpha } })), "");
});

// An orreryd epic: its outcome is its phase.
const orreryd = { ...alpha, source: "orreryd", live: true };

test("an orreryd epic reads paused, then parked, ahead of its track", () => {
  const built = { ...orreryd, outcome: "built" };
  assert.equal(stateOf(detail({ epic: built, paused: true, parked: "budget" })), "paused");
  assert.equal(stateOf(detail({ epic: built, parked: "budget" })), "parked");
});

test("LiveControls: an accepted orreryd epic shows Pause and Stop, and reads planning until it has a task, then building", () => {
  const planning = detail({ epic: { ...orreryd, outcome: "accepted" } });
  assert.deepEqual(controlsOf(planning), { pause: true, resume: false, stop: true });
  assert.equal(stateOf(planning), "planning");
  const building = detail({ epic: { ...orreryd, outcome: "accepted" }, tasks: [{ slug: "t1" }] });
  assert.equal(stateOf(building), "building");
});

test("an accepted orreryd epic with a planned build task reads building", () => {
  assert.equal(stateOf(detail({ epic: { ...orreryd, outcome: "accepted" }, buildTasks: [{ id: "b1" }] })), "building");
});

test("LiveControls: a paused orreryd epic shows Resume and Stop, and reads paused", () => {
  const d = detail({ epic: { ...orreryd, outcome: "built" }, paused: true });
  assert.deepEqual(controlsOf(d), { pause: false, resume: true, stop: true });
  assert.equal(stateOf(d), "paused");
});

test("LiveControls: a releasing orreryd epic shows Stop and no Pause, and reads releasing", () => {
  const d = detail({ epic: { ...orreryd, outcome: "releasing" } });
  assert.deepEqual(controlsOf(d), { pause: false, resume: false, stop: true });
  assert.equal(stateOf(d), "releasing");
});

test("LiveControls: a paused releasing orreryd epic shows Resume and Stop", () => {
  const d = detail({ epic: { ...orreryd, outcome: "releasing" }, paused: true });
  assert.deepEqual(controlsOf(d), { pause: false, resume: true, stop: true });
});

test("LiveControls: a released orreryd epic shows no command, and reads released", () => {
  const d = detail({ epic: { ...orreryd, live: false, outcome: "released" } });
  assert.deepEqual(controlsOf(d), { pause: false, resume: false, stop: false });
  assert.equal(stateOf(d), "released");
});

test("LiveControls: an orreryd epic pauses in accepted, built, reviewing and correcting, and stops on its track", () => {
  for (const phase of ["accepted", "built", "reviewing", "correcting"]) {
    assert.deepEqual(controlsOf(detail({ epic: { ...orreryd, outcome: phase } })), {
      pause: true,
      resume: false,
      stop: true,
    });
  }
  for (const phase of ["clean", "releasing"]) {
    assert.deepEqual(controlsOf(detail({ epic: { ...orreryd, outcome: phase } })), {
      pause: false,
      resume: false,
      stop: true,
    });
  }
  assert.deepEqual(controlsOf(detail({ epic: { ...orreryd, outcome: "stopped" } })), {
    pause: false,
    resume: false,
    stop: false,
  });
});

test("an orreryd epic's state word is its track part in each track phase, and its phase as written off the track", () => {
  const words = [
    ["accepted", "planning"],
    ["built", "building"],
    ["reviewing", "building"],
    ["correcting", "building"],
    ["clean", "releasing"],
    ["releasing", "releasing"],
    ["stopped", "stopped"],
    ["met_at_base", "met_at_base"],
  ];
  for (const [phase, word] of words) {
    assert.equal(stateOf(detail({ epic: { ...orreryd, outcome: phase } })), word, phase);
  }
});

test("LiveControls: an epic off its track shows no command, and Resume once it is paused", () => {
  const released = { ...alpha, live: true, outcome: "released" };
  assert.deepEqual(controlsOf(detail({ epic: released })), { pause: false, resume: false, stop: false });
  assert.deepEqual(controlsOf(detail({ epic: released, paused: true })), { pause: false, resume: true, stop: false });
});

test("an epic with no question, park, or held or limited step has no blocker", () => {
  const d = detail({ epic: alpha, steps: [{ id: "s1", task: "t-1", end: "" }] });
  assert.equal(blockerOf(d), null);
});

test("an ended step gives no blocker even when it was held", () => {
  const d = detail({ steps: [{ id: "s1", task: "t-1", end: "done", held: true, started: 400n }] });
  assert.equal(blockerOf(d), null);
});

test("the first open question is the blocker, waiting on your answer since it was asked", () => {
  const d = detail({
    questions: [
      { id: "Q1", text: "Which branch?", turn: 2n, asked: 1700000100n },
      { id: "Q2", text: "Which index?", turn: 3n, asked: 1700000200n },
    ],
  });
  const b = blockerOf(d);
  assert.equal(b?.kind, "question");
  if (b?.kind !== "question") {
    assert.fail("expected a question blocker");
  }
  assert.equal(b.question.id, "Q1");
  assert.equal(b.text, "waits on your answer");
  assert.equal(b.since, 1700000100);
});

test("SteererHolds: the question blocker is the first open question its steerer does not hold, and none while the steerer holds every one", () => {
  const steererOnly = detail({
    questions: [{ id: "Q1", text: "Which branch?", turn: 1n, asked: 1700000100n, steererHeld: true }],
    steps: [{ id: "s1", task: "t-1", end: "", held: true, started: 400n }],
  });
  assert.equal(blockerOf(steererOnly)?.text, "t-1 held");
  const escalatedSecond = detail({
    questions: [
      { id: "Q1", text: "Which branch?", turn: 1n, asked: 1700000100n, steererHeld: true },
      { id: "Q2", text: "Which index?", turn: 2n, asked: 1700000200n, escalated: 1700000300n },
    ],
  });
  const b = blockerOf(escalatedSecond);
  if (b?.kind !== "question") {
    assert.fail("expected a question blocker");
  }
  assert.equal(b.question.id, "Q2");
  assert.equal(b.text, "waits on your answer");
  assert.equal(b.since, 1700000200);
});

test("a question asked at 0 has a since of 0", () => {
  const b = blockerOf(detail({ questions: [{ id: "Q1", text: "Which branch?", turn: 1n, asked: 0n }] }));
  assert.equal(b?.kind, "question");
  assert.equal(b?.since, 0);
});

test("a question is ahead of a park and a held step", () => {
  const d = detail({
    questions: [{ id: "Q1", text: "Which branch?", turn: 1n, asked: 1700000100n }],
    parked: "budget",
    steps: [{ id: "s1", task: "t-1", end: "", held: true, started: 400n }],
  });
  assert.equal(blockerOf(d)?.kind, "question");
});

test("a park is the blocker with its reason and a since of 0, ahead of a held step", () => {
  const d = detail({
    parked: "budget",
    steps: [{ id: "s1", task: "t-1", end: "", held: true, started: 400n }],
  });
  const b = blockerOf(d);
  assert.equal(b?.kind, "parked");
  assert.equal(b?.text, "parked · budget");
  assert.equal(b?.since, 0);
});

test("a held step is the blocker, held since its stage started", () => {
  const d = detail({
    steps: [{ id: "s1", task: "t-1", end: "", held: true, started: 400n, stageStarted: 500n }],
  });
  const b = blockerOf(d);
  assert.equal(b?.kind, "held");
  if (b?.kind !== "held") {
    assert.fail("expected a held blocker");
  }
  assert.equal(b.step.id, "s1");
  assert.equal(b.text, "t-1 held");
  assert.equal(b.since, 500);
});

test("a held step with no stage start is held since its start", () => {
  const d = detail({
    steps: [{ id: "s1", task: "t-1", end: "", held: true, started: 400n, stageStarted: 0n }],
  });
  const b = blockerOf(d);
  assert.equal(b?.kind, "held");
  assert.equal(b?.since, 400);
});

test("a held step with no task is named by its agent", () => {
  const d = detail({
    steps: [{ id: "s1", task: "", agent: "builder", end: "", held: true, started: 400n, stageStarted: 500n }],
  });
  assert.equal(blockerOf(d)?.text, "builder held");
});

test("a limited step waits on its limit kind since its stage started", () => {
  const d = detail({
    steps: [{ id: "s1", task: "t-2", end: "", limited: true, limitKind: "weekly", started: 400n, stageStarted: 600n }],
  });
  const b = blockerOf(d);
  assert.equal(b?.kind, "held");
  assert.equal(b?.text, "t-2 waits on a weekly limit");
  assert.equal(b?.since, 600);
});

test("a limited step with no limit kind waits on a limit", () => {
  const d = detail({
    steps: [{ id: "s1", task: "t-2", end: "", limited: true, limitKind: "", started: 400n, stageStarted: 600n }],
  });
  assert.equal(blockerOf(d)?.text, "t-2 waits on a limit");
});

test("a step both held and limited reads as held", () => {
  const d = detail({
    steps: [{ id: "s1", task: "t-3", end: "", held: true, limited: true, limitKind: "weekly", started: 400n }],
  });
  assert.equal(blockerOf(d)?.text, "t-3 held");
});

test("the first held or limited step in order is the blocker", () => {
  const limitedFirst = detail({
    steps: [
      { id: "s1", task: "t-a", end: "", limited: true, limitKind: "weekly", started: 400n },
      { id: "s2", task: "t-b", end: "", held: true, started: 410n },
    ],
  });
  assert.equal(blockerOf(limitedFirst)?.text, "t-a waits on a weekly limit");

  const heldFirst = detail({
    steps: [
      { id: "s1", task: "t-b", end: "", held: true, started: 400n },
      { id: "s2", task: "t-a", end: "", limited: true, limitKind: "weekly", started: 410n },
    ],
  });
  assert.equal(blockerOf(heldFirst)?.text, "t-b held");
});

test("a running step that is neither held nor limited is skipped for a later held step", () => {
  const d = detail({
    steps: [
      { id: "s1", task: "t-a", end: "", started: 300n },
      { id: "s2", task: "t-b", end: "", held: true, started: 400n },
    ],
  });
  assert.equal(blockerOf(d)?.text, "t-b held");
});
