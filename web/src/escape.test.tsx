import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { epicsView, type EpicsView } from "./epics";
import { EpicSchema, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// Escape over the fake transport: the App takes a working row's queued messages back and interrupts it, and a row that
// is waiting is never interrupted. Each test's name carries the token EscapeInterrupt.

const now = 1000;
const queuedText = "queued words";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const sayPath = "/orrery.v1.Control/Say";
const withdrawPath = "/orrery.v1.Control/Withdraw";
const interruptPath = "/orrery.v1.Control/Interrupt";
const pullPath = "/orrery.v1.Panes/PullQueued";
const sendKeysPath = "/orrery.v1.Panes/SendKeys";
const takeBackFailedText = "could not take the queued messages back";
const widgetsRepo = "widgets-5d0c1a7";

type Clients = ReturnType<typeof createClients>;

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  body: Record<string, unknown>;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with JSON, and WatchConversation with a stream the
// test feeds through emit. Every call is recorded, and a unary path answers with its queued reply, else an empty one.
class FakeServer {
  readonly calls: Call[] = [];
  // failNext holds the unary paths whose next call the server fails with a Connect error, once each.
  readonly failNext = new Set<string>();
  // replies queues the bodies a unary path answers with, one per call in order.
  readonly replies = new Map<string, object[]>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    // A call's signal is the one the transport passed, so a stream that the App stops is not fed afterwards.
    const signal = init?.signal ?? request.signal;
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    if (!path.endsWith("/WatchConversation")) {
      this.calls.push({ path, body: decodeUnary(bytes), controller: null });
      if (this.failNext.delete(path)) {
        return new Response(JSON.stringify({ code: "unavailable", message: "queue unreachable" }), {
          status: 503,
          headers: { "content-type": "application/json" },
        });
      }
      return json(this.replies.get(path)?.shift() ?? (path === pullPath ? { text: queuedText } : {}));
    }
    const call: Call = { path, body: decodeEnvelope(bytes), controller: null };
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

  readonly clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

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

function decodeUnary(bytes: Uint8Array): Record<string, unknown> {
  return bytes.byteLength === 0 ? {} : JSON.parse(new TextDecoder().decode(bytes));
}

// decodeEnvelope reads the one message of a streamed request: a flag byte, a 4-byte length, and the JSON payload.
function decodeEnvelope(bytes: Uint8Array): Record<string, unknown> {
  const length = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  return JSON.parse(new TextDecoder().decode(bytes.subarray(5, 5 + length)));
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
  archiveId: "s1",
  kind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  changed: 30,
  seen: 30,
});

// oneEpic is a live epic with two running steps, and no detail.
function oneEpic(): EpicsView {
  const step = (id: string) => create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false });
  const epic = create(EpicSchema, {
    id: `${widgetsRepo}/first`,
    repository: widgetsRepo,
    slug: "first",
    outcome: "",
    live: true,
    runningSteps: [step("s1"), step("s2")],
  });
  return epicsView([epic], new Map());
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

async function pressIn(element: Element, key: string): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

async function typeInto(box: HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// until waits as app.test.tsx's untilTurn does: each round steps by turn inside act, so the renders it lets through
// apply. It reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// withApp mounts the App over a fake server in a fresh DOM and runs check. The App is unmounted, its streams closed and
// the DOM removed when the test ends, or as soon as the test's signal aborts first.
async function withApp(
  signal: AbortSignal,
  props: { rows: Row[]; epics?: EpicsView },
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
      render(<App rows={props.rows} error="" now={now} clients={fake.clients} epics={props.epics} />, container);
    });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

function composer(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) .composer textarea");
  assert.ok(box, "the reader has a composer");
  return box;
}

// headWord is the word of the pill the reader's head draws for the cursor's row.
function headWord(container: HTMLElement): string {
  return container.querySelector('[aria-label="reader"] .pill .word')?.textContent ?? "";
}

function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
}

// queueTexts is the text of each message in the cursor's queue above its box, oldest first, without its tag.
function queueTexts(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('[aria-label="reader"] .send .queue li .text'),
    (text) => text.textContent ?? "",
  );
}

function sendingRowCount(container: HTMLElement): number {
  return container.querySelectorAll('[aria-label="reader"] .sending-row').length;
}

function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

// emitQueued waits for the cursor's session stream to open, then sends a delta whose queued set holds each message as
// queued, and waits until the cursor's queue draws each one.
async function emitQueued(
  signal: AbortSignal,
  container: HTMLElement,
  fake: FakeServer,
  queued: { promptId: string; text: string }[],
): Promise<void> {
  await until(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
  fake.emit(fake.callsTo(sessionWatchPath)[0], {
    transcript: "s1.jsonl",
    seq: "1",
    reset: true,
    items: [],
    queued: queued.map((message) => ({ ...message, state: "queued" })),
  });
  await until(signal, () => queueTexts(container).length === queued.length, "the queued messages' rows");
}

// sendFromComposer types text into the cursor's composer and sends it with Enter, then waits for its Say and its
// sending row. It returns the prompt id the Say carried.
async function sendFromComposer(
  signal: AbortSignal,
  container: HTMLElement,
  fake: FakeServer,
  text: string,
): Promise<string> {
  await press("i");
  await typeInto(composer(container), text);
  await pressIn(composer(container), "Enter");
  await until(signal, () => fake.callsTo(sayPath).length === 1, "the say");
  await until(signal, () => sendingRowCount(container) === 1, "the sending row");
  return String(fake.callsTo(sayPath)[0].body.promptId);
}

test("EscapeInterrupt: Escape on a working headless session withdraws its held messages into the composer in order, before it calls Control.Interrupt", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await until(t.signal, () => composer(container).value === "first\n\nsecond", "the returned messages");
    const [interrupt] = fake.callsTo(interruptPath);
    assert.equal(fake.callsTo(withdrawPath).length, 0, "the session's messages are taken back by the interrupt, not a withdraw");
    assert.deepEqual(interrupt.body, { id: "s1" });
  }));

test("EscapeInterrupt: Escape on a working pane pulls its queued input into the composer, then sends the Escape key", (t) =>
  withApp(t.signal, { rows: [paneRow] }, async (container, fake) => {
    await press("Escape");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the Escape key");
    const [pull] = fake.callsTo(pullPath);
    const [sendKeys] = fake.callsTo(sendKeysPath);
    assert.ok(fake.calls.indexOf(pull) < fake.calls.indexOf(sendKeys), "the pull came before the key");
    assert.deepEqual(sendKeys.body, { pane: "a", keys: ["Escape"] });
    assert.equal(composer(container).value, queuedText);
  }));

test("EscapeInterrupt: Escape on a waiting session calls no Control.Interrupt", (t) =>
  withApp(t.signal, { rows: [{ ...sessionRow, state: "waiting" }] }, async (container, fake) => {
    await press("i");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => document.activeElement !== composer(container), "the blur");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "a session that is not working asks for no withdraw");
    assert.equal(fake.callsTo(interruptPath).length, 0, "a waiting session is not interrupted");
  }));

test("EscapeInterrupt: Escape on a waiting session marks no interrupt, so the session later working reads working", (t) =>
  withApp(t.signal, { rows: [{ ...sessionRow, state: "waiting" }] }, async (container, fake) => {
    await press("i");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => document.activeElement !== composer(container), "the blur");
    await act(() => {
      render(<App rows={[sessionRow]} error="" now={now} clients={fake.clients} />, container);
    });
    assert.equal(headWord(container), "working");
  }));

test("EscapeInterrupt: Escape on a working pane marks no interrupt, so its pill still reads working", (t) =>
  withApp(t.signal, { rows: [paneRow] }, async (container, fake) => {
    await press("Escape");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the Escape key");
    assert.equal(headWord(container), "working");
  }));

test("EscapeInterrupt: Escape in a step's box calls neither Control.Interrupt nor Control.Withdraw", (t) =>
  withApp(t.signal, { rows: [], epics: oneEpic() }, async (container, fake) => {
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement === box, true);
    await pressIn(box, "Escape");
    assert.equal(document.activeElement === box, false, "Escape leaves the step's box");
    assert.equal(fake.callsTo(withdrawPath).length, 0);
    assert.equal(fake.callsTo(interruptPath).length, 0);
  }));

test("EscapeInterrupt: composer text already present is kept ahead of the returned messages", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await press("i");
    await typeInto(composer(container), "draft");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => composer(container).value !== "draft", "the returned messages");
    assert.equal(composer(container).value, "draft\n\nfirst\n\nsecond");
  }));

test("EscapeInterrupt: a held message from the stream draws one queued row and no sending row", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    const promptId = await sendFromComposer(t.signal, container, fake, "hello");
    assert.equal(queueTexts(container).length, 1, "the sent message is queued while it is sent");
    await act(() => {
      fake.emit(fake.callsTo(sessionWatchPath)[0], {
        transcript: "s1.jsonl",
        seq: "1",
        reset: true,
        items: [],
        queued: [{ promptId, text: "hello", state: "queued" }],
      });
    });
    await until(t.signal, () => sendingRowCount(container) === 0, "the sending row's end");
    assert.deepEqual(queueTexts(container), ["hello"], "the held message draws one queued row");
    assert.equal(sendingRowCount(container), 0, "the held message draws no sending row");
  }));

test("EscapeInterrupt: after Escape the returned messages leave no sending or queued row", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["hello"] }]);
    const promptId = await sendFromComposer(t.signal, container, fake, "hello");
    await act(() => {
      fake.emit(fake.callsTo(sessionWatchPath)[0], {
        transcript: "s1.jsonl",
        seq: "1",
        reset: true,
        items: [],
        queued: [{ promptId, text: "hello", state: "queued" }],
      });
    });
    await until(t.signal, () => sendingRowCount(container) === 0, "the sending row's end");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await until(t.signal, () => composer(container).value === "hello", "the returned message");
    await act(() => {
      fake.emit(fake.callsTo(sessionWatchPath)[0], { transcript: "s1.jsonl", seq: "2", items: [], queued: [] });
    });
    await until(t.signal, () => queueTexts(container).length === 0, "the queued row's end");
    assert.equal(composer(container).value, "hello", "the message is back in the composer");
    assert.deepEqual(queueTexts(container), [], "no queued row is left");
    assert.equal(sendingRowCount(container), 0, "no sending row is left");
  }));

test("EscapeInterrupt: a message sent but not yet held leaves no sending or queued row after Escape", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["hello"] }]);
    await sendFromComposer(t.signal, container, fake, "hello");
    assert.deepEqual(queueTexts(container), ["hello"], "the sent message is queued while it is sent");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await until(t.signal, () => composer(container).value === "hello", "the returned message");
    assert.equal(composer(container).value, "hello", "the message is back in the composer");
    assert.deepEqual(queueTexts(container), [], "no queued row is left");
    assert.equal(sendingRowCount(container), 0, "no sending row is left");
  }));

test("EscapeInterrupt: a failed withdraw shows an error line, and the interrupt still goes out", (t) =>
  withApp(t.signal, { rows: [paneRow] }, async (container, fake) => {
    fake.failNext.add(pullPath);
    await press("Escape");
    await until(t.signal, () => alertTexts(container).includes(takeBackFailedText), "the error line");
    assert.equal(fake.callsTo(sendKeysPath).length, 1, "the Escape key went out after the failed pull");
    assert.equal(container.textContent?.includes("queue unreachable"), false, "the server's text is not shown");
  }));

// clickRow clicks the list's row for key, as a pointer does: the row opens, and a docked inbox is hidden.
async function clickRow(container: HTMLElement, key: string): Promise<void> {
  const element = container.querySelector(`.b-row[data-key="${key}"]`);
  assert.ok(element, `the list draws the row ${key}`);
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// hoverMark moves the pointer onto the mark, which pops a hidden inbox out over the page.
async function hoverMark(container: HTMLElement): Promise<void> {
  const mark = container.querySelector(".mark-crumb");
  assert.ok(mark, "the page draws the mark");
  await act(() => {
    mark.dispatchEvent(new Event("mouseenter", { bubbles: true }));
  });
}

// paneState is the inbox state the app draws on the panes: docked, pinned, hidden or flyout.
function paneState(container: HTMLElement): string | null {
  return container.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// leaveComposer focuses the cursor's reply box, as a row's opening does on origin, and presses Shift+Tab in it, which
// takes the focus to the list's cursor row.
async function leaveComposer(container: HTMLElement): Promise<void> {
  const box = composer(container);
  await act(() => {
    box.focus();
  });
  await act(() => {
    box.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
  });
}

test("EscapeInterrupt InboxPin: Escape on a working session with the inbox hidden by a click takes back its queued messages and interrupts", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await clickRow(container, "s1");
    assert.equal(paneState(container), "hidden", "the click opened the row and hid the inbox");
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await until(t.signal, () => composer(container).value === "first\n\nsecond", "the returned messages");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "no withdraw is called");
  }));

test("EscapeInterrupt InboxPin: Escape after a hover on the mark popped the inbox out takes back the queued messages and interrupts, and leaves the inbox out", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await clickRow(container, "s1");
    await hoverMark(container);
    assert.equal(paneState(container), "flyout", "the hover popped the inbox out");
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await until(t.signal, () => composer(container).value === "first\n\nsecond", "the returned messages");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "no withdraw is called");
    assert.equal(paneState(container), "flyout", "Escape does not tuck a hover pop-out");
  }));

// resizeWindow sets the viewport of the window the test's DOM is in, the one installTestDom made, to width pixels wide.
// It runs inside act, so the renders the resize causes are applied.
async function resizeWindow(width: number): Promise<void> {
  const view = (globalThis as unknown as { window: ReturnType<typeof installTestDom> }).window;
  await act(() => {
    view.happyDOM.setViewport({ width, height: 800 });
  });
}

// narrowWindow narrows the window from the wide default to width. happy-dom's media query starts out not matching, so
// it fires change for a narrowing only after a resize has shown it a match. The window is widened by a pixel first,
// which is that resize. A browser's query fires change on every crossing, so it needs no such step.
async function narrowWindow(width: number): Promise<void> {
  await resizeWindow(1025);
  await resizeWindow(width);
}

test("EscapeInterrupt InboxPin: at 700px, a list key after a row is opened leaves the inbox docked, and Escape on the working session still takes back its queued messages and interrupts", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await narrowWindow(700);
    await clickRow(container, "s1");
    await leaveComposer(container);
    await press("j");
    assert.equal(paneState(container), "docked", "the list key pops no inbox out");
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    const before = fake.calls.length;
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    assert.deepEqual(
      fake.calls
        .slice(before)
        .map((call) => call.path)
        .filter((path) => path.startsWith("/orrery.v1.Control/")),
      [interruptPath],
      "after the list key, Escape at 700px sends the interrupt, which takes back the messages",
    );
  }));
