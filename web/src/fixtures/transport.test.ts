import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, equals, toJson } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { type ComponentChild, h, render } from "preact";
import { act } from "preact/test-utils";

import { App, Page } from "../app.stories";
import { PageStory } from "../catalog/pagestory";
import { storyName } from "../catalog/story";
import type { Address } from "../router";
import {
  EpicsGetResponseSchema,
  EpicsListResponseSchema,
  EpicsPauseResponseSchema,
  EpicsResumeResponseSchema,
  EpicsRetryResponseSchema,
  EpicsStopResponseSchema,
} from "../gen/orrery/v1/epics_pb";
import {
  AskResponseSchema,
  HostStatusResponseSchema,
  PanesListResponseSchema,
  WatchConversationResponseSchema,
} from "../gen/orrery/v1/orrery_pb";
import { mockTimers } from "../testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { shippedSlices } from "./slices";
import { createFixtureTransport } from "./transport";

// wideWidth is the window width the stories draw at unless one names its own. It is past the 760px the wide layout starts
// at, so the inbox is docked or hidden as its state has it, not stacked under the page.
const wideWidth = 1280;

// overlayWidth is the width the epic panel lies over the conversation at: the 1000px round 6 draws epic-talk at, which
// is at 1250px or narrower.
const overlayWidth = 1000;

const attention: Address = { order: "attention", entry: null };

// refusal resolves to the Connect error a call rejects with, and fails the test when the call is answered.
async function refusal(call: Promise<unknown>): Promise<ConnectError> {
  try {
    await call;
  } catch (error) {
    return ConnectError.from(error);
  }
  assert.fail("the call was answered, not refused");
}

// listView is the order the rows list draws, from its data-list attribute, or null before the list is drawn.
function listView(root: HTMLElement): string | null {
  return root.querySelector(".rows")?.getAttribute("data-list") ?? null;
}

// inboxState is the state the inbox is drawn in, from the panes' data-inbox attribute.
function inboxState(root: HTMLElement): string | null {
  return root.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// The opening prompt of pane %12 and a fragment of pane %11's unfinished reply. Pane %12's prompt is drawn at home too,
// since the cursor rests on %12, and %11's fragment is drawn only when %11's conversation is.
const beaconPrompt = "Why is the roster page slow for weeks with more than forty shifts?";
const lampFragment = "Full jitter is in";

// The open decision of the rota-export epic, and the planning conversation Talk's first line opens in the reader. The
// Talk menu is drawn on the epic page only in the talk states, so the epic's own state checks neither.
const rotaQuestion = "Bump the embedded tz database?";
const rotaPrompt = "Plan the rota export as an epic.";

// inboxHome is Page/inbox-home: the attention rows, with the inbox docked beside the cursor's reader on pane %12.
function inboxHome(root: HTMLElement): boolean {
  return listView(root) === "attention" && inboxState(root) === "docked";
}

// convoWorking is Page/convo-working: the inbox hidden under the reader of pane %11.
function convoWorking(root: HTMLElement): boolean {
  return inboxState(root) === "hidden" && (root.textContent ?? "").includes(lampFragment);
}

// askSession is Page/ask-session: the inbox hidden under the reader of pane %12.
function askSession(root: HTMLElement): boolean {
  return inboxState(root) === "hidden" && (root.textContent ?? "").includes(beaconPrompt);
}

// epicQuestion is Page/epic-question: Q1 is drawn in the now block, with no Talk menu drawn, and the epic panel beside the
// conversation rather than over it, which is epic-talk's layout.
function epicQuestion(root: HTMLElement): boolean {
  return (
    root.querySelector(".now-block .answer .question")?.textContent.includes(rotaQuestion) === true &&
    root.querySelector("ul.talk") === null &&
    root.querySelector(".panes.overlay") === null
  );
}

// epicHandover is Page/epic-handover: the Talk menu is drawn open.
function epicHandover(root: HTMLElement): boolean {
  return root.querySelector("ul.talk") !== null;
}

// epicTalk is Page/epic-talk: under 1250px the epic panel lies over the conversation, and the conversation's reader holds
// the planning prompt beneath it.
function epicTalk(root: HTMLElement): boolean {
  return (
    root.querySelector(".panes.overlay aside.rpanel") !== null &&
    root.querySelector('section[aria-label="reader"]')?.textContent.includes(rotaPrompt) === true
  );
}

// railKinds is the kind of each group the right rail draws, in order: epic, agents or running.
function railKinds(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".rail .rgroup")].map((group) => group.classList[1]);
}

// workRail is Page/work-rail: the buoy session's rail draws its three groups, epic, agents and running, and no panel is open.
function workRail(root: HTMLElement): boolean {
  const kinds = railKinds(root);
  return (
    kinds.length === 3 &&
    kinds[0] === "epic" &&
    kinds[1] === "agents" &&
    kinds[2] === "running" &&
    root.querySelector("aside.rpanel") === null
  );
}

// workRailGroup is Page/work-rail-group: pane %22's rail has its dots tile pressed, so its picker is open with four rows.
function workRailGroup(root: HTMLElement): boolean {
  return root.querySelectorAll(".rail .rpick .rpick-row").length === 4;
}

// orchardTitle is pane %13's line, which its reader draws once the fixture world has loaded.
const orchardTitle = "Parser tests for nested scion grafting";

// workEmpty is Page/work-empty: pane %13's conversation is drawn, and its rail with no tile in it.
function workEmpty(root: HTMLElement): boolean {
  return (
    root.querySelector('section[aria-label="reader"]')?.textContent.includes(orchardTitle) === true &&
    root.querySelector("aside.rail") !== null &&
    root.querySelectorAll(".rail .tile-w").length === 0
  );
}

// dockRunning is Page/dock-running: the Sub-agents panel is open beside the buoy session, with a row for each agent.
function dockRunning(root: HTMLElement): boolean {
  return (
    root.querySelector("aside.rpanel")?.getAttribute("aria-label") === "Sub-agents" &&
    root.querySelectorAll(".rpanel .plist li.row.live").length === 2
  );
}

// dockBackground is Page/dock-background: the Left running panel is open beside the buoy session, with its grey rows.
function dockBackground(root: HTMLElement): boolean {
  return (
    root.querySelector("aside.rpanel")?.getAttribute("aria-label") === "Left running" &&
    root.querySelectorAll(".rpanel .plist li.row.quiet").length === 1
  );
}

// The words of the New session stories, which a prompt row's start form takes as its first message, and the ask
// failure the 90-second story draws.
const rosterWords = "roster page tidepool";
const askFailure = "The search did not finish: the server did not answer in 90 seconds.";

// newPanel is New session's panel, or null when it is not open.
function newPanel(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>('[role="dialog"][aria-label="New session"]');
}

// fieldText is the text in New session's field, or null when the panel is not open.
function fieldText(root: HTMLElement): string | null {
  return newPanel(root)?.querySelector<HTMLInputElement>('input[aria-label="New session"]')?.value ?? null;
}

// assertNoFailure fails unless the story drew no failure element, so no call it made was refused.
function assertNoFailure(root: HTMLElement): void {
  assertNone(root.querySelector("[data-story-failure]"), "the story drew a failure element");
}

// settle waits until check holds, one turn at a time inside act, so the renders and effects it lets through are applied.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// withStory mounts story into a fresh DOM at width, the wide width when none is given, waits until drawn holds on its root,
// then runs check. The story is unmounted and its DOM removed when the test ends, or as soon as the test's signal aborts.
// The test must call mockTimers first.
async function withStory(
  t: TestContext,
  story: () => ComponentChild,
  drawn: (root: HTMLElement) => boolean,
  check: (root: HTMLElement) => void,
  width = wideWidth,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const root = document.createElement("div");
  document.body.append(root);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, root);
    });
    root.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(story(), root);
    });
    await settle(t.signal, () => drawn(root), "the story's page");
    check(root);
  } finally {
    await teardown();
  }
}

test("FixtureTransport answers Epics.List, Epics.Get, Panes.List and Host.Status from the slices", async () => {
  const fixture = createFixtureTransport();
  const list = await fixture.clients.epics.list({});
  assert.ok(equals(EpicsListResponseSchema, list, shippedSlices.epics), "Epics.List answers the epic list");
  const detail = await fixture.clients.epics.get({ id: "lighthouse/rota-export" });
  const wanted = shippedSlices.gets.get("lighthouse/rota-export");
  assert.ok(wanted !== undefined && equals(EpicsGetResponseSchema, detail, wanted), "Epics.Get answers the epic's detail");
  const panes = await fixture.clients.panes.list({});
  assert.ok(equals(PanesListResponseSchema, panes, shippedSlices.panes), "Panes.List answers the pane list");
  const host = await fixture.clients.host.status({});
  assert.ok(equals(HostStatusResponseSchema, host, shippedSlices.host), "Host.Status answers the host");
  assert.deepEqual([...fixture.unserved], [], "no call of these was refused");
});

test("FixtureTransport answers an unknown epic's Epics.Get with NotFound, and records no refusal", async () => {
  const fixture = createFixtureTransport();
  const error = await refusal(fixture.clients.epics.get({ id: "nobody/nothing" }));
  assert.equal(error.code, Code.NotFound);
  assert.deepEqual([...fixture.unserved], [], "a NotFound answer is no refused call");
});

test("FixtureTransport absorbs Epics.Pause: it answers an empty message, and records no call", async () => {
  const fixture = createFixtureTransport();
  const answer = await fixture.clients.epics.pause({ id: "lighthouse/rota-export" });
  assert.deepEqual(toJson(EpicsPauseResponseSchema, answer), {}, "the answer is empty");
  assert.deepEqual([...fixture.unserved], [], "the write is not recorded");
});

test("FixtureTransport records an unserved Picker call, tells each listener until it unregisters, and fails Unimplemented", async () => {
  const fixture = createFixtureTransport();
  const heard: string[] = [];
  const stop = fixture.onUnserved((procedure) => heard.push(procedure));
  const error = await refusal(fixture.clients.picker.ask({}));
  assert.equal(error.code, Code.Unimplemented);
  assert.deepEqual([...fixture.unserved], ["orrery.v1.Picker/Ask"], "the call is recorded by its procedure");
  assert.deepEqual(heard, ["orrery.v1.Picker/Ask"], "the listener is told of it");
  stop();
  await refusal(fixture.clients.picker.run({}));
  assert.deepEqual(heard, ["orrery.v1.Picker/Ask"], "a listener that unregistered hears no more");
});

test("FixtureTransport answers Picker.Ask from its slice for the words, and refuses words with no slice", async () => {
  const jump = { token: "tok-jump", kind: "jump", dir: "/work/api", pane: "%3", label: "auth refactor", note: "waiting on you", prompt: false };
  const fixture = createFixtureTransport({ ...shippedSlices, asks: new Map([["auth", { rows: [jump] }]]) });
  const reply = await fixture.clients.picker.ask({ text: "auth" });
  assert.ok(equals(AskResponseSchema, reply, create(AskResponseSchema, { rows: [jump] })), "Picker.Ask answers the slice's rows");
  assert.deepEqual([...fixture.unserved], [], "the answered call is no refused call");
  const error = await refusal(fixture.clients.picker.ask({ text: "harbour pilot" }));
  assert.equal(error.code, Code.Unimplemented);
  assert.deepEqual([...fixture.unserved], ["orrery.v1.Picker/Ask"], "words with no slice are refused and recorded");
});

test("FixtureTransport holds a Picker.Ask its slice holds open until the call aborts, and the abort ends it", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, asks: new Map([["roster page tidepool", { held: true }]]) });
  const controller = new AbortController();
  const call = fixture.clients.picker.ask({ text: "roster page tidepool" }, { signal: controller.signal });
  assert.equal(await Promise.race([call.then(() => "answered", () => "failed"), turn().then(() => "held")]), "held", "the call is not answered");
  controller.abort();
  const error = await refusal(call);
  assert.equal(error.code, Code.Canceled, "the abort ends the call");
  assert.deepEqual([...fixture.unserved], [], "a held call is no refused call");
});

test("FixtureTransport fails a Picker.Ask its slice holds a failure for with that code, and records no refusal", async () => {
  const fixture = createFixtureTransport({
    ...shippedSlices,
    asks: new Map([["roster page tidepool", { failure: Code.DeadlineExceeded }]]),
  });
  const error = await refusal(fixture.clients.picker.ask({ text: "roster page tidepool" }));
  assert.equal(error.code, Code.DeadlineExceeded);
  assert.deepEqual([...fixture.unserved], [], "a failure from the slice is no refused call");
});

test("EpicCommands: a held Epics.Get stays open until its call aborts, while another epic's Get answers", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, heldGets: new Set(["lighthouse/rota-export"]) });
  const controller = new AbortController();
  const held = fixture.clients.epics.get({ id: "lighthouse/rota-export" }, { signal: controller.signal });
  assert.equal(await Promise.race([held.then(() => "answered", () => "failed"), turn().then(() => "held")]), "held", "the held epic's Get is not answered");
  const other = await fixture.clients.epics.get({ id: "tidepool/tide-alerts" });
  const wanted = shippedSlices.gets.get("tidepool/tide-alerts");
  assert.ok(wanted !== undefined && equals(EpicsGetResponseSchema, other, wanted), "another epic's Get is answered");
  controller.abort();
  const error = await refusal(held);
  assert.equal(error.code, Code.Canceled, "the abort ends the held Get");
  assert.deepEqual([...fixture.unserved], [], "a held Get is no refused call");
});

test("EpicCommands: a failing pause rejects with Unavailable and its reason, while resume still answers", async () => {
  const reason = "the halyard did not take the pause";
  const fixture = createFixtureTransport({ ...shippedSlices, failingEpicCommands: new Map([["pause", reason]]) });
  const error = await refusal(fixture.clients.epics.pause({ id: "lighthouse/rota-export" }));
  assert.equal(error.code, Code.Unavailable, "the failed command is Unavailable");
  assert.equal(error.rawMessage, reason, "the error carries the reason");
  const resumed = await fixture.clients.epics.resume({ id: "lighthouse/rota-export" });
  assert.deepEqual(toJson(EpicsResumeResponseSchema, resumed), {}, "resume still answers with an empty message");
  assert.deepEqual([...fixture.unserved], [], "a failed command is no refused call");
});

test("EpicCommands: with the shipped slices every epic command answers with an empty message", async () => {
  const fixture = createFixtureTransport();
  const id = "lighthouse/rota-export";
  assert.deepEqual(toJson(EpicsPauseResponseSchema, await fixture.clients.epics.pause({ id })), {}, "pause answers");
  assert.deepEqual(toJson(EpicsResumeResponseSchema, await fixture.clients.epics.resume({ id })), {}, "resume answers");
  assert.deepEqual(toJson(EpicsStopResponseSchema, await fixture.clients.epics.stop({ id })), {}, "stop answers");
  assert.deepEqual(toJson(EpicsRetryResponseSchema, await fixture.clients.epics.retry({ id })), {}, "retry answers");
  assert.deepEqual([...fixture.unserved], [], "no epic command is refused");
});

test("FixtureTransport: a pane stream sends its frame and holds open, a reconnect gets no frame, and an abort ends the wait", async () => {
  const fixture = createFixtureTransport();
  const first = new AbortController();
  const frames = fixture.clients.panes.watchConversation({ pane: "%11" }, { signal: first.signal })[Symbol.asyncIterator]();
  const frame = await frames.next();
  assert.ok(!frame.done, "the stream sends its frame");
  const wanted = shippedSlices.paneStreams.get("%11");
  assert.ok(wanted !== undefined && equals(WatchConversationResponseSchema, frame.value, wanted), "the frame is pane %11's");
  const after = frames.next();
  assert.equal(await Promise.race([after.then(() => "sent"), turn().then(() => "held")]), "held", "the stream sends no more");

  // A reconnect names a transcript, so it gets no frame, and it holds open until its own call aborts.
  const reconnect = new AbortController();
  const again = fixture.clients.panes
    .watchConversation({ pane: "%11", transcript: "%11.jsonl" }, { signal: reconnect.signal })
    [Symbol.asyncIterator]();
  const reconnected = again.next();
  assert.equal(await Promise.race([reconnected.then(() => "sent"), turn().then(() => "held")]), "held", "a reconnect sends no frame");

  first.abort();
  assert.equal(await after.then(() => "ended", () => "aborted"), "aborted", "the abort ends the wait");
  reconnect.abort();
  assert.equal(await reconnected.then(() => "ended", () => "aborted"), "aborted", "the reconnect's abort ends its wait");
});

test("FixtureTransport: app/attention draws the attention rows", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.attention,
    (root) => listView(root) === "attention",
    (root) => {
      assertNoFailure(root);
      assert.equal(listView(root), "attention");
    },
  );
});

test("FixtureTransport: app/project draws the project rows", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.project,
    (root) => listView(root) === "project",
    (root) => {
      assertNoFailure(root);
      assert.equal(listView(root), "project");
    },
  );
});

test("FixtureTransport: app/archived draws the archived rows, with the Archived section and no rota-export line", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.archived,
    (root) => listView(root) === "archived",
    (root) => {
      assertNoFailure(root);
      assert.equal(listView(root), "archived");
      const titles = [...root.querySelectorAll(".rows .title")].map((title) => title.textContent);
      assert.ok(titles.includes("Archived"), "the Archived section title is drawn");
      assertNone(root.querySelector('[data-key="epic:lighthouse/rota-export"]'), "the rota-export line is not archived");
    },
  );
});

test("FixtureTransport: app/session draws the headless session's row as the cursor's row", (t) => {
  mockTimers(t, ["setTimeout"]);
  const cursorRow = '.b-row.cur[data-key="session:4be0c6d2-8f3a-4c71-9e25-ephemeris001"]';
  return withStory(
    t,
    App.session,
    (root) => root.querySelector(cursorRow) !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(cursorRow) !== null, "the headless session is the cursor's row");
    },
  );
});

test("FixtureTransport: app/pane draws pane %13's row as the cursor's row", (t) => {
  mockTimers(t, ["setTimeout"]);
  const cursorRow = '.b-row.cur[data-key="pane:halyard:%13"]';
  return withStory(
    t,
    App.pane,
    (root) => root.querySelector(cursorRow) !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(cursorRow) !== null, "pane %13 is the cursor's row");
    },
  );
});

test("FixtureTransport: app/step draws a step reader, with the worker step as the cursor's entry", (t) => {
  mockTimers(t, ["setTimeout"]);
  const cursorStep = '.b-row.step.cur[data-key="step:st-rota-t2-impl"]';
  return withStory(
    t,
    App.step,
    (root) => root.querySelector(cursorStep) !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(cursorStep) !== null, "the worker step is the cursor's entry");
    },
  );
});

test("FixtureTransport: app/epic draws the epic page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.epic,
    (root) => root.querySelector(".epic-page") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(".epic-page") !== null, "the epic page is drawn");
    },
  );
});

test("FixtureTransport: app/contract draws the rota-export contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.contract,
    (root) => root.querySelector("article.contract h2")?.textContent === "rota-export",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, "rota-export");
    },
  );
});

test("FixtureTransport: app/phase draws the phase output", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.phase,
    (root) => root.querySelector(".phase-output .phase-row.open") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(".phase-output .phase-row.open") !== null, "the phase output's open row is drawn");
    },
  );
});

test("FixtureTransport: app/worker-session draws the worker session", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App["worker-session"],
    (root) => root.querySelector(".worker-session") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(".worker-session") !== null, "the worker session is drawn");
    },
  );
});

test("FixtureTransport: page/inbox-home, page/convo-working and page/ask-session each draw their own state, and fail the other two predicates", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const predicates = [
    ["inbox-home", inboxHome],
    ["convo-working", convoWorking],
    ["ask-session", askSession],
  ] as const;
  for (const [state, own] of predicates) {
    await withStory(t, Page[state], own, (root) => {
      assertNoFailure(root);
      for (const [other, predicate] of predicates) {
        assert.equal(predicate(root), other === state, `${state}: the ${other} predicate`);
      }
    });
  }
});

test("FixtureTransport: page/epic-question draws the open decision on the epic page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-question"],
    epicQuestion,
    (root) => {
      assertNoFailure(root);
      assert.ok(epicQuestion(root), "Q1 is drawn, with no Talk menu");
    },
  );
});

test("FixtureTransport: page/epic-shipped draws the released report", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-shipped"],
    (root) => root.querySelector(".proof.report .outcome")?.textContent === "released",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".proof.report .outcome")?.textContent, "released");
    },
  );
});

test("FixtureTransport: page/contract-to-accept draws the tide-alerts contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-to-accept"],
    (root) => root.querySelector("article.contract h2")?.textContent === "tide-alerts",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, "tide-alerts");
    },
  );
});

test("FixtureTransport: page/contract-accepted draws the rota-export contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-accepted"],
    (root) => root.querySelector("article.contract h2")?.textContent === "rota-export",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, "rota-export");
    },
  );
});

test("FixtureTransport: page/phase-implement draws the checking phase's output", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["phase-implement"],
    (root) => root.querySelector(".phase-output .phase-row.open .phase-name")?.textContent === "checking",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".phase-output .phase-row.open .phase-name")?.textContent, "checking");
    },
  );
});

test("FixtureTransport: page/worker-running draws the worker session", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["worker-running"],
    (root) => root.querySelector(".worker-session") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(".worker-session") !== null, "the worker session is drawn");
    },
  );
});

test("FixtureTransport: page/epic-talk draws the epic panel over the planning conversation, under 1250px wide", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-talk"],
    epicTalk,
    (root) => {
      assertNoFailure(root);
      assert.ok(epicTalk(root), "the epic panel lies over the planning conversation");
    },
    overlayWidth,
  );
});

test("FixtureTransport: page/work-rail draws the buoy session's rail with its three groups and no panel", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["work-rail"],
    workRail,
    (root) => {
      assertNoFailure(root);
      assert.ok(workRail(root), "the rail draws epic, agents and running, with no panel open");
    },
  );
});

test("FixtureTransport: page/work-rail-group draws pane %22's picker of four, opened by its dots tile", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["work-rail-group"],
    workRailGroup,
    (root) => {
      assertNoFailure(root);
      assert.ok(workRailGroup(root), "the picker is open with four rows");
    },
  );
});

test("FixtureTransport: page/work-empty draws pane %13's rail with no tile", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["work-empty"],
    workEmpty,
    (root) => {
      assertNoFailure(root);
      assert.ok(workEmpty(root), "the rail is drawn with no tile");
    },
  );
});

test("FixtureTransport: page/dock-running draws the Sub-agents panel with a row for each agent", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["dock-running"],
    dockRunning,
    (root) => {
      assertNoFailure(root);
      assert.ok(dockRunning(root), "the Sub-agents panel has its two rows");
    },
  );
});

test("FixtureTransport: page/dock-background draws the Left running panel with its grey rows", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["dock-background"],
    dockBackground,
    (root) => {
      assertNoFailure(root);
      assert.ok(dockBackground(root), "the Left running panel has its one grey row");
    },
  );
});

test("FixtureTransport: page/epic-handover draws the Talk menu", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-handover"],
    epicHandover,
    (root) => {
      assertNoFailure(root);
      assert.ok(epicHandover(root), "the Talk menu is open");
    },
  );
});

test("FixtureTransport: page/epic-question, page/epic-handover and page/epic-talk each draw their own state, and fail the other two predicates", async (t) => {
  mockTimers(t, ["setTimeout"]);
  // Each state is drawn at the width round 6 draws it at: epic-talk over the conversation, the others beside it.
  const predicates = [
    ["epic-question", epicQuestion, wideWidth],
    ["epic-handover", epicHandover, wideWidth],
    ["epic-talk", epicTalk, overlayWidth],
  ] as const;
  for (const [state, own, width] of predicates) {
    await withStory(
      t,
      Page[state],
      own,
      (root) => {
        assertNoFailure(root);
        for (const [other, predicate] of predicates) {
          assert.equal(predicate(root), other === state, `${state}: the ${other} predicate`);
        }
      },
      width,
    );
  }
});

test("FixtureTransport: page's keys are exactly the 23 catalog names of its scenario states", () => {
  assert.deepEqual(Object.keys(Page).map((state) => storyName("Page", state)).sort(), [
    "page/ask-session",
    "page/contract-accepted",
    "page/contract-to-accept",
    "page/convo-working",
    "page/dock-background",
    "page/dock-running",
    "page/epic-handover",
    "page/epic-question",
    "page/epic-shipped",
    "page/epic-talk",
    "page/inbox-home",
    "page/keys",
    "page/new-empty",
    "page/new-error",
    "page/new-form",
    "page/new-none",
    "page/new-results",
    "page/new-working",
    "page/phase-implement",
    "page/work-empty",
    "page/work-rail",
    "page/work-rail-group",
    "page/worker-running",
  ]);
});

test("FixtureTransport: page/new-empty draws New session with its field empty, its hint, and no result", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-empty"],
    (root) => newPanel(root) !== null && fieldText(root) === "" && (newPanel(root)?.textContent ?? "").includes("Name a directory"),
    (root) => {
      assertNoFailure(root);
      assert.equal(newPanel(root)?.querySelectorAll(".pick-row").length, 0, "no result is drawn");
    },
  );
});

test("FixtureTransport: page/new-working draws New session with the words typed and the ask waiting", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-working"],
    (root) => fieldText(root) === rosterWords && newPanel(root)?.querySelector(".count")?.textContent === "asking…",
    (root) => {
      assertNoFailure(root);
      assert.equal(newPanel(root)?.querySelectorAll(".pick-row").length, 0, "no result is drawn while the ask runs");
    },
  );
});

test("FixtureTransport: page/new-results draws the four results, each with its word, and the chip on the directory that starts with the words", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-results"],
    (root) => newPanel(root)?.querySelectorAll(".pick-row").length === 4,
    (root) => {
      assertNoFailure(root);
      const panel = newPanel(root);
      assert.deepEqual(
        [...(panel?.querySelectorAll(".pick-row .k") ?? [])].map((word) => word.textContent),
        ["go to", "resume", "start in", "start in"],
        "each row leads with its kind's word",
      );
      const rows = [...(panel?.querySelectorAll(".pick-row") ?? [])];
      assert.ok(rows[0]?.classList.contains("cur"), "the first row is current");
      assert.deepEqual(
        rows.map((row) => row.querySelector(".chip.live") !== null),
        [false, false, true, false],
        "only the directory that starts with the words has the chip",
      );
    },
  );
});

test("FixtureTransport: page/new-form draws the start form for the directory, its first message the words asked", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-form"],
    (root) => newPanel(root)?.querySelector<HTMLTextAreaElement>('textarea[aria-label="message"]')?.value === rosterWords,
    (root) => {
      assertNoFailure(root);
      const panel = newPanel(root);
      assert.equal(panel?.querySelector<HTMLInputElement>('input[aria-label="Directory"]')?.value, "/home/owner/code/tidepool");
      assert.equal(panel?.querySelector(".pick-row"), null, "the results give way to the form");
    },
  );
});

test("FixtureTransport: page/new-none says nothing was found for the words", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-none"],
    (root) => (newPanel(root)?.querySelector(".inote")?.textContent ?? "").includes("Nothing found"),
    (root) => {
      assertNoFailure(root);
      assert.equal(fieldText(root), "harbour pilot", "the field keeps the words");
    },
  );
});

test("FixtureTransport: page/new-error draws the ask's reason, and the field keeps the words", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["new-error"],
    (root) => newPanel(root)?.querySelector(".inote.error")?.textContent === askFailure,
    (root) => {
      assertNoFailure(root);
      assert.equal(fieldText(root), rosterWords, "the field keeps the words");
    },
  );
});

test("FixtureTransport: page/keys draws the key list over the page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page.keys,
    (root) => root.querySelector('[role="dialog"][aria-label="Keys"]') !== null,
    (root) => {
      assertNoFailure(root);
    },
  );
});

test("FixtureTransport: a story whose after rejects draws no failure and no error, and reports the rejection once", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const reports: unknown[][] = [];
  t.mock.method(console, "error", (...args: unknown[]) => {
    reports.push(args);
  });
  const story = () =>
    h(PageStory, {
      address: attention,
      after: async () => {
        throw new Error("the after step broke");
      },
    });
  await withStory(
    t,
    story,
    () => reports.length > 0,
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector(".error"), "App drew no error");
      assert.deepEqual(reports, [["story #/attention: the after step broke"]], "the rejection is reported once");
    },
  );
});
