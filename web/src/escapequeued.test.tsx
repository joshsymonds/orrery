import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// Escape on a session with queued messages, over the fake transport: the App interrupts a working session and takes
// back the texts the interrupt cancels, and never calls Control.Withdraw. Each test's name carries the token EscapeQueued.

const now = 1000;
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const interruptPath = "/orrery.v1.Control/Interrupt";
const withdrawPath = "/orrery.v1.Control/Withdraw";

type Clients = ReturnType<typeof createClients>;

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  body: Record<string, unknown>;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with JSON, and WatchConversation with a stream
// the test feeds through emit. Every call is recorded, and a unary path answers with its queued reply, else an empty one.
class FakeServer {
  readonly calls: Call[] = [];
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

// sessionRow is a synthetic headless session over the given state. Its changed and seen times are equal, so it reads as
// seen and arms no seen dwell.
function sessionRow(state: string): Row {
  return {
    tool: "",
    key: "s1",
    id: "s1",
    archiveId: "s1",
    kind: "session",
    label: "notes",
    project: "",
    host: "",
    state,
    summary: "",
    question: "",
    questionHash: "",
    changed: 30,
    seen: 30,
    unseen: false,
    archived: false,
    archiveKind: "none",
  };
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

// until waits as escape.test.tsx's until does: each round steps by turn inside act, so the renders it lets through
// apply. It reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// letTurnsRun lets the promise jobs and I/O the test started run, so a call made after an Escape would be recorded by
// the time it returns.
async function letTurnsRun(): Promise<void> {
  for (let round = 0; round < 50; round++) {
    await act(turn);
  }
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
      render(<App rows={rows} error="" now={now} clients={fake.clients} />, container);
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

// queuedRows counts the queued rows the tray draws: those not cancelled.
function queuedRows(container: HTMLElement): number {
  return container.querySelectorAll(".tray-row:not(.cancelled)").length;
}

// emitQueued waits for the session's stream to open, then sends a delta whose queued set holds each message as queued,
// and waits until the page draws one queued row for each.
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
  await until(signal, () => queuedRows(container) === queued.length, "the queued rows");
}

const twoQueued = [
  { promptId: "p1", text: "first" },
  { promptId: "p2", text: "second" },
];

test("EscapeQueued: Escape on a working session calls Control.Interrupt and never Control.Withdraw, and the reply's two texts go into the box after its text, joined by blank lines", (t) =>
  withApp(t.signal, [sessionRow("working")], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await emitQueued(t.signal, container, fake, twoQueued);
    await press("i");
    await typeInto(composer(container), "draft");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => composer(container).value === "draft\n\nfirst\n\nsecond", "the returned messages");
    assert.equal(fake.callsTo(interruptPath).length, 1, "Escape calls Control.Interrupt once");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "Escape never calls Control.Withdraw");
  }));

test("EscapeQueued: once the stream serves both entries canceled, no queued row is drawn", (t) =>
  withApp(t.signal, [sessionRow("working")], async (container, fake) => {
    fake.replies.set(interruptPath, [{ texts: ["first", "second"] }]);
    await emitQueued(t.signal, container, fake, twoQueued);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    await act(() => {
      fake.emit(fake.callsTo(sessionWatchPath)[0], {
        transcript: "s1.jsonl",
        seq: "2",
        items: [],
        queued: twoQueued.map((message) => ({ ...message, state: "canceled" })),
      });
    });
    await until(t.signal, () => queuedRows(container) === 0, "the canceled entries' end");
    assert.equal(queuedRows(container), 0, "no queued row is drawn");
  }));

test("EscapeQueued: Escape on a session that is not working calls neither Control.Interrupt nor Control.Withdraw", (t) =>
  withApp(t.signal, [sessionRow("done")], async (container, fake) => {
    await press("i");
    await pressIn(composer(container), "Escape");
    await until(t.signal, () => document.activeElement !== composer(container), "the blur");
    await letTurnsRun();
    assert.equal(fake.callsTo(interruptPath).length, 0, "a session that is not working is not interrupted");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "a session that is not working asks for no withdraw");
  }));
