import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { EpicMark } from "./epicmark";
import {
  EpicBuildTaskSchema,
  EpicRequirementSchema,
  EpicsGetResponseSchema,
  type EpicsGetResponse,
} from "./gen/orrery/v1/epics_pb";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";

// Each test's name carries the token EpicMark: the mark draws on its own, outside any page.

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

// epicOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function epicOf(centre: string, states: readonly string[]): Pick<EpicsGetResponse, "requirements" | "centre"> {
  return {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  };
}

test("EpicMark: one rim segment per requirement, in order, each toned by its state: proven ok, in_flight run, held mine, failed bad, not_reached line.firm", () =>
  drawn(<EpicMark detail={epicOf("running", ["proven", "in_flight", "held", "failed", "not_reached"])} />, (container) => {
    const segments = [...container.querySelectorAll("svg.epicmark circle.rim")];
    assert.deepEqual(
      segments.map((segment) => segment.getAttribute("data-id")),
      ["R1", "R2", "R3", "R4", "R5"],
    );
    assert.deepEqual(
      segments.map((segment) => segment.getAttribute("data-tone")),
      ["ok", "run", "mine", "bad", "line.firm"],
    );
    assert.equal(one(container, "svg.epicmark").getAttribute("aria-label"), "1 of 5 requirements proven, running");
  }));

test("EpicMark: a running centre draws the proven count, in the live tone", () =>
  drawn(<EpicMark detail={epicOf("running", ["proven", "proven", "in_flight", "not_reached"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "live");
    assert.equal(one(container, "svg.epicmark g.centre text").textContent, "2");
  }));

test("EpicMark: an asks centre draws a question mark in the attention tone", () =>
  drawn(<EpicMark detail={epicOf("asks", ["proven", "held", "not_reached"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "attention");
    assert.equal(one(container, "svg.epicmark g.centre text").textContent, "?");
  }));

test("EpicMark: a paused centre draws a pause bar in the grey ink", () =>
  drawn(<EpicMark detail={epicOf("paused", ["proven", "not_reached"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "ink.3");
    assert.equal(one(container, "svg.epicmark g.centre path").getAttribute("d"), "M10 8.5v7M14 8.5v7");
  }));

test("EpicMark: a releasing centre draws an arrow in the engine tone", () =>
  drawn(<EpicMark detail={epicOf("releasing", ["proven", "proven"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "engine");
    assert.equal(
      one(container, "svg.epicmark g.centre path").getAttribute("d"),
      "M8.5 12h7M12.5 8.5l3.5 3.5-3.5 3.5",
    );
  }));

test("EpicMark: a shipped centre draws a check in the ok tone", () =>
  drawn(<EpicMark detail={epicOf("shipped", ["proven", "proven"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "ok");
    assert.equal(one(container, "svg.epicmark g.centre path").getAttribute("d"), "M8.6 12.3l2.3 2.3 4.4-4.9");
  }));

test("EpicMark: an ended-short centre draws a dash in the grey ink", () =>
  drawn(<EpicMark detail={epicOf("ended_short", ["proven", "failed", "not_reached"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark g.centre").getAttribute("data-tone"), "ink.3");
    assert.equal(one(container, "svg.epicmark g.centre path").getAttribute("d"), "M9 12h6");
  }));

test("EpicMark: a centre it does not name draws no centre mark, and its rim still draws", () =>
  drawn(<EpicMark detail={epicOf("", ["proven"])} />, (container) => {
    assertNone(container.querySelector("svg.epicmark g.centre"));
    assert.equal(container.querySelectorAll("svg.epicmark circle.rim").length, 1);
  }));

test("EpicMark: a mark at 32 is marked for the mark size, and one at 24 by default for the gauge size", async () => {
  await drawn(<EpicMark detail={epicOf("shipped", ["proven"])} size={32} />, (container) => {
    assert.equal(one(container, "svg.epicmark").getAttribute("data-size"), "32");
  });
  await drawn(<EpicMark detail={epicOf("shipped", ["proven"])} />, (container) => {
    assert.equal(one(container, "svg.epicmark").getAttribute("data-size"), "24");
  });
  assert.equal(styleDeclaration(".epicmark", "width"), "var(--o-size-gauge)");
  assert.equal(styleDeclaration('.epicmark[data-size="32"]', "width"), "var(--o-size-mark)");
});

test("EpicMark: an orreryd epic building tasks, with no record tasks, draws its requirements and not an empty mark", () => {
  const detail = create(EpicsGetResponseSchema, {
    centre: "running",
    buildTasks: [
      create(EpicBuildTaskSchema, { id: "T1", title: "shift-store", status: "open", stage: "implement", requirements: ["R1"] }),
      create(EpicBuildTaskSchema, { id: "T2", title: "csv-writer", status: "open", stage: "", requirements: ["R2"] }),
    ],
    requirements: ["proven", "in_flight", "not_reached"].map((state, i) =>
      create(EpicRequirementSchema, { id: `R${i + 1}`, state }),
    ),
  });
  return drawn(<EpicMark detail={detail} />, (container) => {
    const segments = [...container.querySelectorAll("svg.epicmark circle.rim")];
    assert.deepEqual(
      segments.map((segment) => segment.getAttribute("data-id")),
      ["R1", "R2", "R3"],
      "the rim draws one segment per requirement of the contract",
    );
    assertNone(container.querySelector("svg.epicmark circle.planned"), "the empty ring is drawn only with no requirements");
    assert.equal(one(container, "svg.epicmark g.centre text").textContent, "1");
  });
});

test("EpicMark: an epic with no requirements draws the dotted planned ring and its centre, and no segment", () =>
  drawn(<EpicMark detail={epicOf("running", [])} />, (container) => {
    assert.equal(one(container, "svg.epicmark circle.planned").getAttribute("stroke-dasharray"), "2 3");
    assertNone(container.querySelector("svg.epicmark circle.rim"));
    assert.equal(one(container, "svg.epicmark g.centre text").textContent, "0");
    assert.equal(one(container, "svg.epicmark").getAttribute("aria-label"), "no requirements yet, running");
  }));
