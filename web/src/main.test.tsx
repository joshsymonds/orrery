import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { test, type TestContext } from "node:test";
import { fileURLToPath } from "node:url";

import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import { start as startPage } from "./main";
import { mockTimers } from "./testclock";
import { turn, until } from "./testwait";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const panesPath = "/orrery.v1.Panes/List";
const sessionsPath = "/orrery.v1.Sessions/List";
const headlessPath = "/orrery.v1.Sessions/ListHeadless";
const watchPath = "/orrery.v1.Events/Watch";
const epicsListPath = "/orrery.v1.Epics/List";
const epicsGetPath = "/orrery.v1.Epics/Get";
const presencePath = "/orrery.v1.Presence/Report";
const noticesListPath = "/orrery.v1.Notices/List";
const noticesWatchPath = "/orrery.v1.Notices/Watch";
const hostStatusPath = "/orrery.v1.Host/Status";
const contractsListPath = "/orrery.v1.Contracts/List";
const clientHeader = "X-Orrery-Client";
const clientValue = "orrery web";
const intervalMs = 5;
// The pause before a stream that failed unavailable reopens, short so a test that waits for the reopen does not wait long.
const retryMs = 5;
// A poll interval longer than the suite's --test-timeout lets a test run, so only events and the stream can reload the
// list while a test runs.
const longPollMs = 600_000;

// Synthetic fixtures: one pane and one headless session, no real hosts or paths.
// paneOf is one pane on the box host, as Panes.List answers it, under the synthetic directory dir.
function paneOf(id: string, dir: string) {
  return {
    id,
    host: "box",
    session: "work",
    cwd: `/work/${dir}`,
    state: "waiting",
    summary: dir,
    question: "",
    changed: "40",
    seen: "10",
  };
}

function paneBody(dir: string) {
  return { panes: [paneOf("%1", dir)] };
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

// headlessSession is one headless session as Sessions.ListHeadless answers it, under a synthetic directory, whose headline
// is base, so its row is titled base.
function headlessSession(id: string, base: string) {
  return {
    id,
    dir: `/work/${base}`,
    headline: base,
    state: "working",
    changed: "50",
    seen: "0",
    summary: "",
    question: "",
  };
}

// proposedContract is a contract Contracts.List lists as proposed, written by the conversation whose id it names.
function proposedContract(id: string, conversation: string) {
  return { id, repository: "/work/api", title: `Contract ${id}`, care: "serious", state: "proposed", conversation };
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
  // The panes Panes.List answers after the pane paneBody gives, for a test that needs more rows.
  extraPanes: ReturnType<typeof paneOf>[];
  // The epics Epics.List answers with. Epics.Get answers the one whose id it is asked for, and 404s any other.
  epics: ReturnType<typeof liveEpic>[];
  // The contract each Epics.Get answers with, for a test whose epic page must show one.
  contract: { title: string; approved: string; intent: string } | undefined;
  // The contracts Contracts.List answers with, and how many times it was asked.
  contracts: ReturnType<typeof proposedContract>[];
  contractsListCalls: number;
  // While set, Epics.List waits for it before answering.
  epicsGate: Promise<void> | null;
  epicsListCalls: number;
  // The Epics.Get calls the page has made, counted as each arrives.
  epicsGetCalls: number;
  // The id each Epics.Get call asked for, in the order the calls arrived.
  epicsGetIds: string[];
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
      if (path === contractsListPath) {
        server.contractsListCalls++;
        return json({ contracts: server.contracts });
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
        server.epicsGetIds.push(body.id ?? "");
        const epic = server.epics.find((candidate) => candidate.id === body.id);
        if (epic === undefined) {
          return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
        }
        return json({ ...epicDetail(epic), contract: server.contract });
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
        return json({ panes: [...paneBody(server.paneDir).panes, ...server.extraPanes] });
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
    extraPanes: [],
    epics: [],
    contract: undefined,
    contracts: [],
    contractsListCalls: 0,
    epicsGate: null,
    epicsListCalls: 0,
    epicsGetCalls: 0,
    epicsGetIds: [],
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

// nameOf names a session or pane row by its title, which is its label. An epic or step row, which draws no title, is
// named by its label.
function nameOf(row: Element): string {
  return row.querySelector(".title")?.textContent ?? row.querySelector(".label")?.textContent ?? "";
}

// endedCount is how many ended conversations the drawn projects hold behind their ended lines, which read "N ended".
function endedCount(root: HTMLElement): number {
  return [...root.querySelectorAll(".dir .fold-line")].reduce((sum, line) => {
    const match = /^(\d+) ended$/.exec(line.textContent ?? "");
    return sum + (match === null ? 0 : Number(match[1]));
  }, 0);
}

// labels names each row that is drawn, in the order drawn.
function labels(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".row[data-key]:not(.standin)")].map(nameOf).filter((name) => name !== "");
}

// blankText is the text of the inbox's body state, loading or the server's silence or nothing listed, or "" when the
// body draws none.
function blankText(root: HTMLElement): string {
  return root.querySelector(".inbox-rows .blank")?.textContent ?? "";
}

// tryNow is the Try now button the server's silence draws, or null when none is drawn.
function tryNow(root: HTMLElement): HTMLButtonElement | null {
  return [...root.querySelectorAll<HTMLButtonElement>(".inbox-rows .blank button")].find((button) =>
    (button.textContent ?? "").includes("Try now"),
  ) ?? null;
}

// ageOf returns the age text of the row named name, or "" when no such row is rendered.
function ageOf(root: HTMLElement, name: string): string {
  const rows = [...root.querySelectorAll(".row[data-key]:not(.standin)")];
  const row = rows.find((element) => nameOf(element) === name);
  return row?.querySelector(".age")?.textContent ?? "";
}

// meterHost is the host the strip's host part names, or null when no strip draws one.
function meterHost(root: HTMLElement): string | null {
  return root.querySelector(".pane-strip-host")?.textContent ?? null;
}

// meterButtonShown is whether a strip draws its host part.
function meterButtonShown(root: HTMLElement): boolean {
  return root.querySelector(".pane-strip-host") !== null;
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
  check: (root: HTMLElement, start: typeof startPage, view: ReturnType<typeof installTestDom>) => Promise<void>,
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
    await check(root, start, window);
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

test("InboxTree: a failed Panes.List draws the server did not answer and Try now in place of the tree", (t) =>
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
      await until(t.signal, () => blankText(root).includes("The server did not answer."), "the server did not answer");
      assert.ok(blankText(root).includes("Trying again every few seconds."), "the error does not say it retries");
      assert.ok(tryNow(root) !== null, "the error offers no Try now");
      assert.deepEqual(labels(root), [], "the tree was drawn beside the error");
      assert.equal(root.querySelector('[role="alert"]'), null, "a fixed error line was drawn");
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
// childTimeoutMs is the child run's --test-timeout, short, as testwait's is: its first test can only end by timing out,
// and its second, a page's first load against a fake server, takes milliseconds.
const childTimeoutMs = 300;

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

// The test waits for a child node run, which takes longer than the suite's 5 s limit under load.
test("a page whose test times out stops before the next test, which fails only on its own", { timeout: 30_000 }, async () => {
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
      assert.ok(epicRow.querySelector(".l2")?.textContent?.includes("1 of 2 tasks done"), "the count did not come from Epics.Get");
      assert.ok(labels(root).includes("draft"), `labels were ${JSON.stringify(labels(root))}`);
      assert.ok(labels(root).includes("api"), "the pane row is missing beside the epic");
    } finally {
      stop();
    }
  }));

test("InboxTree: a proposed contract from Contracts.List hangs under its conversation, and stays when a later Contracts.List fails", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.headless = [headlessSession("s-1", "draft")];
    server.contracts = [proposedContract("tide-alerts", "s-1")];
    let contractsFail = false;
    const fetch: typeof globalThis.fetch = (input, init) =>
      contractsFail ? failing(server, [contractsListPath])(input, init) : server.fetch(input, init);
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch }));
    const stop = start(root, clients, intervalMs);
    try {
      const drawn = () => root.querySelector('[data-key="contract:tide-alerts"]') !== null;
      const listed = () => root.querySelector('[data-key="session:s-1"]') !== null;
      await until(t.signal, () => drawn() && listed(), "the contract and its conversation");
      const steers = root.querySelector('[data-key="contract:tide-alerts"]')?.closest(".steers");
      assert.equal(steers?.previousElementSibling?.getAttribute("data-key"), "session:s-1", "the contract hangs elsewhere");
      contractsFail = true;
      const asked = server.epicsListCalls;
      await until(t.signal, () => server.epicsListCalls >= asked + 2, "two epics loads with Contracts.List failing");
      assert.ok(drawn(), "the held contract left the inbox when Contracts.List failed");
      assert.equal(root.querySelector(".error"), null, "a failed Contracts.List drew an error line");
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
          "/orrery.v1.Contracts/List",
          epicsGetPath,
          epicsListPath,
          "/orrery.v1.Epics/History",
          hostStatusPath,
          noticesListPath,
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

test("InboxTree: a failed Epics.List draws the server did not answer and no epics or rows", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const clients = createClients(
      createTransport({ baseUrl: "https://orrery.test", fetch: failing(server, [epicsListPath]) }),
    );
    const stop = start(root, clients, intervalMs);
    try {
      await until(t.signal, () => blankText(root).includes("The server did not answer."), "the server did not answer");
      assert.ok(tryNow(root) !== null, "the error offers no Try now");
      assert.ok(!(root.textContent ?? "").includes("server detail"), "the page showed server text");
      assert.deepEqual(labels(root), [], `the tree was drawn; labels were ${JSON.stringify(labels(root))}`);
    } finally {
      stop();
    }
  }));

test("InboxTree: a failed Panes.List and a failed Epics.List each keep the error until its own load answers", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const failedPaths = [panesPath, epicsListPath];
    const fetch: typeof globalThis.fetch = (input, init) => failing(server, failedPaths)(input, init);
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch }));
    const stop = start(root, clients, intervalMs);
    try {
      await until(t.signal, () => blankText(root).includes("The server did not answer."), "the server did not answer");
      failedPaths.splice(0, 1);
      // A rows load starts only once the one before it has drawn, so a second answered Panes.List means the first
      // answer's draw has run.
      await until(t.signal, () => server.panesCalls >= 2, "a Panes.List that answered and drew, and the next one");
      assert.ok(blankText(root).includes("The server did not answer."), "the error cleared while Epics.List still failed");
      assert.deepEqual(labels(root), [], "the tree was drawn while Epics.List still failed");
      failedPaths.splice(0, 1);
      await until(t.signal, () => labels(root).includes("api") && labels(root).includes("alpha"), "the tree after both answer");
      assert.equal(blankText(root), "", "the error stayed drawn once both answered");
    } finally {
      stop();
    }
  }));

test("InboxTree: before the epics answer the first time the inbox says loading and draws no tree, though the rows have answered", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    server.epics = [liveEpic("draft")];
    let releaseEpics: () => void = () => {};
    server.epicsGate = new Promise<void>((resolve) => {
      releaseEpics = resolve;
    });
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      assert.equal(blankText(root), "loading…", "the inbox did not say loading before any answer");
      // A rows load that answers opens the event stream after it draws, so the stream is the sync on the rows' answer.
      await until(t.signal, () => server.watches.length === 1, "the stream the rows' answer opens");
      assert.equal(blankText(root), "loading…", "the inbox drew the tree before the epics answered");
      assert.deepEqual(labels(root), [], "the tree was drawn before the epics answered");
      releaseEpics();
      await until(t.signal, () => labels(root).includes("alpha"), "the epic once the epics answer");
      assert.equal(blankText(root), "", "the inbox still says loading once both answered");
    } finally {
      stop();
    }
  }));

test("InboxTree: Try now calls the rows and epics loads at once, and the tree returns once they answer", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("draft")];
    const failedPaths = [panesPath, epicsListPath];
    const asked: string[] = [];
    const fetch: typeof globalThis.fetch = (input, init) => {
      asked.push(new URL(new Request(input, init).url).pathname);
      return failing(server, failedPaths)(input, init);
    };
    const callsOf = (path: string) => asked.filter((asker) => asker === path).length;
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch }));
    const stop = start(root, clients, longPollMs);
    try {
      await until(t.signal, () => tryNow(root) !== null, "the error with Try now");
      const panesBefore = callsOf(panesPath);
      const epicsBefore = callsOf(epicsListPath);
      tryNow(root)?.click();
      await until(
        t.signal,
        () => callsOf(panesPath) > panesBefore && callsOf(epicsListPath) > epicsBefore,
        "Try now asking both lists",
      );
      assert.equal(callsOf(panesPath), panesBefore + 1, "Try now asked Panes.List more than once");
      assert.equal(callsOf(epicsListPath), epicsBefore + 1, "Try now asked Epics.List more than once");
      assert.ok(blankText(root).includes("The server did not answer."), "the error cleared while the lists still failed");
      failedPaths.length = 0;
      tryNow(root)?.click();
      await until(t.signal, () => labels(root).includes("api") && labels(root).includes("alpha"), "the tree after Try now");
      assert.equal(blankText(root), "", "the error stayed drawn after both lists answered");
    } finally {
      stop();
    }
  }));

test("InboxTree: nothing listed draws Nothing is running.", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    const fetch: typeof globalThis.fetch = async (input, init) => {
      const path = new URL(new Request(input, init).url).pathname;
      if (path === panesPath) {
        return json({ panes: [] });
      }
      if (path === sessionsPath) {
        return json({ sessions: [], seq: "0" });
      }
      return server.fetch(input, init);
    };
    const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch }));
    const stop = start(root, clients, longPollMs);
    try {
      await until(t.signal, () => blankText(root).includes("Nothing is running."), "the empty inbox");
      assert.equal(tryNow(root), null, "an empty inbox offers Try now");
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
    mockTimers(t, ["setInterval", "setTimeout"]);
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
      await until(t.signal, () => endedCount(root) === 1, "the event's epics load", { step: turn });
      assert.equal(server.epicsGetCalls, gets, "an event reload over an unchanged list asked for an epic");
    } finally {
      stop();
    }
  }));

// liveGamma is a second live epic with the same running step as alpha's, under its own id.
function liveGamma(task: string) {
  return { ...liveEpic(task), id: "repo-a/gamma", slug: "gamma" };
}

// tickAsksChangedOnly fires one poll tick on a page with two live epics whose listed messages the tick's list leaves
// unchanged, and checks that the tick lists the epics once and asks for no epic. The tick's list also holds a folded
// epic, which no load asks for; its label draws only once the tick's epics load has drawn, after its Gets answered.
async function tickAsksChangedOnly(t: TestContext, streaming: boolean): Promise<void> {
  await withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
    const server = fakeServer("api", streaming);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft"), liveGamma("draft")];
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    try {
      await until(
        t.signal,
        () => labels(root).includes("alpha") && labels(root).includes("gamma"),
        "both epics",
        { step: turn },
      );
      const lists = server.epicsListCalls;
      const gets = server.epicsGetCalls;
      server.epics = [liveEpic("draft"), liveGamma("draft"), endedBeta];
      t.mock.timers.tick(pollMs);
      await until(t.signal, () => endedCount(root) === 1, "the tick's epics load", { step: turn });
      assert.equal(server.epicsListCalls, lists + 1, "the tick did not list the epics once");
      assert.equal(server.epicsGetCalls, gets, "a tick over unchanged listed messages asked for an epic");
    } finally {
      stop();
    }
  });
}

test("ChangedEpicsOnly: a poll tick with the event stream open lists the epics and asks for no unchanged one", (t) =>
  tickAsksChangedOnly(t, true));

test("ChangedEpicsOnly: a poll tick with the event stream down lists the epics and asks for no unchanged one", (t) =>
  tickAsksChangedOnly(t, false));

test("ChangedEpicsOnly: a poll tick asks for the live epic whose listed message changed, and for no other", (t) =>
  withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
    const server = fakeServer("api", true);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft"), liveGamma("draft")];
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    try {
      await until(
        t.signal,
        () => server.watches.length === 1 && labels(root).includes("alpha") && labels(root).includes("gamma"),
        "both epics and the stream",
        { step: turn },
      );
      const asked = server.epicsGetIds.length;
      server.epics = [liveEpic("review"), liveGamma("draft")];
      t.mock.timers.tick(pollMs);
      await until(t.signal, () => labels(root).includes("review"), "the tick's changed step", { step: turn });
      assert.deepEqual(server.epicsGetIds.slice(asked), ["repo-a/alpha"]);
    } finally {
      stop();
    }
  }));

test("ChangedEpicsOnly: a poll tick asks for the epic the page shows even when its listed message is unchanged", (t) =>
  withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
    const server = fakeServer("api", true);
    server.gate = Promise.resolve();
    server.epics = [liveEpic("draft"), liveGamma("draft")];
    window.history.replaceState(null, "", "#/inbox/epic/repo-a%2Falpha");
    const stop = start(root, clientsFor(server), pollMs, mockedRetryMs);
    try {
      await until(t.signal, () => epicPageDrawn(root) && server.watches.length === 1, "the epic page", {
        step: settle,
      });
      // Moving the view onto the epic asks for it at once, beside the first load's ask.
      await until(
        t.signal,
        () => server.epicsGetIds.filter((id) => id === "repo-a/alpha").length === 2,
        "the ask for the epic the view moved onto",
        { step: settle },
      );
      assert.deepEqual(server.epicsGetIds.filter((id) => id === "repo-a/gamma"), ["repo-a/gamma"]);
      const asked = server.epicsGetIds.length;
      server.epics = [liveEpic("draft"), liveGamma("draft"), endedBeta];
      t.mock.timers.tick(pollMs);
      // The folded epic's label draws only once the tick's epics load has drawn, after its Gets answered.
      await until(t.signal, () => endedCount(root) === 1, "the tick's epics load", { step: settle });
      assert.deepEqual(server.epicsGetIds.slice(asked), ["repo-a/alpha"]);
    } finally {
      stop();
    }
  }));

test("ChangedEpicsOnly: a tick that lands while an epics load is in flight runs one more epics load once it ends", (t) =>
  withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
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
      // With the gate released, the held load draws its changed step, and the tick's load follows it with no event and
      // no further tick.
      server.epics = [liveEpic("review"), endedBeta];
      release();
      await until(t.signal, () => labels(root).includes("review"), "the held load's changed step", { step: turn });
      await until(t.signal, () => endedCount(root) === 1, "the tick's load after the held one", { step: turn });
      assert.equal(server.epicsListCalls, 3, "the tick's load did not run once after the held one");
      assert.equal(server.maxEpicsInFlight, 1, "two epics loads overlapped");
    } finally {
      release();
      stop();
    }
  }));

// sendTyped writes one event of a typed kind onto a watch's stream, as Events.Watch sends it in JSON.
function sendTyped(watch: Watch, seq: number, sessionId: string, payload: Record<string, object>): void {
  enqueue(watch, 0, { seq: String(seq), sessionId, ...payload });
}

test("ChangedEpicsOnly: an engine_text event reloads nothing, while an event of a kind the page does not know reloads both", (t) =>
  withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
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
      const panes = server.panesCalls;
      const lists = server.epicsListCalls;
      sendTyped(server.watches[0], 7, "s1", {
        engineText: { messageId: "m1", index: 0, kind: "text", text: "so far" },
      });
      await turn();
      t.mock.timers.tick(eventWaitMs);
      // The next event, of no kind the page knows, reloads both. Had the text armed a reload, its wait would have run
      // one first.
      server.paneDir = "web";
      server.epics = [liveEpic("review")];
      await fireEvent(t, server.watches[0], 8);
      await until(
        t.signal,
        () => labels(root).includes("web") && labels(root).includes("review"),
        "the unknown event's reload",
        { step: turn },
      );
      assert.equal(server.panesCalls, panes + 1, "the engine_text reloaded the rows");
      assert.equal(server.epicsListCalls, lists + 1, "the engine_text reloaded the epics");
    } finally {
      stop();
    }
  }));

test("ChangedEpicsOnly: a running step's state change reloads the epics and not the rows", (t) =>
  withPage(t.signal, async (root, start) => {
    mockTimers(t, ["setInterval", "setTimeout"]);
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
      const panes = server.panesCalls;
      server.epics = [liveEpic("review")];
      // step-1 is the running step liveEpic lists, whose id is its session's.
      sendTyped(server.watches[0], 8, "step-1", { engineChanged: { state: "idle" } });
      await turn();
      t.mock.timers.tick(eventWaitMs);
      await until(t.signal, () => labels(root).includes("review"), "the step event's epics load", { step: turn });
      assert.equal(server.panesCalls, panes, "a step's event reloaded the rows");
    } finally {
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

test("the start read shows the host in the strip's host part, before any poll tick", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    // longPollMs means no tick runs, so only the read at start can show the button.
    const stop = start(root, clientsFor(server), longPollMs);
    try {
      // The cursor starts on the pane, which is on another host. G moves it to the session, whose page names this host.
      await until(t.signal, () => labels(root).includes("notes"), "the notes row", { step: () => act(turn) });
      await act(() => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "G" }));
      });
      await until(t.signal, () => meterHost(root) === "den", "the meters button for the host");
    } finally {
      stop();
    }
  }));

test("a refused Host.Status shows no strip host part and no error, while the rows, epics and stream load", (t) =>
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
      // G moves the cursor to the session, whose page would name this host if a status had come.
      await until(t.signal, () => labels(root).includes("notes"), "the notes row", { step: () => act(turn) });
      await act(() => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "G" }));
      });
      // A read starts only once the one before it is handled, so the second read after the stream opens means a refusal
      // from after it has landed. No reload runs with the stream open, so nothing clears the error line after that.
      const readsAtOpen = server.hostCalls;
      await until(t.signal, () => server.hostCalls >= readsAtOpen + 2, "two refused reads with the stream open");
      assert.equal(meterButtonShown(root), false, "a refused Host.Status drew a host part in the strip");
      assert.equal(errorText(root), "", "a refused Host.Status showed in the error line");
    } finally {
      stop();
    }
  }));

test("a Host.Status that starts failing takes the strip's host part away", (t) =>
  withPage(t.signal, async (root, start) => {
    const server = fakeServer("api", true);
    const stop = start(root, clientsFor(server), intervalMs);
    try {
      await until(t.signal, () => labels(root).includes("notes"), "the notes row", { step: () => act(turn) });
      await act(() => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "G" }));
      });
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
      // G moves the cursor to the session, whose page names the host once the read lands.
      await until(t.signal, () => labels(root).includes("notes"), "the notes row", { step: () => act(turn) });
      await act(() => {
        document.dispatchEvent(new KeyboardEvent("keydown", { key: "G" }));
      });
      release();
      await until(t.signal, () => meterHost(root) === "den", "the host after release");
    } finally {
      release();
      stop();
    }
  }));

// narrowWidth is a window under the 760px the wide layout starts at. In the wide layout a row's click hides the inbox,
// and the list's keys then pop it out rather than move the cursor. Under 760px a row's page draws alone, the list hidden
// behind it, and the list's keys still move the cursor, so the test that presses j runs narrow.
const narrowWidth = 700;

// settle is the wait between checks in a test whose timers are mocked: act applies the renders the page has queued, and
// the turn after it lets the answers the fake server has queued reach the page. It waits on no timer.
function settle(): Promise<void> {
  return act(turn);
}

// cursorKey is the key of the row, line or step the cursor rests on, or null when none does.
function cursorKey(root: HTMLElement): string | null {
  return root.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// rowDrawn reports whether the list draws the line under key.
function rowDrawn(root: HTMLElement, key: string): boolean {
  return root.querySelector(`[data-key="${key}"]`) !== null;
}

// viewOn is the view the list shows, which its rows name in data-list.
function viewOn(root: HTMLElement): string {
  return root.querySelector(".inbox-rows")?.getAttribute("data-list") ?? "";
}

// epicPageDrawn is whether the epic page section holds the epic page itself.
function epicPageDrawn(root: HTMLElement): boolean {
  return root.querySelector('section.page[aria-label="epic"] .epic-page') !== null;
}

// answeringServer is a fake server whose Panes.List answers at once. Without a gate, Panes.List waits on a timer, which a
// test with mocked timers would wait on forever.
function answeringServer(): FakeServer {
  const server = fakeServer("api");
  server.gate = Promise.resolve();
  return server;
}

// holdLists holds the answers of Panes.List and Epics.List until the test releases them, so the page's first render
// shows no rows and no epics. It returns the release.
function holdLists(server: FakeServer): () => void {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  server.gate = gate;
  server.epicsGate = gate;
  return () => release();
}

// clickRow clicks the row under key, which moves the cursor to it, then blurs what the click focused: a click on a row
// focuses its reply box, and the keys would type into it.
async function clickRow(root: HTMLElement, key: string): Promise<void> {
  const row = root.querySelector(`[data-key="${key}"]`);
  assert.ok(row, `no row under ${key}`);
  await act(() => {
    row.dispatchEvent(new Event("click", { bubbles: true }));
  });
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// press sends one keydown to the document, as a key press does, and lets the page apply what it changed.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// openAfterArrival loads the page at hash over server, with its lists held, so the first render shows neither. It then
// lets the lists answer, and waits until the cursor rests on key.
async function openAfterArrival(
  signal: AbortSignal,
  root: HTMLElement,
  start: typeof startPage,
  server: FakeServer,
  hash: string,
  key: string,
): Promise<void> {
  window.history.replaceState(null, "", hash);
  const release = holdLists(server);
  start(root, clientsFor(server), longPollMs);
  assert.equal(cursorKey(root), null, "the page opened an entry before its lists answered");
  release();
  await until(signal, () => cursorKey(root) === key, `the cursor on ${key}`, { step: settle });
}

test("HashRoute: a loaded session address opens its row once the rows arrive after the first render", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, (root, start) =>
    openAfterArrival(t.signal, root, start, fakeServer("api"), "#/inbox/session/s1", "session:s1"),
  );
});

test("HashRoute: a loaded pane address whose id is %3 opens its row once the rows arrive after the first render", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, (root, start) => {
    const server = fakeServer("api");
    server.extraPanes = [paneOf("%3", "three")];
    return openAfterArrival(t.signal, root, start, server, "#/inbox/pane/box/%253", "pane:box:%3");
  });
});

test("HashRoute: a loaded step address opens its step once the epics arrive after the first render", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("first step")];
    return openAfterArrival(t.signal, root, start, server, "#/inbox/step/step-1", "step:step-1");
  });
});

test("HashRoute: a loaded epic address whose id holds a slash opens its epic page once the epics arrive", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("first step")];
    await openAfterArrival(t.signal, root, start, server, "#/inbox/epic/repo-a%2Falpha", "epic:repo-a/alpha");
    assert.equal(epicPageDrawn(root), true, "the epic page is not drawn");
  });
});

test("HashRoute: a loaded contract address opens its epic's contract page once the epics arrive", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    const server = fakeServer("api");
    server.epics = [liveEpic("first step")];
    server.contract = { title: "Ship the widget", approved: "approved line", intent: "the intent" };
    await openAfterArrival(
      t.signal,
      root,
      start,
      server,
      "#/inbox/epic/repo-a%2Falpha/contract",
      "epic:repo-a/alpha",
    );
    await until(
      t.signal,
      () => root.querySelector('section.page[aria-label="epic"] article.contract') !== null,
      "the contract page",
      { step: settle },
    );
  });
});

test("InboxTree: an old project address whose pane the answered rows lack opens the inbox, and the hash becomes the inbox", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    const before = window.history.length;
    window.history.replaceState(null, "", "#/project/pane/box/%259");
    start(root, clientsFor(answeringServer()), longPollMs);
    await until(t.signal, () => window.location.hash === "#/inbox", "the hash rewritten to the inbox", {
      step: settle,
    });
    assert.equal(viewOn(root), "inbox", "the page does not show the inbox");
    assert.equal(window.history.length, before, "the fallback added a history entry");
  });
});

test("HashRoute: a malformed percent escape in the hash opens the inbox", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    window.history.replaceState(null, "", "#/archived/session/%E0%A4%A");
    start(root, clientsFor(answeringServer()), longPollMs);
    await until(t.signal, () => rowDrawn(root, "pane:box:%1"), "the rows", { step: settle });
    assert.equal(viewOn(root), "inbox", "the page does not show the inbox");
  });
});

test("HashRoute: a row click and three j keys leave two entries, and Back returns to the clicked row", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start, view) => {
    view.happyDOM.setViewport({ width: narrowWidth, height: 800 });
    const server = answeringServer();
    server.extraPanes = [paneOf("%3", "three"), paneOf("%4", "four"), paneOf("%5", "five")];
    start(root, clientsFor(server), longPollMs);
    await until(t.signal, () => rowDrawn(root, "pane:box:%5") && rowDrawn(root, "session:s1"), "the rows", {
      step: settle,
    });
    // The cursor rests on the first row, so the click is on another row, and each j then moves the cursor one row on.
    const before = window.history.length;
    await clickRow(root, "pane:box:%3");
    await settle();
    assert.equal(cursorKey(root), "pane:box:%3", "the click did not move the cursor to the clicked row");
    assert.equal(window.history.length, before + 1, "the click did not add one entry");
    for (const key of ["j", "j", "j"]) {
      await press(key);
    }
    await settle();
    assert.notEqual(cursorKey(root), "pane:box:%3", "the j keys did not move the cursor");
    assert.equal(window.history.length, before + 2, "the click and the j keys did not leave two entries");
    window.history.back();
    await until(
      t.signal,
      () => window.location.hash === "#/inbox/pane/box/%253" && cursorKey(root) === "pane:box:%3",
      "Back to the clicked row",
      { step: settle },
    );
  });
});

test("HashRoute: after a loaded session address opens and a click opens another row, Back reopens the loaded row", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    window.history.replaceState(null, "", "#/inbox/session/s1");
    start(root, clientsFor(answeringServer()), longPollMs);
    await until(t.signal, () => cursorKey(root) === "session:s1", "the loaded session under the cursor", {
      step: settle,
    });
    await clickRow(root, "pane:box:%1");
    await until(t.signal, () => cursorKey(root) === "pane:box:%1", "the clicked row under the cursor", {
      step: settle,
    });
    window.history.back();
    await until(
      t.signal,
      () => window.location.hash === "#/inbox/session/s1" && cursorKey(root) === "session:s1",
      "Back to the loaded session",
      { step: settle },
    );
  });
});

test("HashRoute: a page loaded with no hash writes no history", (t) => {
  mockTimers(t, ["setTimeout", "setInterval"]);
  return withPage(t.signal, async (root, start) => {
    const before = window.history.length;
    start(root, clientsFor(answeringServer()), longPollMs);
    await until(t.signal, () => rowDrawn(root, "pane:box:%1"), "the rows", { step: settle });
    assert.equal(window.location.hash, "", "the page wrote a hash");
    assert.equal(window.history.length, before, "the bare load added a history entry");
  });
});
