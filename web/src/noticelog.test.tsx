import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Clients } from "./actions";
import { createClients } from "./client";
import { type ListedNotice, ListedNoticeSchema, Notices, NoticesListResponseSchema } from "./gen/orrery/v1/notice_pb";
import { noticeRelistMillis, type NoticeLog, useNoticeLog } from "./noticelog";
import { realClock, type SeenWindow } from "./seen";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom } from "./testdom";
import { turn } from "./testwait";

// FakeWindow is the window the hook watches: its document's visibility, with a visibilitychange on each change, and
// the window's focus. It counts the listeners left on it, so a test can see an unmount remove them all.
interface FakeWindow {
  win: SeenWindow;
  setVisibility: (state: "visible" | "hidden") => void;
  focus: () => void;
  listeners: () => number;
}

function fakeWindow(): FakeWindow {
  const visibility = new Set<() => void>();
  const focus = new Set<() => void>();
  const doc = {
    visibilityState: "visible",
    hasFocus: () => true,
    addEventListener(type: string, fn: () => void) {
      if (type === "visibilitychange") visibility.add(fn);
    },
    removeEventListener(type: string, fn: () => void) {
      if (type === "visibilitychange") visibility.delete(fn);
    },
  };
  return {
    win: {
      document: doc,
      addEventListener(type: string, fn: () => void) {
        if (type === "focus") focus.add(fn);
      },
      removeEventListener(type: string, fn: () => void) {
        if (type === "focus") focus.delete(fn);
      },
    },
    setVisibility(state) {
      doc.visibilityState = state;
      for (const fn of [...visibility]) fn();
    },
    focus() {
      for (const fn of [...focus]) fn();
    },
    listeners: () => visibility.size + focus.size,
  };
}

// Server is the Notices service the hook lists from. Each List reads the rows, failing and hold as they are when it
// arrives, so a list answered after the server changed still answers what it was asked then. A hold, when set, keeps
// that list open until the test resolves it.
interface Server {
  rows: ListedNotice[];
  failing: boolean;
  hold: Promise<void> | null;
  lists: number;
}

function fakeServer(): { clients: Clients; server: Server } {
  const server: Server = { rows: [], failing: false, hold: null, lists: 0 };
  const transport = createRouterTransport((router) => {
    router.service(Notices, {
      list: async () => {
        server.lists++;
        const { rows, failing, hold } = server;
        if (hold !== null) await hold;
        if (failing) throw new ConnectError("the notices are down", Code.Unavailable);
        return create(NoticesListResponseSchema, { notices: rows });
      },
    });
  });
  return { clients: createClients(transport), server };
}

function notice(title: string, read: boolean): ListedNotice {
  return create(ListedNoticeSchema, { state: "done", title, body: `${title} body`, time: 1n, read });
}

// Probe is a component that uses the hook and keeps the log of its latest render.
interface Probed {
  log: NoticeLog | null;
}

function Probe({
  clients,
  win,
  watched,
  probed,
}: {
  clients: Clients;
  win: SeenWindow;
  watched: unknown;
  probed: Probed;
}) {
  probed.log = useNoticeLog(clients, win, realClock, watched);
  return null;
}

// Page is the mounted hook and the fakes it runs against, as a test drives them.
interface Page {
  window: FakeWindow;
  server: Server;
  probed: Probed;
  settle: () => Promise<void>;
  advance: (ms: number) => Promise<void>;
  rerender: (watched: unknown) => Promise<void>;
  unmount: () => Promise<void>;
}

// withPage mounts the hook on a page whose clock is the test's mocked one, with the rows the server answers the first
// List with, runs check, and then unmounts the page and removes its DOM.
async function withPage(
  t: TestContext,
  options: { rows?: ListedNotice[]; watched?: unknown },
  check: (page: Page) => Promise<void>,
): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = fakeWindow();
  const { clients, server } = fakeServer();
  server.rows = options.rows ?? [];
  const probed: Probed = { log: null };
  const draw = (watched: unknown) => (
    <Probe clients={clients} win={fake.win} watched={watched} probed={probed} />
  );
  const page: Page = {
    window: fake,
    server,
    probed,
    settle: () =>
      act(async () => {
        await turn();
        await turn();
        await turn();
      }),
    advance: (ms) =>
      act(() => {
        t.mock.timers.tick(ms);
      }),
    rerender: (watched) =>
      act(() => {
        render(draw(watched), container);
      }),
    unmount: () =>
      act(() => {
        render(null, container);
      }),
  };
  try {
    await act(() => {
      render(draw(options.watched), container);
    });
    await page.settle();
    await check(page);
  } finally {
    await page.unmount();
    container.remove();
    await removeTestDom(window);
  }
}

function log(page: Page): NoticeLog {
  assert.ok(page.probed.log, "the probe rendered the hook");
  return page.probed.log;
}

test("NoticeBell: lists the notices at mount", async (t) => {
  await withPage(t, { rows: [notice("a", false)] }, async (page) => {
    assert.equal(page.server.lists, 1);
    assert.deepEqual(
      log(page).rows.map((row) => row.title),
      ["a"],
    );
  });
});

test("NoticeBell: lists when the page becomes visible and not while it is hidden", async (t) => {
  await withPage(t, {}, async (page) => {
    await act(() => page.window.setVisibility("hidden"));
    await page.settle();
    assert.equal(page.server.lists, 1, "hiding the page lists nothing");
    await act(() => page.window.setVisibility("visible"));
    await page.settle();
    assert.equal(page.server.lists, 2, "showing the page again lists");
  });
});

test("NoticeBell: lists when the window takes focus", async (t) => {
  await withPage(t, {}, async (page) => {
    await act(() => page.window.focus());
    await page.settle();
    assert.equal(page.server.lists, 2);
  });
});

test("NoticeBell: lists every 60 s while visible and not while hidden", async (t) => {
  await withPage(t, {}, async (page) => {
    await page.advance(noticeRelistMillis);
    await page.settle();
    assert.equal(page.server.lists, 2, "the first minute while visible lists once");

    await act(() => page.window.setVisibility("hidden"));
    await page.advance(2 * noticeRelistMillis);
    await page.settle();
    assert.equal(page.server.lists, 2, "two minutes hidden list nothing");

    await act(() => page.window.setVisibility("visible"));
    await page.settle();
    assert.equal(page.server.lists, 3);

    await page.advance(noticeRelistMillis);
    await page.settle();
    assert.equal(page.server.lists, 4, "the next minute while visible lists again");
  });
});

test("NoticeBell: lists again when the watched notices change identity", async (t) => {
  const first: ListedNotice[] = [];
  await withPage(t, { watched: first }, async (page) => {
    assert.equal(page.server.lists, 1);
    await page.rerender(first);
    await page.settle();
    assert.equal(page.server.lists, 1, "the same identity lists nothing");
    await page.rerender([]);
    await page.settle();
    assert.equal(page.server.lists, 2, "a new identity lists");
  });
});

test("NoticeBell: relist lists on demand", async (t) => {
  await withPage(t, {}, async (page) => {
    await act(() => log(page).relist());
    await page.settle();
    assert.equal(page.server.lists, 2);
  });
});

test("NoticeBell: unread counts the rows whose read is false", async (t) => {
  const rows = [notice("a", false), notice("b", true), notice("c", false)];
  await withPage(t, { rows }, async (page) => {
    assert.equal(log(page).rows.length, 3);
    assert.equal(log(page).unread, 2);
  });
});

test("NoticeBell: a failed list keeps the last rows and sets failed until a later answer clears it", async (t) => {
  await withPage(t, { rows: [notice("a", false)] }, async (page) => {
    page.server.failing = true;
    await act(() => log(page).relist());
    await page.settle();
    assert.deepEqual(
      log(page).rows.map((row) => row.title),
      ["a"],
      "the last log is kept",
    );
    assert.equal(log(page).failed, true);

    page.server.failing = false;
    page.server.rows = [notice("a", false), notice("b", false)];
    await act(() => log(page).relist());
    await page.settle();
    assert.equal(log(page).failed, false);
    assert.equal(log(page).rows.length, 2);
  });
});

test("NoticeBell: an older answer that lands after a newer one changes nothing", async (t) => {
  await withPage(t, { rows: [notice("old", false)] }, async (page) => {
    let release: () => void = () => {};
    page.server.hold = new Promise<void>((resolve) => {
      release = resolve;
    });
    await act(() => log(page).relist());
    page.server.hold = null;
    page.server.rows = [notice("new", true)];
    await act(() => log(page).relist());
    await page.settle();
    release();
    await page.settle();
    assert.deepEqual(
      log(page).rows.map((row) => row.title),
      ["new"],
    );
  });
});

test("NoticeBell: unmount removes the listeners and the timer", async (t) => {
  await withPage(t, {}, async (page) => {
    assert.ok(page.window.listeners() > 0, "the mounted hook listens to the window");
    await page.unmount();
    assert.equal(page.window.listeners(), 0);
    const lists = page.server.lists;
    await page.advance(2 * noticeRelistMillis);
    await act(() => page.window.setVisibility("hidden"));
    await act(() => page.window.setVisibility("visible"));
    await act(() => page.window.focus());
    await page.settle();
    assert.equal(page.server.lists, lists, "nothing is listed after unmount");
  });
});
