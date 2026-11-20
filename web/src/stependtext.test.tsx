import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { noEpics } from "./epics";
import { StepCard, stepEventOf } from "./eventring";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// The step's end cards over the delivered step_end they draw from: read from its prompt's block, with its brief. A
// card's return is read through the epic calls its fold is given. Each test's name carries the token StepEndText.

// stepBlock is the block a delivered step_end carries, as enginehost's CallerEventBlock writes it: its agent, end and
// status as attributes of the opening tag, its brief on lines of its own, and the line that points to the full return.
function stepBlock(brief: string[]): string {
  return [
    '<orrery-event kind="step_end" id="step:s1" agent="builder" end="done" status="done">',
    ...brief,
    "More: step_status gives the full result.",
    "</orrery-event>",
  ].join("\n");
}

// deliveredCard draws the card of a step_end whose prompt holds brief, with calls when its fold is given them.
function deliveredCard(brief: string[], calls?: EpicCalls): JSX.Element {
  const event = stepEventOf(stepBlock(brief));
  if (event === null) {
    throw new Error("a step_end block is read as a step's end");
  }
  return <StepCard event={event} calls={calls} />;
}

// callsReturning are the epic calls a step card's fold reads through: stepResult answers result, and no other call is
// made.
function callsReturning(result: string): EpicCalls {
  const unused = async (): Promise<never> => {
    throw new Error("a step card makes no epic call");
  };
  return {
    get: unused,
    pause: unused,
    resume: unused,
    answer: unused,
    openEpic: () => {},
    openStep: () => {},
    stepResult: async () => result,
    epics: noEpics,
  };
}

// withCard draws card into a fresh container, runs check on the container, then removes it.
async function withCard(card: JSX.Element, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(card, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// settle waits until check holds, each round running inside act, so the renders a fold's read causes are applied before
// the next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

function cardOf(container: HTMLElement): HTMLElement {
  const card = container.querySelector<HTMLElement>(".step-card");
  if (card === null) {
    throw new Error("the step card is drawn");
  }
  return card;
}

// summaryOf is the summary of the card's return fold, the control that opens it.
function summaryOf(container: HTMLElement): HTMLElement {
  const summary = container.querySelector<HTMLElement>("details > summary");
  if (summary === null) {
    throw new Error("the card draws its return fold");
  }
  return summary;
}

// openReturn opens the card's return fold and waits until its return is drawn.
async function openReturn(t: TestContext, container: HTMLElement): Promise<void> {
  await click(summaryOf(container));
  await settle(t, () => container.querySelector("details pre") !== null, "the return is drawn");
}

function textOf(root: ParentNode, selector: string): string | null | undefined {
  return root.querySelector(selector)?.textContent;
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

// outsideFold is the text of the card's children other than its fold.
function outsideFold(card: HTMLElement): string {
  return [...card.children]
    .filter((child) => !child.matches("details"))
    .map((child) => child.textContent)
    .join("\n");
}

// assertHead checks the head of a step's card: its agent, end and status, and its brief lines.
function assertHead(card: HTMLElement, brief: string[], what: string): void {
  assert.equal(textOf(card, ".step-agent"), "builder", `${what} names its agent`);
  assert.equal(textOf(card, ".step-end"), "end done", `${what} draws its end`);
  assert.equal(textOf(card, ".step-status"), "status done", `${what} draws its status`);
  assert.deepEqual(textsOf(card, ".step-summary"), brief, `${what} draws each brief line`);
}

// briefCases are the briefs a delivered step_end draws, one line each. A line that starts like an object is text.
const briefCases = [["Shipped the index.", "Ran the suite."], ['{"answer": "Shipped the index."']];

test("StepEndText: a delivered step_end card draws its agent, end and status, and its brief lines", async () => {
  for (const brief of briefCases) {
    await withCard(deliveredCard(brief), async (container) => {
      assertHead(cardOf(container), brief, "a delivered step_end");
    });
  }
});

// jsonResult is a JSON return on one line, as the server writes it, and jsonPretty is the same value as the fold
// draws it once opened.
const jsonResult = '{"answer":"Shipped the index.","evidence":["migration ran","suite green"],"verdict":"pass"}';
const jsonPretty = [
  "{",
  '  "answer": "Shipped the index.",',
  '  "evidence": [',
  '    "migration ran",',
  '    "suite green"',
  "  ],",
  '  "verdict": "pass"',
  "}",
].join("\n");

test("StepEndText: the return is folded and closed by default, and draws pretty-printed JSON once opened", async (t) => {
  await withCard(deliveredCard(["Shipped the index."], callsReturning(jsonResult)), async (container) => {
    const details = container.querySelector("details");
    assert.ok(details !== null, "a delivered step_end draws its return in a fold");
    assert.equal(details.open, false, "a delivered step_end folds its return closed by default");
    const summary = details.querySelector("summary");
    assert.ok(summary !== null, "a delivered step_end names its fold");
    assert.equal(summary.textContent, "return", "a delivered step_end names the fold return");
    await openReturn(t, container);
    assert.equal(details.open, true, "a delivered step_end opens its fold on a click on the summary");
    assert.equal(textOf(details, "pre"), jsonPretty, "a delivered step_end draws its JSON pretty-printed once opened");
  });
});

test("StepEndText: no line of raw JSON draws outside the fold of a delivered step_end", async (t) => {
  const jsonLines = jsonPretty.split("\n").map((line) => line.trim());
  await withCard(deliveredCard(["Shipped the index."], callsReturning(jsonResult)), async (container) => {
    await openReturn(t, container);
    const outside = outsideFold(cardOf(container));
    assert.ok(outside.includes("Shipped the index."), "a delivered step_end draws its brief outside the fold");
    assert.equal(outside.includes(jsonResult), false, "a delivered step_end draws the raw JSON on no line outside the fold");
    for (const line of jsonLines.filter((text) => text.includes('"'))) {
      assert.equal(outside.includes(line), false, `a delivered step_end draws the JSON line ${line} only inside the fold`);
    }
  });
});

// plainReturns are the returns that are not JSON: plain text over lines, and text that starts like an object and does
// not parse as one.
const plainReturns = ["Added the index.\nRan the suite.\nAll green.", '{"answer": "Shipped the index."'];

test("StepEndText: a return that is plain text draws whole in the fold, as it is", async (t) => {
  for (const result of plainReturns) {
    await withCard(deliveredCard(["Added the index."], callsReturning(result)), async (container) => {
      await openReturn(t, container);
      assert.equal(textOf(container, "details pre"), result, "a delivered step_end's fold holds the whole text");
    });
  }
});
