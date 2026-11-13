import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { EpicStepSchema, type EpicStep } from "./gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  WaitingMessageSchema,
  type ConversationItem,
  type EngineText,
} from "./gen/orrery/v1/orrery_pb";
import type { Conversation } from "./conversation";
import { flashMillis } from "./flash";
import type { ParsedQuestion } from "./question";
import { Reader, ReaderHead } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

function unfinished(text: string, kind = "text", index = 0): EngineText {
  return create(EngineTextSchema, { messageId: "m1", index, text, kind });
}

// callItem is a tool call held in the window under id, made with input.
function callItem(id: string, input: string, time = 100n): ConversationItem {
  return item({ kind: "tool", time, tool: "Bash", input, id });
}

// resultItem is the result of the call under id, failed when error is set.
function resultItem(id: string, text: string, error = false, time = 100n): ConversationItem {
  return item({ kind: "result", time, id, text, error });
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
  step?: EpicStep;
  onAnswer?: (choice: 1 | 2 | 3) => void;
  onToggleOpen?: () => void;
}

async function withReader(props: Props, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Reader {...props} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// clickControl clicks a control inside act, so the render its click causes has run before the next check.
async function clickControl(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

function flashing(control: HTMLElement): boolean {
  return control.classList.contains("flash");
}

// rowTexts returns the text of each row of the reader, in order.
function rowTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".reader > .t")].map((row) => row.textContent ?? "");
}

const rowPixels = 100;
const viewportPixels = 300;

// readerOf returns the reader's root element, the scroll container of its conversation.
function readerOf(container: HTMLElement): HTMLElement {
  const reader = container.querySelector<HTMLElement>(".reader");
  assert.ok(reader !== null, "the reader is drawn");
  return reader;
}

// stubLayout gives the reader the layout a browser would: each row is rowPixels tall, the viewport is viewportPixels
// tall, and scrollTop clamps to the content. happy-dom has no layout, so the stub reads the rows the DOM holds.
function stubLayout(reader: HTMLElement): void {
  let top = 0;
  const furthest = () => Math.max(0, reader.scrollHeight - viewportPixels);
  Object.defineProperties(reader, {
    scrollHeight: { configurable: true, get: () => reader.children.length * rowPixels },
    clientHeight: { configurable: true, value: viewportPixels },
    scrollTop: {
      configurable: true,
      get: () => Math.min(top, furthest()),
      set: (value: number) => {
        top = Math.min(Math.max(value, 0), furthest());
      },
    },
  });
}

// withScrollingReader mounts a reader the way the app does, from a null conversation to its conversation, with its
// layout stubbed. The check gets the container and a rerender that draws new props into the same mounted reader.
async function withScrollingReader(
  props: Props,
  check: (container: HTMLElement, rerender: (next: Props) => Promise<void>) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const rerender = (next: Props) =>
    act(() => {
      render(<Reader {...next} />, container);
    });
  try {
    render(<Reader {...props} conversation={null} />, container);
    stubLayout(readerOf(container));
    await rerender(props);
    await check(container, rerender);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// readerProps is the props of a reader that holds items, all read: no item is newer than seenAt.
function readerProps(items: ConversationItem[]): Props {
  return { conversation: conversation({ items }), seenAt: 10_000, open: false };
}

// heldItems returns the replies from first to last, each one's text naming its number.
function heldItems(first: number, last: number): ConversationItem[] {
  const list: ConversationItem[] = [];
  for (let n = first; n <= last; n += 1) {
    list.push(item({ kind: "reply", time: BigInt(100 + n), text: `held-${n}` }));
  }
  return list;
}

// distanceFromBottom is how far the reader's scroll sits above its bottom, by the stubbed layout.
function distanceFromBottom(container: HTMLElement): number {
  const reader = readerOf(container);
  return reader.scrollHeight - reader.scrollTop - reader.clientHeight;
}

// rowWith returns the row of the reader whose text includes label.
function rowWith(container: HTMLElement, label: string): HTMLElement {
  const row = [...container.querySelectorAll<HTMLElement>(".reader > .t")].find((candidate) =>
    (candidate.textContent ?? "").includes(label),
  );
  assert.ok(row !== undefined, `a row draws ${label}`);
  return row;
}

test("a reply holding script markup draws as text with no element", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "<script>alert(1)</script>" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector("script") === null, true);
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
      assert.equal(container.querySelector("img") === null, true);
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

test("a reply draws its markdown as elements inside .reply: a list, a code block and strong", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: 100n, text: "- one\n- two\n\n```\nlet x = a < b && c;\n```\n\n**bold** end" }),
        ],
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".reader > .t > .body > .reply > ul > li").length, 2);
      assert.equal(
        container.querySelector(".reader > .t > .body > .reply > pre > code")?.textContent,
        "let x = a < b && c;",
      );
      assert.equal(container.querySelector(".reader > .t > .body > .reply strong")?.textContent, "bold");
    },
  ));

test("a reply's table draws its header and cells in order inside .reply", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "| a | b < c |\n| - | - |\n| 1 < 2 | 3 & 4 |" })],
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      const headers = [...container.querySelectorAll(".reader > .t > .body > .reply > table thead th")].map(
        (cell) => cell.textContent,
      );
      assert.deepEqual(headers, ["a", "b < c"]);
      const cells = [...container.querySelectorAll(".reader > .t > .body > .reply > table tbody td")].map(
        (cell) => cell.textContent,
      );
      assert.deepEqual(cells, ["1 < 2", "3 & 4"]);
    },
  ));

test("a reply's quote draws blockquote around its paragraph inside .reply", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "> quoted < text" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(
        container.querySelector(".reader > .t > .body > .reply > blockquote > p")?.textContent,
        "quoted < text",
      );
    },
  ));

test("a reply's rule draws hr between its paragraphs inside .reply", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "above\n\n---\n\nbelow" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".reader > .t > .body > .reply > hr").length, 1);
      assert.equal(container.querySelector(".reader > .t > .body > .reply")?.textContent, "abovebelow");
    },
  ));

test("a reply's heading draws h2 with its text inside .reply", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "## Plan < two" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector(".reader > .t > .body > .reply > h2")?.textContent, "Plan < two");
    },
  ));

test("a reply's link draws an a with its http(s) href inside .reply", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "[docs](https://example.test/docs) and [bad](javascript:x)" })],
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".reader > .t > .body > .reply a").length, 1);
      assert.equal(
        container.querySelector(".reader > .t > .body > .reply a")?.getAttribute("href"),
        "https://example.test/docs",
      );
      assert.equal(container.querySelector(".reader > .t > .body > .reply")?.textContent, "docs and bad");
    },
  ));

test("a reply's emphasis and strikethrough draw em and del inside .reply", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "*soft* and ~~gone~~" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector(".reader > .t > .body > .reply > p > em")?.textContent, "soft");
      assert.equal(container.querySelector(".reader > .t > .body > .reply > p > del")?.textContent, "gone");
    },
  ));

test("a reply marked cannot lex draws its text whole inside .reply without throwing", () => {
  const text = ">".repeat(2000) + " x";
  return withReader(
    { conversation: conversation({ items: [item({ kind: "reply", time: 100n, text })] }), seenAt: 0, open: false },
    (container) => {
      assert.equal(container.querySelector(".reader .reply")?.textContent ?? null, text);
    },
  );
});

test("a reply of 1000 nested list items draws without throwing and keeps its deepest item", () => {
  // Levels 0 through 32 draw as lists, so the list at level 33 is the first drawn as its raw source: 967 markers left.
  const text = "- ".repeat(1000) + "item";
  return withReader(
    { conversation: conversation({ items: [item({ kind: "reply", time: 100n, text })] }), seenAt: 0, open: false },
    (container) => {
      assert.equal(container.querySelector(".reader .reply")?.textContent ?? null, "- ".repeat(967) + "item");
    },
  );
});

test("a reply of 3000 nested emphasis markers around a word draws without throwing and keeps the word", () => {
  const text = "*".repeat(3000) + "word" + "*".repeat(3000);
  return withReader(
    { conversation: conversation({ items: [item({ kind: "reply", time: 100n, text })] }), seenAt: 0, open: false },
    (container) => {
      assert.equal(container.querySelector(".reader .reply")?.textContent ?? null, text);
    },
  );
});

test("a prompt draws its markdown as elements inside .reply", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "prompt", time: 100n, text: "**ask** `how`" })] }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector(".you > .reply > p > strong")?.textContent, "ask");
      assert.equal(container.querySelector(".you > .reply > p > code")?.textContent, "how");
    },
  ));

// stepWith is a step holding the checks given, in the order given.
function stepWith(checks: { round: number; name: string; ok: boolean }[]): EpicStep {
  return create(EpicStepSchema, { checks });
}

// chipTexts returns the text of each check chip of a card, in order, and badChipTexts only the failed ones.
function chipTexts(card: HTMLElement): string[] {
  return [...card.querySelectorAll(".chip")].map((chip) => chip.textContent ?? "");
}

function badChipTexts(card: HTMLElement): string[] {
  return [...card.querySelectorAll(".chip.bad")].map((chip) => chip.textContent ?? "");
}

test("a step's brief is one fold line with no YOU, and it opens to its text", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({
            kind: "prompt",
            time: 100n,
            sender: "brief",
            text: "Goal: export the rota\nOwned: cli/export.go",
          }),
        ],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([]),
    },
    async (container) => {
      const fold = container.querySelector<HTMLElement>("button.fold.brief");
      assert.ok(fold !== null, "the brief is a fold button");
      assert.equal(fold.querySelector("b")?.textContent, "Brief");
      assert.equal(fold.querySelector(".names")?.textContent, "from the engine");
      assert.equal(container.querySelector(".you") === null, true, "the brief is not YOU");
      assert.equal(container.querySelector(".fold-text") === null, true, "the brief is folded until clicked");

      await clickControl(fold);
      const text = fold.nextElementSibling;
      assert.equal(text?.getAttribute("class"), "fold-text", "the text opens after the fold");
      assert.equal(text?.textContent?.includes("Owned: cli/export.go"), true, "the whole brief is shown");
    },
  ));

test("a window starting after the brief draws its watchdog prompt as one Engine line, with no Brief", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({
            kind: "prompt",
            time: 200n,
            sender: "engine",
            text: "watchdog: no progress for 10 minutes\nsecond line",
          }),
        ],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([]),
    },
    async (container) => {
      assert.equal(container.querySelector("button.fold.brief") === null, true, "no Brief is drawn");
      assert.equal(container.querySelector(".you") === null, true, "the watchdog is not YOU");
      assert.equal(container.querySelectorAll("button.fold.engine").length, 1);
      const fold = container.querySelector<HTMLElement>("button.fold.engine");
      assert.ok(fold !== null, "the watchdog is an Engine fold");
      assert.equal(fold.querySelector("b")?.textContent, "Engine");
      assert.equal(fold.querySelector(".names")?.textContent, "watchdog: no progress for 10 minutes");

      await clickControl(fold);
      assert.equal(fold.nextElementSibling?.getAttribute("class"), "fold-text");
      assert.equal(fold.nextElementSibling?.textContent?.includes("second line"), true, "the whole text is shown");
    },
  ));

test("a prompt with no sender, and one with round 0, each draw as an Engine fold line", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "prompt", time: 100n, sender: "", text: "unmarked prompt" }),
          item({ kind: "prompt", time: 200n, sender: "engine", round: 0, text: "round zero prompt" }),
        ],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([]),
    },
    (container) => {
      const folds = [...container.querySelectorAll("button.fold.engine .names")].map((names) => names.textContent);
      assert.deepEqual(folds, ["unmarked prompt", "round zero prompt"]);
      assert.equal(container.querySelector(".you") === null, true);
      assert.equal(container.querySelector(".card") === null, true);
    },
  ));

test("each send-back draws a card of its own round's checks, with its first line and its rest folded", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({
            kind: "prompt",
            time: 300n,
            sender: "engine",
            round: 1,
            text: "gate failed: exit 1\nthe gate's output, line two\nline three",
          }),
          item({
            kind: "prompt",
            time: 400n,
            sender: "engine",
            round: 2,
            text: "all checks passed on round two",
          }),
        ],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([
        { round: 1, name: "schema", ok: true },
        { round: 1, name: "gate", ok: false },
        { round: 2, name: "schema", ok: true },
        { round: 2, name: "gate", ok: true },
      ]),
    },
    async (container) => {
      const cards = [...container.querySelectorAll<HTMLElement>("div.card")];
      assert.equal(cards.length, 2, "one card per send-back");
      const [first, second] = cards;
      assert.ok(first !== undefined && second !== undefined, "both rounds draw a card");

      assert.equal(first.classList.contains("bad"), true, "round 1 has a failed check: the edge is bad");
      assert.equal(first.classList.contains("ok"), false);
      assert.equal(second.classList.contains("ok"), true, "round 2 passed: the edge is ok");
      assert.equal(second.classList.contains("bad"), false);
      assert.equal(first.querySelector(".cap")?.textContent?.includes("Checks · round 1"), true);
      assert.equal(second.querySelector(".cap")?.textContent?.includes("Checks · round 2"), true);
      assert.equal(first.querySelector(".cap span")?.textContent, "from the engine");

      assert.deepEqual(chipTexts(first), ["schema ✓", "gate ✗"], "round 1 shows only its own checks");
      assert.deepEqual(badChipTexts(first), ["gate ✗"]);
      assert.deepEqual(chipTexts(second), ["schema ✓", "gate ✓"], "round 2 shows only its own checks");
      assert.deepEqual(badChipTexts(second), []);

      assert.equal(first.querySelector("p.note") === null, true, "a multi-line send-back has no p.note");
      const edge = first.querySelector<HTMLElement>("button.note");
      assert.ok(edge !== null, "the first line is a button");
      assert.equal(edge.textContent, "gate failed: exit 1");
      assert.equal(first.querySelector(".rest") === null, true, "the rest is folded until clicked");

      await clickControl(edge);
      const rest = first.querySelector(".rest");
      assert.equal(rest?.textContent?.includes("line three"), true, "the rest is shown after a click");
      assert.equal(rest?.textContent?.includes("gate failed"), false, "the rest leaves out the first line");

      assert.equal(second.querySelector("button.note") === null, true, "a one-line send-back has no button");
      assert.equal(second.querySelector("p.note")?.textContent, "all checks passed on round two");
      assert.equal(second.querySelector(".rest") === null, true);
    },
  ));

test("an owner prompt is YOU in a step, so a nudge is not drawn as the engine's", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "prompt", time: 100n, sender: "owner", text: "**nudge** text" })],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([{ round: 1, name: "gate", ok: false }]),
    },
    (container) => {
      assert.equal(container.querySelector(".you > small")?.textContent, "YOU");
      assert.equal(container.querySelector(".you > .reply > p > strong")?.textContent, "nudge");
      assert.equal(container.querySelector("button.fold") === null, true, "the nudge is not a fold");
      assert.equal(container.querySelector(".card") === null, true, "the nudge is not a card");
    },
  ));

test("without a step every prompt draws as YOU, whatever its sender and round", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "prompt", time: 100n, sender: "engine", round: 1, text: "engine text" }),
          item({ kind: "prompt", time: 200n, sender: "brief", text: "brief text" }),
        ],
      }),
      seenAt: 10_000,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".you").length, 2);
      assert.equal(container.querySelector("button.fold") === null, true);
      assert.equal(container.querySelector(".card") === null, true);
    },
  ));

test("live text draws its markdown as elements inside .reply, before its caret", () =>
  withReader(
    { conversation: conversation({ unfinished: [unfinished("- streamed\n- item")] }), seenAt: 0, open: false },
    (container) => {
      assert.equal(container.querySelectorAll(".live-row > .body > .reply > ul > li").length, 2);
      assert.equal(container.querySelector(".live-row > .body > .reply + .caret") !== null, true);
    },
  ));

test("a notice holding markdown stays literal text, and a queued message is not drawn in the reader", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "notice", time: 100n, notice: "interrupted", text: "**kept** `raw`" })],
        state: create(ConversationStateSchema, {
          waiting: [create(WaitingMessageSchema, { text: "**queued** `raw`", time: 110n, canceled: false })],
        }),
      }),
      seenAt: 0,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector(".notice strong") === null, true);
      assert.equal(container.querySelector(".notice code") === null, true);
      assert.ok(container.querySelector(".notice")?.textContent?.includes("**kept** `raw`"));
      assert.equal(container.querySelector(".queued") === null, true, "no queued message is drawn");
      assert.equal(container.textContent?.includes("**queued**"), false, "the queued text is in the foot, not here");
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

test("waiting messages are not drawn in the reader, which holds only the items", () =>
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
      assert.equal(container.querySelector(".queued") === null, true, "no queued message is drawn");
      assert.equal(container.querySelector(".canceled") === null, true, "no canceled message is drawn");
      assert.equal(container.textContent?.includes("queued"), false, "the queued text is in the foot, not here");
      assert.ok(container.textContent?.includes("done"));
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
      assert.equal(container.querySelector("b") === null, true);
      assert.equal(container.querySelector("img") === null, true);
      assert.equal(container.querySelector("script") === null, true);
      assert.equal(container.querySelector("a") === null, true);
      assert.ok(container.textContent?.includes("<b>bold</b> <img src=x onerror=alert(1)>"));
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
      assert.ok(container.textContent?.includes("<a href=x>link</a>"));
    },
  ));

test("the compaction rule is a button that calls onToggleOpen, and a plain rule without it", async () => {
  const compaction = item({ kind: "compaction", time: 100n, text: "Synthetic summary text.", trigger: "auto" });
  let toggles = 0;

  await withReader({ conversation: conversation({ items: [compaction] }), seenAt: 0, open: false }, (container) => {
    assert.equal(container.querySelector("button") === null, true);
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

test("an option flashes for 300 ms after a click, restarts on a second click, and still answers", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const answers: number[] = [];
  await withReader(
    {
      conversation: conversation(),
      seenAt: 0,
      open: false,
      question: askQuestion,
      onAnswer: (choice) => answers.push(choice),
    },
    async (container) => {
      const buttons = [...container.querySelectorAll<HTMLButtonElement>("button")];
      assert.equal(flashing(buttons[1]), false, "not flashing before any click");

      await clickControl(buttons[1]);
      assert.deepEqual(answers, [2]);
      assert.equal(flashing(buttons[1]), true, "flashing at once after the click");
      assert.equal(flashing(buttons[0]), false, "the option before it does not flash");
      assert.equal(flashing(buttons[2]), false, "the option after it does not flash");
      await advance(t, flashMillis - 1);
      assert.equal(flashing(buttons[1]), true, "still flashing 1 ms before the window ends");
      await advance(t, 1);
      assert.equal(flashing(buttons[1]), false, "cleared 300 ms after the click");

      await clickControl(buttons[1]);
      await advance(t, 200);
      await clickControl(buttons[1]);
      await advance(t, 200);
      assert.equal(
        flashing(buttons[1]),
        true,
        "still flashing 200 ms after the second click, inside the restarted window",
      );
      await advance(t, 99);
      assert.equal(flashing(buttons[1]), true, "still flashing 1 ms before the restarted window ends");
      await advance(t, 1);
      assert.equal(flashing(buttons[1]), false, "cleared 300 ms after the second click");
      assert.deepEqual(answers, [2, 2, 2]);
    },
  );
});

test("the compaction rule flashes for 300 ms after a click and still toggles", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const compaction = item({ kind: "compaction", time: 100n, text: "Synthetic summary text.", trigger: "auto" });
  let toggles = 0;
  await withReader(
    {
      conversation: conversation({ items: [compaction] }),
      seenAt: 0,
      open: false,
      onToggleOpen: () => {
        toggles += 1;
      },
    },
    async (container) => {
      const rule = container.querySelector<HTMLButtonElement>("button.rule");
      assert.ok(rule !== null);
      assert.equal(flashing(rule), false, "not flashing before any click");

      await clickControl(rule);
      assert.equal(toggles, 1);
      assert.equal(flashing(rule), true, "flashing at once after the click");
      await advance(t, flashMillis - 1);
      assert.equal(flashing(rule), true, "still flashing 1 ms before the window ends");
      await advance(t, 1);
      assert.equal(flashing(rule), false, "cleared 300 ms after the click");
    },
  );
});

test("a conversation opening from null shows its bottom", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), (container) => {
    assert.equal(distanceFromBottom(container), 0);
  }));

test("a new item while the reader sits at its bottom follows to the new bottom", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), async (container, rerender) => {
    const reader = readerOf(container);
    reader.scrollTop = reader.scrollHeight;
    assert.equal(distanceFromBottom(container), 0, "the reader starts at its bottom");

    await rerender(readerProps(heldItems(1, 21)));
    assert.equal(distanceFromBottom(container), 0);
  }));

test("a new item while the reader is scrolled up leaves its scroll place alone", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), async (container, rerender) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;

    await rerender(readerProps(heldItems(1, 21)));
    assert.equal(reader.scrollTop, 500);
  }));

test("at the 50-item cap, appending one item and dropping the oldest keeps a held row's node", () =>
  withScrollingReader(readerProps(heldItems(1, 50)), async (container, rerender) => {
    const held = rowWith(container, "held-25");

    await rerender(readerProps(heldItems(2, 51)));
    assert.equal(readerOf(container).querySelectorAll(".reader > .t").length, 50);
    assert.ok(rowWith(container, "held-25") === held, "the held row keeps its DOM node");
  }));

// foldLine returns the call line that is a button, the line that folds a held result.
function foldLine(container: HTMLElement): HTMLButtonElement {
  const line = container.querySelector<HTMLButtonElement>("button.call");
  assert.ok(line !== null, "a call with a held result draws its line as a button");
  return line;
}

// thinkingLine returns the button of a live thinking row, the button that folds the block's text.
function thinkingLine(container: HTMLElement): HTMLButtonElement {
  const line = container.querySelector<HTMLButtonElement>("button.thinking");
  assert.ok(line !== null, "a live thinking block draws its row as a button");
  return line;
}

test("a tool item is one line of its name and input, and its input draws as text", () =>
  withReader(
    { conversation: conversation({ items: [callItem("c1", "curl https://example.test/a")] }), seenAt: 100, open: false },
    (container) => {
      assert.equal(rowTexts(container).length, 1);
      const line = container.querySelector(".call");
      assert.ok(line !== null, "the call draws a line");
      assert.ok(line.textContent?.includes("Bash"));
      assert.equal(line.querySelector(".in")?.textContent, "curl https://example.test/a");
      assert.equal(line.querySelector("a") === null, true, "the input draws no link");
    },
  ));

test("a call's result folds under its line: absent until the line is clicked, then under it, then folded again", () =>
  withReader(
    {
      conversation: conversation({ items: [callItem("c1", "make"), resultItem("c1", "built-output")] }),
      seenAt: 100,
      open: false,
    },
    async (container) => {
      assert.equal(rowTexts(container).length, 1, "the result is not a row of its own");
      assert.equal(container.textContent?.includes("built-output"), false, "the result is folded");

      const line = foldLine(container);
      await clickControl(line);
      const shown = line.nextElementSibling;
      assert.equal(shown?.textContent, "built-output", "the result draws right under the line");
      assert.equal(rowTexts(container).length, 1);

      await clickControl(line);
      assert.equal(container.textContent?.includes("built-output"), false, "a second click folds it");
    },
  ));

test("a failed result marks its call's line while folded, and a succeeded one does not", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          callItem("c1", "rm -rf build"),
          resultItem("c1", "permission denied", true),
          callItem("c2", "make all"),
          resultItem("c2", "ok"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.ok(rowWith(container, "rm -rf build").textContent?.includes("✗"), "the failed line is marked");
      assert.equal(rowWith(container, "make all").textContent?.includes("✗"), false, "the succeeded line is not");
      assert.equal(container.textContent?.includes("permission denied"), false, "the failure stays folded");
    },
  ));

test("a question item is one line of its tool and question, and its result folds under it", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "question", time: 100n, tool: "AskUserQuestion", text: "Which schedule?", id: "q1" }),
          resultItem("q1", "Daily"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    async (container) => {
      assert.equal(rowTexts(container).length, 1, "the result is not a row of its own");
      const line = foldLine(container);
      assert.ok(line.textContent?.includes("Which schedule?"));
      assert.equal(container.textContent?.includes("Daily"), false, "the result is folded");

      await clickControl(line);
      assert.equal(line.nextElementSibling?.textContent, "Daily");
    },
  ));

test("a result whose call is not held is hidden", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "reply", time: 100n, text: "kept reply" }), resultItem("gone", "orphan-output")],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.equal(rowTexts(container).length, 1);
      assert.ok(container.textContent?.includes("kept reply"));
      assert.equal(container.textContent?.includes("orphan-output"), false);
    },
  ));

test("the new rule sits before the first row newer than seenAt, not before a result it skips", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: 100n, text: "old one" }),
          callItem("c1", "make", 100n),
          resultItem("c1", "late-output", false, 200n),
          item({ kind: "reply", time: 300n, text: "newer reply" }),
        ],
      }),
      seenAt: 150,
      open: false,
    },
    (container) => {
      const rows = rowTexts(container);
      assert.equal(rows.length, 4);
      assert.ok(rows[2].includes("since you looked"), `row 2 was ${rows[2]}`);
      assert.ok(rows[3].includes("newer reply"));
      assert.equal(container.textContent?.includes("late-output"), false);
    },
  ));

test("a live thinking block is one row reading thinking, folded until clicked, then drawn, then folded again", () =>
  withReader(
    {
      conversation: conversation({ unfinished: [unfinished("pondering the schedule", "thinking")] }),
      seenAt: 0,
      open: false,
    },
    async (container) => {
      const rows = rowTexts(container);
      assert.equal(rows.length, 1);
      assert.ok(rows[0].includes("thinking"));
      assert.equal(container.textContent?.includes("pondering the schedule"), false, "the thinking is folded");

      const line = thinkingLine(container);
      await clickControl(line);
      assert.equal(line.nextElementSibling?.textContent, "pondering the schedule");

      await clickControl(line);
      assert.equal(container.textContent?.includes("pondering the schedule"), false, "a second click folds it");
    },
  ));

test("an opened result stays under its call as items arrive and the window slides", () => {
  const call = callItem("c1", "make", 200n);
  const output = resultItem("c1", "built-output", false, 200n);
  return withScrollingReader(readerProps([...heldItems(1, 48), call, output]), async (container, rerender) => {
    await clickControl(foldLine(container));
    assert.equal(rowWith(container, "make").querySelector(".result")?.textContent, "built-output");

    // Two new items arrive and the two oldest drop, so the call and its result stay in the window.
    await rerender(readerProps([...heldItems(3, 48), call, output, ...heldItems(49, 50)]));
    const line = foldLine(container);
    assert.equal(line.nextElementSibling?.textContent, "built-output", "the result stays open under its call");
  });
});

// toolItem is a tool call held in the window under id, made by tool with input.
function toolItem(tool: string, id: string, input: string, time = 100n): ConversationItem {
  return item({ kind: "tool", time, tool, input, id });
}

// foldOf returns the button of a run's fold, the line that counts the run's calls.
function foldOf(container: HTMLElement): HTMLButtonElement {
  const fold = container.querySelector<HTMLButtonElement>("button.fold.calls");
  assert.ok(fold !== null, "a run of three tool calls draws one fold button");
  return fold;
}

// openedCalls returns each call line of an opened run as its tool and input, in order.
function openedCalls(container: HTMLElement): string[] {
  return [...container.querySelectorAll("div.calls .call")].map(
    (line) => `${line.querySelector("b")?.textContent} ${line.querySelector(".in")?.textContent}`,
  );
}

test("two tool calls in a row draw two call lines and no run fold", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Read", "r2", "b.txt"),
          resultItem("r2", "beta"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelectorAll(".call").length, 2);
      assert.equal(container.querySelector("button.fold.calls") === null, true, "two calls draw no fold");
    },
  ));

test("three tool calls fold to one line that opens to a line per call and folds again", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Read", "r2", "b.txt"),
          resultItem("r2", "beta"),
          toolItem("Bash", "b1", "make"),
          resultItem("b1", "built"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    async (container) => {
      const fold = foldOf(container);
      assert.equal(rowTexts(container).length, 1, "the run is one row");
      assert.equal(fold.querySelector("b")?.textContent, "3 tool calls");
      assert.equal(fold.querySelector(".names")?.textContent, "Read ×2, Bash");
      assert.equal(fold.querySelector(".ok")?.textContent, "✓");
      assert.equal(fold.querySelector(".bad") === null, true, "a run that succeeded has no ✗");
      assert.equal(container.querySelectorAll(".call").length, 0, "no call line until the fold is clicked");

      await clickControl(fold);
      assert.deepEqual(openedCalls(container), ["Read a.txt", "Read b.txt", "Bash make"]);
      assert.equal(container.querySelector("div.calls .result") === null, true, "each call still folds its own result");

      const [firstLine] = container.querySelectorAll<HTMLButtonElement>("div.calls .call");
      assert.ok(firstLine !== undefined, "the opened run draws a line for its first call");
      await clickControl(firstLine);
      assert.equal(container.querySelector("div.calls .result")?.textContent, "alpha");

      await clickControl(foldOf(container));
      assert.equal(container.querySelector("div.calls") === null, true, "a second click folds the calls again");
      assert.equal(container.querySelectorAll(".call").length, 0);
    },
  ));

test("a run of three with one failed result shows ✗ in its fold and no ✓", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Bash", "b1", "rm -rf build"),
          resultItem("b1", "permission denied", true),
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Bash", "b2", "make"),
          resultItem("b2", "built"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      const fold = foldOf(container);
      assert.equal(fold.querySelector(".bad")?.textContent, "✗");
      assert.equal(fold.querySelector(".ok") === null, true, "a run with a failure has no ✓");
      assert.equal(container.textContent?.includes("permission denied"), false, "the failure stays folded");
    },
  ));

test("a reply between tool calls leaves no run fold", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Read", "r2", "b.txt"),
          resultItem("r2", "beta"),
          item({ kind: "reply", time: 100n, text: "checking the last one" }),
          toolItem("Bash", "b1", "make"),
          resultItem("b1", "built"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector("button.fold.calls") === null, true, "the reply splits the calls");
      assert.equal(container.querySelectorAll(".call").length, 3, "each call draws its own line");
    },
  ));

test("the since-you-looked rule ends a run, so the calls after it fold on their own", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt", 100n),
          resultItem("r1", "alpha", false, 100n),
          toolItem("Read", "r2", "b.txt", 100n),
          resultItem("r2", "beta", false, 100n),
          toolItem("Read", "r3", "c.txt", 200n),
          resultItem("r3", "gamma", false, 200n),
          toolItem("Bash", "b1", "make", 200n),
          resultItem("b1", "built", false, 200n),
          toolItem("Bash", "b2", "ls", 200n),
          resultItem("b2", "listed", false, 200n),
        ],
      }),
      seenAt: 150,
      open: false,
    },
    (container) => {
      const rows = rowTexts(container);
      assert.equal(rows.length, 4);
      assert.ok(rows[2].includes("since you looked"), `row 2 was ${rows[2]}`);
      assert.equal(foldOf(container).querySelector("b")?.textContent, "3 tool calls");
      assert.equal(container.querySelectorAll(".call").length, 2, "the two calls before the rule draw lines");
    },
  ));

test("a question between tool calls ends a run, so no run fold draws", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Read", "r2", "b.txt"),
          resultItem("r2", "beta"),
          item({ kind: "question", time: 100n, tool: "AskUserQuestion", text: "Which schedule?", id: "q1" }),
          resultItem("q1", "Daily"),
          toolItem("Read", "r3", "c.txt"),
          resultItem("r3", "gamma"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.equal(container.querySelector("button.fold.calls") === null, true, "the question splits the calls");
      assert.equal(container.querySelectorAll(".call").length, 4, "each call and the question draw a line");
    },
  ));

// withHead renders a head the way withReader renders a reader, and removes it the same way.
async function withHead(
  props: Parameters<typeof ReaderHead>[0],
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<ReaderHead {...props} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// headOf returns the head's header element, the element that names the conversation.
function headOf(container: HTMLElement): HTMLElement {
  const head = container.querySelector<HTMLElement>("header.head");
  assert.ok(head !== null, "the head is drawn");
  return head;
}

test("a pane's head names its label as the heading and its where line as a span after it", () =>
  withHead({ label: "Synthetic pane", where: "synthetic-project · synthetic-host" }, (container) => {
    const head = headOf(container);
    assert.equal(head.firstElementChild?.localName, "h1");
    assert.equal(head.querySelector("h1")?.textContent, "Synthetic pane");
    assert.equal(head.querySelector("span.where")?.textContent, "synthetic-project · synthetic-host");
    assert.equal(head.querySelector("h1")?.nextElementSibling?.localName, "span");
  }));

test("a session's head with an empty where draws its label and no where element", () =>
  withHead({ label: "Synthetic session", where: "" }, (container) => {
    const head = headOf(container);
    assert.equal(head.querySelector("h1")?.textContent, "Synthetic session");
    assert.equal(head.querySelector(".where") === null, true);
    assert.equal(head.children.length, 1);
  }));

test("a step's head names its epic, agent, step and round as its where line", () =>
  withHead({ label: "Synthetic step", where: "synthetic-epic › synthetic-agent · step · round 3" }, (container) => {
    const head = headOf(container);
    assert.equal(head.querySelector("h1")?.textContent, "Synthetic step");
    assert.equal(head.querySelector(".where")?.textContent, "synthetic-epic › synthetic-agent · step · round 3");
  }));

test("a child passed to the head draws after the where line as the head's last child", () =>
  withHead(
    {
      label: "Synthetic pane",
      where: "synthetic-project · synthetic-host",
      children: <span class="pill">synthetic pill</span>,
    },
    (container) => {
      const head = headOf(container);
      assert.equal(head.lastElementChild?.getAttribute("class"), "pill");
      assert.equal(head.lastElementChild?.textContent, "synthetic pill");
      assert.equal(head.lastElementChild?.previousElementSibling?.getAttribute("class"), "where");
    },
  ));

test("a label or where holding markup draws as text with no element", () =>
  withHead(
    { label: "<b>bold</b> <img src=x onerror=alert(1)>", where: "<i>italic</i> <a href=x>link</a>" },
    (container) => {
      const head = headOf(container);
      assert.equal(head.querySelector("b") === null, true);
      assert.equal(head.querySelector("img") === null, true);
      assert.equal(head.querySelector("i") === null, true);
      assert.equal(head.querySelector("a") === null, true);
      assert.equal(head.querySelector("h1")?.textContent, "<b>bold</b> <img src=x onerror=alert(1)>");
      assert.equal(head.querySelector(".where")?.textContent, "<i>italic</i> <a href=x>link</a>");
    },
  ));

test("an opened thinking block stays open as its deltas grow", () =>
  withScrollingReader(
    { conversation: conversation({ unfinished: [unfinished("first thought", "thinking")] }), seenAt: 0, open: false },
    async (container, rerender) => {
      await clickControl(thinkingLine(container));
      assert.equal(thinkingLine(container).nextElementSibling?.textContent, "first thought");

      await rerender({
        conversation: conversation({ unfinished: [unfinished("first thought, and then more", "thinking")] }),
        seenAt: 0,
        open: false,
      });
      assert.equal(
        thinkingLine(container).nextElementSibling?.textContent,
        "first thought, and then more",
        "the block stays open and shows its longer text",
      );
    },
  ));
