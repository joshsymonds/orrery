import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { EpicMark } from "./epicmark";
import { EpicRequirementSchema, EpicsGetResponseSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { Icon, StateGlyph } from "./glyphs";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

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

// epicOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function epicOf(centre: string, states: readonly string[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  });
}

test("Glyphs: an EpicMark names its proven of total requirements, carries its rim segments toned by state, and its centre mark", () =>
  drawn(<EpicMark detail={epicOf("asks", ["proven", "proven", "in_flight", "in_flight", "in_flight", "not_reached", "not_reached"])} />, (container) => {
    const mark = one(container, "svg.epicmark");
    assert.equal(mark.getAttribute("aria-label"), "2 of 7 requirements proven, asks you a question");
    assert.equal(one(mark, "title").textContent, "2 of 7 requirements proven, asks you a question");
    assert.equal(container.querySelectorAll("svg.epicmark circle.rim").length, 7);
    assert.equal(container.querySelectorAll('svg.epicmark circle.rim[data-tone="ok"]').length, 2);
    assert.equal(container.querySelectorAll('svg.epicmark circle.rim[data-tone="run"]').length, 3);
    assert.equal(one(mark, "g.centre").getAttribute("data-tone"), "attention");
    assert.equal(one(mark, "g.centre text").textContent, "?");
  }));

test("Glyphs: an EpicMark with no requirements says so, draws the dotted planned ring and no rim, and its centre in its tone", () =>
  drawn(<EpicMark detail={epicOf("running", [])} />, (container) => {
    const mark = one(container, "svg.epicmark");
    assert.equal(mark.getAttribute("aria-label"), "no requirements yet, running");
    assert.equal(one(mark, "g.centre").getAttribute("data-tone"), "live");
    assert.equal(one(mark, "circle.planned").getAttribute("stroke-dasharray"), "2 3");
    assertNone(container.querySelector("circle.rim"), "a planned mark draws no rim");
  }));

test("Glyphs: an EpicMark's shipped and ended-short centres draw their own path", async () => {
  await drawn(<EpicMark detail={epicOf("shipped", ["proven"])} />, (container) => {
    assert.equal(one(container, "g.centre path").getAttribute("d"), "M8.6 12.3l2.3 2.3 4.4-4.9");
  });
  await drawn(<EpicMark detail={epicOf("ended_short", ["failed"])} />, (container) => {
    assert.equal(one(container, "g.centre path").getAttribute("d"), "M9 12h6");
  });
});

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
