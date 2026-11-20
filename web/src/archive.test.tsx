import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { PaneSchema, SessionSchema } from "./gen/orrery/v1/orrery_pb";
import { formatAddress } from "./router";
import { rowsFrom, type Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const archivePath = "/orrery.v1.Epics/Archive";
const unarchivePath = "/orrery.v1.Epics/Unarchive";
const withdrawPath = "/orrery.v1.Control/Withdraw";
const interruptPath = "/orrery.v1.Control/Interrupt";
const markPanePath = "/orrery.v1.Panes/MarkSeen";
const markSessionPath = "/orrery.v1.Control/Seen";
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
    tool: "",
    id: init.key,
    archiveId: "",
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
  return row({
    kind: "session",
    archiveKind: "session",
    archiveId: init.id ?? init.key,
    changed: 30,
    seen: 30,
    ...init,
  });
}

// headless is a headless session row, which archives as a headless session.
function headless(init: Partial<Row> & Pick<Row, "key">): Row {
  return row({
    kind: "session",
    archiveKind: "headless",
    archiveId: init.id ?? init.key,
    changed: 30,
    seen: 30,
    ...init,
  });
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
  return [...container.querySelectorAll(".row[data-key]:not(.standin)")].map((element) => element.getAttribute("data-key") ?? "");
}

// lineOf finds the line of a row, or of an epic by its key, which an epic's line carries as epic:<id>.
function lineOf(container: HTMLElement, key: string): Element | null {
  return container.querySelector(`[data-key="${key}"]`);
}

// moreButtonOf finds a line's ⋯ button, which opens the line's menu.
function moreButtonOf(container: HTMLElement, key: string): HTMLButtonElement {
  const line = lineOf(container, key);
  assert.ok(line, `line ${key} is rendered`);
  const button = line.querySelector<HTMLButtonElement>('button[aria-label="more"]');
  assert.ok(button, `line ${key} has a ⋯ button`);
  return button;
}

// openMenu presses a line's ⋯ or More, which opens the line's menu.
async function openMenu(container: HTMLElement, key: string): Promise<void> {
  await tap(moreButtonOf(container, key));
  assert.ok(lineOf(container, key)?.querySelector(".row-menu-list"), `line ${key}'s menu is open`);
}

// itemLabel is the label of a menu item: its span when it has one, as a row's More menu draws it, else its text.
function itemLabel(item: Element): string | null {
  return item.querySelector("span")?.textContent ?? item.textContent;
}

// menuItem finds the item of a line's open menu whose label is label.
function menuItem(container: HTMLElement, key: string, label: string): HTMLButtonElement {
  const item = [...(lineOf(container, key)?.querySelectorAll<HTMLButtonElement>(".row-menu-list button") ?? [])].find(
    (candidate) => itemLabel(candidate) === label,
  );
  assert.ok(item, `line ${key}'s menu holds ${label}`);
  return item;
}

// menuOpen says whether a scope draws a menu. It answers a boolean, so a failed assert names the state and does not
// format a DOM element.
function menuOpen(scope: ParentNode | null): boolean {
  return scope !== null && scope.querySelector(".row-menu-list") !== null;
}

// cursorKey is the key of the row or epic the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// back presses the foot's Back to the inbox, which leaves the Archived view for the inbox.
async function back(container: HTMLElement): Promise<void> {
  const button = [...container.querySelectorAll(".inbox-foot button")].find(
    (candidate) => candidate.textContent === "Back to the inbox",
  );
  assert.ok(button, "the foot draws Back to the inbox");
  await click(button);
}

function dataList(container: HTMLElement): string | null {
  return container.querySelector(".inbox-rows")?.getAttribute("data-list") ?? null;
}

function archiveCalls(fake: FakeServer): Call[] {
  return fake.calls.filter((call) => call.path === archivePath || call.path === unarchivePath);
}

test("ArchiveView: Archive on a session row sends Epics.Archive with its kind and id, and the row leaves the inbox", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p", label: "pane" }), session({ key: "s1", id: "s1", label: "notes" })] },
    async (container, fake) => {
      await openMenu(container, "s1");
      await click(menuItem(container, "s1", "Archive"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
      await until(t.signal, () => lineOf(container, "s1") === null, "the row leaving the list");
      assert.deepEqual(rowKeys(container), ["p"]);
    },
  ));

test("ArchiveView: A on an epic entry sends Epics.Archive with kind epic and its id, and the line leaves", (t) =>
  withArchive(
    t.signal,
    { epics: epicsView([epic({ id: "w/first", slug: "first", repository: "w" })], new Map()) },
    async (container, fake) => {
      await press("A");
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

test("ArchiveView: lowercase a and p do nothing and archive nothing", (t) =>
  withArchive(
    t.signal,
    { rows: [headless({ key: "h1", id: "h1", label: "docs", state: "waiting" }), row({ key: "p", label: "pane" })] },
    async (container, fake) => {
      await press("p");
      assert.equal(dataList(container), "inbox");
      await press("a");
      assert.equal(dataList(container), "inbox");
      await flush();
      assert.deepEqual(archiveCalls(fake), []);
      assert.deepEqual(rowKeys(container), ["h1", "p"]);
    },
  ));

test("ArchiveView: the Archived view lists the archived row and epic, and the defaults leave them out", (t) =>
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
      assert.deepEqual(rowKeys(container), ["p"], "the inbox leaves the archived row and epic out");
      await press("v");
      assert.equal(dataList(container), "archived");
      assert.deepEqual(rowKeys(container), ["ha", "epic:w/old"]);
      await openMenu(container, "ha");
      assert.ok(menuItem(container, "ha", "Unarchive"), "the headless row's menu holds Unarchive");
      await back(container);
      assert.equal(dataList(container), "inbox");
      assert.deepEqual(rowKeys(container), ["p"]);
    },
  ));

test("InboxTree: RowMenu: the Archived view's menu holds Unarchive and sends Epics.Unarchive with the row's kind and id, and the row returns to the inbox", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })] },
    async (container, fake) => {
      await press("v");
      await openMenu(container, "ha");
      await click(menuItem(container, "ha", "Unarchive"));
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "ha" });
      await until(t.signal, () => lineOf(container, "ha") === null, "the row leaving the Archived view");
      await back(container);
      assert.deepEqual(rowKeys(container), ["ha", "p"]);
    },
  ));

test("ArchivePaneRow: a pane with no orrery_id opens a More menu with no Archive item, and A on it sends nothing", (t) =>
  withArchive(t.signal, { rows: [row({ key: "p", label: "pane", archiveId: "" })] }, async (container, fake) => {
    await openMenu(container, "p");
    assert.deepEqual(menuLabels(container, "p"), ["Open in a new window"], "the pane's menu holds no Archive");
    await press("A");
    await flush();
    assert.deepEqual(archiveCalls(fake), []);
    assert.deepEqual(rowKeys(container), ["p"]);
  }));

test("ArchivePaneRow: a pane row with an orrery_id draws a ⋯ menu whose Archive sends Epics.Archive with kind session and that id", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p1", id: "%3", archiveId: "sess-7", archiveKind: "session", label: "api" })] },
    async (container, fake) => {
      await openMenu(container, "p1");
      await click(menuItem(container, "p1", "Archive"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "sess-7" });
      await until(t.signal, () => lineOf(container, "p1") === null, "the row leaving the list");
    },
  ));

test("ArchivePaneRow: A archives the cursor pane row with an orrery_id, sending Epics.Archive with kind session and that id", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p1", id: "%3", archiveId: "sess-7", archiveKind: "session", label: "api" })] },
    async (container, fake) => {
      await press("A");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "sess-7" });
      await until(t.signal, () => lineOf(container, "p1") === null, "the row leaving the list");
    },
  ));

test("ArchivePaneRow: a pane whose Session is archived leaves the inbox, and shows in Archived with Unarchive that sends kind session and its orrery_id", (t) => {
  const rows = rowsFrom(
    [
      create(PaneSchema, {
        id: "%3",
        host: "box",
        session: "work",
        cwd: "/work/api",
        label: "api",
        state: "working",
        orreryId: "sess-7",
      }),
    ],
    [create(SessionSchema, { id: "sess-7", pane: "%3", archived: true })],
  );
  return withArchive(t.signal, { rows }, async (container, fake) => {
    assert.equal(lineOf(container, "pane:box:%3"), null, "the archived pane has no line in the inbox");
    await press("v");
    assert.equal(dataList(container), "archived");
    assert.deepEqual(rowKeys(container), ["pane:box:%3"]);
    await openMenu(container, "pane:box:%3");
    await click(menuItem(container, "pane:box:%3", "Unarchive"));
    await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
    assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "session", id: "sess-7" });
    await until(t.signal, () => lineOf(container, "pane:box:%3") === null, "the row leaving the Archived view");
    await back(container);
    assert.deepEqual(rowKeys(container), ["pane:box:%3"]);
  });
});

test("ArchiveView: a failed archive leaves the row where it was and shows the error line", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    fake.failNext.add(archivePath);
    await openMenu(container, "s1");
    await click(menuItem(container, "s1", "Archive"));
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the error line");
    assert.equal(container.querySelector('[role="alert"]')?.textContent, archiveFailedText);
    assert.deepEqual(rowKeys(container), ["s1"]);
  }));

test("RowMenu: a session row draws a More button and no Archive text, and an epic line draws no ⋯", (t) =>
  withArchive(
    t.signal,
    {
      rows: [session({ key: "s1", id: "s1", label: "notes" })],
      epics: epicsView([epic({ id: "w/first", slug: "first", repository: "w" })], new Map()),
    },
    (container) => {
      assert.equal(moreButtonOf(container, "s1").getAttribute("aria-expanded"), "false", "the row has no menu open");
      assert.doesNotMatch(lineOf(container, "s1")?.textContent ?? "", /Archive/, "the row draws no Archive text");
      assert.equal(lineOf(container, "epic:w/first")?.querySelector('button[aria-label="more"]') === null, true, "the epic line draws no ⋯");
    },
  ));

test("RowMenu: pressing ⋯ opens a menu with Archive, and choosing it sends Epics.Archive with the row's kind and id", (t) =>
  withArchive(t.signal, { rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    await openMenu(container, "s1");
    const item = menuItem(container, "s1", "Archive");
    assert.equal(moreButtonOf(container, "s1").getAttribute("aria-expanded"), "true");
    await pointerDown(item);
    assert.ok(container.querySelector(".row-menu-list"), "a press on the item leaves the menu open until it is chosen");
    await click(item);
    assert.equal(menuOpen(container), false, "choosing the item closes the menu");
    await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
    assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
    await until(t.signal, () => lineOf(container, "s1") === null, "the row leaving the list");
  }));

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
    const brand = container.querySelector(".inbox-head");
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
    await back(container);
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

test("RowMenu: after A archives a row whose menu is open, the menu closes once the row leaves the list, and Escape on a working cursor row interrupts it", (t) =>
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

test("RowMenu: the A key archives the cursor row and leaves another row's open menu open", (t) =>
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
      await openMenu(container, "h2");
      await press("A");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "headless", id: "h1" });
      await until(t.signal, () => lineOf(container, "h1") === null, "the row leaving the list");
      assert.equal(menuOpen(lineOf(container, "h2")), true, "h2's menu is still open");
    },
  ));

test("ArchiveView: A in the Archived view sends Epics.Unarchive with the row's kind and id, and the row returns to the inbox", (t) =>
  withArchive(
    t.signal,
    { rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })] },
    async (container, fake) => {
      await press("v");
      assert.equal(cursorKey(container), "ha", "the cursor rests on the archived row");
      await press("A");
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "ha" });
      assert.equal(fake.callsTo(archivePath).length, 0, "A in Archived archives nothing");
      await until(t.signal, () => lineOf(container, "ha") === null, "the row leaving the Archived view");
      await back(container);
      assert.deepEqual(rowKeys(container), ["ha", "p"]);
    },
  ));

test("ArchiveView: A in the Archived view on an archived epic sends Epics.Unarchive with kind epic and its id", (t) =>
  withArchive(
    t.signal,
    { epics: epicsView([epic({ id: "w/old", slug: "old", outcome: "released", live: false, archived: true, phaseAt: 99000n })], new Map()) },
    async (container, fake) => {
      await press("v");
      assert.ok(lineOf(container, "epic:w/old"), "the archived epic is drawn");
      assert.equal(container.querySelector(".row.cur")?.getAttribute("data-key"), "epic:w/old", "the cursor is drawn on it");
      await press("A");
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "epic", id: "w/old" });
      assert.equal(fake.callsTo(archivePath).length, 0, "A in Archived archives nothing");
      await until(t.signal, () => lineOf(container, "epic:w/old") === null, "the line leaving the Archived view");
    },
  ));

// menuLabels is the label of each item a line's open menu holds, in order.
function menuLabels(container: HTMLElement, key: string): (string | null)[] {
  return [...(lineOf(container, key)?.querySelectorAll(".row-menu-list button") ?? [])].map(itemLabel);
}

// waitingFirst is a waiting row the cursor starts on, so the rows after it are not under the cursor and keep their unseen mark.
function waitingFirst(): Row {
  return row({ key: "w", state: "waiting", question: "Ship it?", changed: 500, seen: 500 });
}

test("InboxTree: More's menu holds Open in a new window, Mark seen and Archive for an unseen row, and no Mark seen once it is seen", (t) =>
  withArchive(
    t.signal,
    { rows: [waitingFirst(), session({ key: "s1", id: "s1", label: "notes", unseen: true }), session({ key: "s2", id: "s2", label: "plans" })] },
    async (container) => {
      await openMenu(container, "s1");
      assert.deepEqual(menuLabels(container, "s1"), ["Open in a new window", "Mark seen", "Archive"]);
      await openMenu(container, "s2");
      assert.deepEqual(menuLabels(container, "s2"), ["Open in a new window", "Archive"]);
    },
  ));

test("InboxTree: Mark seen from More on a pane row sends Panes.MarkSeen with that pane's id and no other call", (t) =>
  withArchive(
    t.signal,
    {
      rows: [
        waitingFirst(),
        row({ key: "p1", id: "%1", host: "box", label: "build", state: "done", changed: 30, seen: 10, unseen: true }),
      ],
    },
    async (container, fake) => {
      await openMenu(container, "p1");
      await click(menuItem(container, "p1", "Mark seen"));
      await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the mark-seen call");
      assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "%1" });
      assert.deepEqual(fake.callsTo(markSessionPath), []);
      assert.deepEqual(archiveCalls(fake), []);
    },
  ));

test("InboxTree: Mark seen from More on a session row sends Control.Seen with that session's id and no other call", (t) =>
  withArchive(
    t.signal,
    { rows: [waitingFirst(), session({ key: "s1", id: "s1", label: "notes", unseen: true }), session({ key: "s2", id: "s2", label: "plans" })] },
    async (container, fake) => {
      await openMenu(container, "s1");
      await click(menuItem(container, "s1", "Mark seen"));
      await until(t.signal, () => fake.callsTo(markSessionPath).length === 1, "the mark-seen call");
      assert.deepEqual(fake.callsTo(markSessionPath)[0].body, { id: "s1" });
      assert.deepEqual(fake.callsTo(markPanePath), []);
    },
  ));

test("InboxTree: Archive from More sends Epics.Archive with that row's kind and id, and leaves the other row drawn", (t) =>
  withArchive(
    t.signal,
    { rows: [session({ key: "s1", id: "s1", label: "notes" }), session({ key: "s2", id: "s2", label: "plans" })] },
    async (container, fake) => {
      await openMenu(container, "s2");
      await click(menuItem(container, "s2", "Archive"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s2" });
      await until(t.signal, () => lineOf(container, "s2") === null, "the row leaving the list");
      assert.deepEqual(rowKeys(container), ["s1"]);
    },
  ));

test("InboxTree: Open in a new window from More opens the row's own address in a new window", (t) =>
  withArchive(
    t.signal,
    {
      rows: [
        waitingFirst(),
        row({ key: "p1", id: "%1", host: "box", label: "build", state: "done", changed: 30, seen: 30 }),
        session({ key: "s1", id: "s1", label: "notes" }),
      ],
    },
    async (container) => {
      const opened: [string, string | undefined][] = [];
      window.open = (url, target) => {
        opened.push([String(url), target]);
        return null;
      };
      await openMenu(container, "p1");
      await click(menuItem(container, "p1", "Open in a new window"));
      await openMenu(container, "s1");
      await click(menuItem(container, "s1", "Open in a new window"));
      assert.deepEqual(opened, [
        [formatAddress({ view: "inbox", entry: { kind: "pane", host: "box", pane: "%1" } }), "_blank"],
        [formatAddress({ view: "inbox", entry: { kind: "session", id: "s1" } }), "_blank"],
      ]);
    },
  ));
