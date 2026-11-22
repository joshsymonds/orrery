import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { type MessageInitShape } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act } from "preact/test-utils";

import { createClients } from "./client";
import { Epics } from "./gen/orrery/v1/epics_pb";
import { Notices } from "./gen/orrery/v1/notice_pb";
import {
  Control,
  Events,
  HeadlessSessionSchema,
  Host,
  Panes,
  Presence,
  Sessions,
} from "./gen/orrery/v1/orrery_pb";
import { start } from "./main";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test is one tab on the inbox over two headless sessions, with the event stream open. The page's poll is the fake
// clock's 5 s tick, and nothing in a test waits on the real clock: each wait is for the fake server's calls to settle.
const pollMs = 5_000;
// How many turns in a row the fake server must have no call in flight before a tick's work counts as done. A call the
// page starts from a render waits one setImmediate turn for its effect, so a few turns cover every chain the page makes.
const calmTurns = 12;
const firstId = "0123456789abcdef0123456789abcdef";
const secondId = "fedcba9876543210fedcba9876543210";

// Counts is the work the page asks of serve: one count per call that reads the rows, by procedure.
interface Counts {
  panesList: number;
  sessionsList: number;
  listHeadless: number;
}

function minus(after: Counts, before: Counts): Counts {
  return {
    panesList: after.panesList - before.panesList,
    sessionsList: after.sessionsList - before.sessionsList,
    listHeadless: after.listHeadless - before.listHeadless,
  };
}

// headless is one headless session as Sessions.ListHeadless answers it, under a synthetic directory, whose headline is
// base, so its row is titled base.
function headless(id: string, base: string, archived: boolean): MessageInitShape<typeof HeadlessSessionSchema> {
  return { id, dir: `/work/${base}`, headline: base, state: "working", changed: 100n, seen: 0n, archived };
}

// FakeServe answers the page's calls in memory and counts the row reads that reach serve. setHeadless changes the
// sessions Sessions.ListHeadless answers from now on, as serve would when a session is archived. failing names the list
// calls that fail from now on. streams counts the Events.Watch streams the page holds, and how many it ever opened.
interface FakeServe {
  clients: ReturnType<typeof createClients>;
  counts: Counts;
  streams: { events: number; eventsOpened: number };
  setHeadless: (sessions: MessageInitShape<typeof HeadlessSessionSchema>[]) => void;
  failing: { sessions: boolean; headless: boolean };
  // The unary calls answering now. The page's work after a tick is done once none has been in flight for calmTurns.
  inFlight: () => number;
}

// held waits until signal aborts, so a stream with nothing to send stays open until its client ends it.
function held(signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
    } else {
      signal.addEventListener("abort", () => resolve(), { once: true });
    }
  });
}

function fakeServe(): FakeServe {
  const counts: Counts = { panesList: 0, sessionsList: 0, listHeadless: 0 };
  const streams = { events: 0, eventsOpened: 0 };
  const failing = { sessions: false, headless: false };
  let headlessAnswer: MessageInitShape<typeof HeadlessSessionSchema>[] = [];
  let inFlight = 0;
  // counted counts one call under name, and holds it in flight across one promise turn, as a real answer would be.
  const counted = async <T,>(name: keyof Counts, answer: () => T): Promise<T> => {
    counts[name]++;
    inFlight++;
    try {
      await Promise.resolve();
      return answer();
    } finally {
      inFlight--;
    }
  };
  const transport = createRouterTransport((router) => {
    router.service(Panes, {
      list: () => counted("panesList", () => ({ panes: [] })),
    });
    router.service(Sessions, {
      list: () =>
        counted("sessionsList", () => {
          if (failing.sessions) {
            throw new ConnectError("sessions server detail", Code.Unavailable);
          }
          return { sessions: [], seq: 7n };
        }),
      listHeadless: () =>
        counted("listHeadless", () => {
          if (failing.headless) {
            throw new ConnectError("headless server detail", Code.Unavailable);
          }
          return { sessions: headlessAnswer };
        }),
    });
    router.service(Events, {
      watch: async function* (_request, context) {
        streams.events++;
        streams.eventsOpened++;
        try {
          await held(context.signal);
        } finally {
          streams.events--;
        }
      },
    });
    router.service(Epics, {
      list: () => ({ epics: [] }),
    });
    router.service(Host, {
      status: () => ({ host: "den", stats: [], pageBuild: "" }),
    });
    router.service(Notices, {
      watch: async function* (_request, context) {
        await held(context.signal);
      },
    });
    router.service(Presence, { report: () => ({}) });
    router.service(Control, { seen: () => ({}) });
  });
  return {
    clients: createClients(transport),
    counts,
    streams,
    setHeadless: (sessions) => {
      headlessAnswer = sessions;
    },
    failing,
    inFlight: () => inFlight,
  };
}

// calm waits until no call has been in flight for calmTurns turns in a row, each turn inside act so the renders the
// answers cause are applied, and the calls those renders start are counted.
function calm(t: TestContext, serve: FakeServe): Promise<void> {
  let quiet = 0;
  return until(
    t.signal,
    () => {
      quiet = serve.inFlight() === 0 ? quiet + 1 : 0;
      return quiet >= calmTurns;
    },
    "the page's calls to settle",
    { step: () => act(turn) },
  );
}

// rowOf is the inbox row of the headless session id, or null when the inbox draws none.
function rowOf(root: HTMLElement, id: string): Element | null {
  return root.querySelector(`[data-row][data-key="session:${id}"]`);
}

// openInbox starts the page on the inbox, and waits until both rows are drawn and the event stream is open. The page's
// stop is returned.
async function openInbox(t: TestContext, serve: FakeServe, root: HTMLElement): Promise<() => void> {
  const stop = start(root, serve.clients, pollMs, 600_000);
  await until(
    t.signal,
    () => rowOf(root, firstId) !== null && rowOf(root, secondId) !== null && serve.streams.events === 1,
    "the first load and the open stream",
    { step: () => act(turn) },
  );
  await calm(t, serve);
  return stop;
}

// leaveFirst moves the cursor from the first row, where the page starts, to the second. A list answer that archives the
// entry the cursor rests on keeps it drawn, so an archive test moves the cursor off the row it archives first.
async function leaveFirst(root: HTMLElement): Promise<void> {
  assert.equal(
    root.querySelector(".row.cur")?.getAttribute("data-key"),
    `session:${firstId}`,
    "the cursor starts on the first row",
  );
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "j", cancelable: true }));
  });
  assert.equal(
    root.querySelector(".row.cur")?.getAttribute("data-key"),
    `session:${secondId}`,
    "the cursor moved to the second row",
  );
}

// InboxTidyRowTick: with the event stream open, each tick lists the conversations and never the panes. A tick reads
// Sessions.List and Sessions.ListHeadless once each, so the rows are re-listed while the stream is open.
test("InboxTidyRowTick: with the event stream open, each tick lists the conversations and no panes", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  serve.setHeadless([headless(firstId, "gatehouse", false), headless(secondId, "orrery", false)]);
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = await openInbox(t, serve, root);
    const before = { ...serve.counts };
    for (let tick = 0; tick < 3; tick++) {
      t.mock.timers.tick(pollMs);
      await calm(t, serve);
    }
    assert.deepEqual(
      minus(serve.counts, before),
      { panesList: 0, sessionsList: 3, listHeadless: 3 },
      "three ticks with the stream open",
    );
    assert.equal(serve.streams.eventsOpened, 1, "the stream was reopened while it was open");
  } finally {
    await teardown();
  }
});

// InboxTidyRowTick: a row whose answer turns archived leaves the inbox at the next tick, with no event arriving. The
// fake stream never sends anything, so only the tick's conversations list can change the rows.
test("InboxTidyRowTick: a row whose answer turns archived leaves the inbox on the next tick, with no event pushed", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  serve.setHeadless([headless(firstId, "gatehouse", false), headless(secondId, "orrery", false)]);
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = await openInbox(t, serve, root);
    await leaveFirst(root);
    serve.setHeadless([headless(firstId, "gatehouse", true), headless(secondId, "orrery", false)]);
    t.mock.timers.tick(pollMs);
    await calm(t, serve);
    assert.equal(rowOf(root, firstId), null, "the archived row is still in the inbox");
    assert.notEqual(rowOf(root, secondId), null, "the row that stayed in the list left the inbox");
    assert.equal(serve.streams.eventsOpened, 1, "the stream was reopened, though the archive came with no event");
  } finally {
    await teardown();
  }
});

// InboxTidyRowTick: when either list fails, the tick leaves the rows as they were, though the answer of the list that
// did not fail would archive a row. The next tick, with both lists answering, draws the archive.
for (const failed of ["sessions", "headless"] as const) {
  test(`InboxTidyRowTick: a tick whose ${failed} list fails leaves the rows as they were`, async (t) => {
    mockTimers(t, ["setTimeout", "setInterval", "Date"]);
    const window = installTestDom();
    const root = document.createElement("div");
    const serve = fakeServe();
    serve.setHeadless([headless(firstId, "gatehouse", false), headless(secondId, "orrery", false)]);
    let stop: () => void = () => {};
    const teardown = teardownOnce(t.signal, async () => {
      stop();
      await removeTestDom(window);
    });
    try {
      stop = await openInbox(t, serve, root);
      await leaveFirst(root);
      serve.setHeadless([headless(firstId, "gatehouse", true), headless(secondId, "orrery", false)]);
      serve.failing[failed] = true;
      t.mock.timers.tick(pollMs);
      await calm(t, serve);
      assert.notEqual(rowOf(root, firstId), null, "a tick whose list failed took the row out of the inbox");
      assert.notEqual(rowOf(root, secondId), null, "a tick whose list failed took the other row out of the inbox");
      serve.failing[failed] = false;
      t.mock.timers.tick(pollMs);
      await calm(t, serve);
      assert.equal(rowOf(root, firstId), null, "the archive did not leave the inbox once the lists answered");
    } finally {
      await teardown();
    }
  });
}
