import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { AskFirst, NowBlock, nowSentence } from "./blocker";
import { EpicQuestionSchema, EpicSchema, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { PHASES, type Phase, type PhaseSlice, type Tone, type Wedge } from "./phases";
import type { Blocker } from "./status";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof NowBlock>[0];

// now is the instant the Ask is drawn at, in unix seconds.
const now = 10_000;

// liveEpic is the epic the blocker belongs to: its project is the last element of its path.
const liveEpic = create(EpicSchema, {
  id: "orrery/api",
  slug: "api",
  repository: "orrery",
  path: "/work/orrery",
  live: true,
});

// threeChoices are the choices of the question tests: each names its id and what it does, which the answer sends as its text.
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
    epic: liveEpic,
    now,
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

// options are the options of the Ask in order: each choice, then Something else when the question has it.
function options(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(".ask .opts > .opt")];
}

// pickedOption is the position, from 0, of the option that holds the pick, or -1 when none does.
function pickedOption(root: ParentNode): number {
  return options(root).findIndex((option) => option.classList.contains("picked"));
}

// clickOption clicks the option at index, as the owner's pointer does.
async function clickOption(root: ParentNode, index: number): Promise<void> {
  const option = options(root)[index];
  assert.ok(option, `option ${index + 1} is drawn`);
  await act(() => {
    option.click();
  });
}

function confirmButton(root: ParentNode): HTMLButtonElement | null {
  return root.querySelector<HTMLButtonElement>(".now-block .ask .btn.primary");
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

// noteOf is the Ask's error note, or null when none is drawn.
function noteOf(root: ParentNode): string | null {
  return root.querySelector(".ask p.inote.error")?.textContent ?? null;
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

test("with no blocker there is no Ask and no button", () =>
  withBlock({ blocker: null }, async (container) => {
    assert.equal(container.querySelector("section.now-block") !== null, true);
    assert.equal(container.querySelector(".ask") === null, true);
    assert.equal(container.querySelectorAll("button").length, 0);
  }));

test("AskAnswer: an epic question draws its Ask with each choice and Something else, the recommended choice picked and marked", () =>
  withBlock({ blocker: recommendedBlocker }, async (container) => {
    assert.equal(textOf(container, ".ask .q"), "Which index should it add?");
    assert.deepEqual(textsOf(container, ".ask .opts > .opt .nm"), ["composite", "partial", "none", "Something else"]);
    assert.deepEqual(textsOf(container, ".ask .opts > .opt kbd"), ["1", "2", "3", "4"], "the desk draws a key cap per option");
    assert.equal(container.querySelectorAll(".ask .opts .chip").length, 1, "only the recommended option is marked");
    assert.equal(textOf(container, ".ask .opts .chip"), "recommended");
    assert.equal(pickedOption(container), 1);
    assert.equal(confirmButton(container)?.textContent, "Answer with partial");
    assert.equal(confirmDisabled(container), false);
    assert.equal(answerBox(container), null, "Something else keeps its text box hidden until it is picked");
  }));

test("AskAnswer: a question that recommends none starts with nothing picked and confirm disabled", () =>
  withBlock({ blocker: unrecommendedBlocker }, async (container) => {
    assert.equal(pickedOption(container), -1);
    assert.equal(container.querySelectorAll(".ask .opts .chip").length, 0);
    assert.equal(confirmButton(container)?.textContent, "Answer");
    assert.equal(confirmDisabled(container), true);
  }));

test("AskAnswer: an epic answer waits for confirm, then sends its choice's id and what it does", () => {
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
      await clickOption(container, 2);
      assert.equal(pickedOption(container), 2);
      assert.deepEqual(calls, [], "a click picks and does not answer");
      assert.equal(confirmButton(container)?.textContent, "Answer with none");
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with none"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "none", "Leave the table as it is."]]);
    },
  );
});

test("AskAnswer: digits 1 to 9 pick on the epic page and never answer", () => {
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
      assert.equal(pickedOption(container), 0, "digit 1 picks the first choice");
      await pressKey("4");
      assert.equal(pickedOption(container), 3, "digit 4 picks Something else");
      await pressKey("5");
      assert.equal(pickedOption(container), 3, "a digit past the last option moves no pick");
      assert.deepEqual(calls, [], "a digit answered the question");
    },
  );
});

test("AskAnswer: Something else sends other with the typed text, trimmed, once it has some", () => {
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
      await clickOption(container, 3);
      assert.equal(answerBox(container) !== null, true, "picking Something else draws its box");
      assert.equal(confirmDisabled(container), true, "confirm is disabled while the text is blank");
      await type(container, "   ");
      assert.equal(confirmDisabled(container), true, "a blank text keeps confirm disabled");
      await type(container, "  Use main  ");
      assert.equal(confirmDisabled(container), false);
      assert.equal(confirmButton(container)?.textContent, "Answer with Something else");
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with Something else"), true);
      await settle();
      assert.deepEqual(calls, [["Q1", "other", "Use main"]]);
      assert.equal(answerText(container), "", "a recorded answer clears the box");
    },
  );
});

test("AskAnswer: a refused answer shows its reason under the question and keeps the pick, and a later success clears it", () => {
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
      await clickOption(container, 2);
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with none"), true);
      await settle();
      assert.equal(noteOf(container), "question closed");
      assert.equal(pickedOption(container), 2, "the refused pick is kept");
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with none"), true);
      await settle();
      assert.equal(noteOf(container), null);
    },
  );
});

test("AskAnswer: a rejected answer shows the fixed line and no server text, and keeps the pick", () =>
  withBlock(
    {
      blocker: recommendedBlocker,
      onAnswer: () => Promise.reject(new Error("upstream said: secret-7")),
    },
    async (container) => {
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with partial"), true);
      await settle();
      assert.equal(noteOf(container), answerFailedLine);
      assert.equal(pickedOption(container), 1);
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is never shown");
    },
  ));

test("AskAnswer: a different open question starts with no pick of the one before", () =>
  withTree(<NowBlock {...baseProps({ blocker: recommendedBlocker })} />, async (container) => {
    await clickOption(container, 0);
    await act(() => {
      render(<NowBlock {...baseProps({ blocker: unrecommendedBlocker })} />, container);
    });
    assert.equal(textOf(container, ".ask .q"), "Which lane should it use?");
    assert.equal(pickedOption(container), -1);
    assert.equal(confirmDisabled(container), true);
  }));

test("AskAnswer: an epic question's Ask draws no Not now, Bring it back or More", () =>
  withBlock({ blocker: recommendedBlocker }, async (container) => {
    assert.notEqual(container.querySelector(".ask"), null, "the question's Ask is drawn");
    assert.equal(container.querySelector(".ask .head .end button"), null, "the Ask's head draws a control");
  }));

test("AskAnswer: a park's Ask asks its parked reason with Retry, and its confirm calls onRetry once with no Something else or record", () => {
  let retries = 0;
  return withBlock(
    {
      blocker: parkedBlocker,
      onRetry: () => {
        retries++;
      },
    },
    async (container) => {
      assert.equal(textOf(container, ".ask .q"), "parked · rate limit");
      assert.deepEqual(textsOf(container, ".ask .opts > .opt .nm"), ["Retry"], "a park draws its one answer");
      assert.equal(answerBox(container), null, "a park draws a text box");
      assert.equal(container.querySelector(".record") === null, true, "a park draws a record line");
      assert.equal(retries, 0, "the pick calls nothing");
      await clickOption(container, 0);
      assert.equal(await press(container, ".now-block .ask .btn.primary", "Answer with Retry"), true);
      await settle();
      assert.equal(retries, 1);
    },
  );
});

test("Retry is not drawn for an open question or a held step", async () => {
  for (const blocker of [recommendedBlocker, heldBlocker]) {
    await withBlock({ blocker }, async (container) => {
      assert.equal(textsOf(container, ".ask .opts > .opt .nm").includes("Retry"), false, "no Retry option is drawn");
      assert.equal(container.textContent?.includes("Retry") ?? false, false, "no Retry is drawn anywhere");
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
