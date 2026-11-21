import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  EpicsListResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { start } from "./main";
import { mockTimers } from "./testclock";
import { until } from "./testwait";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const panesListPath = "/orrery.v1.Panes/List";
const sessionsListPath = "/orrery.v1.Sessions/List";
const epicsListPath = "/orrery.v1.Epics/List";
const epicsGetPath = "/orrery.v1.Epics/Get";
const panesWatchPath = "/orrery.v1.Panes/WatchConversation";
const noticesWatchPath = "/orrery.v1.Notices/Watch";
const noticesSeenPath = "/orrery.v1.Notices/Seen";
const noticesListPath = "/orrery.v1.Notices/List";
const apiRow = "pane:box:%1";
const webRow = "pane:box:%2";
const limitKey = "five-hour@1800000000";
// clockSeconds is the page's instant in unix seconds, which the age test sets on the mocked clock.
const clockSeconds = 1_800_000_000;
// A poll interval far longer than any test waits, so the rows load once, when the page starts.
const longPollMs = 60_000;
// The pause before a stream that failed unavailable reopens, short so the tests' waits stay well inside their deadline.
const retryMs = 5;

// Synthetic panes, both waiting and both read. The api pane changed last, so the list shows it first.
const panesBody = {
  panes: [
    {
      id: "%1",
      host: "box",
      session: "work",
      label: "api",
      cwd: "/work/api",
      state: "waiting",
      summary: "",
      question: "",
      changed: "40",
      seen: "40",
    },
    {
      id: "%2",
      host: "box",
      session: "work",
      label: "web",
      cwd: "/work/web",
      state: "waiting",
      summary: "",
      question: "",
      changed: "30",
      seen: "30",
    },
  ],
};

// Fixtures are the epics the list answers with, and the detail Epics.Get answers each epic's id with.
interface Fixtures {
  epics?: Epic[];
  details?: EpicsGetResponse[];
  // The notice history Notices.List answers with, as the JSON rows the server sends.
  listed?: object[];
}

// FakeServer answers the page's lists from the fixtures above and holds the Notices.Watch stream open for a test to
// send changes into. It records the key of each Notices.Seen the page sent, as the JSON body named it, counts the
// Notices.List calls and the pane streams the page opened.
interface FakeServer {
  fetch: typeof globalThis.fetch;
  // The controller of the open Notices.Watch stream, or null before the page opens it and after it is aborted.
  notices: ReadableStreamDefaultController<Uint8Array> | null;
  seen: Record<string, string>[];
  lists: number;
  listed: object[];
  paneStreams: number;
  // While set, Notices.Seen answers with a server error.
  seenFails: boolean;
}

function fakeServer({ epics = [], details = [], listed = [] }: Fixtures = {}): FakeServer {
  const server: FakeServer = {
    fetch: async (input, init) => {
      const request = new Request(input, init);
      const path = new URL(request.url).pathname;
      if (path === panesListPath) {
        return json(panesBody);
      }
      if (path === sessionsListPath) {
        return json({ sessions: [], seq: "0" });
      }
      if (path === epicsListPath) {
        return json(toJson(EpicsListResponseSchema, create(EpicsListResponseSchema, { epics })));
      }
      if (path === epicsGetPath) {
        const body = (await request.json()) as { id?: string };
        const detail = details.find((candidate) => candidate.epic?.id === body.id);
        if (detail === undefined) {
          return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
        }
        return json(toJson(EpicsGetResponseSchema, detail));
      }
      if (path === noticesWatchPath) {
        const stream = new ReadableStream<Uint8Array>({
          start: (controller) => {
            server.notices = controller;
          },
        });
        // The abort is heard on the signal the transport passed: a Request's own signal stops following it once the
        // garbage collector takes the Request.
        const signal = init?.signal ?? request.signal;
        signal.addEventListener(
          "abort",
          () => {
            server.notices?.error(signal.reason);
            server.notices = null;
          },
          { once: true },
        );
        return new Response(stream, { status: 200, headers: { "content-type": "application/connect+json" } });
      }
      if (path === noticesListPath) {
        server.lists++;
        return json({ notices: server.listed });
      }
      if (path === noticesSeenPath) {
        const body = (await request.json()) as { key?: Record<string, string> };
        server.seen.push(body.key ?? {});
        if (server.seenFails) {
          return new Response(JSON.stringify({ code: "internal", message: "seen detail" }), {
            status: 500,
            headers: { "content-type": "application/json" },
          });
        }
        return json({});
      }
      if (path === panesWatchPath) {
        server.paneStreams++;
      }
      return new Response("{}", { status: 404, headers: { "content-type": "application/json" } });
    },
    notices: null,
    seen: [],
    lists: 0,
    listed,
    paneStreams: 0,
    seenFails: false,
  };
  return server;
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function clientsFor(server: FakeServer): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch: server.fetch }));
}

// listedMessage is a row of the notice log under key, timed at time in unix seconds.
function listedMessage(key: Record<string, string>, title: string, time: number): object {
  return { key, state: "done", title, body: `${title} body`, time: String(time), read: false };
}

// shownMessage is the Notices.Watch change that shows a notice under key, opening the block it names when one is given.
function shownMessage(key: Record<string, string>, title: string, block?: Record<string, string>): object {
  return { shown: { key, state: "done", title, body: `${title} body`, block } };
}

// endedMessage is the Notices.Watch change that ends the notice under key.
function endedMessage(key: Record<string, string>): object {
  return { ended: key };
}

// sendNotice writes one change onto the open Notices.Watch stream.
function sendNotice(server: FakeServer, message: object): void {
  if (server.notices === null) {
    throw new Error("the notice stream is not open");
  }
  server.notices.enqueue(envelope(0, message));
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

// stackRows reads the rows the stack draws, in order.
function stackRows(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(".notices > .notice")];
}

// rowOf finds the stack's row for the notice titled title. The fixtures give each notice the body "<title> body", which
// its row draws, so the body names the row.
function rowOf(root: HTMLElement, title: string): HTMLElement | undefined {
  return stackRows(root).find((row) => row.querySelector(".b")?.textContent === `${title} body`);
}

// clickRowControl clicks the control of the stack's row titled title: its Answer or Open, or its Mark read when quiet is
// set. It reports whether such a row and control were drawn.
function clickRowControl(root: HTMLElement, title: string, quiet = false): boolean {
  const selector = quiet ? ".acts > button.quiet" : ".acts > button:not(.quiet)";
  const button = rowOf(root, title)?.querySelector<HTMLElement>(selector);
  if (!button) {
    return false;
  }
  button.dispatchEvent(new Event("click", { bubbles: true }));
  return true;
}

// clickRow clicks the list row under key, which moves the cursor to it. It reports whether such a row was drawn.
function clickRow(root: HTMLElement, key: string): boolean {
  const row = root.querySelector(`[data-key="${key}"]`);
  if (row === null) {
    return false;
  }
  row.dispatchEvent(new Event("click", { bubbles: true }));
  return true;
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

// isCursored reports whether the list row under key is the cursor's row.
function isCursored(root: HTMLElement, key: string): boolean {
  return root.querySelector(`[data-key="${key}"]`)?.getAttribute("aria-current") === "true";
}

// errorTexts reads the page's error lines.
function errorTexts(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".error")].map((element) => element.textContent ?? "");
}

// lineDrawn reports whether the list draws the line under key.
function lineDrawn(root: HTMLElement, key: string): boolean {
  return root.querySelector(`[data-key="${key}"]`) !== null;
}

// epicKeyOf is the list key of an epic's line.
function epicKeyOf(epic: Epic): string {
  return `epic:${epic.id}`;
}

// pageCrumb reads the current crumb of the epic page: its slug, or the page it has moved to. Empty when no page is drawn.
function pageCrumb(root: HTMLElement): string {
  return root.querySelector('[aria-label="epic"] .crumb.here')?.textContent ?? "";
}

// pageButtons reads the labels of the buttons the epic page draws.
function pageButtons(root: HTMLElement): string[] {
  return [...root.querySelectorAll('[aria-label="epic"] button')].map((element) => element.textContent ?? "");
}

// withPage starts a page over a fake server in a fresh DOM, waits until its list, its cursor's pane stream and its
// notice stream are up, runs check, and stops the page. The pane stream opens from an effect declared after the
// cursor's own effect, so once it is open the cursor has settled and a click lands on the cursor the page holds.
// scrolled holds the class of each element a scrollIntoView ran on, in the order the calls ran. step is the wait
// between the checks; a test whose timers are mocked passes settle, since the default wait is a timer.
async function withPage(
  signal: AbortSignal,
  check: (root: HTMLElement, server: FakeServer, scrolled: string[]) => Promise<void>,
  fixtures: Fixtures = {},
  step?: () => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const scrolled: string[] = [];
  window.HTMLElement.prototype.scrollIntoView = function scrollIntoView(this: { className: string }) {
    scrolled.push(this.className);
  };
  const root = document.createElement("div");
  const server = fakeServer(fixtures);
  const stop = start(root, clientsFor(server), longPollMs, retryMs);
  // The page is stopped and the DOM removed when the test ends, or as soon as signal aborts if it times out first, so
  // the page never runs on into the next test (see teardownOnce).
  const teardown = teardownOnce(signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    if (step === undefined) {
      await until(signal, () => isCursored(root, apiRow), "the api row under the cursor");
      await until(signal, () => server.paneStreams === 1, "the api pane's stream");
    } else {
      // The cursor and the pane stream are opened from effects, which a mocked timer holds back, so a test with mocked
      // timers waits for the list to draw and for the notice stream alone.
      await until(signal, () => lineDrawn(root, apiRow), "the api row in the list", { step });
    }
    await until(signal, () => server.notices !== null, "the notice stream", { step });
    await check(root, server, scrolled);
  } finally {
    await teardown();
  }
}

test("NoticeStack: a notice from the stream shows in the page's stack", (t) =>
  withPage(t.signal, async (root, server) => {
    assert.deepEqual(stackRows(root), []);
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
  }));

test("NoticeStack: an ended notice leaves the stack", (t) =>
  withPage(t.signal, async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    sendNotice(server, endedMessage({ pane: "%2" }));
    await until(t.signal, () => stackRows(root).length === 0, "the stack to empty");
  }));

test("NoticeStack: Open on a pane notice sends Seen with its key and moves the cursor to that pane's row", (t) =>
  withPage(t.signal, async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    assert.equal(clickRowControl(root, "web needs you"), true, "no Open control for the notice");
    await until(t.signal, () => server.seen.length === 1, "the Seen report");
    assert.equal(server.seen[0].pane, "%2");
    await until(t.signal, () => isCursored(root, webRow), "the cursor on the web row");
    assert.equal(isCursored(root, apiRow), false, "the cursor is still on the api row");
  }));

test("NoticeStack: Answer on a waiting pane notice sends Seen with its key and moves the cursor to that pane's row", (t) =>
  withPage(t.signal, async (root, server) => {
    sendNotice(server, {
      shown: { key: { pane: "%2" }, state: "waiting", title: "web needs you", body: "web needs you body" },
    });
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    assert.equal(
      rowOf(root, "web needs you")?.querySelector(".acts > button:not(.quiet)")?.textContent,
      "Answer",
      "the waiting pane notice has no Answer",
    );
    assert.equal(clickRowControl(root, "web needs you"), true, "no Answer control for the notice");
    await until(t.signal, () => server.seen.length === 1, "the Seen report");
    assert.equal(server.seen[0].pane, "%2");
    await until(t.signal, () => isCursored(root, webRow), "the cursor on the web row");
    assert.equal(isCursored(root, apiRow), false, "the cursor is still on the api row");
  }));

test("NoticeStack: a limit notice has no open control, and its Mark read sends Seen and moves nothing", (t) =>
  withPage(t.signal, async (root, server) => {
    assert.equal(clickRow(root, webRow), true, "no row for the web pane");
    await until(t.signal, () => isCursored(root, webRow), "the cursor on the web row");
    sendNotice(server, shownMessage({ limit: limitKey }, "limit reached"));
    await until(t.signal, () => rowOf(root, "limit reached") !== undefined, "the limit notice in the stack");
    assert.deepEqual(
      [...(rowOf(root, "limit reached")?.querySelectorAll("button") ?? [])].map((button) => button.textContent),
      ["Mark read"],
      "the limit notice has an open control",
    );
    assert.equal(clickRowControl(root, "limit reached", true), true, "no Mark read for the notice");
    await until(t.signal, () => server.seen.length === 1, "the Seen report");
    assert.equal(server.seen[0].limit, limitKey);
    assert.equal(isCursored(root, webRow), true, "the cursor left the web row");
    assert.equal(isCursored(root, apiRow), false, "the cursor moved to the api row");
  }));

test("NoticeStack: a failed Seen is dropped without a page error, and the click still moves the cursor", (t) =>
  withPage(t.signal, async (root, server) => {
    server.seenFails = true;
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    // The load-time List and the one the shown notice starts land before the click, so the list after it is the failed
    // report's alone.
    await until(t.signal, () => server.lists >= 2, "the notice log listed for the shown notice");
    const listsBefore = server.lists;
    assert.equal(clickRowControl(root, "web needs you"), true, "no Open control for the notice");
    await until(t.signal, () => server.seen.length === 1, "the Seen report");
    await until(t.signal, () => isCursored(root, webRow), "the cursor on the web row");
    // The failed report's handling relists the log, so a list after the click means the failure was handled.
    await until(t.signal, () => server.lists > listsBefore, "the notice log listed after the failed report");
    assert.deepEqual(errorTexts(root), []);
  }));

test("NoticeBell: a notice from the Watch stream lists the log again", (t) =>
  withPage(t.signal, async (root, server) => {
    await until(t.signal, () => server.lists >= 1, "the load-time List");
    const listsBefore = server.lists;
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => server.lists > listsBefore, "the notice log listed again");
  }));

test("NoticeBell: an ended notice from the Watch stream lists the log again", (t) =>
  withPage(t.signal, async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    // The load-time List and the one the shown notice starts are the two lists before the end.
    await until(t.signal, () => server.lists >= 2, "the notice log listed for the shown notice");
    const listsBefore = server.lists;
    sendNotice(server, endedMessage({ pane: "%2" }));
    await until(t.signal, () => server.lists > listsBefore, "the notice log listed again");
  }));

test("NoticeStack: the stack is drawn inside .panes, the reading area it sits over", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withPage(
    t.signal,
    async (root, server) => {
      sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
      await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack", {
        step: settle,
      });
      const panes = root.querySelector(".panes");
      assert.ok(panes, "the page draws no .panes");
      assert.ok(panes.querySelector(".notices"), "the notice stack is not a descendant of .panes");
    },
    {},
    settle,
  );
});

test("NoticeStack: Mark read takes its row off at once, sends Seen for its key, and the other notices stay", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withPage(
    t.signal,
    async (root, server) => {
      sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
      sendNotice(server, shownMessage({ limit: limitKey }, "limit reached"));
      await until(t.signal, () => stackRows(root).length === 2, "both notices in the stack", { step: settle });
      await act(() => {
        clickRowControl(root, "web needs you", true);
      });
      assert.equal(rowOf(root, "web needs you"), undefined, "the row stayed after its Mark read");
      assert.notEqual(rowOf(root, "limit reached"), undefined, "the other notice left the stack");
      await until(t.signal, () => server.seen.length === 1, "the Seen report", { step: settle });
      assert.equal(server.seen[0].pane, "%2", "the Seen report names another notice");
    },
    {},
    settle,
  );
});

test("NoticeStack: Mark read lists the notice log again once its Seen lands", (t) =>
  withPage(t.signal, async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack");
    const listsBefore = server.lists;
    await act(() => {
      clickRowControl(root, "web needs you", true);
    });
    await until(t.signal, () => server.seen.length === 1, "the Seen report");
    await until(t.signal, () => server.lists > listsBefore, "the notice log listed again");
  }));

test("NoticeStack: a row's age is the age of the log's newest row under its key", (t) => {
  mockTimers(t, ["setTimeout", "Date"]);
  t.mock.timers.setTime(clockSeconds * 1000);
  return withPage(
    t.signal,
    async (root, server) => {
      sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
      sendNotice(server, shownMessage({ pane: "%1" }, "api needs you"));
      await until(
        t.signal,
        () => rowOf(root, "web needs you") !== undefined && rowOf(root, "api needs you") !== undefined,
        "both notices in the stack",
        { step: settle },
      );
      await settleTurns();
      assert.equal(rowOf(root, "web needs you")?.querySelector(".when")?.textContent, "3h", "the age from the log's newest row");
      assert.equal(rowOf(root, "api needs you")?.querySelector(".when")?.textContent, "", "a notice with no log row has an age");
    },
    {
      // Notices.List answers newest first, so the 3h row leads the older 10h row under the same key.
      listed: [
        listedMessage({ pane: "%2" }, "web needs you", clockSeconds - 3 * 3600),
        listedMessage({ pane: "%2" }, "web needs you", clockSeconds - 10 * 3600),
      ],
    },
    settle,
  );
});

test("NoticeStack: a notice leaves the stack at 8 seconds after it appears, and no Seen is sent for it", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withPage(
    t.signal,
    async (root, server) => {
      sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
      await until(t.signal, () => rowOf(root, "web needs you") !== undefined, "the notice in the stack", {
        step: settle,
      });
      await advance(t, 7999);
      assert.notEqual(rowOf(root, "web needs you"), undefined, "the notice left the stack before 8000 ms");
      await advance(t, 1);
      assert.equal(rowOf(root, "web needs you"), undefined, "the notice was still on the stack at 8000 ms");
      // A focus lists the log, a request sent after the timeout, so a Seen the timeout sent would have reached the
      // server before it. The list's arrival is the signal the wait ends on.
      const listsBefore = server.lists;
      window.dispatchEvent(new Event("focus"));
      await until(t.signal, () => server.lists > listsBefore, "the notice log listed after the timeout", { step: settle });
      await settleTurns();
      assert.deepEqual(server.seen, [], "a Seen was sent for the timed-out notice");
    },
    {},
    settle,
  );
});

const widgetsRepo = "widgets-5d0c1a7";
// endedAt is when the ended epic released, in unix seconds. Only its order against the other epics matters to the page.
const endedAt = 99280n;

// makeEpic is a synthetic epic whose id is its repository and slug joined, as the record names it.
function makeEpic(slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${widgetsRepo}/${slug}`,
    repository: widgetsRepo,
    slug,
    outcome: "",
    live: true,
    ...init,
  });
}

// asking is live with an open question, so its now block draws the question's option cards.
const asking = makeEpic("first");
// stuck is live and parked on a failed release step, so its now block draws Retry and Stop….
const stuck = makeEpic("second");
// shipped is an orreryd epic released and seen, so the ended earlier line holds it. Its detail carries a report.
const shipped = makeEpic("third", {
  source: "orreryd",
  live: false,
  outcome: "released",
  phaseAt: endedAt,
  seen: endedAt,
});
// retired is a record epic that went quiet, so the ended earlier line holds it.
const retired = makeEpic("fourth", { source: "record", live: false });

const landingFixtures: Fixtures = {
  epics: [asking, stuck, shipped, retired],
  details: [
    create(EpicsGetResponseSchema, {
      epic: asking,
      questions: [{ id: "Q1", text: "Which way?" }],
      contract: { title: "Ship the widget" },
    }),
    create(EpicsGetResponseSchema, {
      epic: stuck,
      parked: "release stopped",
      releaseSteps: [{ position: 2n, step: "publish", state: "failed", reason: "registry 503" }],
    }),
    create(EpicsGetResponseSchema, {
      epic: shipped,
      report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "every requirement is green" },
    }),
    create(EpicsGetResponseSchema, { epic: retired }),
  ],
};

// clickEpicNotice waits until the list draws asking, which proves the epics loaded, then sends a notice for epic that
// opens block and clicks it. It waits until the Seen report lands, so the click has run.
async function clickEpicNotice(
  signal: AbortSignal,
  root: HTMLElement,
  server: FakeServer,
  epic: Epic,
  block?: Record<string, string>,
): Promise<void> {
  await until(signal, () => lineDrawn(root, epicKeyOf(asking)), "the epics the list holds");
  const title = `${epic.slug} needs you`;
  sendNotice(server, shownMessage({ epic: epic.id }, title, block));
  await until(signal, () => rowOf(root, title) !== undefined, "the notice in the stack");
  assert.equal(clickRowControl(root, title), true, "no control for the notice");
  await until(signal, () => server.seen.length === 1, "the Seen report");
}

test("NoticeStack: a question notice opens its epic at its Now section, with the question's Ask in the page", (t) =>
  withPage(
    t.signal,
    async (root, server, scrolled) => {
      await clickEpicNotice(t.signal, root, server, asking, { kind: "question", ref: "Q1" });
      await until(t.signal, () => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["now"]);
      assert.equal(pageCrumb(root), asking.slug);
      assert.equal(isCursored(root, epicKeyOf(asking)), true, "the cursor is not on the epic");
      assert.equal(
        root.querySelector('[aria-label="epic"] section.now .ask .opts > .opt') !== null,
        true,
        "no Ask in the epic page",
      );
      assert.equal(server.seen[0].epic, asking.id);
    },
    landingFixtures,
  ));

test("NoticeStack: a release-step notice opens its epic at its Now section, where Retry and Stop… are", (t) =>
  withPage(
    t.signal,
    async (root, server, scrolled) => {
      await clickEpicNotice(t.signal, root, server, stuck, { kind: "release_step", ref: "2" });
      await until(t.signal, () => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["now"]);
      assert.equal(pageCrumb(root), stuck.slug);
      assert.equal(
        root.querySelector('[aria-label="epic"] section.now .ask .opts > .opt .nm')?.textContent,
        "Retry",
        "no Retry in the page",
      );
      assert.equal(pageButtons(root).includes("Stop…"), true, "no Stop… in the page");
    },
    landingFixtures,
  ));

test("EpicReport: NoticeStack: a report notice for an epic under ended earlier opens the line and brings its Report into view", (t) =>
  withPage(
    t.signal,
    async (root, server, scrolled) => {
      await clickEpicNotice(t.signal, root, server, shipped, { kind: "report", ref: "" });
      await until(t.signal, () => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["report"]);
      assert.equal(pageCrumb(root), shipped.slug);
      assert.equal(isCursored(root, epicKeyOf(shipped)), true, "the cursor is not on the epic");
    },
    landingFixtures,
  ));

test("NoticeStack: a contract notice opens the contract page of its epic, without a scroll into view", (t) =>
  withPage(t.signal, async (root, server, scrolled) => {
    await clickEpicNotice(t.signal, root, server, asking, { kind: "contract", ref: "" });
    await until(t.signal, () => pageCrumb(root) === "contract", "the contract page");
    assert.deepEqual(scrolled, []);
  }, landingFixtures));

test("NoticeStack: a notice with no block opens its ended earlier epic at the top of the epic view", (t) =>
  withPage(
    t.signal,
    async (root, server, scrolled) => {
      await clickEpicNotice(t.signal, root, server, retired);
      await until(t.signal, () => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["epic-page"]);
      assert.equal(pageCrumb(root), retired.slug);
      assert.equal(isCursored(root, epicKeyOf(retired)), true, "the cursor is not on the epic");
    },
    landingFixtures,
  ));

test("NoticeStack: a notice for an epic the list does not hold moves the cursor and the page nowhere", (t) =>
  withPage(
    t.signal,
    async (root, server, scrolled) => {
      await until(t.signal, () => lineDrawn(root, epicKeyOf(asking)), "the epics the list holds");
      assert.equal(clickRow(root, epicKeyOf(asking)), true, "no line for the epic");
      await until(t.signal, () => isCursored(root, epicKeyOf(asking)), "the cursor on the epic");
      const title = "gone needs you";
      sendNotice(server, shownMessage({ epic: `${widgetsRepo}/gone` }, title, { kind: "question", ref: "Q1" }));
      await until(t.signal, () => rowOf(root, title) !== undefined, "the notice in the stack");
      assert.equal(clickRowControl(root, title), true, "no control for the notice");
      await until(t.signal, () => server.seen.length === 1, "the Seen report");
      sendNotice(server, shownMessage({ limit: limitKey }, "limit reached"));
      await until(t.signal, () => rowOf(root, "limit reached") !== undefined, "a later notice, drawn after the click");
      assert.equal(isCursored(root, epicKeyOf(asking)), true, "the cursor left the epic");
      assert.deepEqual(scrolled, []);
    },
    landingFixtures,
  ));
