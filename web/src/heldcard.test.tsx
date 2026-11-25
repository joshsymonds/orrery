import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";

import { designMark } from "./convorow";
import { EpicMark } from "./epicmark";
import type { MarkDetail } from "./epicrow";
import { EpicRequirementSchema } from "./gen/orrery/v1/epics_pb";
import { Icon } from "./glyphs";
import type { HeldThing } from "./held";
import { HeldCard, HeldSheet } from "./heldcard";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";

// Each test renders HeldCard or HeldSheet on its own, into a container on a fresh DOM, and checks the DOM it draws. Colours
// are read only as the style sheet declares them, and no test reads the clock.

const design: HeldThing = { kind: "design", key: "design:invented-design", convo: "convo:invented-conversation" };
const contract: HeldThing = { kind: "contract", key: "contract:invented-contract", convo: "convo:invented-conversation" };
const epic: HeldThing = { kind: "epic", key: "epic:invented-epic", convo: "convo:invented-conversation" };

// epicMark is the detail the invented epic's mark reads: two of four requirements proven, one in flight, running.
const epicMark: MarkDetail = {
  centre: "running",
  requirements: [
    create(EpicRequirementSchema, { id: "R1", state: "proven" }),
    create(EpicRequirementSchema, { id: "R2", state: "proven" }),
    create(EpicRequirementSchema, { id: "R3", state: "in_flight" }),
    create(EpicRequirementSchema, { id: "R4", state: "not_reached" }),
  ],
};

const noop = () => undefined;

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

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// card draws a HeldCard of thing into the container, with a record in its body, the epic's mark and onFoldBack as its press.
function card(
  container: HTMLElement,
  thing: HeldThing,
  { mark = null, onFoldBack = noop }: { mark?: MarkDetail | null; onFoldBack?: () => void } = {},
): void {
  render(
    <HeldCard thing={thing} title={`Invented ${thing.kind}`} move="" mark={mark} onFoldBack={onFoldBack}>
      <p>the record</p>
    </HeldCard>,
    container,
  );
}

test("HeldCard FoldBack: the head's first element is Fold back, with its arrow, its words and its ] key", () =>
  withDom((container) => {
    card(container, epic, { mark: epicMark });
    const first = one(container, ".held-head").firstElementChild;
    assert.ok(first !== null && first.matches("button.fold-back"), "the head's first element is the Fold back button");
    assert.equal(first.getAttribute("aria-label"), "Fold back");
    assert.ok(first.querySelector("svg") !== null, "Fold back holds an arrow");
    assert.equal(one(first, "span").textContent, "Fold back");
    assert.equal(one(first, "kbd").textContent, "]");
  }));

test("HeldCard FoldBack: a press on Fold back calls onFoldBack once", () =>
  withDom((container) => {
    let presses = 0;
    card(container, design, {
      onFoldBack: () => {
        presses += 1;
      },
    });
    one(container, "button.fold-back").click();
    assert.equal(presses, 1);
  }));

test("HeldCard FoldBack: the head holds no New window, no Close and no bare Back, and its only button is Fold back", () =>
  withDom((container) => {
    card(container, contract);
    const head = one(container, ".held-head");
    assert.doesNotMatch(head.textContent ?? "", /New window/);
    assert.deepEqual(
      all(head, "button").map((button) => button.getAttribute("aria-label")),
      ["Fold back"],
    );
    assert.deepEqual(
      all(head, "kbd").map((key) => key.parentElement?.className),
      ["fold-back"],
    );
  }));

test("HeldCard FoldBack: the card draws the kind word and the mark of a design, a contract and an epic", () =>
  withDom((container) => {
    card(container, design);
    assert.equal(one(container, ".held-kind").textContent, "design");
    assert.equal(one(container, ".held-mark svg.design-mark path").getAttribute("d"), designMark);

    card(container, contract);
    assert.equal(one(container, ".held-kind").textContent, "contract");
    const docMark = document.createElement("div");
    render(<Icon name="doc" />, docMark);
    assert.equal(one(container, ".held-mark").innerHTML, docMark.innerHTML);
    render(null, docMark);

    card(container, epic, { mark: epicMark });
    assert.equal(one(container, ".held-kind").textContent, "epic");
    const rowMark = document.createElement("div");
    render(<EpicMark detail={epicMark} size={24} />, rowMark);
    assert.equal(one(container, ".held-mark").innerHTML, rowMark.innerHTML);
    render(null, rowMark);
  }));

test("HeldCard OpenColour: the spine and Fold back are on var(--o-ground-open), and Fold back's hover is on var(--o-ground-open-hover)", () =>
  withDom((container) => {
    card(container, epic, { mark: epicMark });
    one(container, ".held-card > .held-spine");
    one(container, ".held-head > button.fold-back");
    assert.equal(styleDeclaration(".held-spine", "background"), "var(--o-ground-open)");
    assert.equal(styleDeclaration(".fold-back", "background"), "var(--o-ground-open)");
    assert.equal(styleDeclaration(".fold-back:hover", "background"), "var(--o-ground-open-hover)");
  }));

test("HeldLayout: the phone's sheet heads with its mark, kind and title, keeps its X at the right, has no Fold back, and a press on the X calls onClose", () =>
  withDom((container) => {
    let closes = 0;
    render(
      <HeldSheet
        thing={epic}
        title="Invented epic"
        move=""
        mark={epicMark}
        onClose={() => {
          closes += 1;
        }}
      >
        <p>the record</p>
      </HeldSheet>,
      container,
    );
    const head = one(container, "section.held-sheet > .held-head");
    assert.deepEqual(
      Array.from(head.children).map((part) => part.className),
      ["held-mark", "held-kind", "held-title", "held-close"],
    );
    assert.equal(one(head, ".held-mark svg.epicmark g.centre").getAttribute("data-tone"), "live");
    assert.equal(one(head, ".held-kind").textContent, "epic");
    assert.equal(one(head, ".held-title").textContent, "Invented epic");
    const close = one(head, "button.held-close");
    assert.equal(close.getAttribute("aria-label"), "Close");
    assertSame(head.lastElementChild, close, "the X is the head's last part");
    assertNone(container.querySelector(".fold-back"), "the sheet has no Fold back");
    close.click();
    assert.equal(closes, 1);
  }));
