import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// InboxWebNotice: a list answer that moves conversations leaves one move notice, which names each moved row and its place
// and which Show and Undo act on; the notice goes toastMs after it shows, on the fake clock, and draws at every width. Each
// test mounts App over invented rows in a fresh DOM and reads the DOM.

const now = 1000;
// wideWidth is past the 760px the wide layout starts at, and narrowWidth is under it.
const wideWidth = 1024;
const narrowWidth = 600;
// layoutWidth is the width the wide layout starts at: a window at 760px is wide.
const layoutWidth = 760;
// toastMs is how long a toast stays on the App's clock, which the App's own toast is set to.
const toastMs = 5000;
const widgets = "/home/owner/code/widgets";
const gadgets = "/home/owner/code/gadgets";
const unarchivePath = "/orrery.v1.Epics/Unarchive";

// session is a headless session row of the widgets directory, which a list answer gives as done, neither ended nor
// archived, unless the init says otherwise.
function session(id: string, init: Partial<Row> = {}): Row {
  return {
    key: `session:${id}`,
    id,
    archiveId: id,
    kind: "session",
    label: `${id} work`,
    project: widgets,
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    tool: "",
    putOff: false,
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "headless",
    ...init,
  };
}

// The rows the tests answer with, each invented. tide is first, so the cursor rests on it until another entry is chosen.
const tide = session("a", { label: "Tide table cache", changed: 400 });
const lamp = session("b", { label: "Lamp reshuffle", changed: 300 });
const rota = session("c", { label: "Rota export", changed: 200 });
const kelp = session("d", { label: "Kelp survey", changed: 100 });
// pane is a pane with no orrery_id, which has no archive kind and so cannot be unarchived.
const pane: Row = session("e", { label: "Pane notes", kind: "pane", archiveKind: "none", archiveId: "" });

interface Call {
  path: string;
  body: unknown;
}

// fakeFetch answers each unary call with an empty message and records it, and holds each WatchConversation stream open, as
// the reader of an open conversation follows it.
function fakeFetch(calls: Call[]): (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>(), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    const bytes = new Uint8Array(await request.arrayBuffer());
    calls.push({ path, body: bytes.byteLength === 0 ? {} : JSON.parse(new TextDecoder().decode(bytes)) });
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };
}

// show renders App over the rows a list answer carries, into container, and lets Preact draw what it changed.
function show(container: HTMLElement, clients: ReturnType<typeof createClients>, rows: Row[]): Promise<void> {
  return act(() => {
    render(<App rows={rows} now={now} clients={clients} host={null} onAddress={() => {}} />, container);
  });
}

interface Shown {
  container: HTMLElement;
  calls: Call[];
  // rerender draws App over the rows a later list answer carries.
  rerender: (rows: Row[]) => Promise<void>;
  // tick moves the fake clock forward, and lets Preact draw what its timers changed.
  tick: (ms: number) => Promise<void>;
}

// withApp mounts App over the rows in a fresh DOM, at the window's width, with its timers on the fake clock, and runs check.
// App is unmounted and the DOM removed when the test ends.
async function withApp(t: TestContext, width: number, rows: Row[], check: (shown: Shown) => Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  window.happyDOM.setViewport({ width, height: 800 });
  const container = document.createElement("div");
  document.body.append(container);
  const calls: Call[] = [];
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch(calls) }));
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(container, clients, rows);
    await check({
      container,
      calls,
      rerender: (next) => show(container, clients, next),
      tick: (ms) =>
        act(() => {
          t.mock.timers.tick(ms);
        }),
    });
  } finally {
    await teardown();
  }
}

// waitFor lets the App's calls run, drawing what they change, until check holds.
function waitFor(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(() => turn()) });
}

// toastsOf is every toast drawn.
function toastsOf(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".toast")];
}

// noticeOf is the one notice drawn, which the test asserts is the only toast.
function noticeOf(container: HTMLElement): HTMLElement {
  const toasts = toastsOf(container);
  assert.equal(toasts.length, 1, `expected one notice, got ${toasts.length}`);
  return toasts[0];
}

// noticeText is the words of the one notice drawn.
function noticeText(container: HTMLElement): string | null | undefined {
  return noticeOf(container).querySelector(".grow")?.textContent;
}

// pressNotice presses the notice's button labelled label, and lets Preact draw what the press changed.
async function pressNotice(container: HTMLElement, label: string): Promise<void> {
  const button = [...noticeOf(container).querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent === label,
  );
  assert.ok(button !== undefined, `the notice has no ${label} button`);
  await act(() => {
    button.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// cardNamed is the project card of the project named name, which the inbox must draw.
function cardNamed(container: HTMLElement, name: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`section.dir[aria-label="${name}"]`);
  assert.ok(card !== null, `the inbox draws no card of ${name}`);
  return card;
}

// press sends one keydown to the document, as a key press does, and lets Preact draw what it changed.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// listHidden is whether the inbox's list is hidden, which the narrow layout does while a page is drawn alone.
function listHidden(container: HTMLElement): boolean {
  const list = container.querySelector<HTMLElement>(".list");
  assert.ok(list, "the list is rendered");
  return list.hasAttribute("hidden");
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// unarchiveCalls are the Unarchive calls recorded so far.
function unarchiveCalls(calls: Call[]): Call[] {
  return calls.filter((call) => call.path === unarchivePath);
}

test("InboxWebNotice: one answer that moves one row leaves one notice that names the row and its new place", (t) =>
  withApp(t, wideWidth, [tide, lamp], async ({ container, rerender }) => {
    assertNone(container.querySelector(".toast"), "the first answer leaves a notice");
    await rerender([tide, { ...lamp, project: gadgets }]);
    assert.equal(noticeText(container), "Moved “Lamp reshuffle” to gadgets.", "the notice does not name the row and its place");
  }));

test("InboxWebNotice: one answer that moves several rows leaves one notice with their count and the name of every moved row", (t) =>
  withApp(t, wideWidth, [tide, lamp, rota, kelp], async ({ container, rerender }) => {
    await rerender([
      tide,
      { ...lamp, project: gadgets },
      { ...rota, project: gadgets },
      { ...kelp, project: gadgets },
    ]);
    assert.equal(
      noticeText(container),
      "Moved 3 conversations: “Lamp reshuffle”, “Rota export”, “Kelp survey”",
      "the notice does not count and name every moved row",
    );
  }));

test("InboxWebNotice: Undo sends Unarchive for every moved row whose archive kind is not none, and takes the notice away", (t) =>
  withApp(t, wideWidth, [tide, lamp, rota, pane], async ({ container, rerender, calls }) => {
    await rerender([tide, { ...lamp, project: gadgets }, { ...rota, project: gadgets }, { ...pane, project: gadgets }]);
    await pressNotice(container, "Undo");
    await waitFor(t.signal, () => unarchiveCalls(calls).length === 2, "the Unarchive calls");
    await act(() => turn());
    assert.deepEqual(
      unarchiveCalls(calls).map((call) => call.body),
      [
        { kind: "headless", id: "b" },
        { kind: "headless", id: "c" },
      ],
      "Undo did not unarchive each moved row that can be unarchived",
    );
    assertNone(container.querySelector(".toast"), "Undo left the notice drawn");
  }));

test("InboxWebNotice: Show on a notice whose row moved behind an ended line opens that line and rests the cursor on the row", (t) =>
  withApp(t, wideWidth, [tide, lamp], async ({ container, rerender }) => {
    await rerender([tide, { ...lamp, ended: true }]);
    assert.equal(noticeText(container), "Moved “Lamp reshuffle” to widgets’s ended line.", "the notice does not name the ended line");
    await pressNotice(container, "Show");
    assert.equal(
      cardNamed(container, "widgets").querySelector<HTMLElement>(".fold-line")?.getAttribute("aria-expanded"),
      "true",
      "Show did not open the ended line",
    );
    assert.equal(cursorKey(container), lamp.key, "Show did not rest the cursor on the row");
    assertNone(container.querySelector(".toast"), "Show left the notice drawn");
  }));

test("InboxWebNotice: Show on a notice whose row moved to Archived switches the view to Archived and rests the cursor on the row", (t) =>
  withApp(t, wideWidth, [tide, lamp], async ({ container, rerender }) => {
    await rerender([tide, { ...lamp, archived: true }]);
    assert.equal(noticeText(container), "Moved “Lamp reshuffle” to Archived.", "the notice does not name Archived");
    await pressNotice(container, "Show");
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "archived", "Show did not switch the view");
    assert.equal(cursorKey(container), lamp.key, "Show did not rest the cursor on the row");
    assertNone(container.querySelector(".toast"), "Show left the notice drawn");
  }));

for (const [width, band] of [
  [narrowWidth, "under 760px"],
  [layoutWidth, "at 760px"],
  [wideWidth, "over 760px"],
] as const) {
  test(`InboxWebNotice: a move notice draws ${band}, with its Show and Undo`, (t) =>
    withApp(t, width, [tide, lamp], async ({ container, rerender }) => {
      await rerender([tide, { ...lamp, project: gadgets }]);
      assert.equal(noticeText(container), "Moved “Lamp reshuffle” to gadgets.", `the notice is not drawn ${band}`);
      assert.deepEqual(
        [...noticeOf(container).querySelectorAll("button")].map((button) => button.textContent),
        ["Show", "Undo"],
        `the notice has not its Show and Undo ${band}`,
      );
    }));
}

test("InboxWebNotice: under 760px, Show closes an open row's page, so the list is drawn with the cursor on the moved row", (t) =>
  withApp(t, narrowWidth, [tide, lamp], async ({ container, rerender }) => {
    await press("Enter");
    assert.equal(listHidden(container), true, "Enter did not open the cursor's row alone");
    await rerender([tide, { ...lamp, project: gadgets }]);
    await pressNotice(container, "Show");
    assert.equal(listHidden(container), false, "Show left the list hidden under the open page");
    assert.equal(cursorKey(container), lamp.key, "Show did not rest the cursor on the moved row");
  }));

test("InboxWebNotice: a move notice goes toastMs after it shows, on the fake clock", (t) =>
  withApp(t, wideWidth, [tide, lamp], async ({ container, rerender, tick }) => {
    await rerender([tide, { ...lamp, project: gadgets }]);
    await tick(toastMs - 1);
    assert.equal(toastsOf(container).length, 1, "the notice went before its time");
    await tick(1);
    assertNone(container.querySelector(".toast"), "the notice stays after its time");
  }));

test("InboxWebNotice: a later answer's moves replace the notice an earlier answer left, name only their rows, and restart its time", (t) =>
  withApp(t, wideWidth, [tide, lamp, rota], async ({ container, rerender, tick }) => {
    await rerender([tide, { ...lamp, project: gadgets }, rota]);
    await tick(3000);
    await rerender([tide, { ...lamp, project: gadgets }, { ...rota, project: gadgets }]);
    assert.equal(noticeText(container), "Moved “Rota export” to gadgets.", "the later answer's notice does not name its own row");
    await tick(toastMs - 1);
    assert.equal(toastsOf(container).length, 1, "the later notice went with the earlier one's time");
    await tick(1);
    assertNone(container.querySelector(".toast"), "the later notice stays after its time");
  }));
