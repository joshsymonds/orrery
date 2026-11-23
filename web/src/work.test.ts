import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  EpicStepSchema,
} from "./gen/orrery/v1/epics_pb";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import { epicsView, noEpics } from "./epics";
import { type EpicThing, type ListThing, type Thing, type WorkConversation, workOf } from "./work";

const repository = "widgets-5d0c1a7";

// makeEpic is a live epic that session s1 steers, in the repository, whose id is its repository and slug joined.
function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering: "s1",
    ...init,
  });
}

function makeStep(id: string, init: MessageInitShape<typeof EpicStepSchema> = {}) {
  return create(EpicStepSchema, { id, task: "", agent: "builder", doing: "", held: false, ...init });
}

// detailed pairs an epic's id with its Get response, for the map epicsView takes.
function detailed(epic: Epic, init: MessageInitShape<typeof EpicsGetResponseSchema> = {}): [string, EpicsGetResponse] {
  return [epic.id, create(EpicsGetResponseSchema, { epic, ...init })];
}

function conversation(session: string, background: BackgroundTask[] = [], pane = false): WorkConversation {
  return { session, pane, background };
}

function task(type: string, status: string, id: string): BackgroundTask {
  return create(BackgroundTaskSchema, { id, description: `${type} ${id}`, type, status });
}

function keysOf(things: Thing[]): string[] {
  return things.map((thing) => thing.key);
}

test("WorkThings: no conversation, and one that steers nothing, give no things", () => {
  const view = epicsView([makeEpic("first", { steering: "" })], new Map());

  assert.deepEqual(workOf(null, view), []);
  assert.deepEqual(workOf(conversation(""), view), [], "an empty session steers nothing, even an epic steered by none");
  assert.deepEqual(workOf(conversation("s2"), view), []);
});

test("WorkThings: the epics come in blocks, waiting, then just ended order, ahead of the lists", () => {
  const view = epicsView(
    [
      makeEpic("ended", { live: false, phaseAt: 50n, seen: 0n }),
      makeEpic("waiting", { waiting: true }),
      makeEpic("busy", { runningSteps: [makeStep("r1")] }),
    ],
    new Map(),
  );

  const things = workOf(conversation("s1", [task("agent", "running", "a1")]), view);

  assert.deepEqual(keysOf(things), [`epic:${repository}/busy`, `epic:${repository}/waiting`, `epic:${repository}/ended`, "agents"]);
});

test("WorkThings: an epic steered by another session is absent", () => {
  const view = epicsView([makeEpic("mine"), makeEpic("theirs", { steering: "s2" })], new Map());

  assert.deepEqual(keysOf(workOf(conversation("s1"), view)), [`epic:${repository}/mine`]);
});

test("WorkThings: an archived epic is absent", () => {
  const view = epicsView([makeEpic("mine"), makeEpic("shelved", { archived: true })], new Map());

  assert.deepEqual(keysOf(workOf(conversation("s1"), view)), [`epic:${repository}/mine`]);
});

test("WorkThings: an ended and seen epic is absent while an ended unseen one is present", () => {
  const view = epicsView(
    [
      makeEpic("seen", { live: false, phaseAt: 100n, seen: 100n }),
      makeEpic("unseen", { live: false, phaseAt: 100n, seen: 0n }),
    ],
    new Map(),
  );

  assert.deepEqual(keysOf(workOf(conversation("s1"), view)), [`epic:${repository}/unseen`]);
});

test("WorkThings: an epic that asks a question is attention", () => {
  const epic = makeEpic("first", { runningSteps: [makeStep("r1")] });
  const view = epicsView([epic], new Map([detailed(epic, { questions: [{ id: "Q1", text: "Use Postgres?" }] })]));

  const thing = workOf(conversation("s1"), view)[0] as EpicThing;

  assert.equal(thing.tone, "attention");
});

test("WorkThings: a running epic is live, and one with nothing running is quiet", () => {
  const view = epicsView([makeEpic("busy", { runningSteps: [makeStep("r1")] }), makeEpic("idle")], new Map());

  const [busy, idle] = workOf(conversation("s1"), view) as EpicThing[];

  assert.equal(busy.tone, "live");
  assert.equal(idle.tone, "quiet");
});

test("WorkLists: agent and shell tasks are two lists, each with its tone and its tasks", () => {
  const agents = [task("agent", "running", "a1"), task("agent", "running", "a2")];
  const shell = task("shell", "running", "sh1");

  const things = workOf(conversation("s1", [...agents, shell]), noEpics);

  assert.deepEqual(keysOf(things), ["agents", "running"]);
  const agentThing = things[0] as ListThing;
  assert.equal(agentThing.kind, "agents");
  assert.equal(agentThing.tone, "live");
  assert.deepEqual(agentThing.tasks, agents);
  const shellThing = things[1] as ListThing;
  assert.equal(shellThing.kind, "running");
  assert.equal(shellThing.tone, "quiet");
  assert.deepEqual(shellThing.tasks, [shell]);
});

test("WorkLists: ended tasks are left out, and a list with none running is absent", () => {
  const running = task("agent", "running", "a1");

  const things = workOf(
    conversation("s1", [running, task("agent", "done", "a2"), task("shell", "failed", "sh1")]),
    noEpics,
  );

  assert.deepEqual(keysOf(things), ["agents"]);
  assert.deepEqual((things[0] as ListThing).tasks, [running]);
});

test("WorkLists: a pane conversation has no lists whatever its background holds", () => {
  const things = workOf(
    conversation("s1", [task("agent", "running", "a1"), task("shell", "running", "sh1")], true),
    noEpics,
  );

  assert.deepEqual(things, []);
});
