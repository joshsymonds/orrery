import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";

import { CENTRE, RING_RADII, cellPath, point, sector, sliceSpan, sliceStart } from "./orbits";
import { Orrery } from "./orrery";
import { PHASES, type PhaseSlice, type RequirementArc, type SliceState, type Wedge } from "./phases";
import { installTestDom, removeTestDom } from "./testdom";

// now is the clock the orrery reads its running and passed times against, in the same seconds as the slices' since.
const now = 400;

function slice(
  phase: PhaseSlice["phase"],
  state: SliceState,
  summary: string,
  since: number,
  seconds: number,
): PhaseSlice {
  return { phase, state, summary, since, seconds };
}

function aheadSlices(): PhaseSlice[] {
  return PHASES.map((phase) => slice(phase, "ahead", "", 0, 0));
}

// integrated is a wedge with every phase passed: its implementing slice has a summary and two minutes, its checking
// slice is empty.
const integrated: Wedge = {
  task: "t1",
  title: "Login",
  requirements: ["R4"],
  place: "integrated",
  tone: "ok",
  slices: [
    slice("implementing", "passed", "built it", 0, 120),
    slice("checking", "passed", "", 0, 0),
    slice("reviewing", "passed", "reviewed", 0, 30),
    slice("integrating", "passed", "merged", 0, 10),
  ],
};

// running is a wedge in implementing whose implementing slice is now, since 100: five minutes at now.
const running: Wedge = {
  task: "t2",
  title: "Parser",
  requirements: ["R5"],
  place: "implementing",
  tone: "run",
  slices: [
    slice("implementing", "now", "writing the parser", 100, 0),
    ...aheadSlices().slice(1),
  ],
};

// planned is a wedge at the rim, with every slice ahead.
const planned: Wedge = {
  task: "t3",
  title: "Lexer",
  requirements: ["R5"],
  place: "planned",
  tone: "plan",
  slices: aheadSlices(),
};

// blocked is a wedge in checking whose checking slice is now with a bad tone and no summary, since 50: five minutes.
const blocked: Wedge = {
  task: "t4",
  title: "Store",
  requirements: ["R4", "R5"],
  place: "checking",
  tone: "bad",
  slices: [
    slice("implementing", "passed", "fixed", 0, 30),
    slice("checking", "now", "", 50, 0),
    ...aheadSlices().slice(2),
  ],
};

const wedges = [integrated, running, planned, blocked];

const arcs: RequirementArc[] = [
  { id: "R4", start: 0, end: 1.5, proven: true },
  { id: "R5", start: 1.5, end: 4, proven: false },
];

// phaseNames are the labels down the top gap, rim first.
const phaseNames = ["planned", "implementing", "checking", "reviewing", "integrating"];

// allowedTexts are the only text classes the orrery draws: no legend, key, caption or ring letter.
const allowedTexts = ["req-id", "phase-name", "count", "count-of"];

// turn lets Preact's queued render run before the test reads the DOM.
async function turn(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// cellOf finds the cell of a task's phase, or the rim when phase is "planned"; a missing cell fails the test.
function cellOf(container: HTMLElement, task: string, phase: string): Element {
  const found = container.querySelector(`.cell[data-task="${task}"][data-phase="${phase}"]`);
  if (found === null) {
    throw new Error(`no cell for ${task} ${phase}`);
  }
  return found;
}

async function withOrrery(check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Orrery wedges={wedges} arcs={arcs} now={now} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

async function hover(container: HTMLElement, task: string, phase: string): Promise<void> {
  cellOf(container, task, phase).dispatchEvent(new Event("pointerenter"));
  await turn();
}

test("the svg reads its view box, role and integrated count of wedges", () =>
  withOrrery((container) => {
    const svg = container.querySelector("svg.orrery");
    assert.equal(svg?.tagName, "svg");
    assert.equal(svg?.getAttribute("viewBox"), "0 0 412 412");
    assert.equal(svg?.getAttribute("role"), "img");
    assert.equal(svg?.getAttribute("aria-label"), "1 of 4 integrated");
  }));

test("each wedge draws five cells: a rim cell and one cell for each of its four phases", () =>
  withOrrery((container) => {
    assert.equal(container.querySelectorAll(".cell").length, 20);
    for (const wedge of wedges) {
      assert.equal(container.querySelectorAll(`.cell[data-task="${wedge.task}"]`).length, 5);
    }
  }));

test("the rim cell reads planned for a planned wedge and passed for any other", () =>
  withOrrery((container) => {
    const plannedRim = cellOf(container, "t3", "planned");
    assert.equal(plannedRim.classList.contains("rim"), true);
    assert.equal(plannedRim.classList.contains("planned"), true);
    assert.equal(plannedRim.classList.contains("passed"), false);
    const runningRim = cellOf(container, "t2", "planned");
    assert.equal(runningRim.classList.contains("rim"), true);
    assert.equal(runningRim.classList.contains("passed"), true);
    assert.equal(runningRim.classList.contains("planned"), false);
  }));

test("a phase cell carries its phase and state, and the now slice alone carries its wedge's tone", () =>
  withOrrery((container) => {
    const now2 = cellOf(container, "t2", "implementing");
    assert.equal(now2.classList.contains("phase-implementing"), true);
    assert.equal(now2.classList.contains("now"), true);
    assert.equal(now2.classList.contains("t-run"), true);

    const ahead = cellOf(container, "t2", "checking");
    assert.equal(ahead.classList.contains("phase-checking"), true);
    assert.equal(ahead.classList.contains("ahead"), true);
    assert.equal([...ahead.classList].some((name) => name.startsWith("t-")), false);

    const passed = cellOf(container, "t1", "reviewing");
    assert.equal(passed.classList.contains("phase-reviewing"), true);
    assert.equal(passed.classList.contains("passed"), true);
    assert.equal([...passed.classList].some((name) => name.startsWith("t-")), false);

    const blockedNow = cellOf(container, "t4", "checking");
    assert.equal(blockedNow.classList.contains("now"), true);
    assert.equal(blockedNow.classList.contains("t-bad"), true);
  }));

test("each cell's path is the cell of its wedge's index and its phase's ring", () =>
  withOrrery((container) => {
    assert.equal(cellOf(container, "t3", "planned").getAttribute("d"), cellPath(4, 2, 1));
    assert.equal(cellOf(container, "t2", "implementing").getAttribute("d"), cellPath(4, 1, 2));
    assert.equal(cellOf(container, "t4", "reviewing").getAttribute("d"), cellPath(4, 3, 4));
  }));

test("a running wedge glows its now slice after all cells, and no other slice glows", () =>
  withOrrery((container) => {
    const glows = container.querySelectorAll(".glows .glow");
    assert.equal(glows.length, 1);
    assert.equal(glows[0]?.getAttribute("d"), cellPath(4, 1, 2));

    const children = [...(container.querySelector("svg.orrery")?.children ?? [])];
    const glowGroup = children.findIndex((child) => child.getAttribute("class") === "glows");
    const lastCell = children.map((child) => child.classList.contains("cell")).lastIndexOf(true);
    assert.equal(glowGroup > lastCell, true);
  }));

test("requirement arcs read proven or open by their ids, drawn from radius 184 to 192 with the id at 200", () =>
  withOrrery((container) => {
    assert.equal(container.querySelectorAll("path.req").length, 2);
    assert.equal(container.querySelector('path.req[data-req="R4"]')?.classList.contains("proven"), true);
    assert.equal(container.querySelector('path.req[data-req="R5"]')?.classList.contains("open"), true);
    assert.equal(container.querySelector('path.req[data-req="R5"]')?.classList.contains("proven"), false);
    assert.deepEqual(textsOf(container, "text.req-id"), ["R4", "R5"]);

    const from = sliceStart(4, 0);
    const to = sliceStart(4, 0) + 1.5 * sliceSpan(4);
    assert.equal(container.querySelector('path.req[data-req="R4"]')?.getAttribute("d"), sector(184, 192, from, to));

    const mid = (from + to) / 2;
    const idX = Number(container.querySelector("text.req-id")?.getAttribute("x"));
    const idY = Number(container.querySelector("text.req-id")?.getAttribute("y"));
    assert.equal(idX, point(200, mid).x);
    assert.equal(idY, point(200, mid).y);
  }));

test("the phase names run down the top gap, one at each ring's radius", () =>
  withOrrery((container) => {
    assert.deepEqual(textsOf(container, "text.phase-name"), phaseNames);
    const ys = [...container.querySelectorAll("text.phase-name")].map((element) => Number(element.getAttribute("y")));
    assert.deepEqual(
      ys,
      RING_RADII.map((radius) => CENTRE - radius),
    );
  }));

test("the centre reads the integrated count of wedges and the word integrated", () =>
  withOrrery((container) => {
    assert.equal(textOf(container, "text.count"), "1 of 4");
    assert.equal(textOf(container, "text.count-of"), "integrated");
  }));

test("hovering a running slice shows its task, phase, summary and the time since it began", () =>
  withOrrery(async (container) => {
    assert.equal(container.querySelectorAll(".slice-card").length, 0);
    await hover(container, "t2", "implementing");
    assert.equal(container.querySelectorAll(".slice-card").length, 1);
    assert.equal(textOf(container, ".slice-card .card-task"), "t2");
    assert.equal(textOf(container, ".slice-card .card-phase"), "implementing");
    assert.equal(textOf(container, ".slice-card .card-summary"), "writing the parser");
    assert.equal(textOf(container, ".slice-card .card-time"), "5 min");
    assert.equal(container.lastElementChild?.classList.contains("slice-card"), true);
  }));

test("hovering a passed slice shows the time its ended steps took", () =>
  withOrrery(async (container) => {
    await hover(container, "t1", "implementing");
    assert.equal(textOf(container, ".slice-card .card-summary"), "built it");
    assert.equal(textOf(container, ".slice-card .card-time"), "2 min");

    await hover(container, "t4", "implementing");
    assert.equal(textOf(container, ".slice-card .card-time"), "30 s");
  }));

test("hovering an empty passed slice shows no summary and no time", () =>
  withOrrery(async (container) => {
    await hover(container, "t1", "checking");
    assert.equal(textOf(container, ".slice-card .card-task"), "t1");
    assert.equal(textOf(container, ".slice-card .card-phase"), "checking");
    assert.equal(container.querySelectorAll(".slice-card .card-summary").length, 0);
    assert.equal(container.querySelectorAll(".slice-card .card-time").length, 0);
  }));

test("hovering a now slice with no summary shows its time and no summary", () =>
  withOrrery(async (container) => {
    await hover(container, "t4", "checking");
    assert.equal(container.querySelectorAll(".slice-card .card-summary").length, 0);
    assert.equal(textOf(container, ".slice-card .card-time"), "5 min");
  }));

test("hovering an ahead slice shows no time", () =>
  withOrrery(async (container) => {
    await hover(container, "t2", "reviewing");
    assert.equal(textOf(container, ".slice-card .card-phase"), "reviewing");
    assert.equal(container.querySelectorAll(".slice-card .card-time").length, 0);
  }));

test("leaving a slice hides its card", () =>
  withOrrery(async (container) => {
    await hover(container, "t2", "implementing");
    assert.equal(container.querySelectorAll(".slice-card").length, 1);
    cellOf(container, "t2", "implementing").dispatchEvent(new Event("pointerleave"));
    await turn();
    assert.equal(container.querySelectorAll(".slice-card").length, 0);
  }));

test("hovering a rim cell shows no card", () =>
  withOrrery(async (container) => {
    await hover(container, "t2", "planned");
    assert.equal(container.querySelectorAll(".slice-card").length, 0);
  }));

test("the orrery draws no legend, key, ring letter or text beyond its labels", () =>
  withOrrery((container) => {
    assert.equal(container.querySelectorAll(".legend, .key, .ring-letter").length, 0);
    const texts = [...container.querySelectorAll("svg.orrery text")];
    assert.equal(
      texts.every((element) => allowedTexts.includes(element.getAttribute("class") ?? "")),
      true,
    );
  }));
