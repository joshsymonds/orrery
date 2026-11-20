import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Epic, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { type HeadStore, localHeadStore, memoryHeadStore } from "./headfold";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

const now = 1000;
const wideWidth = 1024;
const phoneWidth = 400;
const headKey = "orrery.head.hidden";
const archivePath = "/orrery.v1.Epics/Archive";

const denHost = create(HostStatusResponseSchema, { host: "den", stats: [] });

// Stream is one WatchConversation call the app opened: the id it watches, a pane's or a session's, and the controller
// that feeds it.
interface Stream {
  id: string;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// fakeTransport answers the app's unary calls with an empty body, except the paths refused, which fail as an
// unavailable server does. Each WatchConversation call gets a stream the test feeds through emit, in the order opened.
function fakeTransport(refused: ReadonlySet<string>) {
  const streams: Stream[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (refused.has(path)) {
      return new Response(JSON.stringify({ code: "unavailable", message: "queue unreachable" }), {
        status: 503,
        headers: { "content-type": "application/json" },
      });
    }
    if (!path.endsWith("/WatchConversation")) {
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
    }
    const watched = JSON.parse(new TextDecoder().decode(new Uint8Array(await request.arrayBuffer()).subarray(5)));
    const stream: Stream = { id: watched.pane ?? watched.id, controller: null };
    streams.push(stream);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        stream.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };
  return { fetch, streams };
}

type FakeTransport = ReturnType<typeof fakeTransport>;

// emit sends one message down a stream: a flag byte of 0, the payload's length, and the JSON payload.
function emit(stream: Stream, message: object): void {
  assert.ok(stream.controller !== null, "the stream is open");
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  stream.controller.enqueue(bytes);
}

// row is a pane row that has been seen, so no dwell timer runs on the cursor's row.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "den",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 200,
    seen: 200,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// makeEpic is a live epic of the widgets repository with the given slug, steered by the conversation id given.
function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, { id: `widgets/${slug}`, repository: "widgets", slug, outcome: "", live: true, ...init });
}

// askingEpics is an epic list whose Get answers ask each epic the question each one names.
function askingEpics(...asks: [Epic, string][]): EpicsView {
  return epicsView(
    asks.map(([epic]) => epic),
    new Map(asks.map(([epic, question]) => [epic.id, create(EpicsGetResponseSchema, { epic, questions: [{ id: `Q-${epic.slug}`, text: question }] })])),
  );
}

// memoryStorage is the storage a local head store keeps its setting in, as a Map of its keys.
function storageOf(items: Map<string, string>): Pick<Storage, "getItem" | "setItem" | "removeItem"> {
  return {
    getItem: (key) => items.get(key) ?? null,
    setItem: (key, value) => {
      items.set(key, value);
    },
    removeItem: (key) => {
      items.delete(key);
    },
  };
}

interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  host?: HostStatusResponse;
  width?: number;
  headStore?: HeadStore;
  refused?: string[];
}

// Harness is what a check reads: the app's container, its fake transport, and a remount that draws App again over the
// same props and store, as a reload does.
interface Harness {
  container: HTMLElement;
  transport: FakeTransport;
  remount: () => Promise<void>;
}

// withHead mounts the app over the fake transport in a fresh DOM at the width given, and runs check. The app is
// unmounted and the DOM removed when the test ends, or as soon as signal aborts if it times out first.
async function withHead(signal: AbortSignal, setup: Setup, check: (harness: Harness) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = setup.width ?? wideWidth;
  const container = document.createElement("div");
  document.body.append(container);
  const transport = fakeTransport(new Set(setup.refused ?? []));
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: transport.fetch }));
  const draw = () =>
    act(() => {
      render(
        <App
          rows={setup.rows ?? [row({ key: "api", label: "api" })]}
          error=""
          now={now}
          clients={clients}
          epics={setup.epics}
          host={setup.host ?? denHost}
          headStore={setup.headStore}
        />,
        container,
      );
    });
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw();
    await check({
      container,
      transport,
      remount: async () => {
        await act(() => {
          render(null, container);
        });
        await draw();
      },
    });
  } finally {
    await teardown();
  }
}

// untilTurn waits for check, stepping by turn inside act, so each round applies the renders and stream deltas it lets
// through, and no clock runs.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// clickOn clicks the first element that matches selector, and fails when none does.
async function clickOn(container: HTMLElement, selector: string): Promise<void> {
  const element = container.querySelector<HTMLElement>(selector);
  if (element === null) {
    assert.fail(`${selector} is drawn`);
  }
  await act(() => {
    element.click();
  });
}

// clickMenuItem opens the More menu of the cursor's head and chooses the item with the label given.
async function clickMoreItem(container: HTMLElement, label: string): Promise<void> {
  await clickOn(container, 'section[aria-label="reader"] .icon.more');
  const item = [...container.querySelectorAll<HTMLElement>('section[aria-label="reader"] .head-menu [role="menuitem"]')].find(
    (candidate) => candidate.textContent?.startsWith(label),
  );
  if (item === undefined) {
    assert.fail(`the More menu has the item ${label}`);
  }
  await act(() => {
    item.click();
  });
}

// scrollReader scrolls the cursor's reader to top, the way the owner does. happy-dom lays nothing out, so the reader's
// box is given a height and a content height, which read as a list of 2000 pixels in a 400 pixel window.
async function scrollReader(container: HTMLElement, top: number): Promise<void> {
  const reader = container.querySelector<HTMLElement>('section[aria-label="reader"] .reader');
  if (reader === null) {
    assert.fail("the cursor's reader is drawn");
  }
  Object.defineProperty(reader, "scrollHeight", { value: 2000, configurable: true });
  Object.defineProperty(reader, "clientHeight", { value: 400, configurable: true });
  await act(() => {
    reader.scrollTop = top;
    reader.dispatchEvent(new Event("scroll"));
  });
}

function shownLabel(container: HTMLElement): string | null {
  return container.querySelector('section[aria-label="reader"] .title-row .crumb.here')?.textContent ?? null;
}

// showRow moves the cursor down the list, then back up it, until its reader shows the row with the label given. An epic
// the list draws before the rows takes the first step, so the walk is in both directions.
async function showRow(container: HTMLElement, label: string): Promise<void> {
  for (const [key, steps] of [
    ["j", 4],
    ["k", 8],
  ] as const) {
    for (let step = 0; step < steps && shownLabel(container) !== label; step++) {
      await press(key);
    }
  }
  assert.equal(shownLabel(container), label, `the cursor rests on ${label}`);
}

function foldOf(container: HTMLElement): string | null {
  return container.querySelector('section[aria-label="reader"] .convo-head')?.getAttribute("data-fold") ?? null;
}

function bandLines(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>('section[aria-label="reader"] .subj.attention')].map(
    (line) => line.dataset.epic ?? "",
  );
}

// streamOf is the stream the app opened for the id given, once it has opened one.
function streamOf(transport: FakeTransport, id: string): Stream {
  const stream = transport.streams.find((candidate) => candidate.id === id);
  assert.ok(stream !== undefined, `the stream of ${id} is open`);
  return stream;
}

// openConversation opens a row's conversation: it waits for the stream the app opened for the id, and feeds it a reset
// with a goal and the state given, which draws the reader and the band's goal line.
async function openConversation(
  signal: AbortSignal,
  container: HTMLElement,
  transport: FakeTransport,
  id: string,
  state: Record<string, unknown> = {},
): Promise<void> {
  await untilTurn(signal, () => transport.streams.some((stream) => stream.id === id), `the stream of ${id}`);
  emit(streamOf(transport, id), {
    transcript: "t",
    seq: "1",
    reset: true,
    items: [],
    state: { goal: "Ship the rota before Friday.", ...state },
  });
  await untilTurn(signal, () => textOf(container, 'section[aria-label="reader"] .subj.goal .st') !== null, "the goal line");
}

function textOf(container: HTMLElement, selector: string): string | null {
  return container.querySelector(selector)?.textContent ?? null;
}

// badgeOf is the folded line's badge for the key given: a steered epic's id, agents, or running.
function badgeOf(container: HTMLElement, key: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`section[aria-label="reader"] .compact [data-badge="${key}"]`);
}

// replyBox is the reply box of the cursor's reader, or null while none is drawn.
function replyBox(container: HTMLElement): HTMLTextAreaElement | null {
  return container.querySelector<HTMLTextAreaElement>('section[aria-label="reader"] .reply:not([hidden]) .composer textarea');
}

test("Band: an epic whose steering is this row's archiveId shows its line with Answer, Answer opens the epic, and another conversation's epic never shows", (t) => {
  const asking = makeEpic("rota-export", { steering: "orr-api" });
  const elsewhere = makeEpic("tide-alerts", { steering: "orr-web" });
  const epics = askingEpics([asking, "Which tide table?"], [elsewhere, "Ship it?"]);
  const rows = [
    row({ key: "api", label: "api", archiveKind: "session", archiveId: "orr-api" }),
    row({ key: "web", label: "web", archiveKind: "session", archiveId: "orr-web" }),
  ];
  return withHead(t.signal, { rows, epics }, async ({ container, transport }) => {
    await showRow(container, "api");
    await openConversation(t.signal, container, transport, "api");
    assert.equal(textOf(container, 'section[aria-label="reader"] .subj.goal .what'), "Goal");
    assert.deepEqual(bandLines(container), [asking.id], "the api conversation's band names only its own epic");
    const line = container.querySelector('section[aria-label="reader"] .subj.attention');
    assert.equal(line?.querySelector(".what b")?.textContent, "rota-export");
    assert.equal(line?.querySelector(".st")?.textContent, "asks: Which tide table?");
    assert.equal(line?.querySelector(".end")?.textContent, "Answer ›");
    await showRow(container, "web");
    assert.deepEqual(bandLines(container), [elsewhere.id], "the web conversation never shows the api conversation's epic");
    await showRow(container, "api");
    await clickOn(container, 'section[aria-label="reader"] .subj.attention');
    assert.equal(
      container.querySelector('section[aria-label="epic"]')?.hasAttribute("hidden"),
      false,
      "Answer opens the epic",
    );
  });
});

test("HeadTitle: the title row has no pill or state, and its crumbs are the directory in the place colour and the label", (t) =>
  withHead(t.signal, { rows: [row({ key: "api", label: "api", state: "waiting" })] }, ({ container }) => {
    const title = 'section[aria-label="reader"] .convo-head > header.title-row';
    assert.equal(textOf(container, `${title} .crumb.place`), "app");
    assert.equal(textOf(container, `${title} .crumb.here`), "api");
    assert.equal(container.querySelector(`${title} .state`), null, "the title row draws no state");
    assert.equal(container.querySelector(`${title} .pill`), null, "the title row draws no pill");
  }));

test("HeadTitle: a pane on another host reads its host then 'on another host: no meters', and draws no stats", (t) =>
  withHead(t.signal, { rows: [row({ key: "x", label: "x", host: "laptop" })] }, ({ container }) => {
    assert.equal(textOf(container, 'section[aria-label="reader"] .pane-strip-host'), "laptop");
    assert.equal(textOf(container, 'section[aria-label="reader"] .pane-strip .other'), "on another host: no meters");
    assert.equal(container.querySelectorAll('section[aria-label="reader"] .meter').length, 0);
  }));

test("HeadTitle: a failed More Archive puts its note under the title row, with the reason and that nothing changed", (t) =>
  withHead(
    t.signal,
    { rows: [row({ key: "api", label: "api", archiveKind: "session", archiveId: "s-api" })], refused: [archivePath] },
    async ({ container }) => {
      await clickMoreItem(container, "Archive");
      await untilTurn(
        t.signal,
        () => textOf(container, 'section[aria-label="reader"] .page-note') !== null,
        "the archive note",
      );
      const note = container.querySelector('section[aria-label="reader"] .page-note');
      assert.equal(
        note?.textContent,
        "Could not archive this session: the server did not answer. Nothing changed.",
      );
      assert.equal(note?.previousElementSibling?.matches("header.title-row"), true, "the note sits under the title row");
    },
  ));

test("PhoneHead: under 760px, scrolling off the newest line folds the header to its reading line, returning to the newest opens it, and a tap opens it", (t) =>
  withHead(t.signal, { width: phoneWidth }, async ({ container, transport }) => {
    await openConversation(t.signal, container, transport, "api");
    await scrollReader(container, 0);
    assert.equal(foldOf(container), "reading", "a scroll off the newest line folds the header");
    assert.ok(container.querySelector('section[aria-label="reader"] .compact') !== null, "the folded line is drawn");
    assert.equal(container.querySelector('section[aria-label="reader"] .subject'), null, "the band is not drawn");
    await scrollReader(container, 1600);
    assert.equal(foldOf(container), "open", "returning to the newest line opens the header");
    await scrollReader(container, 0);
    assert.equal(foldOf(container), "reading");
    await clickOn(container, 'section[aria-label="reader"] .compact');
    assert.equal(foldOf(container), "open", "a tap on the folded line opens it");
  }));

test("PhoneHead: Hide the header keeps it folded in another conversation and after a remount over the same storage, until a tap opens it", (t) => {
  const items = new Map<string, string>();
  return withHead(
    t.signal,
    { width: phoneWidth, rows: [row({ key: "api", label: "api" }), row({ key: "web", label: "web" })], headStore: localHeadStore(storageOf(items)) },
    async ({ container, transport, remount }) => {
      await openConversation(t.signal, container, transport, "api");
      await clickOn(container, 'section[aria-label="reader"] button.fold-head');
      assert.equal(foldOf(container), "hidden", "the band's hide control folds the header");
      assert.equal(items.get(headKey), "1", "the hidden setting is kept");
      await showRow(container, "web");
      assert.equal(foldOf(container), "hidden", "another conversation is folded too");
      await remount();
      await showRow(container, "api");
      assert.equal(foldOf(container), "hidden", "a remount over the same storage keeps it folded");
      await clickOn(container, 'section[aria-label="reader"] .compact');
      assert.equal(foldOf(container), "open", "a tap opens the header");
      assert.equal(items.has(headKey), false, "opening clears the kept setting");
    },
  );
});

test("PhoneHead: a folded header with an ask draws its line as attention, with '<slug> asks'", (t) => {
  const asking = makeEpic("rota-export", { steering: "orr-api" });
  return withHead(
    t.signal,
    {
      width: phoneWidth,
      rows: [row({ key: "api", label: "api", archiveKind: "session", archiveId: "orr-api" })],
      epics: askingEpics([asking, "Which tide table?"]),
    },
    async ({ container }) => {
      await showRow(container, "api");
      await clickMoreItem(container, "Hide the header");
      assert.equal(foldOf(container), "hidden");
      const compact = container.querySelector('section[aria-label="reader"] .compact');
      assert.equal(compact?.classList.contains("attention"), true, "the folded line is attention");
      assert.equal(textOf(container, 'section[aria-label="reader"] .compact .cask'), "rota-export asks");
    },
  );
});

test("PhoneHead: a waiting row's own question makes the folded line attention, named by its directory", (t) =>
  withHead(t.signal, { width: phoneWidth, rows: [row({ key: "api", label: "api", state: "waiting" })] }, async ({ container, transport }) => {
    await openConversation(t.signal, container, transport, "api");
    await clickMoreItem(container, "Hide the header");
    assert.equal(foldOf(container), "hidden");
    const compact = container.querySelector('section[aria-label="reader"] .compact');
    assert.equal(compact?.classList.contains("attention"), true, "the folded line is attention");
    assert.equal(textOf(container, 'section[aria-label="reader"] .compact .cask'), "app asks");
  }));

test("PhoneHead: a waiting row whose pane is put off draws no asking words and no attention on its folded line", (t) =>
  withHead(t.signal, { width: phoneWidth, rows: [row({ key: "api", label: "api", state: "waiting" })] }, async ({ container, transport }) => {
    await openConversation(t.signal, container, transport, "api", { pane: { putOff: true } });
    await clickMoreItem(container, "Hide the header");
    assert.equal(foldOf(container), "hidden");
    const compact = container.querySelector('section[aria-label="reader"] .compact');
    assert.equal(compact?.classList.contains("attention"), false, "the folded line is not attention");
    assert.equal(textOf(container, 'section[aria-label="reader"] .compact .cask'), null, "no asking words");
  }));

test("PhoneHead: a session's background tasks draw the agents and running badges with their counts, and a steered epic that asks draws its badge with '?'", (t) => {
  const asking = makeEpic("rota-export", { steering: "orr-api" });
  return withHead(
    t.signal,
    {
      width: phoneWidth,
      rows: [row({ key: "sess", kind: "session", label: "api", archiveKind: "session", archiveId: "orr-api" })],
      epics: askingEpics([asking, "Which tide table?"]),
    },
    async ({ container, transport }) => {
      await showRow(container, "api");
      await openConversation(t.signal, container, transport, "sess", {
        background: [
          { id: "a1", type: "agent", status: "running" },
          { id: "a2", type: "agent", status: "running" },
          { id: "a3", type: "agent", status: "done" },
          { id: "s1", type: "shell", status: "running" },
        ],
      });
      await clickMoreItem(container, "Hide the header");
      assert.equal(foldOf(container), "hidden");
      assert.equal(badgeOf(container, "agents")?.textContent, "agents 2", "two agents are running");
      assert.equal(badgeOf(container, "running")?.textContent, "running 1", "one shell is running");
      assert.equal(badgeOf(container, asking.id)?.textContent, "rota?", "the asking epic's badge names it with a question mark");
      assert.equal(badgeOf(container, asking.id)?.classList.contains("attention"), true, "the asking epic's badge is attention");
    },
  );
});

test("PhoneHead: focusing the reply box keeps the title row, and the header's fold", (t) =>
  withHead(t.signal, { width: phoneWidth }, async ({ container, transport }) => {
    await openConversation(t.signal, container, transport, "api");
    await scrollReader(container, 0);
    assert.equal(foldOf(container), "reading");
    await press("i");
    await untilTurn(
      t.signal,
      () => {
        const box = replyBox(container);
        return box !== null && document.activeElement === box;
      },
      "the reply box focus",
    );
    assert.ok(container.querySelector('section[aria-label="reader"] > .convo-head > header.title-row') !== null, "the title row stays");
    assert.equal(foldOf(container), "reading", "focusing the reply box changes no fold");
  }));
