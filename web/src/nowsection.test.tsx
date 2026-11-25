import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import {
  type EpicQuestion,
  EpicQuestionSchema,
  EpicSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  type EpicsGetResponse,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import { NowSection } from "./nowsection";
import { clock as resetAt } from "./reader";
import { nowSentenceOf } from "./recordwords";
import { installTestDom, removeTestDom } from "./testdom";
import { elapsed } from "./dockwords";

type Props = Parameters<typeof NowSection>[0];
type DetailInit = MessageInitShape<typeof EpicsGetResponseSchema>;
type StepInit = MessageInitShape<typeof EpicStepSchema>;

// The epic's identity, and the clock the section draws against. Every test passes now, so none reads the wall clock.
const alpha = { id: "repo-a/alpha", repository: "repo-a", slug: "alpha", source: "orreryd", outcome: "built" };
const now = 10_000;
const resetsAt = 1_700_018_000n;

// detailOf is a response as serve sends it, for the epic alpha, with what the init gives.
function detailOf(init: DetailInit): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: create(EpicSchema, alpha), ...init });
}

// step is a running step of task api-1, started at 8900 and in its stage since 9000, unless the init says otherwise.
function step(init: StepInit): EpicStep {
  return create(EpicStepSchema, {
    task: "api-1",
    agent: "builder",
    end: "",
    stageStarted: 9_000n,
    started: 8_900n,
    ...init,
  });
}

function baseProps(detail: EpicsGetResponse, init: Partial<Props> = {}): Props {
  return {
    detail,
    now,
    narrow: false,
    numberKeys: false,
    onAnswer: () => Promise.resolve(""),
    onRetry: () => {},
    onStop: () => {},
    onResume: () => {},
    ...init,
  };
}

// withSection renders the section into a fresh container, runs check on it, and removes it.
async function withSection(props: Props, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<NowSection {...props} />, container);
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

// sentenceOf is the section's sentence, which the Ask or the line or the button must follow at once.
function sentenceOf(root: ParentNode): HTMLElement {
  const sentence = root.querySelector<HTMLElement>("p.now-sentence");
  assert.ok(sentence, "the sentence is drawn");
  return sentence;
}

// askOf is the element placed directly after the sentence.
function askOf(root: ParentNode): Element | null {
  return sentenceOf(root).nextElementSibling;
}

function buttonTexts(root: ParentNode): (string | null)[] {
  return [...root.querySelectorAll("button")].map((button) => button.textContent);
}

function findButton(root: ParentNode, text: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === text);
}

// press clicks the button that reads text, which must be drawn.
async function press(root: ParentNode, text: string): Promise<void> {
  const button = findButton(root, text);
  assert.ok(button, `a ${text} button is drawn`);
  await act(() => {
    button.click();
  });
}

// settle lets a pending answer resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

// commandTexts are the texts of the section's buttons outside its Ask, in order.
function commandTexts(root: ParentNode): (string | null)[] {
  return [...root.querySelectorAll("button")].filter((button) => button.closest(".ask") === null).map((button) => button.textContent);
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

// pressKey sends a key to the document, as the keyboard does when no field has focus.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
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
  return root.querySelector<HTMLButtonElement>("section.now .ask .btn.primary");
}

function confirmDisabled(root: ParentNode): boolean {
  return confirmButton(root)?.disabled === true;
}

// confirm presses the Ask's confirm, which must be drawn and read text.
async function confirm(root: ParentNode, text: string): Promise<void> {
  const button = confirmButton(root);
  assert.equal(button?.textContent, text);
  await act(() => {
    button?.click();
  });
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

// noteOf is the Ask's error note, or null when none is drawn.
function noteOf(root: ParentNode): string | null {
  return root.querySelector(".ask p.inote.error")?.textContent ?? null;
}

// threeChoices are the choices of the Ask tests: each names its id and what it does, which the answer sends as its text.
const threeChoices = [
  { id: "composite", does: "Add a composite index on keeper and shift." },
  { id: "partial", does: "Add a partial index on open shifts only." },
  { id: "none", does: "Leave the table as it is." },
];
// recommended asks a question that recommends partial, the second choice, for its why.
const recommended = create(EpicQuestionSchema, {
  id: "Q1",
  text: "Which index should it add?",
  choices: threeChoices,
  recommendation: { choice: "partial", why: "open shifts are the only rows the backfill scans", confidence: "high" },
});
// unrecommended asks a question that recommends none.
const unrecommended = create(EpicQuestionSchema, { id: "Q2", text: "Which lane should it use?", choices: threeChoices });

const answerFailedLine = "Not answered. Your answer is kept.";

// asking is a detail whose open question is question, with one running step.
function asking(question: EpicQuestion): EpicsGetResponse {
  return detailOf({ questions: [question], steps: [step({ id: "s1" })] });
}

// answers records each answer the section sends, and answers each with the line result gives, "" by default.
function answers(result: () => Promise<string> = () => Promise.resolve("")): {
  calls: [string, string, string][];
  onAnswer: Props["onAnswer"];
} {
  const calls: [string, string, string][] = [];
  return {
    calls,
    onAnswer: (questionId, choice, text) => {
      calls.push([questionId, choice, text]);
      return result();
    },
  };
}

test("EpicAsk: an open question's Ask follows the sentence, and it sends the choice picked", () => {
  const question = create(EpicQuestionSchema, {
    id: "Q1",
    text: "Which index should it add?",
    choices: [{ id: "composite", does: "Add a composite index." }],
  });
  const calls: string[][] = [];
  const props = baseProps(detailOf({ questions: [question], steps: [step({ id: "s1" })] }), {
    onAnswer: (questionId, choice, text) => {
      calls.push([questionId, choice, text]);
      return Promise.resolve("");
    },
  });
  return withSection(props, async (container) => {
    const ask = askOf(container);
    assert.equal(ask?.classList.contains("ask"), true, "the Ask is the sentence's next sibling");
    assert.equal(ask?.querySelector(".q")?.textContent, "Which index should it add?");
    assert.equal(confirmDisabled(container), true, "nothing is answered before a choice is picked");
    await clickOption(container, 0);
    await confirm(container, "Answer with composite");
    await settle();
    assert.deepEqual(calls, [["Q1", "composite", "Add a composite index."]]);
  });
});

test("EpicAsk: an owner's park follows the sentence as its Ask, with its reason, exactly Retry, and Stop…", () => {
  const props = baseProps(detailOf({ parked: "rate limit", steps: [step({ id: "s1" })] }));
  return withSection(props, async (container) => {
    const ask = askOf(container);
    assert.equal(ask?.classList.contains("ask"), true, "the park's Ask is the sentence's next sibling");
    assert.equal(ask?.querySelector(".head .lbl")?.textContent, "Asks you");
    assert.equal(ask?.querySelector(".q")?.textContent, "Rate limit.");
    assert.deepEqual(textsOf(container, ".ask .opts > .opt .nm"), ["Retry"], "the park's one answer is Retry");
    assert.deepEqual(commandTexts(container), ["Stop…"], "Stop… is the section's one command outside the Ask");
  });
});

test("EpicAsk: Retry, picked then confirmed, calls onRetry once and nothing else", () => {
  const calls: string[] = [];
  const props = baseProps(detailOf({ parked: "rate limit", steps: [step({ id: "s1" })] }), {
    onRetry: () => calls.push("retry"),
    onStop: () => calls.push("stop"),
    onResume: () => calls.push("resume"),
  });
  return withSection(props, async (container) => {
    await clickOption(container, 0);
    assert.deepEqual(calls, [], "the pick calls nothing");
    await confirm(container, "Answer with Retry");
    await settle();
    assert.deepEqual(calls, ["retry"]);
  });
});

test("EpicAsk: Stop… asks first, and calls onStop only after its confirm", () => {
  const calls: string[] = [];
  const props = baseProps(detailOf({ parked: "rate limit", steps: [step({ id: "s1" })] }), {
    onStop: () => calls.push("stop"),
  });
  return withSection(props, async (container) => {
    await press(container, "Stop…");
    assert.deepEqual(calls, [], "the first press asks and stops nothing");
    assert.equal(container.querySelector("span.sure")?.textContent, "Stop alpha?");
    await press(container, "Keep it running");
    assert.deepEqual(calls, [], "keep stops nothing");
    assert.deepEqual(commandTexts(container), ["Stop…"]);
    await press(container, "Stop…");
    await press(container, "Stop alpha");
    assert.deepEqual(calls, ["stop"]);
  });
});

test("EpicAsk: a planner's park is one quiet line after the sentence, with no button in the section", () => {
  const props = baseProps(
    detailOf({ parked: "usage check", plannerParked: true, steps: [step({ id: "s1" })] }),
  );
  return withSection(props, async (container) => {
    const line = askOf(container);
    assert.equal(line?.className, "inote quiet");
    assert.equal(
      line?.textContent,
      "The planner holds this park: usage check. It will retry, replan, or ask you.",
    );
    assert.deepEqual(buttonTexts(container), []);
  });
});

test("SteererHolds: a steerer's park is one quiet line after the sentence, with no Ask and no button in the section", () => {
  const props = baseProps(
    detailOf({
      parked: "workflow node gate failed",
      steererParked: true,
      questions: [{ id: "Q1", text: "Retry the gate?", steererHeld: true, choices: threeChoices }],
      steps: [step({ id: "s1" })],
    }),
  );
  return withSection(props, async (container) => {
    assert.equal(
      sentenceOf(container).textContent,
      "Parked. Its steerer holds the question; nothing is asked of you unless it escalates it.",
    );
    const line = askOf(container);
    assert.equal(line?.className, "inote quiet");
    assert.equal(
      line?.textContent,
      "Its steerer holds this park, and answers it or escalates it to you: workflow node gate failed",
    );
    assert.equal(container.querySelector(".ask"), null, "a steerer's park draws an Ask");
    assert.deepEqual(buttonTexts(container), []);
  });
});

test("SteererHolds: a question its steerer holds is one quiet line after the sentence, with no Ask and no button", () => {
  const props = baseProps(
    detailOf({
      questions: [{ id: "Q1", text: "Which index should it add?", steererHeld: true, choices: threeChoices }],
    }),
  );
  return withSection(props, async (container) => {
    assert.equal(sentenceOf(container).textContent, "It waits on its steerer's answer.");
    const line = askOf(container);
    assert.equal(line?.className, "inote quiet");
    assert.equal(
      line?.textContent,
      "Its steerer holds this question, and answers it or escalates it to you: Which index should it add?",
    );
    assert.equal(container.querySelector(".ask"), null, "a steerer-held question draws an Ask");
    assert.deepEqual(buttonTexts(container), []);
  });
});

test("SteererHolds: the owner's question draws its Ask ahead of a question its steerer holds", () => {
  const props = baseProps(
    detailOf({
      questions: [
        { id: "Q1", text: "Retry the gate?", steererHeld: true, choices: threeChoices },
        { id: "Q2", text: "Which index should it add?", choices: threeChoices },
      ],
    }),
  );
  return withSection(props, async (container) => {
    assert.equal(sentenceOf(container).textContent, "It waits on your answer.");
    assert.equal(askOf(container)?.querySelector(".q")?.textContent, "Which index should it add?");
    assert.equal(container.querySelector(".inote.quiet"), null, "the steerer's line is drawn beside the owner's Ask");
  });
});

test("EpicWords: each running step is a row with its glyph, label, pips, doing and clock, and finished steps draw none", () => {
  const props = baseProps(
    detailOf({
      steps: [
        step({ id: "s1", task: "api-1", doing: "editing the parser" }),
        step({ id: "s2", task: "", agent: "reviewer", doing: "reading the diff" }),
        step({ id: "s3", task: "api-3", end: "done" }),
      ],
    }),
  );
  return withSection(props, async (container) => {
    assert.equal(container.querySelector("section.now")?.getAttribute("aria-label"), "Now");
    assert.equal(container.querySelector("section.now .sec .name")?.textContent, "Now");
    assert.equal(sentenceOf(container).textContent, nowSentenceOf(props.detail));
    const rows = container.querySelectorAll("div.tbl.steps > div.tr");
    assert.equal(rows.length, 2, "the finished step draws no row");
    const [first, second] = rows;
    assert.equal(first.className, "tr t-working");
    assert.equal(first.querySelector("span.g")?.className, "g g-working");
    assert.equal(first.querySelector("span.g")?.getAttribute("aria-label"), "working");
    assert.equal(first.querySelector("span.strong")?.textContent, "api-1");
    assert.equal(first.querySelectorAll("span.pips > i").length, 5, "a task with no build task draws five pips");
    assert.equal(first.querySelector("span.doing")?.textContent, "editing the parser");
    assert.equal(first.querySelector("span.end")?.textContent, elapsed(now - 9_000));
    assert.equal(second.querySelector("span.strong")?.textContent, "reviewer", "a step with no task is named by its agent");
    assert.deepEqual(
      [...second.querySelectorAll("span.pips > i")].map((mark) => mark.getAttribute("data-tone")),
      ["none", "none", "none", "none", "none"],
    );
  });
});

test("EpicWords: a held step's row is held, apart from a limited one, and says why it is held", () => {
  const props = baseProps(detailOf({ steps: [step({ id: "s1", held: true, reason: "waiting for a lane" })] }));
  return withSection(props, async (container) => {
    const row = container.querySelector("div.tbl.steps > div.tr");
    assert.equal(row?.className, "tr t-held");
    assert.equal(row?.querySelector("span.g")?.className, "g g-held");
    assert.equal(row?.querySelector("span.doing")?.textContent, "held · waiting for a lane");
  });
});

test("EpicWords: limited rows are grey and carry the reset, and Resume now calls onResume once", () => {
  const calls: string[] = [];
  const props = baseProps(
    detailOf({
      steps: [
        step({ id: "s1", task: "api-1", limited: true, limitKind: "weekly", limitResets: resetsAt }),
        step({ id: "s2", task: "api-2", limited: true, limitKind: "weekly", limitResets: resetsAt }),
      ],
    }),
    { onResume: () => calls.push("resume"), onRetry: () => calls.push("retry") },
  );
  return withSection(props, async (container) => {
    assert.equal(
      sentenceOf(container).textContent,
      `2 steps wait on the weekly limit, which resets at ${resetAt(resetsAt)}. They resume on their own.`,
    );
    const rows = [...container.querySelectorAll("div.tbl.steps > div.tr")];
    assert.deepEqual(
      rows.map((row) => row.className),
      ["tr t-limited", "tr t-limited"],
    );
    for (const row of rows) {
      assert.equal(row.querySelector("span.g")?.className, "g g-limited", "a limited row's glyph is grey, not held");
      assert.equal(row.querySelector("span.doing")?.textContent, `waits on the weekly limit · resets ${resetAt(resetsAt)}`);
    }
    const resume = findButton(container, "Resume now");
    assert.equal(askOf(container), resume, "Resume now follows the sentence");
    assert.equal(resume?.title, "Try before the reset. A step still over the limit stays held.");
    await press(container, "Resume now");
    assert.deepEqual(calls, ["resume"]);
  });
});

test("with nothing asked there is no Ask and no button", () =>
  withSection(baseProps(detailOf({ steps: [step({ id: "s1" })] })), async (container) => {
    assert.equal(container.querySelector("section.now") !== null, true);
    assert.equal(container.querySelector(".ask") === null, true);
    assert.equal(container.querySelectorAll("button").length, 0);
  }));

test("AskAnswer: an epic question draws its Ask with each choice and Something else, the recommended choice picked and marked", () =>
  withSection(baseProps(asking(recommended)), async (container) => {
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

test("AskAnswer: under 760px the epic question's Ask draws no key caps", () =>
  withSection(baseProps(asking(recommended), { narrow: true }), async (container) => {
    assert.equal(options(container).length, 4, "every option is drawn");
    assert.equal(container.querySelectorAll(".ask kbd").length, 0);
  }));

test("AskAnswer: a question that recommends none starts with nothing picked and confirm disabled", () =>
  withSection(baseProps(asking(unrecommended)), async (container) => {
    assert.equal(pickedOption(container), -1);
    assert.equal(container.querySelectorAll(".ask .opts .chip").length, 0);
    assert.equal(confirmButton(container)?.textContent, "Answer");
    assert.equal(confirmDisabled(container), true);
  }));

test("AskAnswer: an epic answer waits for confirm, then sends its choice's id and what it does", () => {
  const sent = answers();
  return withSection(baseProps(asking(recommended), { onAnswer: sent.onAnswer }), async (container) => {
    await clickOption(container, 2);
    assert.equal(pickedOption(container), 2);
    assert.deepEqual(sent.calls, [], "a click picks and does not answer");
    await confirm(container, "Answer with none");
    await settle();
    assert.deepEqual(sent.calls, [["Q1", "none", "Leave the table as it is."]]);
  });
});

test("AskAnswer: digits 1 to 9 pick on the epic page and never answer", () => {
  const sent = answers();
  return withSection(baseProps(asking(recommended), { numberKeys: true, onAnswer: sent.onAnswer }), async (container) => {
    await pressKey("1");
    assert.equal(pickedOption(container), 0, "digit 1 picks the first choice");
    await pressKey("4");
    assert.equal(pickedOption(container), 3, "digit 4 picks Something else");
    await pressKey("5");
    assert.equal(pickedOption(container), 3, "a digit past the last option moves no pick");
    assert.deepEqual(sent.calls, [], "a digit answered the question");
  });
});

test("AskAnswer: Something else sends other with the typed text, trimmed, once it has some", () => {
  const sent = answers();
  return withSection(baseProps(asking(recommended), { onAnswer: sent.onAnswer }), async (container) => {
    await clickOption(container, 3);
    assert.equal(answerBox(container) !== null, true, "picking Something else draws its box");
    assert.equal(confirmDisabled(container), true, "confirm is disabled while the text is blank");
    await type(container, "   ");
    assert.equal(confirmDisabled(container), true, "a blank text keeps confirm disabled");
    await type(container, "  Use main  ");
    assert.equal(confirmDisabled(container), false);
    await confirm(container, "Answer with Something else");
    await settle();
    assert.deepEqual(sent.calls, [["Q1", "other", "Use main"]]);
    assert.equal(answerBox(container)?.value, "", "a recorded answer clears the box");
  });
});

test("AskAnswer: a refused answer shows its reason under the question and keeps the pick, and a later success clears it", () => {
  let attempts = 0;
  const sent = answers(() => {
    attempts++;
    return Promise.resolve(attempts === 1 ? "question closed" : "");
  });
  return withSection(baseProps(asking(recommended), { onAnswer: sent.onAnswer }), async (container) => {
    await clickOption(container, 2);
    await confirm(container, "Answer with none");
    await settle();
    assert.equal(noteOf(container), "question closed");
    assert.equal(pickedOption(container), 2, "the refused pick is kept");
    await confirm(container, "Answer with none");
    await settle();
    assert.equal(noteOf(container), null);
  });
});

test("AskAnswer: a rejected answer shows the fixed line and no server text, and keeps the pick", () =>
  withSection(
    baseProps(asking(recommended), { onAnswer: () => Promise.reject(new Error("upstream said: secret-7")) }),
    async (container) => {
      await confirm(container, "Answer with partial");
      await settle();
      assert.equal(noteOf(container), answerFailedLine);
      assert.equal(pickedOption(container), 1);
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is never shown");
    },
  ));

test("AskAnswer: a different open question starts with no pick of the one before", () =>
  withSection(baseProps(asking(recommended)), async (container) => {
    await clickOption(container, 0);
    await act(() => {
      render(<NowSection {...baseProps(asking(unrecommended))} />, container);
    });
    assert.equal(textOf(container, ".ask .q"), "Which lane should it use?");
    assert.equal(pickedOption(container), -1);
    assert.equal(confirmDisabled(container), true);
  }));

test("AskAnswer: an epic question's Ask draws no Not now, Bring it back or More", () =>
  withSection(baseProps(asking(recommended)), async (container) => {
    assert.notEqual(container.querySelector(".ask"), null, "the question's Ask is drawn");
    assert.equal(container.querySelector(".ask .head .end button"), null, "the Ask's head draws a control");
  }));

test("Retry is not drawn for an open question or a held step", async () => {
  const details = [asking(recommended), detailOf({ steps: [step({ id: "s1", held: true, reason: "waiting for a lane" })] })];
  for (const detail of details) {
    await withSection(baseProps(detail), async (container) => {
      assert.equal(textsOf(container, ".ask .opts > .opt .nm").includes("Retry"), false, "no Retry option is drawn");
      assert.equal(container.textContent?.includes("Retry") ?? false, false, "no Retry is drawn anywhere");
    });
  }
});
