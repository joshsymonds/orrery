import assert from "node:assert/strict";
import { test } from "node:test";

import { createClients, createTransport } from "./client";
import { start } from "./main";
import { installTestDom, removeTestDom } from "./testdom";

const panesListPath = "/orrery.v1.Panes/List";
const sessionsListPath = "/orrery.v1.Sessions/List";
const epicsListPath = "/orrery.v1.Epics/List";
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

function fakeServer(): FakeServer {
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
        return json({ epics: [] });
      }
      if (path === noticesWatchPath) {
        const stream = new ReadableStream<Uint8Array>({
          start: (controller) => {
            server.notices = controller;
          },
        });
        request.signal.addEventListener(
          "abort",
          () => {
            server.notices?.error(request.signal.reason);
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

// shownMessage is the Notices.Watch change that shows a notice under key.
function shownMessage(key: Record<string, string>, title: string): object {
  return { shown: { key, state: "done", title, body: `${title} body` } };
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

async function until(check: () => boolean, what: string): Promise<void> {
  const deadline = Date.now() + 2000;
  while (!check()) {
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await sleep(2);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// withPage starts a page over a fake server in a fresh DOM, waits until its list, its cursor's pane stream and its
// notice stream are up, runs check, and stops the page. The pane stream opens from an effect declared after the
// cursor's own effect, so once it is open the cursor has settled and a click lands on the cursor the page holds.
async function withPage(check: (root: HTMLElement, server: FakeServer) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const root = document.createElement("div");
  const server = fakeServer();
  const stop = start(root, clientsFor(server), longPollMs, retryMs);
  try {
    await until(() => isCursored(root, apiRow), "the api row under the cursor");
    await until(() => server.paneStreams === 1, "the api pane's stream");
    await until(() => server.notices !== null, "the notice stream");
    await check(root, server);
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
