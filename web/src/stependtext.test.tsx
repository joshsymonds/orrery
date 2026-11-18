import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import { StepCard, stepEventOf, WaitingCard } from "./eventring";
import { PendingEventSchema } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";

// The step's end cards over the two sources they draw from: a delivered step_end, read from its prompt's text, and a
// pending step_end as the server lists it, with its headline and its full result. Each test's name carries the token
// StepEndText.

// stepBlock is the block a delivered step_end carries, as enginehost's CallerEventBlock and stepEndBody write it: the
// step's first line, its agent, and its whole result on the Summary line.
function stepBlock(result: string): string {
  return [
    '<orrery-event kind="step_end" id="step:s1">',
    "Step s1 ended: end done, status done, reason tests pass.",
    "Agent: builder",
    `Summary: ${result}`,
    "</orrery-event>",
  ].join("\n");
}

// deliveredCard draws the card of a step_end whose prompt holds result.
function deliveredCard(result: string): JSX.Element {
  const event = stepEventOf(stepBlock(result));
  if (event === null) {
    throw new Error("a step_end block is read as a step's end");
  }
  return <StepCard event={event} />;
}

// pendingCard draws the waiting card of a step_end the server holds, with its headline and its full result.
function pendingCard(headline: string, result: string): JSX.Element {
  const event = create(PendingEventSchema, {
    id: "step:s1",
    kind: "step_end",
    step: "s1",
    agent: "builder",
    end: "done",
    status: "done",
    reason: "tests pass",
    summary: headline,
    result,
    waits: "speak",
  });
  return <WaitingCard event={event} />;
}

// withCard draws card into a fresh container, runs check on the container, then removes it.
async function withCard(card: JSX.Element, check: (container: HTMLElement) => Promise<void> | void): Promise<void> {
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

function textOf(root: ParentNode, selector: string): string | null | undefined {
  return root.querySelector(selector)?.textContent;
}

// outsideFold is the text of the card's children other than its fold.
function outsideFold(card: HTMLElement): string {
  return [...card.children]
    .filter((child) => !child.matches("details"))
    .map((child) => child.textContent)
    .join("\n");
}

// headlineCases are the results whose headline is read from one of answer, summary and verdict. Each names the field
// the headline comes from, and the summary case gives an answer that is not a string, which the headline skips.
const headlineCases = [
  {
    field: "answer",
    result: '{"verdict":"fail","summary":"Ran the suite.","answer":"Shipped the index."}',
    headline: "Shipped the index.",
  },
  {
    field: "summary",
    result: '{"verdict":"fail","answer":["not","text"],"summary":"Ran the suite."}',
    headline: "Ran the suite.",
  },
  {
    field: "verdict",
    result: '{"verdict":"pass","notes":["two","items"]}',
    headline: "pass",
  },
];

// assertHead checks the head and headline of a step's card: its agent, end, status and headline, and the waiting dim
// and label when it waits.
function assertHead(card: HTMLElement, headline: string, what: string, waiting: boolean): void {
  assert.equal(textOf(card, ".step-agent"), "builder", `${what} names its agent`);
  assert.equal(textOf(card, ".step-end"), "end done", `${what} draws its end`);
  assert.equal(textOf(card, ".step-status"), "status done", `${what} draws its status`);
  assert.equal(textOf(card, ".step-summary"), headline, `${what} draws its headline`);
  assert.equal(card.classList.contains("waiting"), waiting, `${what} is dimmed only while it waits`);
  const label = waiting ? "arrives when you next speak" : "";
  assert.equal(textOf(card, ".wait-label") ?? "", label, `${what} labels its wait`);
}

test("StepEndText: delivered and pending step_end cards draw the headline from answer, summary or verdict", async () => {
  for (const { field, result, headline } of headlineCases) {
    await withCard(deliveredCard(result), (container) => {
      assertHead(cardOf(container), headline, `a delivered step_end whose ${field} is the headline`, false);
    });
    await withCard(pendingCard(headline, result), (container) => {
      assertHead(cardOf(container), headline, `a pending step_end whose ${field} is the headline`, true);
    });
  }
});

// plainCases are the results read by their first line: plain text, and text that starts like an object and does not
// parse as one.
const plainCases = [
  { result: "Added the index.\nRan the suite.\nAll green.", headline: "Added the index." },
  { result: '{"answer": "Shipped the index."', headline: '{"answer": "Shipped the index."' },
];

test("StepEndText: plain text draws its first line as the headline, and the whole text in the fold", async () => {
  for (const { result, headline } of plainCases) {
    await withCard(deliveredCard(result), (container) => {
      assertHead(cardOf(container), headline, "a delivered step_end read by its first line", false);
      assert.equal(textOf(container, "details pre"), result, "the fold holds the whole text, as it is");
    });
    await withCard(pendingCard(headline, result), (container) => {
      assertHead(cardOf(container), headline, "a pending step_end read by its first line", true);
      assert.equal(textOf(container, "details pre"), result, "the fold holds the whole text, as it is");
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

test("StepEndText: the return is folded and closed by default, and draws pretty-printed JSON once opened", async () => {
  const sources = [
    { what: "a delivered step_end", card: deliveredCard(jsonResult) },
    { what: "a pending step_end", card: pendingCard("Shipped the index.", jsonResult) },
  ];
  for (const { what, card } of sources) {
    await withCard(card, async (container) => {
      const details = container.querySelector("details");
      assert.ok(details !== null, `${what} draws its return in a fold`);
      assert.equal(details.open, false, `${what} folds its return closed by default`);
      const summary = details.querySelector("summary");
      assert.ok(summary !== null, `${what} names its fold`);
      assert.equal(summary.textContent, "return", `${what} names the fold return`);
      await click(summary);
      assert.equal(details.open, true, `${what} opens its fold on a click on the summary`);
      assert.equal(textOf(details, "pre"), jsonPretty, `${what} draws its JSON pretty-printed once opened`);
    });
  }
});

test("StepEndText: no line of raw JSON draws outside the fold of a delivered or a pending step_end", async () => {
  const jsonLines = jsonPretty.split("\n").map((line) => line.trim());
  const sources = [
    { what: "a delivered step_end", card: deliveredCard(jsonResult) },
    { what: "a pending step_end", card: pendingCard("Shipped the index.", jsonResult) },
  ];
  for (const { what, card } of sources) {
    await withCard(card, (container) => {
      const outside = outsideFold(cardOf(container));
      assert.ok(outside.includes("Shipped the index."), `${what} draws its headline outside the fold`);
      assert.equal(outside.includes(jsonResult), false, `${what} draws the raw JSON on no line outside the fold`);
      for (const line of jsonLines.filter((text) => text.includes('"'))) {
        assert.equal(outside.includes(line), false, `${what} draws the JSON line ${line} only inside the fold`);
      }
    });
  }
});
