import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicRequirement, EpicRequirementSchema } from "./gen/orrery/v1/epics_pb";
import { CENTRE } from "./orbits";
import { Orrery } from "./orrery";
import type { Phase, Tone } from "./phases";
import { Pips } from "./pips";
import { type RecordTask, type TaskPlace, cellsOf } from "./record";
import { installTestDom, removeTestDom } from "./testdom";

const DEG = Math.PI / 180;
// EPS is the slack, in degrees, allowed where two arcs meet at a boundary the rounding of a path can move.
const EPS = 0.01;

// requirementOf is a requirement of the epic in the given server state, with its text.
function requirementOf(id: string, state: string, text = `${id} is true`): EpicRequirement {
  return create(EpicRequirementSchema, { id, text, state });
}

// taskOf is a record task that names the given requirements and sits at the given place: its cells and tone follow the
// place, and its title and words name it.
function taskOf(name: string, names: string[], place: TaskPlace = "planned"): RecordTask {
  const tone: Tone = place === "planned" ? "plan" : place === "integrated" ? "ok" : "run";
  return {
    task: name,
    title: `Title ${name}`,
    requirements: names,
    place,
    tone,
    cells: cellsOf(place, tone),
    words: `${name} words`,
    since: 0,
  };
}

// dispatch fires a DOM event on target and flushes Preact's queued render: act does it without a timer.
async function dispatch(target: Element, event: string): Promise<void> {
  await act(() => {
    target.dispatchEvent(new Event(event));
  });
}

// textsOf reads the text of each element the selector finds, in order.
function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// numbersOf reads the numbers of a path's data, in order.
function numbersOf(d: string | null): number[] {
  return (d ?? "").match(/-?\d+(\.\d+)?/g)?.map(Number) ?? [];
}

// angleOf is a point's angle in degrees, clockwise from 12 o'clock.
function angleOf(x: number, y: number): number {
  return (Math.atan2(x - CENTRE, CENTRE - y) / DEG + 360) % 360;
}

// arcOf reads the angles a rim segment or a cell runs between, in degrees: its outer arc's first point and its end. Each
// is "M x y A r r 0 large sweep x y ...", so the end point is the eighth and ninth numbers.
function arcOf(element: Element | null): [number, number] {
  const numbers = numbersOf(element?.getAttribute("d") ?? null);
  return [angleOf(numbers[0] ?? 0, numbers[1] ?? 0), angleOf(numbers[7] ?? 0, numbers[8] ?? 0)];
}

// segmentOf finds the rim segment of a requirement; a missing segment fails the test.
function segmentOf(container: HTMLElement, id: string): Element {
  const found = container.querySelector(`path.req[data-requirement="${id}"]`);
  if (found === null) {
    throw new Error(`no segment for ${id}`);
  }
  return found;
}

// cellsOfTask finds a task's five cells, in the order the rings draw them.
function cellsOfTask(container: HTMLElement, task: string): Element[] {
  return [...container.querySelectorAll(`path.cell[data-task="${task}"]`)];
}

// cellOf finds the cell of a task's ring; a missing cell fails the test.
function cellOf(container: HTMLElement, task: string, place: string): Element {
  const found = container.querySelector(`path.cell[data-task="${task}"][data-place="${place}"]`);
  if (found === null) {
    throw new Error(`no cell for ${task} ${place}`);
  }
  return found;
}

// within is whether the inner arc lies inside the outer one, to EPS.
function within(inner: [number, number], outer: [number, number]): boolean {
  return inner[0] >= outer[0] - EPS && inner[1] <= outer[1] + EPS;
}

// Chart is what one drawn chart is given: its tasks, its requirements, its proven set and its open handler.
interface Chart {
  tasks: RecordTask[];
  requirements: EpicRequirement[];
  proven?: ReadonlySet<string>;
  onOpenPhase?: (task: string, phase: Phase) => void;
}

async function withChart(chart: Chart, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <Orrery
        tasks={chart.tasks}
        requirements={chart.requirements}
        proven={chart.proven ?? new Set()}
        onOpenPhase={chart.onOpenPhase}
      />,
      container,
    );
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("ChartRim: one rim segment per Requirement, labelled with its id and toned by its server state", () =>
  withChart(
    {
      tasks: [taskOf("a", ["R1"]), taskOf("b", ["R2"]), taskOf("c", ["R3"]), taskOf("d", ["R4"]), taskOf("e", ["R5"])],
      requirements: [
        requirementOf("R1", "proven"),
        requirementOf("R2", "failed"),
        requirementOf("R3", "held"),
        requirementOf("R4", "in_flight"),
        requirementOf("R5", "not_reached"),
      ],
    },
    (container) => {
      const segments = [...container.querySelectorAll("path.req")];
      assert.equal(segments.length, 5, "one segment per Requirement, and no unlabelled group when every task names one");
      assert.deepEqual(
        segments.map((segment) => segment.getAttribute("data-requirement")),
        ["R1", "R2", "R3", "R4", "R5"],
      );
      assert.deepEqual(textsOf(container, "text.req-id"), ["R1", "R2", "R3", "R4", "R5"]);
      assert.deepEqual(
        segments.map((segment) => segment.getAttribute("class")),
        ["req proven", "req failed", "req held", "req in_flight", "req not_reached"],
      );
    },
  ));

test("ChartRim: a task's cells sit inside the first Requirement it names, and a name no Requirement lists is passed over", () =>
  withChart(
    {
      tasks: [taskOf("t1", ["R2", "R1"], "red"), taskOf("t2", ["R9", "R1"], "green")],
      requirements: [requirementOf("R1", "in_flight"), requirementOf("R2", "in_flight")],
    },
    (container) => {
      for (const cell of cellsOfTask(container, "t1")) {
        assert.equal(cell.getAttribute("data-requirement"), "R2", "t1 names R2 first");
        assert.ok(within(arcOf(cell), arcOf(segmentOf(container, "R2"))), "a cell of t1 lies outside R2's segment");
      }
      for (const cell of cellsOfTask(container, "t2")) {
        assert.equal(cell.getAttribute("data-requirement"), "R1", "t2's R9 is no Requirement, so its R1 holds it");
        assert.ok(within(arcOf(cell), arcOf(segmentOf(container, "R1"))), "a cell of t2 lies outside R1's segment");
      }
    },
  ));

test("ChartRim: tasks naming no Requirement sit in an unlabelled last group, after every Requirement's segment", () =>
  withChart(
    {
      tasks: [taskOf("t1", ["R1"]), taskOf("t2", ["R2"]), taskOf("t3", []), taskOf("t4", ["R9"])],
      requirements: [requirementOf("R1", "in_flight"), requirementOf("R2", "in_flight")],
    },
    (container) => {
      const segments = [...container.querySelectorAll("path.req")];
      const last = segments.at(-1);
      assert.equal(last?.getAttribute("class"), "req unlabelled", "the unlabelled group is the last segment");
      assert.equal(segments.length, 3, "two Requirement segments and one unlabelled group");
      assert.deepEqual(textsOf(container, "text.req-id"), ["R1", "R2"], "the unlabelled group bears no id");
      for (const task of ["t3", "t4"]) {
        for (const cell of cellsOfTask(container, task)) {
          assert.equal(cell.getAttribute("data-requirement"), null, `${task} sits in no Requirement`);
        }
      }
      assert.ok(
        arcOf(last ?? null)[0] >= arcOf(segmentOf(container, "R2"))[1] - EPS,
        "the unlabelled group starts after R2's segment ends",
      );
    },
  ));

test("ChartRim: a Requirement no task names keeps an empty segment, the width of one task's slot", () =>
  withChart(
    {
      tasks: [taskOf("t1", ["R1"])],
      requirements: [requirementOf("R1", "proven"), requirementOf("R2", "in_flight")],
    },
    (container) => {
      const empty = segmentOf(container, "R2");
      assert.equal(empty.getAttribute("class"), "req in_flight");
      assert.deepEqual(textsOf(container, "text.req-id"), ["R1", "R2"], "the empty segment is labelled");
      assert.equal(container.querySelectorAll('path.cell[data-requirement="R2"]').length, 0, "no cell sits in it");
      const [from, to] = arcOf(empty);
      assert.ok(to - from > 1, "the empty segment has an arc of its own");
      const [taskFrom, taskTo] = arcOf(segmentOf(container, "R1"));
      assert.ok(Math.abs(to - from - (taskTo - taskFrom)) < EPS, "one slot each, as one task takes one slot");
    },
  ));

test("ChartRim: the centre reads n of m proven, counting the Requirements the epic proves", () =>
  withChart(
    {
      tasks: [taskOf("t1", ["R1"])],
      requirements: [requirementOf("R1", "proven"), requirementOf("R2", "failed"), requirementOf("R3", "proven")],
      proven: new Set(["R1", "R3"]),
    },
    (container) => {
      assert.equal(container.querySelector("svg.orrery")?.getAttribute("aria-label"), "2 of 3 proven");
      assert.deepEqual(textsOf(container, "text.count"), ["2"]);
      assert.deepEqual(textsOf(container, "text.of"), ["of 3 proven"]);
    },
  ));

test("ChartRim: a click selects a Requirement's segment and captions it, and a hover captions nothing", () =>
  withChart(
    { tasks: [taskOf("t1", ["R1"])], requirements: [requirementOf("R1", "proven", "A user signs in")] },
    async (container) => {
      await dispatch(segmentOf(container, "R1"), "pointerenter");
      assert.equal(container.querySelectorAll("figcaption.orrery-cap").length, 0, "a hover captions nothing");
      await dispatch(segmentOf(container, "R1"), "click");
      assert.deepEqual(textsOf(container, "figcaption.orrery-cap"), ["R1 · proven · A user signs in"]);
      assert.equal(container.querySelectorAll("path.hl").length, 1, "the selected segment is outlined");
    },
  ));

test("ChartRim: a click selects a task and captions it, planned or not, and a hover over another task captions nothing", () => {
  const opened: string[] = [];
  return withChart(
    {
      tasks: [taskOf("t1", ["R1"], "implement"), taskOf("t2", ["R1"])],
      requirements: [requirementOf("R1", "in_flight")],
      onOpenPhase: (task) => opened.push(task),
    },
    async (container) => {
      await dispatch(cellOf(container, "t2", "red"), "click");
      assert.deepEqual(textsOf(container, "figcaption.orrery-cap"), ["Title t2 · t2 words"]);
      assert.deepEqual(opened, [], "a planned task selects but opens no phase");
      await dispatch(cellOf(container, "t1", "review"), "pointerenter");
      assert.deepEqual(textsOf(container, "figcaption.orrery-cap"), ["Title t2 · t2 words"], "a hover moves nothing");
    },
  );
});

test("ChartRim: the chart's cells and the step pips name the same five stages, red, implement, green, review, integrate, in order", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <div>
        <Orrery tasks={[taskOf("t1", ["R1"], "green")]} requirements={[requirementOf("R1", "in_flight")]} proven={new Set()} />
        <Pips cells={cellsOf("green", "run")} />
      </div>,
      container,
    );
    const chartStages = cellsOfTask(container, "t1").map((cell) => cell.getAttribute("data-place"));
    const label = container.querySelector("span.pips")?.getAttribute("aria-label") ?? "";
    const pipStages = label.split(", ").map((part) => part.split(" ")[0]);
    assert.deepEqual(chartStages, ["red", "implement", "green", "review", "integrate"]);
    assert.deepEqual(pipStages, chartStages);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
});
