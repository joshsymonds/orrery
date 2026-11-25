import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";
import { act } from "preact/test-utils";

import type { Phase } from "./phases";
import { cellsOf, type RecordTask } from "./record";
import { TaskTable } from "./tasktable";
import { installTestDom, removeTestDom } from "./testdom";

// now is the clock the table reads its clocks against, in unix seconds.
const now = 100000;

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// taskOf is a record task in the given place with the given tone, its five cells read from the place as the record
// reads them.
function taskOf(task: string, place: RecordTask["place"], tone: RecordTask["tone"], init: Partial<RecordTask> = {}): RecordTask {
  return {
    task,
    title: `Name of ${task}`,
    requirements: ["R2", "R5"],
    place,
    tone,
    cells: cellsOf(place, tone),
    words: "implement · doing",
    since: 0,
    labels: [task],
    ...init,
  };
}

// fifteen is fifteen tasks in the implement ring, each running.
function fifteen(): RecordTask[] {
  return Array.from({ length: 15 }, (_, index) => taskOf(`t${index}`, "implement", "run"));
}

// rowsOf reads the task rows the table draws, past its head row.
function rowsOf(container: HTMLElement): Element[] {
  return Array.from(container.querySelectorAll(".tbl.tasks .tr:not(.head)"));
}

// foldOf reads the fold line the table draws, if it draws one.
function foldOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("button.fold-line");
}

test("EpicOrrery: a row shows its name, the requirements it proves, its five pips, its words and its clock", () =>
  withRender(<TaskTable tasks={[taskOf("t1", "implement", "run", { since: now - 125 })]} now={now} />, (container) => {
    const [row] = rowsOf(container);
    assert.ok(row !== undefined, "the table draws no row");
    assert.equal(row.querySelector("span.strong")?.textContent, "Name of t1");
    assert.deepEqual(Array.from(row.querySelectorAll("span.id"), (id) => id.textContent), ["R2", "R5"]);
    assert.deepEqual(
      Array.from(row.querySelectorAll("span.pips > i"), (mark) => mark.getAttribute("data-tone")),
      ["ok", "run", "none", "none", "none"],
    );
    assert.equal(row.querySelector("span.cell")?.textContent, "implement · doing");
    assert.equal(row.querySelector("span.cell")?.classList.contains("t-run"), true);
    assert.equal(row.querySelector("span.end")?.textContent, "2:05");
  }));

test("EpicOrrery: a row whose task has no clock shows an empty clock", () =>
  withRender(<TaskTable tasks={[taskOf("t1", "implement", "run")]} now={now} />, (container) => {
    const [row] = rowsOf(container);
    assert.equal(row?.querySelector("span.end")?.textContent, "");
  }));

test("EpicOrrery: pressing a row whose phase is open calls onOpenPhase once with its task and phase", () => {
  const opened: [string, Phase][] = [];
  return withRender(
    <TaskTable
      tasks={[taskOf("t1", "green", "run")]}
      now={now}
      onOpenPhase={(task, phase) => opened.push([task, phase])}
    />,
    (container) => {
      const row = container.querySelector<HTMLButtonElement>("button.tr");
      assert.ok(row !== null, "an open phase's row is not a button");
      act(() => row.click());
      assert.deepEqual(opened, [["t1", "checking"]]);
    },
  );
});

test("EpicOrrery: a planned task's row is not a button, and pressing it opens no phase", () => {
  const opened: [string, Phase][] = [];
  return withRender(
    <TaskTable
      tasks={[taskOf("t1", "planned", "plan", { words: "not started" })]}
      now={now}
      onOpenPhase={(task, phase) => opened.push([task, phase])}
    />,
    (container) => {
      assert.equal(container.querySelector("button.tr"), null);
      const row = rowsOf(container)[0];
      assert.ok(row !== undefined, "the table draws no row for the planned task");
      act(() => (row as HTMLElement).click());
      assert.deepEqual(opened, []);
    },
  );
});

test("EpicOrrery: fifteen tasks draw twelve rows and a fold line reading 3 more tasks", () =>
  withRender(<TaskTable tasks={fifteen()} now={now} />, (container) => {
    assert.equal(rowsOf(container).length, 12);
    assert.equal(foldOf(container)?.textContent, "3 more tasks");
  }));

test("EpicOrrery: pressing the fold line draws all fifteen rows, and the fold line goes", () =>
  withRender(<TaskTable tasks={fifteen()} now={now} />, (container) => {
    const fold = foldOf(container);
    assert.ok(fold !== null, "no fold line");
    act(() => fold.click());
    assert.equal(rowsOf(container).length, 15);
    assert.equal(foldOf(container), null);
  }));

test("EpicOrrery: twelve tasks draw twelve rows and no fold line", () =>
  withRender(<TaskTable tasks={fifteen().slice(0, 12)} now={now} />, (container) => {
    assert.equal(rowsOf(container).length, 12);
    assert.equal(foldOf(container), null);
  }));
