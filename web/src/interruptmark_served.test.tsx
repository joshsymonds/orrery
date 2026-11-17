import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// The served interrupting mark over the fake transport: a working session whose served ConversationState carries
// interrupting reads interrupting in its pill with no Escape pressed, and the pill reads the served state again once a
// delta clears the mark. Each test's name carries the token InterruptMark.

const now = 1000;
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const interruptPath = "/orrery.v1.Control/Interrupt";

type Clients = ReturnType<typeof createClients>;

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's unary calls with {}, and WatchConversation with a stream the test feeds through emit.
class FakeServer {
  readonly calls: Call[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
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

function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// row is a synthetic row over the given fields. Its changed and seen times are equal, so it reads as seen.
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

// withPage mounts the App over a fake server in a fresh DOM with rows, and runs check. The App is unmounted, its
// streams closed and the DOM removed when the test ends, or as soon as the test's signal aborts first.
async function withPage(
  signal: AbortSignal,
  rows: Row[],
  check: (page: { container: HTMLElement; fake: FakeServer }) => Promise<void>,
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
    await check({ container, fake });
  } finally {
    await teardown();
  }
}

// until waits as interruptmark.test.tsx's until does: each round steps by turn inside act, so the renders it lets
// through apply. It reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// headPill is the pill the reader's head draws for the cursor's row.
function headPill(container: HTMLElement): Element | null {
  return container.querySelector('[aria-label="reader"] .pill');
}

function pillWord(container: HTMLElement): string {
  return headPill(container)?.querySelector(".word")?.textContent ?? "";
}

function pillIsInterrupting(container: HTMLElement): boolean {
  return headPill(container)?.classList.contains("tone-interrupting") ?? false;
}

// sessionReset is the session stream's first message: a reset that holds the given served state and one reply, which
// the reader draws, so a test can wait until the message has been applied.
function sessionReset(state: object): object {
  return { transcript: "s1.jsonl", seq: "2", reset: true, items: [replyItem("still going", "1700000001")], state };
}

function replyItem(text: string, time: string): object {
  return { kind: "reply", text, time };
}

test("InterruptMark: a working session whose served state carries interrupting reads interrupting with no Escape pressed", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    assert.equal(pillWord(container), "working", "the pill reads the list's state before the served mark arrives");
    fake.emit(stream, sessionReset({ interrupting: true }));
    await until(t.signal, () => pillWord(container) === "interrupting", "the served mark on the pill");
    assert.equal(pillIsInterrupting(container), true);
    assert.equal(fake.callsTo(interruptPath).length, 0, "no Interrupt was sent: the mark came from the served state");
  }));

test("InterruptMark: the pill reads the served state again once a delta clears the served mark", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    fake.emit(stream, sessionReset({ interrupting: true }));
    await until(t.signal, () => pillWord(container) === "interrupting", "the served mark on the pill");
    fake.emit(stream, { transcript: "s1.jsonl", seq: "3", reset: false, items: [replyItem("more", "1700000002")] });
    await until(t.signal, () => container.textContent?.includes("more") === true, "the delta with no state");
    assert.equal(pillIsInterrupting(container), true, "a delta that leaves the state unset keeps the mark");
    fake.emit(stream, {
      transcript: "s1.jsonl",
      seq: "4",
      reset: false,
      items: [replyItem("done", "1700000003")],
      state: { interrupting: false },
    });
    await until(t.signal, () => pillWord(container) === "working", "the served state once the mark clears");
    assert.equal(pillIsInterrupting(container), false);
  }));

test("InterruptMark: a served mark on a row that is not working leaves the pill at the row's state", (t) =>
  withPage(t.signal, [{ ...sessionRow, state: "waiting" }], async ({ container, fake }) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], sessionReset({ interrupting: true }));
    await until(t.signal, () => container.textContent?.includes("still going") === true, "the reply");
    assert.equal(pillWord(container), "waiting", "the pill reads the row's state");
    assert.equal(pillIsInterrupting(container), false, "only a working row reads interrupting");
  }));
