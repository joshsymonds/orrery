import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import {
  type Epic,
  EpicRequirementSchema,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { EpicLine } from "./epicline";
import { type EpicItem, epicsView } from "./epics";
import { installTestDom, removeTestDom } from "./testdom";

// now is the clock the line reads its times against, in unix seconds.
const now = 100000;

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

// itemOf is the line item of an epic whose Get detail is the one given, read from the view that holds it.
function itemOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicItem {
  const details = detail === undefined ? new Map() : new Map([[epic.id, detail]]);
  const view = epicsView([epic], details);
  return [...view.blocks.map((block) => block.epic), ...view.waiting, ...view.folded][0];
}

// synthetic is a live epic building since 49 minutes before now, with three record tasks and three requirements. t0 is
// done and so integrated; t1 and t2 are pending. R1 and R2 have a passing green run; R3 has a failing one. Its open
// question was asked two minutes before now.
function synthetic(): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: makeEpic({ phaseAt: 97060n }),
    tasks: [
      { effort: 1, slug: "t0", subject: "Subject 0", status: "done", requirements: ["R1"] },
      { effort: 1, slug: "t1", subject: "Subject 1", status: "pending", requirements: ["R2"] },
      { effort: 1, slug: "t2", subject: "Subject 2", status: "pending", requirements: ["R3"] },
    ],
    requirements: [
      create(EpicRequirementSchema, { id: "R1", runs: [{ phase: "green", passed: true }] }),
      create(EpicRequirementSchema, { id: "R2", runs: [{ phase: "green", passed: true }] }),
      create(EpicRequirementSchema, { id: "R3", runs: [{ phase: "green", passed: false }] }),
    ],
    questions: [{ id: "Q1", text: "Which way?", asked: 99880n }],
  });
}

function lineFor(
  item: EpicItem,
  options: { full?: boolean; current?: boolean; onSelect?: (item: EpicItem) => void } = {},
): JSX.Element {
  return (
    <EpicLine
      epic={item}
      full={options.full ?? false}
      current={options.current ?? false}
      now={now}
      onSelect={options.onSelect ?? (() => {})}
    />
  );
}

async function withLine(line: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(line, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

test("the line reads its slug, repository, count, state and blocker", () =>
  withLine(lineFor(itemOf(makeEpic({ phaseAt: 97060n }), synthetic())), (container) => {
    assert.equal(textOf(container, ".label"), "first");
    assert.equal(textOf(container, ".proj"), "widgets-5d0c1a7");
    assert.equal(textOf(container, ".rt"), "2 of 3 proven");
    assert.equal(textOf(container, ".state"), "building · 49 min · 1 of 3 units integrated");
    assert.equal(textOf(container, ".blocker"), "waits on your answer · 2 min");
  }));

test("the orrery draws a rim cell and four phase cells for each wedge, and no text", () =>
  withLine(lineFor(itemOf(makeEpic({ phaseAt: 97060n }), synthetic())), (container) => {
    assert.equal(container.querySelectorAll("svg.orrery .cell.rim").length, 3);
    assert.equal(container.querySelectorAll("svg.orrery .cell").length, 15);
    assert.equal(container.querySelectorAll("svg.orrery text").length, 0);
  }));

test("the orrery is hidden from assistive technology", () =>
  withLine(lineFor(itemOf(makeEpic({ phaseAt: 97060n }), synthetic())), (container) => {
    assert.equal(container.querySelector("svg.orrery")?.getAttribute("aria-hidden"), "true");
  }));

test("requirement arcs read proven or open by their ids", () =>
  withLine(lineFor(itemOf(makeEpic({ phaseAt: 97060n }), synthetic())), (container) => {
    assert.equal(container.querySelectorAll("svg.orrery path.req").length, 3);
    assert.equal(container.querySelector('svg.orrery path.req[data-req="R1"]')?.classList.contains("proven"), true);
    assert.equal(container.querySelector('svg.orrery path.req[data-req="R3"]')?.classList.contains("open"), true);
    assert.equal(container.querySelector('svg.orrery path.req[data-req="R3"]')?.classList.contains("proven"), false);
  }));

test("an open question always gives a blocker span, even when the epic is paused and waiting", () => {
  const epic = makeEpic({ waiting: true });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    paused: true,
    questions: [{ id: "Q1", text: "Which way?", asked: 0n }],
  });
  return withLine(lineFor(itemOf(epic, detail)), (container) => {
    assert.equal(textOf(container, ".blocker"), "waits on your answer");
  });
});

test("a line with nothing waiting has no blocker span", () => {
  const epic = makeEpic({ phaseAt: 97060n });
  return withLine(lineFor(itemOf(epic, create(EpicsGetResponseSchema, { epic }))), (container) => {
    assert.equal(container.querySelectorAll(".blocker").length, 0);
  });
});

test("no detail draws no orrery and reads epic unavailable", () =>
  withLine(lineFor(itemOf(makeEpic({}), undefined)), (container) => {
    assert.equal(container.querySelectorAll("svg").length, 0);
    assert.equal(textOf(container, ".rt"), "epic unavailable");
    assert.equal(container.querySelectorAll(".state").length, 0);
    assert.equal(container.querySelectorAll(".blocker").length, 0);
  }));

test("the strip draws one segment per record task when the epic is full, and none otherwise", async () => {
  const item = itemOf(makeEpic({ phaseAt: 97060n }), synthetic());
  await withLine(lineFor(item, { full: true }), (container) => {
    assert.equal(container.querySelectorAll(".strip i").length, 3);
  });
  await withLine(lineFor(item, { full: false }), (container) => {
    assert.equal(container.querySelectorAll(".strip").length, 0);
  });
});

test("the row keeps the epic row's classes and attributes, and a current row reads cur", async () => {
  const item = itemOf(makeEpic({ phaseAt: 97060n }), synthetic());
  await withLine(lineFor(item, { current: true }), (container) => {
    const row = container.querySelector(".b-row");
    assert.equal(row?.classList.contains("epic"), true);
    assert.equal(row?.classList.contains("cur"), true);
    assert.equal(row?.hasAttribute("data-row"), true);
    assert.equal(row?.getAttribute("data-key"), "epic:widgets-5d0c1a7/first");
    assert.equal(row?.getAttribute("aria-current"), "true");
  });
  await withLine(lineFor(item), (container) => {
    const row = container.querySelector(".b-row");
    assert.equal(row?.classList.contains("cur"), false);
    assert.equal(row?.hasAttribute("aria-current"), false);
  });
});

test("a click on the line calls onSelect with the epic", () => {
  const selected: string[] = [];
  const item = itemOf(makeEpic({ phaseAt: 97060n }), synthetic());
  return withLine(
    lineFor(item, { onSelect: (picked) => selected.push(picked.key) }),
    async (container) => {
      await act(() => {
        container.querySelector(".b-row")?.dispatchEvent(new Event("click"));
      });
      assert.deepEqual(selected, ["epic:widgets-5d0c1a7/first"]);
    },
  );
});
