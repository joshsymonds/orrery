import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCommands } from "./epicpage";
import { NeedEntry } from "./needentry";
import type { Need } from "./needs";
import { installTestDom, removeTestDom } from "./testdom";

const questionNeed: Need = {
  kind: "question",
  text: "alpha asks: Which branch should it use?",
  summary: "",
  epic: "repo-a/alpha",
  question: "Q1",
};
const laterQuestionNeed: Need = {
  kind: "question",
  text: "alpha asks: Which lane should it use?",
  summary: "",
  epic: "repo-a/alpha",
  question: "Q2",
};
const parkNeed: Need = {
  kind: "park",
  text: "alpha is parked: rate limit",
  summary: "",
  epic: "repo-a/alpha",
  question: "",
};
const releaseNeed: Need = {
  kind: "release",
  text: "alpha: release step 2 (merge) failed: conflict",
  summary: "",
  epic: "repo-a/alpha",
  question: "",
};
const otherNeed: Need = {
  kind: "other",
  text: "alpha: api-1 held",
  summary: "",
  epic: "repo-a/alpha",
  question: "",
};

// recorder returns commands that log each call as a string, so a test asserts the calls the need made.
function recorder(): { calls: string[]; commands: EpicCommands } {
  const calls: string[] = [];
  return {
    calls,
    commands: {
      pause: () => {
        calls.push("pause");
      },
      resume: () => {
        calls.push("resume");
      },
      stop: () => {
        calls.push("stop");
      },
      retry: () => {
        calls.push("retry");
      },
      answer: async (questionId, text) => {
        calls.push(`answer ${questionId} ${text}`);
        return "";
      },
    },
  };
}

function entry(need: Need, commands: EpicCommands, onOpen: () => void = () => {}): JSX.Element {
  return <NeedEntry need={need} slug="alpha" commands={commands} onOpen={onOpen} />;
}

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

// openSentence clicks the need's sentence, and reports whether one was drawn.
async function openSentence(root: ParentNode): Promise<boolean> {
  const sentence = root.querySelector<HTMLElement>("p.need-text");
  if (sentence === null) {
    return false;
  }
  await act(() => {
    sentence.click();
  });
  return true;
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
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

test("a question need draws its sentence and an answer form without a question line", () => {
  const { commands } = recorder();
  return withTree(entry(questionNeed, commands), async (container) => {
    assert.equal(textOf(container, "p.need-text"), "alpha asks: Which branch should it use?");
    assert.equal(container.querySelector("form.answer") !== null, true);
    assert.equal(container.querySelector("form.answer p.question") === null, true, "the sentence is the question");
    assert.equal(answerBox(container) !== null, true);
    assert.deepEqual(textsOf(container, "button"), ["Answer"], "a question need draws no Retry or Stop");
  });
});

test("submitting a question need's answer calls answer with its question id and the trimmed text", () => {
  const { calls, commands } = recorder();
  return withTree(entry(questionNeed, commands), async (container) => {
    await type(container, "  Use main  ");
    assert.equal(await press(container, "button.act.primary", "Answer"), true);
    await settle();
    assert.deepEqual(calls, ["answer Q1 Use main"]);
    assert.equal(answerText(container), "", "a recorded answer clears the box");
  });
});

test("a draft typed for one question is not sent for the next question", () => {
  const { calls, commands } = recorder();
  return withTree(entry(questionNeed, commands), async (container) => {
    await type(container, "Use main");
    await act(() => {
      render(entry(laterQuestionNeed, commands), container);
    });
    assert.equal(textOf(container, "p.need-text"), "alpha asks: Which lane should it use?");
    assert.equal(answerText(container), "", "the next question starts with an empty box");
    assert.deepEqual(calls, [], "switching questions sends nothing");
  });
});

test("a park need draws its sentence and a Retry that calls retry, with no answer form", () => {
  const { calls, commands } = recorder();
  return withTree(entry(parkNeed, commands), async (container) => {
    assert.equal(textOf(container, "p.need-text"), "alpha is parked: rate limit");
    assert.equal(container.querySelector("form") === null, true);
    assert.deepEqual(textsOf(container, "button"), ["Retry"], "a park need draws Retry only");
    assert.equal(await press(container, "button.act", "Retry"), true);
    assert.deepEqual(calls, ["retry"]);
  });
});

test("a release need draws Retry and Stop…, and Retry calls retry", () => {
  const { calls, commands } = recorder();
  return withTree(entry(releaseNeed, commands), async (container) => {
    assert.equal(textOf(container, "p.need-text"), "alpha: release step 2 (merge) failed: conflict");
    assert.deepEqual(textsOf(container, "button"), ["Retry", "Stop…"]);
    assert.equal(await press(container, "button.act", "Retry"), true);
    assert.deepEqual(calls, ["retry"]);
  });
});

test("a release need's Stop… asks first and calls stop once when confirmed", () => {
  const { calls, commands } = recorder();
  return withTree(entry(releaseNeed, commands), async (container) => {
    assert.equal(await press(container, "button.act", "Stop…"), true);
    assert.deepEqual(calls, [], "the first click calls nothing");
    assert.equal(textOf(container, ".sure"), "Stop alpha?");
    assert.equal(await press(container, "button.act.primary", "Stop alpha"), true);
    assert.deepEqual(calls, ["stop"]);
    assert.equal(container.querySelector(".sure") === null, true);
  });
});

test("a release need's Keep it running returns to Stop… and calls nothing", () => {
  const { calls, commands } = recorder();
  return withTree(entry(releaseNeed, commands), async (container) => {
    assert.equal(await press(container, "button.act", "Stop…"), true);
    assert.equal(await press(container, "button.act", "Keep it running"), true);
    assert.deepEqual(calls, []);
    assert.equal(container.querySelector(".sure") === null, true);
    assert.equal(findButton(container, "button.act", "Stop…") !== undefined, true);
  });
});

test("an unknown need draws its own text and no button", () => {
  const { calls, commands } = recorder();
  return withTree(entry(otherNeed, commands), async (container) => {
    assert.equal(textOf(container, "p.need-text"), "alpha: api-1 held");
    assert.equal(container.querySelectorAll("button").length, 0);
    assert.equal(container.querySelector("form") === null, true);
    assert.deepEqual(calls, []);
  });
});

for (const need of [questionNeed, parkNeed, releaseNeed, otherNeed]) {
  test(`clicking the sentence of a ${need.kind} need calls onOpen once and no command`, () => {
    const { calls, commands } = recorder();
    let opens = 0;
    return withTree(
      entry(need, commands, () => {
        opens++;
      }),
      async (container) => {
        assert.equal(await openSentence(container), true);
        assert.equal(opens, 1);
        assert.deepEqual(calls, []);
      },
    );
  });
}
