import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { findMatches } from "./find";
import { ConversationItemSchema } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// The interrupt mark over the fake transport: Escape on a working headless session marks its dock's line interrupting
// at once, the mark holds while the state is working, and it clears when the state leaves working or the interrupt
// fails. The reader draws a line for an interrupted item and none for a turn that ended without one. Each test's name
// carries the token InterruptMark.

const now = 1000;
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const interruptPath = "/orrery.v1.Control/Interrupt";
const interruptFailedText = "could not interrupt";
const interruptedText = "Interrupted by you";

type Clients = ReturnType<typeof createClients>;

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with JSON, and WatchConversation with a stream the
// test feeds through emit. A unary call answers {} unless the test fails it or holds it open.
class FakeServer {
  readonly calls: Call[] = [];
  // failNext holds the unary paths whose next call the server fails with a Connect error, once each.
  readonly failNext = new Set<string>();
  // gates holds the next call to a unary path until its release is called.
  private readonly gates = new Map<string, Promise<void>>();
  private readonly releases: (() => void)[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    // A call's signal is the one the transport passed, so a stream that the App stops is not fed afterwards.
    const signal = init?.signal ?? request.signal;
    const path = new URL(request.url).pathname;
    if (!path.endsWith("/WatchConversation")) {
      this.calls.push({ path, controller: null });
      const gate = this.gates.get(path);
      this.gates.delete(path);
      await gate;
      if (this.failNext.delete(path)) {
        return new Response(JSON.stringify({ code: "unavailable", message: "queue unreachable" }), {
          status: 503,
          headers: { "content-type": "application/json" },
        });
      }
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

  // hold makes the next call to path wait until the returned release is called.
  hold(path: string): () => void {
    let release: () => void = () => {};
    this.gates.set(
      path,
      new Promise<void>((resolve) => {
        release = resolve;
      }),
    );
    this.releases.push(release);
    return release;
  }

  releaseAll(): void {
    for (const release of this.releases) {
      release();
    }
  }

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

// Page is the App mounted over a fake server: its container, the server, and show, which draws the App again over rows.
interface Page {
  container: HTMLElement;
  fake: FakeServer;
  show(rows: Row[]): Promise<void>;
}

// withPage mounts the App over a fake server in a fresh DOM with rows, and runs check. The App is unmounted, its streams
// closed and the DOM removed when the test ends, or as soon as the test's signal aborts first.
async function withPage(signal: AbortSignal, rows: Row[], check: (page: Page) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeServer();
  const show = (next: Row[]) =>
    act(() => {
      render(<App rows={next} error="" now={now} clients={fake.clients} />, container);
    });
  const teardown = teardownOnce(signal, async () => {
    fake.releaseAll();
    await act(() => {
      render(null, container);
    });
    fake.closeStreams();
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(rows);
    await check({ container, fake, show });
  } finally {
    await teardown();
  }
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// until waits as escape.test.tsx's until does: each round steps by turn inside act, so the renders it lets through
// apply. It reads no clock and arms no timer.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// dockBox is the box of the dock the cursor's reader shows, whose state line reads the cursor's row.
function dockBox(container: HTMLElement): Element | null {
  return container.querySelector('[aria-label="reader"] .send > div:not([hidden]) .dock-box');
}

function dockWord(container: HTMLElement): string {
  return dockBox(container)?.querySelector(".statebar .word")?.textContent ?? "";
}

function dockIsInterrupting(container: HTMLElement): boolean {
  return dockBox(container)?.getAttribute("data-state") === "interrupting";
}

function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

// readerRules is the text of each rule the reader draws.
function readerRules(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[aria-label="reader"] .rule')].map((element) => element.textContent ?? "");
}

// readerNoticeKinds is the kind of each notice the reader draws.
function readerNoticeKinds(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[aria-label="reader"] .notice-kind')].map((element) => element.textContent ?? "");
}

test("InterruptMark: Escape on a working session shows the interrupting line while the Interrupt reply is held open", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    const release = fake.hold(interruptPath);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    assert.equal(dockWord(container), "interrupting…", "the dock's line reads the mark before the reply lands");
    assert.equal(dockIsInterrupting(container), true);
    release();
  }));

test("DockState: Escape on a working session draws interrupting on the dock's line and no state word or pill on the head", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    const release = fake.hold(interruptPath);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    assert.equal(dockWord(container), "interrupting…");
    assert.equal(container.querySelector('[aria-label="reader"] > .title-row .state') === null, true, "the head draws no state word");
    assert.equal(container.querySelector('[aria-label="reader"] > .title-row .pill') === null, true, "the head draws no pill");
    release();
  }));

test("InterruptMark: the dock's line reads the mark while the row is working, and returns to the served state once it leaves working", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake, show }) => {
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    assert.equal(dockWord(container), "interrupting…");
    await show([{ ...sessionRow, state: "interrupted" }]);
    assert.equal(dockWord(container), "cut off", "the served state shows once the row leaves working");
    assert.equal(dockIsInterrupting(container), false);
    await show([{ ...sessionRow, state: "working" }]);
    assert.equal(dockWord(container), "working", "a later turn does not inherit the mark");
  }));

test("InterruptMark: a failed Interrupt clears the mark and shows the failure line", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    const release = fake.hold(interruptPath);
    fake.failNext.add(interruptPath);
    await press("Escape");
    await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
    assert.equal(dockWord(container), "interrupting…", "the mark holds while the reply is held open");
    release();
    await until(t.signal, () => alertTexts(container).includes(interruptFailedText), "the failure line");
    assert.equal(dockWord(container), "working", "the failed interrupt clears the mark");
    assert.equal(dockIsInterrupting(container), false);
  }));

test("InterruptMark: the reader draws Interrupted by you for an interrupted item", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "s1.jsonl",
      seq: "2",
      reset: true,
      items: [
        { kind: "reply", text: "still going", time: "1700000001" },
        { kind: "interrupted", time: "1700000002" },
      ],
    });
    await until(t.signal, () => readerRules(container).includes(interruptedText), "the interrupted line");
    assert.equal(
      readerRules(container).filter((text) => text === interruptedText).length,
      1,
      "the interrupted item draws one line",
    );
  }));

// A turn a preempt interrupted ends with the transcript's interrupted notice, not an interrupted item, so it draws no line.
test("InterruptMark: the reader draws no interrupted line for a turn a preempt interrupted, or one that ended without interrupt", (t) =>
  withPage(t.signal, [sessionRow], async ({ container, fake }) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "s1.jsonl",
      seq: "2",
      reset: true,
      items: [
        { kind: "reply", text: "all done", time: "1700000001" },
        { kind: "notice", notice: "interrupted", time: "1700000002" },
      ],
    });
    await until(t.signal, () => container.textContent?.includes("all done") === true, "the reply");
    assert.equal(readerRules(container).includes(interruptedText), false, "no item draws the owner's line");
    assert.deepEqual(readerNoticeKinds(container), ["interrupted"], "the preempt's notice draws as a notice");
  }));

test("InterruptMark: find does not search the interrupted line", () => {
  const items = [
    create(ConversationItemSchema, { kind: "reply", text: "still going" }),
    create(ConversationItemSchema, { kind: "interrupted", text: interruptedText }),
  ];
  assert.deepEqual(findMatches(items, "Interrupted"), []);
  assert.deepEqual(findMatches(items, "still"), [{ item: 0, start: 0 }]);
});
