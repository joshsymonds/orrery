import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type VNode, render } from "preact";

import { ConvoRow } from "./convorow";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import type { HeldThing } from "./held";
import { type Row, rowsFrom } from "./rows";
import { styleDeclaration } from "./teststyle";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { buildTree, type Convo, type DesignNode, type Hung } from "./tree";

// Each test's name carries the word OpenColour, for the colours the held rows and tab are drawn in, or HeldLayout, for the
// necks and the press of a held design's tab. The tests read the DOM and the declarations of style.css, never pixels.

// now is the clock the rows read their times against, in unix seconds: the fixtures' live instant.
const now = Date.parse(liveEpoch) / 1000;

// tree is the inbox tree of the fixtures, as the page places it on the serving host.
const tree = buildTree({
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
  view: "inbox",
});

// followupsKey is the conversation that steers four epics, the fixture's most crowded; followupsEpic is the first of them, and
// orbitLabels the second, which the tests hold open.
const followupsKey = "pane:halyard:%22";
const followupsEpic = "epic:orrery/orrery-followups";
const orbitLabels = "epic:orrery/orbit-labels";

// convoOf is the conversation the tree draws for the row key, which the fixtures must hold.
function convoOf(rowKey: string): Convo {
  const convo = tree.flatMap((project) => project.convos).find((candidate) => candidate.row?.key === rowKey);
  if (convo === undefined) {
    throw new Error(`the fixture tree draws no conversation for ${rowKey}`);
  }
  return convo;
}

// withDesign is convo with a design of its own, design-a, which holds the follow-ups epic, as the tree draws a design: the
// epic is in the design's children and still among the conversation's things.
function withDesign(convo: Convo): Convo {
  const child = convo.hung.find((thing) => thing.key === followupsEpic);
  if (child === undefined) {
    throw new Error("the fixture conversation does not steer the follow-ups epic");
  }
  const design: Design = create(DesignSchema, {
    id: "design-a",
    title: "Tide alerts",
    conversation: "conv-tide",
    created: 10n,
    state: "1 building",
  });
  const node: DesignNode = { kind: "design", key: "design:design-a", design, hung: [child] };
  return { ...convo, designs: [node] };
}

// ownConvoOf is a conversation with no row of its own, holding the one thing, as the tree draws a thing with no drawn
// conversation.
function ownConvoOf(hung: Hung): Convo {
  return { key: hung.key, row: null, hung: [hung], folded: [] };
}

// callerRow draws the conversation's own row as the caller would, marked with its key so the test can find it.
function callerRow(row: Row): VNode {
  return <p class="caller" data-key={row.key} />;
}

// draw renders the conversation's ConvoRow with held and onSelect, runs check on the container, and removes the DOM again.
async function draw(
  convo: Convo,
  check: (container: HTMLElement) => void,
  options: { held?: HeldThing | null; onSelect?: (key: string) => void } = {},
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <ConvoRow
        convo={convo}
        now={now}
        cursorKey=""
        endedOpen={false}
        onEnded={() => {}}
        renderRow={callerRow}
        onSelect={options.onSelect ?? (() => {})}
        held={options.held ?? null}
      />,
      container,
    );
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// heldEpic holds the orbit-labels epic open in the follow-ups conversation, and heldDesign the design-a tab.
const heldEpic: HeldThing = { kind: "epic", key: orbitLabels, convo: followupsKey };
const heldDesign: HeldThing = { kind: "design", key: "design:design-a", convo: followupsKey };

test("OpenColour the held epic's row is the only row data-held=open, and its row and neck are on var(--o-ground-open)", () => {
  assert.equal(styleDeclaration('.inbox .row[data-held="open"]', "background"), "var(--o-ground-open)", "the open row is on the open ground");
  assert.equal(styleDeclaration('.inbox .row[data-held="open"]', "--held-ground"), "var(--o-ground-open)", "the open row's neck takes the open ground");
  assert.ok(styleDeclaration(".inbox .row[data-held]::before", "background")?.includes("var(--held-ground)"), "the neck is drawn on the held ground");
  return draw(
    convoOf(followupsKey),
    (container) => {
      const open = container.querySelectorAll('[data-held="open"]');
      assert.equal(open.length, 1, "one row is held open");
      assert.equal(open[0].getAttribute("data-key"), orbitLabels, "the held epic's row is the one open");
      assert.equal(open[0].classList.contains("epic"), true, "the open row is an EpicRow");
    },
    { held: heldEpic },
  );
});

test("OpenColour the open row is on var(--o-ground-open-hover) under the pointer", () => {
  assert.equal(styleDeclaration('.inbox .row[data-held="open"]:hover', "background"), "var(--o-ground-open-hover)", "the hovered open row is the hover colour");
  assert.equal(styleDeclaration('.inbox .row[data-held="open"]:hover', "--held-ground"), "var(--o-ground-open-hover)", "the hovered neck is the hover colour");
});

test("OpenColour a held design's tab is data-held=open, on var(--o-ground-open), and no epic row is held with it", () => {
  assert.equal(styleDeclaration('.inbox .design-tab[data-held="open"]', "background"), "var(--o-ground-open)", "the open tab is on the open ground");
  return draw(
    withDesign(convoOf(followupsKey)),
    (container) => {
      const tab = container.querySelector('.design-tab[data-key="design:design-a"]');
      assert.equal(tab?.getAttribute("data-held"), "open", "the held design's tab is open");
      assert.equal(container.querySelectorAll('[data-held="open"]').length, 1, "only the design's tab is held open");
    },
    { held: heldDesign },
  );
});

test("OpenColour the conversation's row is data-held=convo, on surface-page", () => {
  assert.equal(styleDeclaration('.inbox .row[data-held="convo"]', "background"), "var(--o-surface-page)", "the conversation's row is on the page's ground");
  return draw(
    convoOf(followupsKey),
    (container) => {
      const row = container.querySelector('[data-held="convo"]');
      assert.equal(row?.getAttribute("data-key"), followupsKey, "the conversation's row is marked convo");
    },
    { held: heldEpic },
  );
});

test("OpenColour the conversation's block is the tab, data-held=tab, on surface-raised", () => {
  assert.equal(styleDeclaration('.inbox .convo-block[data-held="tab"]', "background"), "var(--o-surface-raised)", "the tab is on the bracket's ground");
  return draw(
    convoOf(followupsKey),
    (container) => {
      assert.equal(container.querySelector(".convo-block")?.getAttribute("data-held"), "tab", "the conversation's block is the tab");
    },
    { held: heldEpic },
  );
});

test("OpenColour rows of a conversation that holds nothing carry no data-held, nor does one that holds another conversation's thing", async () => {
  await draw(convoOf(followupsKey), (container) => {
    assertNone(container.querySelector("[data-held]"), "no row is marked when nothing is held");
  });
  await draw(
    convoOf(followupsKey),
    (container) => {
      assertNone(container.querySelector("[data-held]"), "no row is marked for a thing another conversation holds");
    },
    { held: { kind: "epic", key: orbitLabels, convo: "pane:halyard:%11" } },
  );
});

test("OpenColour a held thing whose conversation has no row draws its open row, with no tab and no conversation neck", () => {
  const hung = convoOf(followupsKey).hung.find((thing) => thing.key === orbitLabels);
  if (hung === undefined) {
    throw new Error("the fixture conversation does not steer the orbit-labels epic");
  }
  return draw(
    ownConvoOf(hung),
    (container) => {
      assert.equal(container.querySelector('[data-held="open"]')?.getAttribute("data-key"), orbitLabels, "the thing's row is open");
      assertNone(container.querySelector(".convo-block"), "no tab is drawn for a conversation with no row");
      assertNone(container.querySelector('[data-held="convo"]'), "no conversation neck is drawn");
      assertNone(container.querySelector('[data-held="tab"]'), "no tab is marked");
    },
    { held: { kind: "epic", key: hung.key, convo: hung.key } },
  );
});

test("HeldLayout the necks of the conversation's row and of the open row are drawn inside their rows, and the list clips them at its edge", () => {
  assert.equal(styleDeclaration(".inbox .row", "position"), "relative", "a row is the containing block of its neck");
  assert.equal(styleDeclaration(".inbox .row[data-held]::before", "position"), "absolute", "a neck is positioned in its row");
  assert.equal(styleDeclaration(".inbox .row[data-held]::before", "left"), "100%", "a neck starts at its row's right edge");
  assert.equal(styleDeclaration(".inbox-rows", "overflow-x"), "hidden", "the scrolling list clips a neck at its right edge");
});

test("HeldLayout a held design's tab draws its neck inside the tab, and the tab is pressable", () => {
  assert.equal(styleDeclaration(".inbox .design-tab", "position"), "relative", "the tab is the containing block of its neck");
  assert.equal(styleDeclaration('.inbox .design-tab[data-held]::before', "position"), "absolute", "the tab's neck is positioned in the tab");
  assert.equal(styleDeclaration('.inbox .design-tab[data-held]::before', "left"), "100%", "the tab's neck starts at the tab's right edge");
});

test("HeldLayout a press on a design's tab calls onSelect with the design's key", () => {
  const pressed: string[] = [];
  return draw(
    withDesign(convoOf(followupsKey)),
    (container) => {
      const tab = container.querySelector<HTMLElement>('.design-tab[data-key="design:design-a"]');
      assert.ok(tab, "the design's tab is drawn");
      assert.equal(tab.getAttribute("role"), "link", "the tab is a link");
      tab.dispatchEvent(new Event("click"));
      assert.deepEqual(pressed, ["design:design-a"], "the press calls onSelect with the design's key");
    },
    { onSelect: (key) => pressed.push(key) },
  );
});
