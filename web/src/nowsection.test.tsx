import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import {
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
import { clock } from "./workline";

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
    assert.equal(ask?.classList.contains("answer"), true, "the Ask is the sentence's next sibling");
    assert.equal(ask?.querySelector("p.question")?.textContent, "Which index should it add?");
    const submit = container.querySelector<HTMLButtonElement>("form.answer .act.primary");
    assert.equal(submit?.disabled, true, "nothing is answered before a choice is picked");
    await act(() => {
      container.querySelector<HTMLButtonElement>("form.answer .opts > .opt")?.click();
    });
    await act(() => {
      container.querySelector<HTMLButtonElement>("form.answer .act.primary")?.click();
    });
    await settle();
    assert.deepEqual(calls, [["Q1", "composite", "Add a composite index."]]);
  });
});

test("EpicAsk: an owner's park follows the sentence with its reason and exactly Retry and Stop…", () => {
  const props = baseProps(detailOf({ parked: "rate limit", steps: [step({ id: "s1" })] }));
  return withSection(props, async (container) => {
    const ask = askOf(container);
    assert.equal(ask?.className, "ask epic park");
    assert.equal(ask?.querySelector("p.ask-head")?.textContent, "It asks you what to do");
    assert.equal(ask?.querySelector("p.reason")?.textContent, "Rate limit.");
    assert.deepEqual(buttonTexts(container), ["Retry", "Stop…"]);
  });
});

test("EpicAsk: Retry calls onRetry once and nothing else", () => {
  const calls: string[] = [];
  const props = baseProps(detailOf({ parked: "rate limit", steps: [step({ id: "s1" })] }), {
    onRetry: () => calls.push("retry"),
    onStop: () => calls.push("stop"),
    onResume: () => calls.push("resume"),
  });
  return withSection(props, async (container) => {
    await press(container, "Retry");
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
    assert.deepEqual(buttonTexts(container), ["Retry", "Stop…"]);
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
    assert.equal(container.querySelector("section.now h3")?.textContent, "Now");
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
    assert.equal(first.querySelector("span.end")?.textContent, clock(now - 9_000));
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
