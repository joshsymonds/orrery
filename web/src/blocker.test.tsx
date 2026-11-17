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

// threeChoices are the choices of the card tests: each names its id and what it does, which the card sends as its text.
const threeChoices = [
  { id: "composite", does: "Add a composite index on keeper and shift." },
  { id: "partial", does: "Add a partial index on open shifts only." },
  { id: "none", does: "Leave the table as it is." },
];
// recommendedBlocker asks a question that recommends partial, the second choice, for its why.
const recommendedBlocker: Blocker = {
  kind: "question",
  text: "waits on your answer",
  since: 0,
  question: create(EpicQuestionSchema, {
    id: "Q1",
    text: "Which index should it add?",
    choices: threeChoices,
    recommendation: { choice: "partial", why: "open shifts are the only rows the backfill scans", confidence: "high" },
  }),
};
// unrecommendedBlocker asks a question that recommends none.
const unrecommendedBlocker: Blocker = {
  kind: "question",
  text: "waits on your answer",
  since: 0,
  question: create(EpicQuestionSchema, { id: "Q2", text: "Which lane should it use?", choices: threeChoices }),
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
    numberKeys: true,
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
  return root.querySelector<HTMLButtonElement>(".now-block .act.primary");
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

test("an open question draws its text and a card per choice, then Something else, each with its key and what it does", () =>
  withBlock({ blocker: recommendedBlocker }, async (container) => {
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
  withBlock({ blocker: recommendedBlocker }, async (container) => {
    assert.equal(cards(container)[1].querySelector("em")?.textContent, "recommended");
    assert.equal(container.querySelectorAll("form.answer .opts em").length, 1, "only the recommended card is marked");
    assert.equal(textOf(container, "form.answer .opt small.why"), "open shifts are the only rows the backfill scans");
    assert.equal(selectedIndex(container), 1);
    assert.equal(confirmButton(container)?.textContent, "Answer with partial");
    assert.equal(confirmDisabled(container), false);
  }));

test("a question that recommends none starts with nothing selected and confirm disabled", () =>
  withBlock({ blocker: unrecommendedBlocker }, async (container) => {
    assert.equal(selectedIndex(container), -1);
    assert.equal(container.querySelectorAll("form.answer .opts em").length, 0);
    assert.equal(confirmButton(container)?.textContent, "Answer");
    assert.equal(confirmDisabled(container), true);
  }));

test("a click on a card selects it, and confirm sends its id and what it does", () => {
  const calls: [string, string, string][] = [];
  return withBlock(
    {
      blocker: recommendedBlocker,
      onAnswer: async (questionId, choice, text) => {
        calls.push([questionId, choice, text]);
        return "";
      },
    },
    async (container) => {
      await clickCard(container, 2);
      assert.equal(selectedIndex(container), 2);
      assert.equal(confirmButton(container)?.textContent, "Answer with none");
      assert.equal(await press(container, ".now-block .act.primary", "Answer with none"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "none", "Leave the table as it is."]]);
    },
  );
});

test("a number key selects the card it names, and confirm sends that choice", () => {
  const calls: [string, string, string][] = [];
  return withBlock(
    {
      blocker: recommendedBlocker,
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
      assert.equal(await press(container, ".now-block .act.primary", "Answer with composite"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "composite", "Add a composite index on keeper and shift."]]);
    },
  );
});

test("Something else reveals a text box, and confirm sends other with the trimmed text once it has some", () => {
  const calls: [string, string, string][] = [];
  return withBlock(
    {
      blocker: recommendedBlocker,
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
      assert.equal(await press(container, ".now-block .act.primary", "Answer with Something else"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "other", "Use main"]]);
      assert.equal(answerText(container), "", "a recorded answer clears the box");
    },
  );
});

test("a refused answer shows its reason under the card and keeps the selection, and a later success clears it", () => {
  let attempts = 0;
  return withBlock(
    {
      blocker: recommendedBlocker,
      onAnswer: async () => {
        attempts++;
        return attempts === 1 ? "question closed" : "";
      },
    },
    async (container) => {
      await clickCard(container, 2);
      assert.equal(await press(container, ".now-block .act.primary", "Answer with none"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), "question closed");
      assert.equal(selectedIndex(container), 2, "the refused selection is kept");
      assert.equal(await press(container, ".now-block .act.primary", "Answer with none"), true);
      await settle();
      assert.equal(container.querySelector("form.answer p.error") === null, true);
    },
  );
});

test("a rejected answer shows the fixed line, keeps the selection, and shows no server text", () =>
  withBlock(
    {
      blocker: recommendedBlocker,
      onAnswer: () => Promise.reject(new Error("upstream said: secret-7")),
    },
    async (container) => {
      assert.equal(await press(container, ".now-block .act.primary", "Answer with partial"), true);
      await settle();
      assert.equal(textOf(container, "form.answer p.error"), answerFailedLine);
      assert.equal(selectedIndex(container), 1);
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is never shown");
    },
  ));

test("a different open question starts with its own selection, so a pick never answers another question", () =>
  withTree(<NowBlock {...baseProps({ blocker: recommendedBlocker })} />, async (container) => {
    await clickCard(container, 0);
    await act(() => {
      render(<NowBlock {...baseProps({ blocker: unrecommendedBlocker })} />, container);
    });
    assert.equal(textOf(container, "form.answer p.question"), "Which lane should it use?");
    assert.equal(selectedIndex(container), -1);
    assert.equal(confirmDisabled(container), true);
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
  for (const blocker of [recommendedBlocker, heldBlocker]) {
    await withBlock({ blocker }, async (container) => {
      assert.equal(findButton(container, "button.act", "Retry") === undefined, true);
    });
  }
});

const blockers: [string, Blocker][] = [
  ["question", recommendedBlocker],
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
      blocker: recommendedBlocker,
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
