import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicStepSchema, EpicsGetResponseSchema, type EpicStep, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { cellsOf, phaseOfPlace, PLACES, recordTasksOf, type RecordTask } from "./record";

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

// The one record task of a Get response that has one task.
function taskOf(detail: EpicsGetResponse): RecordTask {
  const tasks = recordTasksOf(detail);
  assert.equal(tasks.length, 1);
  return tasks[0];
}

// The cells of a record task, outside in, as words.
function cellsWords(task: RecordTask): string {
  return task.cells.join(" ");
}

test("EpicOrrery: each build stage is placed in its ring, and verify is review", () => {
  const cases: [string, string][] = [
    ["red", "red"],
    ["implement", "implement"],
    ["green", "green"],
    ["review", "review"],
    ["verify", "review"],
  ];
  for (const [stage, place] of cases) {
    assert.equal(taskOf(planOf("open", stage, [])).place, place, stage);
  }
});

test("EpicOrrery: a task in the orchestrate stage is placed at implement, running, with or without its step", () => {
  assert.equal(taskOf(planOf("open", "orchestrate", [step({})])).place, "implement");
  const staged = taskOf(planOf("open", "orchestrate", []));
  assert.equal(staged.place, "implement");
  assert.equal(staged.tone, "run");
});

test("EpicOrrery: a gated task and an accepted task sit at integrate, accepted, with the four cells before it ok", () => {
  const gated = taskOf(planOf("gated", "", []));
  assert.equal(gated.place, "integrate");
  assert.equal(gated.tone, "acc");
  assert.equal(cellsWords(gated), "ok ok ok ok acc");

  const accepted = taskOf(planOf("open", "", [step({ end: "done", ended: 160n })]));
  assert.equal(accepted.place, "integrate");
  assert.equal(accepted.tone, "acc");
  assert.equal(cellsWords(accepted), "ok ok ok ok acc");
});

test("EpicOrrery: a done task is integrated, with five ok cells", () => {
  const done = taskOf(planOf("done", "", []));
  assert.equal(done.place, "integrated");
  assert.equal(done.tone, "ok");
  assert.equal(cellsWords(done), "ok ok ok ok ok");
});

test("EpicOrrery: a planned task has five none cells", () => {
  const planned = taskOf(planOf("open", "", []));
  assert.equal(planned.place, "planned");
  assert.equal(planned.tone, "plan");
  assert.equal(cellsWords(planned), "none none none none none");
});

test("EpicOrrery: a task at green and running has ok cells before it, its tone at it, and none after", () => {
  const green = taskOf(planOf("open", "green", []));
  assert.equal(green.place, "green");
  assert.equal(green.tone, "run");
  assert.equal(cellsWords(green), "ok ok run none none");
});

test("EpicOrrery: a held task at implement has its tone at its place, whatever its steps' stage", () => {
  const held = taskOf(planOf("open", "", [step({ held: true })]));
  assert.equal(held.place, "implement");
  assert.equal(held.tone, "mine");
  assert.equal(cellsWords(held), "ok mine none none none");
});

test("EpicOrrery: a task's cells come from its place and tone alone", () => {
  assert.equal(cellsOf("integrated", "ok").join(" "), "ok ok ok ok ok");
  assert.equal(cellsOf("planned", "plan").join(" "), "none none none none none");
  assert.equal(cellsOf("review", "bad").join(" "), "ok ok ok bad none");
});

test("EpicOrrery: each phase of a place is the phase its ring names, and integrated and integrate are integrating", () => {
  assert.equal(phaseOfPlace("red"), "implementing");
  assert.equal(phaseOfPlace("implement"), "implementing");
  assert.equal(phaseOfPlace("green"), "checking");
  assert.equal(phaseOfPlace("review"), "reviewing");
  assert.equal(phaseOfPlace("integrate"), "integrating");
  assert.equal(phaseOfPlace("integrated"), "integrating");
  assert.equal(phaseOfPlace("planned"), undefined);
  assert.equal(PLACES.join(" "), "red implement green review integrate");
});

test("EpicOrrery: a task's since is its running step's stage start, else that step's start, and 0 with no running step", () => {
  assert.equal(taskOf(planOf("open", "implement", [step({ stage: "checks", started: 170n, stageStarted: 175n })])).since, 175);
  assert.equal(taskOf(planOf("open", "implement", [step({ started: 170n })])).since, 170);
  assert.equal(taskOf(planOf("open", "implement", [step({ end: "failed", started: 300n, ended: 330n })])).since, 0);
});

test("EpicOrrery: the record's tasks are the build tasks when there are any, and the record's tasks when there are none", () => {
  const build = recordTasksOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "", requirements: ["R1"] }],
      tasks: [{ effort: 1, slug: "old", subject: "Old", status: "done", requirements: [] }],
    }),
  );
  assert.equal(build.length, 1);
  assert.equal(build[0].task, "T1");
  assert.equal(build[0].title, "Parser");
  assert.equal(build[0].requirements.join(","), "R1");

  const record = recordTasksOf(
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

test("EpicWords: a planned task is not started, and a done one is done", () => {
  assert.equal(taskOf(planOf("open", "", [])).words, "not started");
  assert.equal(taskOf(planOf("done", "", [])).words, "done");
});

test("EpicWords: an accepted task at integrate waits to integrate", () => {
  assert.equal(taskOf(planOf("gated", "", [])).words, "accepted · waits to integrate");
});

test("EpicWords: a limited step waits on a usage limit, or on the kind of limit it names", () => {
  assert.equal(taskOf(planOf("open", "", [step({ limited: true })])).words, "waits on a usage limit");
  assert.equal(taskOf(planOf("open", "", [step({ limited: true, limitKind: "weekly" })])).words, "waits on the weekly limit");
});

test("EpicWords: a held step is its place and held, and held wins over a limit", () => {
  assert.equal(taskOf(planOf("open", "", [step({ held: true, limited: true })])).words, "implement · held");
});

test("EpicWords: a failed step is its place and failed", () => {
  assert.equal(taskOf(planOf("open", "", [step({ end: "failed", ended: 130n })])).words, "implement · failed");
});

test("EpicWords: a running step is its place and its doing, or its place alone when it has no doing", () => {
  assert.equal(taskOf(planOf("open", "", [step({ doing: "writing the parser" })])).words, "implement · writing the parser");
  assert.equal(taskOf(planOf("open", "", [step({})])).words, "implement");
});
