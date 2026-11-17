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
const withdrawPath = "/orrery.v1.Control/Withdraw";
const interruptPath = "/orrery.v1.Control/Interrupt";
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

async function doubleClick(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("dblclick", { bubbles: true, cancelable: true }));
  });
}

async function pointerDown(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("pointerdown", { bubbles: true, cancelable: true }));
  });
}

// tap is a press as a browser sends it: a pointerdown, then the click that ends it.
async function tap(element: Element): Promise<void> {
  await pointerDown(element);
  await click(element);
}

function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-row]")].map((element) => element.getAttribute("data-key") ?? "");
}

// lineOf finds the line of a row, or of an epic by its key, which an epic's line carries as epic:<id>.
function lineOf(container: HTMLElement, key: string): Element | null {
  return container.querySelector(`[data-row][data-key="${key}"]`);
}

// moreButtonOf finds a line's ⋯ button, which opens the line's menu.
function moreButtonOf(container: HTMLElement, key: string): HTMLButtonElement {
  const line = lineOf(container, key);
  assert.ok(line, `line ${key} is rendered`);
  const button = line.querySelector<HTMLButtonElement>('button[aria-label="more"]');
  assert.ok(button, `line ${key} has a ⋯ button`);
  return button;
}

// openMenu presses a line's ⋯ and returns the one item its menu holds.
async function openMenu(container: HTMLElement, key: string): Promise<HTMLButtonElement> {
  await tap(moreButtonOf(container, key));
  const item = lineOf(container, key)?.querySelector<HTMLButtonElement>(".row-menu-list button");
  assert.ok(item, `line ${key}'s menu holds its item`);
  return item;
}

// menuOpen says whether a scope draws a menu. It answers a boolean, so a failed assert names the state and does not
// format a DOM element.
function menuOpen(scope: ParentNode | null): boolean {
  return scope !== null && scope.querySelector(".row-menu-list") !== null;
}

// cursorKey is the key of the row or epic the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
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
      await click(await openMenu(container, "s1"));
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
      await click(await openMenu(container, "epic:w/first"));
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
      assert.equal((await openMenu(container, "ha")).textContent, "Unarchive");
      assert.equal((await openMenu(container, "epic:w/old")).textContent, "Unarchive");
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
      await click(await openMenu(container, "ha"));
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
    await click(await openMenu(container, "epic:w/live"));
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
    await click(await openMenu(container, "s1"));
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

test("RowMenu: a session row and an epic line draw a ⋯ button and no Archive text", (t) =>
  withArchive(
    t.signal,
    {
      rows: [session({ key: "s1", id: "s1", label: "notes" })],
      epics: epicsView([epic({ id: "w/first", slug: "first", repository: "w" })], new Map()),
    },
    (container) => {
      for (const key of ["s1", "epic:w/first"]) {
        assert.equal(moreButtonOf(container, key).textContent, "⋯", `line ${key} draws ⋯`);
        assert.equal(moreButtonOf(container, key).getAttribute("aria-expanded"), "false", `line ${key} has no menu open`);
        assert.doesNotMatch(lineOf(container, key)?.textContent ?? "", /Archive/, `line ${key} draws no Archive text`);
      }
    },
  ));

test("RowMenu: pressing ⋯ opens a menu with Archive, and choosing it sends Epics.Archive with the row's kind and id", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    const item = await openMenu(container, "s1");
    assert.equal(item.textContent, "Archive");
    assert.equal(moreButtonOf(container, "s1").getAttribute("aria-expanded"), "true");
    await pointerDown(item);
    assert.ok(container.querySelector(".row-menu-list"), "a press on the item leaves the menu open until it is chosen");
    await click(item);
    assert.equal(menuOpen(container), false, "choosing the item closes the menu");
    await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
    assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
    await until(t.signal, () => lineOf(container, "s1") === null, "the row leaving the list");
  }));

test("RowMenu: the Archived view's menu holds Unarchive and sends Epics.Unarchive with the row's kind and id", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "ha", id: "ha", label: "old docs", archived: true })] },
    async (container, fake) => {
      await press("v");
      const item = await openMenu(container, "ha");
      assert.equal(item.textContent, "Unarchive");
      await click(item);
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "ha" });
    },
  ));

test("RowMenu: with a working cursor row, Escape with its menu open closes the menu and sends no withdraw, interrupt or archive call", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "h1", id: "h1", label: "docs", state: "working" })] },
    async (container, fake) => {
      await openMenu(container, "h1");
      await press("Escape");
      assert.equal(menuOpen(container), false, "Escape closes the menu");
      await flush();
      assert.deepEqual(fake.callsTo(withdrawPath), []);
      assert.deepEqual(fake.callsTo(interruptPath), []);
      assert.deepEqual(archiveCalls(fake), []);
      assert.deepEqual(rowKeys(container), ["h1"]);
    },
  ));

test("RowMenu: a click outside the open menu closes it with no call", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    await openMenu(container, "s1");
    const brand = container.querySelector(".brand");
    assert.ok(brand, "the brand is rendered");
    await pointerDown(brand);
    await click(brand);
    assert.equal(menuOpen(container), false, "the outside press closes the menu");
    assert.equal(moreButtonOf(container, "s1").getAttribute("aria-expanded"), "false");
    await flush();
    assert.deepEqual(archiveCalls(fake), []);
  }));

test("RowMenu: pressing ⋯ again closes the menu and archives nothing", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    await openMenu(container, "s1");
    await tap(moreButtonOf(container, "s1"));
    assert.equal(menuOpen(container), false, "the second press closes the menu");
    await flush();
    assert.deepEqual(archiveCalls(fake), []);
  }));

test("RowMenu: opening a second row's menu closes the first", (t) =>
  withArchive(
    t.signal,
    {
      rows: [
        session({ key: "s1", id: "s1", label: "notes" }),
        session({ key: "s2", id: "s2", label: "plans" }),
      ],
    },
    async (container) => {
      await openMenu(container, "s1");
      await openMenu(container, "s2");
      assert.equal(menuOpen(lineOf(container, "s1")), false, "the first row's menu is closed");
      assert.equal(menuOpen(lineOf(container, "s2")), true, "the second row's menu is open");
    },
  ));

test("RowMenu: opening the menu leaves the cursor and seen marks unchanged", (t) =>
  withArchive(
    t.signal,
    {
      rows: [row({ key: "p", label: "pane" }), session({ key: "s1", id: "s1", label: "notes", unseen: true })],
    },
    async (container, fake) => {
      // The unseen session sorts ahead of the pane row, so the cursor starts on it, and j moves it to the pane row.
      await press("j");
      assert.equal(cursorKey(container), "p", "the cursor rests on the pane row");
      const calls = fake.calls.length;
      await openMenu(container, "s1");
      await doubleClick(moreButtonOf(container, "s1"));
      await flush();
      assert.equal(cursorKey(container), "p", "the cursor stays on the pane row");
      assert.ok(lineOf(container, "s1")?.hasAttribute("data-unseen"), "s1 is still unseen");
      assert.equal(fake.calls.length, calls, "nothing is sent, so nothing is marked seen");
    },
  ));

test("RowMenu: opening a menu scrolls it into view within the list, to the nearest edge", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container) => {
    // The test window's scrollIntoView records the element and the options of each call, and is discarded with the window.
    const scrolls: { className: string; block: unknown }[] = [];
    HTMLElement.prototype.scrollIntoView = function scrollIntoView(this: HTMLElement, options?: ScrollIntoViewOptions) {
      scrolls.push({ className: this.className, block: options?.block });
    };
    await openMenu(container, "s1");
    assert.deepEqual(
      scrolls.filter((call) => call.className === "row-menu-list"),
      [{ className: "row-menu-list", block: "nearest" }],
      "the opened menu is scrolled into view, to the nearest edge",
    );
  }));

test("RowMenu: a menu whose row leaves the list counts as closed, and does not reopen when the row returns", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container) => {
    await openMenu(container, "s1");
    await press("v");
    assert.ok(lineOf(container, "s1") === null, "s1 is not drawn in the Archived view");
    await press("a");
    assert.ok(lineOf(container, "s1") !== null, "s1 is drawn again");
    assert.equal(menuOpen(container), false, "its menu stays closed");
    assert.equal(moreButtonOf(container, "s1").getAttribute("aria-expanded"), "false");
  }));

test("RowMenu: Escape with the open menu's row no longer drawn does what Escape does without a menu", (t) =>
  withArchive(
    t.signal,
    {
      rows: [
        headless({ key: "h1", id: "h1", label: "docs", state: "working" }),
        headless({ key: "h2", id: "h2", label: "notes", state: "working", archived: true }),
      ],
    },
    async (container, fake) => {
      await openMenu(container, "h1");
      await press("v");
      assert.deepEqual(rowKeys(container), ["h2"]);
      assert.equal(cursorKey(container), "h2", "the cursor rests on the archived working row");
      await press("Escape");
      await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
      assert.deepEqual(fake.callsTo(interruptPath)[0].body, { id: "h2" });
    },
  ));

test("RowMenu: after A archives a row whose menu is open, no menu is open, and Escape on a working cursor row interrupts it", (t) =>
  withArchive(
    t.signal,
    {
      rows: [
        headless({ key: "h1", id: "h1", label: "docs", state: "working" }),
        headless({ key: "h2", id: "h2", label: "notes", state: "working" }),
      ],
    },
    async (container, fake) => {
      assert.equal(cursorKey(container), "h1", "the cursor starts on the first row");
      await openMenu(container, "h1");
      await press("A");
      assert.equal(menuOpen(container), false, "A leaves no menu open");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "headless", id: "h1" });
      await until(t.signal, () => lineOf(container, "h1") === null, "the row leaving the list");
      assert.equal(menuOpen(container), false, "no menu is open once the row has left");
      assert.equal(cursorKey(container), "h2", "the cursor moves to the remaining working row");
      await press("Escape");
      await until(t.signal, () => fake.callsTo(interruptPath).length === 1, "the interrupt");
      assert.deepEqual(fake.callsTo(interruptPath)[0].body, { id: "h2" });
    },
  ));
