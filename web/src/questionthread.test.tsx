import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { at } from "./fixtures/clock";
import {
  DesignLogLineSchema,
  DesignQuestionSchema,
  RecordMakerSchema,
  type DesignLogLine,
  type DesignQuestion,
  type RecordMaker,
} from "./gen/orrery/v1/designs_pb";
import { noticeClock } from "./noticewords";
import { QuestionThread, type QuestionAnswer } from "./questionthread";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

type ThreadProps = Parameters<typeof QuestionThread>[0];

// question is the open question Q3 of an epic's contract, with three options, the first recommended.
function question(init: MessageInitShape<typeof DesignQuestionSchema> = {}): DesignQuestion {
  return create(DesignQuestionSchema, {
    id: 3n,
    contract: "rota-export",
    text: "Which index should the backfill add?",
    options: ["partial", "composite", "none"],
    recommendation: "partial",
    asked: at("@13:50"),
    ...init,
  });
}

// settled is question Q3 answered by a maker, and accepted when accepted is set, at 14:02.
function settled(maker: RecordMaker, init: MessageInitShape<typeof DesignQuestionSchema> = {}): DesignQuestion {
  return question({ answer: "Keep the pin", accepted: false, answered: at("@14:02"), maker, ...init });
}

// asked is the conversation's line that asked Q3, and said is the owner's message in its thread.
const asked: DesignLogLine = create(DesignLogLineSchema, {
  seq: 1n,
  time: at("@13:50"),
  maker: create(RecordMakerSchema, { kind: "conversation" }),
  what: "asked",
  ids: ["Q3"],
});

const said: DesignLogLine = create(DesignLogLineSchema, {
  seq: 2n,
  time: at("@13:52"),
  maker: create(RecordMakerSchema, { kind: "owner" }),
  what: "said",
  ids: ["Q3"],
  words: "Which of the two keeps the Lisbon case?",
});

const recorded: DesignLogLine = create(DesignLogLineSchema, {
  seq: 3n,
  time: at("@13:55"),
  maker: create(RecordMakerSchema, { kind: "orreryd" }),
  what: "recorded",
  ids: ["Q3"],
});

// baseProps is an open question Q3 in its design, with no thread and an onAnswer that resolves.
function baseProps(init: Partial<ThreadProps>): ThreadProps {
  return {
    question: question(),
    lines: [],
    place: "design",
    onAnswer: async () => {},
    ...init,
  };
}

// withThread renders a QuestionThread into a fresh container, runs check on it, and removes it.
async function withThread(init: Partial<ThreadProps>, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<QuestionThread {...baseProps(init)} />, container);
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

// recorder returns an onAnswer that records each answer it is given, and resolves.
function recorder(): { answers: QuestionAnswer[]; onAnswer: (answer: QuestionAnswer) => Promise<void> } {
  const answers: QuestionAnswer[] = [];
  return {
    answers,
    onAnswer: async (answer) => {
      answers.push(answer);
    },
  };
}

// block returns the question's section, which is labelled with its id.
function block(root: ParentNode): HTMLElement {
  const found = root.querySelector<HTMLElement>('[aria-label="question Q3"]');
  assert.ok(found, "the question's block is drawn");
  return found;
}

// optionRows are the option rows of the question, in order.
function optionRows(root: ParentNode): HTMLButtonElement[] {
  return [...root.querySelectorAll<HTMLButtonElement>(".opts > button.opt")];
}

// buttonNamed returns the button whose text is name, and fails when none is drawn for it.
function buttonNamed(root: ParentNode, name: string): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>("button")].find(
    (button) => (button.textContent ?? "").trim() === name,
  );
  assert.ok(found, `a button "${name}" is drawn`);
  return found;
}

// textareaOf returns the answer box, and fails when none is drawn.
function textareaOf(root: ParentNode): HTMLTextAreaElement {
  const found = root.querySelector<HTMLTextAreaElement>('textarea[aria-label="answer"]');
  assert.ok(found, "the answer box is drawn");
  return found;
}

// press clicks the button named name, then lets the call it makes settle and its state render.
async function press(root: ParentNode, name: string): Promise<void> {
  const button = buttonNamed(root, name);
  await act(async () => {
    button.click();
    await turn();
  });
}

// typeInto sets the answer box's text as a typist does: its value, then its input event.
async function typeInto(root: ParentNode, text: string): Promise<void> {
  const box = textareaOf(root);
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

test("QuestionThread: the open question draws its id, its text, and each option as its own row, with the recommended one marked", () =>
  withThread({}, async (container) => {
    const section = block(container);
    assert.equal(section.querySelector("code")?.textContent, "Q3");
    assert.equal(section.querySelector(".q")?.textContent, "Which index should the backfill add?");
    const rows = optionRows(section);
    assert.deepEqual(
      rows.map((row) => row.querySelector(".nm")?.textContent),
      ["partial", "composite", "none"],
      "the options are not each drawn as a row",
    );
    assert.deepEqual(
      rows.map((row) => row.querySelector(".chip")?.textContent ?? null),
      ["recommended", null, null],
      "the recommended option is not the one marked",
    );
  }));

test("QuestionThread: pressing an option's row puts that option in the box", () =>
  withThread({}, async (container) => {
    const rows = optionRows(block(container));
    assert.equal(textareaOf(container).value, "", "the box does not start empty");
    await act(() => {
      rows[1].click();
    });
    assert.equal(textareaOf(container).value, "composite", "pressing composite did not put it in the box");
    await act(() => {
      rows[0].click();
    });
    assert.equal(textareaOf(container).value, "partial", "pressing partial did not put it in the box");
  }));

test("QuestionThread: Send answers once with the box's words, trimmed", () => {
  const { answers, onAnswer } = recorder();
  return withThread({ onAnswer }, async (container) => {
    await typeInto(container, "  Allow the bump, and say so.  ");
    await press(container, "Send");
    assert.deepEqual(answers, [{ words: "Allow the bump, and say so." }], "Send did not answer once with its words");
  });
});

test("QuestionThread: Accept answers once with accept, whatever the box holds", () => {
  const { answers, onAnswer } = recorder();
  return withThread({ onAnswer }, async (container) => {
    await typeInto(container, "Something else");
    await press(container, "Accept");
    assert.deepEqual(answers, [{ accept: true }], "Accept did not answer once with accept");
    assert.equal(textareaOf(container).value, "Something else", "Accept cleared the box");
  });
});

test("QuestionThread: a refused answer shows its message in the block, and the box keeps its words", () =>
  withThread(
    { onAnswer: () => Promise.reject(new Error("Not answered: Q3 is already settled.")) },
    async (container) => {
      await typeInto(container, "Allow the bump.");
      await press(container, "Send");
      assert.equal(
        block(container).querySelector('[role="alert"]')?.textContent,
        "Not answered: Q3 is already settled.",
        "the refusal's message is not shown in the block",
      );
      assert.equal(textareaOf(container).value, "Allow the bump.", "the refused answer's words left the box");
      assert.equal(buttonNamed(container, "Send").disabled, false, "Send is still held after the refusal");
    },
  ));

test("QuestionThread: Send and Accept are disabled while a call is in flight, and a press sends nothing more", () => {
  const answers: QuestionAnswer[] = [];
  let release: () => void = () => {};
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  return withThread(
    {
      onAnswer: (answer) => {
        answers.push(answer);
        return pending;
      },
    },
    async (container) => {
      await typeInto(container, "Allow the bump.");
      await press(container, "Send");
      assert.equal(buttonNamed(container, "Send").disabled, true, "Send is not disabled while its call is in flight");
      assert.equal(buttonNamed(container, "Accept").disabled, true, "Accept is not disabled while a call is in flight");
      await press(container, "Accept");
      assert.deepEqual(answers, [{ words: "Allow the bump." }], "a press while a call was in flight sent an answer");
      await act(async () => {
        release();
        await turn();
      });
      assert.equal(buttonNamed(container, "Send").disabled, false, "Send is still disabled once the call settled");
    },
  );
});

test("QuestionThread: the thread draws each line's who, its time in code, its what and its words, oldest first", () =>
  withThread({ lines: [asked, said, recorded] }, async (container) => {
    const messages = [...block(container).querySelectorAll(".qt-msg")];
    assert.equal(messages.length, 3, "the thread does not draw each line");
    assert.deepEqual(
      messages.map((message) => message.querySelector(".qt-who")?.textContent),
      ["the Director", "you", "orreryd"],
      "a line's who is not named from its maker",
    );
    const times = messages.map((message) => message.querySelector("code")?.textContent ?? "");
    assert.deepEqual(
      times,
      [noticeClock(asked.time), noticeClock(said.time), noticeClock(recorded.time)],
      "a line's time is not drawn in code",
    );
    for (const time of times) {
      assert.match(time, /^\d{2}:\d{2}$/, "a line's time is not hh:mm");
    }
    assert.deepEqual(
      messages.map((message) => message.querySelector(".qt-what")?.textContent),
      ["asked", "said", "recorded"],
    );
    assert.deepEqual(
      messages.map((message) => message.querySelector(".qt-words")?.textContent ?? null),
      [null, "Which of the two keeps the Lisbon case?", null],
      "a line's words are not drawn only where it has them",
    );
  }));

test("QuestionThread: a settled question draws one line with its answer and who settled it, and no box, option or Accept", () =>
  withThread(
    {
      question: settled(create(RecordMakerSchema, { kind: "owner_serve" }), { accepted: true }),
      lines: [asked],
    },
    async (container) => {
      const section = block(container);
      assertNone(section.querySelector("textarea"), "a box is drawn after the question settled");
      assertNone(section.querySelector(".opts"), "an option is drawn after the question settled");
      assert.equal(
        [...section.querySelectorAll("button")].length,
        0,
        "a button is drawn after the question settled",
      );
      assert.equal(section.querySelector("b")?.textContent, "Keep the pin", "the answer is not drawn");
      assert.equal(
        section.querySelector(".ink-3")?.textContent,
        "· accepted the recommendation",
        "the accepted recommendation is not said",
      );
      assert.equal(section.querySelector(".who")?.textContent, "· you, in Orrery", "who settled it is not named");
      assert.deepEqual(
        [...section.children].filter((child) => ["DIV", "P", "TEXTAREA", "BUTTON"].includes(child.tagName)),
        [],
        "the settled block is not one line",
      );
    },
  ));

test("QuestionThread: a settled question's words are drawn quoted, and its thread folds behind a disclosure", () =>
  withThread(
    {
      question: settled(create(RecordMakerSchema, { kind: "owner_serve" }), {
        answer: "Keep the pin, and skip Lisbon until March.",
      }),
      lines: [asked, said],
    },
    async (container) => {
      const section = block(container);
      assert.equal(
        section.querySelector("b")?.textContent,
        "“Keep the pin, and skip Lisbon until March.”",
        "the words are not drawn quoted",
      );
      assert.equal(section.querySelector(".ink-3")?.textContent, "· you, in Orrery");
      const fold = section.querySelector("details");
      assert.ok(fold, "the thread is not folded behind a disclosure");
      assert.equal(fold.open, false, "the thread is open by default");
      assert.equal(fold.querySelector("summary")?.textContent, "2 messages about it");
      assert.equal(fold.querySelectorAll(".qt-msg").length, 2, "the folded thread does not hold each line");
    },
  ));

test("QuestionThread: whoOfMaker's three makers name who settled the question, each with its quote, reason or delegation", async () => {
  const makers: [RecordMaker, string][] = [
    [create(RecordMakerSchema, { kind: "owner_serve" }), "· you, in Orrery"],
    [
      create(RecordMakerSchema, { kind: "owner_conversation", quote: "Keep the pin." }),
      "· you, in the conversation, quoting “Keep the pin.”",
    ],
    [
      create(RecordMakerSchema, {
        kind: "conversation",
        reason: "the Lisbon rule is not on any rota until March",
        delegation: "decide the small ones",
      }),
      "· the Director, because the Lisbon rule is not on any rota until March, under your words “decide the small ones”",
    ],
  ];
  for (const [maker, who] of makers) {
    await withThread({ question: settled(maker) }, async (container) => {
      assert.equal(
        block(container).querySelector(".who")?.textContent,
        who,
        `${maker.kind} is not named as it settled`,
      );
    });
  }
});

test("QuestionThread: in its design the block carries its id as its anchor, and in the conversation it carries none", async () => {
  await withThread({ place: "design" }, async (container) => {
    assert.equal(block(container).id, "Q3", "the design's block is not anchored by its id");
  });
  await withThread({ place: "conversation" }, async (container) => {
    assert.equal(block(container).id, "", "the conversation's block is anchored");
  });
});
