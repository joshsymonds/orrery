import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { EpicView } from "./epicview";
import { EpicsGetResponseSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
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

async function withView(
  props: { detail: EpicsGetResponse; cursorStep: string },
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EpicView {...props} />, container);
    check(container);
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
