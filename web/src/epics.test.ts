import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicSchema,
  EpicSliceSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  type Epic,
  type EpicsGetResponse,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import { epicsView, noEpics, placeEpics, repositoryName, withEpicsGroup, type EpicBlock } from "./epics";
import type { Row, Section } from "./rows";

function makeEpic(init: MessageInitShape<typeof EpicSchema>): Epic {
  return create(EpicSchema, {
    id: "widgets-5d0c1a7/first",
    repository: "widgets-5d0c1a7",
    slug: "first",
    outcome: "",
    live: true,
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

// A Get response whose tasks are the given slugs and statuses, and whose slices are given as they are.
function detailOfTasks(
  epic: Epic,
  tasks: { slug: string; status: string }[],
  slices: MessageInitShape<typeof EpicSliceSchema>[],
): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: tasks.map((task) => ({ effort: 1, subject: task.slug, ...task })),
    slices,
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

function makeRow(key: string, project: string): Row {
  return {
    key,
    id: key,
    kind: "pane",
    label: key,
    project,
    host: "box",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
  };
}

function titlesOf(sections: { title: string }[]): string[] {
  return sections.map((section) => section.title);
}

function blockIds(blocks: EpicBlock[]): string[] {
  return blocks.map((block) => block.epic.id);
}

test("the gauge counts done and integrated tasks, and an unknown status counts as neither", () => {
  const epic = makeEpic({ id: "widgets-5d0c1a7/first" });
  const statuses = ["done", "Integrated", "pending", "frobnicated", "done at 3f9c2e1"];
  const details = new Map([[epic.id, detailWith(epic, statuses)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.equal(block.epic.known, true);
  assert.equal(block.epic.done, 2);
  assert.equal(block.epic.total, 5);
});

test("an epic with no detail is unknown, and its gauge reads zero", () => {
  const epic = makeEpic({ id: "widgets-5d0c1a7/second" });

  const block = epicsView([epic], new Map()).blocks[0];

  assert.equal(block.epic.key, "epic:widgets-5d0c1a7/second");
  assert.equal(block.epic.known, false);
  assert.equal(block.epic.done, 0);
  assert.equal(block.epic.total, 0);
  assert.deepEqual(block.epic.strip, []);
});

test("the strip gives each record task its tone in record order, matched by slug, none when no slice matches", () => {
  const epic = makeEpic({});
  const details = new Map([[epic.id, detailOfTasks(epic, sevenTasks, sevenSlices)]]);

  const block = epicsView([epic], details).blocks[0];

  assert.deepEqual(block.epic.strip, ["passed", "errored", "now", "held", "none", "none", "passed"]);
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

test("a full block's tail reads its gaps, its not-started tasks and its ended steps", () => {
  const epic = makeEpic({ runningSteps: [makeStep({ id: "s1", task: "t2" })], endedSteps: 9 });
  const details = new Map([[epic.id, detailOfTasks(epic, sevenTasks, sevenSlices)]]);

  assert.equal(epicsView([epic], details).blocks[0].tail, "1 gap · 2 not started · 9 steps ended");
});

test("a tail leaves out each part at zero, and is empty when no part is left", () => {
  const onlyEnded = makeEpic({ runningSteps: [makeStep({ id: "s1" })], endedSteps: 4 });
  const nothing = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const unstarted = makeEpic({ runningSteps: [makeStep({ id: "s1" })] });
  const tasks = [
    { slug: "a", status: "pending" },
    { slug: "b", status: "pending" },
  ];
  const details = new Map([[unstarted.id, detailOfTasks(unstarted, tasks, [])]]);

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

test("live epics become blocks in input order and every other epic is folded", () => {
  const epics = [
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "widgets-5d0c1a7/old", live: false, outcome: "released" }),
    makeEpic({ id: "gadgets-0e1f2a3/second" }),
    makeEpic({ id: "gadgets-0e1f2a3/older", live: false }),
  ];

  const view = epicsView(epics, new Map());

  assert.deepEqual(blockIds(view.blocks), ["widgets-5d0c1a7/first", "gadgets-0e1f2a3/second"]);
  assert.equal(view.folded, 2);
  assert.equal(view.live, 2);
});

test("seven of eight live epics with running or held work draw full in input order, and the quiet one folds", () => {
  const quiet = makeEpic({ id: "widgets-5d0c1a7/quiet" });
  const busy = ["a", "b", "c", "d", "e", "f", "g"].map((slug, index) =>
    makeEpic({
      id: `gadgets-1a2b3c4/${slug}`,
      repository: "gadgets-1a2b3c4",
      runningSteps: [makeStep({ id: `s${index}`, held: index === 1 })],
    }),
  );

  const view = epicsView([quiet, ...busy], new Map());

  assert.deepEqual(blockIds(view.blocks), busy.map((epic) => epic.id));
  assert.deepEqual(
    view.blocks.map((block) => block.full),
    busy.map(() => true),
  );
  assert.equal(view.folded, 1);
  assert.equal(view.live, 8);
  assert.equal(withEpicsGroup([], view)[0].count, 8, "the Epics group counts all eight live epics");
});

test("two running epics and six quiet ones draw the two full, then four quiet, and fold two", () => {
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
  ]);
  assert.deepEqual(
    view.blocks.map((block) => block.full),
    [true, true, false, false, false, false],
  );
  assert.equal(view.folded, 2);
  assert.equal(view.live, 8);
});

test("a section's count is its rows and the epics atop it, and the Epics group counts every live epic", () => {
  const view = epicsView(
    [makeEpic({ runningSteps: [makeStep({ id: "s1" })] }), makeEpic({ id: "widgets-5d0c1a7/second" })],
    new Map(),
  );
  const rows = [makeRow("a", "/work/widgets"), makeRow("b", "/work/widgets")];
  const sections: Section[] = [{ title: "/work/widgets", rows }];

  const placed = placeEpics(sections, view.blocks);
  const grouped = withEpicsGroup(sections, view);

  assert.equal(placed[0].count, 4);
  assert.deepEqual(titlesOf(grouped), ["Epics", "/work/widgets"]);
  assert.equal(grouped[0].count, 2);
  assert.equal(grouped[1].count, 2);
});

test("no epics is an empty view", () => {
  assert.deepEqual(noEpics.blocks, []);
  assert.equal(noEpics.folded, 0);
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

test("the Epics group follows the Needs you section", () => {
  const view = epicsView([makeEpic({})], new Map());
  const sections: Section[] = [
    { title: "Needs you", rows: [makeRow("a", "/work/app")] },
    { title: "Working", rows: [] },
  ];

  const result = withEpicsGroup(sections, view);

  assert.deepEqual(titlesOf(result), ["Needs you", "Epics", "Working"]);
  assert.deepEqual(result[0].rows, sections[0].rows);
  assert.deepEqual(result[1].epics, view.blocks);
  assert.deepEqual(result[1].rows, []);
  assert.equal(result[1].folded, 0);
});

test("without a Needs you section the Epics group comes first", () => {
  const view = epicsView([makeEpic({})], new Map());
  const sections: Section[] = [{ title: "Working", rows: [] }];

  assert.deepEqual(titlesOf(withEpicsGroup(sections, view)), ["Epics", "Working"]);
});

test("a folded epic alone makes the Epics group, with no blocks and its count", () => {
  const view = epicsView([makeEpic({ live: false })], new Map());
  const sections: Section[] = [{ title: "Working", rows: [] }];

  const result = withEpicsGroup(sections, view);

  assert.deepEqual(titlesOf(result), ["Epics", "Working"]);
  assert.deepEqual(result[0].epics, []);
  assert.equal(result[0].folded, 1);
});

test("with no epics and nothing folded there is no Epics group, and each section is kept", () => {
  const sections: Section[] = [
    { title: "Needs you", rows: [makeRow("a", "/work/app")] },
    { title: "Seen", rows: [] },
  ];

  const result = withEpicsGroup(sections, noEpics);

  assert.deepEqual(titlesOf(result), ["Needs you", "Seen"]);
  assert.deepEqual(result[0].rows, sections[0].rows);
  assert.deepEqual(result[0].epics, []);
  assert.equal(result[0].folded, 0);
});

test("an epic sits atop the folder named for its repository", () => {
  const sections: Section[] = [
    { title: "/work/other", rows: [makeRow("other", "/work/other")] },
    { title: "/work/widgets", rows: [makeRow("widgets", "/work/widgets")] },
  ];
  const blocks: EpicBlock[] = epicsView([makeEpic({})], new Map()).blocks;

  const result = placeEpics(sections, blocks);

  assert.deepEqual(titlesOf(result), ["/work/other", "/work/widgets"]);
  assert.deepEqual(result[0].epics, []);
  assert.deepEqual(blockIds(result[1].epics), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(result[1].rows, sections[1].rows);
});

test("the first folder whose last element matches takes the epic", () => {
  const sections: Section[] = [
    { title: "/work/widgets", rows: [] },
    { title: "/home/me/widgets", rows: [] },
  ];

  const result = placeEpics(sections, epicsView([makeEpic({})], new Map()).blocks);

  assert.deepEqual(blockIds(result[0].epics), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(result[1].epics, []);
});

test("an epic with no matching folder gets its own section titled by its repository", () => {
  const sections: Section[] = [{ title: "/work/other", rows: [] }];

  const result = placeEpics(sections, epicsView([makeEpic({})], new Map()).blocks);

  assert.deepEqual(titlesOf(result), ["/work/other", "widgets-5d0c1a7"]);
  assert.deepEqual(result[0].epics, []);
  assert.deepEqual(blockIds(result[1].epics), ["widgets-5d0c1a7/first"]);
  assert.deepEqual(result[1].rows, []);
  assert.equal(result[1].folded, 0);
});

test("two epics of one repository with no folder share one section, in input order", () => {
  const epics = [
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "widgets-5d0c1a7/second" }),
  ];

  const result = placeEpics([], epicsView(epics, new Map()).blocks);

  assert.deepEqual(titlesOf(result), ["widgets-5d0c1a7"]);
  assert.deepEqual(blockIds(result[0].epics), ["widgets-5d0c1a7/first", "widgets-5d0c1a7/second"]);
});

test("two epics of one repository with a folder both sit atop that folder, in input order", () => {
  const sections: Section[] = [{ title: "/work/widgets", rows: [makeRow("widgets", "/work/widgets")] }];
  const epics = [
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "widgets-5d0c1a7/second" }),
  ];

  const result = placeEpics(sections, epicsView(epics, new Map()).blocks);

  assert.deepEqual(titlesOf(result), ["/work/widgets"]);
  assert.deepEqual(blockIds(result[0].epics), ["widgets-5d0c1a7/first", "widgets-5d0c1a7/second"]);
  assert.deepEqual(result[0].rows, sections[0].rows);
});

test("unmatched repositories get sections in first-seen order", () => {
  const epics = [
    makeEpic({ id: "gadgets-0e1f2a3/first", repository: "gadgets-0e1f2a3" }),
    makeEpic({ id: "widgets-5d0c1a7/first" }),
    makeEpic({ id: "gadgets-0e1f2a3/second", repository: "gadgets-0e1f2a3" }),
  ];

  const result = placeEpics([], epicsView(epics, new Map()).blocks);

  assert.deepEqual(titlesOf(result), ["gadgets-0e1f2a3", "widgets-5d0c1a7"]);
  assert.deepEqual(blockIds(result[0].epics), ["gadgets-0e1f2a3/first", "gadgets-0e1f2a3/second"]);
  assert.deepEqual(blockIds(result[1].epics), ["widgets-5d0c1a7/first"]);
});
