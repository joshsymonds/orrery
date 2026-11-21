import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, equals, toJson } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { type ComponentChild, h, render } from "preact";
import { act } from "preact/test-utils";

import { App, Page } from "../app.stories";
import { PageStory } from "../catalog/pagestory";
import { storyName } from "../catalog/story";
import { endedLead } from "../endedbar";
import type { Address } from "../router";
import { Contracts, ContractsGetResponseSchema, ContractsListResponseSchema } from "../gen/orrery/v1/contracts_pb";
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
  SayResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
} from "../gen/orrery/v1/orrery_pb";
import { mockTimers } from "../testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { loadRows } from "../load";
import { turn, until } from "../testwait";
import { at } from "./clock";
import { epicScenarios, shippedSlices } from "./slices";
import { createFixtureTransport } from "./transport";

// wideWidth is the window width the stories draw at unless one names its own. It is past the 760px the wide layout starts
// at, so the inbox is docked or hidden as its state has it, not stacked under the page.
const wideWidth = 1280;

// overlayWidth is the width the epic panel lies over the conversation at: the 1000px round 6 draws epic-talk at, which
// is at 1250px or narrower.
const overlayWidth = 1000;

// phoneWidth is the width the epic page is a phone's: under 760px its commands are under More.
const phoneWidth = 412;

const inbox: Address = { view: "inbox", entry: null };

// refusal resolves to the Connect error a call rejects with, and fails the test when the call is answered.
async function refusal(call: Promise<unknown>): Promise<ConnectError> {
  try {
    await call;
  } catch (error) {
    return ConnectError.from(error);
  }
  assert.fail("the call was answered, not refused");
}

// listView is the view the rows list draws, from its data-list attribute, or null before the list is drawn.
function listView(root: HTMLElement): string | null {
  return root.querySelector(".inbox-rows")?.getAttribute("data-list") ?? null;
}

// inboxState is the state the inbox is drawn in, from the panes' data-inbox attribute.
function inboxState(root: HTMLElement): string | null {
  return root.querySelector(".panes")?.getAttribute("data-inbox") ?? null;
}

// The opening prompt of pane %12 and a fragment of pane %11's unfinished reply. Pane %12's prompt is drawn at home too,
// since the cursor rests on %12, and %11's fragment is drawn only when %11's conversation is.
const beaconPrompt = "Why is the roster page slow for weeks with more than forty shifts?";
const lampFragment = "Full jitter is in";

// The open decision of the rota-export epic, and the planning conversation the epic's first line opens in the reader.
// The steer menu is drawn on the epic page only in the handover state, so the epic's own state checks neither.
const rotaQuestion = "Bump the embedded tz database?";
const rotaPrompt = "Plan the rota export as an epic.";
// tideTitle is the title of tide-alerts' contract body, which the contract record's heading draws.
const tideTitle = "Alert the harbour office within a minute when a published tide table changes.";

// inboxHome is Page/inbox-home: the inbox rows, with the inbox docked beside the cursor's reader on pane %12.
function inboxHome(root: HTMLElement): boolean {
  return listView(root) === "inbox" && inboxState(root) === "docked";
}

// convoWorking is Page/convo-working: the inbox at rest as its rail beside the reader of pane %11.
function convoWorking(root: HTMLElement): boolean {
  return inboxState(root) === "rail" && (root.textContent ?? "").includes(lampFragment);
}

// askSession is Page/ask-session: the inbox at rest as its rail beside the reader of pane %12.
function askSession(root: HTMLElement): boolean {
  return inboxState(root) === "rail" && (root.textContent ?? "").includes(beaconPrompt);
}

// askOf is the Ask the page draws, or null when none is drawn. The answered and settled lines are not Asks.
function askOf(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>("section.ask");
}

// askText is the text of the element of the Ask that selector names, or null when the Ask or that element is not drawn.
function askText(root: HTMLElement, selector: string): string | null {
  return askOf(root)?.querySelector(selector)?.textContent ?? null;
}

// optionNames is the name of each option the Ask draws, in order.
function optionNames(root: HTMLElement): string[] {
  return [...(askOf(root)?.querySelectorAll(".opts .opt .nm") ?? [])].map((name) => name.textContent ?? "");
}

// epicQuestion is Page/epic-question: Q1 is drawn in the Now section's Ask, with no steer menu drawn, and the epic panel
// beside the conversation rather than over it, which is epic-talk's layout.
function epicQuestion(root: HTMLElement): boolean {
  return (
    root.querySelector(".epic-page section.now .ask .q")?.textContent.includes(rotaQuestion) === true &&
    root.querySelector(".steer-menu") === null &&
    root.querySelector(".panes.overlay") === null
  );
}

// epicHandover is Page/epic-handover: the steer menu is drawn open.
function epicHandover(root: HTMLElement): boolean {
  return root.querySelector(".steer-menu") !== null;
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

// withPage mounts story into a fresh DOM at width, the wide width when none is given, waits until drawn holds on its root,
// then runs check. prepare runs once the fresh DOM is the global one, before the story mounts. The story is unmounted and
// its DOM removed when the test ends, or as soon as the test's signal aborts. The test must call mockTimers first.
async function withPage(
  t: TestContext,
  story: () => ComponentChild,
  drawn: (root: HTMLElement) => boolean,
  check: (root: HTMLElement) => void,
  width = wideWidth,
  prepare: () => void = () => {},
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  prepare();
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

// withStory is withPage for a story whose first load has answered: drawn holds only once PageStory marks its root
// loaded, so the check sees the rows and epics the fixtures answer with, not the empty list drawn before the load.
function withStory(
  t: TestContext,
  story: () => ComponentChild,
  drawn: (root: HTMLElement) => boolean,
  check: (root: HTMLElement) => void,
  width = wideWidth,
  prepare: () => void = () => {},
): Promise<void> {
  return withPage(t, story, (root) => root.querySelector("[data-loaded]") !== null && drawn(root), check, width, prepare);
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

test("FixtureTransport absorbs Control.Say when the slices set no say: it answers an empty message, and records no call", async () => {
  const fixture = createFixtureTransport();
  const answer = await fixture.clients.control.say({ id: "fixture-new-session", text: "hello" });
  assert.deepEqual(toJson(SayResponseSchema, answer), {}, "the answer is empty");
  assert.deepEqual([...fixture.unserved], [], "the write is not recorded");
});

test("FixtureTransport refuses Control.Say with FailedPrecondition when the slices say refused, and records no call", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, say: "refused" });
  const error = await refusal(fixture.clients.control.say({ id: "fixture-new-session", text: "hello" }));
  assert.equal(error.code, Code.FailedPrecondition);
  assert.equal(error.rawMessage, "the session is not taking messages");
  assert.deepEqual([...fixture.unserved], [], "a refused write is no unserved call");
});

test("FixtureTransport fails Control.Say with Unavailable when the slices say unanswered, and records no call", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, say: "unanswered" });
  const error = await refusal(fixture.clients.control.say({ id: "fixture-new-session", text: "hello" }));
  assert.equal(error.code, Code.Unavailable);
  assert.equal(error.rawMessage, "no answer");
  assert.deepEqual([...fixture.unserved], [], "an unanswered write is no unserved call");
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

test("FixtureTransport answers Contracts.List with tide-alerts proposed by sess-tide-16", async () => {
  const fixture = createFixtureTransport();
  const contracts = createClient(Contracts, fixture.transport);
  const list = await contracts.list({});
  assert.ok(equals(ContractsListResponseSchema, list, shippedSlices.contracts), "Contracts.List answers the contract list");
  assert.equal(list.contracts.length, 1, "one contract is listed");
  const [tide] = list.contracts;
  assert.deepEqual(
    [tide.id, tide.repository, tide.title, tide.care, tide.state, tide.version, tide.openDecisions, tide.conversation, tide.created, tide.changed],
    ["tidepool/tide-alerts", "/home/owner/code/tidepool", "Tide alerts for the harbour office", "serious", "proposed", 2n, 0n, "sess-tide-16", at("@13:58"), at("@14:15")],
  );
  assert.deepEqual([...fixture.unserved], [], "the list is no refused call");
});

test("FixtureTransport answers Contracts.Get with the record its contractGets holds for the id", async () => {
  const fixture = createFixtureTransport();
  const contracts = createClient(Contracts, fixture.transport);
  const record = await contracts.get({ id: "tidepool/tide-alerts" });
  assert.ok(
    equals(ContractsGetResponseSchema, record, shippedSlices.contractGets["tidepool/tide-alerts"]),
    "Contracts.Get answers the record its slices hold for the id",
  );
  assert.equal(record.contract?.id, "tidepool/tide-alerts", "the record is the contract asked for");
  assert.deepEqual([...fixture.unserved], [], "the get is no refused call");
});

test("FixtureTransport fails Contracts.Get of an id with no record with NotFound, and records no refusal", async () => {
  const fixture = createFixtureTransport();
  const contracts = createClient(Contracts, fixture.transport);
  const error = await refusal(contracts.get({ id: "tidepool/no-such-contract" }));
  assert.equal(error.code, Code.NotFound);
  assert.deepEqual([...fixture.unserved], [], "a NotFound is no refused call");
});

test("FixtureTransport refuses the Contracts calls its slices do not serve, and records each", async () => {
  const fixture = createFixtureTransport();
  const contracts = createClient(Contracts, fixture.transport);
  const error = await refusal(contracts.accept({ id: "tidepool/tide-alerts" }));
  assert.equal(error.code, Code.Unimplemented);
  assert.deepEqual([...fixture.unserved], ["orrery.v1.Contracts/Accept"], "the refused call is recorded by its procedure");
});

test("FixtureTransport fails a procedure its slices name with Unavailable, records no refusal, and serves the others", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, fail: new Set(["orrery.v1.Panes/List"]) });
  const error = await refusal(fixture.clients.panes.list({}));
  assert.equal(error.code, Code.Unavailable);
  assert.deepEqual([...fixture.unserved], [], "a failure is no refused call");
  const list = await fixture.clients.epics.list({});
  assert.ok(equals(EpicsListResponseSchema, list, shippedSlices.epics), "the other procedures are answered as before");
});

test("FixtureTransport holds a procedure its slices name until its call aborts, and the abort ends the call", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, hold: new Set(["orrery.v1.Contracts/List"]) });
  const contracts = createClient(Contracts, fixture.transport);
  const controller = new AbortController();
  const call = contracts.list({}, { signal: controller.signal });
  assert.equal(await Promise.race([call.then(() => "answered", () => "failed"), turn().then(() => "held")]), "held", "the call is not answered");
  controller.abort();
  const error = await refusal(call);
  assert.equal(error.code, Code.Canceled, "the abort ends the call");
  assert.deepEqual([...fixture.unserved], [], "a held call is no refused call");
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

test("FixtureTransport answers Control.Interrupt with the texts the slices give, in order, and records no call", async () => {
  const fixture = createFixtureTransport({ ...shippedSlices, interrupt: { texts: ["first", "second"] } });
  const answer = await fixture.clients.control.interrupt({ id: "st-rota-t2-impl" });
  assert.deepEqual(answer.texts, ["first", "second"], "the texts come back in order");
  assert.deepEqual([...fixture.unserved], [], "the interrupt is not recorded as unserved");
});

test("FixtureTransport absorbs Control.Interrupt when the slices set no interrupt: it answers no texts, and an open session stream sends no more", async () => {
  const fixture = createFixtureTransport();
  const stream = new AbortController();
  const frames = fixture.clients.sessions
    .watchConversation({ id: "st-rota-t2-impl" }, { signal: stream.signal })
    [Symbol.asyncIterator]();
  const frame = await frames.next();
  assert.ok(!frame.done, "the stream sends its frame");
  const after = frames.next();
  const answer = await fixture.clients.control.interrupt({ id: "st-rota-t2-impl" });
  assert.deepEqual(answer.texts, [], "the interrupt answers no texts");
  assert.equal(await Promise.race([after.then(() => "sent"), turn().then(() => "held")]), "held", "the stream sends no more");
  assert.deepEqual([...fixture.unserved], [], "the interrupt is not recorded as unserved");

  stream.abort();
  assert.equal(await after.then(() => "ended", () => "aborted"), "aborted", "the abort ends the wait");
});

test("FixtureTransport: an open session stream sends the interrupt's then frame once Interrupt is called, and an abort still ends it", async () => {
  const then = create(SessionsWatchConversationResponseSchema, { transcript: "st-rota-t2-impl.jsonl", seq: 2n });
  const fixture = createFixtureTransport({ ...shippedSlices, interrupt: { texts: [], then } });
  const stream = new AbortController();
  const frames = fixture.clients.sessions
    .watchConversation({ id: "st-rota-t2-impl" }, { signal: stream.signal })
    [Symbol.asyncIterator]();
  const frame = await frames.next();
  assert.ok(!frame.done, "the stream sends its frame");
  const wanted = shippedSlices.sessionStreams.get("st-rota-t2-impl");
  assert.ok(wanted !== undefined && equals(SessionsWatchConversationResponseSchema, frame.value, wanted), "the frame is the session's");

  const next = frames.next();
  assert.equal(await Promise.race([next.then(() => "sent"), turn().then(() => "held")]), "held", "the stream sends nothing before the interrupt");
  await fixture.clients.control.interrupt({ id: "st-rota-t2-impl" });
  const sent = await next;
  assert.ok(!sent.done && equals(SessionsWatchConversationResponseSchema, sent.value, then), "the interrupt sends the then frame");

  const after = frames.next();
  assert.equal(await Promise.race([after.then(() => "sent"), turn().then(() => "held")]), "held", "the stream sends no more");
  stream.abort();
  assert.equal(await after.then(() => "ended", () => "aborted"), "aborted", "the abort ends the wait");
});

test("FixtureTransport: app/inbox draws the inbox rows, with the fixture's proposed contract on its epic", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.inbox,
    (root) => listView(root) === "inbox",
    (root) => {
      assertNoFailure(root);
      assert.equal(listView(root), "inbox");
      const tide = root.querySelector('[data-key="epic:tidepool/tide-alerts"]');
      assert.ok(tide?.classList.contains("contract"), "tide-alerts is not drawn as its proposed contract");
      assertNone(root.querySelector('[data-key="epic:almanac/moon-rounding"]'), "moon-rounding is archived, so the inbox holds it not");
      assertNone(root.querySelector('[data-key="epic:tidepool/tide-warmup"]'), "tide-warmup is archived, so the inbox holds it not");
    },
  );
});

test("FixtureTransport: app/archived draws the archived rows, with the Archived head, no rota-export line, and the archived epics under their conversations", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    App.archived,
    (root) => listView(root) === "archived" && root.querySelector('[data-key="epic:almanac/moon-rounding"]') !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(listView(root), "archived");
      assert.equal(root.querySelector(".inbox-attn .name")?.textContent, "Archived", "the Archived head is drawn");
      assertNone(root.querySelector('[data-key="epic:lighthouse/rota-export"]'), "the rota-export line is not archived");
      assert.ok(
        root.querySelector('[data-key="session:e7c4b2a1-5d9f-4e3a-9b61-2a8f0c4d7e13"]'),
        "the archived conversation Moon phase rounding is drawn",
      );
      assert.ok(root.querySelector('[data-key="epic:almanac/moon-rounding"]'), "its archived epic moon-rounding is drawn under it");
      assert.ok(root.querySelector('[data-key="epic:tidepool/tide-warmup"]'), "the archived epic under the warm-up is drawn");
    },
  );
});

test("FixtureTransport: app/session draws the headless session's row as the cursor's row", (t) => {
  mockTimers(t, ["setTimeout"]);
  const cursorRow = '.row.cur[data-key="session:4be0c6d2-8f3a-4c71-9e25-ephemeris001"]';
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
  const cursorRow = '.row.cur[data-key="pane:halyard:%13"]';
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
  const cursorStep = '.row.step.cur[data-key="step:st-rota-t2-impl"]';
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

test("FixtureTransport: page/ask-permission draws the Bash permission's command, with its three answers", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-permission"],
    (root) => askText(root, ".q") === "Run this command?",
    (root) => {
      assertNoFailure(root);
      assert.equal(askText(root, "figure.cmd code"), "git push origin epic/rota-export --force-with-lease", "the command is drawn");
      assert.deepEqual(optionNames(root), ["Yes", "Yes, and don’t ask again", "No"], "the three permission answers are drawn");
    },
  );
});

test("FixtureTransport: page/ask-many draws five options, each with its key, and says the question is 1 of 2", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-many"],
    (root) => askText(root, ".head .cut")?.endsWith("question 1 of 2") === true,
    (root) => {
      assertNoFailure(root);
      assert.equal(optionNames(root).length, 5, "all five options are drawn");
      assert.equal(askOf(root)?.querySelectorAll(".opts .opt kbd").length, 5, "each option has its key cap");
    },
  );
});

test("FixtureTransport: page/ask-sending holds the picked option, and its foot says sending", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-sending"],
    (root) => askText(root, ".foot .inote") === "sending…",
    (root) => {
      assertNoFailure(root);
      assert.equal(
        askOf(root)?.querySelector(".opt.picked .nm")?.textContent,
        "One query per week",
        "option 1 is held",
      );
      assert.equal(askText(root, ".opt.picked .chip"), null, "the held option is not marked recommended");
    },
  );
});

test("FixtureTransport: page/ask-error keeps the Ask open with the refused-answer note, and holds no option", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-error"],
    (root) =>
      askText(root, ".inote.error")?.includes("Not answered: the question changed before your answer arrived.") === true,
    (root) => {
      assertNoFailure(root);
      assert.equal(askOf(root)?.querySelector(".opt.picked"), null, "no option is held once the answer is refused");
      assert.equal(askText(root, ".foot .inote"), null, "no sending foot is drawn");
    },
  );
});

test("FixtureTransport: page/ask-answered folds the Ask to its answered line once the stream drops the question", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-answered"],
    (root) => root.querySelector(".ask.folded") !== null,
    (root) => {
      assertNoFailure(root);
      const line = root.querySelector(".ask.folded");
      assert.deepEqual(
        [...(line?.children ?? [])].map((part) => part.textContent),
        ["You answered", "1", "One query per week", "· Which fix should I make?"],
        "the line says what was answered, by which key, and the question",
      );
      assert.equal(askOf(root), null, "no Ask is drawn open");
    },
  );
});

test("FixtureTransport: page/ask-answered draws the reply, the answered line, then the Edit line", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-answered"],
    (root) => root.querySelector(".ask.folded") !== null,
    (root) => {
      assertNoFailure(root);
      const drawn = [...(root.querySelector(".reader")?.children ?? [])];
      const line = drawn.findIndex((element) => element.matches(".ask.folded"));
      assert.ok(line > 0, "the answered line is drawn after the items");
      assert.ok(
        drawn[line - 1]?.textContent.includes("Each shift row loads its keeper"),
        "the reply that was newest at the press is drawn right before the answered line",
      );
      const edit = drawn.findIndex(
        (element) => element.textContent.includes("Edit") && element.textContent.includes("internal/roster/query.go"),
      );
      assert.ok(edit > line, "the Edit line the stream added is drawn after the answered line");
    },
  );
});

test("FixtureTransport: page/ask-stale says the question has waited five days, and draws Archive this session", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-stale"],
    (root) => askText(root, ".foot .inote") === "Asked 5 days ago and still waiting.",
    (root) => {
      assertNoFailure(root);
      assert.equal(
        askOf(root)?.querySelector(".foot .btn.quiet")?.textContent.includes("Archive this session"),
        true,
        "the archive button is drawn",
      );
      assert.equal(askText(root, ".opts .opt:first-child .chip"), null, "the first option is not marked recommended");
      assert.equal(askOf(root)?.querySelector(".opts .opt:first-child .nm")?.textContent, "Raise the budget line by $100");
    },
  );
});

test("FixtureTransport: page/ask-later draws round 6's whole question under Put off, with Bring it back and its age five days", (t) => {
  mockTimers(t, ["setTimeout"]);
  const question =
    "The AWS fleet won’t launch: the budget pace check puts November spend ahead of today’s line, so it refuses every gate box. How should the gates run?";
  return withStory(
    t,
    Page["ask-later"],
    (root) => askText(root, ".lbl") === "Put off",
    (root) => {
      assertNoFailure(root);
      assert.equal(askText(root, ".q"), question, "the question is round 6's whole question");
      assert.equal(askText(root, ".age"), "· 5d ago", "the age is five days");
      assert.equal(
        askOf(root)?.querySelector(".end")?.textContent.includes("Bring it back"),
        true,
        "Bring it back is drawn",
      );
    },
  );
});

test("FixtureTransport: page/ask-decline opens More, and its menu holds Decline and end the turn", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-decline"],
    (root) => askOf(root)?.querySelector(".menu-item.danger")?.textContent.includes("Decline and end the turn") === true,
    (root) => {
      assertNoFailure(root);
      assert.equal(
        askOf(root)?.querySelector('button[aria-label="More for this question"]')?.getAttribute("aria-expanded"),
        "true",
        "More is open",
      );
    },
  );
});

test("FixtureTransport: page/ask-unanswerable draws the note that the question cannot be answered here, and no option", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["ask-unanswerable"],
    (root) => askText(root, "p.inote") === "This question cannot be answered from here. Reply in words below.",
    (root) => {
      assertNoFailure(root);
      assert.equal(askOf(root)?.querySelector(".opts"), null, "no option is drawn");
    },
  );
});

test("FixtureTransport: page/epic-question draws the open decision on the epic page", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-question"],
    epicQuestion,
    (root) => {
      assertNoFailure(root);
      assert.ok(epicQuestion(root), "Q1 is drawn, with no steer menu");
    },
  );
});

test("FixtureTransport: page/epic-shipped draws the released epic at Report, its track's now stage", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-shipped"],
    (root) => root.querySelector(".track .stage.now .ds")?.textContent === "released",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".track .stage.now .nm")?.textContent, "Report");
      assert.equal(root.querySelector(".track .stage.now .ds")?.textContent, "released");
    },
  );
});

test("FixtureTransport: page/contract-to-accept draws the tide-alerts contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-to-accept"],
    (root) => root.querySelector("article.contract h2")?.textContent === tideTitle,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, tideTitle);
    },
  );
});

test("FixtureTransport: page/contract-accepted draws the tide-alerts contract", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["contract-accepted"],
    (root) => root.querySelector("article.contract h2")?.textContent === tideTitle,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector("article.contract h2")?.textContent, tideTitle);
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

test("FixtureTransport: page/epic-handover draws the steer menu", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-handover"],
    epicHandover,
    (root) => {
      assertNoFailure(root);
      assert.ok(epicHandover(root), "the steer menu is open");
    },
  );
});

// titleRowTexts are the texts of the title row's commands, in order, the buttons of its acts.
function titleRowTexts(root: HTMLElement): string[] {
  return [...root.querySelectorAll<HTMLButtonElement>(".epic-page .title-row .acts button")].map(
    (button) => button.textContent ?? "",
  );
}

test("SteerMenu: page/epic-handover at the desk draws the menu headed Steer rota-export from, with the steerer marked and the project's conversation offered, and no Talk", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-handover"],
    epicHandover,
    (root) => {
      assertNoFailure(root);
      const menu = root.querySelector(".steer-menu");
      assert.equal(menu?.getAttribute("aria-label"), "Steer rota-export from", "the menu is headed by the epic's slug");
      assert.equal(menu?.querySelector(".menu-head")?.textContent, "Steer rota-export from");
      const current = menu?.querySelector(".steer-row.current");
      assert.equal(current?.querySelector(".t")?.textContent, "Planned the rota export, and steers its epic");
      assert.equal(current?.getAttribute("aria-current"), "true", "the steerer is marked current");
      const offered = [...(menu?.querySelectorAll(".steer-row:not(.current):not(.new) .t") ?? [])].map((row) => row.textContent);
      assert.ok(offered.includes("Retry backoff and jitter for the telemetry uploader"), "the project's live conversation is offered");
      assert.equal(menu?.querySelector(".steer-row.new .t")?.textContent, "A new conversation in lighthouse");
      assert.equal(root.querySelector("ul.talk"), null, "the Talk list is gone");
      assert.ok(!titleRowTexts(root).includes("Talk"), "the title row has no Talk command");
    },
    wideWidth,
  );
});

test("SteerMenu: at 412 wide page/epic-handover opens the menu from the foot bar's More, in a dialog", (t) => {
  mockTimers(t, ["setTimeout"]);
  const dialogMenu = (root: HTMLElement) => root.querySelector('[role="dialog"][aria-label="Steer rota-export from"] .steer-menu');
  return withStory(
    t,
    Page["epic-handover"],
    (root) => dialogMenu(root) !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(
        [...root.querySelectorAll(".epic-page .cmdbar button")].some((button) => button.textContent === "More"),
        "the foot bar holds More",
      );
      assert.equal(root.querySelector(".epic-page .title-row .icon"), null, "the title row holds no steering command");
      assert.equal(root.querySelector(".steer-anchor"), null, "no desk anchor is drawn at this width");
      assert.equal(root.querySelector('[role="dialog"][aria-label="rota-export"]'), null, "More's sheet closes once its item opens the menu");
    },
    412,
  );
});

// The lines the rota-export stories draw: the log line of the owner's move, the line the conversation it left ends with,
// and the line the conversation that took it over ends with.
const movedLog = "moved to “Retry backoff and jitter for the telemetry uploader” from “Planned the rota export, and steers its epic”, by you";
const leftLine = "rota-export moved to “Retry backoff and jitter for the telemetry uploader”, by you. Its questions go there now.";
const tookOverLine = "Took over rota-export. Its questions and its ending come here now.";

// logTexts are the texts of the epic page's log lines, in order.
function logTexts(root: HTMLElement): string[] {
  return [...root.querySelectorAll<HTMLElement>(".epic-page .what")].map((line) => line.textContent ?? "");
}

// lastLine is the text of the last event line in the middle conversation, or null when it has none.
function lastLine(root: HTMLElement): string | null {
  const lines = root.querySelectorAll<HTMLElement>('section[aria-label="reader"] .ring-sentence');
  return lines.length === 0 ? null : lines[lines.length - 1].textContent;
}

// railNames are the names the right rail's tiles carry, from their labels: an epic's tile is labelled with its slug.
function railNames(root: HTMLElement): string[] {
  return [...root.querySelectorAll<HTMLElement>(".rail .tile-w")].map((tile) => tile.getAttribute("aria-label") ?? "");
}

test("FixtureTransport: page/epic-moved draws pane %11's conversation in the middle, the epic page in the panel, and the moved line in its log", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-moved"],
    (root) => logTexts(root).includes(movedLog),
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector('section[aria-label="reader"]')?.textContent.includes(lampFragment), true, "pane %11's conversation is in the middle");
      assert.ok(root.querySelector("aside.rpanel .epic-page") !== null, "the epic page is in the panel");
      assert.ok(logTexts(root).includes(movedLog), "the log holds the owner's move");
    },
  );
});

test("FixtureTransport: page/epic-convo-ended draws the stopped steerer's conversation with the ended bar where its dock was, and no dock input", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-convo-ended"],
    (root) => root.querySelector(".ended-bar") !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(
        root.querySelector('section[aria-label="reader"]')?.textContent.includes("Planned the rota export, and steers its epic"),
        true,
        "the stopped steerer's conversation is in the middle",
      );
      assert.equal(root.querySelector(".dock-box.ended")?.getAttribute("data-state"), "stopped", "the steerer is stopped");
      assert.equal(root.querySelector(".ended-bar .ended-lead")?.textContent, endedLead("rota-export"), "the bar's lead says the conversation ended");
      assertNone(root.querySelector('section.read .send > div:not([hidden]) textarea'), "no dock input is drawn");
    },
  );
});

test("FixtureTransport: page/convo-took-over ends with the took-over line, and its rail holds rota-export", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["convo-took-over"],
    (root) => lastLine(root) === tookOverLine,
    (root) => {
      assertNoFailure(root);
      assert.equal(lastLine(root), tookOverLine, "the taker's line ends the conversation");
      assert.ok(railNames(root).some((name) => name.includes("rota-export")), "the rail holds rota-export");
    },
    wideWidth,
  );
});

test("FixtureTransport: page/convo-handed-over ends with the left line naming where rota-export went, and its rail holds no rota-export tile", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["convo-handed-over"],
    (root) => lastLine(root) === leftLine,
    (root) => {
      assertNoFailure(root);
      assert.equal(lastLine(root), leftLine, "the left line ends the conversation");
      assert.ok(root.querySelector("aside.rail") !== null, "the rail is drawn");
      assert.ok(railNames(root).every((name) => !name.includes("rota-export")), "the rail holds no rota-export tile");
    },
    wideWidth,
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

// epicNow is the Now section of the epic page, or null when the page draws none.
function epicNow(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>(".epic-page section.now");
}

// nowSays reports whether the Now section draws text.
function nowSays(root: HTMLElement, text: string): boolean {
  return epicNow(root)?.textContent.includes(text) === true;
}

// pageTitleText is the epic page's title row, or the empty string when none is drawn.
function pageTitleText(root: HTMLElement): string {
  return root.querySelector("section.page .title-row")?.textContent ?? "";
}

// pageCommands are the epic page's commands in its title row.
function pageCommands(root: HTMLElement): string[] {
  return [...root.querySelectorAll<HTMLButtonElement>("section.page .title-row .acts button.act")].map(
    (button) => button.textContent,
  );
}

// stateNote is the line under the title row that a page command leaves, or the empty string when none is drawn.
function stateNote(root: HTMLElement): string {
  return root.querySelector(".page-note")?.textContent ?? "";
}

// epicNoteOf is the one line an epic page draws in place of its body: the loading or could-not-read line, or the empty
// string when the epic's page is drawn.
function epicNoteOf(root: HTMLElement): string {
  return root.querySelector('section[aria-label="epic"] > p.side-note')?.textContent ?? "";
}

// nowAsk is the Ask the epic page's Now section draws, or null when it draws none.
function nowAsk(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>(".epic-page section.now .ask");
}

// The Ask's confirm button, or null when no Ask is drawn.
function askSubmit(root: HTMLElement): HTMLButtonElement | null {
  return nowAsk(root)?.querySelector<HTMLButtonElement>(".btn.primary") ?? null;
}

// The text a page's commands are typed under, which the epic-question-other story types into its answer.
const otherAnswer = "Vendor the Lisbon rule as a fixture file; do not touch go.mod.";

test("FixtureTransport: page/epic-building draws rota-export building, with two tasks worked on and no Ask", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-building"],
    (root) => nowSays(root, "2 tasks are being worked on."),
    (root) => {
      assertNoFailure(root);
      assert.equal(nowAsk(root), null, "no Ask is drawn");
    },
  );
});

test("FixtureTransport: page/epic-question-other draws the answer picked as Something else, with its text sent by that answer", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-question-other"],
    // The answer is sendable once its text is typed: until then the button is disabled.
    (root) => askSubmit(root)?.textContent === "Answer with Something else" && askSubmit(root)?.disabled === false,
    (root) => {
      assertNoFailure(root);
      const field = root.querySelector<HTMLTextAreaElement>('textarea[aria-label="answer"]');
      assert.equal(field?.value, otherAnswer, "the typed answer is in the box");
      assert.equal(askSubmit(root)?.disabled, false, "the answer is sendable with its text");
    },
  );
});

test("FixtureTransport: page/epic-parked draws notices-usable on the owner's park, with Retry and Stop and no other choice", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-parked"],
    (root) => nowAsk(root) !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(pageTitleText(root).includes("parked"), "the title row says parked");
      const park = nowAsk(root);
      assert.deepEqual(
        [...(park?.querySelectorAll(".opts > .opt .nm") ?? [])].map((name) => name.textContent),
        ["Retry"],
        "the park's Ask answers with Retry alone, and no Something else",
      );
      assert.equal(park?.querySelector("textarea"), null, "no question's answer box is drawn");
      assert.deepEqual(
        [...root.querySelectorAll(".epic-page section.now .doors button")].map((button) => button.textContent),
        ["Stop…"],
      );
    },
  );
});

test("FixtureTransport: page/epic-planner-parked draws the planner's park as one quiet line that asks nothing", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-planner-parked"],
    (root) => nowSays(root, "The planner holds a park and is deciding what to do."),
    (root) => {
      assertNoFailure(root);
      assert.ok(nowSays(root, "The planner holds this park: workflow node gate failed."), "the quiet line names the park");
      assert.equal(nowAsk(root), null, "no Ask is drawn");
    },
  );
});

test("FixtureTransport: page/epic-limited draws the steps on the weekly limit, grey, with Resume now", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-limited"],
    (root) => nowSays(root, "2 steps wait on the weekly limit"),
    (root) => {
      assertNoFailure(root);
      const resume = [...(epicNow(root)?.querySelectorAll("button") ?? [])].map((button) => button.textContent);
      assert.ok(resume.includes("Resume now"), "Resume now is drawn");
    },
  );
});

test("FixtureTransport: page/epic-paused draws harbour-map paused, with Resume as its command", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-paused"],
    (root) => pageTitleText(root).includes("paused"),
    (root) => {
      assertNoFailure(root);
      assert.ok(pageCommands(root).includes("Resume"), "Resume is drawn");
      assert.ok(!pageCommands(root).includes("Pause"), "Pause is not drawn while paused");
    },
  );
});

test("FixtureTransport: page/epic-planning draws buoy-feeds with its summary, round 6's history and no task table", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-planning"],
    (root) =>
      nowSays(root, "The planner is reading the contract and outlining tasks.") &&
      root.querySelector(".epic-page .happen.just") !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".epic-page section.tasks"), null, "no Tasks section is drawn");
      assert.equal(nowAsk(root), null, "no Ask is drawn");
      assert.deepEqual(
        [...root.querySelectorAll(".epic-page .happen.just .what")].map((what) => what.textContent),
        ["planner turn 1 · running", "The epic was created from the accepted contract"],
        "Just happened holds round 6's two entries, newest first",
      );
    },
  );
});

test("FixtureTransport: page/epic-dense draws 120 tasks, 96 done, the table folding the rest", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-dense"],
    (root) =>
      root.querySelector(".epic-page section.tasks")?.textContent.includes("96 of 120 done") === true &&
      root.querySelector(".epic-page .happen.just") !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(root.querySelectorAll(".epic-page .happen.just li").length > 0, "Just happened draws building's history");
      assert.ok(root.querySelector(".epic-page section.tasks .fold-line")?.textContent.includes("108 more tasks"), "the rest fold");
      assert.ok(root.querySelector('.epic-page section.tasks path.cell[data-task="U102"].c-bad'), "U102 is drawn failed");
      assert.ok(root.querySelector('.epic-page section.tasks path.cell[data-task="U107"].c-mine'), "U107 is drawn held");
    },
  );
});

// requirementLogs is the text of each run log drawn in the requirements section, and requirementArtifacts the text of each
// artifact line drawn there.
function requirementLogs(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".epic-page section.requirements .runs .log")].map((log) => log.textContent);
}
function requirementArtifacts(root: HTMLElement): string[] {
  return [...root.querySelectorAll(".epic-page section.requirements .artifact > span")].map((line) => line.textContent);
}

test("FixtureTransport: page/epic-evidence draws R4 open, both runs' logs, and the green run's two artifacts with its screenshot", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-evidence"],
    (root) =>
      requirementLogs(root).some((log) => log.includes("export_test.go:88: want ErrExists, got nil")) &&
      requirementLogs(root).some((log) => log.includes("--- PASS: TestExportRefusesAnExistingFile")) &&
      requirementArtifacts(root).length === 2 &&
      root.querySelector(".epic-page section.requirements img.artifact-image") !== null,
    (root) => {
      assertNoFailure(root);
      const open = root.querySelector('.epic-page section.requirements button.tr[aria-expanded="true"]');
      assert.ok(open?.textContent.includes("Export refuses to overwrite an existing file."), "R4's row is the one open");
      assert.deepEqual(requirementArtifacts(root), [
        "shots/export-refused.png · kept · 49152 bytes",
        "trace.zip · too large to keep · 52428800 bytes",
      ]);
      assert.equal(root.querySelector(".epic-page section.requirements .unavailable"), null, "every log and image is read");
    },
  );
});

// justHappened is the Just happened section of the epic page, or the empty string when none is drawn.
function justHappened(root: HTMLElement): string {
  return root.querySelector(".epic-page section.just")?.textContent ?? "";
}

// The requirement row the evidence story opens, or null when none is open.
function openRequirement(root: HTMLElement): HTMLElement | null {
  return root.querySelector<HTMLElement>(".epic-page section.requirements button.tr[aria-expanded=\"true\"]");
}

test("FixtureTransport: page/epic-evidence scrolls the opened requirement row into view, with its runs under it", (t) => {
  mockTimers(t, ["setTimeout"]);
  // happy-dom has no layout, so the scroll is seen as the call that asks the browser for it: each element passed to
  // scrollIntoView is recorded, in order.
  const scrolled: Element[] = [];
  return withStory(
    t,
    Page["epic-evidence"],
    (root) => {
      const row = openRequirement(root);
      return row !== null && scrolled.includes(row) && requirementLogs(root).length === 2;
    },
    (root) => {
      assertNoFailure(root);
      const row = openRequirement(root);
      assert.ok(row?.textContent.includes("Export refuses to overwrite an existing file."), "R4's row is the one open");
      assert.ok(row !== null && scrolled.includes(row), "the opened row is brought into view");
    },
    wideWidth,
    () => {
      window.Element.prototype.scrollIntoView = function recordScroll(this: Element) {
        scrolled.push(this);
      };
    },
  );
});

test("FixtureTransport: page/epic-parked draws Just happened with round 6's two entries", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-parked"],
    (root) => justHappened(root).includes("red run of R5 · passed, judged holds"),
    (root) => {
      assertNoFailure(root);
      assert.ok(justHappened(root).includes("The engine parked the epic: R5 met at the epic’s base"), "the park is drawn");
    },
  );
});

test("FixtureTransport: page/epic-paused draws Just happened with round 6's two entries", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-paused"],
    (root) => justHappened(root).includes("csv-writer · done · accepted by the judge"),
    (root) => {
      assertNoFailure(root);
      assert.ok(justHappened(root).includes("export-cli · checks passed, the gate is running"), "the gate entry is drawn");
    },
  );
});

test("FixtureTransport: page/epic-planner-parked's history names the gate in plain words, with no markdown", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-planner-parked"],
    (root) => justHappened(root).includes("The planner took the park: gate failed on export-cli."),
    (root) => {
      assertNoFailure(root);
      assert.ok(!justHappened(root).includes("**"), "no literal markdown is drawn");
    },
  );
});

test("FixtureTransport: every epic the slices hold is steered by a row the list draws, so the Steered from door has a title", async () => {
  for (const slices of [shippedSlices, ...Object.values(epicScenarios)]) {
    const { rows } = await loadRows(createFixtureTransport(slices).clients);
    for (const [id, detail] of slices.gets) {
      const steering = detail.epic?.steering ?? "";
      assert.ok(
        rows.some((row) => row.archiveKind !== "none" && row.archiveId === steering),
        `the steerer of ${id} is a listed row`,
      );
    }
  }
});

test("FixtureTransport: page/epic-stop-confirm draws Stop asking first, in the title row, beside Keep it running", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-stop-confirm"],
    (root) => root.querySelector(".sure")?.textContent === "Stop rota-export?",
    (root) => {
      assertNoFailure(root);
      assert.ok(pageCommands(root).includes("Keep it running"), "Keep it running is drawn");
    },
  );
});

test("FixtureTransport: page/epic-stop-confirm at 412 wide draws the confirm sheet alone, with More's sheet closed", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-stop-confirm"],
    (root) => root.querySelector('[role="dialog"][aria-label="Stop rota-export?"]') !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector('[role="dialog"][aria-label="rota-export"]'), null, "More's sheet is closed");
    },
    phoneWidth,
  );
});

test("FixtureTransport: page/epic-command-error draws the did-not-land line under the title row", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-command-error"],
    (root) => stateNote(root).includes("Pause did not land"),
    (root) => {
      assertNoFailure(root);
    },
  );
});

test("FixtureTransport: page/epic-command-error at 412 wide draws the same line, Pause having been pressed under More", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-command-error"],
    (root) => stateNote(root).includes("Pause did not land"),
    (root) => {
      assertNoFailure(root);
    },
    phoneWidth,
  );
});

test("FixtureTransport: page/epic-problem draws the engine's problem in red above the summary", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-problem"],
    (root) => root.querySelector("p.problem")?.textContent.includes("workflow.yaml line 14") === true,
    (root) => {
      assertNoFailure(root);
    },
  );
});

test("FixtureTransport: page/epic-loading draws loading the epic, with no button", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-loading"],
    (root) => epicNoteOf(root) === "loading the epic…",
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector('section[aria-label="epic"] > button'), "no button is drawn");
    },
  );
});

test("FixtureTransport: page/epic-unavailable draws the could-not-read line with Try again", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["epic-unavailable"],
    (root) => epicNoteOf(root) === "The server could not read this epic.",
    (root) => {
      assertNoFailure(root);
      const again = root.querySelector('section[aria-label="epic"] > button');
      assert.equal(again?.textContent, "Try again", "the read can be tried again");
    },
  );
});

test("FixtureTransport: page's keys are exactly the 90 catalog names of its scenario states", () => {
  assert.deepEqual(Object.keys(Page).map((state) => storyName("Page", state)).sort(), [
    "page/ask-answered",
    "page/ask-decline",
    "page/ask-error",
    "page/ask-later",
    "page/ask-many",
    "page/ask-permission",
    "page/ask-sending",
    "page/ask-session",
    "page/ask-stale",
    "page/ask-unanswerable",
    "page/contract-accepted",
    "page/contract-ready",
    "page/contract-set-aside",
    "page/contract-talk",
    "page/contract-to-accept",
    "page/contract-versions",
    "page/convo-blocks",
    "page/convo-contract",
    "page/convo-empty",
    "page/convo-find",
    "page/convo-handed-over",
    "page/convo-lines",
    "page/convo-loading",
    "page/convo-markdown",
    "page/convo-shell",
    "page/convo-took-over",
    "page/convo-working",
    "page/dock-background",
    "page/dock-cancelled",
    "page/dock-cut-off",
    "page/dock-draft",
    "page/dock-failed",
    "page/dock-idle",
    "page/dock-interrupt-queue",
    "page/dock-interrupted",
    "page/dock-interrupting",
    "page/dock-pane-queue",
    "page/dock-queued",
    "page/dock-running",
    "page/dock-send-error",
    "page/dock-send-unknown",
    "page/dock-stopped",
    "page/dock-waiting",
    "page/epic-building",
    "page/epic-command-error",
    "page/epic-convo-ended",
    "page/epic-dense",
    "page/epic-evidence",
    "page/epic-handover",
    "page/epic-limited",
    "page/epic-loading",
    "page/epic-moved",
    "page/epic-parked",
    "page/epic-paused",
    "page/epic-planner-parked",
    "page/epic-planning",
    "page/epic-problem",
    "page/epic-question",
    "page/epic-question-other",
    "page/epic-shipped",
    "page/epic-stop-confirm",
    "page/epic-talk",
    "page/epic-unavailable",
    "page/inbox-archived",
    "page/inbox-empty",
    "page/inbox-error",
    "page/inbox-flyout",
    "page/inbox-folded",
    "page/inbox-home",
    "page/inbox-loading",
    "page/inbox-narrow",
    "page/inbox-new",
    "page/inbox-rail",
    "page/inbox-row-menu",
    "page/inbox-search",
    "page/inbox-search-none",
    "page/inbox-waiting",
    "page/keys",
    "page/new-empty",
    "page/new-error",
    "page/new-form",
    "page/new-none",
    "page/new-results",
    "page/new-working",
    "page/phase-implement",
    "page/project",
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
      address: inbox,
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
      assert.deepEqual(reports, [["story #/inbox: the after step broke"]], "the rejection is reported once");
    },
  );
});

// searchValue is the words in the inbox's search field, or null while the field is shut.
function searchValue(root: HTMLElement): string | null {
  const field = root.querySelector<HTMLInputElement>('.inbox-search input[aria-label="Search sessions and epics"]');
  return field?.value ?? null;
}

// blankText is the text of the inbox body's blank state: loading, the error, nothing running or nothing matching. It
// is empty when the body draws a list.
function blankText(root: HTMLElement): string {
  return root.querySelector(".inbox-rows .blank")?.textContent ?? "";
}

test("InboxTree page/inbox-waiting draws the waiting chip on, and no other chip on", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-waiting"],
    (root) => root.querySelector(".fchip.attention.on") !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelectorAll(".fchip.on").length, 1, "one chip is on");
    },
  );
});

test("InboxTree page/inbox-new draws the new chip on, and no other chip on", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-new"],
    (root) => root.querySelector(".fchip.new.on") !== null,
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelectorAll(".fchip.on").length, 1, "one chip is on");
    },
  );
});

test("InboxTree page/inbox-search draws the search field holding tide, with rows under it", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-search"],
    (root) => searchValue(root) === "tide",
    (root) => {
      assertNoFailure(root);
      assert.equal(searchValue(root), "tide", "the field holds the words");
      assertNone(root.querySelector(".inbox-rows .blank"), "the search draws its blank state, not its rows");
    },
  );
});

test("InboxTree page/inbox-search-none draws the search field holding harbour-pilot, and Nothing matches", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-search-none"],
    (root) => blankText(root).includes("Nothing matches"),
    (root) => {
      assertNoFailure(root);
      assert.equal(searchValue(root), "harbour-pilot", "the field holds the words");
      assert.ok(blankText(root).includes("Nothing matches “harbour-pilot”."), "the blank says what was searched");
    },
  );
});

test("InboxTree page/inbox-row-menu draws pane %12's row with its More menu open", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-row-menu"],
    (root) => root.querySelector('[data-key="pane:halyard:%12"] .row-menu-list') !== null,
    (root) => {
      assertNoFailure(root);
      assert.ok(
        root.querySelector('[data-key="pane:halyard:%12"]')?.classList.contains("menu-open"),
        "pane %12's row is not marked menu-open",
      );
      const labels = [...root.querySelectorAll('[data-key="pane:halyard:%12"] .row-menu-list [role="menuitem"] span')];
      assert.ok(
        labels.some((label) => label.textContent === "Archive"),
        "pane %12's More menu offers Archive, since its row has an orrery_id",
      );
    },
  );
});

test("InboxTree page/inbox-folded folds the orrery directory and opens tidepool's ended line", (t) => {
  mockTimers(t, ["setTimeout"]);
  const tidepoolEnded = 'section.dir[aria-label="tidepool"] button.fold-line';
  return withStory(
    t,
    Page["inbox-folded"],
    (root) =>
      root.querySelector('button.fold[aria-label="Unfold orrery"]') !== null &&
      root.querySelector(tidepoolEnded)?.getAttribute("aria-expanded") === "true",
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector('section.dir[aria-label="orrery"] .dir-kids'), "orrery's conversations are drawn");
      assert.equal(
        root.querySelector(tidepoolEnded)?.getAttribute("aria-expanded"),
        "true",
        "tidepool's ended line is shut",
      );
    },
  );
});

test("InboxTree page/inbox-archived draws the archived view with its Archived head", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-archived"],
    (root) => listView(root) === "archived",
    (root) => {
      assertNoFailure(root);
      assert.equal(root.querySelector(".inbox-attn .name")?.textContent, "Archived", "the Archived head is drawn");
    },
  );
});

test("InboxTree page/inbox-loading draws the inbox as loading, with no project, before the first load", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withPage(
    t,
    Page["inbox-loading"],
    (root) => blankText(root) === "loading…",
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector(".dir"), "a project is drawn before the first load answered");
      assertNone(root.querySelector("[data-loaded]"), "the held first load answered");
    },
  );
});

test("InboxTree page/inbox-empty draws the inbox with nothing running, and no project", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-empty"],
    (root) => blankText(root).includes("Nothing is running."),
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector(".dir"), "a project is drawn with nothing running");
    },
  );
});

test("InboxTree page/inbox-error draws the server did not answer in place of the rows, with no note line", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withStory(
    t,
    Page["inbox-error"],
    (root) => blankText(root).includes("The server did not answer."),
    (root) => {
      assertNoFailure(root);
      assertNone(root.querySelector('[role="alert"]'), "the list's failure is drawn as a note line");
      const buttons = [...root.querySelectorAll("button")].map((button) => button.textContent ?? "");
      assert.ok(buttons.some((text) => text.includes("Try now")), `the error offers no Try now; buttons were ${JSON.stringify(buttons)}`);
    },
  );
});
