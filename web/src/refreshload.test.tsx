import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { act } from "preact/test-utils";

import { createClients } from "./client";
import { Epics, EpicsGetResponseSchema, EpicSchema } from "./gen/orrery/v1/epics_pb";
import { Notices } from "./gen/orrery/v1/notice_pb";
import {
  Control,
  Events,
  Host,
  Panes,
  Presence,
  Sessions,
  SessionsWatchConversationResponseSchema,
  type WatchResponse,
  WatchResponseSchema,
} from "./gen/orrery/v1/orrery_pb";
import { start } from "./main";
import type { SeenWindow } from "./seen";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// The scenario counts the work one tab asks of serve: one tab open on an agent's headless session whose conversation
// created two of five live epics, so its reader draws two epic cards, while the agent streams its reply for a minute of
// mocked time, visible or hidden. Nothing in it waits on the real clock: the page's timers and Date are mocked, and each
// wait is for the fake server's calls to settle.
const agentId = "0123456789abcdef0123456789abcdef";
const liveIds = ["repo-a/e1", "repo-a/e2", "repo-a/e3", "repo-a/e4", "repo-a/e5"];
const cardIds = liveIds.slice(0, 2);
// The page's poll, as main.tsx's default.
const pollMs = 5_000;
// The minute the scenario runs, in steps of a tenth of a second: the agent streams one engineText each step, ten a
// second, and ends a turn every ten seconds.
const stepMs = 100;
const steps = 600;
const turnEvery = 100;
// How many turns in a row the fake server must have no call in flight before a step's work counts as done. A call the
// page starts from a render waits one setImmediate turn for its effect, so a few turns cover every chain the page makes.
const calmTurns = 12;

// Counts is the work the page asks of serve: one count per unary call, by procedure.
interface Counts {
  epicsList: number;
  epicsGet: number;
  panesList: number;
  sessionsList: number;
  listHeadless: number;
  hostStatus: number;
}

function noCounts(): Counts {
  return { epicsList: 0, epicsGet: 0, panesList: 0, sessionsList: 0, listHeadless: 0, hostStatus: 0 };
}

function minus(after: Counts, before: Counts): Counts {
  return {
    epicsList: after.epicsList - before.epicsList,
    epicsGet: after.epicsGet - before.epicsGet,
    panesList: after.panesList - before.panesList,
    sessionsList: after.sessionsList - before.sessionsList,
    listHeadless: after.listHeadless - before.listHeadless,
    hostStatus: after.hostStatus - before.hostStatus,
  };
}

// liveEpic is one live orreryd epic with one running step at revision, as Epics.List lists it.
function liveEpic(id: string, revision = 1n) {
  return create(EpicSchema, {
    id,
    repository: "repo-a",
    slug: id.slice(id.indexOf("/") + 1),
    outcome: "build",
    changed: 100n,
    live: true,
    source: "orreryd",
    phaseAt: 90n,
    runningSteps: [{ id: `${id}-step`, task: "T1", agent: "agent-a", started: 95n }],
    revision,
  });
}

// EventFeed is the Events.Watch stream the test writes to. Each event it pushes reaches the open stream in order.
class EventFeed {
  private readonly queued: WatchResponse[] = [];
  private wake: (() => void) | null = null;

  push(init: MessageInitShape<typeof WatchResponseSchema>): void {
    this.queued.push(create(WatchResponseSchema, init));
    this.wake?.();
    this.wake = null;
  }

  async *stream(signal: AbortSignal): AsyncGenerator<WatchResponse> {
    while (!signal.aborted) {
      const next = this.queued.shift();
      if (next !== undefined) {
        yield next;
        continue;
      }
      await new Promise<void>((resolve) => {
        this.wake = resolve;
        signal.addEventListener("abort", () => resolve(), { once: true });
      });
    }
  }
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

// Streams counts the page's open Events.Watch and Notices.Watch streams, and how many of each it ever opened.
interface Streams {
  events: number;
  notices: number;
  eventsOpened: number;
  noticesOpened: number;
}

// FakeServe answers the page's calls in memory and counts the unary reads that reach serve and the streams the page
// holds. revisions holds each live epic's revision as Epics.List answers it, which a test moves as orreryd would on a
// write to the epic.
interface FakeServe {
  clients: ReturnType<typeof createClients>;
  counts: Counts;
  streams: Streams;
  revisions: Map<string, bigint>;
  // build is the page build Host.Status names, which a test moves as a deploy of serve would.
  build: { served: string };
  // The unary calls answering now. The page's work after a step is done once none has been in flight for calmTurns.
  inFlight: () => number;
  feed: EventFeed;
}

function fakeServe(): FakeServe {
  const counts = noCounts();
  const streams: Streams = { events: 0, notices: 0, eventsOpened: 0, noticesOpened: 0 };
  const revisions = new Map(liveIds.map((id) => [id, 1n]));
  const build = { served: "" };
  const feed = new EventFeed();
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
  const conversation = create(SessionsWatchConversationResponseSchema, {
    transcript: `${agentId}.jsonl`,
    seq: 1n,
    reset: true,
    items: cardIds.flatMap((id, index) => [
      { kind: "tool", time: 100n, tool: "epic_create", input: id, id: `c${index}`, epic: id },
      { kind: "result", time: 100n, id: `c${index}`, text: "created" },
    ]),
    state: { session: { id: agentId, dir: "/work/agent", label: "agent", state: "working", changed: 100n } },
  });
  const transport = createRouterTransport((router) => {
    router.service(Panes, {
      list: () => counted("panesList", () => ({ panes: [] })),
    });
    router.service(Sessions, {
      list: () => counted("sessionsList", () => ({ sessions: [], seq: 7n })),
      listHeadless: () =>
        counted("listHeadless", () => ({
          sessions: [{ id: agentId, dir: "/work/agent", label: "agent", state: "working", changed: 100n }],
        })),
      watchConversation: async function* (request, context) {
        if (request.id === agentId && request.after <= 0n) {
          yield conversation;
        }
        await held(context.signal);
      },
    });
    router.service(Events, {
      watch: async function* (_request, context) {
        streams.events++;
        streams.eventsOpened++;
        try {
          yield* feed.stream(context.signal);
        } finally {
          streams.events--;
        }
      },
    });
    router.service(Epics, {
      list: () => counted("epicsList", () => ({ epics: liveIds.map((id) => liveEpic(id, revisions.get(id))) })),
      get: (request) =>
        counted("epicsGet", () => create(EpicsGetResponseSchema, { epic: liveEpic(request.id, revisions.get(request.id)) })),
      history: () => ({ entries: [], next: "" }),
    });
    router.service(Host, {
      status: () => counted("hostStatus", () => ({ host: "den", stats: [], pageBuild: build.served })),
    });
    router.service(Notices, {
      watch: async function* (_request, context) {
        streams.notices++;
        streams.noticesOpened++;
        try {
          await held(context.signal);
        } finally {
          streams.notices--;
        }
      },
    });
    router.service(Presence, { report: () => ({}) });
    router.service(Control, { seen: () => ({}) });
  });
  return { clients: createClients(transport), counts, streams, revisions, build, inFlight: () => inFlight, feed };
}

// FakeVisibility is the window the page reads its visibility from: visible or hidden as the test sets it, with a
// visibilitychange on each change.
interface FakeVisibility {
  win: SeenWindow;
  set: (state: "visible" | "hidden") => void;
}

function fakeVisibility(): FakeVisibility {
  const listeners = new Set<() => void>();
  const doc = {
    visibilityState: "visible",
    hasFocus: () => true,
    addEventListener: (type: string, fn: () => void) => {
      if (type === "visibilitychange") {
        listeners.add(fn);
      }
    },
    removeEventListener: (type: string, fn: () => void) => {
      if (type === "visibilitychange") {
        listeners.delete(fn);
      }
    },
  };
  return {
    win: { document: doc, addEventListener: () => {}, removeEventListener: () => {} },
    set: (state) => {
      doc.visibilityState = state;
      for (const listener of [...listeners]) {
        listener();
      }
    },
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

// streamMinute runs the minute: each step the agent streams one engineText, every turnEvery steps it also ends a
// turn, and the clock moves stepMs.
async function streamMinute(t: TestContext, serve: FakeServe): Promise<void> {
  let seq = 8n;
  for (let step = 1; step <= steps; step++) {
    serve.feed.push({
      seq,
      sessionId: agentId,
      event: { case: "engineText", value: { messageId: `m${seq}`, index: 0, kind: "text", text: `so far ${step}` } },
    });
    if (step % turnEvery === 0) {
      seq++;
      serve.feed.push({ seq, sessionId: agentId, event: { case: "engineTurnEnded", value: { subtype: "success" } } });
    }
    await calm(t, serve);
    t.mock.timers.tick(stepMs);
    await calm(t, serve);
  }
}

// Scenario is what one run of the page counted: the work of its first load, of the minute, and of the tab becoming
// visible after the minute, and the streams it held at the end of the minute and once shown.
interface Scenario {
  first: Counts;
  minute: Counts;
  shown: Counts;
  minuteStreams: Streams;
  shownStreams: Streams;
}

// runScenario opens the page on the agent's session, waits for its first load and its two epic cards, then runs the
// minute with the tab hidden or visible, and then shows the tab.
async function runScenario(t: TestContext, hidden: boolean): Promise<Scenario> {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  const visibility = fakeVisibility();
  window.history.replaceState(null, "", `#/attention/session/${agentId}`);
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = start(root, serve.clients, pollMs, 600_000, visibility.win);
    await until(
      t.signal,
      () => root.querySelectorAll(".epic-card .track").length === cardIds.length,
      "the two epic cards",
      { step: () => act(turn) },
    );
    await calm(t, serve);
    const first = { ...serve.counts };
    if (hidden) {
      visibility.set("hidden");
      await calm(t, serve);
    }
    const before = { ...serve.counts };
    await streamMinute(t, serve);
    const minute = minus(serve.counts, before);
    const minuteStreams = { ...serve.streams };
    const hiddenEnd = { ...serve.counts };
    visibility.set("visible");
    await calm(t, serve);
    const shown = minus(serve.counts, hiddenEnd);
    const shownStreams = { ...serve.streams };
    assert.equal(root.querySelectorAll(".epic-card .track").length, cardIds.length, "the epic cards went away");
    return { first, minute, shown, minuteStreams, shownStreams };
  } finally {
    await teardown();
  }
}

// counts builds the expected counts: the epics list and gets, one count for each of the three rows reads, and the host.
function counts(epicsList: number, epicsGet: number, rows: number, hostStatus: number): Counts {
  return { epicsList, epicsGet, panesList: rows, sessionsList: rows, listHeadless: rows, hostStatus };
}

// The first load lists the epics and asks for each of the five live epics once, reads the rows once, and reads the
// host. The two cards show the poll's detail of their epics and read nothing.
const firstLoad = counts(1, 5, 1, 1);

// Before the page reloaded by what an event can change, the same visible minute made 600 epics lists, 180 Gets (12
// ticks of 5 live epics, and each card reading its epic each second) and 600 reads of each of the three rows lists, and
// the first load made 7 Gets. The hidden minute made the same work as the visible one.
test("RefreshLoad: a visible tab over a streaming agent, five live epics and two epic cards, for a minute", async (t) => {
  const scenario = await runScenario(t, false);
  assert.deepEqual(scenario.first, firstLoad, "the first load");
  // The agent's streaming text and its turns' ends change neither the rows nor the epics, so only the 12 ticks load:
  // each lists the epics, asks for none, since none changed and the page shows no epic, and reads the host.
  assert.deepEqual(scenario.minute, counts(12, 0, 0, 12), "the minute");
  assert.deepEqual(scenario.shown, counts(0, 0, 0, 0), "a visibilitychange on a tab that stayed visible");
  const open = { events: 1, notices: 1, eventsOpened: 1, noticesOpened: 1 };
  assert.deepEqual(scenario.minuteStreams, open, "the streams of the visible minute");
  assert.deepEqual(scenario.shownStreams, open, "the streams of a tab that stayed visible");
});

test("RefreshLoad: a hidden tab over a streaming agent, five live epics and two epic cards, for a minute", async (t) => {
  const scenario = await runScenario(t, true);
  assert.deepEqual(scenario.first, firstLoad, "the first load");
  assert.deepEqual(scenario.minute, counts(0, 0, 0, 0), "the hidden minute");
  // Hiding the tab closed both streams, and none opened while it stayed hidden.
  assert.deepEqual(
    scenario.minuteStreams,
    { events: 0, notices: 0, eventsOpened: 1, noticesOpened: 1 },
    "the streams of the hidden minute",
  );
  // Shown again, the stale tab reloads once: the rows, the host, and the epics, asking for no detail, since no epic's
  // revision moved and the tab shows no epic. The rows load reopens Events.Watch and the notices watch reopens.
  assert.deepEqual(scenario.shown, counts(1, 0, 1, 1), "the tab shown after the minute");
  assert.deepEqual(
    scenario.shownStreams,
    { events: 1, notices: 1, eventsOpened: 2, noticesOpened: 2 },
    "the streams of the tab shown again",
  );
});

test("RefreshLoad: a tab hidden at start loads nothing until it is shown, and then loads once", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  const visibility = fakeVisibility();
  visibility.set("hidden");
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = start(root, serve.clients, pollMs, 600_000, visibility.win);
    await calm(t, serve);
    for (let tick = 0; tick < 3; tick++) {
      t.mock.timers.tick(pollMs);
      await calm(t, serve);
    }
    assert.deepEqual(serve.counts, counts(0, 0, 0, 0), "a hidden tab loaded");
    assert.deepEqual(serve.streams, { events: 0, notices: 0, eventsOpened: 0, noticesOpened: 0 }, "a hidden tab's streams");
    visibility.set("visible");
    await until(t.signal, () => root.querySelector('[data-key="epic:repo-a/e1"]') !== null, "the shown tab's epics", {
      step: () => act(turn),
    });
    await calm(t, serve);
    assert.deepEqual(serve.counts, counts(1, 5, 1, 1), "the shown tab's load");
    assert.deepEqual(serve.streams, { events: 1, notices: 1, eventsOpened: 1, noticesOpened: 1 }, "the shown tab's streams");
  } finally {
    await teardown();
  }
});

// A question or a park on a live epic the page does not show moves only the epic's revision in its listed message. The
// next tick lists the epics and asks Epics.Get for that epic alone, so the page's Needs-you section reads it within one
// poll.
test("RefreshLoad: a tick asks Epics.Get for exactly the live epic whose revision moved", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  const visibility = fakeVisibility();
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = start(root, serve.clients, pollMs, 600_000, visibility.win);
    await until(t.signal, () => root.querySelector('[data-key="epic:repo-a/e1"]') !== null, "the epics", {
      step: () => act(turn),
    });
    await calm(t, serve);
    const before = { ...serve.counts };
    serve.revisions.set("repo-a/e3", 2n);
    const gets: string[] = [];
    const get = serve.clients.epics.get;
    serve.clients.epics.get = (request, options) => {
      gets.push(request.id ?? "");
      return get(request, options);
    };
    t.mock.timers.tick(pollMs);
    await calm(t, serve);
    assert.deepEqual(minus(serve.counts, before), counts(1, 1, 0, 1), "the tick after the revision moved");
    assert.deepEqual(gets, ["repo-a/e3"], "the epics the tick asked Epics.Get for");
    const settled = { ...serve.counts };
    t.mock.timers.tick(pollMs);
    await calm(t, serve);
    assert.deepEqual(minus(serve.counts, settled), counts(1, 0, 0, 1), "the tick after, with no revision moved");
  } finally {
    await teardown();
  }
});

// The live shape orreryd's metrics showed: the running steps of live epics change state many times a minute, and each
// such event lists the epics again. An epic whose listed message is unchanged is never asked for, however many lists
// its steps' events cause, so the events cost one Epics.List each and no Epics.Get.
test("RefreshLoad: step-session events list the epics again and ask Epics.Get for no unchanged epic", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  const visibility = fakeVisibility();
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    stop = start(root, serve.clients, pollMs, 600_000, visibility.win);
    await until(t.signal, () => root.querySelector('[data-key="epic:repo-a/e1"]') !== null, "the epics", {
      step: () => act(turn),
    });
    await calm(t, serve);
    const before = { ...serve.counts };
    // Forty step-state events, one every 250 ms across ten seconds, each from the running step of a live epic.
    let seq = 8n;
    for (let event = 0; event < 40; event++) {
      const step = `${liveIds[event % liveIds.length]}-step`;
      serve.feed.push({ seq, sessionId: step, event: { case: "engineChanged", value: { state: "working" } } });
      seq++;
      await calm(t, serve);
      t.mock.timers.tick(250);
      await calm(t, serve);
    }
    // Each event lists the epics once after its 100 ms wait, and each of the two 5 s ticks lists them once more.
    assert.deepEqual(minus(serve.counts, before), counts(42, 0, 0, 2), "the ten seconds of step events");
  } finally {
    await teardown();
  }
});

// typeInto sets box's text as the owner typing would, with the input event that bubbles to the page's root.
function typeInto(box: HTMLTextAreaElement, text: string): void {
  box.value = text;
  box.dispatchEvent(new Event("input", { bubbles: true }));
}

// openOnAgent starts the page on the agent's session, whose reader holds a composer, with page as its build and
// reload, and waits until its first loads have settled. It returns the composer's text box.
async function openOnAgent(
  t: TestContext,
  serve: FakeServe,
  visibility: FakeVisibility,
  root: HTMLElement,
  page: { build: string; reload: () => void },
): Promise<{ stop: () => void; box: HTMLTextAreaElement }> {
  window.history.replaceState(null, "", `#/attention/session/${agentId}`);
  const stop = start(root, serve.clients, pollMs, 600_000, visibility.win, page);
  await until(t.signal, () => root.querySelector("textarea") !== null, "the composer", { step: () => act(turn) });
  await calm(t, serve);
  const box = root.querySelector("textarea");
  assert.ok(box !== null, "the composer's text box");
  return { stop, box };
}

// A deploy of serve moves the page build Host.Status names. The page that an earlier serve sent waits while the owner
// can see it, and while its composer holds a draft, and reloads once at the first moment it is hidden with no draft.
test("RefreshLoad: a page whose build is not serve's reloads once it is hidden and holds no draft", async (t) => {
  mockTimers(t, ["setTimeout", "setInterval", "Date"]);
  const window = installTestDom();
  const root = document.createElement("div");
  const serve = fakeServe();
  const visibility = fakeVisibility();
  let reloads = 0;
  let stop: () => void = () => {};
  const teardown = teardownOnce(t.signal, async () => {
    stop();
    await removeTestDom(window);
  });
  try {
    serve.build.served = "build-b";
    let box: HTMLTextAreaElement;
    ({ stop, box } = await openOnAgent(t, serve, visibility, root, { build: "build-a", reload: () => reloads++ }));
    assert.equal(reloads, 0, "a visible page that learned it is stale");
    await act(() => typeInto(box, "a draft"));
    visibility.set("hidden");
    t.mock.timers.tick(pollMs);
    await calm(t, serve);
    assert.equal(reloads, 0, "a hidden page holding a draft, through a tick");
    await act(() => typeInto(box, ""));
    assert.equal(reloads, 1, "the draft cleared while hidden");
    visibility.set("visible");
    visibility.set("hidden");
    t.mock.timers.tick(pollMs);
    await calm(t, serve);
    assert.equal(reloads, 1, "a page reloads once");
  } finally {
    await teardown();
  }
});

test("RefreshLoad: a page whose build is serve's, or that serve names no build for, never reloads", async (t) => {
  for (const served of ["build-a", ""]) {
    await t.test(`serve names ${JSON.stringify(served)}`, async (t) => {
      mockTimers(t, ["setTimeout", "setInterval", "Date"]);
      const window = installTestDom();
      const root = document.createElement("div");
      const serve = fakeServe();
      const visibility = fakeVisibility();
      let reloads = 0;
      let stop: () => void = () => {};
      const teardown = teardownOnce(t.signal, async () => {
        stop();
        await removeTestDom(window);
      });
      try {
        serve.build.served = served;
        ({ stop } = await openOnAgent(t, serve, visibility, root, { build: "build-a", reload: () => reloads++ }));
        visibility.set("hidden");
        t.mock.timers.tick(pollMs);
        await calm(t, serve);
        visibility.set("visible");
        await calm(t, serve);
        visibility.set("hidden");
        assert.equal(reloads, 0);
      } finally {
        await teardown();
      }
    });
  }
});
