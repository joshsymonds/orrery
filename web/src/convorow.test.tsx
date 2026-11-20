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

// standInOf is a stand-in conversation holding the hung things, as the tree draws a thing with no listed conversation.
function standInOf(standIn: "unlisted" | "archived", hung: Hung[]): Convo {
  return { key: `standin:${standIn}:|/home/owner/code/tidepool`, row: null, standIn, again: false, hung };
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

test("InboxTree ConvoRow draws an archived stand-in's words and no row of its own", () => {
  const convo = standInOf("archived", convoOf("pane:halyard:%14").hung);
  let rowsDrawn = 0;
  return withConvo(
    convo,
    (container) => {
      const line = container.querySelector(".row.standin");
      assert.equal(line?.textContent, "Its conversation is archived", "the stand-in says its conversation is archived");
      assert.equal(line?.getAttribute("data-key"), convo.key, "the stand-in line carries its convo key");
      assert.equal(rowsDrawn, 0, "the caller's row is not drawn for a stand-in");
      assert.equal(container.querySelector("p.caller"), null, "no conversation row is drawn");
      assert.equal(container.querySelectorAll(".steers > .row.epic").length, 1, "the epic it holds hangs under it");
    },
    {
      renderRow: (row) => {
        rowsDrawn++;
        return callerRow(row);
      },
    },
  );
});

test("InboxTree ConvoRow draws an unlisted stand-in's words and no row of its own", () => {
  const convo = standInOf("unlisted", convoOf("pane:halyard:%14").hung);
  return withConvo(convo, (container) => {
    assert.equal(container.querySelector(".row.standin")?.textContent, "Its conversation is not listed");
    assert.equal(container.querySelector("p.caller"), null, "no conversation row is drawn");
  });
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

test("InboxTree ConvoRow draws the Archived view's tide warm-up, which is not archived, with its archived epic under .steers", () => {
  const convo = convoOf("session:9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90", archivedTree);
  assert.deepEqual(keysOf(convo.hung), ["epic:tidepool/tide-warmup"], "the archived epic hangs under the live conversation");
  return withConvo(convo, (container) => {
    assert.equal(container.firstElementChild?.getAttribute("data-key"), "session:9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90");
    const steers = container.querySelector(".steers");
    assert.ok(steers, "the archived epic hangs in .steers");
    const drawn = [...steers.querySelectorAll(":scope > .row.epic")].map((row) => row.getAttribute("data-key"));
    assert.deepEqual(drawn, ["epic:tidepool/tide-warmup"], "its EpicRow is drawn under the conversation");
  });
});
