import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicRequirementSchema, type EpicRequirement } from "./gen/orrery/v1/epics_pb";
import { CENTRE, GAP, RING_RADII, cellPath, point, sliceStart } from "./orbits";
import { Orrery } from "./orrery";
import { type Cell, type Phase, PLACES } from "./phases";
import type { RecordTask } from "./record";
import { installTestDom, removeTestDom } from "./testdom";

// Props is the orrery's props, so a test can pass its own tasks, requirements, proven set and open handler.
interface Props {
  tasks: RecordTask[];
  requirements: EpicRequirement[];
  proven: ReadonlySet<string>;
  onOpenPhase?: (task: string, phase: Phase) => void;
}

// requirementOf is a requirement of the epic in the given server state.
function requirementOf(id: string, state: string): EpicRequirement {
  return create(EpicRequirementSchema, { id, text: `${id} is true`, state });
}

// integrated is a task in the integrate ring's place: every cell ok, and it names R4, which is proven.
const integrated: RecordTask = {
  task: "t1",
  title: "Login",
  requirements: ["R4"],
  place: "integrated",
  tone: "ok",
  cells: ["ok", "ok", "ok", "ok", "ok"],
  words: "done",
  since: 0,
  labels: ["t1"],
};

// implementing is a task running in the implement ring: red is ok, implement is live, and the rest are not reached.
const implementing: RecordTask = {
  task: "t2",
  title: "Parser",
  requirements: ["R5"],
  place: "implement",
  tone: "run",
  cells: ["ok", "run", "none", "none", "none"],
  words: "implement · writing the parser",
  since: 100,
  labels: ["t2"],
};

// planned is a task before any ring: every cell none.
const planned: RecordTask = {
  task: "t3",
  title: "Lexer",
  requirements: ["R5"],
  place: "planned",
  tone: "plan",
  cells: ["none", "none", "none", "none", "none"],
  words: "not started",
  since: 0,
  labels: ["t3"],
};

// blocked is a task failing in the green ring, which names R4 and R5.
const blocked: RecordTask = {
  task: "t4",
  title: "Store",
  requirements: ["R4", "R5"],
  place: "green",
  tone: "bad",
  cells: ["ok", "ok", "bad", "none", "none"],
  words: "green · failed",
  since: 50,
  labels: ["t4"],
};

const tasks = [integrated, implementing, planned, blocked];
// R4 is proven, and R5 is in flight. Tasks t1 and t4 sit in R4's segment, and t2 and t3 in R5's.
const requirements = [requirementOf("R4", "proven"), requirementOf("R5", "in_flight")];
const proven: ReadonlySet<string> = new Set(["R4"]);

// The cell slot of each task: R4's segment holds t1 then t4, and R5's holds t2 then t3.
const slotOf: Record<string, number> = { t1: 0, t4: 1, t2: 2, t3: 3 };

// RECORD_GAP is the gap at 12 o'clock the record's chart opens, in degrees: wide enough that each ring's name reads whole.
const RECORD_GAP = 62;
const DEG = Math.PI / 180;
// CAPTION_LINE is the line of the caption under the count: the .of label's 16px line.
const CAPTION_LINE = 16;

// cellOf finds the cell of a task's ring; a missing cell fails the test.
function cellOf(container: HTMLElement, task: string, place: string): Element {
  const found = container.querySelector(`path.cell[data-task="${task}"][data-place="${place}"]`);
  if (found === null) {
    throw new Error(`no cell for ${task} ${place}`);
  }
  return found;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// numbersOf reads the numbers of a path's data, in order: a ring-way path is "M x y A r r 0 0 1 x y".
function numbersOf(d: string | null): number[] {
  return (d ?? "").match(/-?\d+(\.\d+)?/g)?.map(Number) ?? [];
}

// angleOf is a point's angle in degrees, clockwise from 12 o'clock.
function angleOf(x: number, y: number): number {
  return (Math.atan2(x - CENTRE, CENTRE - y) / DEG + 360) % 360;
}

async function withOrrery(
  check: (container: HTMLElement) => void | Promise<void>,
  props: Props = { tasks, requirements, proven },
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(
      <Orrery tasks={props.tasks} requirements={props.requirements} proven={props.proven} onOpenPhase={props.onOpenPhase} />,
      container,
    );
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("EpicOrrery: the five ring names read outside in, each on a textPath of its own ring-way path", () =>
  withOrrery((container) => {
    assert.deepEqual(textsOf(container, "text.ring-name"), ["red", "implement", "green", "review", "integrate"]);
    const names = [...container.querySelectorAll("text.ring-name")];
    names.forEach((name, ring) => {
      const textPath = name.querySelector("textPath");
      assert.equal(textPath?.getAttribute("startOffset"), "100%");
      const id = textPath?.getAttribute("href")?.slice(1) ?? "";
      const way = container.querySelector(`path.ring-way[id="${id}"]`);
      assert.notEqual(way, null, `no ring-way path ${id}`);
      const [startX, startY, , , , , , endX, endY] = numbersOf(way?.getAttribute("d") ?? null);
      const radius = RING_RADII[ring];
      assert.ok(Math.abs(Math.hypot(startX - CENTRE, startY - CENTRE) - radius) < 0.01, "the way starts off its ring");
      assert.ok(Math.abs(Math.hypot(endX - CENTRE, endY - CENTRE) - radius) < 0.01, "the way ends off its ring");
      const left = point(radius, (-RECORD_GAP / 2) * DEG);
      const first = point(radius, (RECORD_GAP / 2) * DEG);
      assert.ok(Math.abs(startX - left.x) < 0.01 && Math.abs(startY - left.y) < 0.01, "the way starts off the gap's left edge");
      assert.ok(Math.abs(endX - first.x) < 0.01 && Math.abs(endY - first.y) < 0.01, "the way ends off the first cell");
    });
  }));

test("EpicOrrery: each task draws five cells, one per ring, each on its ring's radius, in the slot its requirement gives it", () =>
  withOrrery((container) => {
    assert.equal(container.querySelectorAll("path.cell").length, tasks.length * PLACES.length);
    tasks.forEach((task) => {
      PLACES.forEach((place, ring) => {
        assert.equal(
          cellOf(container, task.task, place).getAttribute("d"),
          cellPath(tasks.length, slotOf[task.task], ring + 1, RECORD_GAP),
        );
      });
    });
  }));

test("EpicOrrery: a cell's class is its tone in its ring, so a task reads ok before its place, its tone at it and none after", () =>
  withOrrery((container) => {
    const classesOf = (task: string) =>
      PLACES.map((place) => {
        const cell = cellOf(container, task, place);
        return [...cell.classList].find((name) => name.startsWith("c-"))?.slice(2) as Cell;
      });
    assert.deepEqual(classesOf("t1"), ["ok", "ok", "ok", "ok", "ok"]);
    assert.deepEqual(classesOf("t2"), ["ok", "run", "none", "none", "none"]);
    assert.deepEqual(classesOf("t3"), ["none", "none", "none", "none", "none"]);
    assert.deepEqual(classesOf("t4"), ["ok", "ok", "bad", "none", "none"]);
  }));

test("EpicOrrery: each cell carries its ring as data-place and the phase that ring opens as data-phase", () =>
  withOrrery((container) => {
    const phases: Record<string, Phase> = {
      red: "implementing",
      implement: "implementing",
      green: "checking",
      review: "reviewing",
      integrate: "integrating",
    };
    for (const place of PLACES) {
      assert.equal(cellOf(container, "t2", place).getAttribute("data-phase"), phases[place]);
    }
  }));

test("EpicOrrery: each requirement's segment reads its server state, so R4 reads proven and R5 in flight", () =>
  withOrrery((container) => {
    const segments = container.querySelectorAll("path.req");
    assert.equal(segments.length, requirements.length);
    assert.equal(segments[0]?.getAttribute("class"), "req proven");
    assert.equal(segments[1]?.getAttribute("class"), "req in_flight");
  }));

test("EpicOrrery: a task that proves no requirement has an open rim, not a proven one", () =>
  withOrrery(
    (container) => {
      assert.equal(container.querySelectorAll("path.req.proven").length, 0);
    },
    { tasks: [{ ...planned, requirements: [] }], requirements: [], proven: new Set() },
  ));

test("EpicOrrery: the svg reads how many requirements are proven, and the centre reads n of m proven", () =>
  withOrrery((container) => {
    const svg = container.querySelector("svg.orrery");
    assert.equal(svg?.getAttribute("role"), "img");
    assert.equal(svg?.getAttribute("aria-label"), "1 of 2 proven");
    assert.deepEqual(textsOf(container, "text.count"), ["1"]);
    assert.deepEqual(textsOf(container, "text.of"), ["of 2 proven"]);
  }));

test("EpicOrrery: an epic with no tasks draws nothing", () =>
  withOrrery(
    (container) => {
      assert.equal(container.childNodes.length, 0);
    },
    { tasks: [], requirements: [], proven: new Set() },
  ));

test("EpicOrrery: 120 tasks draw 120 wedges, and the only text is the five ring names, the centre and the requirement's id", () => {
  const many: RecordTask[] = Array.from({ length: 120 }, (_, index) => ({
    ...planned,
    task: `unit-${index + 1}`,
    requirements: ["R1"],
  }));
  return withOrrery(
    (container) => {
      assert.equal(container.querySelectorAll("path.cell").length, 120 * PLACES.length);
      assert.equal(container.querySelectorAll("path.req").length, 1);
      const texts = [...container.querySelectorAll("svg.orrery text")];
      assert.equal(
        texts.every((element) => ["ring-name", "count", "of", "req-id"].includes(element.getAttribute("class") ?? "")),
        true,
      );
      assert.equal(container.querySelectorAll("text.ring-name").length, PLACES.length);
    },
    { tasks: many, requirements: [requirementOf("R1", "in_flight")], proven: new Set() },
  );
});

test("EpicOrrery: a click on a task's cell outlines its wedge and captions it with its title and words", () =>
  withOrrery(async (container) => {
    assert.equal(container.querySelectorAll("path.hl").length, 0);
    assert.equal(container.querySelectorAll("figcaption.orrery-cap").length, 0);
    await act(() => {
      cellOf(container, "t2", "green").dispatchEvent(new Event("click"));
    });
    assert.equal(container.querySelectorAll("path.hl").length, 1);
    assert.equal(textsOf(container, "figcaption.orrery-cap")[0], "Parser · implement · writing the parser");
  }));

test("EpicOrrery: a click on another task's cell moves the outline and caption to it", () =>
  withOrrery(async (container) => {
    await act(() => {
      cellOf(container, "t1", "red").dispatchEvent(new Event("click"));
    });
    assert.equal(textsOf(container, "figcaption.orrery-cap")[0], "Login · done");
    await act(() => {
      cellOf(container, "t4", "review").dispatchEvent(new Event("click"));
    });
    assert.equal(container.querySelectorAll("path.hl").length, 1);
    assert.equal(textsOf(container, "figcaption.orrery-cap")[0], "Store · green · failed");
  }));

test("EpicOrrery: a click on a task's cell calls onOpenPhase once with that task and the phase of its ring", () => {
  const opened: [string, string][] = [];
  return withOrrery(
    async (container) => {
      await act(() => {
        cellOf(container, "t2", "implement").dispatchEvent(new Event("click"));
      });
      await act(() => {
        cellOf(container, "t4", "green").dispatchEvent(new Event("click"));
      });
      assert.deepEqual(opened, [
        ["t2", "implementing"],
        ["t4", "checking"],
      ]);
    },
    { tasks, requirements, proven, onOpenPhase: (task, phase) => opened.push([task, phase]) },
  );
});

test("EpicOrrery: a click on a planned task's cell calls nothing", () => {
  const opened: [string, string][] = [];
  return withOrrery(
    async (container) => {
      await act(() => {
        cellOf(container, "t3", "red").dispatchEvent(new Event("click"));
      });
      assert.deepEqual(opened, []);
    },
    { tasks, requirements, proven, onOpenPhase: (task, phase) => opened.push([task, phase]) },
  );
});

test("EpicOrrery: each drawn chart's ring-name paths have ids no other chart in the document shares", async () => {
  const window = installTestDom();
  const first = document.createElement("div");
  const second = document.createElement("div");
  try {
    render(<Orrery tasks={tasks} requirements={requirements} proven={proven} />, first);
    render(<Orrery tasks={tasks} requirements={requirements} proven={proven} />, second);
    const ids = [...first.querySelectorAll("path.ring-way"), ...second.querySelectorAll("path.ring-way")].map(
      (element) => element.id,
    );
    assert.equal(ids.length, 2 * PLACES.length);
    assert.equal(new Set(ids).size, ids.length);
  } finally {
    render(null, first);
    render(null, second);
    await removeTestDom(window);
  }
});

test("EpicOrrery: the record's gap at 12 o'clock is 62 degrees, so its first cell starts at +31 degrees for any count", async () => {
  for (const count of [1, 4, 120]) {
    const many: RecordTask[] = Array.from({ length: count }, (_, index) => ({
      ...planned,
      task: `unit-${index + 1}`,
      requirements: ["R1"],
    }));
    await withOrrery(
      (container) => {
        const [x, y] = numbersOf(cellOf(container, "unit-1", "red").getAttribute("d"));
        assert.ok(Math.abs(angleOf(x ?? 0, y ?? 0) - RECORD_GAP / 2) < 1.5, `the first cell of ${count} starts off +31 degrees`);
      },
      { tasks: many, requirements: [], proven: new Set() },
    );
  }
});

test("EpicOrrery: the centre count and its caption sit on their own baselines, a caption line apart, with no central baseline", () =>
  withOrrery((container) => {
    const [count] = container.querySelectorAll("text.count");
    const [of] = container.querySelectorAll("text.of");
    assert.equal(count?.getAttribute("dominant-baseline"), null);
    assert.equal(of?.getAttribute("dominant-baseline"), null);
    const apart = Number(of?.getAttribute("y")) - Number(count?.getAttribute("y"));
    assert.ok(apart >= CAPTION_LINE, `the caption sits ${apart} units under the count, less than a caption line`);
  }));

test("EpicOrrery: a slice drawn with no gap, as the inbox's mini chart draws it, keeps the 16 degree gap", () => {
  assert.equal(GAP, 16);
  for (const count of [1, 4, 120]) {
    assert.equal(sliceStart(count, 0), sliceStart(count, 0, GAP));
    assert.notEqual(sliceStart(count, 0), sliceStart(count, 0, 62));
    for (let ring = 0; ring < RING_RADII.length; ring++) {
      assert.equal(cellPath(count, 0, ring), cellPath(count, 0, ring, GAP));
    }
  }
});
