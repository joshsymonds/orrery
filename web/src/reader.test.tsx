import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  WaitingMessageSchema,
  type ConversationItem,
  type EngineText,
} from "./gen/orrery/v1/orrery_pb";
import type { Conversation } from "./conversation";
import type { ParsedQuestion } from "./question";
import { Reader } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

function unfinished(text: string): EngineText {
  return create(EngineTextSchema, { messageId: "m1", index: 0, text, kind: "text" });
}

function conversation(init: Partial<Conversation> = {}): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items: [],
    state: create(ConversationStateSchema, {}),
    unfinished: [],
    ...init,
  };
}

interface Props {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  question?: ParsedQuestion | null;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  onToggleOpen?: () => void;
}

async function withReader(props: Props, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Reader {...props} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// rowTexts returns the text of each row of the reader, in order.
function rowTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".reader > .t")].map((row) => row.textContent ?? "");
}

test("a reply holding script markup draws as text with no element", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "<script>alert(1)</script>" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector("script"), null);
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
    },
  ));

test("a reply holding an img onerror string draws as text with no element", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "<img src=x onerror=alert(1)>" })],
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector("img"), null);
      assert.ok(container.textContent?.includes("<img src=x onerror=alert(1)>"));
    },
  ));

test("only http and https URLs in item text become links", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: 100n, text: "see https://example.test/a and javascript:alert(1)" }),
        ],
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      const links = container.querySelectorAll("a");
      assert.equal(links.length, 1);
      assert.equal(links[0].getAttribute("href"), "https://example.test/a");
    },
  ));

test("the new rule sits before the first item newer than seenAt", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: 100n, text: "old one" }),
          item({ kind: "reply", time: 200n, text: "first new" }),
          item({ kind: "reply", time: 300n, text: "second new" }),
        ],
      }),
      seenAt: 150,
      open: false,
    },
    (container) => {
      const rows = rowTexts(container);
      assert.equal(rows.length, 4);
      assert.ok(rows[0].includes("old one"));
      assert.ok(rows[1].includes("since you looked"), `row 1 was ${rows[1]}`);
      assert.ok(rows[2].includes("first new"));
      assert.ok(rows[3].includes("second new"));
      assert.equal(container.querySelectorAll(".rule.new").length, 1);
    },
  ));

test("the new rule is absent when no item is newer than seenAt", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: 100n, text: "one" }),
          item({ kind: "reply", time: 200n, text: "two" }),
        ],
      }),
      seenAt: 200,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".rule.new").length, 0);
      assert.equal(container.textContent?.includes("since you looked"), false);
    },
  ));

test("a compaction shows its rule line, and its summary only when open", async () => {
  const compaction = item({
    kind: "compaction",
    time: 100n,
    text: "Synthetic summary text.",
    trigger: "auto",
    tokens: 611000n,
  });
  const props = { conversation: conversation({ items: [compaction] }), seenAt: 0 };

  await withReader({ ...props, open: false }, (container) => {
    assert.ok(container.textContent?.includes("compacted"));
    assert.ok(container.textContent?.includes("auto"));
    assert.ok(container.textContent?.includes("611k"));
    assert.equal(container.textContent?.includes("Synthetic summary text."), false);
  });

  await withReader({ ...props, open: true }, (container) => {
    assert.ok(container.textContent?.includes("compacted"));
    assert.ok(container.textContent?.includes("Synthetic summary text."));
  });
});

test("unfinished text follows the items and is marked live", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "finished" })],
        unfinished: [unfinished("still streaming")],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      const rows = rowTexts(container);
      assert.equal(rows.length, 2);
      assert.ok(rows[0].includes("finished"));
      assert.ok(rows[1].includes("still streaming"));
      assert.ok(container.querySelector(".live") !== null);
    },
  ));

test("waiting messages stack under the items, oldest first, and a canceled one is marked", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "done" })],
        state: create(ConversationStateSchema, {
          waiting: [
            create(WaitingMessageSchema, { text: "first queued", time: 110n, canceled: false }),
            create(WaitingMessageSchema, { text: "second queued", time: 120n, canceled: true }),
          ],
        }),
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      const waiting = [...container.querySelectorAll(".queued")];
      assert.equal(waiting.length, 2);
      assert.ok(waiting[0].textContent?.includes("first queued"));
      assert.ok(waiting[1].textContent?.includes("second queued"));
      assert.equal(waiting[0].classList.contains("canceled"), false);
      assert.equal(waiting[1].classList.contains("canceled"), true);
      assert.ok(waiting[1].textContent?.includes("canceled"));
      assert.ok(waiting[0].textContent?.includes("waiting"));
    },
  ));

test("a null conversation draws a loading line", () =>
  withReader({ conversation: null, seenAt: 0, open: false }, (container) => {
    assert.ok(container.textContent?.includes("loading"));
  }));

const askQuestion: ParsedQuestion = {
  title: "Which beacon schedule?",
  options: [
    { label: "1 Hourly", description: "Synthetic hourly option" },
    { label: "2 Daily", description: "Synthetic daily option" },
    { label: "3 Weekly", description: "Synthetic weekly option" },
    { label: "4 Never", description: "Synthetic fourth option" },
  ],
};

test("a question draws its title and labels as text, one button per first three options", async () => {
  const answers: number[] = [];
  await withReader(
    {
      conversation: conversation(),
      seenAt: 0,
      open: false,
      question: askQuestion,
      onAnswer: (choice) => answers.push(choice),
    },
    (container) => {
      const card = container.querySelector(".question-card");
      assert.ok(card?.textContent?.includes("Which beacon schedule?"));
      assert.ok(card?.textContent?.includes("1 Hourly"));
      assert.ok(card?.textContent?.includes("Synthetic fourth option"));

      const buttons = [...container.querySelectorAll<HTMLButtonElement>("button")];
      assert.equal(buttons.length, 3);
      assert.ok(buttons[1].textContent?.includes("2 Daily"));
      assert.ok(buttons[1].textContent?.includes("Synthetic daily option"));
      assert.equal(
        buttons.some((button) => button.textContent?.includes("4 Never")),
        false,
      );
      assert.ok(container.textContent?.includes("4 Never"));

      buttons[1].click();
      assert.deepEqual(answers, [2]);
    },
  );
});

test("a question holding markup draws its title and labels as text with no element", () =>
  withReader(
    {
      conversation: conversation(),
      seenAt: 0,
      open: false,
      question: {
        title: "<b>bold</b> <img src=x onerror=alert(1)>",
        options: [{ label: "<script>alert(1)</script>", description: "<a href=x>link</a>" }],
      },
      onAnswer: () => {},
    },
    (container) => {
      assert.equal(container.querySelector("b"), null);
      assert.equal(container.querySelector("img"), null);
      assert.equal(container.querySelector("script"), null);
      assert.equal(container.querySelector("a"), null);
      assert.ok(container.textContent?.includes("<b>bold</b> <img src=x onerror=alert(1)>"));
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
      assert.ok(container.textContent?.includes("<a href=x>link</a>"));
    },
  ));

test("the compaction rule is a button that calls onToggleOpen, and a plain rule without it", async () => {
  const compaction = item({ kind: "compaction", time: 100n, text: "Synthetic summary text.", trigger: "auto" });
  let toggles = 0;

  await withReader({ conversation: conversation({ items: [compaction] }), seenAt: 0, open: false }, (container) => {
    assert.equal(container.querySelector("button"), null);
    assert.ok(container.querySelector(".compaction .rule") !== null);
  });

  await withReader(
    {
      conversation: conversation({ items: [compaction] }),
      seenAt: 0,
      open: false,
      onToggleOpen: () => {
        toggles += 1;
      },
    },
    (container) => {
      const rule = container.querySelector<HTMLButtonElement>("button.rule");
      assert.ok(rule !== null);
      assert.ok(rule.textContent?.includes("compacted"));
      rule.click();
      assert.equal(toggles, 1);
    },
  );
});
