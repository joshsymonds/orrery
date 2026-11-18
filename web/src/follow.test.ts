import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { turn, until } from "./testwait";
import { follow, type Followed, type Schedule } from "./follow";

const panesPath = "/orrery.v1.Panes/WatchConversation";
const sessionsPath = "/orrery.v1.Sessions/WatchConversation";
const panesOlderPath = "/orrery.v1.Panes/Older";
const sessionsOlderPath = "/orrery.v1.Sessions/Older";
const retryMs = 40;

interface Served {
  path: string;
  body: Record<string, unknown>;
  // The response body of a stream call, or null once the test has ended it.
  controller: ReadableStreamDefaultController<Uint8Array> | null;
  // The response a unary call waits for, or null once the test has answered it.
  respond: ((response: Response) => void) | null;
}

// FakeConnect answers Connect calls through TransportOptions.fetch. Each call is recorded in served, and the test decides
// what it sends and when. A server stream's messages are sent by emit, and it ends by end or fail. A unary Older call
// waits until the test answers it, by answer or refuse. Aborting a call does not close its stream, so a message sent
// after the abort still reaches the client if it is still reading.
class FakeConnect {
  readonly served: Served[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    if (path.endsWith("/Older")) {
      return new Promise<Response>((respond) => {
        this.served.push({ path, body: JSON.parse(new TextDecoder().decode(bytes)), controller: null, respond });
      });
    }
    const entry: Served = { path, body: decodeRequest(bytes), controller: null, respond: null };
    this.served.push(entry);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        entry.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  olderCalls(): Served[] {
    return this.served.filter((call) => call.path.endsWith("/Older"));
  }

  emit(index: number, message: object): void {
    this.open(index).enqueue(envelope(0, message));
  }

  end(index: number): void {
    this.finish(index, envelope(2, {}));
  }

  fail(index: number): void {
    this.finish(index, envelope(2, { error: { code: "unavailable", message: "stream failed" } }));
  }

  // answer sends a unary call its page.
  answer(call: Served, message: object): void {
    this.respondTo(call)(new Response(JSON.stringify(message), { status: 200, headers: jsonHeaders }));
  }

  // refuse ends a unary call with a Connect error of code.
  refuse(call: Served, code: string, message: string): void {
    this.respondTo(call)(new Response(JSON.stringify({ code, message }), { status: 400, headers: jsonHeaders }));
  }

  private respondTo(call: Served): (response: Response) => void {
    const respond = call.respond;
    if (respond === null) {
      throw new Error(`call ${call.path} has been answered`);
    }
    call.respond = null;
    return respond;
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
  }
}

const jsonHeaders = { "content-type": "application/json" };

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

// Ask is one Older request follow has made, and the signal it was given.
interface Ask {
  signal: AbortSignal | undefined;
}

// clientsSpying wraps the clients so each Older request is recorded in asks when follow makes it. follow makes the call
// synchronously, so a test counts its requests at once, without waiting on the network for a request that never comes.
function clientsSpying(fake: FakeConnect): { clients: ReturnType<typeof createClients>; asks: Ask[] } {
  const asks: Ask[] = [];
  const real = clientsFor(fake);
  const clients: ReturnType<typeof createClients> = {
    ...real,
    panes: {
      ...real.panes,
      older: (request, options) => {
        asks.push({ signal: options?.signal });
        return real.panes.older(request, options);
      },
    },
    sessions: {
      ...real.sessions,
      older: (request, options) => {
        asks.push({ signal: options?.signal });
        return real.sessions.older(request, options);
      },
    },
  };
  return { clients, asks };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// settle lets the work already queued run, so a page that is to be dropped has been dropped before a test checks. A
// page's handler runs in the promise jobs a response queues, and each setImmediate turn runs every job queued
// before it. It waits on no time, so no test here reads a clock.
async function settle(): Promise<void> {
  for (let turn = 0; turn < 5; turn++) {
    await new Promise<void>((resolve) => setImmediate(resolve));
  }
}

function texts(conversation: Conversation | undefined): string[] {
  return (conversation?.items ?? []).map((item) => item.text);
}

test("a pane row streams its conversation, and onChange receives each delta folded in", async (t) => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const row: Followed = { kind: "pane", id: "%1" };
  const { stop } = follow(clientsFor(fake), row, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request");
    assert.equal(fake.served[0].path, panesPath);
    assert.equal(fake.served[0].body.pane, "%1");
    assert.equal(fake.served[0].body.transcript ?? "", "");
    assert.equal(Number(fake.served[0].body.after ?? 0), 0);

    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta");
    assert.deepEqual(texts(seen[0]), ["one"]);
    assert.equal(seen[0].seq, 3n);

    fake.emit(0, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
    await until(t.signal, () => seen.length === 2, "the second delta");
    assert.deepEqual(texts(seen[1]), ["one", "two"]);
    assert.equal(seen[1].seq, 4n);
  } finally {
    stop();
  }
});

// HeldSchedule is a Schedule the test drives: each wait it is asked for is kept, with its length, until the test runs it.
class HeldSchedule {
  readonly waits: { ms: number; run: () => void; cancelled: boolean }[] = [];

  readonly schedule: Schedule = (run, ms) => {
    const wait = { ms, run, cancelled: false };
    this.waits.push(wait);
    return () => {
      wait.cancelled = true;
    };
  };

  // end runs wait index, as its timer firing would.
  end(index: number): void {
    this.waits[index].run();
  }
}

test("a stream that ends or fails reconnects after retryMs with the held transcript and seq", async (t) => {
  // The forced order: each end of the stream must arm one wait of retryMs, and no request may go out until the test
  // ends that wait.
  const fake = new FakeConnect();
  const held = new HeldSchedule();
  const seen: Conversation[] = [];
  const { stop } = follow(
    clientsFor(fake),
    { kind: "pane", id: "%1" },
    (conversation) => seen.push(conversation),
    retryMs,
    held.schedule,
  );
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request");
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta");
    fake.end(0);

    await until(t.signal, () => held.waits.length === 1, "the retry wait after a clean end");
    assert.equal(held.waits[0].ms, retryMs, "the retry after a clean end did not wait retryMs");
    assert.equal(fake.served.length, 1, "a request went out before the retry wait ended");
    held.end(0);
    await until(t.signal, () => fake.served.length === 2, "the reconnect after a clean end");
    assert.equal(fake.served[1].path, panesPath);
    assert.equal(fake.served[1].body.pane, "%1");
    assert.equal(fake.served[1].body.transcript, "t1");
    assert.equal(Number(fake.served[1].body.after), 3);

    fake.emit(1, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
    await until(t.signal, () => seen.length === 2, "the delta after the reconnect");
    fake.fail(1);

    await until(t.signal, () => held.waits.length === 2, "the retry wait after a failure");
    assert.equal(held.waits[1].ms, retryMs, "the retry after a failure did not wait retryMs");
    assert.equal(fake.served.length, 2, "a request went out before the retry wait after the failure ended");
    held.end(1);
    await until(t.signal, () => fake.served.length === 3, "the reconnect after a failure");
    assert.equal(fake.served[2].body.transcript, "t1");
    assert.equal(Number(fake.served[2].body.after), 4);
    assert.deepEqual(texts(seen[1]), ["one", "two"]);
  } finally {
    stop();
  }
});

test("a session row streams through Sessions.WatchConversation with its id", async (t) => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const { stop } = follow(clientsFor(fake), { kind: "session", id: "s1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request");
    assert.equal(fake.served[0].path, sessionsPath);
    assert.equal(fake.served[0].body.id, "s1");
    assert.equal(Number(fake.served[0].body.after ?? 0), 0);

    fake.emit(0, { transcript: "s-t", seq: "7", reset: true, items: [{ kind: "assistant", text: "notes" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta");
    assert.deepEqual(texts(seen[0]), ["notes"]);

    fake.fail(0);
    await until(t.signal, () => fake.served.length === 2, "the reconnect");
    assert.equal(fake.served[1].path, sessionsPath);
    assert.equal(fake.served[1].body.id, "s1");
    assert.equal(Number(fake.served[1].body.after), 7);
  } finally {
    stop();
  }
});

test("after stop, onChange is not called and no request is made", async (t) => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const { stop } = follow(clientsFor(fake), { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  await until(t.signal, () => fake.served.length === 1, "the first request");
  fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
  await until(t.signal, () => seen.length === 1, "the reset delta");

  stop();
  fake.emit(0, { transcript: "t1", seq: "4", items: [{ kind: "assistant", text: "two" }] });
  await sleep(retryMs * 3);
  assert.equal(seen.length, 1);
  assert.equal(fake.served.length, 1);
});

test("stop cancels a pending retry", async (t) => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const { stop } = follow(clientsFor(fake), { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request");
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta");
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

test("older asks a session for the page before its reset's oldest, and prepends the page", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "session", id: "s1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, {
      transcript: "s-t",
      seq: "7",
      reset: true,
      oldest: "9",
      items: [{ kind: "assistant", text: "nine" }],
    });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
    assert.equal(asks.length, 1);
    assert.equal(fake.olderCalls()[0].path, sessionsOlderPath);
    assert.equal(fake.olderCalls()[0].body.id, "s1");
    assert.equal(Number(fake.olderCalls()[0].body.before), 9);

    fake.answer(fake.olderCalls()[0], {
      items: [{ kind: "assistant", text: "eight" }],
      oldest: "8",
      more: true,
    });
    await until(t.signal, () => seen.length === 2, "the older page", { step: turn });
    assert.deepEqual(texts(seen[1]), ["eight", "nine"]);
    assert.equal(seen[1].oldest, 8n);
    assert.equal(seen[1].more, true);
    assert.equal(seen[1].seq, 7n, "the page leaves the seq as it was");
  } finally {
    stop();
  }
});

test("older asks a pane for the page before its reset's oldest, with the held transcript", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, {
      transcript: "t1",
      seq: "3",
      reset: true,
      oldest: "120",
      items: [{ kind: "assistant", text: "line-120" }],
    });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
    assert.equal(asks.length, 1);
    assert.equal(fake.olderCalls()[0].path, panesOlderPath);
    assert.equal(fake.olderCalls()[0].body.pane, "%1");
    assert.equal(fake.olderCalls()[0].body.transcript, "t1");
    assert.equal(Number(fake.olderCalls()[0].body.before), 120);
  } finally {
    stop();
  }
});

test("older chains pages back until more is false, and then asks no more", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, oldest: "120", items: [{ kind: "assistant", text: "c" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the first older request", { step: turn });
    fake.answer(fake.olderCalls()[0], { items: [{ kind: "assistant", text: "b" }], oldest: "60", more: true });
    await until(t.signal, () => seen.length === 2, "the first older page", { step: turn });
    assert.equal(seen[1].more, true);

    older();
    await until(t.signal, () => fake.olderCalls().length === 2, "the second older request", { step: turn });
    assert.equal(Number(fake.olderCalls()[1].body.before), 60);
    fake.answer(fake.olderCalls()[1], { items: [{ kind: "assistant", text: "a" }], oldest: "20", more: false });
    await until(t.signal, () => seen.length === 3, "the last older page", { step: turn });
    assert.deepEqual(texts(seen[2]), ["a", "b", "c"]);
    assert.equal(seen[2].more, false);

    older();
    older();
    assert.equal(asks.length, 2, "no request follows a page with more false");
  } finally {
    stop();
  }
});

test("older does nothing before a delta is held", () => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const { stop, older } = follow(clients, { kind: "session", id: "s1" }, () => {}, retryMs);
  try {
    older();
    assert.equal(asks.length, 0);
  } finally {
    stop();
  }
});

test("older does nothing after stop, and a page that answers after stop is dropped", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
  fake.emit(0, { transcript: "t1", seq: "3", reset: true, oldest: "120", items: [{ kind: "assistant", text: "c" }] });
  await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

  older();
  await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
  const signal = asks[0].signal;
  assert.equal(signal?.aborted, false, "the older request carries the stream's signal, not yet aborted");

  stop();
  assert.equal(signal?.aborted, true, "stop aborts the older request's signal");
  older();
  assert.equal(asks.length, 1, "no request follows stop");

  fake.answer(fake.olderCalls()[0], { items: [{ kind: "assistant", text: "b" }], oldest: "60", more: true });
  await settle();
  assert.equal(seen.length, 1, "the page that answered after stop reached no onChange");
});

test("a page that answers after a reset moved the held oldest is dropped", async (t) => {
  const fake = new FakeConnect();
  const { clients } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, oldest: "120", items: [{ kind: "assistant", text: "c" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });

    fake.emit(0, { transcript: "t1", seq: "9", reset: true, oldest: "300", items: [{ kind: "assistant", text: "z" }] });
    await until(t.signal, () => seen.length === 2, "the reset that moves the oldest", { step: turn });

    fake.answer(fake.olderCalls()[0], { items: [{ kind: "assistant", text: "b" }], oldest: "60", more: true });
    await settle();
    assert.equal(seen.length, 2, "the page against the old oldest reached no onChange");
    assert.deepEqual(texts(seen[1]), ["z"]);
    assert.equal(seen[1].oldest, 300n);
  } finally {
    stop();
  }
});

test("only one older page is in flight at a time", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "session", id: "s1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "s-t", seq: "7", reset: true, oldest: "9", items: [{ kind: "assistant", text: "nine" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    older();
    assert.equal(asks.length, 1, "a second request waits for the first page");

    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
    fake.answer(fake.olderCalls()[0], { items: [{ kind: "assistant", text: "eight" }], oldest: "8", more: true });
    await until(t.signal, () => seen.length === 2, "the older page", { step: turn });

    older();
    assert.equal(asks.length, 2, "the next page is asked for once the first has landed");
  } finally {
    stop();
  }
});

test("failed_precondition ends the history, and no page is asked for after it", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "pane", id: "%1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, oldest: "120", items: [{ kind: "assistant", text: "c" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
    fake.refuse(fake.olderCalls()[0], "failed_precondition", "transcript changed");
    await until(t.signal, () => seen.length === 2, "the history ending", { step: turn });
    assert.equal(seen[1].more, false);
    assert.deepEqual(texts(seen[1]), ["c"]);

    older();
    assert.equal(asks.length, 1, "no request follows the end of the history");
  } finally {
    stop();
  }
});

test("another failure leaves more set, so a later call asks again", async (t) => {
  const fake = new FakeConnect();
  const { clients, asks } = clientsSpying(fake);
  const seen: Conversation[] = [];
  const { stop, older } = follow(clients, { kind: "session", id: "s1" }, (conversation) => seen.push(conversation), retryMs);
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "s-t", seq: "7", reset: true, oldest: "9", items: [{ kind: "assistant", text: "nine" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    older();
    await until(t.signal, () => fake.olderCalls().length === 1, "the older request", { step: turn });
    fake.refuse(fake.olderCalls()[0], "unavailable", "try later");
    await settle();
    assert.equal(seen.length, 1, "a failed page changes nothing held");

    older();
    assert.equal(asks.length, 2, "a later call asks again");
    await until(t.signal, () => fake.olderCalls().length === 2, "the second older request", { step: turn });
    assert.equal(Number(fake.olderCalls()[1].body.before), 9);
  } finally {
    stop();
  }
});

test("RenderOnce: a keepalive delta calls no onChange, and the next reconnect resumes after its seq", async (t) => {
  const fake = new FakeConnect();
  const held = new HeldSchedule();
  const seen: Conversation[] = [];
  const { stop } = follow(
    clientsFor(fake),
    { kind: "pane", id: "%1" },
    (conversation) => seen.push(conversation),
    retryMs,
    held.schedule,
  );
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "one" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    // The keepalive is sent, then the stream fails: the stream delivers its messages in order, so the keepalive is
    // folded before the failure is seen, and the retry wait is armed only after it.
    fake.emit(0, { transcript: "t1", seq: "4" });
    fake.fail(0);
    await until(t.signal, () => held.waits.length === 1, "the retry wait after the keepalive", { step: turn });
    assert.equal(seen.length, 1, "the keepalive reached no onChange");
    assert.equal(fake.served.length, 1, "a request went out before the retry wait ended");
    held.end(0);
    await until(t.signal, () => fake.served.length === 2, "the reconnect after the keepalive", { step: turn });
    assert.equal(Number(fake.served[1].body.after), 4, "the keepalive's seq is held: the reconnect resumes after it");

    fake.emit(1, { transcript: "t1", seq: "5", items: [{ kind: "assistant", text: "two" }] });
    await until(t.signal, () => seen.length === 2, "the delta with an item", { step: turn });
    assert.deepEqual(texts(seen[1]), ["one", "two"], "the next onChange holds every item, none lost to the keepalive");
    assert.equal(seen[1].seq, 5n);
  } finally {
    stop();
  }
});

test("a delta with no items reaches onChange when it changes the unfinished set, the state or the held set", async (t) => {
  const fake = new FakeConnect();
  const seen: Conversation[] = [];
  const { stop } = follow(
    clientsFor(fake),
    { kind: "session", id: "s1" },
    (conversation) => seen.push(conversation),
    retryMs,
  );
  try {
    await until(t.signal, () => fake.served.length === 1, "the first request", { step: turn });
    fake.emit(0, { transcript: "s-t", seq: "7", reset: true, items: [{ kind: "assistant", text: "notes" }] });
    await until(t.signal, () => seen.length === 1, "the reset delta", { step: turn });

    const typing = { messageId: "m1", index: 0, text: "typing", kind: "text" };
    fake.emit(0, { transcript: "s-t", seq: "8", unfinished: [typing] });
    await until(t.signal, () => seen.length === 2, "the unfinished change", { step: turn });
    assert.deepEqual(
      seen[1].unfinished.map((block) => block.text),
      ["typing"],
      "the unfinished change reaches onChange",
    );
    assert.equal(seen[1].items, seen[0].items, "a delta with no items keeps the held items array");

    fake.emit(0, { transcript: "s-t", seq: "9", unfinished: [typing], state: { goal: "ship it" } });
    await until(t.signal, () => seen.length === 3, "the state change", { step: turn });
    assert.equal(seen[2].state?.goal, "ship it", "the state change reaches onChange");

    fake.emit(0, {
      transcript: "s-t",
      seq: "10",
      unfinished: [typing],
      state: { goal: "ship it" },
      queued: [{ promptId: "p1", text: "queued", state: "queued" }],
    });
    await until(t.signal, () => seen.length === 4, "the queued change", { step: turn });
    assert.deepEqual(
      seen[3].queued.map((message) => message.text),
      ["queued"],
      "the queued change reaches onChange",
    );
    assert.deepEqual(texts(seen[3]), ["notes"], "no change adds items");
  } finally {
    stop();
  }
});
