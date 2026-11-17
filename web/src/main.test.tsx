import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { test, type TestContext } from "node:test";
import { fileURLToPath } from "node:url";

import { createClients, createTransport } from "./client";
import { start as startPage } from "./main";
import { turn, until } from "./testwait";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const headlessPath = "/orrery.v1.Sessions/ListHeadless";
const watchPath = "/orrery.v1.Events/Watch";
const epicsListPath = "/orrery.v1.Epics/List";
const epicsGetPath = "/orrery.v1.Epics/Get";
const presencePath = "/orrery.v1.Presence/Report";
const noticesWatchPath = "/orrery.v1.Notices/Watch";
const hostStatusPath = "/orrery.v1.Host/Status";
const clientHeader = "X-Orrery-Client";
const clientValue = "orrery web";
const intervalMs = 5;
// The pause before a stream that failed unavailable reopens, short so a test that waits for the reopen does not wait long.
const retryMs = 5;
// A poll interval longer than the suite's --test-timeout lets a test run, so only events and the stream can reload the
// list while a test runs.
const longPollMs = 600_000;

// Synthetic fixtures: one pane and one headless session, no real hosts or paths.
function paneBody(dir: string) {
  return {
    panes: [
      {
        id: "%1",
        host: "box",
        session: "work",
        cwd: `/work/${dir}`,
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

// headlessSession is one headless session as Sessions.ListHeadless answers it, under a synthetic directory, with no
// headline yet, so its row reads its directory alone.
function headlessSession(id: string, base: string) {
  return {
    id,
    dir: `/work/${base}`,
    headline: "",
    state: "working",
    changed: "50",
    seen: "0",
    summary: "",
    question: "",
  };
}

// The host Host.Status answers with: one stat, so the meters button has a value to show.
const hostBody = {
  host: "den",
  stats: [{ label: "cpu", value: "12%", percent: 12, level: "ok" }],
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
  // The directory the next Panes.List answers its pane in, named by its last element.
  paneDir: string;
  // While set, Panes.List waits for it before answering.
  gate: Promise<void> | null;
  panesCalls: number;
  panesInFlight: number;
  maxPanesInFlight: number;
  // The store position Sessions.List reports with its sessions.
  storeSeq: bigint;
  // The headless sessions Sessions.ListHeadless answers with.
  headless: ReturnType<typeof headlessSession>[];
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
  // The Epics.Get calls the page has made, counted as each arrives.
  epicsGetCalls: number;
  epicsInFlight: number;
  maxEpicsInFlight: number;
  // While set, Host.Status answers with a server error.
  hostFails: boolean;
  // While set, Host.Status waits for it before answering.
  hostGate: Promise<void> | null;
  hostCalls: number;
  // Every path the page has called.
  paths: Set<string>;
}

// fakeServer answers the lists from its fields, so a test can change what the next reload shows. It starts with no
// epics, so a test that does not ask for one sees the same page as before epics existed. Without streaming,
// Events.Watch answers 404, so the stream is down and the poll alone reloads.
function fakeServer(paneDir: string, streaming = false): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      server.paths.add(path);
      if (path === sessionsPath) {
        return json({ ...sessionBody, seq: String(server.storeSeq) });
      }
      if (path === headlessPath) {
        return json({ sessions: server.headless });
      }
      if (path === watchPath && streaming) {
        const body = decodeRequest(new Uint8Array(await request.arrayBuffer()));
        const watch: Watch = {
          after: BigInt(String(body.after ?? "0")),
          // The signal the transport passed: a Request's own signal stops following it once the garbage collector
          // takes the Request.
          signal: init?.signal ?? request.signal,
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
        server.epicsGetCalls++;
        const body = (await request.json()) as { id?: string };
        const epic = server.epics.find((candidate) => candidate.id === body.id);
        if (epic === undefined) {
          return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
        }
        return json(epicDetail(epic));
      }
      if (path === hostStatusPath) {
        server.hostCalls++;
        if (server.hostGate) {
          await server.hostGate;
        }
        if (server.hostFails) {
          return new Response(JSON.stringify({ code: "internal", message: "host detail" }), {
            status: 500,
            headers: { "content-type": "application/json" },
          });
        }
        return json(hostBody);
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
        return json(paneBody(server.paneDir));
      } finally {
        server.panesInFlight--;
      }
    },
    paneDir,
    gate: null,
    panesCalls: 0,
    panesInFlight: 0,
    maxPanesInFlight: 0,
    storeSeq: 7n,
    headless: [],
    watches: [],
    reports: [],
    presenceFails: false,
    epics: [],
    epicsGate: null,
    epicsListCalls: 0,
    epicsGetCalls: 0,
    epicsInFlight: 0,
    maxEpicsInFlight: 0,
    hostFails: false,
    hostGate: null,
    hostCalls: 0,
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

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// nameOf names a session or pane row by its directory's last element, or by its line when it has no directory. An epic
// or step row, which draws no title, is named by its label.
function nameOf(row: Element): string {
  return (
    row.querySelector(".title-dir")?.textContent ??
    row.querySelector(".title-line")?.textContent ??
    row.querySelector(".label")?.textContent ??
    ""
  );
}

// labels names each row that is drawn, in the order drawn.
function labels(root: HTMLElement): string[] {
  return [...root.querySelectorAll("[data-row]")].map(nameOf).filter((name) => name !== "");
}

// ageOf returns the age text of the row named name, or "" when no such row is rendered.
function ageOf(root: HTMLElement, name: string): string {
  const rows = [...root.querySelectorAll("[data-row]")];
  const row = rows.find((element) => nameOf(element) === name);
  return row?.querySelector(".age")?.textContent ?? "";
}

// meterHost is the host the footer's meters button names, or null when the footer draws no button.
function meterHost(root: HTMLElement): string | null {
  return root.querySelector(".lfoot > .meters .meters-host")?.textContent ?? null;
}

// meterButtonShown is whether the footer draws the meters button.
function meterButtonShown(root: HTMLElement): boolean {
  return root.querySelector(".lfoot > .meters") !== null;
}

// errorText is the page's error line, or "" when it shows none.
function errorText(root: HTMLElement): string {
  return root.querySelector(".error")?.textContent ?? "";
}

// withPage runs check with a fresh DOM, a root element, and a start that starts a page and returns its stop. Every page
// that start started is stopped, and the DOM removed, when the test ends, or as soon as signal aborts if the test times
// out first, so a page never runs on into the next test (see teardownOnce). A stop is the page's only once.
async function withPage(
  signal: AbortSignal,
  check: (root: HTMLElement, start: typeof startPage) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const root = document.createElement("div");
  const stops: (() => void)[] = [];
  const start: typeof startPage = (...args) => {
    const stop = startPage(...args);
    let stopped = false;
    const once = () => {
      if (!stopped) {
        stopped = true;
        stop();
      }
    };
    stops.push(once);
    return once;
  };
  const teardown = teardownOnce(signal, async () => {
    for (const stop of stops) {
      stop();
    }
    await removeTestDom(window);
  });
  try {
    await check(root, start);
  } finally {
    await teardown();
  }
}

// sendEvent writes one event onto a watch's stream. The client only needs the event to arrive, not its content.
function sendEvent(watch: Watch, seq: number): void {
  enqueue(watch, 0, { seq: String(seq), sessionId: "%1" });
}

// sendEngineAdded sends the engine_added event of the headless session id in dir.
function sendEngineAdded(watch: Watch, seq: number, id: string, dir: string): void {
  enqueue(watch, 0, { seq: String(seq), sessionId: id, engineAdded: { dir, state: "working" } });
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

test("loaded rows render into the root", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the pane row");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("before the first load lands the root holds no rows", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    let release: () => void = () => {};
    server.gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      assert.deepEqual(labels(root), []);
      release();
      await until(t.signal, () => labels(root).includes("api"), "the pane row after release");
    } finally {
      stop();
    }
  }));

test("a reload shows the changed pane", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the first pane label");
      server.paneDir = "web";
      await until(t.signal, () => labels(root).includes("web") && !labels(root).includes("api"), "the renamed pane");
    } finally {
      stop();
    }
  }));

test("a failed Panes.List shows the fixed error and keeps the sessions", (t) =>
  withPage(t.signal, async (root, start) => {
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
      await until(t.signal, () => root.querySelector(".error") !== null, "the error line");
      assert.equal(root.querySelector(".error")?.textContent, "panes unavailable");
      assert.ok(labels(root).includes("notes"), `labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("a reload never starts while the last one is in flight", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.panesCalls >= 3, "three loads");
      assert.equal(server.maxPanesInFlight, 1);
    } finally {
      stop();
    }
  }));

test("a headless session from Sessions.ListHeadless is a row on the first load", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.headless = [headlessSession("170bf619aa00bb11cc22dd33ee44ff55", "gatehouse")];
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the first load");
      await until(t.signal, () => labels(root).includes("gatehouse"), "the headless session's row");
      assert.ok(server.paths.has(headlessPath), "the page never called Sessions.ListHeadless");
    } finally {
      stop();
    }
  }));

test("an engine_added on the open stream adds the new headless session's row without a poll", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.headless = [headlessSession("170bf619aa00bb11cc22dd33ee44ff55", "gatehouse")];
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(
        t.signal,
        () => server.watches.length === 1 && labels(root).includes("gatehouse"),
        "the first load and stream",
      );
      // The poll waits longPollMs, longer than the suite lets a test run, so the row this wait sees can only come from
      // the event's reload.
      const added = headlessSession("cfe64ee3aa00bb11cc22dd33ee44ff55", "orrery");
      server.headless = [...server.headless, added];
      sendEngineAdded(server.watches[0], 8, added.id, added.dir);
      await until(t.signal, () => labels(root).includes("orrery"), "the added session's row");
      assert.ok(labels(root).includes("gatehouse"), "the first headless session's row went away");
    } finally {
      stop();
    }
  }));

// childEnv marks the run of this file that holds a page open past its test's timeout. Only that run defines the two
// tests named with childPrefix, and it runs only those.
const childEnv = "ORRERY_MAIN_TEST_CHILD";
const childPrefix = "child run: ";
// childTimeoutMs is the child run's --test-timeout: its first test can only end by timing out, and its second, a page's
// first load, ends long before it.
const childTimeoutMs = 3000;

if (process.env[childEnv] === "1") {
  test(`${childPrefix}a page whose wait never ends`, (t) =>
    withPage(t.signal, async (root, start) => {
      start(root, clientsFor(fakeServer("api")), longPollMs);
      await until(t.signal, () => false, "a state that never comes");
    }));

  test(`${childPrefix}a page that loads after it`, (t) =>
    withPage(t.signal, async (root, start) => {
      start(root, clientsFor(fakeServer("api")), longPollMs);
      await until(t.signal, () => labels(root).includes("api"), "the first load");
    }));
}

test("a page whose test times out stops before the next test, which fails only on its own", async () => {
  // The forced order: in a child run, the first test's page is up when its test times out, and node:test starts the
  // second test at once. A page that ran on past its test would render into the second test's DOM, or remove it, and
  // fail the second test in the first one's place. The child must report the first test alone as failed.
  const { NODE_TEST_CONTEXT: _, ...env } = process.env;
  const child = spawn(
    process.execPath,
    [
      "--test",
      "--test-reporter=spec",
      `--test-timeout=${childTimeoutMs}`,
      `--test-name-pattern=^${childPrefix}`,
      fileURLToPath(import.meta.url),
    ],
    { env: { ...env, [childEnv]: "1" }, stdio: ["ignore", "pipe", "pipe"] },
  );
  let output = "";
  child.stdout.on("data", (chunk: Buffer) => {
    output += chunk.toString();
  });
  child.stderr.on("data", (chunk: Buffer) => {
    output += chunk.toString();
  });
  const code = await new Promise<number | null>((resolve) => child.on("close", resolve));
  assert.notEqual(code, 0, `the child run passed:\n${output}`);
  assert.ok(output.includes(`✖ ${childPrefix}a page whose wait never ends`), `the first test did not fail:\n${output}`);
  assert.ok(output.includes(`✔ ${childPrefix}a page that loads after it`), `the second test did not pass:\n${output}`);
  // node:test counts a test its timeout ends as cancelled, not failed: the first test is the run's one test that did not
  // pass, and the only one its failing list names.
  for (const count of ["ℹ pass 1\n", "ℹ fail 0\n", "ℹ cancelled 1\n"]) {
    assert.ok(output.includes(count), `the child's counts lack ${count.trim()}:\n${output}`);
  }
  const failing = output.slice(output.indexOf("failing tests:"));
  assert.ok(!failing.includes(`${childPrefix}a page that loads after it`), `the second test is listed as failing:\n${output}`);
});

test("stop unmounts the root and stops reloading", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), intervalMs);
    await until(t.signal, () => labels(root).includes("api"), "the first load");
    stop();
    assert.equal(root.childNodes.length, 0, "stop left nodes in the root");
    const callsAtStop = server.panesCalls;
    await sleep(intervalMs * 10);
    assert.equal(server.panesCalls, callsAtStop, "a reload ran after stop");
    assert.equal(root.childNodes.length, 0, "a reload rendered after stop");
  }));

test("an event on the open stream reloads without waiting for the poll", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      assert.equal(server.watches[0].header, clientValue, "the stream call lacked the client header");
      server.paneDir = "web";
      // The poll waits longPollMs, longer than the suite lets a test run, so the reload this wait sees can only be the
      // event's.
      sendEvent(server.watches[0], 8);
      await until(t.signal, () => labels(root).includes("web"), "the reload after the event");
    } finally {
      stop();
    }
  }));

test("a burst of events gives one reload", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      const before = server.panesCalls;
      for (let seq = 8; seq < 13; seq++) {
        sendEvent(server.watches[0], seq);
      }
      await until(t.signal, () => server.panesCalls > before, "the reload after the burst");
      await sleep(300);
      assert.equal(server.panesCalls, before + 1, "the burst gave more than one reload");
    } finally {
      stop();
    }
  }));

test("while the stream is open, poll ticks reload nothing", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      const before = server.panesCalls;
      await sleep(intervalMs * 40);
      assert.equal(server.panesCalls, before, "a poll tick reloaded while the stream was open");
      assert.equal(server.watches.length, 1, "the stream was reopened while it was open");
    } finally {
      stop();
    }
  }));

test("while the stream is open, poll ticks advance the age labels without reloading", (t) =>
  withPage(t.signal, async (root, start) => {
    // The clock starts 120 s after the fixture's changed (40 s), so the label reads minutes and not days.
    const realNow = Date.now;
    let offsetMs = 160_000 - realNow();
    Date.now = () => realNow() + offsetMs;
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
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
async function pollTakesOver(signal: AbortSignal, finish: (watch: Watch) => void): Promise<void> {
  await withPage(signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      assert.equal(server.watches[0].after, 7n, "the first stream did not resume from the loaded seq");
      server.storeSeq = 9n;
      const before = server.panesCalls;
      finish(server.watches[0]);
      await until(signal, () => server.watches.length === 2, "the stream reopened");
      assert.ok(server.panesCalls > before, "the poll did not reload after the stream went down");
      assert.equal(server.watches[1].after, 9n, "the stream did not reopen from the newest seq");
    } finally {
      stop();
    }
  });
}

test("after the stream ends, the poll reloads and the stream reopens from the newest seq", (t) =>
  pollTakesOver(t.signal, endStream));

test("after the stream fails with another code, the poll reloads and the stream reopens from the newest seq", (t) =>
  pollTakesOver(t.signal, (watch) => failStream(watch, "internal")));

test("after the stream fails unavailable, it reopens from the last seq it delivered, not from the poll", (t) =>
  withPage(t.signal, async (root, start) => {
    // longPollMs keeps the poll from running, so only the retry can reopen the stream.
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs, retryMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("api"), "the first load and stream");
      sendEvent(server.watches[0], 8);
      failStream(server.watches[0], "unavailable");
      await until(t.signal, () => server.watches.length === 2, "the stream reopened");
      assert.equal(server.watches[1].after, 8n, "the stream did not reopen from the last seq it delivered");
    } finally {
      stop();
    }
  }));

test("a load that reports seq 0 opens no stream, and the poll reloads until a load reports a seq", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.storeSeq = 0n;
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.panesCalls >= 3, "the poll's reloads");
      assert.equal(server.watches.length, 0, "a stream opened from seq 0");
      server.storeSeq = 9n;
      await until(t.signal, () => server.watches.length === 1, "the stream after a load with a seq");
      assert.equal(server.watches[0].after, 9n);
    } finally {
      stop();
    }
  }));

test("stop aborts the open stream", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => server.watches.length === 1, "the stream");
      stop();
      assert.ok(server.watches[0].signal.aborted, "stop left the stream open");
    } finally {
      stop();
    }
  }));

test("a live epic from Epics.List and Get shows in the list with its count", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => labels(root).includes("alpha") && labels(root).includes("api"), "the epic and the pane row");
      const epicRow = root.querySelector('[data-key="epic:repo-a/alpha"]');
      assert.ok(epicRow !== null, `no epic row; labels were ${JSON.stringify(labels(root))}`);
      assert.equal(epicRow.querySelector(".rt")?.textContent, "1 of 2 tasks done", "the count did not come from Epics.Get");
      assert.ok(labels(root).includes("draft"), `labels were ${JSON.stringify(labels(root))}`);
      assert.ok(labels(root).includes("api"), "the pane row is missing beside the epic");
    } finally {
      stop();
    }
  }));

test("the page calls only the list, get and watch paths it reads", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => labels(root).includes("alpha") && server.watches.length === 1, "the epic and the stream");
      assert.deepEqual(
        [...server.paths].sort(),
        [
          epicsGetPath,
          epicsListPath,
          "/orrery.v1.Epics/History",
          hostStatusPath,
          noticesWatchPath,
          panesPath,
          sessionsPath,
          headlessPath,
          watchPath,
        ].sort(),
      );
    } finally {
      stop();
    }
  }));

test("with the event stream open, a poll tick reloads the epics and shows a changed step", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.watches.length === 1 && labels(root).includes("draft"), "the first step and the stream");
      const panesBefore = server.panesCalls;
      server.epics = [liveEpic("review")];
      await until(t.signal, () => labels(root).includes("review") && !labels(root).includes("draft"), "the changed step");
      assert.equal(server.panesCalls, panesBefore, "the tick reloaded the rows while the stream was open");
      assert.equal(server.watches.length, 1, "the stream was reopened while it was open");
    } finally {
      stop();
    }
  }));

test("a failed Epics.List shows the fixed epics error and no epics, beside the rows", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const clients = createClients(
      createTransport({ baseUrl: "https://orrery.test", fetch: failing(server, [epicsListPath]) }),
    );
    const stop = start(root, clients, intervalMs);
    try {
      await until(t.signal, 
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

test("a failed Panes.List and a failed Epics.List join their fixed errors in that order", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const clients = createClients(
      createTransport({ baseUrl: "https://orrery.test", fetch: failing(server, [panesPath, epicsListPath]) }),
    );
    const stop = start(root, clients, intervalMs);
    try {
      await until(t.signal, 
        () => root.querySelector(".error")?.textContent === "panes unavailable; epics unavailable",
        "the joined error line",
      );
    } finally {
      stop();
    }
  }));

test("a second tick during a slow epics load starts no second load", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    let release: () => void = () => {};
    server.epicsGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.epicsListCalls === 1, "the first epics load");
      await until(t.signal, () => server.watches.length === 1, "the stream while epics are loading");
      await sleep(intervalMs * 40);
      assert.equal(server.epicsListCalls, 1, "a tick started a second epics load while the first was in flight");
      assert.equal(server.maxEpicsInFlight, 1);
      release();
      await until(t.signal, () => labels(root).includes("alpha"), "the epic after release");
    } finally {
      release();
      stop();
    }
  }));

// The ChangedEpicsOnly tests mock the page's timers, so each fires only when its test advances the clock. Advancing
// pollMs fires the poll once. Advancing eventWaitMs runs the page's event reload wait, which is shorter, and stays short
// of the poll's next firing. mockedRetryMs is out of reach of every advance these tests make.
const pollMs = 60_000;
const eventWaitMs = 1_000;
const mockedRetryMs = 600_000;

// endedBeta is a folded epic that ended before its last seen, so the page lists it as just ended. No load asks for it.
const endedBeta = {
  id: "repo-a/beta",
  repository: "repo-a",
  slug: "beta",
  outcome: "released",
  changed: "50",
  live: false,
  runningSteps: [],
  endedSteps: 0,
  phaseAt: "5",
  seen: "0",
};

// fireEvent sends an event on the watch, lets the page read it, which starts its event reload wait, and fires that wait.
async function fireEvent(t: TestContext, watch: Watch, seq: number): Promise<void> {
  sendEvent(watch, seq);
  await turn();
  t.mock.timers.tick(eventWaitMs);
}

test("ChangedEpicsOnly: with the event stream open, an event reload over an unchanged Epics.List makes no Epics.Get call", (t) =>
  withPage(t.signal, async (root, start) => {
    t.mock.timers.enable({ apis: ["setInterval", "setTimeout"] });
    const server = fakeServer("api", true);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    try {
      await until(
        t.signal,
        () => server.watches.length === 1 && labels(root).includes("draft"),
        "the first load and stream",
        { step: turn },
      );
      const gets = server.epicsGetCalls;
      // The event's list also holds a folded epic, which no load asks for. Its label draws only once the event's epics
      // load has drawn, and a load draws only after its Gets have answered, so the count below is final by then.
      server.epics = [liveEpic("draft"), endedBeta];
      await fireEvent(t, server.watches[0], 8);
      await until(t.signal, () => labels(root).includes("beta"), "the event's epics load", { step: turn });
      assert.equal(server.epicsGetCalls, gets, "an event reload over an unchanged list asked for an epic");
    } finally {
      stop();
    }
  }));

// tickFetchesEveryLiveEpic fires one poll tick on a page with two live epics, and checks that the tick lists the epics
// once and asks for each live epic once.
async function tickFetchesEveryLiveEpic(t: TestContext, streaming: boolean): Promise<void> {
  await withPage(t.signal, async (root, start) => {
    t.mock.timers.enable({ apis: ["setInterval", "setTimeout"] });
    const server = fakeServer("api", streaming);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft"), { ...liveEpic("draft"), id: "repo-a/beta", slug: "beta" }];
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    try {
      await until(
        t.signal,
        () => labels(root).includes("alpha") && labels(root).includes("beta"),
        "both epics",
        { step: turn },
      );
      const lists = server.epicsListCalls;
      const gets = server.epicsGetCalls;
      t.mock.timers.tick(pollMs);
      await until(t.signal, () => server.epicsGetCalls >= gets + 2, "the tick's Epics.Get calls", { step: turn });
      assert.equal(server.epicsListCalls, lists + 1, "the tick listed the epics more than once");
      assert.equal(server.epicsGetCalls, gets + 2, "the tick did not ask for each live epic once");
    } finally {
      stop();
    }
  });
}

test("ChangedEpicsOnly: a poll tick with the event stream open makes one Epics.Get per live epic", (t) =>
  tickFetchesEveryLiveEpic(t, true));

test("ChangedEpicsOnly: a poll tick with the event stream down makes one Epics.Get per live epic", (t) =>
  tickFetchesEveryLiveEpic(t, false));

test("ChangedEpicsOnly: a tick that lands while an epics load is in flight makes the next load fetch every live epic", (t) =>
  withPage(t.signal, async (root, start) => {
    t.mock.timers.enable({ apis: ["setInterval", "setTimeout"] });
    const server = fakeServer("api", true);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    let release: () => void = () => {};
    try {
      await until(
        t.signal,
        () => server.watches.length === 1 && labels(root).includes("draft"),
        "the first load and stream",
        { step: turn },
      );
      // The event's epics load is held in flight, and the list it answers with has a changed step, so it will fetch.
      server.epicsGate = new Promise<void>((resolve) => {
        release = resolve;
      });
      server.epics = [liveEpic("review")];
      await fireEvent(t, server.watches[0], 8);
      await until(
        t.signal,
        () => server.epicsListCalls === 2 && server.epicsInFlight === 1,
        "the held epics load",
        { step: turn },
      );
      // A tick lands while that load is in flight, and starts no load of its own.
      t.mock.timers.tick(pollMs);
      await turn();
      assert.equal(server.epicsListCalls, 2, "a tick started a second epics load while one was in flight");
      release();
      await until(t.signal, () => labels(root).includes("review"), "the held load's changed step", { step: turn });

      // The next event lists the same epics. Only the tick that landed mid-load makes it fetch the epic again.
      const gets = server.epicsGetCalls;
      await fireEvent(t, server.watches[0], 9);
      await until(t.signal, () => server.epicsGetCalls >= gets + 1, "the next load's Epics.Get", { step: turn });
      assert.equal(server.epicsGetCalls, gets + 1, "the next load did not fetch every live epic");
    } finally {
      release();
      stop();
    }
  }));

test("a keydown reports the input age to Presence.Report with the client header", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the first load");
      document.dispatchEvent(new KeyboardEvent("keydown"));
      // The keydown sends at once. The page may send one more report when App's effect first names the cursor's view,
      // which can land after the keydown, so the test waits for a report rather than for exactly one.
      await until(t.signal, () => server.reports.length >= 1, "the report after the keydown");
      for (const report of server.reports) {
        assert.equal(report.header, clientValue, "a report lacked the client header");
        assert.match(report.inputAgeMs, /^\d+$/, "a report did not carry an age in milliseconds");
      }
    } finally {
      stop();
    }
  }));

// No report has gone out before stop, so a live watch would send this keydown at once.
test("a keydown after stop reports nothing", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the first load");
      stop();
      document.dispatchEvent(new KeyboardEvent("keydown"));
      await sleep(intervalMs * 10);
      assert.equal(server.reports.length, 0, "a keydown after stop reported");
    } finally {
      stop();
    }
  }));

test("a failed Presence.Report leaves the page without an error line and keeps the rows", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.presenceFails = true;
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => labels(root).includes("api"), "the first load");
      document.dispatchEvent(new KeyboardEvent("keydown"));
      // As above, a report naming the view may follow the keydown's; the test needs the first to have failed.
      await until(t.signal, () => server.reports.length >= 1, "the failed report");
      await sleep(intervalMs * 10);
      assert.equal(root.querySelector(".error")?.textContent ?? "", "", "a failed report showed an error line");
      assert.ok(labels(root).includes("api"), "the rows went away after a failed report");
    } finally {
      stop();
    }
  }));

test("the start read shows the host in the footer's meters button, before any poll tick", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    // longPollMs means no tick runs, so only the read at start can show the button.
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      await until(t.signal, () => meterHost(root) === "den", "the meters button for the host");
    } finally {
      stop();
    }
  }));

test("a refused Host.Status shows no meters button and no error, while the rows, epics and stream load", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.hostFails = true;
    server.epics = [liveEpic("draft")];
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, 
        () => labels(root).includes("api") && labels(root).includes("alpha") && server.watches.length === 1,
        "the rows, the epics and the stream",
      );
      // A read starts only once the one before it is handled, so the second read after the stream opens means a refusal
      // from after it has landed. No reload runs with the stream open, so nothing clears the error line after that.
      const readsAtOpen = server.hostCalls;
      await until(t.signal, () => server.hostCalls >= readsAtOpen + 2, "two refused reads with the stream open");
      assert.equal(meterButtonShown(root), false, "a refused Host.Status drew a meters button");
      assert.equal(errorText(root), "", "a refused Host.Status showed in the error line");
    } finally {
      stop();
    }
  }));

test("a Host.Status that starts failing takes the meters button away", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => meterHost(root) === "den", "the meters button");
      server.hostFails = true;
      await until(t.signal, () => !meterButtonShown(root), "the meters button gone after a refused read");
    } finally {
      stop();
    }
  }));

test("each poll tick reads Host.Status while the stream is down", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.storeSeq = 0n;
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.hostCalls >= 2 && server.watches.length === 0, "ticks with the stream down");
    } finally {
      stop();
    }
  }));

test("a tick during a slow Host.Status starts no second read", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    let release: () => void = () => {};
    server.hostGate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => server.hostCalls === 1, "the first read");
      // With the stream down every tick reloads the rows, so three Panes.List calls mean at least two ticks ran.
      const panesAtFirstRead = server.panesCalls;
      await until(t.signal, () => server.panesCalls >= panesAtFirstRead + 3, "ticks while the first read is in flight");
      assert.equal(server.hostCalls, 1, "a tick started a second read while the first was in flight");
      release();
      await until(t.signal, () => meterHost(root) === "den", "the host after release");
    } finally {
      release();
      stop();
    }
  }));
