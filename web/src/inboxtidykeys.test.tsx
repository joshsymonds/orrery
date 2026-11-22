import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema } from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const archivePath = "/orrery.v1.Epics/Archive";
const unarchivePath = "/orrery.v1.Epics/Unarchive";
// The turns a test lets a key's write run to its end before it reads that nothing was sent.
const flushRounds = 10;

// listKeys are the keys the list reads with no text box focused, each as a keydown carries it: A and Shift+A, the
// other printable keys the list's table binds, and the named keys it reads.
const printableKeys = [
  "a",
  "G",
  "g",
  "j",
  "k",
  "v",
  "w",
  "n",
  "N",
  "x",
  "e",
  "b",
  "p",
  "h",
  "i",
  "o",
  "m",
  "/",
  "|",
  "]",
  "?",
  "1",
  "2",
  "3",
  "4",
  "5",
  "6",
  "7",
  "8",
  "9",
];
const namedKeys = ["Enter", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft"];
const listKeys: KeyboardEventInit[] = [
  { key: "A" },
  { key: "A", shiftKey: true },
  ...printableKeys.map((key) => ({ key })),
  ...namedKeys.map((key) => ({ key })),
  { key: "Tab", shiftKey: true },
];

// Call is one unary request the fake server received, with its JSON body.
interface Call {
  path: string;
  body: Record<string, unknown>;
}

// FakeServer answers the App's calls on the Connect wire. Each unary call is recorded and answers an empty message.
// Each WatchConversation holds its stream open until its call aborts, and closeStreams ends the ones still open.
class FakeServer {
  readonly calls: Call[] = [];
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

// withInbox mounts the app over a fresh fake server in a fresh DOM and runs check. The app is unmounted, its streams
// closed and the DOM removed when check ends, or throws.
async function withInbox(
  setup: Setup,
  check: (container: HTMLElement, fake: FakeServer) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeServer();
  try {
    await act(() => {
      render(<App rows={setup.rows ?? []} now={now} clients={fake.clients} epics={setup.epics} />, container);
    });
    await check(container, fake);
  } finally {
    await act(() => {
      render(null, container);
    });
    fake.closeStreams();
    container.remove();
    await removeTestDom(window);
  }
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
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

// until waits for check, one turn of the event loop at a time, inside act, so the renders it lets through are applied and
// the wait reads no clock.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// flush lets the writes a key or click started run to their end before a test reads that nothing was sent.
async function flush(): Promise<void> {
  for (let round = 0; round < flushRounds; round++) {
    await act(turn);
  }
}

async function press(init: KeyboardEventInit): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { cancelable: true, ...init }));
  });
}

async function click(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// tap is a press as a browser sends it: a pointerdown, then the click that ends it.
async function tap(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("pointerdown", { bubbles: true, cancelable: true }));
  });
  await click(element);
}

// lineOf finds the line of a row, or of an epic by its key, which an epic's line carries as epic:<id>.
function lineOf(container: HTMLElement, key: string): Element | null {
  return container.querySelector(`[data-key="${key}"]`);
}

// openMenu presses a row's ⋯, which opens the row's menu.
async function openMenu(container: HTMLElement, key: string): Promise<void> {
  const line = lineOf(container, key);
  assert.ok(line, `line ${key} is rendered`);
  const button = line.querySelector<HTMLButtonElement>('button[aria-label="more"]');
  assert.ok(button, `line ${key} has a ⋯ button`);
  await tap(button);
  assert.ok(lineOf(container, key)?.querySelector(".row-menu-list"), `line ${key}'s menu is open`);
}

// menuItemOf finds the item of a line's open menu whose label is label.
function menuItemOf(container: HTMLElement, key: string, label: string): HTMLButtonElement {
  const item = [...(lineOf(container, key)?.querySelectorAll<HTMLButtonElement>(".row-menu-list button") ?? [])].find(
    (candidate) => candidate.querySelector("span")?.textContent === label,
  );
  assert.ok(item, `line ${key}'s menu holds ${label}`);
  return item;
}

// archiveCalls are the requests that archive or unarchive a row or epic.
function archiveCalls(fake: FakeServer): Call[] {
  return fake.calls.filter((call) => call.path === archivePath || call.path === unarchivePath);
}

function dataList(container: HTMLElement): string | null {
  return container.querySelector(".inbox-rows")?.getAttribute("data-list") ?? null;
}

// keyName is a key as its test names it: Shift+ before a shifted key, then the key itself.
function keyName(init: KeyboardEventInit): string {
  return `${init.shiftKey === true ? "Shift+" : ""}${init.key ?? ""}`;
}

// pressKey mounts a fresh app over setup, presses init once and checks that no archive or unarchive call was sent.
// When archived is set, the Archived view is entered first with v, which is not the key pressed.
async function pressKey(setup: Setup, init: KeyboardEventInit, archived: boolean): Promise<void> {
  await withInbox(setup, async (container, fake) => {
    if (archived) {
      await press({ key: "v" });
      assert.equal(dataList(container), "archived", "v opens the Archived view");
    }
    await press(init);
    await flush();
    assert.deepEqual(archiveCalls(fake), [], `the ${keyName(init)} key sends no Epics.Archive or Epics.Unarchive`);
  });
}

// The four setups each key in listKeys is pressed in.
const inboxRows: Setup = {
  rows: [
    session({ key: "s1", id: "s1", label: "notes" }),
    headless({ key: "h1", id: "h1", label: "docs", state: "waiting" }),
  ],
};
const inboxEpics: Setup = { epics: epicsView([epic({ id: "w/first", slug: "first", repository: "w" })], new Map()) };
const archivedRows: Setup = {
  rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })],
};
const archivedEpics: Setup = {
  epics: epicsView(
    [epic({ id: "w/old", slug: "old", outcome: "released", live: false, archived: true, phaseAt: 99000n })],
    new Map(),
  ),
};

// Each key in listKeys gets a test in each setup, so every test mounts the app once and presses one key.
for (const init of listKeys) {
  const name = keyName(init);
  test(`InboxTidyKeys: in the inbox, the ${name} key archives no conversation row`, () =>
    pressKey(inboxRows, init, false));
  test(`InboxTidyKeys: in the inbox, the ${name} key archives no epic`, () => pressKey(inboxEpics, init, false));
  test(`InboxTidyKeys: in the Archived view, the ${name} key unarchives no conversation row`, () =>
    pressKey(archivedRows, init, true));
  test(`InboxTidyKeys: in the Archived view, the ${name} key unarchives no archived epic`, () =>
    pressKey(archivedEpics, init, true));
}

test("InboxTidyKeys: the row menu archives a row in the inbox, and draws no key cap on Archive", (t) =>
  withInbox({ rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    await openMenu(container, "s1");
    const item = menuItemOf(container, "s1", "Archive");
    assertNone(item.querySelector("kbd"), "Archive draws no key cap");
    await click(item);
    await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
    assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
  }));

test("InboxTidyKeys: the row menu unarchives a row in the Archived view, and draws no key cap on Unarchive", (t) =>
  withInbox(
    { rows: [row({ key: "p", label: "pane" }), headless({ key: "ha", id: "ha", label: "old docs", archived: true })] },
    async (container, fake) => {
      await press({ key: "v" });
      await openMenu(container, "ha");
      const item = menuItemOf(container, "ha", "Unarchive");
      assertNone(item.querySelector("kbd"), "Unarchive draws no key cap");
      await click(item);
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "ha" });
    },
  ));

test("InboxTidyKeys: the head menu archives the conversation it shows, and draws no key cap on Archive", (t) =>
  withInbox({ rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container, fake) => {
    await click(one(container, '.row[data-key="s1"]'));
    await click(one(container, 'section[aria-label="reader"] .icon.more'));
    const items = container.querySelectorAll<HTMLButtonElement>('section[aria-label="reader"] .head-menu [role="menuitem"]');
    const item = [...items].find((candidate) => candidate.firstChild?.textContent === "Archive");
    assert.ok(item, "the head's More menu holds Archive");
    assertNone(item.querySelector("kbd"), "Archive draws no key cap");
    await click(item);
    await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the archive call");
    assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
  }));

test("InboxTidyKeys: the help card lists no archive key", () =>
  withInbox({ rows: [session({ key: "s1", id: "s1", label: "notes" })] }, async (container) => {
    await press({ key: "?" });
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "the help card is drawn");
    const caps = [...help.querySelectorAll("kbd")].map((cap) => cap.textContent);
    assert.equal(caps.includes("A"), false, "the help card draws no key cap A");
    const texts = [...help.querySelectorAll(".keyline")].map((line) => line.textContent);
    assert.equal(
      texts.some((text) => text.includes("archive the row")),
      false,
      "the help card names no archive action",
    );
  }));

// one returns the element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}
