import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicRequirementSchema,
  EpicSchema,
  EpicSliceSchema,
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
    source: "record",
    ...init,
  });
}

function makeStep(init: MessageInitShape<typeof EpicStepSchema>): EpicStep {
  return create(EpicStepSchema, { task: "", agent: "builder", doing: "", held: false, ...init });
}

// A Get response whose tasks carry the given statuses, one task per status.
function detailWith(epic: Epic, statuses: string[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: statuses.map((status, index) => ({
      effort: 1,
      slug: `task-${index}`,
      subject: `Subject ${index}`,
      status,
    })),
  });
}

// A Get response whose tasks are the given slugs and statuses, and whose slices and steps are given as they are.
function detailOfTasks(
  epic: Epic,
  tasks: { slug: string; status: string }[],
  slices: MessageInitShape<typeof EpicSliceSchema>[],
  steps: EpicStep[] = [],
): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: tasks.map((task) => ({ effort: 1, subject: task.slug, ...task })),
    slices,
    steps,
  });
}

// Seven record tasks in record order. Four have slices, listed out of record order: t3 held, t0 done, t2 running,
// t1 gap, t6 done. t4 and t5 have none.
const sevenTasks = ["t0", "t1", "t2", "t3", "t4", "t5", "t6"].map((slug) => ({ slug, status: "pending" }));
const sevenSlices: MessageInitShape<typeof EpicSliceSchema>[] = [
  { task: "t3", hue: "dispatched", at: 4, held: true, redTest: true, working: true, testsPass: true, checks: true },
  { task: "t0", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true },
  { task: "t2", hue: "dispatched", at: 2, redTest: true },
  { task: "t1", hue: "gap", errorAt: 3, redTest: true, working: true },
  { task: "t6", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true },
];

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

test("StripAgreesOrrery: the strip gives each record task its tone in record order, matched by slug, none when no step matches; a not-done task whose latest step ended goal_unmet is errored, a running step is now, a held step is held and a task no step touched is none", () => {
  const epic = makeEpic({});
  const tasks = [
    { slug: "t0", status: "done" },
    { slug: "t1", status: "pending" },
    { slug: "t2", status: "pending" },
    { slug: "t3", status: "pending" },
    { slug: "t4", status: "pending" },
    { slug: "t5", status: "pending" },
    { slug: "t6", status: "pending" },
  ];
  const steps = [
    makeStep({ id: "s1", task: "t1", end: "goal_unmet" }),
    makeStep({ id: "s2", task: "t2" }),
    makeStep({ id: "s3", task: "t3", held: true }),
    makeStep({ id: "s4", task: "t5", end: "done" }),
  ];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks, [], steps)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.deepEqual(block.epic.strip, ["passed", "errored", "now", "held", "none", "accepted", "none"]);
});

test("a step's pips are the five cells of its slice, and five track without one", () => {
  const epic = makeEpic({
    runningSteps: [makeStep({ id: "s1", task: "t2" }), makeStep({ id: "s2", task: "t4" })],
  });
  const details = new Map([[epic.id, detailOfTasks(epic, sevenTasks, sevenSlices)]]);

  const [sliced, unsliced] = epicsView([epic], details).blocks[0].steps;

  assert.deepEqual(sliced.pips, ["passed", "now", "track", "track", "track"]);
  assert.deepEqual(unsliced.pips, ["track", "track", "track", "track", "track"]);
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

test("a full block's tail reads its gaps and its not-started tasks, and no ended steps", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s1", task: "t2" })], endedSteps: 9 });
  const tasks = [
    { slug: "t0", status: "pending" },
    { slug: "t1", status: "pending" },
    { slug: "t2", status: "pending" },
    { slug: "t3", status: "pending" },
  ];
  const steps = [makeStep({ id: "s2", task: "t0", end: "goal_unmet" }), makeStep({ id: "s3", task: "t3" })];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks, [], steps)]]);

  assert.equal(epicsView([epic], details).blocks[0].tail, "1 gap · 2 not started");
});

// The strip of an epic whose one record task, t0, has the given status and steps.
function stripOfOne(status: string, steps: EpicStep[]): StripTone[] {
  const epic = makeEpic({});
  const details = new Map([[epic.id, detailOfTasks(epic, [{ slug: "t0", status }], [], steps)]]);
  return epicsView([epic], details).blocks[0].epic.strip;
}

test("StripAgreesOrrery: a done record task whose latest writing step ended returned or goal_unmet is passed", () => {
  for (const end of ["returned", "goal_unmet"]) {
    assert.deepEqual(stripOfOne("done", [makeStep({ id: "s1", task: "t0", end })]), ["passed"], end);
  }
});

test("StripAgreesOrrery: an integrated record task whose latest writing step ended returned or goal_unmet is passed", () => {
  for (const end of ["returned", "goal_unmet"]) {
    assert.deepEqual(stripOfOne("integrated", [makeStep({ id: "s1", task: "t0", end })]), ["passed"], end);
  }
});

test("StripAgreesOrrery: a not-done record task whose writing step ended returned, and whose later read-only step ended done, is accepted, not errored", () => {
  const steps = [
    makeStep({ id: "s1", task: "t0", end: "returned" }),
    makeStep({ id: "s2", task: "t0", end: "done", readOnly: true }),
  ];

  assert.deepEqual(stripOfOne("pending", steps), ["accepted"]);
});

test("StripAgreesOrrery: on an epic whose wedges are its record tasks the tail's gap count equals the errored segments", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "run" })] });
  const tasks = [
    { slug: "t0", status: "pending" },
    { slug: "t1", status: "pending" },
    { slug: "t2", status: "pending" },
    { slug: "t3", status: "done" },
    { slug: "t4", status: "pending" },
  ];
  const steps = [
    makeStep({ id: "s1", task: "t0", end: "goal_unmet" }),
    makeStep({ id: "s2", task: "t1", end: "returned" }),
    makeStep({ id: "s3", task: "t1", end: "done", readOnly: true }),
    makeStep({ id: "s4", task: "t2", end: "checks_failed" }),
  ];
  const details = new Map([[epic.id, detailOfTasks(epic, tasks, [], steps)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.equal(block.epic.strip.filter((tone) => tone === "errored").length, 2);
  assert.equal(block.tail, "2 gap · 1 not started");
});

test("StripAgreesOrrery: an epic with no record tasks and one failed build task reads '1 gap'", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "run" })] });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    buildTasks: [{ id: "T1", title: "Parser", status: "open", stage: "implement", requirements: ["R1"] }],
    steps: [makeStep({ id: "s1", task: "T1", end: "goal_unmet" })],
  });

  assert.equal(epicsView([epic], new Map([[epic.id, detail]])).blocks[0].tail, "1 gap");
});

test("a tail leaves out each part at zero, and is empty when no part is left, ended steps included", () => {
  const onlyEnded = makeEpic({ runningSteps: [makeStep({ id: "s1" })], endedSteps: 4 });
  const nothing = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const unstarted = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const tasks = [
    { slug: "a", status: "pending" },
    { slug: "b", status: "pending" },
  ];
  const details = new Map([[unstarted.id, detailOfTasks(unstarted, tasks, [])]]);

  assert.equal(epicsView([onlyEnded], new Map()).blocks[0].tail, "");
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

test("five running steps show four, count one more, keep a held mark, and leave ended steps out of the tail", () => {
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
  assert.equal(block.tail, "");
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
    pips: ["track", "track", "track", "track", "track"],
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
    pips: ["track", "track", "track", "track", "track"],
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

test("a released record epic is listed among the ended, and the live epics around it are not", () => {
  const quiet = ["a", "b", "c", "d", "e", "f", "g"].map((slug) => makeEpic({ id: `widgets-5d0c1a7/${slug}`, slug }));
  const released = makeEpic({
    id: "gadgets-0e1f2a3/old",
    repository: "gadgets-0e1f2a3",
    slug: "old",
    outcome: "released",
    live: false,
  });
  const waiting = makeEpic({ id: "gadgets-0e1f2a3/next", repository: "gadgets-0e1f2a3", slug: "next", waiting: true });

  const view = epicsView([...quiet.slice(0, 3), released, ...quiet.slice(3), waiting], new Map());

  assert.deepEqual(
    view.endedEarlier.map((item) => item.slug),
    ["old"],
    "the released record epic is among the ended, and no live epic is",
  );
  assert.equal(view.endedEarlier[0].outcome, "released");
  assert.equal(view.endedEarlier[0].detail === undefined, true, "an ended epic carries no detail it was not given");
  assert.deepEqual(
    view.waiting.map((item) => item.slug),
    ["next"],
  );
});

test("waiting epics leave the blocks for their own list, in input order, and are neither drawn nor ended", () => {
  const epics = [
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "widgets-5d0c1a7/waiting-one", slug: "waiting-one", waiting: true }),
    makeEpic({ id: "gadgets-0e1f2a3/old", repository: "gadgets-0e1f2a3", live: false }),
    makeEpic({ id: "gadgets-0e1f2a3/waiting-two", repository: "gadgets-0e1f2a3", slug: "waiting-two", waiting: true }),
  ];

  const view = epicsView(epics, new Map());

  assert.deepEqual(blockIds(view.blocks), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(
    view.waiting.map((item) => item.slug),
    ["waiting-one", "waiting-two"],
  );
  assert.equal(view.live, 3);
  assert.deepEqual(itemIds(view.endedEarlier), ["gadgets-0e1f2a3/old"]);
});

test("ArchiveView: archived epics, live or ended, leave every default list and the live count, and are listed as archived in input order", () => {
  const view = epicsView(
    [
      makeEpic({ id: "widgets-5d0c1a7/archived-live", slug: "archived-live", archived: true }),
      makeEpic({ id: "widgets-5d0c1a7/first" }),
      makeEpic({
        id: "gadgets-0e1f2a3/archived-waiting",
        repository: "gadgets-0e1f2a3",
        slug: "archived-waiting",
        waiting: true,
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
  assert.deepEqual(view.waiting, []);
  assert.deepEqual(view.justEnded, [], "the archived ended epic is not unseen in Just ended");
  assert.deepEqual(itemIds(view.endedEarlier), ["gadgets-0e1f2a3/quiet"]);
  assert.equal(view.live, 1);
  assert.deepEqual(itemIds(view.archived), [
    "widgets-5d0c1a7/archived-live",
    "gadgets-0e1f2a3/archived-waiting",
    "widgets-5d0c1a7/archived-ended",
  ]);
  assert.deepEqual(
    view.archived.map((item) => item.archived),
    [true, true, true],
  );
  assert.equal(view.blocks[0].epic.archived, false);
});

test("ArchiveView: every live unarchived epic is listed, busy, quiet or waiting, and the archived one between them is not", () => {
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
      makeEpic({ id: "widgets-5d0c1a7/c", slug: "c", waiting: true }),
      makeEpic({ id: "widgets-5d0c1a7/d", slug: "d" }),
    ],
    new Map(),
  );

  assert.deepEqual(blockIds(view.blocks), ["gadgets-0e1f2a3/b", "widgets-5d0c1a7/a", "widgets-5d0c1a7/d"]);
  assert.deepEqual(
    view.waiting.map((item) => item.slug),
    ["c"],
  );
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

test("a record epic that went quiet is listed among the ended, in endedEarlier, as it has no phase time", () => {
  const quiet = makeEpic({
    id: "gadgets-0e1f2a3/quiet",
    repository: "gadgets-0e1f2a3",
    slug: "quiet",
    source: "record",
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
  assert.deepEqual(noEpics.waiting, []);
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

test("the count is the requirements the server states as proven, out of all of them", () => {
  const epic = makeEpic({});
  const requirements = [
    create(EpicRequirementSchema, { id: "R1", state: "proven" }),
    create(EpicRequirementSchema, { id: "R2", state: "failed" }),
    create(EpicRequirementSchema, { id: "R3", state: "proven" }),
  ];
  const detail = create(EpicsGetResponseSchema, { epic, requirements });

  assert.equal(lineOf(epic, detail).count, "2 of 3 proven");
});

test("a requirement the server does not state as proven is not counted, whatever its green run says", () => {
  const epic = makeEpic({});
  const requirements = [
    create(EpicRequirementSchema, { id: "R1", state: "in_flight", runs: [{ phase: "green", passed: true }] }),
  ];
  const detail = create(EpicsGetResponseSchema, { epic, requirements });

  assert.equal(lineOf(epic, detail).count, "0 of 1 proven");
});

test("with no requirements the count is the record tasks done, out of all of them", () => {
  const epic = makeEpic({});
  const detail = detailWith(epic, ["done", "pending", "pending", "pending"]);

  assert.equal(lineOf(epic, detail).count, "1 of 4 tasks done");
});

test("the state is the state phrase with its time, then the units integrated", () => {
  const epic = makeEpic({ phaseAt: 97060n });
  const detail = detailWith(epic, ["done", "done", "pending", "pending", "pending", "pending", "pending"]);

  assert.equal(lineOf(epic, detail).state, "building · 49 min · 2 of 7 units integrated");
});

test("a state with no units has no units clause", () => {
  const epic = makeEpic({ phaseAt: 97060n });
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

test("an epic with an open question never has an empty blocker, even when paused and waiting", () => {
  const epic = makeEpic({ waiting: true });
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
  const item = [...view.blocks.map((block) => block.epic), ...view.waiting, ...view.justEnded, ...view.endedEarlier][0];
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
