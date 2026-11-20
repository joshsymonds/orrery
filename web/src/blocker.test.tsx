import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { AskFirst, QuestionCard } from "./blocker";
import { type EpicQuestion, EpicQuestionSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

type CardProps = Parameters<typeof QuestionCard>[0];

// threeChoices are the choices of the card tests: each names its id and what it does, which the card sends as its text.
const threeChoices = [
  { id: "composite", does: "Add a composite index on keeper and shift." },
  { id: "partial", does: "Add a partial index on open shifts only." },
  { id: "none", does: "Leave the table as it is." },
];
// recommendedQuestion asks a question that recommends partial, the second choice, for its why.
const recommendedQuestion = create(EpicQuestionSchema, {
  id: "Q1",
  text: "Which index should it add?",
  choices: threeChoices,
  recommendation: { choice: "partial", why: "open shifts are the only rows the backfill scans", confidence: "high" },
});
// unrecommendedQuestion asks a question that recommends none.
const unrecommendedQuestion = create(EpicQuestionSchema, {
  id: "Q2",
  text: "Which lane should it use?",
  choices: threeChoices,
});

const answerFailedLine = "Not answered. Your answer is kept.";

// answerNothing answers with no error, as an answer that is sent does.
const answerNothing = () => Promise.resolve("");

// withTree renders a tree into a fresh container, runs check on it, and removes it.
async function withTree(tree: JSX.Element, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(tree, container);
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

// withCard renders a question's card, keyed by the question as the page keys it, runs check on it, and removes it.
async function withCard(
  init: { question: EpicQuestion; onAnswer?: CardProps["onAnswer"] },
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  return withTree(
    <QuestionCard
      key={init.question.id}
      question={init.question}
      numberKeys
      onAnswer={init.onAnswer ?? answerNothing}
    />,
    check,
  );
}

// settle lets a pending answer resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

function findButton(root: ParentNode, selector: string, text: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>(selector)].find((button) => button.textContent === text);
}

// press clicks the button that matches selector and reads text, and reports whether one was drawn.
async function press(root: ParentNode, selector: string, text: string): Promise<boolean> {
  const button = findButton(root, selector, text);
  if (button === undefined) {
    return false;
  }
  await act(() => {
    button.click();
  });
  return true;
}

// pressKey sends a key to the document, as the keyboard does when no field has focus.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// cards are the option cards of the question: its choices in order, then Something else.
function cards(root: ParentNode): HTMLButtonElement[] {
  return [...root.querySelectorAll<HTMLButtonElement>("form.answer .opts > .opt")];
}

// selectedIndex is the card that is selected, or -1 when none is.
function selectedIndex(root: ParentNode): number {
  return cards(root).findIndex((card) => card.classList.contains("sel"));
}

// clickCard clicks the card at index, as the owner's pointer does.
async function clickCard(root: ParentNode, index: number): Promise<void> {
  const card = cards(root)[index];
  assert.ok(card, `a card ${index + 1} is drawn`);
  await act(() => {
    card.click();
  });
}

function confirmButton(root: ParentNode): HTMLButtonElement | null {
  return root.querySelector<HTMLButtonElement>("form.answer .act.primary");
}

function confirmDisabled(root: ParentNode): boolean {
  return confirmButton(root)?.disabled === true;
}

function answerBox(root: ParentNode): HTMLTextAreaElement | null {
  return root.querySelector<HTMLTextAreaElement>('textarea[aria-label="answer"]');
}

// type sets the answer box's text as a typist does: its value, then its input event.
async function type(root: ParentNode, text: string): Promise<void> {
  const box = answerBox(root);
  await act(() => {
    if (box) {
      box.value = text;
      box.dispatchEvent(new Event("input", { bubbles: true }));
    }
  });
}

function answerText(root: ParentNode): string | null {
  return answerBox(root)?.value ?? null;
}

function askFirst(onConfirm: () => void): JSX.Element {
  return <AskFirst label="Remove" question="Remove it?" confirm="Yes, remove" keep="Keep" onConfirm={onConfirm} />;
}

test("AskFirst draws its label, and its first click asks and calls nothing", () => {
  let confirms = 0;
  return withTree(
    askFirst(() => {
      confirms++;
    }),
    async (container) => {
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.equal(await press(container, "button.act", "Remove"), true);
      assert.equal(confirms, 0);
      assert.equal(textOf(container, "span.sure"), "Remove it?");
      assert.deepEqual(textsOf(container, "button.act.primary"), ["Yes, remove"]);
      assert.deepEqual(textsOf(container, "button.act"), ["Yes, remove", "Keep"]);
      assert.equal(await press(container, "button.act.primary", "Yes, remove"), true);
      assert.equal(confirms, 1);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
    },
  );
});

test("AskFirst's keep returns to its label and calls nothing", () => {
  let confirms = 0;
  return withTree(
    askFirst(() => {
      confirms++;
    }),
    async (container) => {
      assert.equal(await press(container, "button.act", "Remove"), true);
      assert.equal(await press(container, "button.act", "Keep"), true);
      assert.equal(confirms, 0);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
    },
  );
});

test("an open question draws its text and a card per choice, then Something else, each with its key and what it does", () =>
  withCard({ question: recommendedQuestion }, async (container) => {
    assert.equal(textOf(container, "form.answer p.question"), "Which index should it add?");
    assert.deepEqual(textsOf(container, "form.answer .opts > .opt kbd"), ["1", "2", "3", "4"]);
    assert.deepEqual(textsOf(container, "form.answer .opts > .opt b"), [
      "composite",
      "partial",
      "none",
      "Something else",
    ]);
    assert.deepEqual(textsOf(container, "form.answer .opts > .opt small:not(.why)"), [
      "Add a composite index on keeper and shift.",
      "Add a partial index on open shifts only.",
      "Leave the table as it is.",
    ]);
    assert.equal(answerBox(container), null, "Something else keeps its text box hidden until it is picked");
  }));

test("the recommended card is marked with its why and starts selected, and confirm names it", () =>
  withCard({ question: recommendedQuestion }, async (container) => {
    assert.equal(cards(container)[1].querySelector("em")?.textContent, "recommended");
    assert.equal(container.querySelectorAll("form.answer .opts em").length, 1, "only the recommended card is marked");
    assert.equal(textOf(container, "form.answer .opt small.why"), "open shifts are the only rows the backfill scans");
    assert.equal(selectedIndex(container), 1);
    assert.equal(confirmButton(container)?.textContent, "Answer with partial");
    assert.equal(confirmDisabled(container), false);
  }));

test("a question that recommends none starts with nothing selected and confirm disabled", () =>
  withCard({ question: unrecommendedQuestion }, async (container) => {
    assert.equal(selectedIndex(container), -1);
    assert.equal(container.querySelectorAll("form.answer .opts em").length, 0);
    assert.equal(confirmButton(container)?.textContent, "Answer");
    assert.equal(confirmDisabled(container), true);
  }));

test("a click on a card selects it, and confirm sends its id and what it does", () => {
  const calls: [string, string, string][] = [];
  return withCard(
    {
      question: recommendedQuestion,
      onAnswer: async (questionId, choice, text) => {
        calls.push([questionId, choice, text]);
        return "";
      },
    },
    async (container) => {
      await clickCard(container, 2);
      assert.equal(selectedIndex(container), 2);
      assert.equal(confirmButton(container)?.textContent, "Answer with none");
      assert.equal(await press(container, "form.answer .act.primary", "Answer with none"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "none", "Leave the table as it is."]]);
    },
  );
});

test("a number key selects the card it names, and confirm sends that choice", () => {
  const calls: [string, string, string][] = [];
  return withCard(
    {
      question: recommendedQuestion,
      onAnswer: async (questionId, choice, text) => {
        calls.push([questionId, choice, text]);
        return "";
      },
    },
    async (container) => {
      await pressKey("1");
      assert.equal(selectedIndex(container), 0);
      await pressKey("5");
      assert.equal(selectedIndex(container), 0, "a key that names no card leaves the selection as it was");
      assert.equal(await press(container, "form.answer .act.primary", "Answer with composite"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "composite", "Add a composite index on keeper and shift."]]);
    },
  );
});

test("Something else reveals a text box, and confirm sends other with the trimmed text once it has some", () => {
  const calls: [string, string, string][] = [];
  return withCard(
    {
      question: recommendedQuestion,
      onAnswer: async (questionId, choice, text) => {
        calls.push([questionId, choice, text]);
        return "";
      },
    },
    async (container) => {
      await clickCard(container, 3);
      assert.equal(answerBox(container) !== null, true, "picking Something else draws its box");
      assert.equal(confirmDisabled(container), true, "confirm is disabled while the text is blank");
      await type(container, "   ");
      assert.equal(confirmDisabled(container), true, "a blank text keeps confirm disabled");
      await type(container, "  Use main  ");
      assert.equal(confirmDisabled(container), false);
      assert.equal(confirmButton(container)?.textContent, "Answer with Something else");
      assert.equal(await press(container, "form.answer .act.primary", "Answer with Something else"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "other", "Use main"]]);
      assert.equal(answerText(container), "", "a recorded answer clears the box");
    },
  );
});

test("a refused answer shows its reason under the card and keeps the selection, and a later success clears it", () => {
  let attempts = 0;
  return withCard(
    {
      question: recommendedQuestion,
      onAnswer: async () => {
        attempts++;
        return attempts === 1 ? "question closed" : "";
      },
    },
    async (container) => {
      await clickCard(container, 2);
      assert.equal(await press(container, "form.answer .act.primary", "Answer with none"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), "question closed");
      assert.equal(selectedIndex(container), 2, "the refused selection is kept");
      assert.equal(await press(container, "form.answer .act.primary", "Answer with none"), true);
      await settle();
      assert.equal(container.querySelector("form.answer p.error") === null, true);
    },
  );
});

test("a rejected answer shows the fixed line, keeps the selection, and shows no server text", () =>
  withCard(
    {
      question: recommendedQuestion,
      onAnswer: () => Promise.reject(new Error("upstream said: secret-7")),
    },
    async (container) => {
      assert.equal(await press(container, "form.answer .act.primary", "Answer with partial"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), answerFailedLine);
      assert.equal(selectedIndex(container), 1);
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is never shown");
    },
  ));

test("a different open question starts with its own selection, so a pick never answers another question", () =>
  withCard({ question: recommendedQuestion }, async (container) => {
    await clickCard(container, 0);
    await act(() => {
      render(
        <QuestionCard
          key={unrecommendedQuestion.id}
          question={unrecommendedQuestion}
          numberKeys
          onAnswer={answerNothing}
        />,
        container,
      );
    });
    assert.equal(textOf(container, "form.answer p.question"), "Which lane should it use?");
    assert.equal(selectedIndex(container), -1);
    assert.equal(confirmDisabled(container), true);
  }));
