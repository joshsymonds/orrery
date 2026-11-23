import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import {
  type EpicHistoryEntry,
  EpicHistoryEntrySchema,
  type EpicStep,
  EpicStepSchema,
  type EvidenceRun,
  EvidenceRunSchema,
} from "./gen/orrery/v1/epics_pb";
import { JustHappened } from "./happenings";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";
import { styleDeclaration } from "./teststyle";

function entry(init: MessageInitShape<typeof EpicHistoryEntrySchema>): EpicHistoryEntry {
  return create(EpicHistoryEntrySchema, init);
}

function run(init: MessageInitShape<typeof EvidenceRunSchema>): EvidenceRun {
  return create(EvidenceRunSchema, init);
}

function step(init: MessageInitShape<typeof EpicStepSchema>): EpicStep {
  return create(EpicStepSchema, init);
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

async function withRender(
  element: JSX.Element,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// press clicks the first element under root that selector names, inside act so its effects are drawn.
async function press(root: ParentNode, selector: string): Promise<void> {
  await act(() => {
    root.querySelector<HTMLElement>(selector)?.click();
  });
}

// opened is a spy for onOpenStep: the steps it was asked to open, in order.
function spy(): { opened: string[]; onOpenStep: (step: string) => void } {
  const opened: string[] = [];
  return { opened, onOpenStep: (step) => opened.push(step) };
}

test("HistorySentences: each line draws the server's sentence for its entry, not a label built from its fields", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "h1",
      sentence: "Planner turn 2 succeeded: outlined the rota export.",
      item: { case: "turn", value: { seq: 2n, state: "succeeded", outcome: "Outlined 3 units." } },
    }),
    entry({
      time: 200n,
      cursor: "h2",
      sentence: "R1 green run · passed.",
      item: { case: "evidence", value: { item: "R1", phase: "green", state: "ended", passed: true } },
      runs: [run({ id: "r1", item: "R1", phase: "green", state: "ended", passed: true })],
    }),
    entry({
      time: 300n,
      cursor: "h3",
      sentence: "T4 ended: done.",
      item: { case: "step", value: { id: "s4", task: "T4", end: "done" } },
    }),
    entry({
      time: 400n,
      cursor: "h4",
      sentence: "Decision by you: use the table.",
      item: { case: "decision", value: { text: "use the table", by: "owner" } },
    }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "Decision by you: use the table.",
      "T4 ended: done.",
      "R1 green run · passed.",
    ]);
  });
});

test("HistorySentences: a turn's line opens in place to its outline and decisions, and a second press shuts it", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "t1",
      sentence: "Planner turn 1 succeeded.",
      item: {
        case: "turn",
        value: {
          seq: 1n,
          state: "succeeded",
          outline: "shift-store, rota-endpoint, csv-writer",
          decisions: ["use the table", "keep the audit log"],
        },
      },
    }),
    entry({ time: 200n, cursor: "t2", sentence: "build one is in", item: { case: "tell", value: { text: "build one is in" } } }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, async (container) => {
    assert.equal(container.querySelector("section.happen.just button.happen-line")?.getAttribute("aria-expanded"), "false");
    assert.equal(container.querySelectorAll("section.happen.just .detail").length, 0, "shut at first");
    assert.equal(container.querySelectorAll("section.happen.just div.happen-line").length, 1, "a tell has no detail to open");
    await press(container, "section.happen.just button.happen-line");
    assert.deepEqual(textsOf(container, "section.happen.just .outline"), ["shift-store, rota-endpoint, csv-writer"]);
    assert.deepEqual(textsOf(container, "section.happen.just .decision"), ["use the table", "keep the audit log"]);
    const turnRow = container.querySelectorAll("section.happen.just li")[1];
    assert.equal(turnRow?.querySelectorAll(".detail").length, 1, "the detail sits in the line's own row");
    assert.deepEqual(textsOf(container, "section.happen.just .at"), [clock(200n), clock(100n)], "no other line moved");
    assert.equal(container.querySelector("section.happen.just button.happen-line")?.getAttribute("aria-expanded"), "true");
    await press(container, "section.happen.just button.happen-line");
    assert.equal(container.querySelectorAll("section.happen.just .detail").length, 0);
  });
});

test("HistorySentences: a line with no detail to open is a plain row, and a click on it opens nothing", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "n1",
      sentence: "Planner turn 2 succeeded.",
      item: { case: "turn", value: { seq: 2n, state: "succeeded" } },
    }),
    entry({
      time: 200n,
      cursor: "n2",
      sentence: "R1 green run · passed.",
      item: { case: "evidence", value: { item: "R1", phase: "green", state: "ended", passed: true } },
    }),
    entry({
      time: 300n,
      cursor: "n3",
      sentence: "b1 started.",
      item: { case: "step", value: { id: "s1", task: "b1", end: "" } },
    }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, async (container) => {
    assert.equal(container.querySelectorAll("section.happen.just button.happen-line").length, 0, "no line is a button");
    assert.equal(container.querySelectorAll("section.happen.just div.happen-line").length, 3);
    await press(container, "section.happen.just div.happen-line");
    assert.equal(container.querySelectorAll("section.happen.just .detail").length, 0, "nothing opens");
  });
});

test("HistorySentences: an evidence line opens in place to each run's results, with its judge's verdict and reason", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "e1",
      sentence: "R2 green run · failed.",
      item: { case: "evidence", value: { item: "R2", phase: "green", state: "ended", passed: false } },
      runs: [
        run({
          id: "r2",
          item: "R2",
          phase: "green",
          state: "ended",
          passed: false,
          exit: 1,
          timedOut: true,
          verdict: "fails",
          judgeReason: "The export overwrote the file.",
        }),
        run({ id: "r3", item: "R2", phase: "green", state: "judging", passed: false }),
      ],
    }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, async (container) => {
    await press(container, "section.happen.just button.happen-line");
    assert.deepEqual(textsOf(container, "section.happen.just .run-result p"), [
      "failed · exit 1 · timed out",
      "judge: fails · The export overwrote the file.",
      "judging",
      "judge: no verdict",
    ]);
  });
});

test("HistorySentences: a step line opens to its checks, and its door opens the step's page", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "p1",
      sentence: "T2 ended: checks failed.",
      item: { case: "step", value: { id: "st-t2", task: "T2", end: "checks_failed" } },
    }),
  ];
  const steps = [
    step({
      id: "st-t2",
      task: "T2",
      end: "checks_failed",
      checks: [{ round: 1, name: "lint", ok: false, detail: "exit 1" }],
    }),
  ];
  const { opened, onOpenStep } = spy();
  await withRender(<JustHappened entries={entries} steps={steps} onOpenStep={onOpenStep} />, async (container) => {
    assert.equal(container.querySelectorAll("section.happen.just button.open-step").length, 0, "the door waits for the line");
    await press(container, "section.happen.just button.happen-line");
    assert.deepEqual(textsOf(container, "section.happen.just .check p"), ["round 1 · lint · failed", "exit 1"]);
    assert.equal(textOf(container, "section.happen.just button.open-step"), "Open the step");
    await press(container, "section.happen.just button.open-step");
    assert.deepEqual(opened, ["st-t2"]);
  });
});

test("HistorySentences: an evidence run's judge step opens that step's page, and a run with none has no door", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "j1",
      sentence: "R1 green run · passed.",
      item: { case: "evidence", value: { item: "R1", phase: "green", state: "ended", passed: true } },
      runs: [
        run({ id: "rj", item: "R1", phase: "green", state: "ended", passed: true, verdict: "holds", judgeStep: "st-judge" }),
        run({ id: "rn", item: "R1", phase: "red", state: "ended", passed: true, verdict: "fails" }),
      ],
    }),
  ];
  const { opened, onOpenStep } = spy();
  await withRender(<JustHappened entries={entries} steps={[]} onOpenStep={onOpenStep} />, async (container) => {
    await press(container, "section.happen.just button.happen-line");
    assert.equal(container.querySelectorAll("section.happen.just button.open-step").length, 1);
    assert.equal(textOf(container, "section.happen.just button.open-step"), "Open the judge step");
    await press(container, "section.happen.just button.open-step");
    assert.deepEqual(opened, ["st-judge"]);
  });
});

test("HistorySentences: each line's time is drawn in the mono family at the small size", async () => {
  const entries = [
    entry({ time: 100n, cursor: "m1", sentence: "build one is in", item: { case: "tell", value: { text: "build one is in" } } }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.equal(textOf(container, "section.happen.just span.at"), clock(100n));
    assert.equal(styleDeclaration(".epic-page .happen .at", "font"), "var(--o-type-small)");
    const tokens = readTokens();
    assert.match(tokens, /--o-type-small: 400 13px\/20px var\(--o-font-mono\);/);
  });
});

test("HistorySentences: the section's head is round 6's .sec head, with the retry icon in hue 1", async () => {
  await withRender(<JustHappened entries={[]} steps={[]} />, (container) => {
    const head = container.querySelector("section.happen.just > .sec[data-hue='1']");
    assert.ok(head, "the head is a .sec with data-hue 1");
    assert.equal(textOf(head, ".name"), "Just happened");
    const icon = head.querySelector("svg.icon");
    assert.ok(icon, "the head draws an icon");
    assert.equal(icon.querySelector("path")?.getAttribute("d"), "M3.2 8a4.8 4.8 0 1 0 1.5-3.5M3 3v3.2h3.2");
    assert.equal(styleDeclaration('.happen.just > .sec[data-hue="1"]', "--hue"), "var(--o-series-1)");
    assert.equal(styleDeclaration(".happen.just > .sec > .icon", "color"), "var(--hue)");
    const sheet = readFileSync(join(process.cwd(), "web/src/style.css"), "utf8");
    assert.doesNotMatch(sheet, /^\.sec\[data-hue/m, "no base .sec rule takes the head's hue, so no other section is tinted");
    assert.doesNotMatch(sheet, /^\.sec > \.icon/m, "no base .sec rule colours an icon, so no other section's icon is tinted");
  });
});

// readTokens is the design tokens' sheet, which defines the small type and the mono family it names.
function readTokens(): string {
  return readFileSync(join(process.cwd(), "web/src/tokens.css"), "utf8");
}
