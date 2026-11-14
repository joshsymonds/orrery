import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { start } from "./main";
import { installTestDom, removeTestDom } from "./testdom";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const watchPath = "/orrery.v1.Events/Watch";
const epicsListPath = "/orrery.v1.Epics/List";
const epicsGetPath = "/orrery.v1.Epics/Get";
const presencePath = "/orrery.v1.Presence/Report";
const clientHeader = "X-Orrery-Client";
const clientValue = "orrery web";
const intervalMs = 5;
// The pause before a stream that failed unavailable reopens, short so the tests' waits stay well inside their deadline.
const retryMs = 5;
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

// One live epic with one running step, as Epics.List answers it. The step's task is the label a test can change.
function liveEpic(task: string) {
  return {
    id: "repo-a/alpha",
    repository: "repo-a",
    slug: "alpha",
    outcome: "",
    changed: "100",
    live: true,
    runningSteps: [{ id: "step-1", task, agent: "agent-a" }],
    endedSteps: 0,
  };
}

// Epics.Get's answer for an epic: one done task and one pending, so the count reads 1 of 2.
function epicDetail(epic: ReturnType<typeof liveEpic>) {
  return {
    epic,
    tasks: [
      { effort: 1, slug: "a", subject: "first", status: "done" },
      { effort: 1, slug: "b", subject: "second", status: "pending" },
    ],
    steps: [],
  };
}

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
  // The Presence.Report calls, in order: the client header each carried and the input age it sent.
  reports: { header: string | null; inputAgeMs: string }[];
  // While set, Presence.Report answers with a server error.
  presenceFails: boolean;
  // The epics Epics.List answers with. Epics.Get answers the one whose id it is asked for, and 404s any other.
  epics: ReturnType<typeof liveEpic>[];
  // While set, Epics.List waits for it before answering.
  epicsGate: Promise<void> | null;
  epicsListCalls: number;
  epicsInFlight: number;
  maxEpicsInFlight: number;
  // Every path the page has called.
  paths: Set<string>;
}

// fakeServer answers the lists from its fields, so a test can change what the next reload shows. It starts with no
// epics, so a test that does not ask for one sees the same page as before epics existed. Without streaming,
// Events.Watch answers 404, so the stream is down and the poll alone reloads.
function fakeServer(paneLabel: string, streaming = false): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      server.paths.add(path);
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
      if (path === presencePath) {
        // proto3 JSON omits a zero age, so an absent inputAgeMs is "0".
        const body = (await request.json()) as { inputAgeMs?: string };
        server.reports.push({
          header: request.headers.get(clientHeader),
          inputAgeMs: body.inputAgeMs ?? "0",
        });
        if (server.presenceFails) {
          return new Response(JSON.stringify({ code: "internal", message: "presence detail" }), {
            status: 500,
            headers: { "content-type": "application/json" },
          });
        }
        return json({});
      }
      if (path === epicsListPath) {
        server.epicsListCalls++;
        server.epicsInFlight++;
        server.maxEpicsInFlight = Math.max(server.maxEpicsInFlight, server.epicsInFlight);
        try {
          if (server.epicsGate) {
            await server.epicsGate;
          }
          return json({ epics: server.epics });
        } finally {
          server.epicsInFlight--;
        }
      }
      if (path === epicsGetPath) {
        const body = (await request.json()) as { id?: string };
        const epic = server.epics.find((candidate) => candidate.id === body.id);
        if (epic === undefined) {
          return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
        }
        return json(epicDetail(epic));
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
    reports: [],
    presenceFails: false,
    epics: [],
    epicsGate: null,
    epicsListCalls: 0,
    epicsInFlight: 0,
    maxEpicsInFlight: 0,
    paths: new Set(),
  };
  return server;
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

// failing answers each path in failed with a server error that carries server text, and every other call from server.
function failing(server: FakeServer, failed: string[]): typeof globalThis.fetch {
  return async (input, init) => {
    const path = new URL(new Request(input, init).url).pathname;
    if (failed.includes(path)) {
      return new Response(JSON.stringify({ code: "internal", message: "server detail" }), {
        status: 500,
        headers: { "content-type": "application/json" },
      });
    }
    return server.fetch(input, init);
  };
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

// ageOf returns the age text of the row labelled name, or "" when no such row is rendered.
function ageOf(root: HTMLElement, name: string): string {
  const rows = [...root.querySelectorAll("[data-row]")];
  const row = rows.find((element) => element.querySelector(".label")?.textContent === name);
  return row?.querySelector(".age")?.textContent ?? "";
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

function failStream(watch: Watch, code: string): void {
  finishStream(watch, { error: { code, message: "stream failed" } });
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

test("while the stream is open, poll ticks advance the age labels without reloading", () =>
  withPage(async (root) => {
    // The clock starts 120 s after the fixture's changed (40 s), so the label reads minutes and not days.
    const realNow = Date.now;
    let offsetMs = 160_000 - realNow();
    Date.now = () => realNow() + offsetMs;
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      assert.equal(ageOf(root, "api"), "2m", "the first load did not show the minutes label");
      const before = server.panesCalls;
      offsetMs += 300_000;
      await sleep(intervalMs * 40);
      assert.equal(ageOf(root, "api"), "7m", "the age label did not advance while the stream was open");
      assert.equal(server.panesCalls, before, "a poll tick reloaded while the stream was open");
    } finally {
      stop();
      Date.now = realNow;
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

test("after the stream fails with another code, the poll reloads and the stream reopens from the newest seq", () =>
  pollTakesOver((watch) => failStream(watch, "internal")));

test("after the stream fails unavailable, it reopens from the last seq it delivered, not from the poll", () =>
  withPage(async (root) => {
    // longPollMs keeps the poll from running, so only the retry can reopen the stream.
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs, retryMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      sendEvent(server.watches[0], 8);
      failStream(server.watches[0], "unavailable");
      await until(() => server.watches.length === 2, "the stream reopened");
      assert.equal(server.watches[1].after, 8n, "the stream did not reopen from the last seq it delivered");
    } finally {
      stop();
    }
  }));

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

test("a live epic from Epics.List and Get shows in the list with its count", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("alpha") && labels(root).includes("api"), "the epic and the pane row");
      const epicRow = root.querySelector('[data-key="epic:repo-a/alpha"]');
      assert.ok(epicRow !== null, `no epic row; labels were ${JSON.stringify(labels(root))}`);
      assert.equal(epicRow.querySelector(".rt")?.textContent, "1 of 2", "the count did not come from Epics.Get");
      assert.ok(labels(root).includes("draft"), `labels were ${JSON.stringify(labels(root))}`);
      assert.ok(labels(root).includes("api"), "the pane row is missing beside the epic");
    } finally {
      stop();
    }
  }));

test("the page calls only the list, get and watch paths it reads", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => labels(root).includes("alpha") && server.watches.length === 1, "the epic and the stream");
      assert.deepEqual(
        [...server.paths].sort(),
        [epicsGetPath, epicsListPath, panesPath, sessionsPath, watchPath].sort(),
      );
    } finally {
      stop();
    }
  }));

test("with the event stream open, a poll tick reloads the epics and shows a changed step", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.watches.length === 1 && labels(root).includes("draft"), "the first step and the stream");
      const panesBefore = server.panesCalls;
      server.epics = [liveEpic("review")];
      await until(() => labels(root).includes("review") && !labels(root).includes("draft"), "the changed step");
      assert.equal(server.panesCalls, panesBefore, "the tick reloaded the rows while the stream was open");
      assert.equal(server.watches.length, 1, "the stream was reopened while it was open");
    } finally {
      stop();
    }
  }));

test("a failed Epics.List shows the fixed epics error and no epics, beside the rows", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const clients = createClients(
      createTransport({ baseUrl: "https://orrery.test", fetch: failing(server, [epicsListPath]) }),
    );
    const stop = start(root, clients, intervalMs);
    try {
      await until(
        () => root.querySelector(".error") !== null && labels(root).includes("api"),
        "the error line and the pane row",
      );
      assert.equal(root.querySelector(".error")?.textContent, "epics unavailable");
      assert.ok(!(root.textContent ?? "").includes("server detail"), "the page showed server text");
      assert.ok(!labels(root).includes("alpha"), `an epic was drawn; labels were ${JSON.stringify(labels(root))}`);
      assert.ok(labels(root).includes("api"), `the pane row is missing; labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("a failed Panes.List and a failed Epics.List join their fixed errors in that order", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const clients = createClients(
      createTransport({ baseUrl: "https://orrery.test", fetch: failing(server, [panesPath, epicsListPath]) }),
    );
    const stop = start(root, clients, intervalMs);
    try {
      await until(
        () => root.querySelector(".error")?.textContent === "panes unavailable; epics unavailable",
        "the joined error line",
      );
    } finally {
      stop();
    }
  }));

test("a second tick during a slow epics load starts no second load", () =>
  withPage(async (root) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    let release: () => void = () => {};
    server.epicsGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(() => server.epicsListCalls === 1, "the first epics load");
      await until(() => server.watches.length === 1, "the stream while epics are loading");
      await sleep(intervalMs * 40);
      assert.equal(server.epicsListCalls, 1, "a tick started a second epics load while the first was in flight");
      assert.equal(server.maxEpicsInFlight, 1);
      release();
      await until(() => labels(root).includes("alpha"), "the epic after release");
    } finally {
      release();
      stop();
    }
  }));

test("a keydown reports the input age to Presence.Report with the client header", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => labels(root).includes("api"), "the first load");
      document.dispatchEvent(new KeyboardEvent("keydown"));
      await until(() => server.reports.length === 1, "the report after the keydown");
      assert.equal(server.reports[0].header, clientValue, "the report lacked the client header");
      assert.match(server.reports[0].inputAgeMs, /^\d+$/, "the report did not carry an age in milliseconds");
    } finally {
      stop();
    }
  }));

// No report has gone out before stop, so a live watch would send this keydown at once.
test("a keydown after stop reports nothing", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => labels(root).includes("api"), "the first load");
      stop();
      document.dispatchEvent(new KeyboardEvent("keydown"));
      await sleep(intervalMs * 10);
      assert.equal(server.reports.length, 0, "a keydown after stop reported");
    } finally {
      stop();
    }
  }));

test("a failed Presence.Report leaves the page without an error line and keeps the rows", () =>
  withPage(async (root) => {
    const server = fakeServer("api");
    server.presenceFails = true;
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(() => labels(root).includes("api"), "the first load");
      document.dispatchEvent(new KeyboardEvent("keydown"));
      await until(() => server.reports.length === 1, "the failed report");
      await sleep(intervalMs * 10);
      assert.equal(root.querySelector(".error")?.textContent ?? "", "", "a failed report showed an error line");
      assert.ok(labels(root).includes("api"), "the rows went away after a failed report");
    } finally {
      stop();
    }
  }));
