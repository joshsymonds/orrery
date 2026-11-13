import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { EpicView } from "./epicview";
import { EpicsGetResponseSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { CENTRE, handPath, point, sliceMid, VIEW, wedgePath } from "./orbits";
import { installTestDom, removeTestDom } from "./testdom";

function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

const epic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "ship the demo" };

const tasks = [
  { slug: "1-alpha", status: "integrated" },
  { slug: "2-beta", status: "gated" },
  { slug: "3-gamma", status: "done at 3f9c2e1" },
];

const steps = [
  {
    id: "s1",
    task: "1-alpha",
    agent: "scout",
    started: 100n,
    end: "done",
    checks: [
      { round: 1, name: "unit", ok: true },
      { round: 1, name: "lint", ok: false },
    ],
  },
  { id: "s2", task: "2-beta", agent: "builder", started: 200n, held: true },
  { id: "s3", agent: "reviewer", started: 300n },
];

// syntheticSlices makes count slices labelled 1-task, 2-task and so on. Slice 1 is running and slice 2 is held: each
// has red test and working reached, so each is on tests pass, its at. The rest are done.
function syntheticSlices(count: number) {
  return Array.from({ length: count }, (_, index) => {
    const live = index < 2;
    return {
      task: `${index + 1}-task`,
      hue: live ? "dispatched" : "done",
      held: index === 1,
      redTest: true,
      working: true,
      testsPass: !live,
      checks: !live,
      gateAccepted: !live,
      at: live ? 3 : 0,
    };
  });
}

// mixSlices is the synthetic-35 epic of chart-options.html: 11 done, 3 running and 1 held, and 20 in the gap.
function mixSlices() {
  return Array.from({ length: 35 }, (_, index) => {
    const dispatched = index >= 11 && index < 15;
    return {
      task: `${index + 1}-task`,
      hue: index < 11 ? "done" : dispatched ? "dispatched" : "gap",
      held: index === 14,
      at: dispatched ? 1 : 0,
    };
  });
}

async function withView(
  props: { detail: EpicsGetResponse; cursorStep: string },
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EpicView {...props} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function classOf(element: Element): string | null {
  return element.getAttribute("class");
}

type Point = { x: number; y: number };

// endpoints lists where a cell's path reaches: the end of each move, line, curve and arc. An arc takes seven numbers
// and ends at the last two; a quadratic curve takes four and ends at the last two.
function endpoints(d: string): Point[] {
  const points: Point[] = [];
  for (const [, letter, args] of d.matchAll(/([MLAQZ])([^MLAQZ]*)/g)) {
    const numbers = args
      .trim()
      .split(/[\s,]+/)
      .filter((text) => text !== "")
      .map(Number);
    if (letter === "M" || letter === "L") {
      points.push({ x: numbers[0], y: numbers[1] });
    } else if (letter === "A") {
      points.push({ x: numbers[5], y: numbers[6] });
    } else if (letter === "Q") {
      points.push({ x: numbers[2], y: numbers[3] });
    }
  }
  return points;
}

// centreOf is the middle of the chart, half its viewBox width.
function centreOf(container: HTMLElement): number {
  return Number(container.querySelector(".chart")?.getAttribute("viewBox")?.split(" ")[2]) / 2;
}

// angleOf is the angle of a point in radians, clockwise from the top of the chart, in [0, 2π).
function angleOf(point: Point, centre: number): number {
  const angle = Math.atan2(point.x - centre, centre - point.y);
  return angle < 0 ? angle + 2 * Math.PI : angle;
}

function sameAngle(actual: number, expected: number): boolean {
  const gap = Math.abs(actual - expected) % (2 * Math.PI);
  return Math.min(gap, 2 * Math.PI - gap) < 1e-6;
}

function radiusOf(point: Point, centre: number): number {
  return Math.hypot(point.x - centre, point.y - centre);
}

// slicesOf is the chart's slice groups, in order.
function slicesOf(container: HTMLElement): Element[] {
  return [...container.querySelectorAll(".chart .slice")];
}

// cellsOf is a slice's cells, outermost ring first.
function cellsOf(slice: Element): Element[] {
  return [...slice.querySelectorAll(".cell")];
}

// cellClassesOf is a slice's cell classes, outermost ring first.
function cellClassesOf(slice: Element): (string | null)[] {
  return cellsOf(slice).map(classOf);
}

// titlesOf is a slice's cell titles, outermost ring first.
function titlesOf(slice: Element): (string | null)[] {
  return cellsOf(slice).map((cell) => cell.querySelector("title")?.textContent ?? null);
}

// glowsOf is the d of each glow, in chart order.
function glowsOf(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".chart .glow")].map((glow) => glow.getAttribute("d"));
}

function pathOf(cell: Element): Point[] {
  return endpoints(cell.getAttribute("d") ?? "");
}

// chartBox is the chart's bounding box in client pixels: off the origin and not the viewBox's size, so the scaling of
// a pointer to the 412 box is what the hover tests check.
const chartBox = { left: 100, top: 50, width: 404, height: 404 };

// pointChart sends a pointermove to the chart at a point in viewBox units, or a pointerleave, and waits for Preact to
// paint what the event changed.
async function pointChart(
  container: HTMLElement,
  type: "pointermove" | "pointerleave",
  at: Point = { x: CENTRE, y: CENTRE },
): Promise<void> {
  const chart = container.querySelector(".chart");
  const view = container.ownerDocument.defaultView;
  if (chart === null || view === null) {
    throw new Error("no chart to point at");
  }
  Object.assign(chart, { getBoundingClientRect: () => chartBox });
  chart.dispatchEvent(
    new view.PointerEvent(type, {
      clientX: chartBox.left + (at.x / VIEW) * chartBox.width,
      clientY: chartBox.top + (at.y / VIEW) * chartBox.height,
    }),
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
}

// onSlice is a point in the middle of slice index of count, at radius 130: inside the band of the cells.
function onSlice(count: number, index: number): Point {
  return point(130, sliceMid(count, index));
}

test("with no slices there is no chart: five dotted orbits, and the line says there are no orrery-tracked steps", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelectorAll(".chart").length, 0);
    assert.equal(container.querySelectorAll(".no-steps .orbit").length, 5);
    assert.deepEqual(
      [...container.querySelectorAll(".no-steps .orbit")].map((orbit) => orbit.getAttribute("r")),
      ["62", "51", "40", "29", "18"],
    );
    assert.equal(container.querySelector(".side-note")?.textContent ?? null, "No orrery-tracked steps");
    assert.equal(container.querySelectorAll(".task").length, 3, "the task list stays");
    assert.equal(container.querySelectorAll(".step").length, 3, "the step list stays");
  }));

for (const count of [3, 35, 150]) {
  test(`${count} slices draw five cells each, one now cell, one held cell and one glow, and no dot`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(count) }), cursorStep: "" }, (container) => {
      const groups = slicesOf(container);
      assert.equal(groups.length, count);
      for (const group of groups) {
        assert.equal(cellsOf(group).length, 5);
      }
      assert.equal(container.querySelectorAll(".chart .cell").length, 5 * count);
      assert.equal(container.querySelectorAll(".chart .cell.now").length, 1, "the running slice has the now cell");
      assert.equal(container.querySelectorAll(".chart .cell.held").length, 1, "the held slice has the held cell");
      assert.equal(container.querySelectorAll(".chart .glow").length, 1, "one glow, for the running slice not held");
      assert.equal(container.querySelectorAll(".chart .dot").length, 0, "no dot");
    }));
}

test("each cell's class is its section 4 kind for the slice's fields", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const [running, held, done] = slicesOf(container);
    assert.deepEqual(cellClassesOf(running), ["cell passed", "cell passed", "cell now", "cell track", "cell track"]);
    assert.deepEqual(cellClassesOf(held), ["cell passed", "cell passed", "cell held", "cell track", "cell track"]);
    assert.deepEqual(cellClassesOf(done), Array(5).fill("cell passed"));
  }));

// Each ring's field is set by its own pattern across three slices (red test 1-0-0, working 0-1-0, tests pass 1-1-0,
// checks 0-0-1, gate accepted 1-0-1), so a ring that reads another ring's field draws a different class somewhere.
test("each ring reads its own field, outermost first", () =>
  withView(
    {
      detail: detail({
        epic,
        slices: [
          { task: "1-task", hue: "gap", redTest: true, testsPass: true, gateAccepted: true },
          { task: "2-task", hue: "done", working: true, testsPass: true },
          { task: "3-task", hue: "dispatched", checks: true, gateAccepted: true, at: 1 },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      const [first, second, third] = slicesOf(container);
      assert.deepEqual(cellClassesOf(first), ["cell passed", "cell track", "cell passed", "cell track", "cell passed"]);
      assert.deepEqual(cellClassesOf(second), ["cell track", "cell passed", "cell passed", "cell track", "cell track"]);
      assert.deepEqual(cellClassesOf(third), ["cell now", "cell track", "cell track", "cell passed", "cell passed"]);
    },
  ));

test("each cell's title names its task, its ring and whether it is reached", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const [running, held] = slicesOf(container);
    assert.deepEqual(titlesOf(running), [
      "1-task red test: passed",
      "1-task working: passed",
      "1-task tests pass: working now",
      "1-task checks: not reached",
      "1-task gate accepted: not reached",
    ]);
    assert.equal(titlesOf(held)[2], "2-task tests pass: held here");
  }));

test("the rings run from red test outermost in to gate accepted innermost", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const centre = centreOf(container);
    const [running] = slicesOf(container);
    const cells = cellsOf(running);
    assert.deepEqual(
      cells.map((cell) => cell.querySelector("title")?.textContent?.split(":")[0]),
      ["1-task red test", "1-task working", "1-task tests pass", "1-task checks", "1-task gate accepted"],
    );
    const inner = cells.map((cell) => Math.min(...pathOf(cell).map((point) => radiusOf(point, centre))));
    for (let index = 1; index < inner.length; index++) {
      assert.ok(inner[index] < inner[index - 1], `ring ${index} sits inside ring ${index - 1}: ${inner}`);
    }
  }));

test("the start gap holds the ring letters R W T C G, one on each ring's radius", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const letters = [...container.querySelectorAll(".chart .ring-letter")];
    assert.deepEqual(
      letters.map((letter) => letter.textContent),
      ["R", "W", "T", "C", "G"],
    );
    assert.deepEqual(
      letters.map((letter) => letter.getAttribute("x")),
      ["206", "206", "206", "206", "206"],
    );
    assert.deepEqual(
      letters.map((letter) => letter.getAttribute("y")),
      ["38", "55", "72", "89", "106"],
    );
  }));

// The bezel has a tick every fifth slice from ten slices up, and its numbers follow section 8's step: 25 at 150 slices,
// 5 at 35.
const bezelCases: [number, number, string[]][] = [
  [9, 0, []],
  [10, 2, ["5", "10"]],
  [35, 7, ["5", "10", "15", "20", "25", "30", "35"]],
  [150, 30, ["25", "50", "75", "100", "125", "150"]],
];
for (const [count, ticks, labels] of bezelCases) {
  test(`a chart of ${count} slices has ${ticks} bezel ticks and ${labels.length} bezel numbers`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(count) }), cursorStep: "" }, (container) => {
      assert.equal(container.querySelectorAll(".chart .tick").length, ticks);
      assert.equal(container.querySelectorAll(".chart .tick.major").length, labels.length);
      assert.deepEqual(
        [...container.querySelectorAll(".chart .tick-number")].map((number) => number.textContent),
        labels,
      );
    }));
}

for (const count of [1, 3, 35, 150]) {
  test(`${count} slices start at 8 degrees, run clockwise, and each takes 344 / ${count} degrees`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(count) }), cursorStep: "" }, (container) => {
      const centre = centreOf(container);
      const span = (344 * Math.PI) / 180 / count;
      slicesOf(container).forEach((group, index) => {
        const start = (8 * Math.PI) / 180 + index * span;
        const end = start + span;
        const mid = start + span / 2;
        const angles = cellsOf(group).flatMap((cell) => pathOf(cell).map((point) => angleOf(point, centre)));
        assert.equal(
          angles.every((angle) => angle > start && angle < end),
          true,
          `slice ${index} lies between ${start} and ${end}`,
        );
        assert.equal(Math.min(...angles) < mid && mid < Math.max(...angles), true, `slice ${index} covers its middle`);
        for (const edge of [start, mid, end]) {
          assert.equal(
            angles.some((angle) => sameAngle(angle, edge)),
            false,
            `slice ${index} has no point at ${edge}`,
          );
        }
      });
    }));
}

test("the running slice's now cell is the cell of ring at, and the glow's d is its d", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(35) }), cursorStep: "" }, (container) => {
    const [running] = slicesOf(container);
    const cells = cellsOf(running);
    const now = cellClassesOf(running).indexOf("cell now");
    assert.equal(now, 2, "at 3 is the tests pass ring");
    assert.deepEqual(glowsOf(container), [cells[now]?.getAttribute("d") ?? null]);
  }));

test("a held slice has one held cell on ring at, and no now cell and no glow", () =>
  withView(
    {
      detail: detail({
        epic,
        slices: [{ task: "1-task", hue: "dispatched", held: true, redTest: true, working: true, at: 3 }],
      }),
      cursorStep: "",
    },
    (container) => {
      const [held] = slicesOf(container);
      assert.deepEqual(cellClassesOf(held), ["cell passed", "cell passed", "cell held", "cell track", "cell track"]);
      assert.equal(titlesOf(held)[2], "1-task tests pass: held here");
      assert.deepEqual(glowsOf(container), []);
    },
  ));

test("a slice with error_at has one errored cell on that ring, past a track ring and before a passed one", () =>
  withView(
    {
      detail: detail({
        epic,
        slices: [
          { task: "1-task", hue: "gap", redTest: true, working: true, testsPass: true, errorAt: 4 },
          { task: "2-task", hue: "gap", working: true, errorAt: 4 },
          { task: "3-task", hue: "gap", redTest: true, testsPass: true, errorAt: 2 },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      const [checksFailed, afterTrack, beforePassed] = slicesOf(container);
      assert.deepEqual(cellClassesOf(checksFailed), [
        "cell passed",
        "cell passed",
        "cell passed",
        "cell errored",
        "cell track",
      ]);
      assert.deepEqual(cellClassesOf(afterTrack), [
        "cell track",
        "cell passed",
        "cell track",
        "cell errored",
        "cell track",
      ]);
      assert.deepEqual(cellClassesOf(beforePassed), [
        "cell passed",
        "cell errored",
        "cell passed",
        "cell track",
        "cell track",
      ]);
      assert.equal(titlesOf(checksFailed)[3], "1-task checks: errored here");
      assert.deepEqual(glowsOf(container), []);
    },
  ));

test("a done slice has no now, held or errored cell, and no glow", () =>
  withView(
    {
      detail: detail({
        epic,
        slices: [
          {
            task: "1-task",
            hue: "done",
            redTest: true,
            working: true,
            testsPass: true,
            checks: true,
            gateAccepted: true,
          },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      const [done] = slicesOf(container);
      assert.deepEqual(cellClassesOf(done), Array(5).fill("cell passed"));
      assert.deepEqual(glowsOf(container), []);
    },
  ));

test("a running slice with only working true and at 1 is now on red test, passed on working, and track after", () =>
  withView(
    {
      detail: detail({ epic, slices: [{ task: "1-task", hue: "dispatched", working: true, at: 1 }] }),
      cursorStep: "",
    },
    (container) => {
      const [running] = slicesOf(container);
      assert.deepEqual(cellClassesOf(running), ["cell now", "cell passed", "cell track", "cell track", "cell track"]);
      assert.equal(glowsOf(container).length, 1);
    },
  ));

test("a running slice with no true boolean and at 1 is now on red test, and track on the four after", () =>
  withView(
    { detail: detail({ epic, slices: [{ task: "1-task", hue: "dispatched", at: 1 }] }), cursorStep: "" },
    (container) => {
      const [running] = slicesOf(container);
      assert.deepEqual(cellClassesOf(running), ["cell now", "cell track", "cell track", "cell track", "cell track"]);
      assert.equal(glowsOf(container).length, 1);
    },
  ));

test("at wins over error_at, and error_at wins over a true boolean on the same ring", () =>
  withView(
    {
      detail: detail({
        epic,
        slices: [
          { task: "1-task", hue: "dispatched", working: true, at: 2, errorAt: 2 },
          { task: "2-task", hue: "gap", working: true, errorAt: 2 },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      const [running, gapped] = slicesOf(container);
      assert.deepEqual(cellClassesOf(running), ["cell track", "cell now", "cell track", "cell track", "cell track"]);
      assert.deepEqual(cellClassesOf(gapped), ["cell track", "cell errored", "cell track", "cell track", "cell track"]);
    },
  ));

test("the glow filter is a blur of 3.4 units named orbit-glow", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelector(".chart filter")?.getAttribute("id") ?? null, "orbit-glow");
    assert.equal(container.querySelector(".chart filter feGaussianBlur")?.getAttribute("stdDeviation") ?? null, "3.4");
  }));

test("the cursor step's slice is outlined, and no other slice", () => {
  const cursorSteps = [{ id: "s1", task: "2-task", agent: "builder", started: 1n }];
  return withView(
    { detail: detail({ epic, steps: cursorSteps, slices: syntheticSlices(3) }), cursorStep: "s1" },
    (container) => {
      assert.deepEqual(
        [...container.querySelectorAll(".chart .slice")].map(classOf),
        ["slice", "slice cur", "slice"],
      );
      assert.equal(container.querySelectorAll(".step.cur").length, 1, "the step is still marked");
    },
  );
});

test("a cursor step with no task, or whose task has no slice, outlines no slice", async () => {
  const cursorSteps = [
    [{ id: "s1", agent: "reviewer", started: 1n }],
    [{ id: "s1", task: "9-task", agent: "builder", started: 1n }],
  ];
  for (const stepsOfCase of cursorSteps) {
    await withView(
      { detail: detail({ epic, steps: stepsOfCase, slices: syntheticSlices(3) }), cursorStep: "s1" },
      (container) => {
        assert.equal(container.querySelectorAll(".chart .cur").length, 0);
      },
    );
  }
});

test("an empty cursorStep marks no step and no slice", () =>
  withView({ detail: detail({ epic, tasks, steps, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelectorAll(".cur").length, 0);
    assert.equal(container.querySelectorAll("[aria-current]").length, 0);
  }));

test("the header draws the slug, repository, outcome and problem", () =>
  withView(
    { detail: detail({ epic: { ...epic, problem: "lock held" } }), cursorStep: "" },
    (container) => {
      assert.equal(container.querySelector(".epic-head .slug")?.textContent, "demo-epic");
      assert.equal(container.querySelector(".epic-head .repo")?.textContent, "demo-repo");
      assert.equal(container.querySelector(".epic-head .outcome")?.textContent, "ship the demo");
      assert.equal(container.querySelector(".epic-head .problem")?.textContent, "lock held");
    },
  ));

test("no problem element when the epic has no problem", () =>
  withView({ detail: detail({ epic }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelectorAll(".problem").length, 0);
  }));

test("markup in slugs, statuses, labels and slice tasks draws as text with no element", () =>
  withView(
    {
      detail: detail({
        epic,
        tasks: [{ slug: "<img src=x onerror=alert(1)>", status: "<b>done</b>" }],
        steps: [{ id: "s1", task: "<script>alert(1)</script>", started: 1n }],
        slices: [{ ...syntheticSlices(1)[0], task: "<i>slice</i>" }],
      }),
      cursorStep: "",
    },
    (container) => {
      assert.equal(container.querySelectorAll("img").length, 0);
      assert.equal(container.querySelectorAll("script").length, 0);
      assert.equal(container.querySelectorAll("b").length, 0);
      assert.equal(container.querySelectorAll("i").length, 0);
      assert.ok(container.textContent?.includes("<img src=x onerror=alert(1)>"));
      assert.ok(container.textContent?.includes("<b>done</b>"));
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
      assert.ok(container.textContent?.includes("<i>slice</i> tests pass: working now"));
    },
  ));

// cursorSteps is one step on the task, which the chart and the readout follow.
function cursorSteps(task: string) {
  return [{ id: "s1", task, agent: "builder", started: 1n }];
}

// childClassesOf is the class of each child of the chart's svg, in document order.
function childClassesOf(container: HTMLElement): (string | null)[] {
  return [...(container.querySelector(".chart")?.children ?? [])].map(classOf);
}

for (const count of [35, 150]) {
  test(`a cursor task on a chart of ${count} slices outlines its wedge and draws its hand`, () =>
    withView(
      { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(count) }), cursorStep: "s1" },
      (container) => {
        assert.equal(container.querySelectorAll(".chart .outline").length, 1);
        assert.equal(container.querySelector(".chart .outline")?.getAttribute("d") ?? null, wedgePath(count, 1));
        assert.equal(container.querySelectorAll(".chart .hand").length, 1);
        assert.equal(container.querySelector(".chart .hand")?.getAttribute("d") ?? null, handPath(count, 1));
      },
    ));
}

for (const count of [3, 17, 18, 35, 150]) {
  test(`a cursor task on a chart of ${count} slices washes its wedge only from 18 slices`, () =>
    withView(
      { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(count) }), cursorStep: "s1" },
      (container) => {
        assert.equal(container.querySelectorAll(".chart .wash").length, count >= 18 ? 1 : 0);
        if (count >= 18) {
          assert.equal(container.querySelector(".chart .wash")?.getAttribute("d") ?? null, wedgePath(count, 1));
        }
      },
    ));
}

test("the wash is drawn before the cells, under the first slice", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(18) }), cursorStep: "s1" },
    (container) => {
      const order = childClassesOf(container);
      const wash = order.indexOf("wash");
      const firstSlice = order.findIndex((name) => name?.startsWith("slice"));
      assert.ok(wash >= 0 && wash < firstSlice, `wash at ${wash}, first slice at ${firstSlice}: ${order}`);
    },
  ));

test("the cursor's outline and hand are drawn after the glows, and the readout last", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(35) }), cursorStep: "s1" },
    (container) => {
      const order = childClassesOf(container);
      const glows = order.indexOf("glows");
      const outline = order.indexOf("outline");
      const hand = order.indexOf("hand");
      assert.ok(glows >= 0 && glows < outline && outline < hand, `order: ${order}`);
      assert.equal(order.at(-1), "readout", `order: ${order}`);
    },
  ));

test("with no cursor task, or one whose task has no slice, there is no wash, outline or hand", async () => {
  const cases = [
    { steps: [], cursorStep: "" },
    { steps: [{ id: "s1", agent: "reviewer", started: 1n }], cursorStep: "s1" },
    { steps: cursorSteps("99-task"), cursorStep: "s1" },
  ];
  for (const { steps: stepsOfCase, cursorStep } of cases) {
    await withView(
      { detail: detail({ epic, steps: stepsOfCase, slices: syntheticSlices(35) }), cursorStep },
      (container) => {
        assert.equal(container.querySelectorAll(".chart .wash").length, 0);
        assert.equal(container.querySelectorAll(".chart .outline").length, 0);
        assert.equal(container.querySelectorAll(".chart .hand").length, 0);
      },
    );
  }
});

for (const [count, index] of [
  [3, 2],
  [35, 4],
  [150, 100],
]) {
  test(`a pointer over slice ${index + 1} of ${count} draws its hover wedge and the readout reads its task`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(count) }), cursorStep: "" }, async (container) => {
      await pointChart(container, "pointermove", onSlice(count, index));
      assert.equal(container.querySelectorAll(".chart .outline.hover").length, 1);
      assert.equal(
        container.querySelector(".chart .outline.hover")?.getAttribute("d") ?? null,
        wedgePath(count, index),
      );
      assert.equal(container.querySelectorAll(".chart .hand").length, 0, "a hover draws no hand");
      assert.equal(
        container.querySelector(".chart .readout .ordinal")?.textContent ?? null,
        `${index + 1} of ${count}`,
      );
      assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, `${index + 1}-task`);
    }));
}

// The centre and the start gap are nowhere: a pointer there clears a hover over a slice, and the epic reads again.
const nowhere = [
  { name: "the centre", at: { x: CENTRE, y: CENTRE } },
  { name: "the start gap", at: point(130, 0) },
];
for (const { name, at } of nowhere) {
  test(`a pointer over ${name} draws no hover wedge, and clears the one a slice had`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(35) }), cursorStep: "" }, async (container) => {
      await pointChart(container, "pointermove", onSlice(35, 4));
      assert.equal(container.querySelectorAll(".chart .outline.hover").length, 1, "the slice is hovered first");
      await pointChart(container, "pointermove", at);
      assert.equal(container.querySelectorAll(".chart .outline.hover").length, 0);
      assert.equal(container.querySelector(".chart .readout .count")?.textContent ?? null, "33/35");
    }));
}

test("pointerleave removes the hover wedge, and the readout returns to the cursor's task", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(35) }), cursorStep: "s1" },
    async (container) => {
      await pointChart(container, "pointermove", onSlice(35, 4));
      assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, "5-task");
      await pointChart(container, "pointerleave");
      assert.equal(container.querySelectorAll(".chart .outline.hover").length, 0);
      assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, "2-task");
      assert.equal(container.querySelector(".chart .readout .ordinal")?.textContent ?? null, "2 of 35");
    },
  ));

test("pointerleave with no cursor task returns the readout to the epic", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(35) }), cursorStep: "" }, async (container) => {
    await pointChart(container, "pointermove", onSlice(35, 4));
    assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, "5-task");
    await pointChart(container, "pointerleave");
    assert.equal(container.querySelectorAll(".chart .outline.hover").length, 0);
    assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, null);
    assert.equal(container.querySelector(".chart .readout .count")?.textContent ?? null, "33/35");
  }));

test("a hovered slice beats the cursor's task in the readout, and the cursor's outline, hand and mark stay", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(35) }), cursorStep: "s1" },
    async (container) => {
      await pointChart(container, "pointermove", onSlice(35, 4));
      assert.equal(container.querySelector(".chart .readout .ordinal")?.textContent ?? null, "5 of 35");
      assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, "5-task");
      assert.deepEqual([...container.querySelectorAll(".chart .outline")].map(classOf), ["outline", "outline hover"]);
      assert.deepEqual(
        [...container.querySelectorAll(".chart .outline")].map((outline) => outline.getAttribute("d")),
        [wedgePath(35, 1), wedgePath(35, 4)],
      );
      assert.equal(container.querySelector(".chart .hand")?.getAttribute("d") ?? null, handPath(35, 1));
      assert.equal(slicesOf(container).findIndex((slice) => classOf(slice) === "slice cur"), 1);
    },
  ));

test("the epic's centre reads 11 of 35 tasks done, the running and held tallies on one line and the gap on the next", () =>
  withView({ detail: detail({ epic, tasks, slices: mixSlices() }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelector(".chart .readout .count")?.textContent ?? null, "11/35");
    assert.equal(container.querySelector(".chart .readout .count")?.getAttribute("y") ?? null, "200");
    assert.equal(container.querySelector(".chart .readout .caption")?.textContent ?? null, "tasks done");
    assert.equal(container.querySelector(".chart .readout .caption")?.getAttribute("y") ?? null, "218");
    assert.deepEqual(
      [...container.querySelectorAll(".chart .readout .tallies")].map((line) => line.textContent),
      ["3 running · 1 held", "20 gap"],
    );
    assert.deepEqual(
      [...container.querySelectorAll(".chart .readout .tallies")].map((line) => line.getAttribute("y")),
      ["237", "252"],
    );
  }));

test("a 150-slice epic reads 148 of 150 done, with its one running and one held task on one line", () =>
  withView({ detail: detail({ epic, tasks, slices: syntheticSlices(150) }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelector(".chart .readout .count")?.textContent ?? null, "148/150");
    assert.deepEqual(
      [...container.querySelectorAll(".chart .readout .tallies")].map((line) => line.textContent),
      ["1 running · 1 held"],
    );
  }));

test("one tally is one line at +38, and a record with more tasks than slices reads tracked tasks done", () =>
  withView(
    {
      detail: detail({
        epic,
        tasks: Array.from({ length: 5 }, (_, index) => ({ slug: `${index + 1}-task`, status: "done" })),
        slices: [
          { task: "1-task", hue: "done" },
          { task: "2-task", hue: "done" },
          { task: "3-task", hue: "done" },
          { task: "4-task", hue: "gap", errorAt: 1 },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      assert.equal(container.querySelector(".chart .readout .count")?.textContent ?? null, "3/4");
      assert.equal(container.querySelector(".chart .readout .count")?.getAttribute("y") ?? null, "204");
      assert.equal(container.querySelector(".chart .readout .caption")?.textContent ?? null, "tracked tasks done");
      assert.equal(container.querySelector(".chart .readout .caption")?.getAttribute("y") ?? null, "223");
      assert.deepEqual(
        [...container.querySelectorAll(".chart .readout .tallies")].map((line) => line.textContent),
        ["1 gap"],
      );
      assert.deepEqual(
        [...container.querySelectorAll(".chart .readout .tallies")].map((line) => line.getAttribute("y")),
        ["244"],
      );
    },
  ));

test("the cursor task's centre reads its ordinal, label, running status and five letters by kind", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("1-task"), slices: syntheticSlices(3) }), cursorStep: "s1" },
    (container) => {
      assert.equal(container.querySelectorAll(".chart .readout .count").length, 0, "no epic count");
      assert.equal(container.querySelector(".chart .readout .ordinal")?.textContent ?? null, "1 of 3");
      assert.equal(container.querySelector(".chart .readout .label")?.textContent ?? null, "1-task");
      assert.equal(container.querySelector(".chart .readout .status")?.textContent ?? null, "running · tests pass");
      assert.equal(container.querySelector(".chart .readout .status")?.getAttribute("class") ?? null, "status now");
      assert.deepEqual(
        [...container.querySelectorAll(".chart .readout .letter")].map((letter) => letter.textContent),
        ["R", "W", "T", "C", "G"],
      );
      assert.deepEqual(
        [...container.querySelectorAll(".chart .readout .letter")].map(classOf),
        ["letter passed", "letter passed", "letter now", "letter track", "letter track"],
      );
    },
  ));

test("a held cursor task's centre reads held with the ring it waits on", () =>
  withView(
    { detail: detail({ epic, steps: cursorSteps("2-task"), slices: syntheticSlices(3) }), cursorStep: "s1" },
    (container) => {
      assert.equal(container.querySelector(".chart .readout .ordinal")?.textContent ?? null, "2 of 3");
      assert.equal(container.querySelector(".chart .readout .status")?.textContent ?? null, "held · tests pass");
      assert.equal(container.querySelector(".chart .readout .status")?.getAttribute("class") ?? null, "status held");
      assert.deepEqual(
        [...container.querySelectorAll(".chart .readout .letter")].map(classOf),
        ["letter passed", "letter passed", "letter held", "letter track", "letter track"],
      );
    },
  ));

test("two key lines under the chart name the rings and the colours", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    assert.deepEqual(
      [...container.querySelectorAll(".chart-key")].map((line) => line.textContent),
      ["R red test W working T tests pass C checks G gate", "passed now held errored"],
    );
  }));
