import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type Address, formatAddress } from "./router";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const now = 1000;
// wideWidth is the window the tests run at, past the 760px the wide layout starts at, where a row's or epic's page draws
// beside the inbox from the start.
const wideWidth = 1024;

type Clients = ReturnType<typeof createClients>;

// Report is one address App reported: its hash, and whether it pushed or replaced the entry it shows.
type Report = [string, "push" | "replace"];

// Setup is what one render of App is given: the conversation rows and the epics the list holds.
interface Setup {
  rows?: Row[];
  epics?: EpicsView;
}

// Shown is what a test's check reads: the DOM App draws into, the reports App has made, and a render of App over
// another setup, on the same clients and DOM.
interface Shown {
  container: HTMLElement;
  reports: Report[];
  rerender: (next: Setup) => Promise<void>;
}

// fakeFetch answers each unary call with an empty message, and holds each WatchConversation stream open, as the reader
// of an open conversation follows it.
function fakeFetch(): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>(), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };
}

// show renders App over the setup, with onAddress recording each address it reports.
function show(container: HTMLElement, clients: Clients, setup: Setup, reports: Report[]): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={now}
        clients={clients}
        epics={setup.epics}
        onAddress={(address: Address, mode) => {
          reports.push([formatAddress(address), mode]);
        }}
      />,
      container,
    );
  });
}

// withApp mounts App in a fresh DOM over a fake transport, and runs check. The timers App arms run on the mocked clock,
// so a test advances none of them. App is unmounted and the DOM removed when the test ends.
async function withApp(t: TestContext, setup: Setup, check: (shown: Shown) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  window.happyDOM.setViewport({ width: wideWidth, height: 800 });
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch() }));
  const reports: Report[] = [];
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(container, clients, setup, reports);
    await check({ container, reports, rerender: (next) => show(container, clients, next, reports) });
  } finally {
    await teardown();
  }
}

// row is a conversation row with the fields a list answer gives it.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "session",
    label: init.key,
    project: "/work/api",
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "session",
    archiveId: init.key,
    ...init,
  };
}

// session is a session row of the api project, whose id is the session's id.
function session(id: string, init: Partial<Row> = {}): Row {
  return row({ key: `session:${id}`, id, archiveId: id, ...init });
}

// conversations are the rows the tests share, in the inbox order: s0 needs you, and s1 and s3 are unseen, s1 first.
function conversations(): Row[] {
  return [
    session("s0", { label: "waiting work", state: "waiting" }),
    session("s1", { label: "auth work", unseen: true, changed: 300 }),
    session("s3", { label: "docs work", unseen: true, changed: 250 }),
  ];
}

// archivedIn is the rows as a list answer carries them, with the row under key archived.
function archivedIn(list: Row[], key: string): Row[] {
  return list.map((item) => (item.key === key ? { ...item, archived: true } : item));
}

const repository = "widgets-5d0c1a7";

// epicOf is a live epic of the repository, with the given slug and fields.
function epicOf(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, { id: `${repository}/${slug}`, repository, slug, outcome: "", live: true, ...init });
}

// twoEpics are the two live epics the tests share, in the inbox order: first, then second. The first is archived when
// archived is set, as a list answer carries it. Each epic's detail is held.
function twoEpics(archived: boolean): EpicsView {
  const epics = [epicOf("first", { archived }), epicOf("second")];
  return epicsView(epics, new Map(epics.map((epic) => [epic.id, create(EpicsGetResponseSchema, { epic })])));
}

const firstKey = `epic:${repository}/first`;
const secondKey = `epic:${repository}/second`;

// press sends one keydown to the document, as a key press does, and lets Preact draw what it changed.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// unfocus takes the focus off the focused element, so the list's keys reach the list again.
async function unfocus(): Promise<void> {
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// click clicks the first element the selector names, and lets Preact draw what the click changed. Then it unfocuses.
async function click(container: HTMLElement, selector: string): Promise<void> {
  const target = container.querySelector<HTMLElement>(selector);
  assert.ok(target !== null, `${selector} is drawn`);
  await act(() => {
    target.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
  await unfocus();
}

// lineOf is the line of the row or epic under key, which is drawn while the inbox holds it.
function lineOf(container: HTMLElement, key: string): Element | null {
  return container.querySelector(`[data-key="${key}"]`);
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

test("InboxTidyOpenStays: an open conversation a list archives stays drawn and open until the cursor moves on", (t) =>
  withApp(t, { rows: conversations() }, async ({ container, reports, rerender }) => {
    const key = "session:s1";
    await click(container, `[data-key="${key}"]`);
    assert.equal(cursorKey(container), key, "the conversation is the cursor entry once it is clicked");
    const before = reports.length;
    await rerender({ rows: archivedIn(conversations(), key) });
    assert.ok(lineOf(container, key), "the archived conversation stays drawn in the inbox");
    assert.equal(cursorKey(container), key, "the archived conversation stays the cursor entry");
    assert.deepEqual(reports.slice(before), [], "the answer moves no address");
    await rerender({ rows: archivedIn(conversations(), key) });
    assert.ok(lineOf(container, key), "a later answer that still archives it keeps it drawn");
    assert.equal(cursorKey(container), key, "and it stays the cursor entry");
    await press("j");
    assert.equal(lineOf(container, key), null, "once the cursor moves on, the archived conversation leaves the inbox");
    assert.notEqual(cursorKey(container), key, "the cursor has moved on");
  }));

test("InboxTidyOpenStays: an open epic a list archives stays drawn and open until the cursor moves on", (t) =>
  withApp(t, { epics: twoEpics(false) }, async ({ container, reports, rerender }) => {
    await click(container, `[data-key="${firstKey}"]`);
    assert.equal(cursorKey(container), firstKey, "the epic is the cursor entry once it is clicked");
    const before = reports.length;
    await rerender({ epics: twoEpics(true) });
    assert.ok(lineOf(container, firstKey), "the archived epic stays drawn in the inbox");
    assert.equal(cursorKey(container), firstKey, "the archived epic stays the cursor entry");
    assert.deepEqual(reports.slice(before), [], "the answer moves no address");
    await rerender({ epics: twoEpics(true) });
    assert.ok(lineOf(container, firstKey), "a later answer that still archives it keeps it drawn");
    await press("j");
    assert.equal(lineOf(container, firstKey), null, "once the cursor moves on, the archived epic leaves the inbox");
    assert.equal(cursorKey(container), secondKey, "the cursor moves on to the second epic");
  }));
