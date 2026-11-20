import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { Rule } from "./rules";
import { installTestDom, removeTestDom } from "./testdom";

// withRule renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withRule(element: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function rootOf(container: ParentNode): HTMLElement {
  const root = container.querySelector<HTMLElement>('[data-shape="rule"]');
  assert.ok(root !== null, "the Rule has no rule root");
  return root;
}

test("Shapes: a new rule draws a rule of its kind with its label and no detail", () =>
  withRule(<Rule kind="new" label="New since you looked" />, (container) => {
    assert.equal(container.querySelectorAll('[data-shape="rule"]').length, 1, "one rule is drawn");
    const root = rootOf(container);
    assert.equal(root.tagName, "DIV", "a new rule is not a button");
    assert.equal(root.classList.contains("rule"), true);
    assert.equal(root.classList.contains("new"), true);
    assert.equal(root.querySelector(".label")?.textContent, "New since you looked");
    assert.equal(root.querySelector(".detail"), null, "no detail is drawn");
  }));

test("Shapes: an interrupted rule draws its label and its time as detail", () =>
  withRule(<Rule kind="interrupted" label="Interrupted by you" detail="14:02" />, (container) => {
    const root = rootOf(container);
    assert.equal(root.classList.contains("interrupted"), true);
    assert.equal(root.querySelector(".label")?.textContent, "Interrupted by you");
    assert.equal(root.querySelector(".detail")?.textContent, "14:02");
  }));

test("Shapes: a day rule draws the day as its label, with no detail", () =>
  withRule(<Rule kind="day" label="Monday 16 November" />, (container) => {
    const root = rootOf(container);
    assert.equal(root.classList.contains("day"), true);
    assert.equal(root.querySelector(".label")?.textContent, "Monday 16 November");
    assert.equal(root.querySelector(".detail"), null, "no detail is drawn");
  }));

test("Shapes: a compaction rule with a toggle is a button that shows its detail and a chevron, and calls the toggle on click", async () => {
  let toggles = 0;
  await withRule(
    <Rule
      kind="compaction"
      label="Compacted"
      detail="auto · 611k tokens"
      onToggle={() => {
        toggles += 1;
      }}
    />,
    async (container) => {
      const root = rootOf(container);
      assert.equal(root.tagName, "BUTTON", "a compaction rule with a toggle is a button");
      assert.equal(root.classList.contains("compaction"), true);
      assert.equal(root.querySelector(".label")?.textContent, "Compacted");
      assert.equal(root.querySelector(".detail")?.textContent, "auto · 611k tokens");
      assert.ok(root.querySelector("svg") !== null, "the rule draws a chevron");
      assert.equal(root.classList.contains("open"), false, "a closed rule is not open");
      await act(() => {
        root.click();
      });
      assert.equal(toggles, 1, "a click calls the toggle once");
    },
  );
});

test("Shapes: an open compaction rule carries the open class and an upward chevron", () =>
  withRule(<Rule kind="compaction" label="Compacted" onToggle={() => {}} open />, (container) => {
    const root = rootOf(container);
    assert.equal(root.classList.contains("open"), true);
    assert.equal(root.querySelector("svg path")?.getAttribute("d"), "M3 6l5 5 5-5", "the open chevron points down");
  }));

test("Shapes: a compaction rule without a toggle is a plain rule, not a button", () =>
  withRule(<Rule kind="compaction" label="Compacted" />, (container) => {
    assert.equal(rootOf(container).tagName, "DIV");
    assert.equal(container.querySelector("button"), null);
  }));
