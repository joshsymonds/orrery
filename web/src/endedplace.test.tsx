import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { endedLead, unsteeredLead } from "./endedbar";
import { type EpicsView, epicsView } from "./epics";
import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token EndedBar: the bar that stands where a stopped or failed conversation's dock was, and
// the bar in the middle beside an epic whose steerer has no listed row. The no-conversation line is drawn where the bar is
// not. Each test mounts App at a window width of its own, since the layout reads the width when App mounts.

const now = 1000;
const repository = "widgets-5d0c1a7";
const dir = "/home/owner/code/widgets";

// Probe records each path App calls, and holds the checks waiting for a call to be made. read holds each epic read until
// it is released with the epic it answers, and is null while the reads answer at once.
interface Probe {
  calls: string[];
  waiting: { suffix: string; ready: () => void }[];
  read: Promise<EpicsGetResponse> | null;
}

// Mount is what a check gets: the container App draws into, the probe, and rerender, which draws App again with the setup
// given, as the poll does when the epics list changes.
interface Mount {
  container: HTMLElement;
  probe: Probe;
  rerender: (setup: Setup) => Promise<void>;
}

interface Setup {
  rows: Row[];
  epics: EpicsView;
  address: Address;
}

// called resolves once App has made a call whose path ends with suffix. A check awaits it rather than reading the probe at
// once, since a call is made after the effect or the click that starts it.
function called(probe: Probe, suffix: string): Promise<void> {
  return new Promise((ready) => {
    if (probe.calls.some((path) => path.endsWith(suffix))) {
      ready();
    } else {
      probe.waiting.push({ suffix, ready });
    }
  });
}

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open. Each call is recorded
// by its path.
async function fakeFetch(probe: Probe, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  await request.text();
  probe.calls.push(path);
  probe.waiting = probe.waiting.filter((waiter) => {
    if (!path.endsWith(waiter.suffix)) {
      return true;
    }
    waiter.ready();
    return false;
  });
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  if (path.endsWith("/Get") && probe.read !== null) {
    return new Response(JSON.stringify(toJson(EpicsGetResponseSchema, await probe.read)), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

// holdReads makes the probe's epic reads wait, and returns the release that answers the read in hand with reading.
function holdReads(probe: Probe): (reading: EpicsGetResponse) => void {
  let release: (reading: EpicsGetResponse) => void = () => undefined;
  probe.read = new Promise<EpicsGetResponse>((settle) => {
    release = settle;
  });
  return (reading) => release(reading);
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: dir,
    host: "box",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// headlessRow is the headless session sessionId in the epic's directory, in the given state, under the label given.
function headlessRow(sessionId: string, state: string, label = "Planned the rota export"): Row {
  return row({
    key: `session:${sessionId}`,
    id: sessionId,
    kind: "session",
    label,
    host: "",
    state,
    archiveId: sessionId,
    archiveKind: "headless",
  });
}

// epicOf is an epic in the repository, with the fields init gives. Its id is the repository and its slug.
function epicOf(slug: string, steering: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    source: "orreryd",
    steering,
    ...init,
  });
}

// view is the epics view of the epics, each with a detail whose summary is "Ship" and its slug.
function view(epics: Epic[]): EpicsView {
  const details = epics.map((epic): [string, EpicsGetResponse] => [
    epic.id,
    create(EpicsGetResponseSchema, { epic, summary: `Ship ${epic.slug}` }),
  ]);
  return epicsView(epics, new Map(details));
}

// epicAddress is the address of an epic's page.
function epicAddress(slug: string): Address {
  return { view: "inbox", entry: { kind: "epic", id: `${repository}/${slug}`, page: { kind: "epic" } } };
}

// sessionAddress is the address of a session's row.
function sessionAddress(id: string): Address {
  return { view: "inbox", entry: { kind: "session", id } };
}

// rota is the orreryd epic the bar concerns, steered by the session steering.
function rota(steering: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return epicOf("rota", steering, init);
}

// Clients are the clients App is drawn with, which a check keeps across its rerenders.
type Clients = ReturnType<typeof createClients>;

// draw renders App over the setup into container, with the clients given.
function draw(container: HTMLElement, clients: Clients, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows}
        now={now}
        clients={clients}
        epics={setup.epics}
        address={setup.address}
        host={null}
        onAddress={() => {}}
      />,
      container,
    );
  });
}

// withApp mounts App at width in a fresh DOM over the setup and runs check with the mount. App is unmounted and the DOM
// removed when the test ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  width: number,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { calls: [], waiting: [], read: null };
  const clients = createClients(
    createTransport({ baseUrl: "https://orrery.test", fetch: (input, init) => fakeFetch(probe, input, init) }),
  );
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(container, clients, setup);
    await check({ container, probe, rerender: (next) => draw(container, clients, next) });
  } finally {
    await teardown();
  }
}

// shownReply is the dock wrapper of the cursor's row that is drawn, or null when no row's dock is drawn.
function shownReply(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="reader"] .reply:not([hidden])');
}

// noConversation is the desk's middle section for an epic no listed row steers, or null when it is not drawn.
function noConversation(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="no conversation"]');
}

// continueButton is the bar's Continue in a new conversation button, or null when the bar is not drawn.
function continueButton(scope: HTMLElement): HTMLButtonElement | null {
  return [...scope.querySelectorAll<HTMLButtonElement>("button")].find(
    (button) => button.textContent === "Continue in a new conversation",
  ) ?? null;
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// The session the stopped and failed conversations are, and the session the running one is.
const endedSession = "sess-rota-plan";

test("EndedBar: a stopped headless conversation steering a live orreryd epic draws the bar in place of its dock, with the conversation-ended lead", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [headlessRow(endedSession, "stopped")], epics: view([rota(endedSession)]), address: sessionAddress(endedSession) },
    async ({ container }) => {
      const reply = shownReply(container);
      assert.ok(reply !== null, "the stopped conversation's row is drawn");
      const bar = reply.querySelector<HTMLElement>(".ended-bar");
      assert.ok(bar !== null, "the bar stands in the row's reply");
      assert.equal(bar.querySelector(".ended-lead")?.textContent, endedLead("rota"), "the bar's lead says the conversation ended");
      assert.equal(reply.querySelector("textarea"), null, "no dock input is drawn");
    },
  ));

test("EndedBar: a failed headless conversation steering a live orreryd epic draws the bar in place of its dock, with the conversation-ended lead", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [headlessRow(endedSession, "failed")], epics: view([rota(endedSession)]), address: sessionAddress(endedSession) },
    async ({ container }) => {
      const reply = shownReply(container);
      assert.ok(reply !== null, "the failed conversation's row is drawn");
      const bar = reply.querySelector<HTMLElement>(".ended-bar");
      assert.ok(bar !== null, "the bar stands in the row's reply");
      assert.equal(bar.querySelector(".ended-lead")?.textContent, endedLead("rota"), "the bar's lead says the conversation ended");
      assert.equal(reply.querySelector("textarea"), null, "no dock input is drawn");
    },
  ));

test("EndedBar: a running headless conversation steering a live orreryd epic draws its dock and no bar", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [headlessRow(endedSession, "running")], epics: view([rota(endedSession)]), address: sessionAddress(endedSession) },
    ({ container }) => {
      const reply = shownReply(container);
      assert.ok(reply !== null, "the running conversation's row is drawn");
      assert.ok(reply.querySelector("textarea") !== null, "the dock's input is drawn");
      assert.equal(container.querySelector(".ended-bar"), null, "no bar is drawn");
    },
  ));

test("EndedBar: Continue in a new conversation sends one Epics.Continue, and nothing else moves the epic", (t) =>
  withApp(
    t.signal,
    1600,
    { rows: [headlessRow(endedSession, "stopped")], epics: view([rota(endedSession)]), address: sessionAddress(endedSession) },
    async ({ container, probe }) => {
      const button = continueButton(shownReply(container) ?? container);
      assert.ok(button !== null, "the bar's Continue button is drawn");
      await press(button);
      await called(probe, "/Get");
      assert.equal(probe.calls.filter((path) => path.endsWith("/Continue")).length, 1, "Continue is called once");
      assert.equal(probe.calls.filter((path) => path.endsWith("/Move")).length, 0, "no move is called");
    },
  ));

test("EndedBar: at the desk, an epic whose steering names no listed session draws the bar in the middle, with the conversation-ended lead", (t) =>
  withApp(t.signal, 1600, { rows: [], epics: view([rota("sess-gone")]), address: epicAddress("rota") }, ({ container }) => {
    const middle = noConversation(container);
    assert.ok(middle !== null, "the middle section is drawn");
    assert.equal(middle.querySelector(".ended-lead")?.textContent, endedLead("rota"), "the bar's lead says the conversation ended");
    assert.ok(continueButton(middle) !== null, "the bar's Continue button is drawn");
    assert.equal(container.textContent?.includes("No conversation steers this epic."), false, "the no-conversation line is not drawn");
  }));

test("EndedBar: at 412, an epic whose steering names no listed session draws the bar in the narrow foot, with the conversation-ended lead", (t) =>
  withApp(t.signal, 412, { rows: [], epics: view([rota("sess-gone")]), address: epicAddress("rota") }, ({ container }) => {
    const foot = container.querySelector<HTMLElement>(".epic-page > .ended-bar");
    assert.ok(foot !== null, "the bar is drawn at the epic page's foot");
    assert.equal(foot.nextElementSibling?.classList.contains("cmdbar"), true, "the bar stands right above the foot bar");
    assert.equal(foot.querySelector(".ended-lead")?.textContent, endedLead("rota"), "the bar's lead says the conversation ended");
    assert.ok(continueButton(foot) !== null, "the bar's Continue button is drawn");
  }));

test("EndedBar: at the desk, an epic with no steering draws the bar in the middle, with the no-conversation lead", (t) =>
  withApp(t.signal, 1600, { rows: [], epics: view([rota("")]), address: epicAddress("rota") }, ({ container }) => {
    const middle = noConversation(container);
    assert.ok(middle !== null, "the middle section is drawn");
    assert.equal(middle.querySelector(".ended-lead")?.textContent, unsteeredLead("rota"), "the bar's lead says no conversation steers it");
    assert.ok(continueButton(middle) !== null, "the bar's Continue button is drawn");
  }));

test("EndedBar: at 412, an epic with no steering draws the bar in the narrow foot, with the no-conversation lead", (t) =>
  withApp(t.signal, 412, { rows: [], epics: view([rota("")]), address: epicAddress("rota") }, ({ container }) => {
    const foot = container.querySelector<HTMLElement>(".epic-page > .ended-bar");
    assert.ok(foot !== null, "the bar is drawn at the epic page's foot");
    assert.equal(foot.nextElementSibling?.classList.contains("cmdbar"), true, "the bar stands right above the foot bar");
    assert.equal(foot.querySelector(".ended-lead")?.textContent, unsteeredLead("rota"), "the bar's lead says no conversation steers it");
    assert.ok(continueButton(foot) !== null, "the bar's Continue button is drawn");
  }));

test("EndedBar: an ended epic and a record epic with no steerer draw the no-conversation line and no bar, at the desk", async (t) => {
  const ended = rota("sess-gone", { live: false });
  const record = epicOf("rota", "", { source: "record", live: true });
  for (const epic of [ended, record]) {
    await withApp(t.signal, 1600, { rows: [], epics: view([epic]), address: epicAddress("rota") }, ({ container }) => {
      const middle = noConversation(container);
      assert.ok(middle !== null, "the middle section is drawn");
      assert.equal(middle.textContent?.includes("No conversation steers this epic."), true, "the no-conversation line is drawn");
      assert.equal(middle.querySelector(".ended-bar"), null, "no bar is drawn");
      const own = [...middle.querySelectorAll("button")].filter((control) => control.closest("article.held-card") === null);
      assert.equal(own.length, 0, "the middle draws no control of its own beside the card");
      assert.ok(middle.querySelector('article.held-card button[aria-label="Fold back"]') !== null, "the card draws its Fold back");
    });
  }
});

// readingNaming is the epic as Epics.Get answers it, steered by the session given.
function readingNaming(steering: string): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: rota(steering), summary: "Ship rota" });
}

test("EndedBar: Continue on a stopped conversation, with the epics list rerendered naming another steerer while the re-read is held, opens the epic's entry with the middle on the session the read names", (t) => {
  const named = "sess-rota-next";
  const rows = [headlessRow(endedSession, "stopped"), headlessRow(named, "running", "Checks the rota export")];
  const address = sessionAddress(endedSession);
  return withApp(t.signal, 1600, { rows, epics: view([rota(endedSession)]), address }, async ({ container, probe, rerender }) => {
    const release = holdReads(probe);
    const button = continueButton(shownReply(container) ?? container);
    assert.ok(button !== null, "the bar's Continue button is drawn");
    await press(button);
    await called(probe, "/Get");
    await rerender({ rows, epics: view([rota(named)]), address });
    assert.equal(container.querySelector(".ended-bar"), null, "the bar is unmounted once the list names another steerer");

    release(readingNaming(named));
    await until(t.signal, () => container.querySelector(".row.epic.cur") !== null, "the cursor reaches the epic's entry", {
      step: () => act(turn),
    });

    assert.equal(probe.calls.filter((path) => path.endsWith("/Continue")).length, 1, "Continue is called once");
    assert.equal(probe.calls.filter((path) => path.endsWith("/Get")).length, 1, "the epic is read once");
    const reader = container.querySelector<HTMLElement>('section[aria-label="reader"]');
    assert.equal(reader?.textContent?.includes("Checks the rota export"), true, "the middle is the session the read names");
  });
});
