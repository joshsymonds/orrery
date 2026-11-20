import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// A headless session's queue over the fake transport: its queued messages draw as rows above the box, each with its note
// and a withdraw; a discarded one draws to put back; a canceled one draws nothing; and a prompt the queue holds is not
// drawn as sent. Each test's name carries the token QueueLifecycle or WithdrawQueued.

const now = 1000;
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const withdrawPath = "/orrery.v1.Control/Withdraw";
const messageText = "queued words";
const takesNextNote = "Claude takes it at its next tool call.";
const alreadyTookNote = "Claude already took it in.";
const withdrawFailedNote = "Could not withdraw the message.";

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

// sessionRow is the headless session the page opens on: working, and seen, so it arms no seen dwell.
const sessionRow: Row = {
  tool: "",
  key: "s1",
  id: "s1",
  kind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  summary: "",
  question: "",
  questionHash: "",
  changed: 30,
  seen: 30,
  unseen: false,
  archived: false,
  archiveKind: "none",
  archiveId: "s1",
};

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
    await mount(container, fake);
    await check(container, fake);
  } finally {
    await teardown();
  }
}

// mount draws the App over the fake server, as a page opening or a reload does.
async function mount(container: HTMLElement, fake: FakeServer): Promise<void> {
  await act(() => {
    render(<App rows={[sessionRow]} error="" now={now} clients={fake.clients} />, container);
  });
}

// emit waits for the session stream at index to open, then sends it message.
async function emit(signal: AbortSignal, fake: FakeServer, index: number, message: object): Promise<void> {
  await until(signal, () => fake.callsTo(sessionWatchPath).length > index, "the session stream");
  fake.emit(fake.callsTo(sessionWatchPath)[index], message);
}

function composer(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) .composer textarea");
  assert.ok(box, "the reader has a composer");
  return box;
}

// queuedRows are the session's queued rows above the cursor's box.
function queuedRows(container: HTMLElement): Element[] {
  return Array.from(container.querySelectorAll(".reply:not([hidden]) li.queued"));
}

// undeliveredRows are the discarded rows above the cursor's box.
function undeliveredRows(container: HTMLElement): Element[] {
  return Array.from(container.querySelectorAll(".reply:not([hidden]) li.undelivered"));
}

// readerText is the text the reader draws, without its composer.
function readerText(container: HTMLElement): string {
  return container.querySelector('[aria-label="reader"] .reader')?.textContent ?? "";
}

// sentPrompts are the prompt rows the reader draws as sent.
function sentPrompts(container: HTMLElement): Element[] {
  return Array.from(container.querySelectorAll('[aria-label="reader"] .you'));
}

function noteOf(row: Element): string {
  return row.querySelector(".note")?.textContent ?? "";
}

function withdrawButton(row: Element): HTMLButtonElement | null {
  return row.querySelector<HTMLButtonElement>('button[aria-label="withdraw"]');
}

// queuedDelta is the first delta of a session stream: a reset that carries the given items and queued messages.
function queuedDelta(
  seq: string,
  queued: { promptId: string; text: string; state: string }[],
  items: object[] = [],
): object {
  return { transcript: "s1.jsonl", seq, reset: true, items, queued };
}

test("QueueLifecycle: a session delta with a queued entry draws one li.queued row with its text and the next-tool-call note, and draws no sent prompt item for that prompt id in the reader, even though the delta's items hold that prompt item", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(
      t.signal,
      fake,
      0,
      queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }], [
        { kind: "prompt", text: messageText, promptId: "p1" },
      ]),
    );
    await until(t.signal, () => queuedRows(container).length === 1, "the queued row");
    const [row] = queuedRows(container);
    assert.equal(row.querySelector(".text")?.textContent, messageText, "the row reads the message's text");
    assert.equal(noteOf(row), takesNextNote, "the row says Claude takes it at its next tool call");
    assert.equal(sentPrompts(container).length, 0, "the queued prompt is not drawn as sent in the reader");
  }));

test("QueueLifecycle: a delta with two queued entries draws one li.queued row per not-started message, each with its own text and the next-tool-call note, and no sent prompt item for either prompt", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(
      t.signal,
      fake,
      0,
      queuedDelta(
        "1",
        [
          { promptId: "p1", text: "first queued", state: "queued" },
          { promptId: "p2", text: "second queued", state: "queued" },
        ],
        [
          { kind: "prompt", text: "first queued", promptId: "p1" },
          { kind: "prompt", text: "second queued", promptId: "p2" },
        ],
      ),
    );
    await until(t.signal, () => queuedRows(container).length === 2, "the two queued rows");
    const rows = queuedRows(container);
    assert.equal(rows.length, 2, "each not-started message draws one queued row");
    assert.equal(rows[0].querySelector(".text")?.textContent, "first queued", "the first row reads the first message");
    assert.equal(rows[1].querySelector(".text")?.textContent, "second queued", "the second row reads the second message");
    assert.equal(noteOf(rows[0]), takesNextNote, "the first row says Claude takes it at its next tool call");
    assert.equal(noteOf(rows[1]), takesNextNote, "the second row says Claude takes it at its next tool call");
    assert.ok(withdrawButton(rows[0]), "the first row has a withdraw control");
    assert.ok(withdrawButton(rows[1]), "the second row has a withdraw control");
    assert.equal(sentPrompts(container).length, 0, "neither queued prompt is drawn as sent in the reader");
    assert.equal(readerText(container).includes("first queued"), false, "the first queued text is not drawn in the reader");
    assert.equal(readerText(container).includes("second queued"), false, "the second queued text is not drawn in the reader");
  }));

test("QueueLifecycle: a fresh stream (reload) whose reset carries the queued entry draws the row again", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(t.signal, fake, 0, queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }]));
    await until(t.signal, () => queuedRows(container).length === 1, "the first stream's row");
    await act(() => {
      render(null, container);
    });
    await mount(container, fake);
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 2, "the fresh stream");
    assert.equal(queuedRows(container).length, 0, "the fresh page holds no row until its stream answers");
    await emit(t.signal, fake, 1, queuedDelta("2", [{ promptId: "p1", text: messageText, state: "queued" }]));
    await until(t.signal, () => queuedRows(container).length === 1, "the row after the reload");
    assert.equal(noteOf(queuedRows(container)[0]), takesNextNote);
  }));

test("QueueLifecycle: a later delta whose queued list no longer holds the entry drops the row, and the prompt item is drawn as sent", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(
      t.signal,
      fake,
      0,
      queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }], [
        { kind: "prompt", text: messageText, promptId: "p1" },
      ]),
    );
    await until(t.signal, () => queuedRows(container).length === 1, "the queued row");
    fake.emit(fake.callsTo(sessionWatchPath)[0], { transcript: "s1.jsonl", seq: "2", items: [], queued: [] });
    await until(t.signal, () => queuedRows(container).length === 0, "the row's end");
    assert.equal(sentPrompts(container).length, 1, "the prompt is drawn as sent once the queue lets it go");
    assert.ok(sentPrompts(container)[0].textContent?.includes(messageText), "the sent prompt carries its text");
  }));

test("QueueLifecycle: a canceled entry draws no row and its prompt item stays hidden even after a later delta drops the entry", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(
      t.signal,
      fake,
      0,
      queuedDelta(
        "1",
        [{ promptId: "p1", text: messageText, state: "canceled" }],
        [
          { kind: "prompt", text: messageText, promptId: "p1" },
          { kind: "assistant", text: "reply before the drop" },
        ],
      ),
    );
    await until(t.signal, () => readerText(container).includes("reply before the drop"), "the reply");
    assert.equal(queuedRows(container).length, 0, "a canceled message draws no row");
    assert.equal(undeliveredRows(container).length, 0, "a canceled message draws no undelivered row");
    assert.equal(sentPrompts(container).length, 0, "the canceled prompt is not drawn");

    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "s1.jsonl",
      seq: "2",
      items: [{ kind: "assistant", text: "reply after the drop" }],
      queued: [],
    });
    await until(t.signal, () => readerText(container).includes("reply after the drop"), "the later reply");
    assert.equal(sentPrompts(container).length, 0, "the prompt stays hidden after the queue drops it");
    assert.equal(queuedRows(container).length, 0, "no row is drawn after the queue drops it");
  }));

test("QueueLifecycle: a discarded entry draws an li.undelivered row reading not delivered and no li.queued row, and its put-back control puts the text into the composer", (t) =>
  withApp(t.signal, async (container, fake) => {
    await emit(t.signal, fake, 0, queuedDelta("1", [{ promptId: "p1", text: messageText, state: "discarded" }]));
    await until(t.signal, () => undeliveredRows(container).length === 1, "the undelivered row");
    const [row] = undeliveredRows(container);
    assert.equal(row.querySelector(".tag")?.textContent, "not delivered", "the row is tagged not delivered");
    assert.equal(row.querySelector(".text")?.textContent, messageText, "the row reads the message's text");
    assert.equal(queuedRows(container).length, 0, "a discarded message draws no queued row");

    const putBack = row.querySelector<HTMLButtonElement>('button[aria-label="put back"]');
    assert.ok(putBack, "the row has a put-back control");
    await act(() => {
      putBack.click();
    });
    assert.equal(composer(container).value, messageText, "the text is put into the composer");
    assert.equal(undeliveredRows(container).length, 0, "the row is hidden once put back");
  }));

test("WithdrawQueued: the Withdraw control calls Control.Withdraw with the session id and that prompt id, and a reply's text goes into the composer after the box's text, joined by a blank line", (t) =>
  withApp(t.signal, async (container, fake) => {
    fake.replies.set(withdrawPath, [{ texts: ["withdrawn words"] }]);
    await emit(t.signal, fake, 0, queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }]));
    await until(t.signal, () => queuedRows(container).length === 1, "the queued row");
    await typeInto(composer(container), "draft");
    const button = withdrawButton(queuedRows(container)[0]);
    assert.ok(button, "the row has a withdraw control");
    await act(() => {
      button.click();
    });
    await until(t.signal, () => composer(container).value !== "draft", "the withdrawn text");
    const [withdraw] = fake.callsTo(withdrawPath);
    assert.deepEqual(withdraw.body, { id: "s1", promptId: "p1" }, "the withdraw names the session and the prompt");
    assert.equal(composer(container).value, "draft\n\nwithdrawn words");
    assert.equal(queuedRows(container).length, 1, "the row stays until the stream drops it");
  }));

test("WithdrawQueued: a reply with no texts shows the taken-in wording, the row reading Claude already took it in, and takes its withdraw control away", (t) =>
  withApp(t.signal, async (container, fake) => {
    fake.replies.set(withdrawPath, [{}]);
    await emit(t.signal, fake, 0, queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }]));
    await until(t.signal, () => queuedRows(container).length === 1, "the queued row");
    const button = withdrawButton(queuedRows(container)[0]);
    assert.ok(button, "the row has a withdraw control");
    await act(() => {
      button.click();
    });
    await until(t.signal, () => noteOf(queuedRows(container)[0]) === alreadyTookNote, "the note");
    assert.equal(withdrawButton(queuedRows(container)[0]), null, "the withdraw control is gone");
    assert.equal(composer(container).value, "", "no text is put into the composer");
  }));

test("WithdrawQueued: a refused withdraw makes the row read Could not withdraw the message., and puts no text into the composer", (t) =>
  withApp(t.signal, async (container, fake) => {
    fake.failNext.add(withdrawPath);
    await emit(t.signal, fake, 0, queuedDelta("1", [{ promptId: "p1", text: messageText, state: "queued" }]));
    await until(t.signal, () => queuedRows(container).length === 1, "the queued row");
    const button = withdrawButton(queuedRows(container)[0]);
    assert.ok(button, "the row has a withdraw control");
    await act(() => {
      button.click();
    });
    await until(t.signal, () => noteOf(queuedRows(container)[0]) === withdrawFailedNote, "the failure note");
    assert.equal(composer(container).value, "", "no text is put into the composer");
    assert.equal(container.textContent?.includes("queue unreachable"), false, "the server's text is not shown");
  }));
