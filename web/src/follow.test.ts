import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { follow, type Followed } from "./follow";

const panesPath = "/orrery.v1.Panes/WatchConversation";
const sessionsPath = "/orrery.v1.Sessions/WatchConversation";
const retryMs = 40;

interface Served {
  path: string;
  body: Record<string, unknown>;
  // When the call reached the fake server.
  at: number;
  // When the test ended the call's stream, or 0 while it is open.
  endedAt: number;
  // The response body of the call, or null once the test has ended it.
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeConnect answers Connect server streams through TransportOptions.fetch. Each call is recorded in served, and the
// test decides what its stream sends and when it ends. Aborting a call does not close its stream, so a message sent
// after the abort still reaches the client if it is still reading.
class FakeConnect {
  readonly served: Served[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const entry: Served = {
      path: new URL(request.url).pathname,
      body: decodeRequest(new Uint8Array(await request.arrayBuffer())),
      at: Date.now(),
      endedAt: 0,
      controller: null,
    };
    this.served.push(entry);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        entry.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  emit(index: number, message: object): void {
    this.open(index).enqueue(envelope(0, message));
  }

  end(index: number): void {
    this.finish(index, envelope(2, {}));
  }

  fail(index: number): void {
    this.finish(index, envelope(2, { error: { code: "unavailable", message: "stream failed" } }));
  }

  private open(index: number): ReadableStreamDefaultController<Uint8Array> {
    const controller = this.served[index].controller;
    if (controller === null) {
      throw new Error(`call ${index} has ended`);
    }
    return controller;
  }

  private finish(index: number, last: Uint8Array): void {
    const controller = this.open(index);
    controller.enqueue(last);
    controller.close();
    this.served[index].controller = null;
    this.served[index].endedAt = Date.now();
  }
}

function decodeRequest(bytes: Uint8Array): Record<string, unknown> {
  const length = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  return JSON.parse(new TextDecoder().decode(bytes.subarray(5, 5 + length)));
}

// envelope frames one Connect message: a flag byte, a 4-byte big-endian length, and the JSON payload.
function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

function clientsFor(fake: FakeConnect): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fake.fetch }));
}

async function until(check: () => boolean, what: string): Promise<void> {
  const deadline = Date.now() + 2000;
  while (!check()) {
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await sleep(2);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function texts(conversation: Conversation | undefined): string[] {
  return (conversation?.items ?? []).map((item) => item.text);
}

test("a pane row streams its conversation, and onChange receives each delta folded in", async () => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const row: Followed = { kind: "pane", id: "%1" };
  const stop = follow(clientsFor(fake), row, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(() => fake.served.length === 1, "the first request");
    assert.equal(fake.served[0].path, panesPath);
    assert.equal(fake.served[0].body.pane, "%1");
    assert.equal(fake.served[0].body.transcript ?? "", "");
    assert.equal(Number(fake.served[0].body.after ?? 0), 0);

    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(() => seen.length === 1, "the reset delta");
    assert.deepEqual(texts(seen[0]), ["one"]);
    assert.equal(seen[0].seq, 3n);

    fake.emit(0, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
    await until(() => seen.length === 2, "the second delta");
    assert.deepEqual(texts(seen[1]), ["one", "two"]);
    assert.equal(seen[1].seq, 4n);
  } finally {
    stop();
  }
});

test("a stream that ends or fails reconnects after retryMs with the held transcript and seq", async () => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const stop = follow(clientsFor(fake), { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(() => fake.served.length === 1, "the first request");
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(() => seen.length === 1, "the reset delta");
    fake.end(0);

    await until(() => fake.served.length === 2, "the reconnect after a clean end");
    assert.ok(
      fake.served[1].at - fake.served[0].endedAt >= retryMs - 5,
      `reconnected after ${fake.served[1].at - fake.served[0].endedAt}ms, want at least ${retryMs}ms`,
    );
    assert.equal(fake.served[1].path, panesPath);
    assert.equal(fake.served[1].body.pane, "%1");
    assert.equal(fake.served[1].body.transcript, "t1");
    assert.equal(Number(fake.served[1].body.after), 3);

    fake.emit(1, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
    await until(() => seen.length === 2, "the delta after the reconnect");
    fake.fail(1);

    await until(() => fake.served.length === 3, "the reconnect after a failure");
    assert.equal(fake.served[2].body.transcript, "t1");
    assert.equal(Number(fake.served[2].body.after), 4);
    assert.deepEqual(texts(seen[1]), ["one", "two"]);
  } finally {
    stop();
  }
});

test("a session row streams through Sessions.WatchConversation with its id", async () => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const stop = follow(clientsFor(fake), { kind: "session", id: "s1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(() => fake.served.length === 1, "the first request");
    assert.equal(fake.served[0].path, sessionsPath);
    assert.equal(fake.served[0].body.id, "s1");
    assert.equal(Number(fake.served[0].body.after ?? 0), 0);

    fake.emit(0, { transcript: "s-t", seq: "7", reset: true, items: [{ kind: "assistant", text: "notes" }] });
    await until(() => seen.length === 1, "the reset delta");
    assert.deepEqual(texts(seen[0]), ["notes"]);

    fake.fail(0);
    await until(() => fake.served.length === 2, "the reconnect");
    assert.equal(fake.served[1].path, sessionsPath);
    assert.equal(fake.served[1].body.id, "s1");
    assert.equal(Number(fake.served[1].body.after), 7);
  } finally {
    stop();
  }
});

test("after stop, onChange is not called and no request is made", async () => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const stop = follow(clientsFor(fake), { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  await until(() => fake.served.length === 1, "the first request");
  fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
  await until(() => seen.length === 1, "the reset delta");

  stop();
  fake.emit(0, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
  await sleep(retryMs * 3);
  assert.equal(seen.length, 1);
  assert.equal(fake.served.length, 1);
});

test("stop cancels a pending retry", async () => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const stop = follow(clientsFor(fake), { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(() => fake.served.length === 1, "the first request");
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(() => seen.length === 1, "the reset delta");
    fake.fail(0);

    await sleep(10);
    stop();
    await sleep(retryMs * 3);
    assert.equal(fake.served.length, 1);
    assert.equal(seen.length, 1);
  } finally {
    stop();
  }
});
