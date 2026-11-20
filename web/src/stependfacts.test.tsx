import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import { StepCard, type StepEvent } from "./eventring";
import { StepFactsSchema } from "./gen/orrery/v1/orrery_pb";
import { installTestDom, removeTestDom } from "./testdom";

// The facts line of a step's end card: the counts it draws, its rounds and its duration, from the item's step facts.
// Each test's name carries the token StepEndFacts.

const step: StepEvent = {
  step: "s1",
  agent: "builder",
  end: "done",
  epic: "",
  task: "",
  brief: ["Shipped the index."],
};

// factsOf is a step's facts as the item carries them: its rounds, its seconds, and its line counts when counted.
function factsOf(fields: { rounds?: number; seconds?: bigint; counted?: boolean; files?: bigint; added?: bigint; removed?: bigint }) {
  return create(StepFactsSchema, {
    rounds: 0,
    seconds: 0n,
    counted: false,
    files: 0n,
    added: 0n,
    removed: 0n,
    ...fields,
  });
}

// withCard draws the step's card with facts into a fresh container, runs check on the container, then removes it.
async function withCard(facts: ReturnType<typeof factsOf> | undefined, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const card: JSX.Element = <StepCard event={step} facts={facts} />;
  try {
    await act(() => {
      render(card, container);
    });
    check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// factsLineOf is the facts line of the card, or null when the card draws none.
function factsLineOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(".facts");
}

// textsOf is the text of each element the facts line draws, in order.
function textsOf(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(`.facts ${selector}`)].map((element) => element.textContent ?? "");
}

test("StepEndFacts: a counted step draws its files and each line count in their own elements", async () => {
  const facts = factsOf({ rounds: 1, seconds: 480n, counted: true, files: 2n, added: 96n, removed: 4n });
  await withCard(facts, (container) => {
    assert.deepEqual(textsOf(container, "span"), ["2 files", "+96", "−4", "1 round", "8m"]);
    assert.deepEqual(textsOf(container, ".plus"), ["+96"], "the added lines draw in their own element");
    assert.deepEqual(textsOf(container, ".minus"), ["−4"], "the removed lines draw in their own element");
  });
});

test("StepEndFacts: a step whose lines were not counted draws no file or line count", async () => {
  const facts = factsOf({ rounds: 3, seconds: 780n, counted: false });
  await withCard(facts, (container) => {
    assert.deepEqual(textsOf(container, "span"), ["3 rounds", "13m"]);
    assert.equal(container.querySelectorAll(".facts .plus, .facts .minus").length, 0, "no line count is drawn");
  });
});

test("StepEndFacts: rounds draw only when above 0, as one round or as a count of rounds", async () => {
  await withCard(factsOf({ rounds: 0, seconds: 60n }), (container) => {
    assert.deepEqual(textsOf(container, "span"), ["1m"], "a step with no rounds draws no rounds");
  });
  await withCard(factsOf({ rounds: 1, seconds: 60n }), (container) => {
    assert.deepEqual(textsOf(container, "span"), ["1 round", "1m"], "one round draws in the singular");
  });
});

test("StepEndFacts: how long draws whenever facts are present, in seconds under a minute, minutes under an hour, then hours", async () => {
  const durations: [bigint, string][] = [
    [0n, "0s"],
    [45n, "45s"],
    [480n, "8m"],
    [3900n, "1h 5m"],
  ];
  for (const [seconds, drawn] of durations) {
    await withCard(factsOf({ seconds }), (container) => {
      assert.deepEqual(textsOf(container, "span"), [drawn], `a step that ran ${seconds} seconds draws ${drawn}`);
    });
  }
});

test("StepEndFacts: a step with no facts draws no facts line", async () => {
  await withCard(undefined, (container) => {
    assert.equal(factsLineOf(container), null, "no facts line is drawn");
    assert.equal(container.querySelectorAll(".facts").length, 0);
  });
});
