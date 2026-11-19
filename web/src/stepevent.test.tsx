import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import type { Conversation } from "./conversation";
import { noEpics } from "./epics";
import {
  type ConversationItem,
  ConversationItemSchema,
  ConversationStateSchema,
  type PendingEvent,
  PendingEventSchema,
} from "./gen/orrery/v1/orrery_pb";
import { Reader } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// The reader draws the new-form event blocks: a step's end as its card, an epic's event as its ring, and a step's end
// the session holds as a waiting card whose return fold reads through stepResult. Each test's name carries the token
// StepEventBrief web. The epic and step ids are invented.

const ringEpic = "orrery-1a2b3c4/planner-runner";
const seenAt = 10_000;

// eventBlock is an epic's event block as the daemon writes it: the opening tag with kind and id, the body, and the
// closing tag.
function eventBlock(kind: string, tail: string, body: string): string {
  return `<orrery-event kind="${kind}" id="epic:${ringEpic}:${tail}">\n${body}\n</orrery-event>`;
}

// deliveredStep is a delivered step's end block: its agent, end and status on the opening tag, then its brief, then the
// More line that points to the full return.
const deliveredStep = [
  '<orrery-event kind="step_end" id="step:s1" agent="builder" end="done" status="done">',
  "Added the index.",
  "Ran the suite.",
  "More: step_status gives the full result.",
  "</orrery-event>",
].join("\n");

// eventItem is a prompt a caller event is delivered as: sent by an event, with its text.
function eventItem(text: string): ConversationItem {
  return create(ConversationItemSchema, { kind: "prompt", time: 100n, sender: "event", text });
}

function conversation(items: ConversationItem[], pending: PendingEvent[] = []): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items,
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, { pending }),
    unfinished: [],
    queued: [],
    unsent: [],
  };
}

// readerCalls are the epic calls the reader's rows read through. The epic reads never settle, so no question form is
// drawn, and stepResult answers with stepResult's return.
function readerCalls(stepResult: (id: string) => Promise<string>): EpicCalls {
  const unsettled = (): Promise<never> => new Promise(() => {});
  return {
    get: unsettled,
    pause: unsettled,
    resume: unsettled,
    answer: unsettled,
    openEpic: () => {},
    openStep: () => {},
    stepResult,
    epics: noEpics,
  };
}

// withReader draws a reader over conversation with epicCalls, runs check on the container, then removes the DOM.
async function withReader(
  conv: Conversation,
  epicCalls: EpicCalls | undefined,
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<Reader conversation={conv} seenAt={seenAt} open={false} epicCalls={epicCalls} />, container);
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

function textOf(root: ParentNode, selector: string): string | null | undefined {
  return root.querySelector(selector)?.textContent;
}

// summaryOf is the summary of the step card's return fold, the control that opens it.
function summaryOf(container: HTMLElement): HTMLElement {
  const summary = container.querySelector<HTMLElement>("details > summary");
  if (summary === null) {
    throw new Error("the step card draws its return fold");
  }
  return summary;
}

test("StepEventBrief web: a delivered step card draws its head from the tag's agent, end and status, and its lines are the brief's lines without the More line", () =>
  withReader(conversation([eventItem(deliveredStep)]), undefined, async (container) => {
    assert.equal(container.querySelectorAll(".step-card").length, 1, "one step card is drawn");
    assert.equal(textOf(container, ".step-agent"), "builder");
    assert.equal(textOf(container, ".step-end"), "end done");
    assert.equal(textOf(container, ".step-status"), "status done");
    const lines = [...container.querySelectorAll(".step-summary")].map((line) => line.textContent);
    assert.deepEqual(lines, ["Added the index.", "Ran the suite."], "the brief draws, and the More line does not");
  }));

test("StepEventBrief web: a waiting card draws the pending brief without its More line", () =>
  withReader(
    conversation(
      [],
      [
        create(PendingEventSchema, {
          id: "step:s9",
          kind: "step_end",
          step: "s9",
          agent: "builder",
          end: "done",
          status: "done",
          brief: "Return: shipped\nNot shown: none\nMore: step_result id s9",
          waits: "speak",
        }),
      ],
    ),
    undefined,
    async (container) => {
      assert.equal(container.querySelectorAll(".step-card.waiting").length, 1, "one waiting step card is drawn");
      const lines = [...container.querySelectorAll(".step-summary")].map((line) => line.textContent);
      assert.deepEqual(lines, ["Return: shipped", "Not shown: none"], "the brief draws, and the More line does not");
    },
  ));

// questionBody is the body of an open question's block: its epic line, then the question's own line.
const questionBody = `Epic ${ringEpic} (Plan the runner) asks a question.\nQ1 which-store: which store?`;

test("StepEventBrief web: an epic ring drawn from a new-form question block shows its first sentence, its icon and its slug", () =>
  withReader(
    conversation([eventItem(eventBlock("question", "Q1", questionBody))]),
    readerCalls(() => new Promise(() => {})),
    async (container) => {
      assert.equal(container.querySelectorAll(".ring-line").length, 1, "one ringed line is drawn");
      assert.equal(container.querySelector(".ring-icon")?.classList.contains("question"), true, "the icon draws");
      assert.equal(textOf(container, ".ring-sentence"), `Epic ${ringEpic} (Plan the runner) asks a question.`);
      assert.equal(textOf(container, ".ring-slug"), "planner-runner");
    },
  ));

test("StepEventBrief web: an epic ring drawn from a new-form release block draws its escaped body's &amp; &lt; &quot; unescaped, and &amp;lt; once", () =>
  withReader(
    conversation([
      eventItem(
        eventBlock(
          "release",
          "released",
          `Epic ${ringEpic} (Plan &amp; test &lt;fast&gt; &quot;one&quot; &amp;lt;raw&amp;gt;) is released.\nepic_get gives its report.`,
        ),
      ),
    ]),
    readerCalls(() => new Promise(() => {})),
    async (container) => {
      assert.equal(
        textOf(container, ".ring-sentence"),
        `Epic ${ringEpic} (Plan & test <fast> "one" &lt;raw&gt;) is released.`,
        "the sentence draws each escape as its character, and &amp;lt; as &lt;",
      );
      assert.equal(container.querySelector(".ring-icon")?.classList.contains("release"), true, "the icon draws");
    },
  ));

test("StepEventBrief web: a waiting step card's return fold calls stepResult with the step's id once, and draws the full return", async (t) => {
  const full = "Shipped the index.\n\nRan the suite:\n- 12 passed\n- 0 failed\n";
  const asked: string[] = [];
  const pending = [
    create(PendingEventSchema, {
      id: "step:s3",
      kind: "step_end",
      step: "s3",
      agent: "builder",
      end: "done",
      status: "done",
      brief: "Shipped the index.\nMore: step_result id s3",
      epic: ringEpic,
      waits: "speak",
    }),
  ];
  const calls = readerCalls(async (id) => {
    asked.push(id);
    return full;
  });
  await withReader(conversation([], pending), calls, async (container) => {
    await settle(t, () => container.querySelector(".step-card.waiting") !== null, "the waiting card is drawn");
    const summary = summaryOf(container);
    await click(summary);
    await settle(t, () => container.querySelector(".step-return pre") !== null, "the full return is drawn");
    assert.deepEqual(asked, ["s3"], "opening the fold asks for the step's return by its id");
    assert.equal(textOf(container, ".step-return pre"), full, "the fold draws the whole return");

    await click(summary);
    await click(summary);
    assert.deepEqual(asked, ["s3"], "reopening the fold asks for the return no second time");
  });
});

test("StepEventBrief web: a failed return read draws 'return could not be loaded' and no server text", async (t) => {
  const calls = readerCalls(async () => {
    throw new Error("server text: secret detail");
  });
  await withReader(conversation([eventItem(deliveredStep)]), calls, async (container) => {
    await click(summaryOf(container));
    await settle(t, () => container.querySelector(".error") !== null, "the failed read draws its line");
    assert.equal(textOf(container, ".error"), "return could not be loaded");
    assert.equal(container.textContent?.includes("secret detail"), false, "no server text is drawn");
  });
});

test("StepEventBrief web: an old-form step_end prompt draws as a plain prompt, not as a step card", () =>
  withReader(
    conversation([
      eventItem(
        [
          '<orrery-event kind="step_end" id="step:s1">',
          "Step s1 ended: end done, status done, reason tests pass.",
          "Agent: builder",
          "Summary: Added the index.",
          "step_status gives the full result.",
          "</orrery-event>",
        ].join("\n"),
      ),
    ]),
    readerCalls(() => new Promise(() => {})),
    async (container) => {
      assert.equal(container.querySelectorAll(".step-card").length, 0, "no step card is drawn");
      assert.equal(container.querySelectorAll(".ring-line").length, 0, "no ring is drawn");
      assert.equal(textOf(container, ".event > small"), "EVENT", "the prompt draws as an EVENT row");
    },
  ));
