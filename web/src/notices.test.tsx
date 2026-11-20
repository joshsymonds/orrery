import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test, type TestContext } from "node:test";
import { fileURLToPath } from "node:url";
import v8 from "node:v8";
import vm from "node:vm";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import {
  type InAppNotice,
  InAppNoticeSchema,
  type NoticeKey,
  NoticeKeySchema,
  type NoticesWatchResponse,
  NoticesWatchResponseSchema,
} from "./gen/orrery/v1/notice_pb";
import { applyNotice, Notice, NoticeBanner, noticeLifetimeMillis, watchNotices } from "./notices";
import type { NoticeAct, NoticeHead } from "./noticewords";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom } from "./testdom";
import { settledFlag, until } from "./testwait";

const watchPath = "/orrery.v1.Notices/Watch";
const retryMs = 40;

function paneKey(pane: string): NoticeKey {
  return create(NoticeKeySchema, { pane });
}

function notice(pane: string, title: string): InAppNotice {
  return create(InAppNoticeSchema, { key: paneKey(pane), state: "done", title, body: `${title} body` });
}

function alertKey(alert: string): NoticeKey {
  return create(NoticeKeySchema, { alert });
}

function alertNotice(alert: string, title: string): InAppNotice {
  return create(InAppNoticeSchema, { key: alertKey(alert), state: "done", title, body: `${title} body` });
}

function shown(item: InAppNotice): NoticesWatchResponse {
  return create(NoticesWatchResponseSchema, { change: { case: "shown", value: item } });
}

function ended(key: NoticeKey): NoticesWatchResponse {
  return create(NoticesWatchResponseSchema, { change: { case: "ended", value: key } });
}

function titles(list: InAppNotice[]): string[] {
  return list.map((item) => item.title);
}

test("a shown notice joins the end of the list, in the order it was first shown", (t) => {
  const first = applyNotice([], shown(notice("%1", "one")));
  const second = applyNotice(first, shown(notice("%2", "two")));
  assert.deepEqual(titles(second), ["one", "two"]);
});

test("a shown notice under a key already showing replaces it in place and keeps the list it was given", (t) => {
  const before = [notice("%1", "one"), notice("%2", "two")];
  const after = applyNotice(before, shown(notice("%1", "one again")));
  assert.deepEqual(titles(after), ["one again", "two"]);
  assert.deepEqual(titles(before), ["one", "two"], "the list it was given changed");
});

test("keys that set different fields are different notices, even when the text matches", (t) => {
  const session = create(InAppNoticeSchema, {
    key: create(NoticeKeySchema, { session: "%1" }),
    state: "done",
    title: "session",
    body: "",
  });
  const after = applyNotice([notice("%1", "pane")], shown(session));
  assert.deepEqual(titles(after), ["pane", "session"]);
});

test("alert notices with different names are different notices", (t) => {
  const first = applyNotice([], shown(alertNotice("backup", "backup")));
  const second = applyNotice(first, shown(alertNotice("deploy", "deploy")));
  assert.deepEqual(titles(second), ["backup", "deploy"]);
});

test("an ended alert key removes only that alert's notice", (t) => {
  const before = [notice("%1", "one"), alertNotice("backup", "backup"), alertNotice("deploy", "deploy")];
  const after = applyNotice(before, ended(alertKey("backup")));
  assert.deepEqual(titles(after), ["one", "deploy"]);
});

test("an alert notice beside a pane notice with the same text stays separate", (t) => {
  const after = applyNotice([notice("%1", "same")], shown(alertNotice("same", "same")));
  assert.deepEqual(titles(after), ["same", "same"]);
});

test("an ended notice leaves the list and the others keep their order", (t) => {
  const before = [notice("%1", "one"), notice("%2", "two"), notice("%3", "three")];
  const after = applyNotice(before, ended(paneKey("%2")));
  assert.deepEqual(titles(after), ["one", "three"]);
});

test("an ended notice under a key that is not showing leaves the list as it was", (t) => {
  const before = [notice("%1", "one")];
  const after = applyNotice(before, ended(paneKey("%9")));
  assert.deepEqual(titles(after), ["one"]);
});

// FakeWatch answers Notices.Watch streams through the transport's fetch. Each stream is recorded, and the test decides
// what it sends and when it ends. Like fetch, aborting a call errors its body, so a reader stops. The abort is heard on
// the signal the transport passed, not on the Request built from it: a Request's signal follows its init's only while
// the Request lives, so once the garbage collector takes the Request its signal never aborts, and a reader of the body
// would wait forever.
class FakeWatch {
  readonly paths: string[] = [];
  readonly streams: (ReadableStreamDefaultController<Uint8Array> | null)[] = [];

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    this.paths.push(new URL(request.url).pathname);
    const index = this.streams.length;
    this.streams.push(null);
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.streams[index] = controller;
        const signal = init?.signal ?? request.signal;
        signal.addEventListener("abort", () => controller.error(signal.reason), { once: true });
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  emitShown(index: number, pane: string, title: string): void {
    this.open(index).enqueue(envelope(0, { shown: { key: { pane }, state: "done", title, body: `${title} body` } }));
  }

  end(index: number): void {
    this.finish(index, envelope(2, {}));
  }

  fail(index: number): void {
    this.finish(index, envelope(2, { error: { code: "unavailable", message: "stream failed" } }));
  }

  private open(index: number): ReadableStreamDefaultController<Uint8Array> {
    const controller = this.streams[index];
    if (controller === null) {
      throw new Error(`stream ${index} has ended`);
    }
    return controller;
  }

  private finish(index: number, last: Uint8Array): void {
    const controller = this.open(index);
    controller.enqueue(last);
    controller.close();
    this.streams[index] = null;
  }
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

function clientsFor(fake: FakeWatch): ReturnType<typeof createClients> {
  return createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fake.fetch }));
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

test("watchNotices calls Notices.Watch and delivers the list after each change", async (t) => {
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  try {
    await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
    assert.equal(fake.paths[0], watchPath);
    fake.emitShown(0, "%1", "one");
    await until(t.signal, () => lists.length === 1, "the first change", { ended });
    assert.deepEqual(lists[0], ["one"]);
    fake.emitShown(0, "%2", "two");
    await until(t.signal, () => lists.length === 2, "the second change", { ended });
    assert.deepEqual(lists[1], ["one", "two"]);
  } finally {
    controller.abort();
    await done;
  }
});

test("a stream that fails clears the list, then reopens after retryMs and delivers again", async (t) => {
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  try {
    await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
    fake.emitShown(0, "%1", "one");
    await until(t.signal, () => lists.length === 1, "the first change", { ended });
    fake.fail(0);
    await until(t.signal, () => lists.length === 2, "the clear after the failure", { ended });
    assert.deepEqual(lists[1], [], "the list was not cleared when the stream failed");
    await until(t.signal, () => fake.streams.length === 2, "the reopened stream", { ended });
    assert.equal(fake.paths[1], watchPath);
    fake.emitShown(1, "%2", "two");
    await until(t.signal, () => lists.length === 3, "the change on the reopened stream", { ended });
    assert.deepEqual(lists[2], ["two"]);
  } finally {
    controller.abort();
    await done;
  }
});

test("a stream that ends cleanly clears the list and reopens", async (t) => {
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  try {
    await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
    fake.emitShown(0, "%1", "one");
    await until(t.signal, () => lists.length === 1, "the first change", { ended });
    fake.end(0);
    await until(t.signal, () => lists.length === 2, "the clear after the end", { ended });
    assert.deepEqual(lists[1], [], "the list was not cleared when the stream ended");
    await until(t.signal, () => fake.streams.length === 2, "the reopened stream", { ended });
  } finally {
    controller.abort();
    await done;
  }
});

test("aborting an open stream ends watchNotices and nothing reaches onChange after it", async (t) => {
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
  fake.emitShown(0, "%1", "one");
  await until(t.signal, () => lists.length === 1, "the first change", { ended });
  controller.abort();
  await done;
  await sleep(retryMs * 3);
  assert.equal(fake.streams.length, 1, "a stream opened after the abort");
  assert.equal(lists.length, 1, "onChange ran after the abort");
});

// collectGarbage runs a full garbage collection, as V8 may at any moment between two awaits.
function collectGarbage(): void {
  v8.setFlagsFromString("--expose-gc");
  const gc = vm.runInNewContext("gc") as () => void;
  gc();
}

test("aborting an open stream ends watchNotices after the garbage collector has run", async (t) => {
  // The forced order of the hang: the stream is open and reading, the garbage collector runs, and only then does the
  // abort come. The Request the fetch built is unreachable by then, so an abort heard only on its signal would never
  // reach the body, and watchNotices would wait on the body forever.
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
  fake.emitShown(0, "%1", "one");
  await until(t.signal, () => lists.length === 1, "the first change", { ended });
  collectGarbage();
  controller.abort();
  await done;
  assert.equal(lists.length, 1, "onChange ran after the abort");
});

test("aborting while a failed stream waits to reopen stops the reopen", async (t) => {
  const fake = new FakeWatch();
  const controller = new AbortController();
  const lists: string[][] = [];
  const done = watchNotices(clientsFor(fake), (list) => lists.push(titles(list)), retryMs, controller.signal);
  const ended = settledFlag(done);
  await until(t.signal, () => fake.streams.length === 1, "the first stream", { ended });
  fake.fail(0);
  await until(t.signal, () => lists.length === 1, "the clear after the failure", { ended });
  controller.abort();
  await done;
  await sleep(retryMs * 3);
  assert.equal(fake.streams.length, 1, "the stream reopened after the abort");
  assert.equal(lists.length, 1, "onChange ran after the abort");
});

test("a banner shows each notice's title and body as one button, and a click opens that notice", async (t) => {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const opened: InAppNotice[] = [];
  try {
    await act(() => {
      render(
        <NoticeBanner
          notices={[notice("%1", "one"), notice("%2", "two")]}
          onOpen={(item) => opened.push(item)}
          onDismiss={() => undefined}
        />,
        container,
      );
    });
    const texts = buttonTexts(container);
    assert.equal(texts.length, 2, "a button per notice");
    assert.ok(texts[0].includes("one") && texts[0].includes("one body"), "the first button lacks its title or body");
    assert.ok(texts[1].includes("two") && texts[1].includes("two body"), "the second button lacks its title or body");

    await act(() => {
      clickButton(container, 1);
    });
    assert.equal(opened.length, 1, "the click did not open exactly one notice");
    assert.equal(opened[0].key?.pane, "%2", "the click opened a different notice");
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
});

// A row's open button is its .notice; its × is a separate button, so the open buttons are the ones read here.
function buttonTexts(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("button.notice"), (button) => button.textContent ?? "");
}

function clickButton(container: HTMLElement, index: number): void {
  const button = container.querySelectorAll("button.notice")[index];
  assert.ok(button !== undefined, `no button at index ${index}`);
  button.dispatchEvent(new Event("click", { bubbles: true }));
}

interface BannerHandlers {
  onOpen?: (notice: InAppNotice) => void;
  onDismiss?: (notice: InAppNotice) => void;
}

// drawBanner renders the banner of notices into container, in the current act.
function drawBanner(container: HTMLElement, notices: InAppNotice[], handlers: BannerHandlers = {}): void {
  render(
    <NoticeBanner
      notices={notices}
      onOpen={handlers.onOpen ?? (() => undefined)}
      onDismiss={handlers.onDismiss ?? (() => undefined)}
    />,
    container,
  );
}

// withContainer runs check in a fresh DOM, and removes the DOM when check ends.
async function withContainer(check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// rowTitles reads the titles of the rows the banner draws, in order.
function rowTitles(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".notice-row .notice-title")].map((element) => element.textContent ?? "");
}

// moreLine reads the "+n more" line the banner draws, or an empty string when it draws none.
function moreLine(container: HTMLElement): string {
  return container.querySelector(".notices-more")?.textContent ?? "";
}

// advance moves the mocked clock forward by ms and flushes the renders its timers cause.
async function advance(t: TestContext, ms: number): Promise<void> {
  await act(() => {
    t.mock.timers.tick(ms);
  });
}

test("a row holds its title and its body as separate elements, with a separator between them", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withContainer(async (container) => {
    await act(() => {
      drawBanner(container, [notice("%1", "one")]);
    });
    const open = container.querySelector(".notice-row > button.notice");
    assert.ok(open, "the row has no open button");
    assert.deepEqual(
      [...open.children].map((child) => child.className),
      ["notice-title", "notice-separator", "notice-body"],
      "the title, the separator and the body are not three elements in that order",
    );
    assert.equal(open.querySelector(".notice-title")?.textContent, "one");
    assert.equal(open.querySelector(".notice-body")?.textContent, "one body");
  });
});

test("the banner draws at most three rows, then a +n more line for the rest", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withContainer(async (container) => {
    const five = ["one", "two", "three", "four", "five"].map((title, index) => notice(`%${index + 1}`, title));
    await act(() => {
      drawBanner(container, five);
    });
    assert.deepEqual(rowTitles(container), ["one", "two", "three"], "the rows are not the first three notices");
    assert.equal(moreLine(container), "+2 more");

    await act(() => {
      drawBanner(container, five.slice(0, 3));
    });
    assert.equal(moreLine(container), "", "a line for the rest drawn with none left over");
  });
});

test("a × drops its row at once and calls onDismiss with its notice, and the other rows stay", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const dismissed: InAppNotice[] = [];
  const opened: InAppNotice[] = [];
  await withContainer(async (container) => {
    await act(() => {
      drawBanner(container, [notice("%1", "one"), notice("%2", "two")], {
        onDismiss: (item) => dismissed.push(item),
        onOpen: (item) => opened.push(item),
      });
    });
    const dismiss = container.querySelectorAll("button.notice-dismiss")[0];
    assert.ok(dismiss, "the first row has no ×");
    await act(() => {
      dismiss.dispatchEvent(new Event("click", { bubbles: true }));
    });
    assert.deepEqual(rowTitles(container), ["two"], "the × did not drop its row at once");
    assert.deepEqual(
      dismissed.map((item) => item.key?.pane),
      ["%1"],
      "onDismiss was not called once with the dismissed notice",
    );
    assert.deepEqual(opened, [], "the × opened its notice");
  });
});

test("a row leaves the stack 8 seconds after it first shows, and nothing is told of it", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const dismissed: InAppNotice[] = [];
  await withContainer(async (container) => {
    await act(() => {
      drawBanner(container, [notice("%1", "one")], { onDismiss: (item) => dismissed.push(item) });
    });
    await advance(t, noticeLifetimeMillis - 1);
    assert.deepEqual(rowTitles(container), ["one"], "the row left before 8 seconds");
    await advance(t, 1);
    assert.deepEqual(rowTitles(container), [], "the row was still drawn at 8 seconds");
    assert.deepEqual(dismissed, [], "onDismiss was called for the timed-out row");
  });
});

test("a fourth notice starts its own 8 seconds when it moves into the three rows", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withContainer(async (container) => {
    const four = ["one", "two", "three", "four"].map((title, index) => notice(`%${index + 1}`, title));
    await act(() => {
      drawBanner(container, four);
    });
    assert.equal(moreLine(container), "+1 more");
    await advance(t, noticeLifetimeMillis);
    assert.deepEqual(rowTitles(container), ["four"], "the fourth notice did not move into the rows");
    await advance(t, noticeLifetimeMillis - 1);
    assert.deepEqual(rowTitles(container), ["four"], "the fourth notice left before its own 8 seconds");
    await advance(t, 1);
    assert.deepEqual(rowTitles(container), [], "the fourth notice was still drawn at its own 8 seconds");
  });
});

test("a notice that leaves the list and is shown again under its key shows again", async (t) => {
  mockTimers(t, ["setTimeout"]);
  await withContainer(async (container) => {
    await act(() => {
      drawBanner(container, [notice("%1", "one")], { onDismiss: () => undefined });
    });
    const dismiss = container.querySelector("button.notice-dismiss");
    assert.ok(dismiss, "the row has no ×");
    await act(() => {
      dismiss.dispatchEvent(new Event("click", { bubbles: true }));
    });
    await act(() => {
      drawBanner(container, [notice("%1", "one, changed")]);
    });
    assert.deepEqual(rowTitles(container), [], "the notice came back while still on the list after its ×");
    await act(() => {
      drawBanner(container, []);
    });
    await act(() => {
      drawBanner(container, [notice("%1", "one")]);
    });
    assert.deepEqual(rowTitles(container), ["one"], "a notice shown again after it left the list did not show");
  });
});

test("unmounting clears the pending lifetime timer of each row", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const scheduled = t.mock.method(globalThis, "setTimeout");
  const cleared = t.mock.method(globalThis, "clearTimeout");
  await withContainer(async (container) => {
    await act(() => {
      drawBanner(container, [notice("%1", "one")]);
    });
    const lifetime = scheduled.mock.calls.find((call) => call.arguments[1] === noticeLifetimeMillis);
    assert.ok(lifetime, "a row's first draw schedules its lifetime timer");
    await act(() => {
      render(null, container);
    });
    assert.ok(
      cleared.mock.calls.some((call) => call.arguments[0] === lifetime.result),
      "unmounting does not clear the lifetime timer, so the row's timer outlives the banner",
    );
  });
});

// drawNotice renders one Notice into container, in the current act. Its props default as a stack row's do, with a
// two-minute age and a one-line body.
interface NoticeDraw {
  head: NoticeHead;
  when?: string;
  body?: string;
  act?: NoticeAct;
  onAct?: () => void;
  onRead?: () => void;
  flat?: boolean;
  unread?: boolean;
  onClick?: () => void;
}

function drawNotice(container: HTMLElement, draw: NoticeDraw): void {
  render(
    <Notice
      head={draw.head}
      when={draw.when ?? "2m"}
      body={draw.body ?? "a body"}
      act={draw.act}
      onAct={draw.onAct}
      onRead={draw.onRead}
      flat={draw.flat}
      unread={draw.unread}
      onClick={draw.onClick}
    />,
    container,
  );
}

// noticeRoot is the one notice container draws.
function noticeRoot(container: HTMLElement): HTMLElement {
  const root = container.querySelector<HTMLElement>(".notice");
  assert.ok(root, "no notice is drawn");
  return root;
}

// controlTexts reads the labels of the buttons inside root, in order.
function controlTexts(root: HTMLElement): string[] {
  return [...root.querySelectorAll("button")].map((button) => button.textContent ?? "");
}

// clickControl clicks the button of root labelled label.
function clickControl(root: HTMLElement, label: string): void {
  const button = [...root.querySelectorAll("button")].find((item) => item.textContent === label);
  assert.ok(button, `no ${label} button`);
  button.dispatchEvent(new Event("click", { bubbles: true }));
}

test("NoticeStack: an epic's notice is led by its gauge", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: {
          lead: { kind: "gauge", gauge: { done: 2, total: 5, tone: "live" } },
          dir: "",
          what: "rota-export asks you a question",
        },
      });
    });
    assert.equal(noticeRoot(container).firstElementChild?.getAttribute("class"), "gauge");
  });
});

test("NoticeStack: a pane's or session's notice is led by its state glyph, which names the state", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" },
      });
    });
    const lead = noticeRoot(container).firstElementChild;
    assert.equal(lead?.getAttribute("class"), "glyph", "the lead is not a state glyph");
    assert.equal(lead?.getAttribute("data-state"), "waiting", "the glyph does not draw the notice's state");
  });
});

test("NoticeStack: an alert's and a limit's lead is an icon, and the icon is the head's", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, { head: { lead: { kind: "icon", icon: "bell" }, dir: "", what: "a bell alert" } });
    });
    const bell = noticeRoot(container).firstElementChild;
    assert.equal(bell?.getAttribute("class"), "icon", "the lead is not an icon");
    const bellPath = bell?.querySelector("path")?.getAttribute("d");
    await act(() => {
      drawNotice(container, { head: { lead: { kind: "icon", icon: "clock" }, dir: "", what: "a limit" } });
    });
    const clockPath = noticeRoot(container).firstElementChild?.querySelector("path")?.getAttribute("d");
    assert.ok(bellPath && clockPath && bellPath !== clockPath, "the bell and the clock draw the same icon");
  });
});

test("NoticeStack: the directory is drawn in its own element, and the words follow it", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" },
      });
    });
    const title = noticeRoot(container).querySelector(".t");
    assert.equal(title?.querySelector(".dirn")?.textContent, "tidepool", "the directory is not in the place element");
    assert.equal(title?.textContent, "tidepool waiting on you", "the words do not follow the directory");
  });
});

test("NoticeStack: a head with no directory draws its words alone", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "icon", icon: "clock" }, dir: "", what: "halyard usage limit reached" },
      });
    });
    const title = noticeRoot(container).querySelector(".t");
    assert.equal(title?.querySelector(".dirn"), null, "an empty directory is drawn");
    assert.equal(title?.textContent, "halyard usage limit reached");
  });
});

test("NoticeStack: the second line is when, then the body's first line", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "icon", icon: "bell" }, dir: "", what: "a bell alert" },
        when: "3h",
        body: "first line\nsecond line",
      });
    });
    const root = noticeRoot(container);
    assert.equal(root.querySelector(".when")?.textContent, "3h", "the age is not drawn as when");
    assert.equal(root.querySelector(".b")?.textContent, "first line", "the body's first line is not the second line");
  });
});

test("NoticeStack: Answer is the primary control and a click calls onAct", async () => {
  await withContainer(async (container) => {
    let acted = 0;
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" },
        act: "Answer",
        onAct: () => (acted += 1),
      });
    });
    const root = noticeRoot(container);
    const answer = [...root.querySelectorAll("button")].find((button) => button.textContent === "Answer");
    assert.ok(answer?.classList.contains("primary"), "Answer is not the primary control");
    await act(() => {
      clickControl(root, "Answer");
    });
    assert.equal(acted, 1, "Answer did not call onAct once");
  });
});

test("NoticeStack: Open is an ordinary control, not the primary one", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "done" }, dir: "tidepool", what: "done" },
        act: "Open",
      });
    });
    const open = [...noticeRoot(container).querySelectorAll("button")].find((button) => button.textContent === "Open");
    assert.ok(open, "no Open control");
    assert.equal(open.classList.contains("primary"), false, "Open is drawn as the primary control");
  });
});

test("NoticeStack: a notice with no act has Mark read as its only control", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, { head: { lead: { kind: "icon", icon: "clock" }, dir: "", what: "a limit" }, act: null });
    });
    assert.deepEqual(controlTexts(noticeRoot(container)), ["Mark read"]);
  });
});

test("NoticeStack: Mark read is quiet, and a click calls onRead and not onAct", async () => {
  await withContainer(async (container) => {
    const calls: string[] = [];
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" },
        act: "Answer",
        onAct: () => calls.push("act"),
        onRead: () => calls.push("read"),
      });
    });
    const root = noticeRoot(container);
    const read = [...root.querySelectorAll("button")].find((button) => button.textContent === "Mark read");
    assert.ok(read?.classList.contains("quiet"), "Mark read is not the quiet control");
    await act(() => {
      clickControl(root, "Mark read");
    });
    assert.deepEqual(calls, ["read"], "Mark read did not call onRead alone");
  });
});

test("NoticeStack: no control reads Dismiss or ×", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "waiting" }, dir: "tidepool", what: "waiting on you" },
        act: "Answer",
      });
    });
    assert.deepEqual(controlTexts(noticeRoot(container)), ["Answer", "Mark read"]);
  });
});

test("NoticeStack: the stack is a status block", async () => {
  await withContainer(async (container) => {
    await act(() => {
      drawNotice(container, { head: { lead: { kind: "icon", icon: "bell" }, dir: "", what: "a bell alert" } });
    });
    assert.equal(noticeRoot(container).getAttribute("role"), "status");
  });
});

test("NoticeStack: a flat notice is one button that calls onClick, with no control inside it", async () => {
  await withContainer(async (container) => {
    let clicks = 0;
    await act(() => {
      drawNotice(container, {
        head: { lead: { kind: "glyph", state: "done" }, dir: "tidepool", what: "done" },
        flat: true,
        onClick: () => (clicks += 1),
      });
    });
    const root = noticeRoot(container);
    assert.equal(root.tagName, "BUTTON", "the flat notice is not the one button");
    assert.equal(container.querySelectorAll("button").length, 1, "the flat notice holds controls inside it");
    await act(() => {
      root.dispatchEvent(new Event("click", { bubbles: true }));
    });
    assert.equal(clicks, 1, "the flat notice did not call onClick once");
  });
});

test("NoticeStack: an unread flat notice is marked unread, and a read one is not", async () => {
  await withContainer(async (container) => {
    const head: NoticeHead = { lead: { kind: "glyph", state: "done" }, dir: "tidepool", what: "done" };
    await act(() => {
      drawNotice(container, { head, flat: true, unread: true });
    });
    assert.ok(noticeRoot(container).classList.contains("unread"), "an unread notice is not marked");
    await act(() => {
      drawNotice(container, { head, flat: true, unread: false });
    });
    assert.equal(noticeRoot(container).classList.contains("unread"), false, "a read notice is marked unread");
  });
});

test("NoticeStack: the stack rule sits under the title row's band, at the right", () => {
  // This file is bundled to web/build, so web/src is one directory up.
  const css = readFileSync(fileURLToPath(new URL("../src/style.css", import.meta.url)), "utf8");
  const rule = /^\.notices \{([^}]*)\}/m.exec(css);
  assert.ok(rule, "style.css has no .notices rule");
  const declarations = new Map(
    rule[1]
      .split(";")
      .map((declaration) => declaration.trim())
      .filter((declaration) => declaration !== "")
      .map((declaration) => {
        const colon = declaration.indexOf(":");
        return [declaration.slice(0, colon).trim(), declaration.slice(colon + 1).trim()];
      }),
  );
  assert.equal(declarations.get("right"), "var(--o-space-16)", "the stack is not at the right, in the page's gutter");
  assert.ok(
    declarations.get("top")?.includes("var(--o-size-control-desk)"),
    "the stack's top does not clear the title row, whose height its controls set",
  );
});
