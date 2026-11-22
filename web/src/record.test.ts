import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicStepSchema, EpicsGetResponseSchema, type EpicStep, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { PLACES } from "./phases";
import { cellsOf, phaseOfPlace, pipsOf, recordTasksOf, type RecordTask } from "./record";

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

// A Get response with the build task T1, which the server stands at place in tone, at status and stage, and the given
// steps.
function planOf(place: string, tone: string, steps: EpicStep[] = [], status = "open", stage = ""): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    buildTasks: [{ id: "T1", title: "Parser", status, stage, requirements: ["R1"], place, tone, green: { id: "green-1" } }],
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

test("EpicOrrery: a task stands where the server stands it, whatever its steps", () => {
  const steps = [step({ end: "goal_unmet", ended: 130n }), step({ end: "checks_failed", ended: 160n })];
  for (const place of [...PLACES, "planned", "integrated"]) {
    const task = taskOf(planOf(place, "run", steps));
    assert.equal(task.place, place, place);
    assert.equal(task.tone, "run", place);
  }
});

test("EpicOrrery: an accepted task whose first attempt ended goal_unmet and whose review passed waits at integrate, not failed", () => {
  const steps = [
    step({ end: "goal_unmet", ended: 130n }),
    step({ end: "done", ended: 160n, started: 140n }),
    step({ task: "evidence-green-1", readOnly: true, end: "done", ended: 190n, started: 170n }),
    step({ readOnly: true, end: "done", ended: 220n, started: 200n }),
  ];
  const accepted = taskOf(planOf("integrate", "acc", steps, "done", "review"));
  assert.equal(cellsWords(accepted), "ok ok ok ok acc");
  assert.equal(accepted.words, "accepted · waits to integrate");
});

test("EpicOrrery: a running task has ok cells before its place, its tone at it, none after, and no holes", () => {
  assert.equal(cellsWords(taskOf(planOf("red", "run", [step({})]))), "run none none none none");
  assert.equal(cellsWords(taskOf(planOf("implement", "run", [step({})]))), "ok run none none none");
  assert.equal(cellsWords(taskOf(planOf("green", "run", [step({})]))), "ok ok run none none");
  assert.equal(cellsWords(taskOf(planOf("review", "run", [step({ readOnly: true })]))), "ok ok ok run none");
});

test("EpicOrrery: a task retrying after a goal_unmet or checks_failed attempt draws running, not failed", () => {
  for (const end of ["goal_unmet", "checks_failed"]) {
    const retrying = taskOf(planOf("implement", "run", [step({ end, ended: 130n })]));
    assert.equal(cellsWords(retrying), "ok run none none none", end);
    assert.equal(retrying.words, "implement", end);
  }
});

test("EpicOrrery: a gap draws failed at its place", () => {
  const gap = taskOf(planOf("green", "bad", [step({ end: "goal_unmet", ended: 130n })], "gap", "green"));
  assert.equal(cellsWords(gap), "ok ok bad none none");
  assert.equal(gap.words, "green · failed");
});

test("EpicOrrery: a done task is integrated, with five ok cells, and a planned task has five none", () => {
  assert.equal(cellsWords(taskOf(planOf("integrated", "ok", [], "done"))), "ok ok ok ok ok");
  assert.equal(cellsWords(taskOf(planOf("planned", "plan"))), "none none none none none");
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
  assert.equal(taskOf(planOf("implement", "run", [step({ stage: "checks", started: 170n, stageStarted: 175n })])).since, 175);
  assert.equal(taskOf(planOf("implement", "run", [step({ started: 170n })])).since, 170);
  assert.equal(taskOf(planOf("implement", "run", [step({ end: "failed", started: 300n, ended: 330n })])).since, 0);
});

test("EpicOrrery: a task's running step is the latest of its steps that has not ended, its evidence judge's among them", () => {
  const judging = step({ task: "evidence-green-1", readOnly: true, started: 200n, doing: "judging the green run" });
  const task = taskOf(planOf("green", "run", [step({ end: "done", started: 100n, ended: 150n }), judging]));
  assert.equal(task.since, 200);
  assert.equal(task.words, "green · judging the green run");
});

test("EpicOrrery: the record's tasks are the build tasks when there are any, and the record's tasks when there are none", () => {
  const build = recordTasksOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "", requirements: ["R1"], place: "planned", tone: "plan" }],
      tasks: [{ effort: 1, slug: "old", subject: "Old", status: "done", requirements: [], place: "integrated", tone: "ok" }],
    }),
  );
  assert.deepEqual(
    build.map((task) => [task.task, task.title, task.requirements.join(",")]),
    [["T1", "Parser", "R1"]],
  );

  const record = recordTasksOf(
    create(EpicsGetResponseSchema, {
      tasks: [
        { effort: 1, slug: "first", subject: "First task", status: "done", requirements: ["R2", "R3"], place: "integrated", tone: "ok" },
        { effort: 1, slug: "second", subject: "Second", status: "pending", requirements: [], place: "planned", tone: "plan" },
      ],
    }),
  );
  assert.deepEqual(
    record.map((task) => [task.task, task.title, task.requirements.join(","), task.place]),
    [
      ["first", "First task", "R2,R3", "integrated"],
      ["second", "Second", "", "planned"],
    ],
  );
});

test("a step's pips are its task's cells, found by its label or its evidence judge's, and five none for no task", () => {
  const tasks = recordTasksOf(planOf("green", "run"));
  assert.deepEqual(pipsOf("T1", tasks), ["ok", "ok", "run", "none", "none"]);
  assert.deepEqual(pipsOf("evidence-green-1", tasks), ["ok", "ok", "run", "none", "none"]);
  assert.deepEqual(pipsOf("T9", tasks), ["none", "none", "none", "none", "none"]);
  assert.deepEqual(pipsOf("", tasks), ["none", "none", "none", "none", "none"]);
});

test("EpicWords: a planned task is not started, and a done one is done", () => {
  assert.equal(taskOf(planOf("planned", "plan")).words, "not started");
  assert.equal(taskOf(planOf("integrated", "ok", [], "done")).words, "done");
});

test("EpicWords: a limited task waits on a usage limit, or on the kind of limit its running step names", () => {
  assert.equal(taskOf(planOf("implement", "limited", [step({ limited: true })])).words, "waits on a usage limit");
  assert.equal(
    taskOf(planOf("implement", "limited", [step({ limited: true, limitKind: "weekly" })])).words,
    "waits on the weekly limit",
  );
});

test("EpicWords: a held task is its place and held", () => {
  assert.equal(taskOf(planOf("implement", "mine", [step({ held: true, limited: true })])).words, "implement · held");
});

test("EpicWords: a running task is its place and its step's doing, or its place alone when it has no doing", () => {
  assert.equal(taskOf(planOf("implement", "run", [step({ doing: "writing the parser" })])).words, "implement · writing the parser");
  assert.equal(taskOf(planOf("implement", "run", [step({})])).words, "implement");
});
