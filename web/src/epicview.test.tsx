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

// syntheticSlices makes count slices labelled 1-task, 2-task and so on. Slice 1 is running: its red test and working
// cells are reached and its dot is on the working ring, the innermost ring it reached. The rest are done.
function syntheticSlices(count: number) {
  return Array.from({ length: count }, (_, index) => {
    const running = index === 0;
    return {
      task: `${index + 1}-task`,
      hue: running ? "dispatched" : "done",
      redTest: true,
      working: true,
      testsPass: !running,
      checks: !running,
      gateAccepted: !running,
      at: running ? 2 : 0,
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

// endpoints lists where a cell's path reaches: the end of each move, line and arc. An arc takes seven numbers and
// ends at the last two.
function endpoints(d: string): Point[] {
  const points: Point[] = [];
  for (const [, letter, args] of d.matchAll(/([MLAZ])([^MLAZ]*)/g)) {
    const numbers = args
      .trim()
      .split(/[\s,]+/)
      .filter((text) => text !== "")
      .map(Number);
    if (letter === "M" || letter === "L") {
      points.push({ x: numbers[0], y: numbers[1] });
    } else if (letter === "A") {
      points.push({ x: numbers[5], y: numbers[6] });
    }
  }
  return points;
}

// centreOf is the middle of the chart, half its viewBox width.
function centreOf(chart: Element): number {
  return Number(chart.getAttribute("viewBox")?.split(" ")[2]) / 2;
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

function pointOfDot(dot: Element): Point {
  return { x: Number(dot.getAttribute("cx")), y: Number(dot.getAttribute("cy")) };
}

// cellsOf is a slice's cells, outermost ring first.
function cellsOf(slice: Element): Element[] {
  return [...slice.querySelectorAll(".cell")];
}

function pathOf(cell: Element): Point[] {
  return endpoints(cell.getAttribute("d") ?? "");
}

test("with no slices there is no chart, and the line says there are no orrery-tracked steps", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelector(".chart"), null);
    assert.equal(container.querySelector(".side-note")?.textContent, "No orrery-tracked steps");
    assert.equal(container.querySelectorAll(".task").length, 3, "the task list stays");
    assert.equal(container.querySelectorAll(".step").length, 3, "the step list stays");
  }));

for (const count of [3, 35, 150]) {
  test(`${count} slices draw five cells each, and only the running slice has a dot`, () =>
    withView({ detail: detail({ epic, slices: syntheticSlices(count) }), cursorStep: "" }, (container) => {
      const groups = [...container.querySelectorAll(".chart .slice")];
      assert.equal(groups.length, count);
      for (const group of groups) {
        assert.equal(cellsOf(group).length, 5);
      }
      assert.equal(container.querySelectorAll(".chart .cell").length, 5 * count);
      assert.equal(container.querySelectorAll(".chart .dot").length, 1, "exactly one dot");
      assert.equal(groups[0].querySelectorAll(".dot").length, 1, "the running slice has the dot");
    }));
}

test("a reached cell carries its slice's hue class and an unreached cell the track class", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const [running, done] = [...container.querySelectorAll(".chart .slice")];
    assert.deepEqual(cellsOf(running).map(classOf), [
      "cell ts-dispatched",
      "cell ts-dispatched",
      "cell track",
      "cell track",
      "cell track",
    ]);
    assert.deepEqual(cellsOf(done).map(classOf), [
      "cell ts-done",
      "cell ts-done",
      "cell ts-done",
      "cell ts-done",
      "cell ts-done",
    ]);
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
          { task: "3-task", hue: "dispatched", checks: true, gateAccepted: true },
        ],
      }),
      cursorStep: "",
    },
    (container) => {
      const [first, second, third] = [...container.querySelectorAll(".chart .slice")];
      assert.deepEqual(cellsOf(first).map(classOf), [
        "cell ts-gap",
        "cell track",
        "cell ts-gap",
        "cell track",
        "cell ts-gap",
      ]);
      assert.deepEqual(cellsOf(second).map(classOf), [
        "cell track",
        "cell ts-done",
        "cell ts-done",
        "cell track",
        "cell track",
      ]);
      assert.deepEqual(cellsOf(third).map(classOf), [
        "cell track",
        "cell track",
        "cell track",
        "cell ts-dispatched",
        "cell ts-dispatched",
      ]);
    },
  ));

test("each cell's title names its task, its ring and whether it is reached", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const [running] = [...container.querySelectorAll(".chart .slice")];
    assert.deepEqual(
      cellsOf(running).map((cell) => cell.querySelector("title")?.textContent),
      [
        "1-task red test: reached",
        "1-task working: reached",
        "1-task tests pass: not reached",
        "1-task checks: not reached",
        "1-task gate accepted: not reached",
      ],
    );
  }));

test("the rings run from red test outermost in to gate accepted innermost", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(3) }), cursorStep: "" }, (container) => {
    const chart = container.querySelector(".chart");
    assert.ok(chart, "a chart is drawn");
    const centre = centreOf(chart);
    const [running] = [...container.querySelectorAll(".chart .slice")];
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

test("the running slice's dot sits on the ring its at names, and on no other", () =>
  withView({ detail: detail({ epic, slices: syntheticSlices(35) }), cursorStep: "" }, (container) => {
    const chart = container.querySelector(".chart");
    assert.ok(chart, "a chart is drawn");
    const centre = centreOf(chart);
    const [running] = [...container.querySelectorAll(".chart .slice")];
    const dot = running.querySelector(".dot");
    assert.ok(dot, "the running slice has a dot");
    const radius = radiusOf(pointOfDot(dot), centre);
    const onRing = cellsOf(running).map((cell) => {
      const radii = pathOf(cell).map((point) => radiusOf(point, centre));
      return Math.min(...radii) < radius && radius < Math.max(...radii);
    });
    assert.deepEqual(onRing, [false, true, false, false, false], "at 2 is the working ring");
  }));

for (const count of [1, 3, 35, 150]) {
  test(`${count} slices start at the top, run clockwise, and each takes an equal angle`, () => {
    const slices = syntheticSlices(count).map((slice) => ({ ...slice, at: 1 }));
    return withView({ detail: detail({ epic, slices }), cursorStep: "" }, (container) => {
      const chart = container.querySelector(".chart");
      assert.ok(chart, "a chart is drawn");
      const centre = centreOf(chart);
      const span = (2 * Math.PI) / count;
      const groups = [...container.querySelectorAll(".chart .slice")];
      assert.equal(groups.length, count);
      groups.forEach((group, index) => {
        const start = index * span;
        for (const cell of cellsOf(group)) {
          const angles = pathOf(cell).map((point) => angleOf(point, centre));
          for (const expected of [start, start + span / 2, start + span]) {
            assert.ok(
              angles.some((angle) => sameAngle(angle, expected)),
              `slice ${index} has no point at ${expected}`,
            );
          }
        }
        const dot = group.querySelector(".dot");
        assert.ok(dot, `slice ${index} has a dot`);
        assert.ok(sameAngle(angleOf(pointOfDot(dot), centre), start + span / 2), `slice ${index} dot is mid-slice`);
      });
    });
  });
}

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
    assert.equal(container.querySelector(".problem"), null);
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
      assert.equal(container.querySelector("img"), null);
      assert.equal(container.querySelector("script"), null);
      assert.equal(container.querySelector("b"), null);
      assert.equal(container.querySelector("i"), null);
      assert.ok(container.textContent?.includes("<img src=x onerror=alert(1)>"));
      assert.ok(container.textContent?.includes("<b>done</b>"));
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
      assert.ok(container.textContent?.includes("<i>slice</i> tests pass: not reached"));
    },
  ));
