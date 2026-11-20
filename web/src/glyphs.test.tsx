import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { Gauge, Icon, Pips, StateGlyph } from "./glyphs";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token Glyphs: the glyphs draw on their own, outside any page.

// drawn renders element into a fresh DOM, runs check on the container, and removes the DOM again.
async function drawn(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// one returns the first element of container that matches selector, which the drawing must hold.
function one(container: ParentNode, selector: string): Element {
  const found = container.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

test("Glyphs: a StateGlyph names its state's word in its accessible name and title, and carries data-state", () =>
  drawn(<StateGlyph state="waiting" />, (container) => {
    const glyph = one(container, "svg.glyph");
    assert.equal(glyph.getAttribute("data-state"), "waiting");
    assert.equal(glyph.getAttribute("aria-label"), "waiting on you");
    assert.equal(one(glyph, "title").textContent, "waiting on you");
  }));

test("Glyphs: a StateGlyph of a limit names it apart from a held one, which shares its drawing", async () => {
  await drawn(<StateGlyph state="limited" />, (container) => {
    assert.equal(one(container, "svg.glyph").getAttribute("aria-label"), "waiting on a limit");
  });
  await drawn(<StateGlyph state="held" />, (container) => {
    assert.equal(one(container, "svg.glyph").getAttribute("aria-label"), "held");
  });
});

test("Glyphs: a StateGlyph of a state it does not know draws idle", () =>
  drawn(<StateGlyph state="bogus" />, (container) => {
    const glyph = one(container, "svg.glyph");
    assert.equal(glyph.getAttribute("data-state"), "idle");
    assert.equal(glyph.getAttribute("aria-label"), "idle");
  }));

test("Glyphs: a Gauge names its done of total tasks, carries its tone, its arcs and its inner mark", () =>
  drawn(<Gauge done={2} started={3} total={7} tone="attention" inner="dot" />, (container) => {
    const gauge = one(container, "svg.gauge");
    assert.equal(gauge.getAttribute("aria-label"), "2 of 7 tasks done");
    assert.equal(one(gauge, "title").textContent, "2 of 7 tasks done");
    assert.equal(gauge.getAttribute("data-tone"), "attention");
    assert.equal(one(gauge, "circle.done").getAttribute("stroke-dasharray"), "28.6 100");
    assert.equal(one(gauge, "circle.started").getAttribute("stroke-dasharray"), "71.4 100");
    assert.ok(one(gauge, "circle.core") !== null, "the dot mark is not drawn");
  }));

test("Glyphs: a Gauge with no tasks says so, draws the dotted planned ring and no arcs", () =>
  drawn(<Gauge total={0} tone="live" />, (container) => {
    const gauge = one(container, "svg.gauge");
    assert.equal(gauge.getAttribute("aria-label"), "no tasks yet");
    assert.equal(gauge.getAttribute("data-tone"), "live");
    assert.equal(one(gauge, "circle.planned").getAttribute("stroke-dasharray"), "2 3");
    assert.equal(container.querySelector("circle.done"), null);
    assert.equal(container.querySelector("circle.started"), null);
  }));

test("Glyphs: a Gauge's check and dash inner marks draw their own path", async () => {
  await drawn(<Gauge done={4} total={4} inner="check" />, (container) => {
    assert.equal(one(container, "path.done").getAttribute("d"), "M8.6 12.3l2.3 2.3 4.4-4.9");
  });
  await drawn(<Gauge done={0} started={1} total={3} inner="dash" />, (container) => {
    assert.equal(one(container, "path.done").getAttribute("d"), "M9 12h6");
  });
});

test("Glyphs: Pips draws one cell per tone, each carrying its tone, and hides itself", () =>
  drawn(<Pips tones={["ok", "run", "", "", ""]} />, (container) => {
    const pips = one(container, "span.pips");
    assert.equal(pips.getAttribute("aria-hidden"), "true");
    const cells = Array.from(pips.querySelectorAll("i")).map((cell) => cell.getAttribute("data-tone"));
    assert.deepEqual(cells, ["ok", "run", "", "", ""]);
  }));

test("Glyphs: an Icon with a label is named, and one without is hidden", async () => {
  await drawn(<Icon name="back" label="Back" />, (container) => {
    const icon = one(container, "svg.icon");
    assert.equal(icon.getAttribute("role"), "img");
    assert.equal(icon.getAttribute("aria-label"), "Back");
    assert.equal(icon.getAttribute("aria-hidden"), null);
  });
  await drawn(<Icon name="back" />, (container) => {
    const icon = one(container, "svg.icon");
    assert.equal(icon.getAttribute("aria-hidden"), "true");
    assert.equal(icon.getAttribute("role"), null);
    assert.equal(icon.getAttribute("aria-label"), null);
  });
});

test("Glyphs: a filled Icon draws its path filled with currentColor and no stroke", () =>
  drawn(<Icon name="play" />, (container) => {
    const path = one(container, "svg.icon > path");
    assert.equal(path.getAttribute("fill"), "currentColor");
    assert.equal(path.getAttribute("stroke"), "none");
  }));
