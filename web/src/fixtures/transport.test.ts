import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { equals, toJson } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { type ComponentChild, h, render } from "preact";
import { act } from "preact/test-utils";

import { App, Page } from "../app.stories";
import { PageStory } from "../catalog/pagestory";
import { storyName } from "../catalog/story";
import type { Address } from "../router";
import { EpicsGetResponseSchema, EpicsListResponseSchema, EpicsPauseResponseSchema } from "../gen/orrery/v1/epics_pb";
import {
  HostStatusResponseSchema,
  PanesListResponseSchema,
  WatchConversationResponseSchema,
} from "../gen/orrery/v1/orrery_pb";
import { mockTimers } from "../testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { shippedSlices } from "./slices";
import { createFixtureTransport } from "./transport";

// wideWidth is the window width the stories draw at. It is past the 760px the wide layout starts at, so the inbox is
// docked or hidden as its state has it, not stacked under the page.
const wideWidth = 1280;

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

// The open decision of the rota-export epic, and the planning conversation the talk dock draws beside the epic page.
// The Talk menu and the dock are drawn on the epic page only in the talk states, so the epic's own state checks neither.
const rotaQuestion = "Bump the embedded tz database?";
const rotaPrompt = "Plan the rota export as an epic.";
const talkDock = 'aside.dock[aria-label="talk"]';

// listHome is Page/list-home: the attention rows, with the inbox docked beside the cursor's reader on pane %12.
function listHome(root: HTMLElement): boolean {
  return listView(root) === "attention" && inboxState(root) === "docked";
}

// lampWorking is Page/session-lamp-working: the inbox hidden under the reader of pane %11.
function lampWorking(root: HTMLElement): boolean {
  return inboxState(root) === "hidden" && (root.textContent ?? "").includes(lampFragment);
}

// beaconQuestion is Page/session-beacon-question: the inbox hidden under the reader of pane %12.
function beaconQuestion(root: HTMLElement): boolean {
  return inboxState(root) === "hidden" && (root.textContent ?? "").includes(beaconPrompt);
}

// rotaDecision is Page/epic-rota-decision: Q1 is drawn in the now block, with no Talk menu and no talk dock drawn.
function rotaDecision(root: HTMLElement): boolean {
  return (
    root.querySelector(".now-block .answer .question")?.textContent.includes(rotaQuestion) === true &&
    root.querySelector("ul.talk") === null &&
    root.querySelector(talkDock) === null
  );
}

// talkMenu is Page/talk-rota-menu: the Talk menu is drawn open, with no talk dock beside the epic page.
function talkMenu(root: HTMLElement): boolean {
  return root.querySelector("ul.talk") !== null && root.querySelector(talkDock) === null;
}

// talkDocked is Page/talk-rota-rotaplan-docked: the talk dock is drawn with the planning conversation in it.
function talkDocked(root: HTMLElement): boolean {
  return root.querySelector(talkDock)?.textContent.includes(rotaPrompt) === true;
}

// assertNoFailure fails unless the story drew no failure element, so no call it made was refused.
function assertNoFailure(root: HTMLElement): void {
  assertNone(root.querySelector("[data-story-failure]"), "the story drew a failure element");
}

// settle waits until check holds, one turn at a time inside act, so the renders and effects it lets through are applied.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// withStory mounts story into a fresh DOM at the wide width, waits until drawn holds on its root, then runs check. The
// story is unmounted and its DOM removed when the test ends, or as soon as the test's signal aborts. The test must call
// mockTimers first.
async function withStory(
  t: TestContext,
  story: () => ComponentChild,
  drawn: (root: HTMLElement) => boolean,
  check: (root: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = wideWidth;
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

test("FixtureTransport: page/list-home, page/session-lamp-working and page/session-beacon-question each draw their own state, and fail the other two predicates", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const predicates = [
    ["list-home", listHome],
    ["session-lamp-working", lampWorking],
    ["session-beacon-question", beaconQuestion],
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

test("FixtureTransport: page/epic-rota-decision draws the open decision on the epic page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-rota-decision"],
    rotaDecision,
    (root) => {
      assertNoFailure(root);
      assert.ok(rotaDecision(root), "Q1 is drawn, with no Talk menu or talk dock");
    },
  );
});

test("FixtureTransport: page/epic-holder-released draws the released report", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-holder-released"],
    (root) => root.querySelector(".proof.report .outcome")?.textContent === "released",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".proof.report .outcome")?.textContent, "released");
    },
  );
});

test("FixtureTransport: page/contract-tidealerts-open draws the tide-alerts contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-tidealerts-open"],
    (root) => root.querySelector("article.contract h2")?.textContent === "tide-alerts",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, "tide-alerts");
    },
  );
});

test("FixtureTransport: page/contract-rota-accepted draws the rota-export contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-rota-accepted"],
    (root) => root.querySelector("article.contract h2")?.textContent === "rota-export",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, "rota-export");
    },
  );
});

test("FixtureTransport: page/phase-rota-export-cli-checking draws the checking phase's output", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["phase-rota-export-cli-checking"],
    (root) => root.querySelector(".phase-output .phase-row.open .phase-name")?.textContent === "checking",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".phase-output .phase-row.open .phase-name")?.textContent, "checking");
    },
  );
});

test("FixtureTransport: page/worker-rota-export-cli-implementer draws the worker session", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["worker-rota-export-cli-implementer"],
    (root) => root.querySelector(".worker-session") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelector(".worker-session") !== null, "the worker session is drawn");
    },
  );
});

test("FixtureTransport: page/talk-rota-rotaplan-docked draws the planning conversation docked beside the epic page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["talk-rota-rotaplan-docked"],
    talkDocked,
    (root) => {
      assertNoFailure(root);
      assert.ok(talkDocked(root), "the planning conversation is docked");
    },
  );
});

test("FixtureTransport: page/talk-rota-menu draws the Talk menu", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["talk-rota-menu"],
    talkMenu,
    (root) => {
      assertNoFailure(root);
      assert.ok(talkMenu(root), "the Talk menu is open, with no talk dock");
    },
  );
});

test("FixtureTransport: page/epic-rota-decision, page/talk-rota-menu and page/talk-rota-rotaplan-docked each draw their own state, and fail the other two predicates", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const predicates = [
    ["epic-rota-decision", rotaDecision],
    ["talk-rota-menu", talkMenu],
    ["talk-rota-rotaplan-docked", talkDocked],
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

test("FixtureTransport: page's keys are exactly the 11 catalog names of its scenario states", () => {
  assert.deepEqual(Object.keys(Page).map((state) => storyName("Page", state)).sort(), [
    "page/contract-rota-accepted",
    "page/contract-tidealerts-open",
    "page/epic-holder-released",
    "page/epic-rota-decision",
    "page/list-home",
    "page/phase-rota-export-cli-checking",
    "page/session-beacon-question",
    "page/session-lamp-working",
    "page/talk-rota-menu",
    "page/talk-rota-rotaplan-docked",
    "page/worker-rota-export-cli-implementer",
  ]);
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
