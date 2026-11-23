import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, type VNode, render } from "preact";

import { ConvoRow } from "./convorow";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { rowsFrom, type Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";
import { buildTree, type Convo, type EpicNode, type Hung } from "./tree";

// Each test's name carries the words InboxTree ConvoRow: the conversation's row with what it steers under it.

// now is the clock the rows read their times against, in unix seconds: the fixtures' live instant.
const now = Date.parse(liveEpoch) / 1000;

// treeInput is the fixture world the inbox tree places, as the page places it on the serving host.
const treeInput = {
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
};

// tree is the inbox tree of the fixtures, and archivedTree the Archived view of them.
const tree = buildTree({ ...treeInput, view: "inbox" });
const archivedTree = buildTree({ ...treeInput, view: "archived" });

// convoOf is the conversation the tree draws for the row key, which the fixtures must hold.
function convoOf(rowKey: string, drawn: readonly { convos: Convo[] }[] = tree): Convo {
  const convo = drawn.flatMap((project) => project.convos).find((candidate) => candidate.row?.key === rowKey);
  if (convo === undefined) {
    throw new Error(`the fixture tree draws no conversation for ${rowKey}`);
  }
  return convo;
}

// epicKeysOf is the keys of the hung things, in order, which every hung thing of these fixtures is an epic for.
function keysOf(hung: readonly Hung[]): string[] {
  return hung.map((thing) => thing.key);
}

// callerRow draws the conversation's own row as the caller would, marked with its key so the test can find it.
function callerRow(row: Row): VNode {
  return <p class="caller" data-key={row.key} />;
}

// withConvo draws the conversation row in a fresh DOM, runs check on the container, and removes the DOM again.
async function withConvo(
  convo: Convo,
  check: (container: HTMLElement) => void | Promise<void>,
  options: { cursorKey?: string; onSelect?: (key: string) => void; renderRow?: (row: Row) => VNode } = {},
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const row: JSX.Element = (
    <ConvoRow
      convo={convo}
      now={now}
      cursorKey={options.cursorKey ?? ""}
      renderRow={options.renderRow ?? callerRow}
      onSelect={options.onSelect ?? (() => {})}
    />
  );
  try {
    render(row, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// ownOf is the conversation the tree draws for a thing with no drawn conversation, as its own row, which the fixtures must
// hold under the thing's key.
function ownOf(thingKey: string, drawn: readonly { convos: Convo[] }[] = tree): Convo {
  const convo = drawn.flatMap((project) => project.convos).find((candidate) => candidate.key === thingKey);
  if (convo === undefined) {
    throw new Error(`the fixture tree draws no own row for ${thingKey}`);
  }
  return convo;
}

// ownConvoOf is a conversation with no row of its own holding the one thing, as the tree draws a thing with no drawn
// conversation.
function ownConvoOf(hung: Hung): Convo {
  return { key: hung.key, row: null, again: false, hung: [hung] };
}

test("InboxTree ConvoRow draws the caller's row first, then each epic it steers in order inside .steers", () => {
  const convo = convoOf("pane:halyard:%22");
  const expected = [
    "epic:orrery/orrery-followups",
    "epic:orrery/orbit-labels",
    "epic:orrery/notices-usable",
    "epic:orrery/harbour-map",
  ];
  assert.deepEqual(keysOf(convo.hung), expected, "the fixture steers the four epics in this order");
  return withConvo(convo, (container) => {
    const first = container.firstElementChild;
    assert.equal(first?.getAttribute("data-key"), "pane:halyard:%22", "the caller's row is drawn first");
    const steers = container.querySelector(".steers");
    assert.ok(steers, "the steered things hang in .steers");
    assert.equal(steers.previousElementSibling, first, "the .steers follows the caller's row");
    const drawn = [...steers.querySelectorAll(":scope > .row.epic")].map((row) => row.getAttribute("data-key"));
    assert.deepEqual(drawn, expected, "each hung epic draws one EpicRow, in order");
  });
});

test("InboxTree ConvoRow draws no .steers when nothing hangs under the conversation", () => {
  const convo = convoOf("pane:halyard:%11");
  assert.equal(convo.hung.length, 0, "the lamp steers nothing in the fixtures");
  return withConvo(convo, (container) => {
    assert.equal(container.querySelector(".steers"), null, "no .steers when nothing hangs");
    assert.equal(container.querySelector(".row.epic"), null, "no EpicRow when nothing hangs");
  });
});

test("InboxTree ConvoRow draws a conversation with no row of its own as its one EpicRow at the top level, with no stand-in line", () => {
  const convo = ownConvoOf(convoOf("pane:halyard:%14").hung[0]);
  let rowsDrawn = 0;
  return withConvo(
    convo,
    (container) => {
      const first = container.firstElementChild;
      assert.equal(first?.getAttribute("data-key"), convo.key, "the thing's EpicRow is drawn first, at the top level");
      assert.equal(first?.classList.contains("epic"), true, "the first row is an epic row");
      assert.equal(container.querySelector(".steers"), null, "no .steers wraps the thing");
      assert.equal(container.querySelector(".standin"), null, "no stand-in line is drawn");
      assert.equal(container.textContent.includes("Its conversation"), false, "no stand-in words are drawn");
      assert.equal(rowsDrawn, 0, "the caller's row is not drawn for a conversation with no row");
    },
    {
      renderRow: (row) => {
        rowsDrawn++;
        return callerRow(row);
      },
    },
  );
});

test("InboxTree ConvoRow marks the cursor's EpicRow current, and only that one", () => {
  const convo = convoOf("pane:halyard:%22");
  return withConvo(
    convo,
    (container) => {
      const current = container.querySelectorAll(".row.cur");
      assert.equal(current.length, 1, "one row is current");
      assert.equal(current[0].getAttribute("data-key"), "epic:orrery/orbit-labels", "the cursor's epic is current");
    },
    { cursorKey: "epic:orrery/orbit-labels" },
  );
});

test("InboxTree ConvoRow marks the cursor's step current, under its epic", () => {
  const convo = convoOf("pane:halyard:%22");
  const epic = convo.hung.find((thing): thing is EpicNode => thing.key === "epic:orrery/orrery-followups");
  const step = epic?.block.steps[0]?.key;
  assert.ok(step !== undefined, "the fixture's epic shows a running step");
  return withConvo(
    convo,
    (container) => {
      assert.equal(container.querySelector(".row.step.cur")?.getAttribute("data-key"), step, "the cursor's step is current");
      assert.equal(container.querySelector(".row.epic.cur"), null, "its epic is not current");
    },
    { cursorKey: step },
  );
});

test("InboxTree ConvoRow presses call onSelect with the pressed epic's key, and with a step's key", () => {
  const convo = convoOf("pane:halyard:%22");
  const epic = convo.hung.find((thing): thing is EpicNode => thing.key === "epic:orrery/orrery-followups");
  const step = epic?.block.steps[0]?.key;
  assert.ok(step !== undefined, "the fixture's epic shows a running step");
  const pressed: string[] = [];
  return withConvo(
    convo,
    (container) => {
      container.querySelector('.row.epic[data-key="epic:orrery/harbour-map"]')?.dispatchEvent(new Event("click"));
      container.querySelector(`.row.step[data-key="${step}"]`)?.dispatchEvent(new Event("click"));
      assert.deepEqual(pressed, ["epic:orrery/harbour-map", step]);
    },
    { onSelect: (key) => pressed.push(key) },
  );
});

test("InboxTree ConvoRow draws the Archived view's tide warm-up epic, whose conversation is not archived, as its own row at the top level", () => {
  const convo = ownOf("epic:tidepool/tide-warmup", archivedTree);
  assert.deepEqual(keysOf(convo.hung), ["epic:tidepool/tide-warmup"], "the archived epic is the only thing it holds");
  assert.equal(convo.row, null, "no conversation is drawn above it, since its conversation is not archived");
  return withConvo(convo, (container) => {
    assert.equal(container.firstElementChild?.getAttribute("data-key"), "epic:tidepool/tide-warmup", "its EpicRow is drawn first");
    assert.equal(container.querySelector(".steers"), null, "no .steers wraps it");
    assert.equal(container.querySelector('.row[data-key="session:9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90"]'), null, "its conversation is not drawn");
  });
});
