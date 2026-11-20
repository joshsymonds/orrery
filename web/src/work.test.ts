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
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import { epicsView, noEpics } from "./epics";
import { contractThingsOf, type EpicThing, type ListThing, type RailGroup, shortName, type WorkConversation, workOf } from "./work";

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

function keysOf(group: RailGroup): string[] {
  return group.things.map((thing) => thing.key);
}

test("WorkRail: no conversation, and one that steers nothing, give no groups", () => {
  const view = epicsView([makeEpic("first", { steering: "" })], new Map());

  assert.deepEqual(workOf(null, view, 0), []);
  assert.deepEqual(workOf(conversation(""), view, 0), [], "an empty session steers nothing, even an epic steered by none");
  assert.deepEqual(workOf(conversation("s2"), view, 0), []);
});

test("WorkRail: the epics come in blocks, waiting, then just ended order, ahead of the lists", () => {
  const view = epicsView(
    [
      makeEpic("ended", { live: false, phaseAt: 50n, seen: 0n }),
      makeEpic("waiting", { waiting: true }),
      makeEpic("busy", { runningSteps: [makeStep("r1")] }),
    ],
    new Map(),
  );

  const groups = workOf(conversation("s1", [task("agent", "running", "a1")]), view, 100);

  assert.deepEqual(
    groups.map((group) => group.kind),
    ["epic", "agents"],
  );
  assert.deepEqual(keysOf(groups[0]), [`epic:${repository}/busy`, `epic:${repository}/waiting`, `epic:${repository}/ended`]);
  assert.equal(groups[0].label, "epics");
  assert.equal(groups[1].label, "agents");
});

test("WorkRail: one epic is labelled epic, and more than three epics are marked many", () => {
  const one = workOf(conversation("s1"), epicsView([makeEpic("only")], new Map()), 0);
  assert.equal(one[0].label, "epic");
  assert.equal(one[0].many, false);

  const three = ["a", "b", "c"].map((slug) => makeEpic(slug));
  assert.equal(workOf(conversation("s1"), epicsView(three, new Map()), 0)[0].many, false, "three epics are not many");

  const four = ["a", "b", "c", "d"].map((slug) => makeEpic(slug));
  const many = workOf(conversation("s1"), epicsView(four, new Map()), 0)[0];
  assert.equal(many.label, "epics");
  assert.equal(many.many, true, "four epics are many");
});

test("WorkRail: an epic steered by another session is absent", () => {
  const view = epicsView([makeEpic("mine"), makeEpic("theirs", { steering: "s2" })], new Map());

  const groups = workOf(conversation("s1"), view, 0);

  assert.deepEqual(keysOf(groups[0]), [`epic:${repository}/mine`]);
});

test("WorkRail: an archived epic is absent", () => {
  const view = epicsView([makeEpic("mine"), makeEpic("shelved", { archived: true })], new Map());

  const groups = workOf(conversation("s1"), view, 0);

  assert.deepEqual(keysOf(groups[0]), [`epic:${repository}/mine`]);
});

test("WorkRail: an ended and seen epic is absent while an ended unseen one is present", () => {
  const view = epicsView(
    [
      makeEpic("seen", { live: false, phaseAt: 100n, seen: 100n }),
      makeEpic("unseen", { live: false, phaseAt: 100n, seen: 0n }),
    ],
    new Map(),
  );

  const groups = workOf(conversation("s1"), view, 200);

  assert.deepEqual(keysOf(groups[0]), [`epic:${repository}/unseen`]);
});

test("WorkRail: an epic that asks a question is attention, and its state is the question", () => {
  const epic = makeEpic("first", { runningSteps: [makeStep("r1")] });
  const view = epicsView([epic], new Map([detailed(epic, { questions: [{ id: "Q1", text: "Use Postgres?" }] })]));

  const thing = workOf(conversation("s1"), view, 0)[0].things[0] as EpicThing;

  assert.equal(thing.asks, true);
  assert.equal(thing.tone, "attention");
  assert.equal(thing.state, "first asks: Use Postgres?");
});

test("WorkRail: a running epic is live, and one with nothing running is quiet and does not ask", () => {
  const view = epicsView(
    [makeEpic("busy", { runningSteps: [makeStep("r1")] }), makeEpic("idle")],
    new Map(),
  );

  const [busy, idle] = workOf(conversation("s1"), view, 0)[0].things as EpicThing[];

  assert.equal(busy.tone, "live");
  assert.equal(busy.asks, false);
  assert.equal(idle.tone, "quiet");
  assert.equal(idle.asks, false);
});

test("WorkRail: the short name is the slug past its repository, up to the first dash", () => {
  assert.equal(shortName("rota-export", "lighthouse"), "rota");
  assert.equal(shortName("orrery-followups", "orrery"), "followups");
  assert.equal(shortName("first", "widgets-5d0c1a7"), "first");

  const epic = makeEpic("orrery-followups", { repository: "orrery", id: "orrery/orrery-followups" });
  const thing = workOf(conversation("s1"), epicsView([epic], new Map()), 0)[0].things[0] as EpicThing;
  assert.equal(thing.short, "followups");
});

test("WorkRail: the gauge counts the done tasks, the started steps and all the tasks", () => {
  const epic = makeEpic("first");
  const tasks = [
    { effort: 1, slug: "t0", subject: "Subject t0", status: "done" },
    { effort: 1, slug: "t1", subject: "Subject t1", status: "pending" },
    { effort: 1, slug: "t2", subject: "Subject t2", status: "pending" },
  ];
  const view = epicsView([epic], new Map([detailed(epic, { tasks, steps: [makeStep("s1", { task: "t2" })] })]));

  const thing = workOf(conversation("s1"), view, 0)[0].things[0] as EpicThing;

  assert.deepEqual(thing.gauge, { done: 1, started: 1, total: 3 });
});

test("WorkLists: agent and shell tasks sit in their own groups, with their counts", () => {
  const agents = [task("agent", "running", "a1"), task("agent", "running", "a2")];
  const shell = task("shell", "running", "sh1");

  const groups = workOf(conversation("s1", [...agents, shell]), noEpics, 0);

  assert.deepEqual(
    groups.map((group) => group.kind),
    ["agents", "running"],
  );
  const agentThing = groups[0].things[0] as ListThing;
  assert.equal(agentThing.key, "agents");
  assert.equal(agentThing.count, 2);
  assert.equal(agentThing.tone, "live");
  assert.deepEqual(agentThing.tasks, agents);
  const shellThing = groups[1].things[0] as ListThing;
  assert.equal(shellThing.key, "running");
  assert.equal(shellThing.count, 1);
  assert.equal(shellThing.tone, "quiet");
  assert.deepEqual(shellThing.tasks, [shell]);
});

test("WorkLists: ended tasks are left out, and a list with none running has no group", () => {
  const running = task("agent", "running", "a1");

  const groups = workOf(
    conversation("s1", [running, task("agent", "done", "a2"), task("shell", "failed", "sh1")]),
    noEpics,
    0,
  );

  assert.deepEqual(groups.length, 1);
  assert.equal(groups[0].kind, "agents");
  assert.equal((groups[0].things[0] as ListThing).count, 1);
  assert.deepEqual((groups[0].things[0] as ListThing).tasks, [running]);
});

test("WorkLists: a pane conversation has no list groups whatever its background holds", () => {
  const groups = workOf(
    conversation("s1", [task("agent", "running", "a1"), task("shell", "running", "sh1")], true),
    noEpics,
    0,
  );

  assert.deepEqual(groups, []);
});

// makeContract is a proposed contract that session s1 proposed, from the repository tidepool, with no open decisions.
function makeContract(id: string, init: MessageInitShape<typeof ContractSchema> = {}): Contract {
  return create(ContractSchema, {
    id,
    repository: "/home/owner/code/tidepool",
    title: "Tide alerts",
    care: "limited",
    state: "proposed",
    version: 1n,
    openDecisions: 0n,
    conversation: "s1",
    ...init,
  });
}

test("ContractTile: a proposed contract of this session is a thing with its key, slug, short name and title", () => {
  const contract = makeContract("tidepool/tide-alerts");

  assert.deepEqual(contractThingsOf("s1", [contract]), [
    {
      kind: "contract",
      key: "contract:tidepool/tide-alerts",
      id: "tidepool/tide-alerts",
      slug: "tide-alerts",
      short: "tide",
      asks: false,
      tone: "attention",
      title: "tide-alerts: a contract waits for you",
    },
  ]);
});

test("ContractTile: asks while a decision is open, and not once none is", () => {
  const open = makeContract("tidepool/open", { openDecisions: 2n });
  const settled = makeContract("tidepool/settled", { openDecisions: 0n });

  const [openThing, settledThing] = contractThingsOf("s1", [open, settled]);

  assert.equal(openThing.asks, true);
  assert.equal(settledThing.asks, false);
});

test("ContractTile: a contract another conversation proposed is not a thing", () => {
  assert.deepEqual(contractThingsOf("s1", [makeContract("tidepool/theirs", { conversation: "s2" })]), []);
});

test("ContractTile: an accepted or set-aside contract is not a thing", () => {
  const contracts = [
    makeContract("tidepool/accepted", { state: "accepted" }),
    makeContract("tidepool/set-aside", { state: "set_aside" }),
  ];

  assert.deepEqual(contractThingsOf("s1", contracts), []);
});

test("ContractTile: an empty session has no contract things, even a contract whose conversation is empty", () => {
  const contract = makeContract("tidepool/orphan", { conversation: "" });

  assert.deepEqual(contractThingsOf("", [contract]), []);
});

test("ContractTile: the contract things keep the List order", () => {
  const contracts = ["tidepool/b", "tidepool/a", "tidepool/c"].map((id) => makeContract(id));

  assert.deepEqual(
    contractThingsOf("s1", contracts).map((thing) => thing.key),
    ["contract:tidepool/b", "contract:tidepool/a", "contract:tidepool/c"],
  );
});
