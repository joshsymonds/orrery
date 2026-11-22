import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";

import { EpicRequirementSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { durationText, provenOf, taskOfLabel, tasksOf } from "./phases";

test("tasks come from the build tasks when there are any, each where the server stands it", () => {
  const tasks = tasksOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [
        { id: "T1", title: "Parser", status: "open", stage: "implement", requirements: ["R1"], place: "implement", tone: "run" },
        { id: "T2", title: "Lexer", status: "done", stage: "review", requirements: [], place: "integrate", tone: "acc" },
      ],
      tasks: [{ effort: 1, slug: "old", subject: "Old", status: "done", requirements: [], place: "integrated", tone: "ok" }],
    }),
  );
  assert.deepEqual(
    tasks.map((task) => [task.task, task.title, task.requirements.join(","), task.place, task.tone]),
    [
      ["T1", "Parser", "R1", "implement", "run"],
      ["T2", "Lexer", "", "integrate", "acc"],
    ],
  );
});

test("tasks come from the record's tasks when there are no build tasks, each where the server stands it", () => {
  const tasks = tasksOf(
    create(EpicsGetResponseSchema, {
      tasks: [
        { effort: 1, slug: "first", subject: "First task", status: "done", requirements: ["R2", "R3"], place: "integrated", tone: "ok" },
        { effort: 1, slug: "second", subject: "Second", status: "gap", requirements: [], place: "green", tone: "bad" },
      ],
    }),
  );
  assert.deepEqual(
    tasks.map((task) => [task.task, task.title, task.requirements.join(","), task.place, task.tone, task.labels.join(",")]),
    [
      ["first", "First task", "R2,R3", "integrated", "ok", "first"],
      ["second", "Second", "", "green", "bad", "second"],
    ],
  );
});

test("a place or tone the page does not know reads as planned", () => {
  const [task] = tasksOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "", place: "launched", tone: "sparkling" }],
    }),
  );
  assert.equal(task.place, "planned");
  assert.equal(task.tone, "plan");
});

test("a build task's steps are labelled with its id, or with the evidence label of its red or green run", () => {
  const tasks = tasksOf(
    create(EpicsGetResponseSchema, {
      buildTasks: [
        { id: "T1", title: "Parser", status: "open", stage: "green", red: { id: "red-1" }, green: { id: "green-1" } },
        { id: "T2", title: "Lexer", status: "open", stage: "" },
      ],
    }),
  );
  assert.deepEqual(tasks[0].labels, ["T1", "evidence-red-1", "evidence-green-1"]);
  assert.deepEqual(tasks[1].labels, ["T2"]);
  assert.equal(taskOfLabel(tasks, "evidence-green-1")?.task, "T1");
  assert.equal(taskOfLabel(tasks, "T2")?.task, "T2");
  assert.equal(taskOfLabel(tasks, "evidence-other"), undefined);
  assert.equal(taskOfLabel(tasks, ""), undefined);
});

test("a requirement is proven by its own passed green run, else by every task naming it being integrated", () => {
  const requirements = [
    create(EpicRequirementSchema, { id: "R1", runs: [{ phase: "green", passed: true }] }),
    create(EpicRequirementSchema, { id: "R2", runs: [{ phase: "green", passed: false }] }),
  ];
  const tasks = [
    { requirements: ["R3"], place: "integrated" as const },
    { requirements: ["R3", "R4"], place: "integrate" as const },
  ];
  assert.equal(provenOf("R1", tasks, requirements), true);
  assert.equal(provenOf("R2", tasks, requirements), false);
  assert.equal(provenOf("R3", tasks, requirements), false, "an accepted task waiting to integrate proves nothing yet");
  assert.equal(provenOf("R3", [tasks[0]], requirements), true);
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
