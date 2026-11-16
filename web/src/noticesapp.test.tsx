import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";

import { createClients, createTransport } from "./client";
import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  EpicsListResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { start } from "./main";
import { until } from "./testwait";
import { installTestDom, removeTestDom } from "./testdom";

const panesListPath = "/orrery.v1.Panes/List";
const sessionsListPath = "/orrery.v1.Sessions/List";
const epicsListPath = "/orrery.v1.Epics/List";
const epicsGetPath = "/orrery.v1.Epics/Get";
const panesWatchPath = "/orrery.v1.Panes/WatchConversation";
const noticesWatchPath = "/orrery.v1.Notices/Watch";
const noticesSeenPath = "/orrery.v1.Notices/Seen";
const apiRow = "pane:box:%1";
const webRow = "pane:box:%2";
const limitKey = "five-hour@1800000000";
// A poll interval far longer than any test waits, so the rows load once, when the page starts.
const longPollMs = 60_000;
// The pause before a stream that failed unavailable reopens, short so the tests' waits stay well inside their deadline.
const retryMs = 5;
// How long a test waits for a failed report's answer to reach the page before it looks for an error line.
const settleMs = 20;

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
}

// FakeServer answers the page's lists from the fixtures above and holds the Notices.Watch stream open for a test to
// send changes into. It records the key of each Notices.Seen the page sent, as the JSON body named it, and counts the
// pane streams the page opened.
interface FakeServer {
  fetch: typeof globalThis.fetch;
  // The controller of the open Notices.Watch stream, or null before the page opens it and after it is aborted.
  notices: ReadableStreamDefaultController<Uint8Array> | null;
  seen: Record<string, string>[];
  paneStreams: number;
  // While set, Notices.Seen answers with a server error.
  seenFails: boolean;
}

function fakeServer({ epics = [], details = [] }: Fixtures = {}): FakeServer {
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

// bannerTitles reads the titles of the banner's notices, in the order the banner shows them.
function bannerTitles(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".notice-title")].map((element) => element.textContent ?? "");
}

// clickBanner clicks the banner button of the notice titled title. It reports whether such a button was drawn.
function clickBanner(root: HTMLElement, title: string): boolean {
  const button = [...root.querySelectorAll(".notice")].find(
    (element) => element.querySelector(".notice-title")?.textContent === title,
  );
  if (button === undefined) {
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

// isCursored reports whether the list row under key is the cursor's row.
function isCursored(root: HTMLElement, key: string): boolean {
  return root.querySelector(`[data-key="${key}"]`)?.getAttribute("aria-current") === "true";
}

// errorTexts reads the page's error lines.
function errorTexts(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".error")].map((element) => element.textContent ?? "");
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
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
// scrolled holds the class of each element a scrollIntoView ran on, in the order the calls ran.
async function withPage(
  check: (root: HTMLElement, server: FakeServer, scrolled: string[]) => Promise<void>,
  fixtures: Fixtures = {},
): Promise<void> {
  const window = installTestDom();
  const scrolled: string[] = [];
  window.HTMLElement.prototype.scrollIntoView = function scrollIntoView(this: { className: string }) {
    scrolled.push(this.className);
  };
  const root = document.createElement("div");
  const server = fakeServer(fixtures);
  const stop = start(root, clientsFor(server), longPollMs, retryMs);
  try {
    await until(() => isCursored(root, apiRow), "the api row under the cursor");
    await until(() => server.paneStreams === 1, "the api pane's stream");
    await until(() => server.notices !== null, "the notice stream");
    await check(root, server, scrolled);
  } finally {
    stop();
    await removeTestDom(window);
  }
}

test("a notice from the stream shows in the page's banner", () =>
  withPage(async (root, server) => {
    assert.deepEqual(bannerTitles(root), []);
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(() => bannerTitles(root).includes("web needs you"), "the notice in the banner");
  }));

test("an ended notice leaves the banner", () =>
  withPage(async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(() => bannerTitles(root).includes("web needs you"), "the notice in the banner");
    sendNotice(server, endedMessage({ pane: "%2" }));
    await until(() => bannerTitles(root).length === 0, "the banner to empty");
  }));

test("a click on a pane notice sends Seen with its key and moves the cursor to that pane's row", () =>
  withPage(async (root, server) => {
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(() => bannerTitles(root).includes("web needs you"), "the notice in the banner");
    assert.equal(clickBanner(root, "web needs you"), true, "no banner button for the notice");
    await until(() => server.seen.length === 1, "the Seen report");
    assert.equal(server.seen[0].pane, "%2");
    await until(() => isCursored(root, webRow), "the cursor on the web row");
    assert.equal(isCursored(root, apiRow), false, "the cursor is still on the api row");
  }));

test("a click on a limit notice sends Seen and moves nothing", () =>
  withPage(async (root, server) => {
    assert.equal(clickRow(root, webRow), true, "no row for the web pane");
    await until(() => isCursored(root, webRow), "the cursor on the web row");
    sendNotice(server, shownMessage({ limit: limitKey }, "limit reached"));
    await until(() => bannerTitles(root).includes("limit reached"), "the limit notice in the banner");
    assert.equal(clickBanner(root, "limit reached"), true, "no banner button for the notice");
    await until(() => server.seen.length === 1, "the Seen report");
    assert.equal(server.seen[0].limit, limitKey);
    assert.equal(isCursored(root, webRow), true, "the cursor left the web row");
    assert.equal(isCursored(root, apiRow), false, "the cursor moved to the api row");
  }));

test("a failed Seen is dropped without a page error, and the click still moves the cursor", () =>
  withPage(async (root, server) => {
    server.seenFails = true;
    sendNotice(server, shownMessage({ pane: "%2" }, "web needs you"));
    await until(() => bannerTitles(root).includes("web needs you"), "the notice in the banner");
    assert.equal(clickBanner(root, "web needs you"), true, "no banner button for the notice");
    await until(() => server.seen.length === 1, "the Seen report");
    await until(() => isCursored(root, webRow), "the cursor on the web row");
    await sleep(settleMs);
    assert.deepEqual(errorTexts(root), []);
  }));

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

// asking is live with an open question, so its now block draws the answer form.
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
// retired is a record epic that went quiet, so the folded line holds it.
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
  root: HTMLElement,
  server: FakeServer,
  epic: Epic,
  block?: Record<string, string>,
): Promise<void> {
  await until(() => lineDrawn(root, epicKeyOf(asking)), "the epics the list holds");
  const title = `${epic.slug} needs you`;
  sendNotice(server, shownMessage({ epic: epic.id }, title, block));
  await until(() => bannerTitles(root).includes(title), "the notice in the banner");
  assert.equal(clickBanner(root, title), true, "no banner button for the notice");
  await until(() => server.seen.length === 1, "the Seen report");
}

test("a question notice opens its epic at the now block, with the answer form in the page", () =>
  withPage(
    async (root, server, scrolled) => {
      await clickEpicNotice(root, server, asking, { kind: "question", ref: "Q1" });
      await until(() => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["now-block"]);
      assert.equal(pageCrumb(root), asking.slug);
      assert.equal(isCursored(root, epicKeyOf(asking)), true, "the cursor is not on the epic");
      assert.equal(
        root.querySelector('[aria-label="epic"] textarea[aria-label="answer"]') !== null,
        true,
        "no answer form in the epic page",
      );
      assert.equal(server.seen[0].epic, asking.id);
    },
    landingFixtures,
  ));

test("a release-step notice opens its epic at the now block, where Retry and Stop… are", () =>
  withPage(
    async (root, server, scrolled) => {
      await clickEpicNotice(root, server, stuck, { kind: "release_step", ref: "2" });
      await until(() => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["now-block"]);
      assert.equal(pageCrumb(root), stuck.slug);
      assert.equal(pageButtons(root).includes("Retry"), true, "no Retry in the page");
    },
    landingFixtures,
  ));

test("a report notice for an epic folded under ended earlier opens the line and brings the report into view", () =>
  withPage(
    async (root, server, scrolled) => {
      await clickEpicNotice(root, server, shipped, { kind: "report", ref: "" });
      await until(() => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["proof report"]);
      assert.equal(pageCrumb(root), shipped.slug);
      assert.equal(isCursored(root, epicKeyOf(shipped)), true, "the cursor is not on the epic");
    },
    landingFixtures,
  ));

test("a contract notice opens the contract page of its epic, without a scroll into view", () =>
  withPage(async (root, server, scrolled) => {
    await clickEpicNotice(root, server, asking, { kind: "contract", ref: "" });
    await until(() => pageCrumb(root) === "contract", "the contract page");
    assert.deepEqual(scrolled, []);
  }, landingFixtures));

test("a notice with no block opens its folded epic at the top of the epic view", () =>
  withPage(
    async (root, server, scrolled) => {
      await clickEpicNotice(root, server, retired);
      await until(() => scrolled.length === 1, "the scroll into view");
      assert.deepEqual(scrolled, ["epic-page"]);
      assert.equal(pageCrumb(root), retired.slug);
      assert.equal(isCursored(root, epicKeyOf(retired)), true, "the cursor is not on the epic");
    },
    landingFixtures,
  ));

test("a notice for an epic the list does not hold moves the cursor and the page nowhere", () =>
  withPage(
    async (root, server, scrolled) => {
      await until(() => lineDrawn(root, epicKeyOf(asking)), "the epics the list holds");
      assert.equal(clickRow(root, epicKeyOf(asking)), true, "no line for the epic");
      await until(() => isCursored(root, epicKeyOf(asking)), "the cursor on the epic");
      const title = "gone needs you";
      sendNotice(server, shownMessage({ epic: `${widgetsRepo}/gone` }, title, { kind: "question", ref: "Q1" }));
      await until(() => bannerTitles(root).includes(title), "the notice in the banner");
      assert.equal(clickBanner(root, title), true, "no banner button for the notice");
      await until(() => server.seen.length === 1, "the Seen report");
      sendNotice(server, shownMessage({ limit: limitKey }, "limit reached"));
      await until(() => bannerTitles(root).includes("limit reached"), "a later notice, drawn after the click");
      assert.equal(isCursored(root, epicKeyOf(asking)), true, "the cursor left the epic");
      assert.deepEqual(scrolled, []);
    },
    landingFixtures,
  ));
