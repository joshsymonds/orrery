import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicStepSchema, EpicsGetResponseSchema, type EpicStep, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { durationText, wedgesOf, type Place, type Wedge } from "./phases";

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

// A Get response with the build task T1 at the given status and stage, and the given steps.
function planOf(status: string, stage: string, steps: EpicStep[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    buildTasks: [{ id: "T1", title: "Parser", status, stage, requirements: ["R1"] }],
    steps,
  });
}

// The one wedge of a Get response that has one task.
function wedgeOf(detail: EpicsGetResponse): Wedge {
  const wedges = wedgesOf(detail);
  assert.equal(wedges.length, 1);
  return wedges[0];
}

// The state of each phase slice in phase order, as words.
function states(wedge: Wedge): string {
  return wedge.slices.map((slice) => slice.state).join(" ");
}

test("a task whose status is done or integrated is integrated and ok, whatever its steps", () => {
  for (const status of ["done", "integrated"]) {
    const wedge = wedgeOf(planOf(status, "", [step({ end: "" })]));
    assert.equal(wedge.place, "integrated");
    assert.equal(wedge.tone, "ok");
    assert.equal(states(wedge), "passed passed passed passed");
  }
});

test("a running step in stage turn places its task in implementing, running", () => {
  const wedge = wedgeOf(planOf("open", "implement", [step({ stage: "turn" })]));
  assert.equal(wedge.place, "implementing");
  assert.equal(wedge.tone, "run");
  assert.equal(states(wedge), "now ahead ahead ahead");
});

test("a running step in stage checks, gate or judge places its task in checking, running with the slice now", () => {
  for (const stage of ["checks", "gate", "judge"]) {
    const wedge = wedgeOf(planOf("open", "implement", [step({ stage })]));
    assert.equal(wedge.place, "checking");
    assert.equal(wedge.tone, "run");
    assert.equal(states(wedge), "passed now ahead ahead");
  }
});

test("a running read-only step places its task in reviewing, running with the slice now", () => {
  for (const stage of ["turn", "checks"]) {
    const wedge = wedgeOf(planOf("open", "implement", [step({ stage, readOnly: true })]));
    assert.equal(wedge.place, "reviewing");
    assert.equal(wedge.tone, "run");
    assert.equal(states(wedge), "passed passed now ahead");
  }
});

test("EpicWords: a held running step's task is mine, a limited one's is limited, and held wins over limited", () => {
  const held = wedgeOf(planOf("open", "implement", [step({ held: true })]));
  assert.equal(held.place, "implementing");
  assert.equal(held.tone, "mine");

  const limited = wedgeOf(planOf("open", "implement", [step({ limited: true })]));
  assert.equal(limited.place, "implementing");
  assert.equal(limited.tone, "limited");

  const both = wedgeOf(planOf("open", "implement", [step({ held: true, limited: true })]));
  assert.equal(both.tone, "mine");
});

test("a latest step that ended done places its task in integrating, accepted", () => {
  const wedge = wedgeOf(planOf("open", "green", [step({ end: "done", ended: 160n })]));
  assert.equal(wedge.place, "integrating");
  assert.equal(wedge.tone, "acc");
  assert.equal(states(wedge), "passed passed passed now");
});

test("a latest step that ended otherwise is bad, in the phase its end and read-only flag name", () => {
  const cases: [StepInit, Place, string][] = [
    [{ end: "checks_failed", ended: 130n }, "checking", "passed now ahead ahead"],
    [{ end: "goal_unmet", ended: 130n }, "checking", "passed now ahead ahead"],
    [{ end: "failed", ended: 130n }, "implementing", "now ahead ahead ahead"],
    [{ end: "returned", ended: 130n, readOnly: true }, "reviewing", "passed passed now ahead"],
  ];
  for (const [init, place, slices] of cases) {
    const wedge = wedgeOf(planOf("open", "implement", [step(init)]));
    assert.equal(wedge.place, place);
    assert.equal(wedge.tone, "bad");
    assert.equal(states(wedge), slices);
  }
});

test("a task with no step places by its build stage, running", () => {
  const cases: [string, Place, string][] = [
    ["red", "implementing", "now ahead ahead ahead"],
    ["implement", "implementing", "now ahead ahead ahead"],
    ["green", "checking", "passed now ahead ahead"],
    ["review", "reviewing", "passed passed now ahead"],
    ["verify", "reviewing", "passed passed now ahead"],
  ];
  for (const [stage, place, slices] of cases) {
    const wedge = wedgeOf(planOf("open", stage, []));
    assert.equal(wedge.place, place);
    assert.equal(wedge.tone, "run");
    assert.equal(states(wedge), slices);
  }
});

test("a task with no step and no build stage places by its status: dispatched, gated or gap", () => {
  const dispatched = wedgeOf(planOf("dispatched", "", []));
  assert.equal(dispatched.place, "implementing");
  assert.equal(dispatched.tone, "run");
  assert.equal(states(dispatched), "now ahead ahead ahead");

  const gated = wedgeOf(planOf("gated", "", []));
  assert.equal(gated.place, "integrating");
  assert.equal(gated.tone, "acc");
  assert.equal(states(gated), "passed passed passed now");

  const gap = wedgeOf(planOf("gap", "", []));
  assert.equal(gap.place, "implementing");
  assert.equal(gap.tone, "bad");
  assert.equal(states(gap), "now ahead ahead ahead");
});

test("a task with no step, no build stage and no dispatch is planned, at the rim", () => {
  for (const status of ["open", "pending"]) {
    const wedge = wedgeOf(planOf(status, "", []));
    assert.equal(wedge.place, "planned");
    assert.equal(wedge.tone, "plan");
    assert.equal(states(wedge), "ahead ahead ahead ahead");
  }
});

test("a slice's summary is its phase's latest step's summary, doing while that step runs, and empty with no step", () => {
  const wedge = wedgeOf(
    planOf("open", "implement", [
      step({ id: "s0", stage: "turn", end: "stopped", started: 40n, ended: 90n, summary: "started the parser" }),
      step({ id: "s1", stage: "turn", end: "done", started: 100n, ended: 160n, summary: "wrote the parser" }),
      step({ id: "s2", stage: "checks", started: 170n, stageStarted: 175n, doing: "running checks" }),
    ]),
  );
  assert.equal(wedge.place, "checking");
  assert.equal(wedge.slices[0].summary, "wrote the parser");
  assert.equal(wedge.slices[1].summary, "running checks");
  assert.equal(wedge.slices[2].summary, "");
  assert.equal(wedge.slices[3].summary, "");
});

test("a now slice's since is its running step's stage start, else that step's start, and 0 for other slices", () => {
  const running = wedgeOf(
    planOf("open", "implement", [
      step({ end: "done", started: 100n, ended: 160n }),
      step({ stage: "checks", started: 170n, stageStarted: 175n }),
    ]),
  );
  assert.equal(running.slices[1].since, 175);
  assert.equal(running.slices[0].since, 0);
  assert.equal(running.slices[2].since, 0);

  const ended = wedgeOf(planOf("open", "implement", [step({ end: "failed", started: 300n, ended: 330n })]));
  assert.equal(ended.slices[0].since, 300);

  const none = wedgeOf(planOf("open", "red", []));
  assert.equal(none.slices[0].since, 0);
});

test("a passed slice's seconds are the sum of its ended steps' durations, and 0 for every other slice", () => {
  const wedge = wedgeOf(
    planOf("open", "implement", [
      step({ id: "s1", end: "returned", started: 100n, ended: 130n }),
      step({ id: "s2", end: "returned", started: 140n, ended: 200n }),
      step({ id: "s3", readOnly: true, end: "done", started: 210n, ended: 240n }),
    ]),
  );
  assert.equal(wedge.place, "integrating");
  assert.equal(wedge.slices[0].seconds, 90);
  assert.equal(wedge.slices[1].seconds, 0);
  assert.equal(wedge.slices[2].seconds, 30);
  assert.equal(wedge.slices[3].seconds, 0);

  const failed = wedgeOf(planOf("open", "implement", [step({ stage: "checks", end: "checks_failed", started: 300n, ended: 330n })]));
  assert.equal(failed.place, "checking");
  assert.equal(failed.slices[1].seconds, 0);
});

test("wedges come from the build tasks when there are any, and from the record's tasks when there are none", () => {
  const planned = wedgesOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "", requirements: ["R1"] }],
      tasks: [{ effort: 1, slug: "old", subject: "Old", status: "done", requirements: [] }],
    }),
  );
  assert.equal(planned.length, 1);
  assert.equal(planned[0].task, "T1");
  assert.equal(planned[0].title, "Parser");
  assert.equal(planned[0].requirements.join(","), "R1");

  const record = wedgesOf(
    create(EpicsGetResponseSchema, {
      tasks: [
        { effort: 1, slug: "first", subject: "First task", status: "done", requirements: ["R2", "R3"] },
        { effort: 1, slug: "second", subject: "Second", status: "pending", requirements: [] },
      ],
    }),
  );
  assert.equal(record.length, 2);
  assert.equal(record[0].task, "first");
  assert.equal(record[0].title, "First task");
  assert.equal(record[0].requirements.join(","), "R2,R3");
  assert.equal(record[0].place, "integrated");
  assert.equal(record[1].place, "planned");
});

test("a task's steps are those labelled with its id", () => {
  const wedge = wedgeOf(planOf("open", "", [step({ task: "T2" })]));
  assert.equal(wedge.place, "planned");
  assert.equal(wedge.tone, "plan");
});

test("durationText gives seconds under a minute, minutes under an hour, and hours with the minutes left over", () => {
  assert.equal(durationText(0), "0 s");
  assert.equal(durationText(59), "59 s");
  assert.equal(durationText(60), "1 min");
  assert.equal(durationText(119), "1 min");
  assert.equal(durationText(3599), "59 min");
  assert.equal(durationText(3600), "1 h");
  assert.equal(durationText(3660), "1 h 1 min");
  assert.equal(durationText(7320), "2 h 2 min");
});
