import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { start } from "./main";
import { installTestDom, removeTestDom } from "./testdom";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const watchPath = "/orrery.v1.Events/Watch";
const clientHeader = "X-Orrery-Client";
const clientValue = "orrery web";
const intervalMs = 5;
// A poll interval far longer than any test waits, so only events and the stream can reload the list.
const longPollMs = 60_000;

// Synthetic fixtures: one pane and one headless session, no real hosts or paths.
function paneBody(label: string) {
  return {
    panes: [
      {
        id: "%1",
        host: "box",
        session: "work",
        label,
        cwd: "/work/api",
        state: "waiting",
        summary: "building",
        question: "",
        changed: "40",
        seen: "10",
      },
    ],
  };
}

const sessionBody = {
  sessions: [
    {
      id: "s1",
      pane: "",
      label: "notes",
      state: "working",
      summary: "",
      question: "",
      changed: "30",
      seen: "30",
    },
  ],
};

// One Events.Watch call the fake server received. The test decides what its stream sends and when it ends.
interface Watch {
  // The seq the call resumes after.
  after: bigint;
  // The call's signal, aborted when the page stops.
  signal: AbortSignal;
  // The client header the call carried.
  header: string | null;
  // The stream's controller, or null once the test has ended it.
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

interface FakeServer {
  fetch: typeof globalThis.fetch;
  // The pane label the next Panes.List answers with.
  paneLabel: string;
  // While set, Panes.List waits for it before answering.
  gate: Promise<void> | null;
  panesCalls: number;
  panesInFlight: number;
  maxPanesInFlight: number;
  // The store position Sessions.List reports with its sessions.
  storeSeq: bigint;
  // The Events.Watch calls, in order. Only a server built with streaming answers them.
  watches: Watch[];
}

// fakeServer answers both Connect lists from its fields, so a test can change what the next reload shows. Without
// streaming, Events.Watch answers 404, so the stream is down and the poll alone reloads.
function fakeServer(paneLabel: string, streaming = false): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      if (path === sessionsPath) {
        return json({ ...sessionBody, seq: String(server.storeSeq) });
      }
      if (path === watchPath && streaming) {
        const body = decodeRequest(new Uint8Array(await request.arrayBuffer()));
        const watch: Watch = {
          after: BigInt(String(body.after ?? "0")),
          signal: request.signal,
          header: request.headers.get(clientHeader),
          controller: null,
        };
        const stream = new ReadableStream<Uint8Array>({
          start: (controller) => {
            watch.controller = controller;
          },
        });
        server.watches.push(watch);
        return new Response(stream, { status: 200, headers: { "content-type": "application/connect+json" } });
      }
      if (path !== panesPath) {
        return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
      }
      server.panesCalls++;
      server.panesInFlight++;
      server.maxPanesInFlight = Math.max(server.maxPanesInFlight, server.panesInFlight);
      try {
        if (server.gate) {
          await server.gate;
        } else {
          // Longer than intervalMs, so a reload that ignored the in-flight one would overlap it.
          await new Promise((resolve) => setTimeout(resolve, intervalMs * 3));
        }
        return json(paneBody(server.paneLabel));
      } finally {
        server.panesInFlight--;
      }
    },
    paneLabel,
    gate: null,
    panesCalls: 0,
    panesInFlight: 0,
    maxPanesInFlight: 0,
    storeSeq: 7n,
    watches: [],
  };
  return server;
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function clientsFor(server: FakeServer): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch: server.fetch }));
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

function labels(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".label")].map((element) => element.textContent ?? "");
}

// withPage runs check with a fresh DOM, a root element for start, and the test's own cleanup.
async function withPage(check: (root: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const root = document.createElement("div");
  try {
    await check(root);
  } finally {
    await removeTestDom(window);
  }
}

// sendEvent writes one event onto a watch's stream. The client only needs the event to arrive, not its content.
function sendEvent(watch: Watch, seq: number): void {
  enqueue(watch, 0, { seq: String(seq), sessionId: "%1" });
}

function endStream(watch: Watch): void {
  finishStream(watch, {});
}

function failStream(watch: Watch): void {
  finishStream(watch, { error: { code: "unavailable", message: "stream failed" } });
}

function finishStream(watch: Watch, last: object): void {
  enqueue(watch, 2, last);
  watch.controller?.close();
  watch.controller = null;
}

function enqueue(watch: Watch, flag: number, message: object): void {
  if (watch.controller === null) {
    throw new Error("the stream has ended");
  }
  watch.controller.enqueue(envelope(flag, message));
}

// decodeRequest reads the one message a server-stream call sends: a flag byte, a 4-byte length, and the JSON payload.
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

test("loaded rows render into the root", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("api"), "the pane row");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("before the first load lands the root holds no rows", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    let release: () => void = () => {};
    server.gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      assert.deepEqual(labels(root), []);
      release();
      await until(() => labels(root).includes("api"), "the pane row after release");
    } finally {
      stop();
    }
  }));

test("a reload shows the changed pane", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("api"), "the first pane label");
      server.paneLabel = "web";
      await until(() => labels(root).includes("web") && !labels(root).includes("api"), "the renamed pane");
    } finally {
      stop();
    }
  }));

test("a failed Panes.List shows the fixed error and keeps the sessions", () =>
  withPage(async (root) => {
    const failing: typeof globalThis.fetch = async (input, init) => {
      const path = new URL(new Request(input, init).url).pathname;
      if (path === panesPath) {
        return new Response(JSON.stringify({ code: "unavailable", message: "panes server detail" }), {
          status: 503,
          headers: { "content-type": "application/json" },
        });
      }
      return json(sessionBody);
    };
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: failing }));
    const stop = start(root, clients, intervalMs);
    try {
      await until(() => root.querySelector(".error") !== null, "the error line");
      assert.equal(root.querySelector(".error")?.textContent, "panes unavailable");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("a reload never starts while the last one is in flight", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.panesCalls >= 3, "three loads");
      assert.equal(server.maxPanesInFlight, 1);
    } finally {
      stop();
    }
  }));

test("stop unmounts the root and stops reloading", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    await until(() => labels(root).includes("api"), "the first load");
    stop();
    assert.equal(root.childNodes.length, 0, "stop left nodes in the root");
    const callsAtStop = server.panesCalls;
    await sleep(intervalMs * 10);
    assert.equal(server.panesCalls, callsAtStop, "a reload ran after stop");
    assert.equal(root.childNodes.length, 0, "a reload rendered after stop");
  }));

test("an event on the open stream reloads within a second, without waiting for the poll", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      assert.equal(server.watches[0].header, clientValue, "the stream call lacked the client header");
      server.paneLabel = "web";
      const sentAt = Date.now();
      sendEvent(server.watches[0], 8);
      await until(() => labels(root).includes("web"), "the reload after the event");
      const elapsed = Date.now() - sentAt;
      assert.ok(elapsed < 1000, `the reload took ${elapsed} ms`);
    } finally {
      stop();
    }
  }));

test("a burst of events gives one reload", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      const before = server.panesCalls;
      for (let seq = 8; seq < 13; seq++) {
        sendEvent(server.watches[0], seq);
      }
      await until(() => server.panesCalls > before, "the reload after the burst");
      await sleep(300);
      assert.equal(server.panesCalls, before + 1, "the burst gave more than one reload");
    } finally {
      stop();
    }
  }));

test("while the stream is open, poll ticks reload nothing", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      const before = server.panesCalls;
      await sleep(intervalMs * 40);
      assert.equal(server.panesCalls, before, "a poll tick reloaded while the stream was open");
      assert.equal(server.watches.length, 1, "the stream was reopened while it was open");
    } finally {
      stop();
    }
  }));

// pollTakesOver ends the first stream with finish, then checks that the poll reloads and the stream reopens from the
// newest seq the next load reports.
async function pollTakesOver(finish: (watch: Watch) => void): Promise<void> {
  await withPage(async (root) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      assert.equal(server.watches[0].after, 7n, "the first stream did not resume from the loaded seq");
      server.storeSeq = 9n;
      const before = server.panesCalls;
      finish(server.watches[0]);
      await until(() => server.watches.length === 2, "the stream reopened");
      assert.ok(server.panesCalls > before, "the poll did not reload after the stream went down");
      assert.equal(server.watches[1].after, 9n, "the stream did not reopen from the newest seq");
    } finally {
      stop();
    }
  });
}

test("after the stream ends, the poll reloads and the stream reopens from the newest seq", () =>
  pollTakesOver(endStream));

test("after the stream fails, the poll reloads and the stream reopens from the newest seq", () =>
  pollTakesOver(failStream));

test("a load that reports seq 0 opens no stream, and the poll reloads until a load reports a seq", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    server.storeSeq = 0n;
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.panesCalls >= 3, "the poll's reloads");
      assert.equal(server.watches.length, 0, "a stream opened from seq 0");
      server.storeSeq = 9n;
      await until(() => server.watches.length === 1, "the stream after a load with a seq");
      assert.equal(server.watches[0].after, 9n);
    } finally {
      stop();
    }
  }));

test("stop aborts the open stream", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => server.watches.length === 1, "the stream");
      stop();
      assert.ok(server.watches[0].signal.aborted, "stop left the stream open");
    } finally {
      stop();
    }
  }));
