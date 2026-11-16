import assert from "node:assert/strict";
import { test } from "node:test";
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
import { applyNotice, NoticeBanner, watchNotices } from "./notices";
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
        <NoticeBanner notices={[notice("%1", "one"), notice("%2", "two")]} onOpen={(item) => opened.push(item)} />,
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

function buttonTexts(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("button"), (button) => button.textContent ?? "");
}

function clickButton(container: HTMLElement, index: number): void {
  const button = container.querySelectorAll("button")[index];
  assert.ok(button !== undefined, `no button at index ${index}`);
  button.dispatchEvent(new Event("click", { bubbles: true }));
}
