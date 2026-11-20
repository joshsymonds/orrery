import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";
import { act } from "preact/test-utils";

import { liveEpoch } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { Phase } from "./phases";
import { ContextBand, EpicPin } from "./taskband";
import { installTestDom, removeTestDom } from "./testdom";

// now is the fixtures' instant in unix seconds: the clock the band reads its durations against.
const now = Date.parse(liveEpoch) / 1000;

// rota is lighthouse/rota-export: it asks a question, and its task T2 runs. orbit is orrery/orbit-labels: it asks nothing
// and has no step running.
const rota = "lighthouse/rota-export";
const orbit = "orrery/orbit-labels";

// epicOf is the fixtures' Epics.Get answer for an epic id.
function epicOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

// withRender renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withRender(node: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// textOf reads the text of the first element under root that matches the selector, or null.
function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// chipsOf reads the task chips the strip draws, in order.
function chipsOf(container: HTMLElement): HTMLButtonElement[] {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("nav.tstrip button.tchip"));
}

// pipsOf reads the tones of a chip's five pips.
function pipsOf(chip: Element): (string | null)[] {
  return Array.from(chip.querySelectorAll("span.pips > i"), (mark) => mark.getAttribute("data-tone"));
}

test("TaskBand: an asking epic's pin draws its slug, its state and task words, and its blocker", () =>
  withRender(<EpicPin detail={epicOf(rota)} now={now} onOpen={() => {}} />, (container) => {
    const pin = container.querySelector("button.epic-pin");
    assert.ok(pin !== null, "the pin is not a button.epic-pin");
    assert.equal(textOf(pin, ".slug"), "rota-export");
    assert.equal(textOf(pin, ".words"), "building · 49 min · 2 of 7 tasks done");
    assert.equal(textOf(pin, ".blocker"), "asks you a question · 2 min");
    assert.equal(textOf(pin, ".open"), "open the epic");
    assert.equal(textOf(pin, ".chev"), "›");
  }));

test("TaskBand: a quiet epic's pin draws no blocker", () =>
  withRender(<EpicPin detail={epicOf(orbit)} now={now} onOpen={() => {}} />, (container) => {
    const pin = container.querySelector("button.epic-pin");
    assert.ok(pin !== null, "the pin is not a button.epic-pin");
    assert.equal(textOf(pin, ".slug"), "orbit-labels");
    assert.equal(textOf(pin, ".words"), "releasing · 4 min · 3 of 3 tasks done");
    assert.equal(pin.querySelector(".blocker"), null);
  }));

test("TaskBand: pressing the pin opens the epic once", () => {
  let opened = 0;
  return withRender(
    <EpicPin
      detail={epicOf(rota)}
      now={now}
      onOpen={() => {
        opened += 1;
      }}
    />,
    (container) => {
      const pin = container.querySelector<HTMLButtonElement>("button.epic-pin");
      assert.ok(pin !== null, "the pin is not a button.epic-pin");
      act(() => pin.click());
      assert.equal(opened, 1);
    },
  );
});

test("TaskBand: the strip draws one chip per task in task order, each with five pips", () =>
  withRender(<ContextBand detail={epicOf(rota)} now={now} current="T2" onOpenEpic={() => {}} onOpenTask={() => {}} />, (container) => {
    const chips = chipsOf(container);
    assert.deepEqual(
      chips.map((chip) => textOf(chip, ".tt")),
      ["tz-fixtures", "export-cli", "rota-endpoint", "shift-store", "csv-writer", "readme", "audit-log"],
    );
    assert.deepEqual(
      chips.map((chip) => pipsOf(chip)),
      [
        ["ok", "mine", "none", "none", "none"],
        ["ok", "run", "none", "none", "none"],
        ["ok", "ok", "ok", "ok", "ok"],
        ["ok", "ok", "ok", "ok", "ok"],
        ["ok", "ok", "ok", "ok", "acc"],
        ["none", "none", "none", "none", "none"],
        ["none", "none", "none", "none", "none"],
      ],
    );
  }));

test("TaskBand: the strip is labelled as this epic's tasks, and only the current chip is marked", () =>
  withRender(<ContextBand detail={epicOf(rota)} now={now} current="T2" onOpenEpic={() => {}} onOpenTask={() => {}} />, (container) => {
    assert.equal(container.querySelector("nav.tstrip")?.getAttribute("aria-label"), "this epic's tasks");
    const chips = chipsOf(container);
    assert.deepEqual(
      chips.map((chip) => chip.getAttribute("aria-current")),
      [null, "page", null, null, null, null, null],
    );
    assert.deepEqual(
      chips.map((chip) => chip.classList.contains("cur")),
      [false, true, false, false, false, false, false],
    );
  }));

test("TaskBand: each other chip opens its task at its phase, and a planned task opens at implementing", () => {
  const opened: [string, Phase][] = [];
  return withRender(
    <ContextBand
      detail={epicOf(rota)}
      now={now}
      current="T2"
      onOpenEpic={() => {}}
      onOpenTask={(task, phase) => {
        opened.push([task, phase]);
      }}
    />,
    (container) => {
      const chips = chipsOf(container);
      for (const index of [0, 2, 4, 5]) {
        const chip = chips[index];
        assert.ok(chip !== undefined, `the strip draws no chip ${index}`);
        act(() => chip.click());
      }
      assert.deepEqual(opened, [
        ["T1", "implementing"],
        ["T3", "integrating"],
        ["T5", "integrating"],
        ["T6", "implementing"],
      ]);
    },
  );
});

test("TaskBand: pressing the current chip opens nothing", () => {
  const opened: [string, Phase][] = [];
  return withRender(
    <ContextBand
      detail={epicOf(rota)}
      now={now}
      current="T2"
      onOpenEpic={() => {}}
      onOpenTask={(task, phase) => {
        opened.push([task, phase]);
      }}
    />,
    (container) => {
      const chip = chipsOf(container)[1];
      assert.ok(chip !== undefined, "the strip draws no current chip");
      act(() => chip.click());
      assert.deepEqual(opened, []);
    },
  );
});
