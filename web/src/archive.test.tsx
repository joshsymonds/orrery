import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const archivePath = "/orrery.v1.Epics/Archive";
const unarchivePath = "/orrery.v1.Epics/Unarchive";
const archiveFailedText = "could not change archive";
// The turns a test lets a write it started run to its end before it reads that nothing was sent.
const flushRounds = 10;

// Call is one unary request the fake server received, with its JSON body.
interface Call {
  path: string;
  body: Record<string, unknown>;
}

// FakeServer answers the App's calls on the Connect wire, as app.test.tsx's does. Each unary call is recorded and answers
// an empty message, or the refusal failNext queues for its path once. Each WatchConversation holds its stream open until
// its call aborts, and closeStreams ends the ones still open.
class FakeServer {
  readonly calls: Call[] = [];
  readonly failNext = new Set<string>();
  private readonly open = new Map<AbortSignal, ReadableStreamDefaultController<Uint8Array>>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const signal = init?.signal ?? request.signal;
    const path = new URL(request.url).pathname;
    if (path.endsWith("/WatchConversation")) {
      const body = new ReadableStream<Uint8Array>({
        start: (controller) => {
          this.open.set(signal, controller);
        },
      });
      signal.addEventListener("abort", () => this.open.delete(signal), { once: true });
      return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
    }
    const call: Call = { path, body: {} };
    this.calls.push(call);
    const text = await request.text();
    call.body = text === "" ? {} : JSON.parse(text);
    if (this.failNext.delete(path)) {
      return new Response(JSON.stringify({ code: "unavailable", message: "queue unreachable" }), {
        status: 503,
        headers: { "content-type": "application/json" },
      });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }

  closeStreams(): void {
    for (const controller of this.open.values()) {
      controller.close();
    }
    this.open.clear();
  }
}

interface Setup {
  rows?: Row[];
  epics?: EpicsView;
}

// withArchive mounts the app over a fake server in a fresh DOM and runs check. The app is unmounted, its streams closed
// and the DOM removed when the test ends, or as soon as signal aborts if it times out first.
async function withArchive(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement, fake: FakeServer) => void | Promise<void>,
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
      render(<App rows={setup.rows ?? []} error="" now={now} clients={fake.clients} epics={setup.epics} />, container);
    });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

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
    ...init,
  };
}

// session is a session row that archives as a session.
function session(init: Partial<Row> & Pick<Row, "key">): Row {
  return row({ kind: "session", archiveKind: "session", changed: 30, seen: 30, ...init });
}

// headless is a headless session row, which archives as a headless session.
function headless(init: Partial<Row> & Pick<Row, "key">): Row {
  return row({ kind: "session", archiveKind: "headless", changed: 30, seen: 30, ...init });
}

// epic is a live or ended orreryd epic with the given fields.
function epic(init: MessageInitShape<typeof EpicSchema>): Epic {
  return create(EpicSchema, { repository: "w", outcome: "", live: true, ...init });
}

// until waits for check, one turn of the event loop at a time, so the renders it lets through are applied and the wait
// reads no clock.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// flush lets the writes a key or click started run to their end before a test reads that nothing was sent.
async function flush(): Promise<void> {
  for (let round = 0; round < flushRounds; round++) {
    await act(turn);
  }
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

async function click(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-row]")].map((element) => element.getAttribute("data-key") ?? "");
}

// lineOf finds the line of a row, or of an epic by its key, which an epic's line carries as epic:<id>.
function lineOf(container: HTMLElement, key: string): Element | null {
  return container.querySelector(`[data-row][data-key="${key}"]`);
}

function archiveButtonOf(container: HTMLElement, key: string): HTMLButtonElement {
  const line = lineOf(container, key);
  assert.ok(line, `line ${key} is rendered`);
  const button = line.querySelector<HTMLButtonElement>("button.archive");
  assert.ok(button, `line ${key} has an archive button`);
  return button;
}

function dataList(container: HTMLElement): string | null {
  return container.querySelector(".rows")?.getAttribute("data-list") ?? null;
}

function archiveCalls(fake: FakeServer): Call[] {
  return fake.calls.filter((call) => call.path === archivePath || call.path === unarchivePath);
}

function sectionTitles(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".b-sec")].map((element) => element.querySelector(".title")?.textContent ?? null);
}

test("ArchiveView: Archive on a session row sends Epics.Archive with its kind and id, and the row leaves By attention", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p", label: "pane" }), session({ key: "s1", id: "s1", label: "notes" })] },
    async (container, fake) => {
      await click(archiveButtonOf(container, "s1"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
      await until(t.signal, () => lineOf(container, "s1") === null, "the row leaving the list");
      assert.deepEqual(rowKeys(container), ["p"]);
    },
  ));

test("ArchiveView: Archive on an epic line sends Epics.Archive with kind epic and its id, and the line leaves", (t) =>
  withArchive(
    t.signal,
    { epics: epicsView([epic({ id: "w/first", slug: "first", repository: "w" })], new Map()) },
    async (container, fake) => {
      await click(archiveButtonOf(container, "epic:w/first"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "epic", id: "w/first" });
      await until(t.signal, () => lineOf(container, "epic:w/first") === null, "the line leaving the list");
    },
  ));

test("ArchiveView: the A key archives the cursor's headless row, as kind headless", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "h1", id: "h1", label: "docs", state: "waiting" })] },
    async (container, fake) => {
      await press("A");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "headless", id: "h1" });
      await until(t.signal, () => lineOf(container, "h1") === null, "the row leaving the list");
    },
  ));

test("ArchiveView: lowercase a selects By attention and archives nothing", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "h1", id: "h1", label: "docs", state: "waiting" }), row({ key: "p", label: "pane" })] },
    async (container, fake) => {
      await press("p");
      assert.equal(dataList(container), "project");
      await press("a");
      assert.equal(dataList(container), "attention");
      await flush();
      assert.deepEqual(archiveCalls(fake), []);
      assert.deepEqual(rowKeys(container), ["h1", "p"]);
    },
  ));

test("ArchiveView: the Archived view lists the archived row and epic with Unarchive, and the defaults leave them out", (t) =>
  withArchive(
    t.signal,
    {
      rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })],
      epics: epicsView(
        [epic({ id: "w/old", slug: "old", outcome: "released", live: false, archived: true, phaseAt: 99000n })],
        new Map(),
      ),
    },
    async (container) => {
      await press("v");
      assert.equal(dataList(container), "archived");
      assert.deepEqual(rowKeys(container), ["epic:w/old", "ha"]);
      assert.equal(archiveButtonOf(container, "ha").textContent, "Unarchive");
      assert.equal(archiveButtonOf(container, "epic:w/old").textContent, "Unarchive");
      await press("a");
      assert.deepEqual(rowKeys(container), ["p"]);
    },
  ));

test("ArchiveView: Unarchive sends Epics.Unarchive with the row's kind and id, and the row returns to By attention", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })] },
    async (container, fake) => {
      await press("v");
      await click(archiveButtonOf(container, "ha"));
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "ha" });
      await until(t.signal, () => lineOf(container, "ha") === null, "the row leaving the Archived view");
      await press("a");
      assert.deepEqual(rowKeys(container), ["ha", "p"]);
    },
  ));

test("ArchiveView: Unarchive on an archived live epic returns its line to By attention", (t) => {
  const live = epic({ id: "w/live", slug: "live", archived: true });
  const epics = epicsView([live], new Map([[live.id, create(EpicsGetResponseSchema, { epic: live })]]));
  return withArchive(t.signal, { epics }, async (container, fake) => {
    await press("v");
    await click(archiveButtonOf(container, "epic:w/live"));
    await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
    assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "epic", id: "w/live" });
    await until(t.signal, () => lineOf(container, "epic:w/live") === null, "the line leaving the Archived view");
    await press("a");
    assert.ok(lineOf(container, "epic:w/live"), "the line is back in By attention");
  });
});

test("ArchiveView: an archived live epic's needs leave Needs you", (t) => {
  const asked = epic({ id: "w/asked", slug: "asked", archived: true });
  const detail = create(EpicsGetResponseSchema, {
    epic: asked,
    questions: [{ id: "Q1", text: "Which way?", asked: 99880n }],
  });
  const epics = epicsView([asked], new Map([[asked.id, detail]]));
  return withArchive(t.signal, { epics }, (container) => {
    assert.equal(sectionTitles(container).includes("Needs you"), false, "no Needs you section names the archived epic");
    assert.equal(lineOf(container, "epic:w/asked"), null, "the archived epic has no line in By attention");
  });
});

test("ArchiveView: a pane row has no Archive button, and A on it sends nothing", (t) =>
  withArchive(t.signal, { rows: [row({ key: "p", label: "pane" })] }, async (container, fake) => {
    assert.equal(lineOf(container, "p")?.querySelector("button"), null, "the pane row draws no button");
    await press("A");
    await flush();
    assert.deepEqual(archiveCalls(fake), []);
    assert.deepEqual(rowKeys(container), ["p"]);
  }));

test("ArchiveView: a failed archive leaves the row where it was and shows the error line", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    fake.failNext.add(archivePath);
    await click(archiveButtonOf(container, "s1"));
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the error line");
    assert.equal(container.querySelector('[role="alert"]')?.textContent, archiveFailedText);
    assert.deepEqual(rowKeys(container), ["s1"]);
  }));

test("ArchiveView: the brand counts an archived row that needs you, as the list reads every loaded row", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "ha", id: "ha", label: "old docs", state: "waiting", archived: true })] },
    (container) => {
      assert.equal(container.querySelector(".brand .counts .st-waiting")?.textContent, "1 need you");
    },
  ));
