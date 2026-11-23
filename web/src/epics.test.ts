import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicBuildTaskSchema,
  EpicRequirementSchema,
  EpicSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  type Epic,
  type EpicsGetResponse,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import {
  endedWords,
  epicLine,
  epicsView,
  noEpics,
  repositoryName,
  type EpicBlock,
  type EpicItem,
  type EpicLine,
  type StripTone,
} from "./epics";

function makeEpic(init: MessageInitShape<typeof EpicSchema>): Epic {
  return create(EpicSchema, {
    id: "widgets-5d0c1a7/first",
    repository: "widgets-5d0c1a7",
    slug: "first",
    outcome: "",
    live: true,
    source: "orreryd",
    ...init,
  });
}

function makeStep(init: MessageInitShape<typeof EpicStepSchema>): EpicStep {
  return create(EpicStepSchema, { task: "", agent: "builder", doing: "", held: false, ...init });
}

// A Get response whose tasks carry the given statuses, one task per status, each integrated when its status is done or
// integrated and planned otherwise, as the server stands a record task with no step.
function detailWith(epic: Epic, statuses: string[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: statuses.map((status, index) => ({
      effort: 1,
      slug: `task-${index}`,
      subject: `Subject ${index}`,
      status,
      ...(["done", "integrated"].includes(status.toLowerCase())
        ? { place: "integrated", tone: "ok" }
        : { place: "planned", tone: "plan" }),
    })),
  });
}

// A record task of a Get response: its slug and status, and where the server stands it.
interface TaskOf {
  slug: string;
  status: string;
  place?: string;
  tone?: string;
}

// A Get response whose record tasks are tasks, each planned unless it says where it stands, and whose steps are given.
function detailOfTasks(epic: Epic, tasks: TaskOf[], steps: EpicStep[] = []): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: tasks.map((task) => ({ effort: 1, subject: task.slug, place: "planned", tone: "plan", ...task })),
    steps,
  });
}

function blockIds(blocks: EpicBlock[]): string[] {
  return blocks.map((block) => block.epic.id);
}

function itemIds(items: EpicItem[]): string[] {
  return items.map((item) => item.id);
}

test("the done count takes done and integrated tasks, and an unknown status counts as neither", () => {
  const epic = makeEpic({ id: "widgets-5d0c1a7/first" });
  const statuses = ["done", "Integrated", "pending", "frobnicated", "done at 3f9c2e1"];
  const details = new Map([[epic.id, detailWith(epic, statuses)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.equal(block.epic.detail !== undefined, true);
  assert.equal(block.epic.done, 2);
  assert.equal(block.epic.total, 5);
});

test("an epic with no detail carries none, and its counts read zero", () => {
  const epic = makeEpic({ id: "widgets-5d0c1a7/second" });

  const block = epicsView([epic], new Map()).blocks[0];

  assert.equal(block.epic.key, "epic:widgets-5d0c1a7/second");
  assert.equal(block.epic.detail === undefined, true);
  assert.equal(block.epic.done, 0);
  assert.equal(block.epic.total, 0);
  assert.deepEqual(block.epic.strip, []);
});

test("StripAgreesOrrery: the strip gives each record task the strip tone of the tone the server stands it in, in record order", () => {
  const epic = makeEpic({});
  const tasks = [
    { slug: "t0", status: "done", place: "integrated", tone: "ok" },
    { slug: "t1", status: "gap", place: "green", tone: "bad" },
    { slug: "t2", status: "dispatched", place: "implement", tone: "run" },
    { slug: "t3", status: "dispatched", place: "implement", tone: "mine" },
    { slug: "t4", status: "dispatched", place: "implement", tone: "limited" },
    { slug: "t5", status: "gated", place: "integrate", tone: "acc" },
    { slug: "t6", status: "pending" },
  ];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.deepEqual(block.epic.strip, ["passed", "errored", "now", "held", "held", "accepted", "none"]);
});

test("StripAgreesOrrery: the strip reads no step: a task the server stands running is now though its latest step ended goal_unmet", () => {
  const epic = makeEpic({});
  const tasks = [{ slug: "t0", status: "dispatched", place: "green", tone: "run" }];
  const steps = [makeStep({ id: "s1", task: "t0", end: "goal_unmet" })];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks, steps)]]);

  assert.deepEqual(epicsView([epic], details).blocks[0].epic.strip, ["now"]);
});

// A Get response of an orreryd epic whose build tasks are tasks.
function detailOfBuild(epic: Epic, tasks: MessageInitShape<typeof EpicBuildTaskSchema>[], steps: EpicStep[] = []): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic, buildTasks: tasks, steps });
}

test("a step's pips are its task's lifecycle: ok before the task's place, its tone there, none after, with no holes", () => {
  const epic = makeEpic({
    runningSteps: [makeStep({ id: "s1", task: "T1" }), makeStep({ id: "s2", task: "T2" }), makeStep({ id: "s3", task: "T3" })],
  });
  const detail = detailOfBuild(epic, [
    { id: "T1", status: "open", stage: "implement", place: "implement", tone: "run" },
    { id: "T2", status: "open", stage: "review", place: "review", tone: "mine" },
    { id: "T3", status: "open", stage: "red", place: "red", tone: "limited" },
  ]);

  const [implementing, held, limited] = epicsView([epic], new Map([[epic.id, detail]])).blocks[0].steps;

  assert.deepEqual(implementing.pips, ["ok", "run", "none", "none", "none"]);
  assert.deepEqual(held.pips, ["ok", "ok", "ok", "mine", "none"]);
  assert.deepEqual(limited.pips, ["limited", "none", "none", "none", "none"]);
});

test("a step of an accepted task whose first attempt ended goal_unmet draws the task accepted at integrate, not failed", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s3", task: "T5", readOnly: true })] });
  const detail = detailOfBuild(
    epic,
    [{ id: "T5", status: "done", stage: "review", place: "integrate", tone: "acc" }],
    [
      makeStep({ id: "s1", task: "T5", end: "goal_unmet" }),
      makeStep({ id: "s2", task: "T5", end: "done", readOnly: true }),
    ],
  );

  const [step] = epicsView([epic], new Map([[epic.id, detail]])).blocks[0].steps;

  assert.deepEqual(step.pips, ["ok", "ok", "ok", "ok", "acc"]);
});

test("a retrying task's step draws running, and a gap's step draws failed at the gap's place", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s2", task: "T1" }), makeStep({ id: "s3", task: "T2" })] });
  const detail = detailOfBuild(
    epic,
    [
      { id: "T1", status: "open", stage: "implement", place: "implement", tone: "run" },
      { id: "T2", status: "gap", stage: "green", place: "green", tone: "bad" },
    ],
    [makeStep({ id: "s1", task: "T1", end: "checks_failed" })],
  );

  const [retrying, gap] = epicsView([epic], new Map([[epic.id, detail]])).blocks[0].steps;

  assert.deepEqual(retrying.pips, ["ok", "run", "none", "none", "none"]);
  assert.deepEqual(gap.pips, ["ok", "ok", "bad", "none", "none"]);
});

test("an evidence judge step draws the pips of the task whose red or green run it judges, and a step of no task five none", () => {
  const epic = makeEpic({
    runningSteps: [
      makeStep({ id: "s1", task: "evidence-run-red-1", readOnly: true }),
      makeStep({ id: "s2", task: "evidence-run-green-2", readOnly: true }),
      makeStep({ id: "s3", task: "evidence-run-other" }),
      makeStep({ id: "s4", task: "" }),
    ],
  });
  const detail = detailOfBuild(epic, [
    { id: "T1", status: "open", stage: "red", place: "red", tone: "run", red: { id: "run-red-1", phase: "red" } },
    { id: "T2", status: "open", stage: "green", place: "green", tone: "run", green: { id: "run-green-2", phase: "green" } },
  ]);

  const [red, green, other, untasked] = epicsView([epic], new Map([[epic.id, detail]])).blocks[0].steps;

  assert.deepEqual(red.pips, ["run", "none", "none", "none", "none"]);
  assert.deepEqual(green.pips, ["ok", "ok", "run", "none", "none"]);
  assert.deepEqual(other.pips, ["none", "none", "none", "none", "none"]);
  assert.deepEqual(untasked.pips, ["none", "none", "none", "none", "none"]);
});

test("a held step carries its reason", () => {
  const epic = makeEpic({
    runningSteps: [makeStep({ id: "s1", held: true, reason: "waiting on review" }), makeStep({ id: "s2" })],
  });

  const [held, running] = epicsView([epic], new Map()).blocks[0].steps;

  assert.equal(held.held, true);
  assert.equal(held.reason, "waiting on review");
  assert.equal(running.reason, "");
});

test("a full block's tail reads its gaps, its not-started tasks and its ended steps", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s1", task: "t2" })], endedSteps: 9 });
  const tasks = [
    { slug: "t0", status: "gap", place: "implement", tone: "bad" },
    { slug: "t1", status: "pending" },
    { slug: "t2", status: "dispatched", place: "implement", tone: "run" },
    { slug: "t3", status: "pending" },
  ];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks)]]);

  assert.equal(epicsView([epic], details).blocks[0].tail, "1 gap · 2 not started · 9 steps ended");
});

test("StripAgreesOrrery: on an epic whose tasks are its record tasks the tail's gap count equals the errored segments", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "run" })] });
  const tasks = [
    { slug: "t0", status: "gap", place: "green", tone: "bad" },
    { slug: "t1", status: "gated", place: "integrate", tone: "acc" },
    { slug: "t2", status: "gap", place: "implement", tone: "bad" },
    { slug: "t3", status: "done", place: "integrated", tone: "ok" },
    { slug: "t4", status: "pending" },
  ];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.equal(block.epic.strip.filter((tone) => tone === "errored").length, 2);
  assert.equal(block.tail, "2 gap · 1 not started");
});

test("StripAgreesOrrery: an epic with no record tasks reads one gap for a gap build task, and none for a retrying one", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "run" })] });
  const steps = [makeStep({ id: "s1", task: "T1", end: "goal_unmet" }), makeStep({ id: "s2", task: "T2", end: "goal_unmet" })];
  const detail = detailOfBuild(
    epic,
    [
      { id: "T1", title: "Parser", status: "gap", stage: "implement", requirements: ["R1"], place: "implement", tone: "bad" },
      { id: "T2", title: "Lexer", status: "open", stage: "implement", requirements: ["R1"], place: "implement", tone: "run" },
    ],
    steps,
  );

  assert.equal(epicsView([epic], new Map([[epic.id, detail]])).blocks[0].tail, "1 gap");
});

test("a tail leaves out each part at zero, and is empty when no part is left", () => {
  const onlyEnded = makeEpic({ runningSteps: [makeStep({ id: "s1" })], endedSteps: 4 });
  const nothing = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const unstarted = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const tasks = [
    { slug: "a", status: "pending" },
    { slug: "b", status: "pending" },
  ];
  const details = new Map([[unstarted.id, detailOfTasks(unstarted, tasks)]]);

  assert.equal(epicsView([onlyEnded], new Map()).blocks[0].tail, "4 steps ended");
  assert.equal(epicsView([nothing], new Map()).blocks[0].tail, "");
  assert.equal(epicsView([unstarted], details).blocks[0].tail, "2 not started");
});

test("a one-line block has no steps, no more and an empty tail, even with ended steps", () => {
  const epic = makeEpic({ endedSteps: 3 });

  const block = epicsView([epic], new Map()).blocks[0];

  assert.equal(block.full, false);
  assert.deepEqual(block.steps, []);
  assert.equal(block.more, 0);
  assert.equal(block.tail, "");
});

test("five running steps show four, count one more, keep a held mark, and carry the tail", () => {
  const epic = makeEpic({
    runningSteps: [
      makeStep({ id: "s1", task: "first task" }),
      makeStep({ id: "s2", task: "", agent: "reviewer", held: true, doing: "waiting" }),
      makeStep({ id: "s3", task: "third task" }),
      makeStep({ id: "s4", task: "fourth task" }),
      makeStep({ id: "s5", task: "fifth task" }),
    ],
    endedSteps: 3,
  });

  const block = epicsView([epic], new Map()).blocks[0];

  assert.deepEqual(
    block.steps.map((step) => step.id),
    ["s1", "s2", "s3", "s4"],
  );
  assert.equal(block.full, true);
  assert.equal(block.more, 1);
  assert.equal(block.tail, "3 steps ended");
  assert.deepEqual(block.steps[0], {
    kind: "step",
    key: "step:s1",
    id: "s1",
    epic: "widgets-5d0c1a7/first",
    epicSlug: "first",
    label: "first task",
    doing: "",
    held: false,
    agent: "builder",
    started: 0,
    round: 1,
    pips: ["none", "none", "none", "none", "none"],
    reason: "",
    step: epic.runningSteps[0],
  });
  assert.deepEqual(block.steps[1], {
    kind: "step",
    key: "step:s2",
    id: "s2",
    epic: "widgets-5d0c1a7/first",
    epicSlug: "first",
    label: "reviewer",
    doing: "waiting",
    held: true,
    agent: "reviewer",
    started: 0,
    round: 1,
    pips: ["none", "none", "none", "none", "none"],
    reason: "",
    step: epic.runningSteps[1],
  });
});

test("a step carries the running EpicStep it is made from, with its checks", () => {
  const running = makeStep({
    id: "s1",
    checks: [{ round: 1, name: "lint", ok: false }],
  });
  const epic = makeEpic({ runningSteps: [running] });

  const step = epicsView([epic], new Map()).blocks[0].steps[0];

  assert.equal(step.step, running);
  assert.equal(step.step.checks.length, 1);
  assert.equal(step.step.checks[0].name, "lint");
});

test("a step carries its epic's slug, its agent and its start", () => {
  const epic = makeEpic({
    runningSteps: [makeStep({ id: "s1", agent: "reviewer", started: 1786000000n })],
  });

  const step = epicsView([epic], new Map()).blocks[0].steps[0];

  assert.equal(step.epicSlug, "first");
  assert.equal(step.agent, "reviewer");
  assert.equal(step.started, 1786000000);
});

test("a running step with no checks is round 1", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });

  assert.equal(epicsView([epic], new Map()).blocks[0].steps[0].round, 1);
});

test("a running step whose checks reach round 2 is round 3", () => {
  const epic = makeEpic({
    runningSteps: [
      makeStep({
        id: "s1",
        checks: [
          { round: 1, name: "tests", ok: false },
          { round: 2, name: "tests", ok: true },
        ],
      }),
    ],
  });

  assert.equal(epicsView([epic], new Map()).blocks[0].steps[0].round, 3);
});

test("an ended step whose checks reach round 2 is round 2", () => {
  const epic = makeEpic({
    runningSteps: [
      makeStep({
        id: "s1",
        end: "done",
        checks: [
          { round: 1, name: "tests", ok: false },
          { round: 2, name: "tests", ok: true },
        ],
      }),
    ],
  });

  assert.equal(epicsView([epic], new Map()).blocks[0].steps[0].round, 2);
});

test("live epics become blocks in input order, and each epic that is not live is listed among the ended", () => {
  const epics = [
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "widgets-5d0c1a7/old", live: false, outcome: "released" }),
    makeEpic({ id: "gadgets-0e1f2a3/second" }),
    makeEpic({ id: "gadgets-0e1f2a3/older", live: false }),
  ];

  const view = epicsView(epics, new Map());

  assert.deepEqual(blockIds(view.blocks), ["widgets-5d0c1a7/first", "gadgets-0e1f2a3/second"]);
  assert.deepEqual(itemIds(view.endedEarlier), ["widgets-5d0c1a7/old", "gadgets-0e1f2a3/older"]);
  assert.deepEqual(view.justEnded, []);
  assert.equal(view.live, 2);
});

test("a released epic is listed among the ended, and the live epics around it are not", () => {
  const quiet = ["a", "b", "c", "d", "e", "f", "g"].map((slug) => makeEpic({ id: `widgets-5d0c1a7/${slug}`, slug }));
  const released = makeEpic({
    id: "gadgets-0e1f2a3/old",
    repository: "gadgets-0e1f2a3",
    slug: "old",
    outcome: "released",
    live: false,
  });

  const view = epicsView([...quiet.slice(0, 3), released, ...quiet.slice(3)], new Map());

  assert.deepEqual(
    view.endedEarlier.map((item) => item.slug),
    ["old"],
    "the released epic is among the ended, and no live epic is",
  );
  assert.equal(view.endedEarlier[0].outcome, "released");
  assert.equal(view.endedEarlier[0].detail === undefined, true, "an ended epic carries no detail it was not given");
});

test("ArchiveView: archived epics, live or ended, leave every default list and the live count, and are listed as archived in input order", () => {
  const view = epicsView(
    [
      makeEpic({ id: "widgets-5d0c1a7/archived-live", slug: "archived-live", archived: true }),
      makeEpic({ id: "widgets-5d0c1a7/first" }),
      makeEpic({
        id: "gadgets-0e1f2a3/archived-quiet",
        repository: "gadgets-0e1f2a3",
        slug: "archived-quiet",
        archived: true,
      }),
      makeEpic({
        id: "gadgets-0e1f2a3/quiet",
        repository: "gadgets-0e1f2a3",
        slug: "quiet",
        live: false,
      }),
      makeEpic({
        id: "widgets-5d0c1a7/archived-ended",
        slug: "archived-ended",
        source: "orreryd",
        live: false,
        phaseAt: 2000n,
        seen: 0n,
        archived: true,
      }),
    ],
    new Map(),
  );

  assert.deepEqual(blockIds(view.blocks), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(view.justEnded, [], "the archived ended epic is not unseen in Just ended");
  assert.deepEqual(itemIds(view.endedEarlier), ["gadgets-0e1f2a3/quiet"]);
  assert.equal(view.live, 1);
  assert.deepEqual(itemIds(view.archived), [
    "widgets-5d0c1a7/archived-live",
    "gadgets-0e1f2a3/archived-quiet",
    "widgets-5d0c1a7/archived-ended",
  ]);
  assert.deepEqual(
    view.archived.map((item) => item.archived),
    [true, true, true],
  );
  assert.equal(view.blocks[0].epic.archived, false);
});

test("ArchiveView: every live unarchived epic is listed, busy or quiet, and the archived one between them is not", () => {
  const view = epicsView(
    [
      makeEpic({ id: "widgets-5d0c1a7/a", slug: "a" }),
      makeEpic({ id: "widgets-5d0c1a7/hidden", slug: "hidden", archived: true }),
      makeEpic({
        id: "gadgets-0e1f2a3/b",
        repository: "gadgets-0e1f2a3",
        slug: "b",
        runningSteps: [makeStep({ id: "s1" })],
      }),
      makeEpic({ id: "widgets-5d0c1a7/c", slug: "c" }),
      makeEpic({ id: "widgets-5d0c1a7/d", slug: "d" }),
    ],
    new Map(),
  );

  assert.deepEqual(blockIds(view.blocks), [
    "gadgets-0e1f2a3/b",
    "widgets-5d0c1a7/a",
    "widgets-5d0c1a7/c",
    "widgets-5d0c1a7/d",
  ]);
  assert.equal(view.live, 4);
});

// An orreryd epic that is not live, ended at phaseAt and last seen at seen, unix seconds.
function endedOf(slug: string, phaseAt: bigint, seen: bigint): Epic {
  return makeEpic({
    id: `widgets-5d0c1a7/${slug}`,
    slug,
    source: "orreryd",
    live: false,
    outcome: "released",
    phaseAt,
    seen,
  });
}

test("an orreryd epic that ended unseen is in justEnded, and not in endedEarlier", () => {
  const view = epicsView([endedOf("first", 1000n, 999n)], new Map());

  assert.deepEqual(itemIds(view.justEnded), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(view.endedEarlier, []);
});

test("an orreryd epic seen at or after it ended is in endedEarlier, and one with no phaseAt reads seen", () => {
  const view = epicsView(
    [endedOf("at-end", 1000n, 1000n), endedOf("later", 1000n, 1500n), endedOf("no-phase", 0n, 0n)],
    new Map(),
  );

  assert.deepEqual(view.justEnded, []);
  assert.deepEqual(
    view.endedEarlier.map((item) => item.slug),
    ["at-end", "later", "no-phase"],
  );
});

test("an epic that went quiet with no phase time is listed in endedEarlier, never in justEnded", () => {
  const quiet = makeEpic({
    id: "gadgets-0e1f2a3/quiet",
    repository: "gadgets-0e1f2a3",
    slug: "quiet",
    live: false,
    outcome: "released",
  });

  const view = epicsView([quiet], new Map());

  assert.deepEqual(itemIds(view.endedEarlier), ["gadgets-0e1f2a3/quiet"]);
  assert.deepEqual(view.justEnded, []);
  assert.deepEqual(view.blocks, []);
});

test("justEnded and endedEarlier each list the newest ending first", () => {
  const view = epicsView(
    [
      endedOf("older", 1000n, 0n),
      endedOf("newest", 3000n, 0n),
      endedOf("middle", 2000n, 0n),
      endedOf("seen-old", 500n, 600n),
      endedOf("seen-new", 900n, 900n),
    ],
    new Map(),
  );

  assert.deepEqual(
    view.justEnded.map((item) => item.slug),
    ["newest", "middle", "older"],
  );
  assert.deepEqual(
    view.endedEarlier.map((item) => item.slug),
    ["seen-new", "seen-old"],
  );
});

test("seven of eight live epics with running or held work draw full in input order, and the quiet one after", () => {
  const quiet = makeEpic({ id: "widgets-5d0c1a7/quiet" });
  const busy = ["a", "b", "c", "d", "e", "f", "g"].map((slug, index) =>
    makeEpic({
      id: `gadgets-1a2b3c4/${slug}`,
      repository: "gadgets-1a2b3c4",
      runningSteps: [makeStep({ id: `s${index}`, held: index === 1 })],
    }),
  );

  const view = epicsView([quiet, ...busy], new Map());

  assert.deepEqual(blockIds(view.blocks), [...busy.map((epic) => epic.id), "widgets-5d0c1a7/quiet"]);
  assert.deepEqual(
    view.blocks.map((block) => block.full),
    [...busy.map(() => true), false],
  );
  assert.equal(view.live, 8);
});

test("two running epics and six quiet ones draw all eight, running first, then the quiet in input order", () => {
  const quiet = (slug: string) => makeEpic({ id: `widgets-5d0c1a7/${slug}` });
  const busy = (slug: string) =>
    makeEpic({ id: `gadgets-1a2b3c4/${slug}`, repository: "gadgets-1a2b3c4", runningSteps: [makeStep({ id: slug })] });
  const epics = [quiet("q1"), busy("r1"), quiet("q2"), quiet("q3"), quiet("q4"), quiet("q5"), quiet("q6"), busy("r2")];

  const view = epicsView(epics, new Map());

  assert.deepEqual(blockIds(view.blocks), [
    "gadgets-1a2b3c4/r1",
    "gadgets-1a2b3c4/r2",
    "widgets-5d0c1a7/q1",
    "widgets-5d0c1a7/q2",
    "widgets-5d0c1a7/q3",
    "widgets-5d0c1a7/q4",
    "widgets-5d0c1a7/q5",
    "widgets-5d0c1a7/q6",
  ]);
  assert.deepEqual(
    view.blocks.map((block) => block.full),
    [true, true, false, false, false, false, false, false],
  );
  assert.equal(view.live, 8);
});

test("no epics is an empty view", () => {
  assert.deepEqual(noEpics.blocks, []);
  assert.deepEqual(noEpics.archived, []);
  assert.equal(noEpics.details.size, 0);
});

test("repositoryName drops a trailing dash and seven lowercase hex digits", () => {
  assert.equal(repositoryName("widgets-5d0c1a7"), "widgets");
  assert.equal(repositoryName("gadget-factory-0e1f2a3"), "gadget-factory");
});

test("repositoryName returns a name without that suffix whole", () => {
  const names = [
    "widgets",
    "widgets-5d0c1a",
    "widgets-5d0c1a77",
    "widgets-5D0C1A7",
    "widgets5d0c1a7",
    "widgets-5d0c1a7x",
  ];
  for (const name of names) {
    assert.equal(repositoryName(name), name);
  }
});

test("the count is the requirements a passing green run proves, out of all of them", () => {
  const epic = makeEpic({});
  const requirements = [
    create(EpicRequirementSchema, { id: "R1", runs: [{ phase: "green", passed: true }] }),
    create(EpicRequirementSchema, { id: "R2", runs: [{ phase: "green", passed: false }] }),
    create(EpicRequirementSchema, { id: "R3", runs: [{ phase: "green", passed: true }] }),
  ];
  const detail = create(EpicsGetResponseSchema, { epic, requirements });

  assert.equal(lineOf(epic, detail).count, "2 of 3 proven");
});

test("a requirement with only a red run is not proven", () => {
  const epic = makeEpic({});
  const requirements = [create(EpicRequirementSchema, { id: "R1", runs: [{ phase: "red", passed: true }] })];
  const detail = create(EpicsGetResponseSchema, { epic, requirements });

  assert.equal(lineOf(epic, detail).count, "0 of 1 proven");
});

test("with no requirements the count is the record tasks done, out of all of them", () => {
  const epic = makeEpic({});
  const detail = detailWith(epic, ["done", "pending", "pending", "pending"]);

  assert.equal(lineOf(epic, detail).count, "1 of 4 tasks done");
});

test("the state is the state phrase with its time, then the units integrated", () => {
  const epic = makeEpic({ phaseAt: 97060n, outcome: "accepted" });
  const detail = detailWith(epic, ["done", "done", "pending", "pending", "pending", "pending", "pending"]);

  assert.equal(lineOf(epic, detail).state, "building · 49 min · 2 of 7 units integrated");
});

test("a state with no units has no units clause", () => {
  const epic = makeEpic({ phaseAt: 97060n, outcome: "accepted" });
  const detail = create(EpicsGetResponseSchema, { epic });

  assert.equal(lineOf(epic, detail).state, "planning · 49 min");
});

test("units alone read without a leading separator when the epic has no state phrase", () => {
  const epic = makeEpic({ live: false });
  const detail = detailWith(epic, ["done", "pending"]);

  assert.equal(lineOf(epic, detail).state, "1 of 2 units integrated");
});

test("the blocker of an open question is its wait with its time", () => {
  const epic = makeEpic({});
  const detail = create(EpicsGetResponseSchema, {
    epic,
    questions: [{ id: "Q1", text: "Which way?", asked: 99880n }],
  });

  assert.equal(lineOf(epic, detail).blocker, "waits on your answer · 2 min");
});

test("an epic with an open question never has an empty blocker, even when paused", () => {
  const epic = makeEpic({});
  const detail = create(EpicsGetResponseSchema, {
    epic,
    paused: true,
    questions: [{ id: "Q1", text: "Which way?", asked: 0n }],
  });

  assert.equal(lineOf(epic, detail).blocker, "waits on your answer");
});

test("an epic with no detail reads epic unavailable, and its state and blocker are empty", () => {
  assert.deepEqual(lineOf(makeEpic({}), undefined), { count: "epic unavailable", state: "", blocker: "" });
});

// The clock the epic line tests read at: unix seconds.
const lineNow = 100000;

function lineOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicLine {
  const details = detail === undefined ? new Map() : new Map([[epic.id, detail]]);
  const view = epicsView([epic], details);
  const item = [...view.blocks.map((block) => block.epic), ...view.justEnded, ...view.endedEarlier][0];
  return epicLine(item, lineNow);
}

test("endedWords reads the outcome, then the time since the epic ended", () => {
  const [item] = epicsView([endedOf("first", 99280n, 0n)], new Map()).justEnded;

  assert.equal(endedWords(item, lineNow), "released · 12 min ago");
});

test("endedWords reads the outcome alone when the epic has no phaseAt", () => {
  const [item] = epicsView([endedOf("first", 0n, 0n)], new Map()).endedEarlier;

  assert.equal(endedWords(item, lineNow), "released");
});

test("endedWords reads zero seconds when the clock is behind the epic's end", () => {
  const [item] = epicsView([endedOf("first", 100500n, 0n)], new Map()).justEnded;

  assert.equal(endedWords(item, lineNow), "released · 0 s ago");
});
