import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// The dock's calls, proven through the App over the fake transport: Resume, take back, Take all back, Send now, a
// no-answer re-say, and Escape's texts. Each test's name carries the token DockState or DockQueue.

const now = 1000;
const queuedText = "queued words";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const paneWatchPath = "/orrery.v1.Panes/WatchConversation";
const sayPath = "/orrery.v1.Control/Say";
const interruptPath = "/orrery.v1.Control/Interrupt";
const resumePath = "/orrery.v1.Control/Resume";
const pullPath = "/orrery.v1.Panes/PullQueued";

type Clients = ReturnType<typeof createClients>;

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  body: Record<string, unknown>;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with JSON, and the watch calls with a stream the
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
      return json(this.replies.get(path)?.shift() ?? {});
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
  archiveId: "s1",
  kind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  changed: 30,
  seen: 30,
});

async function typeInto(box: HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// until waits as escape.test.tsx's does: each round steps by turn inside act, so the renders it lets through apply. It
// reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// withApp mounts the App over a fake server in a fresh DOM and runs check. The App is unmounted, its streams closed and
// the DOM removed when the test ends, or as soon as the test's signal aborts first.
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

function composer(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) textarea");
  assert.ok(box, "the reader has a composer");
  return box;
}

// buttonNamed is the control the cursor's dock draws under its label: a state line control, or a tray head control.
function buttonNamed(container: HTMLElement, label: string): HTMLButtonElement | null {
  const controls = container.querySelectorAll<HTMLButtonElement>(".reply:not([hidden]) button.ctl");
  return Array.from(controls).find((control) => control.querySelector("span")?.textContent === label) ?? null;
}

// queueTexts is the text of each message in the cursor's queue above its box, oldest first, without its tag.
function queueTexts(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('[aria-label="reader"] .send .tray-row .text'),
    (text) => text.textContent ?? "",
  );
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

// headWord is the word the cursor's dock line draws in its state bar.
function headWord(container: HTMLElement): string {
  return container.querySelector('[aria-label="reader"] .send > div:not([hidden]) .statebar .word')?.textContent ?? "";
}

// press sends a key to the document, where the App reads its keys.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// working is a session's state on the turn that started at turnSince, interrupting once the owner has interrupted it.
function working(turnSince: number, interrupting = false): object {
  return { session: { state: "working" }, turnSince: String(turnSince), interrupting };
}

// sessionFrame is one delta of the cursor's session stream. Each delta sends the whole queued set, so a frame names
// the messages still queued with its state.
function sessionFrame(reset: boolean, seq: number, state: object, queued: object[] = []): object {
  return { transcript: "s1.jsonl", seq: String(seq), reset, items: [], state, queued };
}

test("DockState: Resume on a cut-off session calls Control.Resume with the session id", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "s1.jsonl",
      seq: "1",
      reset: true,
      items: [],
      state: { session: { state: "interrupted" } },
    });
    await until(t.signal, () => buttonNamed(container, "Resume") !== null, "the Resume control");
    await act(() => {
      buttonNamed(container, "Resume")?.click();
    });
    await until(t.signal, () => fake.callsTo(resumePath).length === 1, "the resume call");
    assert.deepEqual(fake.callsTo(resumePath)[0].body, { id: "s1" }, "the resume names the session");
  }));

test("DockQueue: a pane's Take all back calls Panes.PullQueued with the pane id and puts the pulled text in the box", (t) =>
  withApp(t.signal, [paneRow], async (container, fake) => {
    fake.replies.set(pullPath, [{ text: queuedText }]);
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { waiting: [{ text: queuedText, canceled: false }] },
    });
    await until(t.signal, () => buttonNamed(container, "Take all back") !== null, "the Take all back control");
    await act(() => {
      buttonNamed(container, "Take all back")?.click();
    });
    await until(t.signal, () => composer(container).value === queuedText, "the pulled text");
    assert.equal(fake.callsTo(pullPath).length, 1, "one pull is called");
    assert.deepEqual(fake.callsTo(pullPath)[0].body, { pane: "a" }, "the pull names the pane");
  }));

test("DockQueue: Send now calls Control.Interrupt, then Control.Say for each returned text in send order", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await until(t.signal, () => buttonNamed(container, "Send now") !== null, "the Send now control");
    await act(() => {
      buttonNamed(container, "Send now")?.click();
    });
    await until(t.signal, () => fake.callsTo(sayPath).length === 2, "the two says");
    const [interrupt] = fake.callsTo(interruptPath);
    const [first, second] = fake.callsTo(sayPath);
    assert.deepEqual(interrupt.body, { id: "s1" }, "the interrupt names the session");
    assert.ok(fake.calls.indexOf(interrupt) < fake.calls.indexOf(first), "the interrupt comes before the first say");
    assert.equal(first.body.text, "first", "the first say carries the first returned text");
    assert.equal(second.body.text, "second", "the second say carries the second returned text");
    assert.equal(first.body.id, "s1");
    assert.equal(second.body.id, "s1");
  }));

test("DockQueue: a re-say with no answer keeps that text and every later one in the box after the draft, and says nothing more", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    fake.failNext.add(sayPath);
    await emitQueued(t.signal, container, fake, [
      { promptId: "p1", text: "first" },
      { promptId: "p2", text: "second" },
    ]);
    await typeInto(composer(container), "draft");
    await until(t.signal, () => buttonNamed(container, "Send now") !== null, "the Send now control");
    await act(() => {
      buttonNamed(container, "Send now")?.click();
    });
    await until(t.signal, () => composer(container).value !== "draft", "the held texts");
    assert.equal(composer(container).value, "draft\n\nfirst\n\nsecond", "the unsaid texts follow the draft in send order");
    assert.equal(fake.callsTo(sayPath).length, 1, "only the refused say was called, and nothing more was said");
  }));

test("DockState: after Send now on a working session, a stream frame with the new turn's turn_since reads working again, with Interrupt", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: [] }]);
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    fake.emit(stream, sessionFrame(true, 1, working(100), [{ promptId: "p1", text: "first", state: "queued" }]));
    await until(t.signal, () => buttonNamed(container, "Send now") !== null, "the Send now control");
    await act(() => {
      buttonNamed(container, "Send now")?.click();
    });
    await until(t.signal, () => buttonNamed(container, "Interrupting…") !== null, "the interrupting line");
    fake.emit(stream, sessionFrame(false, 2, working(200)));
    await until(t.signal, () => buttonNamed(container, "Interrupt") !== null, "the Interrupt control");
    assert.equal(headWord(container), "working", "the line reads working on the new turn");
  }));

test("DockState: after Escape, a frame reporting interrupting reads interrupting…, and a following frame on a new turn reads working", (t) =>
  withApp(t.signal, [sessionRow], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: [] }]);
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    fake.emit(stream, sessionFrame(true, 1, working(100)));
    await until(t.signal, () => headWord(container) === "working", "the dock's line from the stream");
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    fake.emit(stream, sessionFrame(false, 2, working(100, true)));
    await until(t.signal, () => buttonNamed(container, "Interrupting…") !== null, "the interrupting line");
    fake.emit(stream, sessionFrame(false, 3, working(200)));
    await until(t.signal, () => buttonNamed(container, "Interrupt") !== null, "the Interrupt control");
    assert.equal(headWord(container), "working", "the line reads working on the new turn");
  }));
