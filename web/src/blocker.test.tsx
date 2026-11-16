import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { AskFirst, NowBlock, nowSentence } from "./blocker";
import { EpicQuestionSchema, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { PHASES, type Phase, type PhaseSlice, type Tone, type Wedge } from "./phases";
import type { Blocker } from "./status";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof NowBlock>[0];

const questionBlocker: Blocker = {
  kind: "question",
  text: "waits on your answer",
  since: 0,
  question: create(EpicQuestionSchema, { id: "Q1", text: "Which branch should it use?" }),
};
const laterQuestionBlocker: Blocker = {
  kind: "question",
  text: "waits on your answer",
  since: 0,
  question: create(EpicQuestionSchema, { id: "Q2", text: "Which lane should it use?" }),
};
const parkedBlocker: Blocker = { kind: "parked", text: "parked · rate limit", since: 0 };
const heldBlocker: Blocker = {
  kind: "held",
  text: "api-1 held",
  since: 0,
  step: create(EpicStepSchema, { task: "api-1", held: true }),
};

const answerFailedLine = "Not answered. Your answer is kept.";

function baseProps(init: Partial<Props>): Props {
  return {
    sentence: "",
    blocker: null,
    slug: "api",
    onAnswer: () => Promise.resolve(""),
    onRetry: () => {},
    onStop: () => {},
    ...init,
  };
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

async function withBlock(props: Partial<Props>, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  return withTree(<NowBlock {...baseProps(props)} />, check);
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

function answerDisabled(root: ParentNode): boolean {
  return findButton(root, "button.act.primary", "Answer")?.disabled === true;
}

function slicesAt(place: Phase, summary: string): PhaseSlice[] {
  const reached = PHASES.indexOf(place);
  return PHASES.map((phase, index): PhaseSlice => ({
    phase,
    state: index < reached ? "passed" : index === reached ? "now" : "ahead",
    summary: index === reached ? summary : "",
    since: 0,
    seconds: 0,
  }));
}

function wedge(task: string, place: Phase, tone: Tone, summary = ""): Wedge {
  return { task, title: task, requirements: [], place, tone, slices: slicesAt(place, summary) };
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

test("the now sentence draws as p.now when it has text, and not when it is empty", async () => {
  await withBlock({ sentence: "api-2 · checking · gate running" }, async (container) => {
    assert.equal(textOf(container, "section.now-block p.now"), "api-2 · checking · gate running");
  });
  await withBlock({ sentence: "" }, async (container) => {
    assert.equal(container.querySelector("p.now") === null, true);
  });
});

test("with no blocker there is no form and no button", () =>
  withBlock({ blocker: null }, async (container) => {
    assert.equal(container.querySelector("section.now-block") !== null, true);
    assert.equal(container.querySelector("form") === null, true);
    assert.equal(container.querySelectorAll("button").length, 0);
  }));

test("an open question draws its text in an answer form with a textarea and a disabled Answer button", () =>
  withBlock({ blocker: questionBlocker }, async (container) => {
    assert.equal(container.querySelector("form.answer") !== null, true);
    assert.equal(textOf(container, "form.answer p.question"), "Which branch should it use?");
    assert.equal(answerBox(container) !== null, true);
    assert.equal(findButton(container, "button.act.primary", "Answer") !== undefined, true);
    assert.equal(answerDisabled(container), true, "Answer is disabled while the answer is empty");
  }));

test("Answer is disabled while the trimmed answer is empty, and enabled once it has text", () =>
  withBlock({ blocker: questionBlocker }, async (container) => {
    await type(container, "   ");
    assert.equal(answerDisabled(container), true, "a blank answer leaves Answer disabled");
    await type(container, "  Use main  ");
    assert.equal(answerDisabled(container), false, "an answer with text enables Answer");
  }));

test("submitting sends the question id and the trimmed answer, and clears the box when it is recorded", () => {
  const calls: [string, string][] = [];
  return withBlock(
    {
      blocker: questionBlocker,
      onAnswer: async (questionId, text) => {
        calls.push([questionId, text]);
        return "";
      },
    },
    async (container) => {
      await type(container, "  Use main  ");
      assert.equal(await press(container, "button.act.primary", "Answer"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "Use main"]]);
      assert.equal(answerText(container), "");
      assert.equal(container.querySelector("form.answer p.error") === null, true);
    },
  );
});

test("a returned error shows as p.error under the form and keeps the answer, and a later success clears it", () => {
  let attempts = 0;
  return withBlock(
    {
      blocker: questionBlocker,
      onAnswer: async () => {
        attempts++;
        return attempts === 1 ? "question closed" : "";
      },
    },
    async (container) => {
      await type(container, "Use main");
      assert.equal(await press(container, "button.act.primary", "Answer"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), "question closed");
      assert.equal(answerText(container), "Use main");
      assert.equal(await press(container, "button.act.primary", "Answer"), true);
      await settle();
      assert.equal(container.querySelector("form.answer p.error") === null, true);
      assert.equal(answerText(container), "");
    },
  );
});

test("a rejected answer shows the fixed line, keeps the answer, and shows no server text", () =>
  withBlock(
    {
      blocker: questionBlocker,
      onAnswer: () => Promise.reject(new Error("upstream said: secret-7")),
    },
    async (container) => {
      await type(container, "Use main");
      assert.equal(await press(container, "button.act.primary", "Answer"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), answerFailedLine);
      assert.equal(answerText(container), "Use main");
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is never shown");
    },
  ));

test("a different open question starts with an empty answer box, so a draft never answers another question", () =>
  withTree(<NowBlock {...baseProps({ blocker: questionBlocker })} />, async (container) => {
    await type(container, "Use main");
    await act(() => {
      render(<NowBlock {...baseProps({ blocker: laterQuestionBlocker })} />, container);
    });
    assert.equal(textOf(container, "form.answer p.question"), "Which lane should it use?");
    assert.equal(answerText(container), "");
  }));

test("a park draws its text and a Retry that calls onRetry once, with no answer form", () => {
  let retries = 0;
  return withBlock(
    {
      blocker: parkedBlocker,
      onRetry: () => {
        retries++;
      },
    },
    async (container) => {
      assert.equal(textOf(container, "p.blocked"), "parked · rate limit");
      assert.equal(container.querySelector("form") === null, true);
      assert.equal(await press(container, "button.act", "Retry"), true);
      assert.equal(retries, 1);
    },
  );
});

test("Retry is drawn for a park only", async () => {
  for (const blocker of [questionBlocker, heldBlocker]) {
    await withBlock({ blocker }, async (container) => {
      assert.equal(findButton(container, "button.act", "Retry") === undefined, true);
    });
  }
});

const blockers: [string, Blocker][] = [
  ["question", questionBlocker],
  ["parked", parkedBlocker],
  ["held", heldBlocker],
];

for (const [kind, blocker] of blockers) {
  test(`a ${kind} blocker has a Stop that asks first and calls onStop once when confirmed`, () => {
    let stops = 0;
    return withBlock(
      {
        blocker,
        onStop: () => {
          stops++;
        },
      },
      async (container) => {
        assert.equal(await press(container, "button.act", "Stop…"), true);
        assert.equal(stops, 0, "the first click calls nothing");
        assert.equal(textOf(container, ".sure"), "Stop api?");
        assert.equal(await press(container, "button.act.primary", "Stop api"), true);
        assert.equal(stops, 1);
        assert.equal(container.querySelector(".sure") === null, true);
        assert.equal(findButton(container, "button.act", "Stop…") !== undefined, true);
      },
    );
  });
}

test("Keep it running returns to the Stop… button and calls nothing", () => {
  let stops = 0;
  return withBlock(
    {
      blocker: questionBlocker,
      onStop: () => {
        stops++;
      },
    },
    async (container) => {
      assert.equal(await press(container, "button.act", "Stop…"), true);
      assert.equal(await press(container, "button.act", "Keep it running"), true);
      assert.equal(stops, 0);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.equal(findButton(container, "button.act", "Stop…") !== undefined, true);
    },
  );
});

test("nowSentence reads the first running wedge's task, phase and now summary", () => {
  assert.equal(
    nowSentence([
      wedge("api-1", "implementing", "mine", "waiting on you"),
      wedge("api-2", "checking", "run", "gate running"),
      wedge("api-3", "reviewing", "run", "review"),
    ]),
    "api-2 · checking · gate running",
  );
});

test("nowSentence leaves out the summary when the now slice has none", () => {
  assert.equal(nowSentence([wedge("api-2", "reviewing", "run")]), "api-2 · reviewing");
});

test("nowSentence is empty when no wedge runs", () => {
  assert.equal(nowSentence([wedge("api-1", "implementing", "mine", "waiting on you")]), "");
  assert.equal(nowSentence([]), "");
});
