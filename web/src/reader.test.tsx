import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { options, render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { noEpics } from "./epics";
import { tideAccepted, tideProposed } from "./fixtures/contractrecords";
import type { Contract, ContractsGetResponse } from "./gen/orrery/v1/contracts_pb";
import {
  EpicQuestionSchema,
  EpicSchema,
  EpicsGetResponseSchema,
  EpicStepSchema,
  type EpicStep,
} from "./gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  WaitingMessageSchema,
  type ConversationItem,
  type EngineText,
} from "./gen/orrery/v1/orrery_pb";
import type { Conversation } from "./conversation";
import { findMatches, type Match } from "./find";
import { flashMillis } from "./flash";
import { clock, type ContractCalls, Reader, ReaderHead, type ReaderHandle } from "./reader";
import type { Sending } from "./sending";
import { mockTimers } from "./testclock";
import { TitleParts } from "./titleparts";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

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
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, {}),
    unfinished: [],
    queued: [],
    unsent: [],
    ...init,
  };
}

interface Props {
  conversation: Conversation | null;
  seenAt: number;
  open: boolean;
  asking?: boolean;
  step?: EpicStep;
  onToggleOpen?: () => void;
  onOlder?: () => void;
  onNewest?: (atNewest: boolean) => void;
  register?: (handle: ReaderHandle | null) => void;
  sending?: Sending[];
  find?: { query: string; matches: Match[]; current: number };
  contractCalls?: ContractCalls;
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

// ReaderLayout moves the stubbed layout the way a browser's would: the reader's box takes a new height, or a row takes
// one. report delivers those changes to the observers the reader made, which a browser does after a size change.
interface ReaderLayout {
  resizeBox(height: number): void;
  resizeRow(row: Element, height: number): void;
  report(): void;
}

// stubLayout gives the reader the layout a browser would: each row is rowPixels tall unless resizeRow says otherwise,
// the box is viewportPixels tall unless resizeBox says otherwise, and scrollTop clamps to the content. happy-dom has no
// layout, so the stub reads the rows the DOM holds.
function stubLayout(reader: HTMLElement): ReaderLayout {
  let top = 0;
  let box = viewportPixels;
  const rowHeights = new Map<Element, number>();
  const furthest = () => Math.max(0, reader.scrollHeight - box);
  Object.defineProperties(reader, {
    scrollHeight: {
      configurable: true,
      get: () => [...reader.children].reduce((sum, row) => sum + (rowHeights.get(row) ?? rowPixels), 0),
    },
    clientHeight: { configurable: true, get: () => box },
    scrollTop: {
      configurable: true,
      get: () => Math.min(top, furthest()),
      set: (value: number) => {
        top = Math.min(Math.max(value, 0), furthest());
      },
    },
  });
  return {
    resizeBox: (height) => {
      box = height;
    },
    resizeRow: (row, height) => {
      rowHeights.set(row, height);
    },
    report: () => {
      FakeResizeObserver.report();
    },
  };
}

// FakeResizeObserver stands in for the browser's ResizeObserver, which happy-dom stubs to report nothing. It keeps each
// observer the reader makes with the elements that observer watches, and report calls the callback of each one still
// connected.
class FakeResizeObserver {
  static made: FakeResizeObserver[] = [];

  readonly watching = new Set<Element>();
  connected = true;
  readonly callback: () => void;

  constructor(callback: () => void) {
    this.callback = callback;
    FakeResizeObserver.made.push(this);
  }

  observe(target: Element): void {
    this.watching.add(target);
  }

  disconnect(): void {
    this.watching.clear();
    this.connected = false;
  }

  static report(): void {
    for (const observer of FakeResizeObserver.made) {
      if (observer.connected) {
        observer.callback();
      }
    }
  }
}

// withScrollingReader mounts a reader the way the app does, from a null conversation to its conversation, with its
// layout stubbed and its observers faked. The check gets the container, a rerender that draws new props into the same
// mounted reader, and the layout.
async function withScrollingReader(
  props: Props,
  check: (container: HTMLElement, rerender: (next: Props) => Promise<void>, layout: ReaderLayout) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  (globalThis as unknown as Record<string, unknown>).ResizeObserver = FakeResizeObserver;
  FakeResizeObserver.made = [];
  const container = document.createElement("div");
  const rerender = (next: Props) =>
    act(() => {
      render(<Reader {...next} />, container);
    });
  try {
    render(<Reader {...props} conversation={null} />, container);
    const layout = stubLayout(readerOf(container));
    await rerender(props);
    await check(container, rerender, layout);
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

// localTime is the unix seconds of a wall-clock time on one local day, so the test reads the same in any time zone.
function localTime(hour: number, minute: number): bigint {
  return BigInt(Math.floor(new Date(2026, 10, 12, hour, minute).getTime() / 1000));
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

test("a gutter time is local 24-hour HH:MM, so 00:05 and 15:26 draw as 00:05 and 15:26", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "reply", time: localTime(0, 5), text: "after midnight" }),
          item({ kind: "reply", time: localTime(15, 26), text: "in the afternoon" }),
        ],
      }),
      // every item is read: none is newer than seenAt, so no new rule draws a gutter of its own
      seenAt: Number(localTime(23, 59)),
      open: false,
    },
    (container) => {
      const gutters = [...container.querySelectorAll(".reader > .t > .gut")].map((gut) => gut.textContent ?? "");
      assert.deepEqual(gutters, ["00:05", "15:26"]);
    },
  ));

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

test("a CallerEvent prompt in a session draws as an EVENT row with its text, and not as YOU", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "prompt", time: 100n, sender: "event", text: "**orrery** step finished" })],
      }),
      seenAt: 10_000,
      open: false,
    },
    (container) => {
      const event = container.querySelector<HTMLElement>(".t > .body > .event");
      assert.ok(event !== null, "the event is an event row");
      assert.equal(event.querySelector("small")?.textContent, "EVENT");
      assert.equal(event.querySelector(".reply > p > strong")?.textContent, "orrery", "the text draws as Markdown");
      assert.equal(event.textContent?.includes("step finished"), true, "the whole text is shown");
      assert.equal(container.querySelector(".you") === null, true, "the event is not YOU");
    },
  ));

test("a CallerEvent prompt in a step draws as an EVENT row, not as a step prompt or YOU", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "prompt", time: 100n, sender: "event", round: 1, text: "step finished: gate passed" })],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([{ round: 1, name: "gate", ok: false }]),
    },
    (container) => {
      const event = container.querySelector<HTMLElement>(".t > .body > .event");
      assert.ok(event !== null, "the event is an event row");
      assert.equal(event.querySelector("small")?.textContent, "EVENT");
      assert.equal(event.textContent?.includes("step finished: gate passed"), true, "the whole text is shown");
      assert.equal(container.querySelector(".you") === null, true, "the event is not YOU");
      assert.equal(container.querySelector("button.fold") === null, true, "the event is not an engine fold");
      assert.equal(container.querySelector(".card") === null, true, "the event is not a send-back card");
    },
  ));

test("CronDeliver: a sender cron prompt draws as a labelled CRON row", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "prompt", time: 100n, sender: "cron", text: "**orrery** check the build" })],
      }),
      seenAt: 10_000,
      open: false,
    },
    (container) => {
      const cron = container.querySelector<HTMLElement>(".t > .body > .cron");
      assert.ok(cron !== null, "the cron prompt is a cron row");
      assert.equal(cron.querySelector("small")?.textContent, "CRON");
      assert.equal(cron.querySelector(".reply > p > strong")?.textContent, "orrery", "the text draws as Markdown");
      assert.equal(cron.textContent?.includes("check the build"), true, "the whole text is shown");
      assertNone(container.querySelector(".you"), "the cron prompt is not YOU");
    },
  ));

test("CronDeliver: a sender cron prompt is not drawn as the owner's turn", async () => {
  const items = [
    item({ kind: "prompt", time: 100n, sender: "cron", text: "check the build" }),
    item({ kind: "prompt", time: 200n, sender: "owner", text: "fix it" }),
  ];
  for (const step of [undefined, stepWith([{ round: 1, name: "gate", ok: false }])]) {
    await withReader({ conversation: conversation({ items }), seenAt: 10_000, open: false, step }, (container) => {
      const [cron, owner] = container.querySelectorAll<HTMLElement>(".reader > .t");
      assert.ok(cron !== undefined && owner !== undefined, "both prompts are rows of the reader");
      assert.equal(cron.className, "t", "the cron row carries no owner-turn class");
      assertNone(cron.querySelector(".you"), "the cron row is not YOU");
      assert.equal(owner.classList.contains("owner-turn"), true, "the owner's row carries owner-turn");
      assert.equal(owner.querySelector(".you > small")?.textContent, "YOU", "the owner's prompt still draws as YOU");
    });
  }
});

// ringEpic is the epic the event blocks in the EventRing tests name.
const ringEpic = "orrery-1a2b3c4/planner-runner";

// eventText is the block an epic's event carries, as the server writes it.
function eventText(kind: string, tail: string): string {
  return `<orrery-event kind="${kind}" id="epic:${ringEpic}:${tail}">\nEpic ${ringEpic} (Plan the runner) is done.\n</orrery-event>`;
}

// ringNow is the instant the ring's epic calls hold, in unix seconds.
const ringNow = 1_800_000_000;

// ringCalls are the epic calls an event ring reads through, at ringNow. The epic reads with the questions given open;
// with none it reads as one with no open question, so a question event draws its settled line.
function ringCalls(questions: MessageInitShape<typeof EpicQuestionSchema>[] = []): EpicCalls {
  const reading = create(EpicsGetResponseSchema, {
    epic: create(EpicSchema, { id: ringEpic, slug: "planner-runner" }),
    questions: questions.map((question) => create(EpicQuestionSchema, question)),
  });
  return {
    get: async () => reading,
    pause: () => Promise.resolve(),
    resume: () => Promise.resolve(),
    answer: async () => "",
    openEpic: () => {},
    openStep: () => {},
    stepResult: () => Promise.resolve(""),
    now: ringNow,
    epics: noEpics,
  };
}

// withEventReader draws the reader over items with the ring's epic calls, with the questions given open, lets the ring's
// read settle inside act, runs check on the container, then removes the DOM.
async function withEventReader(
  items: ConversationItem[],
  check: (container: HTMLElement) => void,
  questions: MessageInitShape<typeof EpicQuestionSchema>[] = [],
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(
        <Reader
          conversation={conversation({ items })}
          seenAt={10_000}
          open={false}
          epicCalls={ringCalls(questions)}
        />,
        container,
      );
    });
    await act(turn);
    check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

test("EventRing: an orrery-event prompt of each kind draws one ringed line and no EVENT block", async () => {
  for (const [kind, tail] of [
    ["question", "Q1"],
    ["park", "Q1"],
    ["release", "released"],
    ["end", "done"],
  ]) {
    await withEventReader(
      [item({ kind: "prompt", time: 100n, sender: "event", text: eventText(kind, tail) })],
      (container) => {
        assert.equal(container.querySelectorAll(".ring-line").length, 1, `a ${kind} event draws one ringed line`);
        assertNone(container.querySelector(".event"), `a ${kind} event is not an EVENT row`);
        const labels = [...container.querySelectorAll("small")].map((small) => small.textContent);
        assert.equal(labels.includes("EVENT"), false, `a ${kind} event shows no EVENT label`);
      },
    );
  }
});

test("EventRing: a pane prompt with the same text draws the ring too, and its row takes no owner's-turn gap", () =>
  withEventReader(
    [item({ kind: "prompt", time: 100n, text: eventText("release", "released") })],
    (container) => {
      assert.equal(container.querySelectorAll(".ring-line").length, 1, "the pane prompt draws its ring");
      assertNone(container.querySelector(".you"), "the pane prompt is not YOU");
      assertNone(container.querySelector(".t.owner-turn"), "the ring's row is not the owner's turn");
    },
  ));

test("EventRing: an orrery-event prompt draws its EVENT row when the reader holds no epic calls", () =>
  withReader(
    {
      conversation: conversation({
        items: [item({ kind: "prompt", time: 100n, sender: "event", text: eventText("release", "released") })],
      }),
      seenAt: 10_000,
      open: false,
    },
    (container) => {
      assertNone(container.querySelector(".ring-line"), "without epic calls the event draws no ring");
      assert.equal(container.querySelector(".t > .body > .event > small")?.textContent, "EVENT");
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

test("the compaction rule flashes for 300 ms after a click and still toggles", async (t) => {
  mockTimers(t, ["setTimeout"]);
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

test("PinnedScroll: a reader at its bottom stays at its bottom as items arrive and as a sending row appears and goes", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), async (container, rerender) => {
    const items = heldItems(1, 21);
    const sending: Sending[] = [{ promptId: "p1", text: "fix the build", after: items[19] }];

    await rerender({ ...readerProps(heldItems(1, 20)), sending });
    assert.equal(distanceFromBottom(container), 0, "a sending row that appears keeps the reader at its bottom");

    await rerender({ ...readerProps(items), sending });
    assert.equal(distanceFromBottom(container), 0, "a new item that arrives keeps the reader at its bottom");

    await rerender(readerProps(items));
    assert.equal(distanceFromBottom(container), 0, "a sending row that goes keeps the reader at its bottom");
  }));

test("PinnedScroll: a reader at its bottom stays at its bottom when the composer area above it grows", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), (container, _rerender, layout) => {
    const reader = readerOf(container);
    assert.equal(distanceFromBottom(container), 0, "the reader starts at its bottom");

    layout.resizeBox(200);
    assert.equal(distanceFromBottom(container), 100, "the box shrank, so the bottom sits below the box");
    layout.report();

    assert.equal(distanceFromBottom(container), 0, "the observer returns the reader to its bottom");
    assert.equal(
      FakeResizeObserver.made.some((observer) => observer.connected && observer.watching.has(reader)),
      true,
      "the observer watches the reader's own box",
    );
  }));

test("PinnedScroll: a reader at its bottom stays at its bottom when a row grows without a render", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), (container, _rerender, layout) => {
    const row = rowWith(container, "held-20");

    layout.resizeRow(row, rowPixels + 60);
    assert.equal(distanceFromBottom(container), 60, "the grown row moved the bottom below the box");
    layout.report();

    assert.equal(distanceFromBottom(container), 0, "the observer returns the reader to its bottom");
  }));

test("PinnedScroll: a reader the owner scrolled up stays put as its box changes, and follows again once the owner scrolls back to the bottom", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), async (container, rerender, layout) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;
    reader.dispatchEvent(new Event("scroll"));

    layout.resizeBox(200);
    layout.report();
    assert.equal(reader.scrollTop, 500, "a size change leaves a reader the owner scrolled up where it is");

    await rerender(readerProps(heldItems(1, 21)));
    assert.equal(reader.scrollTop, 500, "new items leave it where it is too");

    reader.scrollTop = reader.scrollHeight;
    reader.dispatchEvent(new Event("scroll"));
    layout.resizeBox(150);
    layout.report();
    assert.equal(distanceFromBottom(container), 0, "back at the bottom, the reader follows its box again");
  }));

test("PinnedScroll: the reader's observers are disconnected when the reader unmounts", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), (container) => {
    assert.equal(
      FakeResizeObserver.made.some((observer) => observer.connected),
      true,
      "the reader holds a connected observer",
    );

    render(null, container);
    assert.equal(
      FakeResizeObserver.made.some((observer) => observer.connected),
      false,
      "no observer is left connected",
    );
  }));

test("PinnedScroll: a conversation that opens after a null one follows its items, though the owner scrolled the one before up", () =>
  withScrollingReader(readerProps(heldItems(1, 20)), async (container, rerender) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;
    reader.dispatchEvent(new Event("scroll"));

    await rerender({ ...readerProps([]), conversation: null });
    await rerender(readerProps(heldItems(1, 2)));
    await rerender(readerProps(heldItems(1, 6)));
    assert.equal(
      distanceFromBottom(container),
      0,
      "the row that opened follows its items as they outgrow the viewport",
    );
  }));

// newestReports is an onNewest that records each report the reader makes, in order.
function newestReports(): { reports: boolean[]; onNewest: (atNewest: boolean) => void } {
  const reports: boolean[] = [];
  return {
    reports,
    onNewest: (atNewest) => {
      reports.push(atNewest);
    },
  };
}

test("PhoneHead: a conversation that opens reports that the owner is at its newest line", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, () => {
    assert.deepEqual(newest.reports, [true]);
  });
});

test("PhoneHead: an owner scroll away from the bottom reports not at newest, and a further scroll away reports nothing", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, (container) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;
    reader.dispatchEvent(new Event("scroll"));
    assert.deepEqual(newest.reports, [true, false]);

    reader.scrollTop = 400;
    reader.dispatchEvent(new Event("scroll"));
    assert.deepEqual(newest.reports, [true, false], "a scroll that leaves the reader away from its bottom reports nothing");
  });
});

test("PhoneHead: scrolling back to the bottom reports at newest", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, (container) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;
    reader.dispatchEvent(new Event("scroll"));

    reader.scrollTop = reader.scrollHeight;
    reader.dispatchEvent(new Event("scroll"));
    assert.deepEqual(newest.reports, [true, false, true]);
  });
});

test("PhoneHead: a scroll that leaves the reader at its bottom reports nothing", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, (container) => {
    const reader = readerOf(container);
    reader.scrollTop = reader.scrollHeight;
    reader.dispatchEvent(new Event("scroll"));
    assert.deepEqual(newest.reports, [true]);
  });
});

test("PhoneHead: a render that grows the content reports nothing, at the bottom or above a reader the owner scrolled up", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, async (container, rerender) => {
    const reader = readerOf(container);
    await rerender({ ...readerProps(heldItems(1, 21)), onNewest: newest.onNewest });
    assert.deepEqual(newest.reports, [true], "a new item at the bottom reports nothing");

    reader.scrollTop = 500;
    reader.dispatchEvent(new Event("scroll"));
    await rerender({ ...readerProps(heldItems(1, 22)), onNewest: newest.onNewest });
    assert.deepEqual(newest.reports, [true, false], "a new item under a reader the owner scrolled up reports nothing");
  });
});

// stubPageScroll gives the window's document the layout of a page that scrolls as a whole: its content is contentPixels
// tall, the window's box is viewportPixels, and scrollTop clamps to the content. happy-dom has no layout, so the stub
// stands in for it. scrollTo moves the page and dispatches the window's scroll, as a browser's scroll does.
function stubPageScroll(contentPixels: number): { scrollTo: (top: number) => void } {
  const page = document.documentElement;
  let top = 0;
  const furthest = () => Math.max(0, contentPixels - viewportPixels);
  Object.defineProperties(page, {
    scrollHeight: { configurable: true, get: () => contentPixels },
    clientHeight: { configurable: true, get: () => viewportPixels },
    scrollTop: {
      configurable: true,
      get: () => Math.min(top, furthest()),
      set: (value: number) => {
        top = Math.min(Math.max(value, 0), furthest());
      },
    },
  });
  return {
    scrollTo: (value) => {
      page.scrollTop = value;
      window.dispatchEvent(new Event("scroll"));
    },
  };
}

test("PhoneHead: under 760 px a window scroll away from the page's end reports not at newest, and a further scroll away reports nothing", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, () => {
    window.innerWidth = 759;
    const page = stubPageScroll(1000);
    page.scrollTo(500);
    assert.deepEqual(newest.reports, [true, false]);

    page.scrollTo(400);
    assert.deepEqual(newest.reports, [true, false], "a scroll that leaves the page away from its end reports nothing");
  });
});

test("PhoneHead: under 760 px scrolling the window back to its end reports at newest", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, () => {
    window.innerWidth = 759;
    const page = stubPageScroll(1000);
    page.scrollTo(500);
    page.scrollTo(700);
    assert.deepEqual(newest.reports, [true, false, true]);
  });
});

test("PhoneHead: under 760 px a window scroll that leaves the page at its end reports nothing", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, () => {
    window.innerWidth = 759;
    stubPageScroll(1000).scrollTo(700);
    assert.deepEqual(newest.reports, [true]);
  });
});

test("PhoneHead: under 760 px the window's scroll listener is removed on unmount", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, (container) => {
    window.innerWidth = 759;
    const page = stubPageScroll(1000);
    render(null, container);
    page.scrollTo(500);
    assert.deepEqual(newest.reports, [true], "a window scroll after unmount reports nothing");
  });
});

test("PhoneHead: at 760 px and wider a window scroll reports nothing", () => {
  const newest = newestReports();
  return withScrollingReader({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, () => {
    window.innerWidth = 1024;
    stubPageScroll(1000).scrollTo(500);
    assert.deepEqual(newest.reports, [true], "the page does not report at 760 px and wider");
  });
});

test("PhoneHead: a page up from the bottom reports not at newest", () => {
  const holder: { handle: ReaderHandle | null } = { handle: null };
  const newest = newestReports();
  return withScrollingReader(
    pagedProps({ ...readerProps(heldItems(1, 20)), onNewest: newest.onNewest }, holder),
    () => {
      assert.equal(handleOf(holder).page("up"), true);
      assert.deepEqual(newest.reports, [true, false]);
    },
  );
});

// olderItems are count replies older than heldItems, each with its own text, so a page of them is drawn above the held
// rows.
function olderItems(count: number): ConversationItem[] {
  return Array.from({ length: count }, (_, index) =>
    item({ kind: "reply", time: BigInt(50 + index), text: `older-${index}` }),
  );
}

// withOlder is props with a conversation that holds items and may have older ones, asking onOlder on each call.
function withOlder(items: ConversationItem[], asked: { count: number }, more = true): Props {
  return {
    ...readerProps(items),
    conversation: conversation({ items, more, oldest: 50n }),
    onOlder: () => {
      asked.count += 1;
    },
  };
}

test("reaching the top of a reader whose conversation has older items calls onOlder", () => {
  const asked = { count: 0 };
  return withScrollingReader(withOlder(heldItems(1, 20), asked), (container) => {
    const reader = readerOf(container);
    assert.equal(asked.count, 0, "a reader opened at its bottom asks for nothing");

    reader.scrollTop = 0;
    reader.dispatchEvent(new Event("scroll"));
    assert.equal(asked.count, 1, "the reader at its top asks for the page before its oldest item");
  });
});

test("reaching the top of a reader whose conversation has no older items does not call onOlder", () => {
  const asked = { count: 0 };
  return withScrollingReader(withOlder(heldItems(1, 20), asked, false), (container) => {
    const reader = readerOf(container);
    reader.scrollTop = 0;
    reader.dispatchEvent(new Event("scroll"));
    assert.equal(asked.count, 0, "a conversation with nothing older asks for nothing");
  });
});

test("a reader that does not fill its height asks for its older items as it is drawn", () => {
  const asked = { count: 0 };
  return withScrollingReader(withOlder(heldItems(1, 2), asked), (container) => {
    assert.equal(readerOf(container).scrollTop, 0, "two rows fit the viewport, so the reader has no scroll");
    assert.equal(asked.count, 1, "the reader at its top, with nothing to scroll, asks for the page before");
  });
});

test("an older page drawn above keeps the row on screen where it was", () => {
  const held = heldItems(1, 20);
  const asked = { count: 0 };
  return withScrollingReader(withOlder(held, asked), async (container, rerender) => {
    const reader = readerOf(container);
    reader.scrollTop = 500;
    const visible = readerOf(container).children[5];
    assert.equal(visible.textContent?.includes("held-6"), true, "the row at the top of the view is held-6");

    await rerender({
      ...withOlder([...olderItems(10), ...held], asked),
    });
    assert.ok(readerOf(container).children[15] === visible, "the row moved down past the ten older rows");
    assert.equal(reader.scrollTop, 1500, "the reader moved by the height the older rows took above");
    assert.equal(asked.count, 0, "a reader moved away from its top asks for nothing");
  });
});

test("an older item equal to a held one keeps the held row's node", () => {
  const twin = item({ kind: "reply", time: 100n, text: "same" });
  return withScrollingReader(readerProps([twin]), async (container, rerender) => {
    const held = readerOf(container).children[0];

    await rerender(readerProps([twin, twin]));
    assert.ok(readerOf(container).children[1] === held, "the held row is still the row after the older twin");
  });
});

test("a window of 50 that drops its oldest item for a new one keeps a held row's node", () =>
  withScrollingReader(readerProps(heldItems(1, 50)), async (container, rerender) => {
    const held = rowWith(container, "held-25");

    await rerender(readerProps(heldItems(2, 51)));
    assert.equal(readerOf(container).querySelectorAll(".reader > .t").length, 50);
    assert.ok(rowWith(container, "held-25") === held, "the held row keeps its DOM node");
  }));

// pagedProps is props whose reader keeps its handle in holder while it is mounted, as the page does.
function pagedProps(props: Props, holder: { handle: ReaderHandle | null }): Props {
  return {
    ...props,
    register: (handle) => {
      holder.handle = handle;
    },
  };
}

// handleOf returns the handle the reader holds, which the test asserts is held while the reader is mounted.
function handleOf(holder: { handle: ReaderHandle | null }): ReaderHandle {
  const { handle } = holder;
  assert.ok(handle !== null, "the reader holds its handle while mounted");
  return handle;
}

// pageOverlap is the overlap reader.tsx's pageOverlapPixels keeps, so a page moves the reader by the box less this.
const pageOverlap = 40;

test("ReaderPaging: a page moves scrollTop by the box's height less the overlap in each direction, and holds at the top and bottom", () => {
  const holder: { handle: ReaderHandle | null } = { handle: null };
  return withScrollingReader(pagedProps(readerProps(heldItems(1, 20)), holder), (container) => {
    const reader = readerOf(container);
    const screen = viewportPixels - pageOverlap;
    reader.scrollTop = 0;

    assert.equal(handleOf(holder).page("down"), true);
    assert.equal(reader.scrollTop, screen, "a page down moves the reader by the box less the overlap");
    assert.equal(handleOf(holder).page("down"), true);
    assert.equal(reader.scrollTop, 2 * screen, "a second page down moves it the same again");
    assert.equal(handleOf(holder).page("up"), true);
    assert.equal(reader.scrollTop, screen, "a page up moves the reader back by the same");

    handleOf(holder).page("up");
    handleOf(holder).page("up");
    assert.equal(reader.scrollTop, 0, "a page up past the top stops at the top");

    reader.scrollTop = reader.scrollHeight - viewportPixels;
    handleOf(holder).page("down");
    assert.equal(distanceFromBottom(container), 0, "a page down past the bottom stops at the bottom");
  });
});

test("ReaderPaging: paging up from the bottom unpins the reader, so a later render and a size change keep its place", () => {
  const holder: { handle: ReaderHandle | null } = { handle: null };
  return withScrollingReader(pagedProps(readerProps(heldItems(1, 20)), holder), async (container, rerender, layout) => {
    const reader = readerOf(container);
    assert.equal(distanceFromBottom(container), 0, "the reader starts at its bottom");

    assert.equal(handleOf(holder).page("up"), true);
    const placed = reader.scrollTop;
    const bottom = reader.scrollHeight - viewportPixels;
    assert.equal(placed, bottom - (viewportPixels - pageOverlap), "the page moved the reader up from the bottom");

    await rerender(pagedProps(readerProps(heldItems(1, 21)), holder));
    assert.equal(reader.scrollTop, placed, "a render with a new item keeps the reader where the page left it");

    layout.resizeBox(200);
    layout.report();
    assert.equal(reader.scrollTop, placed, "a size change keeps it there too, since the page unpinned the reader");
  });
});

test("ReaderPaging: paging up to the top calls onOlder when older items exist", () => {
  const holder: { handle: ReaderHandle | null } = { handle: null };
  const asked = { count: 0 };
  return withScrollingReader(pagedProps(withOlder(heldItems(1, 20), asked), holder), (container) => {
    const reader = readerOf(container);
    reader.scrollTop = viewportPixels;

    assert.equal(handleOf(holder).page("up"), true);
    assert.equal(asked.count, 0, "a page that stops short of the top asks for nothing");

    assert.equal(handleOf(holder).page("up"), true);
    assert.equal(reader.scrollTop, 0, "the second page reaches the top");
    assert.equal(asked.count, 1, "the reader at its top asks for the page before its oldest item");
  });
});

test("ReaderPaging: under 760 px a page does nothing and returns false, leaving scrollTop alone", () => {
  const holder: { handle: ReaderHandle | null } = { handle: null };
  return withScrollingReader(pagedProps(readerProps(heldItems(1, 20)), holder), (container) => {
    const reader = readerOf(container);
    reader.scrollTop = 900;
    window.innerWidth = 759;

    assert.equal(handleOf(holder).page("up"), false, "a page up under 760 px is left to the browser");
    assert.equal(handleOf(holder).page("down"), false, "a page down under 760 px is left to the browser");
    assert.equal(reader.scrollTop, 900, "neither page moved the reader");
  });
});

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

// dotClasses returns the class of each dot in a run's fold, in call order: "dot ok" for a call that succeeded, "dot
// failed" when its result failed, "dot running" while it runs, and "dot none" when it has no result and is not running.
function dotClasses(fold: HTMLElement): string[] {
  return [...fold.querySelectorAll<HTMLElement>(".trail .dot")].map((dot) => dot.className);
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

test("ToolTrail: three tool calls fold to one line with a dot per call, which opens to a line per call and folds again", () =>
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
      assert.deepEqual(dotClasses(fold), ["dot ok", "dot ok", "dot ok"], "one dot per call, each ok, as all succeeded");
      assert.doesNotMatch(fold.textContent ?? "", /[✓✗]/, "the fold draws no ✓ or ✗ of its own");
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

test("ToolTrail: a run of three with one failed result marks that call's dot failed, and its fold draws no ✓ or ✗", () =>
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
      assert.deepEqual(dotClasses(fold), ["dot failed", "dot ok", "dot ok"], "the failed call's dot is marked failed");
      assert.doesNotMatch(fold.textContent ?? "", /[✓✗]/, "the fold draws no ✓ or ✗ of its own");
      assert.equal(container.textContent?.includes("permission denied"), false, "the failure stays folded");
    },
  ));

test("CallStatus: a run of a succeeded call, a failed call and a call with no result, followed by a reply, draws its dots ok, failed and none in call order", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Bash", "b1", "rm -rf build"),
          resultItem("b1", "permission denied", true),
          toolItem("Bash", "b2", "make"),
          item({ kind: "reply", time: 100n, text: "the build did not run" }),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      assert.deepEqual(dotClasses(foldOf(container)), ["dot ok", "dot failed", "dot none"]);
    },
  ));

test("ToolTrail: a click opens a failed run to a line per call, and the failed call's line keeps its ✗", () =>
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
    async (container) => {
      await clickControl(foldOf(container));
      assert.deepEqual(openedCalls(container), ["Bash rm -rf build", "Read a.txt", "Bash make"], "a line per call");
      const lines = [...container.querySelectorAll<HTMLElement>("div.calls .call")];
      assert.equal(lines[0].querySelector(".fail")?.textContent, "✗", "the failed call's line keeps its ✗");
      assert.equal(container.querySelectorAll("div.calls .fail").length, 1, "only the failed line is marked");
    },
  ));

test("ToolTrail: a tool call with no result, after nothing but calls and results, draws running with its rail and mark", () =>
  withReader(
    {
      conversation: conversation({
        items: [toolItem("Read", "r1", "a.txt"), resultItem("r1", "alpha"), toolItem("Bash", "b1", "make")],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      const line = rowWith(container, "make").querySelector<HTMLElement>(".call");
      assert.ok(line !== null, "the call draws a line");
      assert.equal(line.classList.contains("running"), true, "the line carries the running class");
      assert.equal(line.querySelector(".running-mark")?.textContent, "running", "the line carries the running mark");
    },
  ));

test("CallStatus: a run folded around a running call carries the running class and mark, and its dots draw ok, failed and running in call order", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Bash", "b1", "rm -rf build"),
          resultItem("b1", "permission denied", true),
          toolItem("Bash", "b2", "make"),
        ],
      }),
      seenAt: 100,
      open: false,
    },
    (container) => {
      const fold = foldOf(container);
      assert.equal(fold.classList.contains("running"), true, "the fold carries the running class");
      assert.equal(fold.querySelector(".running-mark")?.textContent, "running", "the fold carries the running mark");
      assert.deepEqual(dotClasses(fold), ["dot ok", "dot failed", "dot running"]);
    },
  ));

test("ToolTrail: the same items drawn with a question carry no running class or mark", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          toolItem("Read", "r1", "a.txt"),
          resultItem("r1", "alpha"),
          toolItem("Read", "r2", "b.txt"),
          resultItem("r2", "beta"),
          toolItem("Bash", "b1", "make"),
        ],
      }),
      seenAt: 100,
      open: false,
      asking: true,
    },
    (container) => {
      const fold = foldOf(container);
      assert.equal(fold.classList.contains("running"), false, "the fold carries no running class");
      assertNone(fold.querySelector(".running-mark"), "the fold carries no running mark");
      assertNone(container.querySelector(".running"), "no line of the reader is running");
      assertNone(container.querySelector(".running-mark"));
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

// withTitleParts renders a title's parts alone in a container, and removes them the same way withHead does.
async function withTitleParts(dir: string, line: string, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<TitleParts dir={dir} line={line} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("TitleParts: a title draws the directory's last element, then the line, and no id or hash", () =>
  withTitleParts("/work/synthetic/app", "Synthetic summary", (container) => {
    assert.equal(container.querySelector(".title-dir")?.textContent, "app", "the directory's last element");
    assert.equal(container.querySelector(".title-line")?.textContent, "Synthetic summary", "the line");
    assert.equal(container.textContent, "app Synthetic summary", "the title holds nothing else, no id or hash");
  }));

test("TitleParts: a title with an empty line draws its directory alone, with no line element", () =>
  withTitleParts("/work/synthetic-app", "", (container) => {
    assert.equal(container.textContent, "synthetic-app");
    assert.equal(container.querySelector(".title-line") === null, true);
  }));

test("TitleParts: markup in a line draws as text, with no element", () =>
  withTitleParts("/work/synthetic-app", "<b>bold</b> <img src=x onerror=alert(1)>", (container) => {
    assert.equal(container.querySelector("b") === null, true);
    assert.equal(container.querySelector("img") === null, true);
    assert.equal(container.querySelector(".title-line")?.textContent, "<b>bold</b> <img src=x onerror=alert(1)>");
  }));

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

// withRerenderableReader mounts a reader and gives the check a rerender that draws new props into the same mounted
// reader, as the page does when a stored prompt arrives.
async function withRerenderableReader(
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
    await rerender(props);
    await check(container, rerender);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("a sending entry draws a dimmed row marked sending after the items, and the stored prompt makes it an ordinary row", async () => {
  const items = [
    item({ kind: "prompt", time: 100n, text: "earlier", promptId: "p0" }),
    item({ kind: "reply", time: 101n, text: "Done." }),
  ];
  const sending: Sending[] = [{ promptId: "p1", text: "fix the build", after: items[1] }];
  await withRerenderableReader(
    { conversation: conversation({ items }), seenAt: 1000, open: false, sending },
    async (container, rerender) => {
      const rows = container.querySelectorAll<HTMLElement>(".reader > .t");
      assert.equal(rows.length, 3, "the two items, then the sending row");
      const last = rows[2];
      assert.equal(last.classList.contains("sending-row"), true, "the sending row is dimmed");
      assert.equal(last.querySelector(".gut")?.textContent, "sending", "the row is marked sending");
      assert.equal(last.querySelector(".you")?.textContent, "YOUfix the build");

      await rerender({
        conversation: conversation({
          items: [...items, item({ kind: "prompt", time: 102n, text: "fix the build", promptId: "p1" })],
        }),
        seenAt: 1000,
        open: false,
        sending: [],
      });
      const after = container.querySelectorAll<HTMLElement>(".reader > .t");
      assert.equal(after.length, 3, "the stored prompt takes the sending row's place");
      assert.equal(after[2].classList.contains("sending-row"), false, "the row is no longer dimmed");
      assert.equal(after[2].querySelector(".you")?.textContent, "YOUfix the build");
      assertNone(container.querySelector(".gut.sending"), "nothing is marked sending");
    },
  );
});

test("TurnRhythm: a reply is drawn with no rail or surface class on its row, its body or inside them", () =>
  withReader(
    {
      conversation: conversation({ items: [item({ kind: "reply", time: 100n, text: "a reply" })] }),
      seenAt: 10_000,
      open: false,
    },
    (container) => {
      const row = container.querySelector<HTMLElement>(".reader > .t");
      assert.ok(row !== null, "the reply is a row of the reader");
      assert.equal(row.className, "t", "the row carries no owner-turn or surface class");
      assert.equal(row.querySelector(".body")?.className, "body", "the body carries no rail or surface class");
      assertNone(row.querySelector(".you, .card, .fold, .summary"), "no surface inside");
      assert.ok(row.querySelector(".reply") !== null, "the text draws as a reply");
    },
  ));

test("TurnRhythm: an owner prompt's row carries owner-turn, and an engine prompt's row in a step does not", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "prompt", time: 100n, sender: "owner", text: "fix it" }),
          item({ kind: "prompt", time: 200n, sender: "engine", round: 0, text: "engine text" }),
        ],
      }),
      seenAt: 10_000,
      open: false,
      step: stepWith([]),
    },
    (container) => {
      const [owner, engine] = container.querySelectorAll<HTMLElement>(".reader > .t");
      assert.ok(owner !== undefined && engine !== undefined, "both prompts are rows of the reader");
      assert.equal(owner.classList.contains("owner-turn"), true, "the owner's row carries owner-turn");
      assert.ok(owner.querySelector(".you") !== null, "the owner's row draws YOU");
      assert.equal(engine.className, "t", "the engine's row carries no owner-turn class");
    },
  ));

test("TurnRhythm: the compaction rule and the since-you-looked rule are rows, direct children of .reader", () =>
  withReader(
    {
      conversation: conversation({
        items: [
          item({ kind: "compaction", time: 100n, text: "Synthetic summary.", trigger: "auto", tokens: 611000n }),
          item({ kind: "reply", time: 200n, text: "after the rules" }),
        ],
      }),
      seenAt: 150,
      open: false,
    },
    (container) => {
      const reader = readerOf(container);
      const compaction = container.querySelector(".compaction")?.closest(".t");
      const since = container.querySelector(".rule.new")?.closest(".t");
      assert.ok(compaction !== null && compaction !== undefined, "the compaction rule is in a row");
      assert.ok(since !== null && since !== undefined, "the since-you-looked rule is in a row");
      assertSame(compaction.parentElement, reader, "the compaction row is a direct child of .reader");
      assertSame(since.parentElement, reader, "the since-you-looked row is a direct child of .reader");
    },
  ));

// findProps is the find prop of a reader searching query over items, with current as the index of the match it marks.
function findProps(items: ConversationItem[], query: string, current: number): Props["find"] {
  return { query, matches: findMatches(items, query), current };
}

// recordScrolls replaces scrollIntoView on the test window's elements with one that records the element it is called
// on: happy-dom has no layout, so its own does nothing. The record holds the elements in the order they were scrolled.
function recordScrolls(t: TestContext): Element[] {
  const scrolled: Element[] = [];
  t.mock.method(HTMLElement.prototype, "scrollIntoView", function (this: Element) {
    scrolled.push(this);
  });
  return scrolled;
}

// currentMarkIndex returns the index, among the reader's marks in document order, of the one mark with class current.
// It fails unless exactly one mark is current.
function currentMarkIndex(container: HTMLElement): number {
  const current = [...container.querySelectorAll("mark")].filter((mark) => mark.classList.contains("current"));
  assert.equal(current.length, 1, "exactly one mark is current");
  return [...container.querySelectorAll("mark")].indexOf(current[0]);
}

test("TranscriptSearch: a find marks each case-insensitive occurrence in a reply, a prompt, a call's input and a notice, and no Markdown syntax", () => {
  const items = [
    item({
      kind: "reply",
      time: 100n,
      text: "Build the **build**, `BUILD` and [build log](https://example.test/build)",
    }),
    item({ kind: "prompt", time: 200n, sender: "owner", text: "run the build" }),
    callItem("c1", "make build", 300n),
    item({ kind: "notice", time: 400n, notice: "build", text: "" }),
  ];
  return withReader(
    { conversation: conversation({ items }), seenAt: 1000, open: false, find: findProps(items, "build", -1) },
    (container) => {
      const marks = [...container.querySelectorAll("mark")].map((mark) => mark.textContent);
      assert.deepEqual(marks, ["Build", "build", "BUILD", "build", "build", "build", "build"]);
      assert.ok(container.querySelector("strong mark") !== null, "a mark inside bold draws within the bold text");
      assert.ok(container.querySelector(".you mark") !== null, "the owner's prompt is marked as YOU");
      assert.equal(container.querySelector(".in mark")?.textContent, "build", "the call's input is marked");
    },
  );
});

test("TranscriptSearch: exactly one mark is current, and the current mark moves and is scrolled into view as current changes", async (t) => {
  const items = [item({ kind: "reply", time: 100n, text: "one build, two build, three build" })];
  const props: Props = {
    conversation: conversation({ items }),
    seenAt: 1000,
    open: false,
    find: findProps(items, "build", 0),
  };
  await withScrollingReader(props, async (container, rerender) => {
    const scrolled = recordScrolls(t);
    assert.equal(container.querySelectorAll("mark").length, 3);
    assert.equal(currentMarkIndex(container), 0, "the first mark is current");

    await rerender({ ...props, find: findProps(items, "build", 2) });
    assert.equal(currentMarkIndex(container), 2, "the third mark is current, and the first is no longer");
    assert.equal(
      scrolled.at(-1),
      container.querySelector("mark.current"),
      "the mark that became current is scrolled to",
    );
  });
});

test("TranscriptSearch: a current match in a call's hidden result opens the call and marks the match, and a match in a closed result has no mark", async () => {
  const items = [callItem("c1", "make"), resultItem("c1", "built output")];
  const props: Props = { conversation: conversation({ items }), seenAt: 1000, open: false };
  await withScrollingReader({ ...props, find: findProps(items, "output", -1) }, async (container, rerender) => {
    assert.equal(container.querySelector("mark"), null, "the result is hidden, so its match is not marked");
    assert.equal(container.querySelector(".result"), null, "the result is folded");

    await rerender({ ...props, find: findProps(items, "output", 0) });
    const shown = container.querySelector(".result mark.current");
    assert.equal(shown?.textContent, "output", "the call opens and marks the match");
  });
});

test("TranscriptSearch: the owner can close a fold a find opened, and the find opens it again once the match moves back in", async () => {
  const items = [callItem("c1", "make"), resultItem("c1", "built output")];
  const props: Props = { conversation: conversation({ items }), seenAt: 1000, open: false };
  await withScrollingReader({ ...props, find: findProps(items, "output", 0) }, async (container, rerender) => {
    assert.ok(container.querySelector(".result mark.current") !== null, "the find opens the call");

    await clickControl(foldLine(container));
    assert.equal(container.querySelector(".result"), null, "the owner closes the result");

    await rerender({ ...props, find: findProps(items, "output", 0) });
    assert.equal(container.querySelector(".result"), null, "the match is still current, and the result stays closed");

    await rerender({ ...props, find: findProps(items, "output", -1) });
    await rerender({ ...props, find: findProps(items, "output", 0) });
    assert.ok(container.querySelector(".result mark.current") !== null, "the match moving back in opens the result");
  });
});

test("TranscriptSearch: a step's fold line is drawn as written and never marked, and the engine fold's text is marked once it opens", async () => {
  const items = [
    item({ kind: "prompt", time: 100n, sender: "brief", text: "Goal: export the rota" }),
    item({ kind: "prompt", time: 200n, sender: "engine", text: "watchdog: the engine stalled\nsecond line" }),
  ];
  const props: Props = {
    conversation: conversation({ items }),
    seenAt: 10_000,
    open: false,
    step: stepWith([]),
    find: findProps(items, "engine", -1),
  };
  await withScrollingReader(props, async (container, rerender) => {
    assert.equal(container.querySelector(".fold-text"), null, "both folds are closed");
    assert.equal(
      container.querySelector(".fold.engine .names")?.textContent,
      "watchdog: the engine stalled",
      "the engine line is drawn as written",
    );
    assert.equal(container.querySelector("mark"), null, "a match in a closed fold is not marked, and no line is");

    await rerender({ ...props, find: findProps(items, "engine", 0) });
    assert.equal(container.querySelector(".fold.engine .names mark"), null, "the engine line is not marked");
    assert.equal(
      container.querySelector(".fold-text mark.current")?.textContent,
      "engine",
      "the engine fold opens and marks its match",
    );
    assert.equal(container.querySelectorAll("mark").length, 1, "the match in the fold's text is the only mark");
    assert.equal(container.querySelector(".fold.brief mark"), null, "the brief's line is not marked");
  });
});

test("TranscriptSearch: a current match in a call inside a folded tool run opens the run and marks the call's match", async () => {
  const items = [
    toolItem("Read", "r1", "a.txt"),
    resultItem("r1", "alpha"),
    toolItem("Read", "r2", "b.txt"),
    resultItem("r2", "beta"),
    toolItem("Bash", "b1", "make"),
    resultItem("b1", "built"),
  ];
  const props: Props = { conversation: conversation({ items }), seenAt: 1000, open: false };
  await withScrollingReader({ ...props, find: findProps(items, "make", -1) }, async (container, rerender) => {
    assert.equal(container.querySelector("div.calls"), null, "the run is folded");

    await rerender({ ...props, find: findProps(items, "make", 0) });
    assert.equal(container.querySelector("div.calls mark.current")?.textContent, "make", "the run opens and marks it");
    assert.equal(container.querySelector("div.calls .result"), null, "the call of the match stays folded");
  });
});

test("TranscriptSearch: a compaction's summary is not marked, though the query occurs in it", () => {
  const items = [
    item({ kind: "compaction", time: 100n, text: "Synthetic summary text.", trigger: "auto", tokens: 611000n }),
    item({ kind: "reply", time: 200n, text: "a summary of the work" }),
  ];
  return withReader(
    { conversation: conversation({ items }), seenAt: 1000, open: true, find: findProps(items, "summary", 0) },
    (container) => {
      const summary = container.querySelector(".summary")?.textContent;
      assert.ok(summary?.includes("Synthetic summary text."), "the summary is drawn");
      assert.equal(container.querySelector(".summary mark"), null, "the summary has no mark");
      assert.equal(container.querySelectorAll("mark").length, 1, "only the reply's occurrence is marked");
      assert.equal(container.querySelector(".reply mark.current")?.textContent, "summary");
    },
  );
});

test("TranscriptSearch: a current match after a list and a table is the mark at its place in the drawn text", async () => {
  const text = "- first item\n- second item\n\n| a | b |\n| --- | --- |\n| build | x |\n\nlast line build";
  const items = [item({ kind: "reply", time: 100n, text })];
  const matches = findMatches(items, "build");
  assert.equal(matches.length, 2, "the table's cell and the last line each hold a match");
  for (const [current, parent] of [
    [0, "TD"],
    [1, "P"],
  ] as const) {
    const find = { query: "build", matches, current };
    await withReader({ conversation: conversation({ items }), seenAt: 1000, open: false, find }, (container) => {
      assert.equal(container.querySelectorAll("mark").length, 2);
      const marked = container.querySelector("mark.current")?.parentElement?.tagName;
      assert.equal(marked, parent, `match ${current} is the current one`);
    });
  }
});

test("TranscriptSearch: a match that Markdown splits across elements is marked on each part, and only the first part is current", () => {
  const items = [item({ kind: "reply", time: 100n, text: "a *foo*bar" })];
  return withReader(
    { conversation: conversation({ items }), seenAt: 1000, open: false, find: findProps(items, "foobar", 0) },
    (container) => {
      const marks = [...container.querySelectorAll("mark")];
      assert.deepEqual(marks.map((mark) => mark.textContent), ["foo", "bar"]);
      const emphasized = container.querySelector("em mark.current");
      assert.equal(emphasized?.textContent, "foo", "the part inside the emphasis is current");
      assert.equal(marks[1].classList.contains("current"), false, "the part after it is not");
    },
  );
});

test("TranscriptSearch: an EVENT line draws no mark under a find query its text contains, while a reply in the same transcript is marked", () => {
  const items = [
    item({ kind: "prompt", time: 100n, sender: "event", text: "**orrery** build passed" }),
    item({ kind: "reply", time: 200n, text: "the build is green" }),
  ];
  return withReader(
    { conversation: conversation({ items }), seenAt: 1000, open: false, find: findProps(items, "build", 0) },
    (container) => {
      const event = container.querySelector<HTMLElement>(".event");
      assert.ok(event !== null, "the event is an EVENT line");
      assert.equal(event.textContent?.includes("build passed"), true, "the line draws its text");
      assert.equal(event.querySelector("mark"), null, "the EVENT line holds no mark");
      assert.equal(container.querySelectorAll("mark").length, 1, "only the reply's occurrence is marked");
      assert.equal(container.querySelector("mark.current")?.textContent, "build", "the reply's match is current");
    },
  );
});

test("TranscriptSearch: a reader drawn without a find, or with an empty query, has no mark", async () => {
  const items = [item({ kind: "reply", time: 100n, text: "build it" }), callItem("c1", "make build")];
  await withReader({ conversation: conversation({ items }), seenAt: 1000, open: false }, (container) => {
    assert.equal(container.querySelector("mark"), null, "a reader without find marks nothing");
  });
  await withReader(
    { conversation: conversation({ items }), seenAt: 1000, open: false, find: { query: "", matches: [], current: -1 } },
    (container) => {
      assert.equal(container.querySelector("mark"), null, "a reader with an empty query marks nothing");
    },
  );
});

// Render is one component render a RenderCounter saw: the component's name and the props it rendered with.
interface Render {
  name: string;
  props: Record<string, unknown>;
}

// RenderCounter records each component render made while it is in place, through the diffed option, which runs after
// each vnode is diffed. A component's own vnode is made by its parent's render, so a row its memo skips never has a
// vnode diffed and leaves no record. stop restores the option the counter found.
class RenderCounter {
  private readonly renders: Render[] = [];
  private readonly previous = options.diffed;

  constructor() {
    options.diffed = (vnode) => {
      this.previous?.(vnode);
      const { type } = vnode;
      if (typeof type === "function") {
        this.renders.push({ name: type.name, props: vnode.props });
      }
    };
  }

  // rendersOf returns the props of each render of the component named name, in render order.
  rendersOf(name: string): Record<string, unknown>[] {
    return this.renders.filter((render) => render.name === name).map((render) => render.props);
  }

  clear(): void {
    this.renders.length = 0;
  }

  stop(): void {
    options.diffed = this.previous;
  }
}

// withRowRenders is withRerenderableReader with a RenderCounter the check gets, stopped when the check ends.
async function withRowRenders(
  props: Props,
  check: (
    container: HTMLElement,
    rerender: (next: Props) => Promise<void>,
    counter: RenderCounter,
  ) => void | Promise<void>,
): Promise<void> {
  const counter = new RenderCounter();
  try {
    await withRerenderableReader(props, (container, rerender) => check(container, rerender, counter));
  } finally {
    counter.stop();
  }
}

// itemsRendered returns the item of each ItemRow the counter saw render, in render order.
function itemsRendered(counter: RenderCounter): unknown[] {
  return counter.rendersOf("ItemRow").map((props) => props.item);
}

test("RenderOnce: re-rendering the reader with the same conversation, including a run of three or more tool calls, and with fresh onToggleOpen and onOlder and a new but equal step, renders no ItemRow or ToolRun again", () => {
  const conv = conversation({
    items: [
      item({ kind: "reply", time: 100n, text: "before the run" }),
      toolItem("Read", "r1", "a.txt"),
      resultItem("r1", "alpha"),
      toolItem("Read", "r2", "b.txt"),
      resultItem("r2", "beta"),
      toolItem("Bash", "b1", "make"),
      resultItem("b1", "built"),
      item({ kind: "compaction", time: 150n, text: "Synthetic summary.", trigger: "auto", tokens: 611000n }),
      item({ kind: "prompt", time: 200n, sender: "engine", round: 1, text: "gate failed: exit 1" }),
    ],
  });
  const checks = [{ round: 1, name: "gate", ok: false }];
  return withRowRenders(
    {
      conversation: conv,
      seenAt: 10_000,
      open: false,
      step: stepWith(checks),
      onToggleOpen: () => {},
      onOlder: () => {},
    },
    async (container, rerender, counter) => {
      const calls: string[] = [];
      counter.clear();
      await rerender({
        conversation: conv,
        seenAt: 10_000,
        open: false,
        step: stepWith(checks),
        onToggleOpen: () => calls.push("toggle"),
        onOlder: () => calls.push("older"),
      });
      assert.equal(container.querySelectorAll(".fold.calls").length, 1, "the three-call run is drawn");
      assert.equal(counter.rendersOf("ItemRow").length, 0, "no item row renders again");
      assert.equal(counter.rendersOf("ToolRun").length, 0, "the three-call run does not render again");

      const rule = container.querySelector<HTMLButtonElement>("button.rule");
      assert.ok(rule !== null, "the compaction rule is a button, as it has a toggle");
      await clickControl(rule);
      assert.deepEqual(calls, ["toggle"], "the rule calls the onToggleOpen of the latest render");
    },
  );
});

test("RenderOnce: changing one item's result re-renders only that row, and its fold shows the new result", () => {
  const before = item({ kind: "reply", time: 100n, text: "before" });
  const call = toolItem("Read", "r1", "a.txt");
  const after = item({ kind: "reply", time: 300n, text: "after" });
  const alpha = resultItem("r1", "alpha");
  const beta = resultItem("r1", "beta");
  const props = (result: ConversationItem): Props => ({
    conversation: conversation({ items: [before, call, result, after] }),
    seenAt: 10_000,
    open: false,
  });
  return withRowRenders(props(alpha), async (container, rerender, counter) => {
    const line = container.querySelector<HTMLButtonElement>("button.call");
    assert.ok(line !== null, "a call with a result is a button");
    await clickControl(line);
    assert.equal(container.querySelector(".result")?.textContent, "alpha", "the fold shows the first result");

    counter.clear();
    await rerender(props(beta));
    const rendered = itemsRendered(counter);
    assert.equal(rendered.length, 1, "one row renders again");
    assert.equal(rendered[0], call, "the row is the call the changed result answers");
    assert.equal(container.querySelector(".result")?.textContent, "beta", "the fold shows the new result");
  });
});

test("RenderOnce: a running call re-renders its row when it gets its result, or when text follows it", () => {
  const before = item({ kind: "reply", time: 100n, text: "before" });
  const call = toolItem("Bash", "b1", "make");
  const built = resultItem("b1", "built");
  return withRowRenders(
    { conversation: conversation({ items: [before, call] }), seenAt: 10_000, open: false },
    async (container, rerender, counter) => {
      assert.ok(container.querySelector(".call.running") !== null, "a call with no result is running");

      // live text after the call ends the calls, so the call stops running though it has no result
      counter.clear();
      await rerender({
        conversation: conversation({ items: [before, call], unfinished: [unfinished("typing")] }),
        seenAt: 10_000,
        open: false,
      });
      assert.deepEqual(itemsRendered(counter), [call], "the call's row renders again as its running state changes");
      assert.equal(container.querySelector(".call.running"), null, "the call is no longer running");

      counter.clear();
      await rerender({
        conversation: conversation({ items: [before, call, built], unfinished: [unfinished("typing")] }),
        seenAt: 10_000,
        open: false,
      });
      const rendered = itemsRendered(counter);
      assert.equal(rendered.length, 1, "one row renders again");
      assert.equal(rendered[0], call, "the row is the call that got its result");
      assert.equal(container.querySelector(".running-mark"), null);
    },
  );
});

test("RenderOnce: toggling open re-renders the compaction row alone, and that row shows its summary", () => {
  const compaction = item({
    kind: "compaction",
    time: 100n,
    text: "Synthetic summary.",
    trigger: "auto",
    tokens: 611000n,
  });
  const reply = item({ kind: "reply", time: 200n, text: "after" });
  const conv = conversation({ items: [compaction, reply] });
  return withRowRenders({ conversation: conv, seenAt: 10_000, open: false }, async (container, rerender, counter) => {
    assert.equal(container.textContent?.includes("Synthetic summary."), false, "the summary is folded while closed");

    counter.clear();
    await rerender({ conversation: conv, seenAt: 10_000, open: true });
    const rendered = itemsRendered(counter);
    assert.equal(rendered.length, 1, "one row renders again");
    assert.equal(rendered[0], compaction, "the row is the compaction");
    assert.equal(container.querySelector(".compaction.open .summary")?.textContent, "Synthetic summary.");
  });
});

// readerCard returns the send-back card the reader draws.
function readerCard(container: HTMLElement): HTMLElement {
  const card = container.querySelector<HTMLElement>("div.card");
  assert.ok(card !== null, "a send-back card is drawn");
  return card;
}

test("RenderOnce: a step whose checks change re-renders its send-back card with the new chips", () => {
  const sendBack = item({ kind: "prompt", time: 300n, sender: "engine", round: 1, text: "gate failed: exit 1" });
  const conv = conversation({ items: [sendBack] });
  return withRowRenders(
    { conversation: conv, seenAt: 10_000, open: false, step: stepWith([{ round: 1, name: "gate", ok: true }]) },
    async (container, rerender, counter) => {
      assert.deepEqual(chipTexts(readerCard(container)), ["gate ✓"]);

      counter.clear();
      await rerender({
        conversation: conv,
        seenAt: 10_000,
        open: false,
        step: stepWith([{ round: 1, name: "gate", ok: false }]),
      });
      const rendered = itemsRendered(counter);
      assert.equal(rendered.length, 1, "one row renders again");
      assert.equal(rendered[0], sendBack, "the row is the send-back");
      const card = readerCard(container);
      assert.deepEqual(chipTexts(card), ["gate ✗"], "the chip shows the check as failed");
      assert.deepEqual(badChipTexts(card), ["gate ✗"]);
      assert.equal(card.classList.contains("bad"), true, "the card's edge is bad");
    },
  );
});

// ReadCounts counts the reads a render makes of items: each read of an item's id, and each scan of the items array by
// forEach or findIndex. itemKeys reads every id, resultsByCall and groupRows scan the array, and newIndex scans it.
interface ReadCounts {
  ids: number;
  scans: number;
}

// countScans wraps an items array so each scan of it by forEach or findIndex is counted in reads.
function countScans(items: ConversationItem[], reads: ReadCounts): ConversationItem[] {
  return new Proxy(items, {
    get(target, key, receiver) {
      if (key === "forEach" || key === "findIndex") {
        reads.scans += 1;
      }
      return Reflect.get(target, key, receiver);
    },
  });
}

// probeItems returns items for a conversation, with each id read counted in reads and the array's scans too.
function probeItems(items: ConversationItem[], reads: ReadCounts): ConversationItem[] {
  const wrapped = items.map(
    (entry) =>
      new Proxy(entry, {
        get(target, key, receiver) {
          if (key === "id") {
            reads.ids += 1;
          }
          return Reflect.get(target, key, receiver);
        },
      }),
  );
  return countScans(wrapped, reads);
}

test("RenderOnce: a redraw with the same items array derives no keys, results or rows again, and a new array does", () => {
  const reads: ReadCounts = { ids: 0, scans: 0 };
  const items = probeItems(
    [
      item({ kind: "reply", time: 100n, text: "before the run" }),
      toolItem("Read", "r1", "a.txt"),
      resultItem("r1", "alpha"),
      toolItem("Read", "r2", "b.txt"),
      resultItem("r2", "beta"),
      toolItem("Bash", "b1", "make"),
      resultItem("b1", "built"),
      item({ kind: "reply", time: 300n, text: "after the run" }),
    ],
    reads,
  );
  const conv = conversation({ items });
  const props = { conversation: conv, seenAt: 10_000, open: false, onToggleOpen: () => {} };
  return withRowRenders(props, async (_container, rerender, counter) => {
    reads.ids = 0;
    reads.scans = 0;
    counter.clear();
    await rerender({ conversation: conv, seenAt: 10_000, open: false, onToggleOpen: () => {} });
    assert.equal(reads.ids, 0, "a redraw with the same array reads no item's id");
    assert.equal(reads.scans, 0, "a redraw with the same array scans the array for no rows, results or new rule");
    assert.equal(counter.rendersOf("ItemRow").length, 0, "and renders no row");

    await rerender({ conversation: conversation({ items: countScans([...items], reads) }), seenAt: 10_000, open: false });
    assert.ok(reads.ids > 0, "a new array with the same items derives its keys again");
    assert.ok(reads.scans > 0, "a new array with the same items derives its rows and results again");
  });
});

test("RenderOnce: rendering 100 rows calls no Date.prototype.toLocaleTimeString and constructs no Intl.DateTimeFormat", async (t) => {
  const base = localTime(9, 30);
  const items = Array.from({ length: 100 }, (_, n) =>
    item({ kind: "reply", time: base + BigInt(n), text: `row ${n}` }),
  );
  const toLocale = t.mock.method(Date.prototype, "toLocaleTimeString");
  const RealDateTimeFormat = Intl.DateTimeFormat;
  let constructed = 0;
  Intl.DateTimeFormat = new Proxy(RealDateTimeFormat, {
    construct(target, args, newTarget) {
      constructed += 1;
      return Reflect.construct(target, args, newTarget);
    },
  });
  try {
    // every item is read, so no since-you-looked row draws among the 100
    const readAll = Number(localTime(23, 59));
    await withReader({ conversation: conversation({ items }), seenAt: readAll, open: false }, (container) => {
      const gutters = [...container.querySelectorAll(".reader > .t > .gut")].map((gut) => gut.textContent ?? "");
      assert.equal(gutters.length, 100, "100 rows are drawn");
      assert.equal(
        gutters.every((gut) => /^\d\d:\d\d$/.test(gut)),
        true,
        "each row's time is drawn as HH:MM",
      );
    });
  } finally {
    Intl.DateTimeFormat = RealDateTimeFormat;
  }
  assert.equal(toLocale.mock.callCount(), 0, "no row formats its time with toLocaleTimeString");
  assert.equal(constructed, 0, "no row constructs an Intl.DateTimeFormat");
});

// contractId is the contract the ContractLines tests write, as the fixture records name it.
const contractId = "tidepool/tide-alerts";

// listOf is the contracts a list holds, one for each record that holds a contract.
function listOf(...records: ContractsGetResponse[]): Contract[] {
  return records.flatMap((record) => (record.contract === undefined ? [] : [record.contract]));
}

// contractItem is a tool call that writes the contract under id, under the call id callId.
function contractItem(callId: string, id: string): ConversationItem {
  return item({ kind: "tool", time: 100n, tool: "contract_propose", input: id, id: callId, contract: id });
}

// contractCallsOf are the contract calls a reader draws its lines through, over contracts, and records each press in
// opened.
function contractCallsOf(contracts: Contract[], opened: string[]): ContractCalls {
  return { contracts, onOpen: (id) => opened.push(id) };
}

// withContractReader draws the reader over items with the contract calls, runs check on the container, then removes
// the DOM.
async function withContractReader(
  items: ConversationItem[],
  calls: ContractCalls | undefined,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(
        <Reader conversation={conversation({ items })} seenAt={10_000} open={false} contractCalls={calls} />,
        container,
      );
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// contractLineWith returns the contract line the reader draws, of the tone given, or null when there is none.
function contractLineWith(container: HTMLElement, tone: string): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>(`button.block.oneline.${tone}`);
}

test("ContractLines: a proposed contract's item draws the line with its waiting words, and opens it by id", () => {
  const opened: string[] = [];
  const contracts = listOf(tideProposed);
  const title = contracts[0]?.title ?? "";
  return withContractReader(
    [contractItem("call-1", contractId), resultItem("call-1", "proposed")],
    contractCallsOf(contracts, opened),
    async (container) => {
      const line = contractLineWith(container, "attention");
      assert.ok(line !== null, "a proposed contract draws its attention line");
      assert.equal(line.querySelector(".lbl")?.textContent, "Contract", "the line is labelled Contract");
      assert.equal(
        line.textContent,
        `Contract${title} · severe care · waits for you`,
        "the line says the waiting words",
      );
      assertNone(container.querySelector(".call"), "the contract's call row is not drawn");
      await clickControl(line);
      assert.deepEqual(opened, [contractId], "a press opens the contract by its id");
    },
  );
});

test("ContractLines: an accepted contract's item draws its accepted words", () => {
  const contracts = listOf(tideAccepted);
  const accepted = contracts[0];
  assert.ok(accepted !== undefined, "the accepted fixture holds its contract");
  return withContractReader(
    [contractItem("call-1", contractId), resultItem("call-1", "accepted")],
    contractCallsOf(contracts, []),
    (container) => {
      const line = contractLineWith(container, "ok");
      assert.ok(line !== null, "an accepted contract draws its ok line");
      assert.equal(
        line.querySelector(".body")?.textContent,
        `${accepted.title} · accepted ${clock(accepted.changed)} · severe care`,
        "the line says the accepted words",
      );
    },
  );
});

test("ContractLines: an item whose id the list does not hold draws the id alone", () =>
  withContractReader(
    [contractItem("call-1", contractId), resultItem("call-1", "proposed")],
    contractCallsOf([], []),
    (container) => {
      const line = contractLineWith(container, "quiet");
      assert.ok(line !== null, "a contract the list does not hold draws a quiet line");
      assert.equal(line.querySelector(".body")?.textContent, contractId, "the line draws the id alone");
    },
  ));

test("ContractLines: an error result, or a reader with no contract calls, draws the call row", async () => {
  await withContractReader(
    [contractItem("call-1", contractId), resultItem("call-1", "refused", true)],
    contractCallsOf(listOf(tideProposed), []),
    (container) => {
      assertNone(contractLineWith(container, "attention"), "an error result draws no contract line");
      assert.ok(container.querySelector(".call") !== null, "an error result draws its call row");
    },
  );
  await withContractReader(
    [contractItem("call-1", contractId), resultItem("call-1", "proposed")],
    undefined,
    (container) => {
      assertNone(container.querySelector("button.block"), "without contract calls no contract line is drawn");
      assert.ok(container.querySelector(".call") !== null, "without contract calls the item draws its call row");
    },
  );
});

test("ContractLines: a contract item is drawn as its line, not folded into a run of tool calls", async () => {
  const items = [
    callItem("a", "ls"),
    contractItem("c", contractId),
    resultItem("c", "proposed"),
    callItem("b", "pwd"),
  ];
  await withContractReader(items, contractCallsOf(listOf(tideProposed), []), (container) => {
    assertNone(container.querySelector("button.fold.calls"), "a contract item among two other calls folds no run");
    assert.ok(contractLineWith(container, "attention") !== null, "the contract item draws its line");
  });
  await withContractReader(items, undefined, (container) => {
    assert.ok(
      container.querySelector("button.fold.calls") !== null,
      "without contract calls the three calls fold as a run",
    );
  });
});

test("ContractLines: the line draws again when the list the reader holds changes", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  const items = [contractItem("call-1", contractId), resultItem("call-1", "proposed")];
  try {
    await act(() => {
      render(
        <Reader
          conversation={conversation({ items })}
          seenAt={10_000}
          open={false}
          contractCalls={contractCallsOf([], [])}
        />,
        container,
      );
    });
    assert.ok(contractLineWith(container, "quiet") !== null, "the list holds no contract, so the id alone is drawn");
    await act(() => {
      render(
        <Reader
          conversation={conversation({ items })}
          seenAt={10_000}
          open={false}
          contractCalls={contractCallsOf(listOf(tideProposed), [])}
        />,
        container,
      );
    });
    assert.ok(
      contractLineWith(container, "attention") !== null,
      "the list now holds the contract, so its line is drawn",
    );
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
});
