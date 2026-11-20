import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type InAppNotice, InAppNoticeSchema } from "./gen/orrery/v1/notice_pb";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { until } from "./testwait";

const noticesListPath = "/orrery.v1.Notices/List";
const noticesSeenPath = "/orrery.v1.Notices/Seen";
const noticesReadAllPath = "/orrery.v1.Notices/ReadAll";
const wideWidth = 1024;
const narrowWidth = 700;
const webTime = 1_800_000_000;
// now is the page's instant in unix seconds, a minute after the newest notice.
const now = webTime + 60;
const limitKey = "five-hour@1800000000";
const alertKey = "backup";

// ListedRow is a row of the notice log as Notices.List answers it, with the read mark the server keeps for it.
interface ListedRow {
  key: Record<string, string>;
  state: string;
  title: string;
  body: string;
  time: string;
  read: boolean;
}

// FakeServer answers the notice calls and records them: each Seen's key, each ReadAll, and each List. A Seen marks the
// log row under its key read, and a ReadAll marks every row read, so each List answers the log as the page left it.
// While listFails is set, a List answers with a server error. The watch streams stay open, and every other call answers
// an empty object.
interface FakeServer {
  fetch: typeof globalThis.fetch;
  requests: { path: string; body: string }[];
  listed: ListedRow[];
  lists: number;
  listFails: boolean;
  seen: Record<string, string>[];
  readAlls: number;
}

function fakeServer(listed: ListedRow[]): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      server.requests.push({ path, body: await request.clone().text() });
      if (path.endsWith("/WatchConversation") || path.endsWith("/Watch")) {
        return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
          status: 200,
          headers: { "content-type": "application/connect+json" },
        });
      }
      if (path === noticesListPath) {
        server.lists++;
        if (server.listFails) {
          return new Response(JSON.stringify({ code: "internal", message: "list detail" }), {
            status: 500,
            headers: { "content-type": "application/json" },
          });
        }
        return json({ notices: server.listed });
      }
      if (path === noticesSeenPath) {
        const body = (await request.json()) as { key?: Record<string, string> };
        server.seen.push(body.key ?? {});
        const sent = JSON.stringify(body.key ?? {});
        server.listed = server.listed.map((row) => (JSON.stringify(row.key) === sent ? { ...row, read: true } : row));
        return json({});
      }
      if (path === noticesReadAllPath) {
        server.readAlls++;
        server.listed = server.listed.map((row) => ({ ...row, read: true }));
        return json({});
      }
      return json({});
    },
    requests: [],
    listed,
    lists: 0,
    listFails: false,
    seen: [],
    readAlls: 0,
  };
  return server;
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

// listedMessage is a row of the notice log under key, timed at time in unix seconds, read or not.
function listedMessage(key: Record<string, string>, title: string, time: number, read: boolean): ListedRow {
  return { key, state: "done", title, body: `${title} body`, time: String(time), read };
}

// readOnly is a notice log of one row, which is read.
function readOnly(): ListedRow[] {
  return [listedMessage({ pane: "b" }, "web finished", webTime, true)];
}

// The notice log the centre lists: an unread pane notice, an unread limit, and a read pane notice.
function listed(): ListedRow[] {
  return [
    listedMessage({ pane: "b" }, "web finished", webTime, false),
    listedMessage({ limit: limitKey }, "five-hour limit", webTime - 120, false),
    listedMessage({ pane: "c" }, "docs failed", webTime - 300, true),
  ];
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/api",
    host: "",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    putOff: false,
    ...init,
  };
}

// rows are two conversations in the api directory, one waiting, and one in docs, all seen, so no dwell timer runs.
function rows(): Row[] {
  return [
    row({
      key: "a",
      label: "api one",
      state: "waiting",
      question: "Ship it?",
      questionHash: "hash-a",
      changed: 300,
      seen: 300,
      archiveKind: "session",
      archiveId: "sess-a",
    }),
    row({ key: "b", label: "api two", changed: 200, seen: 200 }),
    row({ key: "c", label: "docs", project: "/work/docs", state: "done", changed: 150, seen: 150 }),
  ];
}

// stackNotice is a notice the page's stack shows, under key and titled title.
function stackNotice(key: Record<string, string>, title: string): InAppNotice {
  return create(InAppNoticeSchema, { key, state: "done", title, body: `${title} body` });
}

interface Setup {
  width: number;
  listed?: ListedRow[];
  notices?: InAppNotice[];
  // The wait between checks while the page loads. A test whose timers are mocked passes settle, since the default wait is
  // a timer.
  step?: () => Promise<void>;
}

// withDesk mounts App at the setup's width over a fake server in a fresh DOM, and runs check with its container and the
// server. App is unmounted and the DOM removed when the test ends, or as soon as signal aborts first.
async function withDesk(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement, server: FakeServer) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.happyDOM.setViewport({ width: setup.width, height: 800 });
  const container = document.createElement("div");
  document.body.append(container);
  const server = fakeServer(setup.listed ?? listed());
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: server.fetch }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(<App rows={rows()} now={now} clients={clients} notices={setup.notices} />, container);
    });
    await until(signal, () => server.lists > 0, "the notice log listed", { step: setup.step });
    await act(() => {});
    await check(container, server);
  } finally {
    await teardown();
  }
}

// one returns the element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  if (found === null) {
    throw new Error(`no element matches ${selector}`);
  }
  return found;
}

// press dispatches a click on the element, and lets Preact draw what the click changed.
async function press(element: Element | null | undefined): Promise<void> {
  if (!element) {
    throw new Error("the element to press is drawn");
  }
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// rail is the inbox rail, which must be drawn.
function rail(container: HTMLElement): HTMLElement {
  return one(container, "nav.rail.start");
}

// headBell is the bell in the inbox head, which must be drawn.
function headBell(container: HTMLElement): HTMLElement {
  return one(container, '.inbox-head .end button[aria-label="Notices"]');
}

// railBell is the Notices tile on the rail, which must be drawn.
function railBell(container: HTMLElement): HTMLElement {
  return one(rail(container), 'button[aria-label="Notices"]');
}

// labelOf names a rail child: its aria-label when it has one, else its class.
function labelOf(element: Element): string {
  return element.getAttribute("aria-label") ?? element.className;
}

// centre is the notice centre, when it is drawn.
function centre(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("section.ncentre");
}

// centreDrawn reports whether the centre is drawn. The tests assert on this flag and not on the element: a failed
// assertion on an element prints the page's DOM, which is far too large to print.
function centreDrawn(container: HTMLElement): boolean {
  return centre(container) !== null;
}

// centreRows are the rows the centre draws, in order.
function centreRows(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>("section.ncentre button.notice")];
}

// markAllOf is the centre's Mark all read control, when it is drawn.
function markAllOf(container: HTMLElement): HTMLElement | undefined {
  return Array.from(container.querySelectorAll<HTMLElement>("section.ncentre button")).find(
    (button) => button.textContent === "Mark all read",
  );
}

// stackRowOf finds the stack's row whose body is body, which the fixtures give each notice as "<title> body".
function stackRowOf(container: HTMLElement, body: string): HTMLElement | undefined {
  return [...container.querySelectorAll<HTMLElement>(".notices > .notice")].find(
    (element) => element.querySelector(".b")?.textContent === body,
  );
}

// bellCount reads the unread count the head bell draws, or "" when it draws none.
function bellCount(container: HTMLElement): string {
  return headBell(container).querySelector(".n.new")?.textContent ?? "";
}

// settle is the wait between checks in a test whose timers are mocked. It awaits act, which applies the renders the page
// has queued, and waits on no timer, so the mocked clock stays where the test put it.
function settle(): Promise<void> {
  return act(() => {});
}

// settleTurns awaits settle a fixed number of times, so a request the page has sent reaches the fake server before the
// test looks at what the server recorded.
async function settleTurns(): Promise<void> {
  for (let turn = 0; turn < 50; turn++) {
    await settle();
  }
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

// centreRowOf finds the centre's row whose body is body, which the fixtures give each notice as "<title> body".
function centreRowOf(container: HTMLElement, body: string): HTMLElement | undefined {
  return centreRows(container).find((element) => element.querySelector(".b")?.textContent === body);
}

// cursored is the key of the list row the cursor rests on, or null when none does.
function cursored(container: HTMLElement): string | null {
  return container.querySelector('[data-key][aria-current="true"]')?.getAttribute("data-key") ?? null;
}

// readerDrawn reports whether the reader column is drawn.
function readerDrawn(container: HTMLElement): boolean {
  return container.querySelector('section[aria-label="reader"]:not([hidden])') !== null;
}

// listHidden reports whether the inbox list is hidden.
function listHidden(container: HTMLElement): boolean {
  return one(container, ".list").hasAttribute("hidden");
}

// openCentre opens the centre from the inbox head's bell.
async function openCentre(container: HTMLElement): Promise<void> {
  await press(headBell(container));
  assert.ok(centre(container) !== null, "the centre is drawn once its bell is pressed");
}

// stackMore is the stack's "N more in Notices" link, which must be drawn.
function stackMore(container: HTMLElement): HTMLElement {
  return one(container, "button.notices-more");
}

// stackedNotices are the notices the page's stack holds, one per pane and one for the limit.
function stackedNotices(): InAppNotice[] {
  return [
    stackNotice({ pane: "b" }, "web finished"),
    stackNotice({ pane: "c" }, "docs failed"),
    stackNotice({ limit: limitKey }, "five-hour limit"),
    stackNotice({ pane: "a" }, "api needs you"),
  ];
}

// pressRow clicks the row under key, which moves the cursor to it and opens its reply box with the focus in it, then
// takes the focus off the box, so the page's keys reach the list.
async function pressRow(container: HTMLElement, key: string): Promise<void> {
  await press(one(container, `[data-key="${key}"]`));
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// keyPress dispatches a keydown on the document, where the page's key listener hears it, and lets Preact draw what the
// key changed.
async function keyPress(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// callsTo counts the requests the fake server took for the method whose path ends in suffix.
function callsTo(server: FakeServer, suffix: string): number {
  return server.requests.filter((request) => request.path.endsWith(suffix)).length;
}

// keysSent are the keys of each SendKeys the fake server took, in the order they were sent.
function keysSent(server: FakeServer): string[][] {
  return server.requests
    .filter((request) => request.path.endsWith(sendKeysSuffix))
    .map((request) => (JSON.parse(request.body) as { keys: string[] }).keys);
}

const sendKeysSuffix = "/orrery.v1.Panes/SendKeys";
const markSeenSuffix = "/orrery.v1.Panes/MarkSeen";
const archiveSuffix = "/orrery.v1.Epics/Archive";

test("NoticeBell: the inbox head draws the bell between search and New, labelled Notices", (t) =>
  withDesk(t.signal, { width: wideWidth }, (container) => {
    const controls = Array.from(container.querySelectorAll(".inbox-head .end > button")).map(
      (button) => button.getAttribute("aria-label") ?? button.textContent?.trim(),
    );
    assert.deepEqual(controls, ["Search sessions and epics", "Notices", "New"]);
  }));

test("NoticeBell: the rail's Notices tile sits between Search and Keys, with a page open", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await press(one(container, `[data-key="b"]`));
    assert.deepEqual(
      Array.from(rail(container).children).map(labelOf).slice(-4),
      ["New session", "Search", "Notices", "Keys"],
    );
  }));

test("NoticeBell: no title row draws a bell", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await press(one(container, `[data-key="b"]`));
    assert.equal(container.querySelectorAll(".title-row button[aria-label='Notices']").length, 0);
  }));

test("NoticeBell: with unread notices the head bell and the rail tile tip Notices · N unread", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await until(
      t.signal,
      () => headBell(container).getAttribute("title") === "Notices · 2 unread",
      "the head bell's tip",
    );
    await press(one(container, `[data-key="b"]`));
    assert.equal(railBell(container).getAttribute("data-tip"), "Notices · 2 unread");
  }));

test("NoticeBell: with no unread notices the head bell and the rail tile tip Notices", (t) =>
  withDesk(t.signal, { width: wideWidth, listed: readOnly() }, async (container) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 1, "the log's one row");
    await press(one(container, 'section.ncentre button[aria-label="Close"]'));
    assert.equal(headBell(container).getAttribute("title"), "Notices");
    await press(one(container, `[data-key="b"]`));
    assert.equal(railBell(container).getAttribute("data-tip"), "Notices");
  }));

test("NoticeBell: the unread count is drawn in the new role on the head bell and the rail tile", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await until(t.signal, () => headBell(container).querySelector(".n.new") !== null, "the head bell's count");
    assert.equal(headBell(container).querySelector(".n.new")?.textContent, "2");
    await press(one(container, `[data-key="b"]`));
    assert.equal(railBell(container).querySelector(".n.new")?.textContent, "2");
  }));

test("NoticeBell: no count is drawn on either bell when no notice is unread", (t) =>
  withDesk(t.signal, { width: wideWidth, listed: readOnly() }, async (container) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 1, "the log's one row");
    await press(one(container, 'section.ncentre button[aria-label="Close"]'));
    assert.equal(headBell(container).querySelector(".n"), null);
    await press(one(container, `[data-key="b"]`));
    assert.equal(railBell(container).querySelector(".n"), null);
  }));

test("NoticeBell: a click on the head bell opens the notice centre", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    assert.equal(centreDrawn(container), false);
    await openCentre(container);
  }));

test("NoticeBell: a click on the rail's Notices tile opens the notice centre", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await press(one(container, `[data-key="b"]`));
    assert.equal(centreDrawn(container), false);
    await press(railBell(container));
    assert.ok(centre(container) !== null, "the centre is drawn once the rail's Notices tile is pressed");
  }));

test("NoticeStack: the N more in Notices link opens the notice centre in the page", (t) =>
  withDesk(t.signal, { width: wideWidth, notices: stackedNotices() }, async (container) => {
    assert.equal(stackMore(container).textContent, "1 more in Notices");
    assert.equal(centreDrawn(container), false);
    await press(stackMore(container));
    assert.ok(centre(container) !== null, "the centre is drawn once the link is pressed");
  }));

test("NoticeStack: a notice that leaves the stack after 8 s leaves its log row unread, so the bell still counts it", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withDesk(
    t.signal,
    { width: wideWidth, notices: [stackNotice({ pane: "b" }, "web finished")], step: settle },
    async (container) => {
      await until(t.signal, () => stackRowOf(container, "web finished body") !== undefined, "the notice in the stack", {
        step: settle,
      });
      await until(t.signal, () => bellCount(container) === "2", "the bell's count of 2", { step: settle });
      await advance(t, 8000);
      assert.equal(stackRowOf(container, "web finished body"), undefined, "the notice is still on the stack at 8000 ms");
      await settleTurns();
      assert.equal(bellCount(container), "2", "the notice that left the stack no longer counts as unread");
    },
  );
});

test("NoticeBell: Mark read on a stack row lists the log again, and the bell's count drops by the notice it read", (t) =>
  withDesk(t.signal, { width: wideWidth, notices: stackedNotices() }, async (container) => {
    await until(t.signal, () => bellCount(container) === "2", "the bell's count of 2");
    await press(stackRowOf(container, "web finished body")?.querySelector(".acts > button.quiet"));
    await until(t.signal, () => bellCount(container) === "1", "the bell's count after Mark read");
  }));

test("NoticeBell: Mark all read in the centre lists the log again, and the bell's count clears", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await until(t.signal, () => bellCount(container) === "2", "the bell's count of 2");
    await openCentre(container);
    await until(t.signal, () => markAllOf(container) !== undefined, "the Mark all read control");
    await press(markAllOf(container));
    await until(t.signal, () => bellCount(container) === "", "the bell's count cleared");
  }));

test("NoticeCentre: its rows are the notice log's rows, each drawn with its body", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 3, "the centre's three rows");
    assert.deepEqual(
      centreRows(container).map((element) => element.querySelector(".b")?.textContent),
      ["web finished body", "five-hour limit body", "docs failed body"],
    );
  }));

test("NoticeCentre: a click on a row sends Seen for its key, lands on it, and closes the centre", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 3, "the centre's three rows");
    await press(centreRowOf(container, "web finished body"));
    await until(t.signal, () => server.seen.length === 1, "the Seen the row sent");
    assert.deepEqual(server.seen, [{ pane: "b" }]);
    await until(t.signal, () => cursored(container) === "b", "the cursor on the row's pane");
    assert.equal(centreDrawn(container), false, "the centre closes once its row is opened");
  }));

test("NoticeCentre: a limit row sends Seen, lands nowhere, and closes the centre", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 3, "the centre's three rows");
    const before = cursored(container);
    await press(centreRowOf(container, "five-hour limit body"));
    await until(t.signal, () => server.seen.length === 1, "the Seen the limit row sent");
    assert.deepEqual(server.seen, [{ limit: limitKey }]);
    assert.equal(cursored(container), before, "the limit row moves the cursor nowhere");
    assert.equal(centreDrawn(container), false, "the centre closes once its row is opened");
  }));

test("NoticeCentre: an alert row sends Seen, lands nowhere, and closes the centre", (t) =>
  withDesk(
    t.signal,
    { width: wideWidth, listed: [listedMessage({ alert: alertKey }, "backup failed", webTime, false)] },
    async (container, server) => {
      await pressRow(container, "c");
      await until(t.signal, () => cursored(container) === "c", "the cursor on the docs row");
      await openCentre(container);
      await until(t.signal, () => centreRows(container).length === 1, "the centre's alert row");
      await press(centreRowOf(container, "backup failed body"));
      await until(t.signal, () => server.seen.length === 1, "the Seen the alert row sent");
      assert.deepEqual(server.seen, [{ alert: alertKey }]);
      assert.equal(cursored(container), "c", "the alert row moves the cursor nowhere");
      assert.equal(centreDrawn(container), false, "the centre closes once its row is opened");
    },
  ));

test("NoticeCentre: Mark all read sends ReadAll, then lists the log again", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await openCentre(container);
    await until(t.signal, () => markAllOf(container) !== undefined, "the Mark all read control");
    const listsBefore = server.lists;
    await press(markAllOf(container));
    await until(t.signal, () => server.readAlls === 1, "the ReadAll the centre sent");
    await until(t.signal, () => server.lists > listsBefore, "the log listed again after ReadAll");
  }));

test("NoticeCentre: Close closes the centre", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await openCentre(container);
    await press(one(container, 'section.ncentre button[aria-label="Close"]'));
    assert.equal(centreDrawn(container), false);
  }));

test("NoticeBell: a failed List keeps the rows and says so", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await openCentre(container);
    await until(t.signal, () => centreRows(container).length === 3, "the centre's three rows");
    server.listFails = true;
    const listsBefore = server.lists;
    await act(() => {
      window.dispatchEvent(new window.Event("focus"));
    });
    await until(t.signal, () => server.lists > listsBefore, "the failed List sent");
    await until(t.signal, () => container.querySelector('section.ncentre [role="alert"]') !== null, "the failure line");
    assert.equal(
      one(container, 'section.ncentre [role="alert"]').textContent,
      "The notices could not be read.",
    );
    assert.equal(centreRows(container).length, 3, "the rows the last List held stay drawn");
  }));

test("NoticeCentre: wide, the centre is an overlay in the page, and the inbox list stays drawn", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container) => {
    await openCentre(container);
    const overlay = one(container, "section.ncentre");
    assert.equal(overlay.getAttribute("role"), "dialog");
    assert.ok(overlay.closest(".panes") !== null, "the overlay is drawn in the page's panes");
    assert.equal(listHidden(container), false, "the inbox list stays on screen behind the overlay");
  }));

test("NoticeCentre: under 760px the centre is a Notices page, and Back returns to the inbox", (t) =>
  withDesk(t.signal, { width: narrowWidth }, async (container) => {
    await openCentre(container);
    const page = one(container, "section.ncentre.flat");
    assert.equal(page.querySelector(".crumb.here")?.textContent, "Notices");
    assert.equal(listHidden(container), true, "the list does not draw beside the centre page");
    await press(one(container, 'section.ncentre button[aria-label="back"]'));
    assert.equal(centreDrawn(container), false);
    assert.equal(listHidden(container), false, "Back shows the inbox again");
  }));

test("NoticeCentre: under 760px Back from the centre returns to the page open before it", (t) =>
  withDesk(t.signal, { width: narrowWidth, notices: stackedNotices() }, async (container) => {
    await press(one(container, `[data-key="b"]`));
    assert.equal(readerDrawn(container), true, "the reader page is drawn before the centre opens");
    await press(stackMore(container));
    assert.ok(centre(container) !== null, "the centre is drawn once its link is pressed");
    assert.equal(readerDrawn(container), false, "the centre draws alone, without the reader page");
    await press(one(container, 'section.ncentre button[aria-label="back"]'));
    assert.equal(readerDrawn(container), true, "Back returns to the reader page");
  }));

test("NoticeCentre: Escape closes the centre overlay, and once it is shut Escape interrupts the working row", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await pressRow(container, "b");
    await until(t.signal, () => cursored(container) === "b", "the cursor on the working row");
    await openCentre(container);
    await keyPress("Escape");
    assert.equal(centreDrawn(container), false, "Escape closes the centre");
    await keyPress("Escape");
    await until(
      t.signal,
      () => callsTo(server, sendKeysSuffix) === 1,
      "with the centre shut, Escape interrupts the working row",
    );
  }));

test("NoticeCentre: under 760px Escape closes the centre page, and the reader page the centre covered draws again", (t) =>
  withDesk(t.signal, { width: narrowWidth, notices: stackedNotices() }, async (container) => {
    await pressRow(container, "b");
    await press(stackMore(container));
    assert.ok(centre(container) !== null, "the centre page is drawn");
    await keyPress("Escape");
    assert.equal(centreDrawn(container), false, "Escape closes the centre page");
    assert.equal(readerDrawn(container), true, "the reader page the centre covered draws again");
  }));

test("NoticeCentre: 1, 2 and 3 send nothing while the centre is open, and 2 answers once it is shut", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await pressRow(container, "a");
    await until(t.signal, () => cursored(container) === "a", "the cursor on the waiting row");
    await openCentre(container);
    await keyPress("1");
    await keyPress("2");
    await keyPress("3");
    await keyPress("Escape");
    assert.equal(centreDrawn(container), false, "Escape closes the centre");
    await keyPress("2");
    await until(t.signal, () => keysSent(server).length >= 1, "the answer once the centre is shut");
    assert.deepEqual(keysSent(server), [["2"]], "only the key pressed once the centre was shut was sent");
  }));

test("NoticeCentre: with the list on screen beside the centre, x, e and A act on the cursor row", (t) =>
  withDesk(t.signal, { width: wideWidth }, async (container, server) => {
    await pressRow(container, "a");
    await until(t.signal, () => cursored(container) === "a", "the cursor on the waiting row");
    await keyPress("B");
    await until(t.signal, () => listHidden(container) === false, "the pinned inbox draws beside the page");
    await openCentre(container);
    const seenBefore = callsTo(server, markSeenSuffix);
    await keyPress("x");
    await until(t.signal, () => callsTo(server, markSeenSuffix) === seenBefore + 1, "x marks the cursor row seen");
    await keyPress("e");
    await until(t.signal, () => callsTo(server, markSeenSuffix) === seenBefore + 2, "e marks the cursor row seen");
    await keyPress("A");
    await until(t.signal, () => callsTo(server, archiveSuffix) === 1, "A archives the cursor row");
  }));

test("NoticeCentre: under 760px the centre covers the cursor row, so x, e and A do nothing on it until it is shut", (t) =>
  withDesk(t.signal, { width: narrowWidth, notices: stackedNotices() }, async (container, server) => {
    await pressRow(container, "a");
    await press(stackMore(container));
    assert.ok(centre(container) !== null, "the centre page is drawn");
    await keyPress("x");
    await keyPress("e");
    await keyPress("A");
    await keyPress("Escape");
    assert.equal(centreDrawn(container), false, "Escape closes the centre page");
    await keyPress("A");
    await until(t.signal, () => callsTo(server, archiveSuffix) >= 1, "A archives once the centre is shut");
    assert.equal(callsTo(server, markSeenSuffix), 0, "x and e marked nothing while the centre covered the row");
    assert.equal(callsTo(server, archiveSuffix), 1, "only the A pressed once the centre was shut archived");
  }));
