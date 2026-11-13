import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicSchema,
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
});

test("five running steps show four, count one more, keep a held mark, and carry the ended count", () => {
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
  assert.equal(block.more, 1);
  assert.equal(block.ended, 3);
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
  });
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
