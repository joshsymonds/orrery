import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { FindBar, findMatches } from "./find";
import { ConversationItemSchema, type ConversationItem } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

// itemsOf returns the item index of each match of query in items, in order.
function itemsOf(items: ConversationItem[], query: string): number[] {
  return findMatches(items, query).map((match) => match.item);
}

test("Find: a query matches case-insensitively in drawn reply, prompt and tool-input text", () => {
  const items = [
    item({ kind: "reply", text: "The **Build** passed." }),
    item({ kind: "prompt", sender: "owner", text: "rerun the BUILD" }),
    item({ kind: "tool", tool: "Bash", input: "make build", id: "c1" }),
  ];
  assert.deepEqual(findMatches(items, "build"), [
    { item: 0, start: 4 },
    { item: 1, start: 10 },
    { item: 2, start: 5 },
  ]);
});

test("Find: a query does not match Markdown syntax, only the text Markdown draws", () => {
  const items = [item({ kind: "reply", text: "## Plan\n\nUse **bold**, `code` and [docs](https://example.test/guide)." })];
  assert.deepEqual(findMatches(items, "##"), []);
  assert.deepEqual(findMatches(items, "**"), []);
  assert.deepEqual(findMatches(items, "`"), []);
  assert.deepEqual(findMatches(items, "https"), []);
  assert.equal(findMatches(items, "bold").length, 1);
  assert.equal(findMatches(items, "docs").length, 1);
});

test("Find: a match inside a call's result is counted under the call, and a result that answers no call is not", () => {
  const items = [
    item({ kind: "reply", text: "looking" }),
    item({ kind: "tool", tool: "Bash", input: "ls", id: "c1" }),
    item({ kind: "result", id: "c1", text: "found Needle here" }),
    item({ kind: "result", id: "ghost", text: "needle" }),
  ];
  assert.deepEqual(itemsOf(items, "needle"), [1]);
});

test("Find: compaction and check-card items are not searched", () => {
  const items = [
    item({ kind: "compaction", text: "lint summary", trigger: "auto", tokens: 9000n }),
    item({ kind: "prompt", sender: "engine", round: 1, text: "lint failed" }),
    item({ kind: "prompt", sender: "engine", round: 0, text: "lint the brief" }),
  ];
  assert.deepEqual(itemsOf(items, "lint"), [2]);
});

test("Find: a prompt an event sent draws as an EVENT line and is not searched, while an owner's prompt with the same text matches", () => {
  const items = [
    item({ kind: "prompt", sender: "event", text: "build finished" }),
    item({ kind: "prompt", sender: "owner", text: "build finished" }),
  ];
  assert.deepEqual(findMatches(items, "build"), [{ item: 1, start: 0 }]);
});

test("Find: a prompt a cron job sent draws as a CRON row and is not searched, while an owner's prompt with the same text matches", () => {
  const items = [
    item({ kind: "prompt", sender: "cron", text: "build finished" }),
    item({ kind: "prompt", sender: "owner", text: "build finished" }),
  ];
  assert.deepEqual(findMatches(items, "build"), [{ item: 1, start: 0 }]);
});

test("Find: an empty query matches nothing", () => {
  assert.deepEqual(findMatches([item({ kind: "reply", text: "anything" })], ""), []);
});

interface BarProps {
  query: string;
  matches: number;
  current: number;
  onQuery: (query: string) => void;
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}

// withFindBar renders a bar into the document, so the keys it sees bubble as they do on the page, and checks it. The
// props it does not name take a bar with a query and three matches, the first current.
async function withFindBar(
  props: Partial<BarProps>,
  check: (input: HTMLInputElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const barProps: BarProps = {
    query: "build",
    matches: 3,
    current: 0,
    onQuery: () => {},
    onNext: () => {},
    onPrevious: () => {},
    onClose: () => {},
    ...props,
  };
  try {
    await act(() => {
      render(<FindBar {...barProps} />, container);
    });
    const input = container.querySelector("input");
    assert.ok(input, "the bar draws its input");
    await check(input);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// press dispatches a keydown on target inside act, as the browser does: it bubbles from the element to the document. It
// returns the event, so a test can read whether a listener prevented it.
async function press(target: Element, key: string, init: KeyboardEventInit = {}): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  await act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

// keysAtDocument records the key of each keydown that reaches the document, the page's own keydown listener's view.
function keysAtDocument(): string[] {
  const keys: string[] = [];
  document.addEventListener("keydown", (event) => {
    keys.push(event.key);
  });
  return keys;
}

test("Find: the bar takes focus, draws the search icon and its field, and shows the current match as 1 of N", async () => {
  await withFindBar({}, async (input) => {
    assert.equal(document.activeElement, input);
    assert.equal(input.placeholder, "Find in this conversation");
    assert.ok(input.closest(".find")?.querySelector("svg.icon"), "the bar draws the search icon");
    assert.equal(input.closest(".find")?.querySelector(".count")?.textContent, "1 of 3");
  });
});

test("Find: a query with no match says no matches, and an empty query shows no count", async () => {
  await withFindBar({ matches: 0 }, (input) => {
    assert.equal(input.closest(".find")?.querySelector(".count")?.textContent, "no matches");
  });
  await withFindBar({ query: "", matches: 0 }, (input) => {
    assert.equal(input.closest(".find")?.querySelector(".count"), null);
  });
});

test("Find: the close button calls close, as Escape does", async () => {
  let closed = 0;
  await withFindBar({ onClose: () => (closed += 1) }, async (input) => {
    const close = input.closest(".find")?.querySelector<HTMLButtonElement>('button[aria-label="Close find"]');
    assert.ok(close, "the bar draws a Close find button");
    await act(() => {
      close.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    });
    assert.equal(closed, 1);
  });
});

test("Find: typing in the bar passes the field's text to onQuery", async () => {
  const typed: string[] = [];
  await withFindBar({ onQuery: (query) => typed.push(query) }, async (input) => {
    await act(() => {
      input.value = "bu";
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    assert.deepEqual(typed, ["bu"]);
  });
});

test("Find: Enter calls next, Shift+Enter calls previous, and neither reaches the document", async () => {
  const moves: string[] = [];
  await withFindBar({ onNext: () => moves.push("next"), onPrevious: () => moves.push("previous") }, async (input) => {
    const reached = keysAtDocument();
    await press(input, "a");
    await press(input, "Enter");
    await press(input, "Enter", { shiftKey: true });
    assert.deepEqual(moves, ["next", "previous"]);
    assert.deepEqual(reached, ["a"], "only the key the bar does not handle reaches the document");
  });
});

test("Find: Escape calls close and does not reach a document keydown listener", async () => {
  let closed = 0;
  await withFindBar({ onClose: () => (closed += 1) }, async (input) => {
    const reached = keysAtDocument();
    await press(input, "a");
    await press(input, "Escape");
    assert.equal(closed, 1);
    assert.deepEqual(reached, ["a"], "Escape does not reach the document");
  });
});

// The App tests below render the App over a fake server, as escape.test.tsx does, and drive its find bar over a
// session's transcript. Each test's name carries the token Find.

const now = 1000;
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const withdrawPath = "/orrery.v1.Control/Withdraw";
const interruptPath = "/orrery.v1.Control/Interrupt";

// One request the fake server received. A stream call has a controller until it ends.
interface Call {
  path: string;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with an empty JSON body, and WatchConversation
// with a stream the test feeds through emit. Every call is recorded.
class FakeServer {
  readonly calls: Call[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    // A call's signal is the one the transport passed, so a stream that the App stops is not fed afterwards.
    const signal = init?.signal ?? request.signal;
    const path = new URL(request.url).pathname;
    if (!path.endsWith("/WatchConversation")) {
      this.calls.push({ path, controller: null });
      return json({});
    }
    const call: Call = { path, controller: null };
    this.calls.push(call);
    signal.addEventListener("abort", () => {
      call.controller = null;
    });
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        call.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }

  emit(call: Call, message: object): void {
    if (call.controller === null) {
      throw new Error(`the stream to ${call.path} has ended`);
    }
    call.controller.enqueue(envelope(0, message));
  }

  closeStreams(): void {
    for (const call of this.calls) {
      if (call.controller !== null) {
        call.controller.enqueue(envelope(2, {}));
        call.controller.close();
        call.controller = null;
      }
    }
  }
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// row is a synthetic row over the given fields. Its changed and seen times are equal, so it reads as seen and arms no
// seen dwell.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

const paneRow = row({ key: "a", label: "api", state: "working", changed: 300, seen: 300 });
const sessionRow = row({
  key: "s1",
  id: "s1",
  kind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  changed: 30,
  seen: 30,
});

// transcript is the session's first page. The query "build" finds three matches: the reply's and the owner's prompt's
// are drawn, and the third sits in the call of a run of three calls, which stays folded until the match moves into it.
const transcript = [
  { kind: "reply", text: "The **Build** passed." },
  { kind: "prompt", sender: "owner", text: "rerun the BUILD" },
  { kind: "tool", tool: "Read", id: "r1", input: "a.txt" },
  { kind: "result", id: "r1", text: "alpha" },
  { kind: "tool", tool: "Read", id: "r2", input: "b.txt" },
  { kind: "result", id: "r2", text: "beta" },
  { kind: "tool", tool: "Bash", id: "b1", input: "make build" },
  { kind: "result", id: "b1", text: "ok" },
  { kind: "reply", text: "all done" },
];

// until waits as escape.test.tsx's does: each round steps by turn inside act, so the renders it lets through apply. It
// reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// settle lets the work a call starts run, so a call the test expects to be absent has time to be made.
async function settle(): Promise<void> {
  for (let round = 0; round < 10; round++) {
    await act(turn);
  }
}

// withApp mounts the App over the rows and a fake server in a fresh DOM and runs check. The App is unmounted, its
// streams closed and the DOM removed when the test ends, or as soon as the test's signal aborts first.
async function withApp(
  signal: AbortSignal,
  rows: Row[],
  check: (container: HTMLElement, fake: FakeServer) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeServer();
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    fake.closeStreams();
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(<App rows={rows} now={now} clients={fake.clients} />, container);
    });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

// typeInto sets a field's text and fires its input event, as typing does.
async function typeInto(field: HTMLInputElement | HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    field.value = text;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// clickRow clicks the list's row with the key, as a pointer click on it does.
async function clickRow(container: HTMLElement, key: string): Promise<void> {
  const element = container.querySelector(`[data-row][data-key="${key}"]`);
  assert.ok(element, `the list draws the row ${key}`);
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// composer is the cursor row's reply box, the one that is drawn.
function composer(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) textarea");
  assert.ok(box, "the cursor's row has a reply box");
  return box;
}

// findBar is the find bar, a div: the head's find button also has the class find.
function findBar(container: HTMLElement): Element | null {
  return container.querySelector("div.find");
}

function findInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>(".find input");
  assert.ok(input, "the find bar is open");
  return input;
}

function countText(container: HTMLElement): string | null {
  return container.querySelector(".find .count")?.textContent ?? null;
}

// markTexts is the text of each mark the reader draws, in document order.
function markTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[aria-label="reader"] mark')].map((mark) => mark.textContent ?? "");
}

// openTranscript waits for the cursor's session stream to open, then sends the transcript as its first page, and waits
// until the reader draws it.
async function openTranscript(signal: AbortSignal, container: HTMLElement, fake: FakeServer): Promise<void> {
  await until(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
  fake.emit(fake.callsTo(sessionWatchPath)[0], { transcript: "s1.jsonl", seq: "1", reset: true, items: transcript });
  await until(
    signal,
    () => container.querySelector('[aria-label="reader"]')?.textContent?.includes("all done") === true,
    "the transcript",
  );
}

// openFind focuses the cursor row's reply box, which is empty, and presses / in it: the find bar opens over the
// transcript.
async function openFind(container: HTMLElement): Promise<HTMLInputElement> {
  await act(() => {
    composer(container).focus();
  });
  await press(composer(container), "/");
  return findInput(container);
}

test("Find: after a click on a session row, / in its focused empty reply box opens the find bar, the bar's input takes focus, and the box stays empty", (t) =>
  withApp(t.signal, [paneRow, sessionRow], async (container) => {
    await clickRow(container, sessionRow.key);
    assert.equal(document.activeElement, composer(container), "the click focuses the row's reply box");
    const event = await press(composer(container), "/");
    assert.equal(event.defaultPrevented, true, "the slash is not typed");
    assert.equal(document.activeElement, findInput(container), "the find bar's input has focus");
    assert.equal(composer(container).value, "", "the reply box stays empty");
  }));

test("Find: a query marks the case-insensitive matches in a reply, a prompt and a call's input, and the bar counts a match in a closed fold", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const input = await openFind(container);
    await typeInto(input, "build");
    assert.deepEqual(markTexts(container), ["Build", "BUILD"], "the reply's and the prompt's matches are marked");
    assert.equal(
      container.querySelector('[aria-label="reader"] mark.current')?.textContent,
      "Build",
      "the first match is current",
    );
    assert.equal(container.querySelector("div.calls"), null, "the run holding the third match stays folded");
    assert.equal(countText(container), "1 of 3", "the match in the closed run is counted");
  }));

test("Find: Enter moves the current match to the next one and Shift+Enter to the previous, and both wrap at their ends", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const input = await openFind(container);
    await typeInto(input, "build");
    assert.equal(countText(container), "1 of 3");
    await press(input, "Enter");
    assert.equal(countText(container), "2 of 3", "Enter goes to the next match");
    await press(input, "Enter");
    assert.equal(countText(container), "3 of 3");
    await press(input, "Enter");
    assert.equal(countText(container), "1 of 3", "Enter on the last match wraps to the first");
    await press(input, "Enter", { shiftKey: true });
    assert.equal(countText(container), "3 of 3", "Shift+Enter on the first match wraps to the last");
    await press(input, "Enter", { shiftKey: true });
    assert.equal(countText(container), "2 of 3", "Shift+Enter goes to the previous match");
    assert.equal(document.activeElement, input, "the bar keeps its focus");
  }));

test("Find: a current match in a call inside a folded tool run opens the run", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const input = await openFind(container);
    await typeInto(input, "build");
    assert.equal(container.querySelector("div.calls"), null, "the run is folded");
    await press(input, "Enter");
    await press(input, "Enter");
    assert.equal(countText(container), "3 of 3");
    assert.equal(
      container.querySelector('[aria-label="reader"] div.calls mark.current')?.textContent,
      "build",
      "the run opens and marks the call's match",
    );
  }));

test("Find: on a working session, Escape in the find bar closes it, removes every mark, focuses the empty reply box, and calls neither Control.Interrupt nor Control.Withdraw", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const input = await openFind(container);
    await typeInto(input, "build");
    assert.equal(markTexts(container).length, 2, "the query is marked before Escape");
    await press(input, "Escape");
    assert.equal(findBar(container), null, "the bar closes");
    assert.deepEqual(markTexts(container), [], "no mark is left");
    assert.equal(document.activeElement, composer(container), "the empty reply box has focus");
    assert.equal(composer(container).value, "", "the reply box stays empty");
    await settle();
    assert.equal(fake.callsTo(withdrawPath).length, 0, "Escape takes no queued message back");
    assert.equal(fake.callsTo(interruptPath).length, 0, "Escape interrupts nothing");
  }));

test("Find: / in a reply box with text is typed, so its keydown is not prevented and no find bar opens", (t) =>
  withApp(t.signal, [sessionRow], async (container) => {
    await act(() => {
      composer(container).focus();
    });
    await typeInto(composer(container), "draft");
    const event = await press(composer(container), "/");
    assert.equal(event.defaultPrevented, false, "the slash is left to be typed");
    assert.equal(findBar(container), null, "no find bar opens");
    assert.equal(composer(container).value, "draft", "the box keeps its text");
  }));

test("Find: a find bar closes when the cursor moves to another row, and does not reopen when the cursor returns", (t) =>
  withApp(t.signal, [paneRow, sessionRow], async (container) => {
    await clickRow(container, sessionRow.key);
    await openFind(container);
    await clickRow(container, paneRow.key);
    assert.equal(findBar(container), null, "the bar closes when the cursor leaves its row");
    await clickRow(container, sessionRow.key);
    assert.equal(findBar(container), null, "the bar does not reopen when the cursor returns to the row");
  }));

// readerOf is the cursor's reader, the element the transcript scrolls in on a wide page.
function readerOf(container: HTMLElement): HTMLElement {
  const reader = container.querySelector<HTMLElement>('[aria-label="reader"] .reader');
  assert.ok(reader, "the cursor draws its reader");
  return reader;
}

// layOut gives the reader a height the scroll tests can move in: 1000px of content in a 200px box, so a scroll of 300px
// is not at its bottom and the reader stops following its end, as an owner's scroll stops it.
function layOut(reader: HTMLElement): void {
  Object.defineProperties(reader, { scrollHeight: { value: 1000 }, clientHeight: { value: 200 } });
}

// scrollReader moves the reader to top and reports the owner's scroll to it.
async function scrollReader(reader: HTMLElement, top: number): Promise<void> {
  await act(() => {
    reader.scrollTop = top;
    reader.dispatchEvent(new Event("scroll"));
  });
}

// findAwayFromEnd opens the find bar over the transcript at a reader scrolled to 300px and a window scrolled to 250px,
// types the query, and moves both as the current match's scroll does while the bar is open.
async function findAwayFromEnd(
  signal: AbortSignal,
  container: HTMLElement,
  fake: FakeServer,
): Promise<{ input: HTMLInputElement; reader: HTMLElement }> {
  await openTranscript(signal, container, fake);
  const reader = readerOf(container);
  layOut(reader);
  await scrollReader(reader, 300);
  window.scrollTo(0, 250);
  const input = await openFind(container);
  await typeInto(input, "build");
  await scrollReader(reader, 600);
  window.scrollTo(0, 700);
  return { input, reader };
}

test("Find: Escape closes the bar and returns the reader and the page to the scroll position they had when find opened", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    const { input, reader } = await findAwayFromEnd(t.signal, container, fake);
    await press(input, "Escape");
    assert.equal(reader.scrollTop, 300, "the reader is back where find opened");
    assert.equal(window.scrollY, 250, "the page is back where find opened");
  }));

test("Find: Close find closes the bar and returns the reader and the page to the scroll position they had when find opened", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    const { reader } = await findAwayFromEnd(t.signal, container, fake);
    const close = container.querySelector<HTMLButtonElement>('button[aria-label="Close find"]');
    assert.ok(close, "the bar draws a Close find button");
    await act(() => {
      close.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    });
    assert.equal(findBar(container), null, "the bar closes");
    assert.deepEqual(markTexts(container), [], "no mark is left");
    assert.equal(document.activeElement, composer(container), "the empty reply box has focus");
    assert.equal(reader.scrollTop, 300, "the reader is back where find opened");
    assert.equal(window.scrollY, 250, "the page is back where find opened");
  }));

test("Find: the title row's search control opens the bar over the transcript, with its input focused", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const control = container.querySelector<HTMLButtonElement>('button[aria-label="Find in this conversation"]');
    assert.ok(control, "the title row draws the search control");
    await act(() => {
      control.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    });
    assert.equal(document.activeElement, findInput(container), "the bar's input has focus");
    await typeInto(findInput(container), "build");
    assert.equal(countText(container), "1 of 3", "the bar counts the matches in the transcript");
  }));

test("Find: the current mark is scrolled into view when the current match moves to it", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await openTranscript(t.signal, container, fake);
    const scrolled: Element[] = [];
    HTMLElement.prototype.scrollIntoView = function (this: HTMLElement) {
      scrolled.push(this);
    };
    const input = await openFind(container);
    await typeInto(input, "build");
    scrolled.length = 0;
    await press(input, "Enter");
    const current = container.querySelector('[aria-label="reader"] mark.current');
    assert.ok(current, "the second match is the current mark");
    assert.equal(scrolled.length, 1, "moving the current match scrolls one element");
    assert.ok(scrolled[0] === current, "the element scrolled into view is the current mark");
  }));
