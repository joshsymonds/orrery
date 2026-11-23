import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { at, liveEpoch } from "./fixtures/clock";
import { type EpicsView, epicsView } from "./epics";
import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import {
  type BackgroundTask,
  BackgroundTaskSchema,
  ConversationStateSchema,
  type ConversationItem,
  ConversationItemSchema,
} from "./gen/orrery/v1/orrery_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token TileStrip, which names the phone's work: a phone is 412px wide, under the 760px the
// wide layout starts at, so the conversation's work is its work links in the title row and its running tasks are activity
// lines above its composer, with no rail and no strip; an epic is the page with a foot bar, and the +n menu and a list are
// sheets or menus. A session's stream is a fake the test feeds, and every time is a fixture's instant, so no test reads
// the wall clock.

const phoneWidth = 412;
const now = Date.parse(liveEpoch) / 1000;
const repository = "widgets-5d0c1a7";
const footLabel = "Talk to the session that steers it";

type Clients = ReturnType<typeof createClients>;

// Report is one address App reported, with the mode it named.
interface Report {
  address: Address;
  mode: string;
}

// FakeServer answers WatchConversation with a stream per followed conversation, which the test feeds through send. Each
// unary call answers with an empty message.
class FakeServer {
  readonly streams = new Map<string, ReadableStreamDefaultController<Uint8Array>>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (!path.endsWith("/WatchConversation")) {
      return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
    }
    const body = JSON.parse((await request.text()).slice(5)) as { id?: string; pane?: string };
    const id = body.id ?? body.pane ?? "";
    const stream = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.streams.set(id, controller);
      },
    });
    return new Response(stream, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  readonly clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  send(id: string, message: object): void {
    const controller = this.streams.get(id);
    assert.ok(controller !== undefined, `no stream follows ${id}`);
    controller.enqueue(envelope(message));
  }
}

// envelope frames a stream message as Connect's JSON streaming frames it: a flag of 0, the payload's length, the
// payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = 0;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// delta is a WatchConversation message that holds one prompt, the conversation's goal, and the background tasks the
// session reports.
function delta(tasks: BackgroundTask[]): object {
  const prompt: ConversationItem = create(ConversationItemSchema, {
    kind: "prompt",
    text: "a message the stream holds",
  });
  const state = create(ConversationStateSchema, { goal: "Ship the rail.", background: tasks });
  return {
    transcript: "s1.jsonl",
    seq: "1",
    reset: true,
    items: [toJson(ConversationItemSchema, prompt)],
    state: toJson(ConversationStateSchema, state),
  };
}

// task is a background task the session reports, its fields given as the server gives them.
function task(init: MessageInitShape<typeof BackgroundTaskSchema>): BackgroundTask {
  return create(BackgroundTaskSchema, init);
}

// background holds two running agents and one running shell.
const background: BackgroundTask[] = [
  task({
    id: "a1",
    description: "Map the rota",
    type: "agent",
    subagentType: "Explore",
    status: "running",
    activity: "Reading the rota",
    started: at("@14:37:58"),
  }),
  task({
    id: "a2",
    description: "Check the holds",
    type: "agent",
    status: "running",
    activity: "Running the tests",
    started: at("@14:40:30"),
  }),
  task({
    id: "s1",
    description: "Watch the build",
    type: "shell",
    status: "running",
    activity: "tail -f build.log",
    started: at("@14:37:58"),
  }),
];

// row is a row as the list holds it, with the fields the tests do not set at their defaults.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
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

// steererRow is the headless session s1, which steers the epics these tests name with steering s1.
function steererRow(): Row {
  return row({
    key: "session:s1",
    id: "s1",
    kind: "session",
    host: "",
    label: "auth work",
    project: "/work/api",
    archiveId: "s1",
    archiveKind: "session",
  });
}

// epicOf is a live epic in the repository, steered by the session steering.
function epicOf(slug: string, steering: string): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering,
  });
}

// view is the epics view of the epics, each with a detail whose summary is its slug.
function view(epics: Epic[]): EpicsView {
  const details = epics.map((epic): [string, EpicsGetResponse] => [
    epic.id,
    create(EpicsGetResponseSchema, { epic, summary: `Ship ${epic.slug}` }),
  ]);
  return epicsView(epics, new Map(details));
}

const alpha = epicOf("alpha", "s1");
const sessionAddress: Address = { view: "inbox", entry: { kind: "session", id: "s1" } };
const agentsAddress: Address = { view: "inbox", entry: { kind: "session", id: "s1", panel: "agents" } };

// epicAddress is the address of an epic's page.
function epicAddress(slug: string): Address {
  return { view: "inbox", entry: { kind: "epic", id: `${repository}/${slug}`, page: { kind: "epic" } } };
}

// Setup is what a mount draws: the rows, the epics and the address App is given.
interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  address?: Address;
}

// Mount is what a check gets: the container App draws into, the server it follows, and the addresses App reported.
interface Mount {
  container: HTMLElement;
  server: FakeServer;
  reported: Report[];
}

// draw renders App over the setup, with its address reports recorded.
function draw(container: HTMLElement, clients: Clients, reported: Report[], setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={now}
        clients={clients}
        epics={setup.epics}
        address={setup.address}
        onAddress={(address, mode) => reported.push({ address, mode })}
      />,
      container,
    );
  });
}

// withPhone mounts App at the phone's width in a fresh DOM and runs check with the mount. App is unmounted and the DOM
// removed when the test ends, or as soon as signal aborts first.
async function withPhone(
  signal: AbortSignal,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = phoneWidth;
  const container = document.createElement("div");
  document.body.append(container);
  const server = new FakeServer();
  const reported: Report[] = [];
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw(container, server.clients, reported, setup);
    await check({ container, server, reported });
  } finally {
    await teardown();
  }
}

// settle waits until check holds, each round inside act, so the renders a message causes are applied before the next
// check.
function settle(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// feed sends the session's stream the delta with the background tasks, once the stream is open.
async function feed(signal: AbortSignal, server: FakeServer, id: string, tasks: BackgroundTask[]): Promise<void> {
  await settle(signal, () => server.streams.has(id), `the stream that follows ${id}`);
  server.send(id, delta(tasks));
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// pressEscape sends Escape to an element, as a key press inside a focused sheet does.
async function pressEscape(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
}

// activityLines are the lines the conversation's activity draws above its composer, in order.
function activityLines(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>('[aria-label="running now"] .activity-line')];
}

// lineOf is the activity line whose who is the one given, which must be drawn.
function lineOf(container: HTMLElement, who: string): HTMLElement {
  const line = activityLines(container).find((candidate) => candidate.querySelector(".who")?.textContent === who);
  assert.ok(line !== undefined, `no activity line of ${who}`);
  return line;
}

// workLinkOf is the title row's work link with the title given, which must be drawn.
function workLinkOf(container: HTMLElement, title: string): HTMLElement {
  const link = container.querySelector<HTMLElement>(`.title-row .wlink[title="${title}"]`);
  assert.ok(link !== null, `no work link titled ${title}`);
  return link;
}

// lastReport is the address App reported last, with the mode it named.
function lastReport(reported: Report[]): Report {
  const report = reported.at(-1);
  assert.ok(report !== undefined, "App reported no address");
  return report;
}

// conversationShown is the reader column when it is drawn, or null when the page hides it.
function conversationShown(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="reader"]:not([hidden])');
}

// epicSection is the epic drawn as the page, or null when it is not drawn.
function epicSection(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section[aria-label="epic"]');
}

// footButton is the button the epic's foot bar draws, or null when there is none.
function footButton(container: HTMLElement): HTMLElement | null {
  const buttons = [...container.querySelectorAll<HTMLElement>("button")];
  return buttons.find((button) => button.textContent === footLabel) ?? null;
}

// sheetOf is the sheet drawn, or null when none is.
function sheetOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section.sheet[role="dialog"]');
}

// settleLinks waits until the title row links alpha and the conversation draws the three activity lines its stream gives.
function settleLinks(signal: AbortSignal, container: HTMLElement): Promise<void> {
  return settle(
    signal,
    () => container.querySelector('.title-row .wlink[title="Open alpha"]') !== null && activityLines(container).length === 3,
    "the alpha link and the three activity lines",
  );
}

// settleLines waits until the conversation draws the three activity lines its stream gives.
function settleLines(signal: AbortSignal, container: HTMLElement): Promise<void> {
  return settle(signal, () => activityLines(container).length === 3, "the three activity lines");
}

// onSession opens the conversation of s1, which alpha steers, where a phone's conversation shows its strip.
const onSession: Setup = { rows: [steererRow()], epics: view([alpha]), address: sessionAddress };

test("TileStrip: under 760px the band holds the header's hide control, with no strip, no rail and no side panel", (t) =>
  withPhone(t.signal, onSession, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleLinks(t.signal, container);
    assert.equal(container.querySelector("aside.rail"), null, "no rail is drawn");
    assert.equal(container.querySelector("aside.rpanel"), null, "no side panel is drawn");
    const goal = container.querySelector<HTMLElement>(".subj.goal");
    assert.ok(goal !== null, "the band's goal line is drawn");
    assert.equal(goal.querySelectorAll(".fold-head").length, 1, "the hide control is drawn on the band's goal line");
    assert.equal(container.querySelectorAll(".fold-head").length, 1, "the hide control is drawn once");
    assert.deepEqual(
      activityLines(container).map((line) => line.querySelector(".who")?.textContent),
      ["Explore", "agent", "shell"],
      "the running agents and the shell are drawn as lines",
    );
  }));

test("TileStrip: an epic's work link pushes the epic's address and holds it as the sheet under the conversation's head, with no reader", (t) =>
  withPhone(t.signal, onSession, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleLinks(t.signal, container);
    await press(workLinkOf(container, "Open alpha"));
    assert.deepEqual(lastReport(reported), { address: epicAddress("alpha"), mode: "push" }, "the epic is pushed");
    assert.ok(epicSection(container) !== null, "the epic is drawn");
    assert.ok(container.querySelector("section.held-sheet") !== null, "the epic is held as the sheet");
    assertNone(container.querySelector(".reader"), "the conversation's reader is not drawn");
  }));

// onEpic opens alpha's page with its steerer's conversation under it.
const onEpic: Setup = { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") };

// unsteered opens beta's page, which no conversation steers.
const unsteered: Setup = { rows: [], epics: view([epicOf("beta", "")]), address: epicAddress("beta") };

test("TileStrip: the epic page's foot bar pushes the steering conversation's address and draws it", (t) =>
  withPhone(t.signal, onEpic, async ({ container, reported }) => {
    assert.ok(epicSection(container) !== null, "the epic is drawn as the page at its address");
    const foot = footButton(container);
    assert.ok(foot !== null, "the foot bar has its button");
    await press(foot);
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" }, "the steerer is pushed");
    assert.equal(epicSection(container), null, "the epic page is shut");
    assert.ok(conversationShown(container) !== null, "the conversation is drawn");
  }));

test("TileStrip: an epic no conversation steers draws its foot with More and no Talk button", (t) =>
  withPhone(t.signal, unsteered, async ({ container }) => {
    assert.ok(epicSection(container) !== null, "the epic is drawn as the page");
    assert.equal(footButton(container), null, "no Talk button is drawn");
    assert.deepEqual(
      [...container.querySelectorAll(".cmdbar button")].map((button) => button.textContent),
      ["More"],
      "the foot holds More alone",
    );
  }));

test("TileStrip: the phone's epic page keeps its commands out of the title row, and More lists them with its steering", (t) =>
  withPhone(t.signal, onEpic, async ({ container }) => {
    assert.deepEqual(
      [...container.querySelectorAll(".title-row .acts button")]
        .map((button) => button.textContent)
        .filter((label) => ["Talk", "Pause", "Resume", "Stop…"].includes(label ?? "")),
      [],
      "the title row draws no Talk, Pause, Resume or Stop",
    );
    const more = [...container.querySelectorAll<HTMLButtonElement>(".cmdbar button")].find(
      (button) => button.textContent === "More",
    );
    assert.ok(more !== undefined, "the foot holds More");
    await press(more);
    assert.deepEqual(
      [...container.querySelectorAll('section[role="dialog"][aria-label="alpha"] button')].map((b) => b.textContent),
      ["Pause", "Stop…", "Steer it from another conversation"],
      "More lists the commands alpha allows, then its steering",
    );
  }));

test("TileStrip: four epics' +n control opens a menu of the four, and its item pushes that epic's address", (t) => {
  const four = ["alpha", "bravo", "charlie", "delta"].map((slug) => epicOf(slug, "s1"));
  const setup: Setup = { rows: [steererRow()], epics: view(four), address: sessionAddress };
  return withPhone(t.signal, setup, async ({ container, reported }) => {
    const more = container.querySelector<HTMLElement>(".title-row .wlink-more");
    assert.ok(more !== null, "four epics draw one +n control");
    await press(more);
    const items = [...container.querySelectorAll<HTMLElement>(".title-row .menu [role='menuitem']")];
    assert.equal(items.length, 4, "the menu holds an item for each epic");
    const delta = items.find((item) => item.textContent === "delta");
    assert.ok(delta !== undefined, "the menu has the delta item");
    await press(delta);
    assert.deepEqual(lastReport(reported), { address: epicAddress("delta"), mode: "push" }, "the item's epic is pushed");
    assertNone(container.querySelector(".title-row .menu"), "the menu is shut");
  });
});

// onListSession opens the conversation of s1 with no epic, so its title row links nothing and its lines are the running
// tasks alone. Under 760px a conversation draws only once it is opened, as its address opens it.
const onListSession: Setup = { rows: [steererRow()], address: sessionAddress };

test("TileStrip: an activity line pushes the list's address and draws the list in a sheet over the conversation", (t) =>
  withPhone(t.signal, onListSession, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleLines(t.signal, container);
    await press(lineOf(container, "Explore"));
    assert.deepEqual(lastReport(reported), { address: agentsAddress, mode: "push" }, "the list's address is pushed");
    const sheet = sheetOf(container);
    assert.ok(sheet !== null && sheet.getAttribute("aria-label") === "Sub-agents", "the agents are drawn in a sheet");
    assert.deepEqual(
      [...sheet.querySelectorAll(".plist .row .title")].map((title) => title.textContent),
      ["Map the rota", "Check the holds"],
      "the sheet lists the running agents",
    );
    assert.ok(conversationShown(container) !== null, "the conversation stays drawn behind the sheet");
  }));

test("TileStrip: Escape on a list's sheet returns to the session's address", (t) =>
  withPhone(t.signal, onListSession, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleLines(t.signal, container);
    await press(lineOf(container, "Explore"));
    const sheet = sheetOf(container);
    assert.ok(sheet !== null, "the sheet is drawn before Escape");
    await pressEscape(sheet);
    assert.equal(sheetOf(container), null, "Escape shuts the sheet");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" }, "the session is pushed");
  }));

test("TileStrip: a pinned conversation's column draws no work link or activity line, so a line always acts on the conversation it names", (t) =>
  withPhone(t.signal, onSession, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleLinks(t.signal, container);
    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "|", bubbles: true }));
    });
    assert.ok(container.querySelector('section[aria-label="pinned reader"]') !== null, "the row is pinned");
    assertNone(
      container.querySelector('section[aria-label="pinned reader"] .wlinks'),
      "the pinned column draws no work links",
    );
    assertNone(
      container.querySelector('section[aria-label="pinned reader"] [aria-label="running now"]'),
      "the pinned column draws no activity lines",
    );
    assert.equal(container.querySelectorAll(".wlinks").length, 1, "only the cursor's conversation draws its links");
  }));
