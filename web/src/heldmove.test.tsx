import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render, type VNode } from "preact";

import { ConvoRow } from "./convorow";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { type HeldMove, type HeldThing, heldMove } from "./held";
import { HeldCard, HeldSheet } from "./heldcard";
import { RailLineage } from "./inboxrail";
import type { LineageConvo } from "./railtiles";
import { type Row, rowsFrom } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { buildTree, type Convo, type DesignNode } from "./tree";

// Each test's name carries the word PaneMotion. The move tests read the DOM the components draw; the CSS tests read
// style.css as text, with its comments removed. No test waits on the clock.

const noop = () => undefined;

const oneEpic: HeldThing = { kind: "epic", key: "epic:one", convo: "convo:one" };
const twoEpic: HeldThing = { kind: "epic", key: "epic:two", convo: "convo:one" };
const otherEpic: HeldThing = { kind: "epic", key: "epic:other", convo: "convo:two" };

test("PaneMotion heldMove gives open from null to a thing, and close from a thing to null", () => {
  assert.equal(heldMove(null, oneEpic), "open", "null to a thing is open");
  assert.equal(heldMove(oneEpic, null), "close", "a thing to null is close");
});

test("PaneMotion heldMove gives switch for two keys of one convo, and other for two convos", () => {
  assert.equal(heldMove(oneEpic, twoEpic), "switch", "two keys of one convo are a switch");
  assert.equal(heldMove(oneEpic, otherEpic), "other", "two convos are other");
});

test("PaneMotion heldMove gives \"\" for the same key, and for null to null", () => {
  assert.equal(heldMove(oneEpic, { ...oneEpic }), "", "the same key is no move");
  assert.equal(heldMove(null, null), "", "null to null is no move");
});

// withDom gives the check a container on a fresh DOM, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

const moves: HeldMove[] = ["open", "switch", "other", "close"];

test("PaneMotion data-move HeldCard and HeldSheet carry their move, and none when it is empty", () =>
  withDom((container) => {
    const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };
    for (const move of moves) {
      render(
        <HeldCard thing={epic} title="Invented epic" move={move} gauge={null} onFoldBack={noop}>
          <p>the record</p>
        </HeldCard>,
        container,
      );
      assert.equal(one(container, ".held-card").getAttribute("data-move"), move, `the card carries ${move}`);

      render(
        <HeldSheet thing={epic} title="Invented epic" move={move} gauge={null} onClose={noop}>
          <p>the record</p>
        </HeldSheet>,
        container,
      );
      assert.equal(one(container, ".held-sheet").getAttribute("data-move"), move, `the sheet carries ${move}`);
    }

    render(
      <HeldCard thing={epic} title="Invented epic" move="" gauge={null} onFoldBack={noop}>
        <p>the record</p>
      </HeldCard>,
      container,
    );
    assertNone(container.querySelector("[data-move]"), "a card with no move carries no data-move");

    render(
      <HeldSheet thing={epic} title="Invented epic" move="" gauge={null} onClose={noop}>
        <p>the record</p>
      </HeldSheet>,
      container,
    );
    assertNone(container.querySelector("[data-move]"), "a sheet with no move carries no data-move");
  }));

// The fixture tree of the inbox, as the page places it on the serving host. followupsKey is the conversation that steers four
// epics, and its design, design-a, is given here as the tree draws a design.
const now = Date.parse(liveEpoch) / 1000;
const tree = buildTree({
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
  view: "inbox",
});
const followupsKey = "pane:halyard:%22";
const followupsEpic = "epic:orrery/orrery-followups";

// convoOf is the conversation the tree draws for the row key, which the fixtures must hold.
function convoOf(rowKey: string): Convo {
  const convo = tree.flatMap((project) => project.convos).find((candidate) => candidate.row?.key === rowKey);
  if (convo === undefined) {
    throw new Error(`the fixture tree draws no conversation for ${rowKey}`);
  }
  return convo;
}

// withDesign is convo with a design of its own, design-a, which holds the follow-ups epic.
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

// callerRow draws the conversation's own row as the caller would, marked with its key so the test can find it.
function callerRow(row: Row): VNode {
  return <p class="caller" data-key={row.key} />;
}

// heldDesign and heldEpic are the things the conversation holds open in the two ConvoRow checks: its design, and the epic
// under that design.
const heldDesign: HeldThing = { kind: "design", key: "design:design-a", convo: followupsKey };
const heldEpic: HeldThing = { kind: "epic", key: followupsEpic, convo: followupsKey };

// drawConvo renders the conversation's ConvoRow with held held and moved by move, runs check on the container, and removes
// the DOM again.
async function drawConvo(held: HeldThing, move: HeldMove, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <ConvoRow
        convo={withDesign(convoOf(followupsKey))}
        now={now}
        cursorKey=""
        endedOpen={false}
        onEnded={() => {}}
        renderRow={callerRow}
        onSelect={() => {}}
        held={held}
        move={move}
      />,
      container,
    );
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("PaneMotion data-move a held ConvoRow marks its tab, the held design's tab and its conversation row with the move", async () => {
  await drawConvo(heldDesign, "open", (container) => {
    assert.equal(one(container, ".convo-block").getAttribute("data-move"), "open", "the held tab carries the move");
    assert.equal(
      one(container, '.design-tab[data-key="design:design-a"]').getAttribute("data-move"),
      "open",
      "the held design's tab carries the move",
    );
    assert.equal(one(container, '[data-held="convo"]').getAttribute("data-move"), "open", "the held conversation row carries the move");
  });
  await drawConvo(heldDesign, "", (container) => {
    assertNone(container.querySelector("[data-move]"), "a held ConvoRow with no move carries no data-move");
    assert.ok(container.querySelector('.design-tab[data-held="open"]') !== null, "the design is still held open");
  });
});

test("PaneMotion data-move a held ConvoRow marks the held epic's row and its tab with the move, and the design's tab with none", async () => {
  await drawConvo(heldEpic, "switch", (container) => {
    assert.equal(
      one(container, `[data-key="${followupsEpic}"][data-held="open"]`).getAttribute("data-move"),
      "switch",
      "the held epic's row carries the move",
    );
    assert.equal(one(container, ".convo-block").getAttribute("data-move"), "switch", "the held tab carries the move");
    assertNone(container.querySelector(".design-tab[data-move]"), "the design that is not held carries no data-move");
  });
});

// heldDesignLineage is a lineage of one conversation holding its design open, with an epic under the design that is not open;
// heldEpicLineage is a lineage of one conversation holding its own epic open.
const heldDesignLineage: LineageConvo[] = [
  {
    key: "convo:one",
    held: true,
    own: [],
    designs: [
      {
        key: "design:d",
        name: "d",
        open: true,
        marks: [{ key: "epic:in", kind: "epic", name: "in", open: false }],
      },
    ],
  },
];
const heldEpicLineage: LineageConvo[] = [
  {
    key: "convo:two",
    held: true,
    own: [{ key: "epic:own", kind: "epic", name: "own", open: true }],
    designs: [],
  },
];

test("PaneMotion data-move RailLineage marks the held conversation's neck and the open mark with the move", () =>
  withDom((container) => {
    render(<RailLineage projectKey="project:p" convos={heldDesignLineage} move="switch" onPress={noop} />, container);
    assert.equal(one(container, '.lconv[data-held="convo"]').getAttribute("data-move"), "switch", "the held conversation's neck carries the move");
    assert.equal(one(container, '.lmark.open[data-held="open"]').getAttribute("data-move"), "switch", "the open design mark carries the move");
    assertNone(container.querySelector(".lwell [data-move]"), "a mark of a design that is not open carries no data-move");

    render(<RailLineage projectKey="project:p" convos={heldEpicLineage} move="open" onPress={noop} />, container);
    assert.equal(one(container, '.lmark.open[data-held="open"]').getAttribute("data-move"), "open", "the open epic mark carries the move");

    render(<RailLineage projectKey="project:p" convos={heldEpicLineage} onPress={noop} />, container);
    assertNone(container.querySelector("[data-move]"), "a lineage with no move carries no data-move");
  }));

// Rule is one rule of the sheet: its selector, its declarations, and the at-rules it sits in, outermost first.
interface Rule {
  selector: string;
  body: string;
  within: string[];
}

// closeOf is the index of the brace that closes the block opened at open.
function closeOf(css: string, open: number): number {
  let depth = 0;
  for (let at = open; at < css.length; at++) {
    if (css[at] === "{") {
      depth++;
    } else if (css[at] === "}") {
      depth--;
      if (depth === 0) {
        return at;
      }
    }
  }
  throw new Error("style.css has a block that is not closed");
}

// rulesOf lists every rule in css, at any depth, with the at-rules around it.
function rulesOf(css: string, within: string[] = []): Rule[] {
  const rules: Rule[] = [];
  let head = "";
  let at = 0;
  while (at < css.length) {
    const char = css[at];
    if (char === ";") {
      head = "";
      at++;
    } else if (char === "{") {
      const close = closeOf(css, at);
      const body = css.slice(at + 1, close);
      const selector = head.trim();
      if (selector.startsWith("@")) {
        rules.push(...rulesOf(body, [...within, selector]));
      } else {
        rules.push({ selector, body, within });
      }
      head = "";
      at = close + 1;
    } else {
      head += char;
      at++;
    }
  }
  return rules;
}

const sheet = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const rules = rulesOf(sheet);

// isMoving says whether a rule's declarations animate or transition.
function isMoving(body: string): boolean {
  return body.split(";").some((declaration) => /^\s*(animation|transition)(-[a-z-]+)?\s*:/.test(declaration));
}

// dataMoveRules are the rules that animate or transition a [data-move] selector. The pattern is the attribute itself, so the
// existing [data-moving] rule is not one of them.
const dataMoveRules = rules.filter((rule) => /\[data-move[=\]]/.test(rule.selector) && isMoving(rule.body));

const noPreference = "@media (prefers-reduced-motion: no-preference)";

test("PaneMotion motion: every rule that animates a [data-move] selector is inside the no-preference condition and names a --o-move token", () => {
  assert.ok(dataMoveRules.length > 0, "style.css animates some [data-move] rule");
  for (const rule of dataMoveRules) {
    assert.ok(rule.within.includes(noPreference), `${rule.selector} is inside ${noPreference}`);
    assert.match(rule.body, /var\(--o-move-[a-z]+\)/, `${rule.selector} names a --o-move token`);
  }
});

test("PaneMotion motion: the card's rising keyframes start at scale(0.86, 0.5) and var(--o-distance-drill)", () => {
  const rise = dataMoveRules.find((rule) => rule.selector.startsWith(".held-card") && rule.body.includes("animation: held-rise"));
  assert.ok(rise !== undefined, "the card rises by its held-rise keyframes");
  const name = rise.body.match(/animation: ([\w-]+)/)?.[1];
  const from = rules.find((candidate) => candidate.within[candidate.within.length - 1] === `@keyframes ${name}` && candidate.selector === "from");
  assert.ok(from !== undefined, `style.css has the from frame of ${name}`);
  assert.ok(from.body.includes("scale(0.86, 0.5)"), "the card starts at scale(0.86, 0.5)");
  assert.ok(from.body.includes("var(--o-distance-drill)"), "the card starts var(--o-distance-drill) toward its row");
});
