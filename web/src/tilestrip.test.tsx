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

// Each test's name carries the token TileStrip. A phone is 412px wide, under the 760px the wide layout starts at, so
// the conversation's groups draw as a strip under its header with no rail, an epic is the page with a foot bar, and the
// picker and a list are sheets. A session's stream is a fake the test feeds, and every time is a fixture's instant, so
// no test reads the wall clock.

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

// stripTiles is each tile the strip under the conversation's header draws, in order.
function stripTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".tilestrip .tile-w")];
}

// tileTitled is the strip's tile whose title starts with prefix: an epic's short name, or a list's key and a colon.
function tileTitled(container: HTMLElement, prefix: string): HTMLElement {
  const tile = stripTiles(container).find((candidate) => candidate.getAttribute("title")?.startsWith(prefix) === true);
  assert.ok(tile !== undefined, `no strip tile titled ${prefix}`);
  return tile;
}

// tileName is the name a strip tile's title gives: the text before its colon.
function tileName(tile: HTMLElement): string {
  return (tile.getAttribute("title") ?? "").split(":")[0];
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

// settleTiles waits until the strip draws the two list tiles the stream's background gives and the epic's tile.
function settleTiles(signal: AbortSignal, container: HTMLElement): Promise<void> {
  return settle(signal, () => stripTiles(container).length === 3, "the two list tiles and the epic's");
}

// onSession opens the conversation of s1, which alpha steers, where a phone's conversation shows its strip.
const onSession: Setup = { rows: [steererRow()], epics: view([alpha]), address: sessionAddress };

test("TileStrip: under 760px the groups draw as a strip under the band, ending in the header's hide control, with no rail and no side panel", (t) =>
  withPhone(t.signal, onSession, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    assert.equal(container.querySelector("aside.rail"), null, "no rail is drawn");
    assert.equal(container.querySelector("aside.rpanel"), null, "no side panel is drawn");
    const strip = container.querySelector<HTMLElement>(".tilestrip");
    assert.ok(strip !== null, "the strip is drawn");
    assert.equal(
      strip.parentElement?.classList.contains("convo-head"),
      true,
      "the strip sits in the conversation's head, under its band",
    );
    assert.equal(
      strip.lastElementChild?.getAttribute("aria-label"),
      "Hide the header",
      "the strip ends in the header's hide control",
    );
    assert.equal(container.querySelectorAll(".fold-head").length, 1, "the hide control is drawn once, at the strip's end");
    const goal = container.querySelector<HTMLElement>(".subj.goal");
    assert.ok(goal !== null, "the band's goal line is drawn");
    assertNone(goal.querySelector(".fold-head"));
    assert.deepEqual(
      stripTiles(container).map(tileName),
      ["alpha", "agents", "running"],
      "the strip draws the epic's group, then the agents and running groups",
    );
  }));

test("TileStrip: an epic tile pushes the epic's address and draws it as the page, without the conversation", (t) =>
  withPhone(t.signal, onSession, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(tileTitled(container, "alpha:"));
    assert.deepEqual(lastReport(reported), { address: epicAddress("alpha"), mode: "push" }, "the epic is pushed");
    assert.ok(epicSection(container) !== null, "the epic is drawn as the page");
    assert.equal(conversationShown(container), null, "the conversation is not drawn");
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

test("TileStrip: an epic no conversation steers draws its foot line and no button", (t) =>
  withPhone(t.signal, unsteered, async ({ container }) => {
    assert.ok(epicSection(container) !== null, "the epic is drawn as the page");
    assert.equal(footButton(container), null, "no button is drawn");
    assert.ok(container.textContent?.includes("No conversation steers this epic.") === true, "the foot line says so");
  }));

test("TileStrip: four epics' dots tile opens the picker as a sheet, and its row opens that epic", (t) => {
  const four = ["alpha", "bravo", "charlie", "delta"].map((slug) => epicOf(slug, "s1"));
  const setup: Setup = { rows: [steererRow()], epics: view(four), address: sessionAddress };
  return withPhone(t.signal, setup, async ({ container, reported }) => {
    const dots = container.querySelector<HTMLElement>(".tilestrip .tile-w.group");
    assert.ok(dots !== null, "four epics draw one dots tile");
    await press(dots);
    const sheet = sheetOf(container);
    assert.ok(sheet !== null, "the picker is drawn as a sheet");
    assert.equal(sheet.querySelectorAll(".rpick-row").length, 4, "the sheet holds a row for each epic");
    const rows = [...sheet.querySelectorAll<HTMLElement>(".rpick-row")];
    const deltaRow = rows.find((candidate) => candidate.querySelector(".t")?.textContent === "delta");
    assert.ok(deltaRow !== undefined, "the sheet has the delta row");
    await press(deltaRow);
    assert.deepEqual(lastReport(reported), { address: epicAddress("delta"), mode: "push" }, "the row's epic is pushed");
    assert.equal(sheetOf(container), null, "the sheet is shut");
  });
});

// onListSession opens the conversation of s1 with no epic, so its strip holds only the list tiles. Under 760px a
// conversation draws only once it is opened, as its address opens it.
const onListSession: Setup = { rows: [steererRow()], address: sessionAddress };

test("TileStrip: a list tile pushes the list's address and draws the list in a sheet over the conversation", (t) =>
  withPhone(t.signal, onListSession, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settle(t.signal, () => stripTiles(container).length === 2, "the two list tiles");
    await press(tileTitled(container, "agents:"));
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
    await settle(t.signal, () => stripTiles(container).length === 2, "the two list tiles");
    await press(tileTitled(container, "agents:"));
    const sheet = sheetOf(container);
    assert.ok(sheet !== null, "the sheet is drawn before Escape");
    await pressEscape(sheet);
    assert.equal(sheetOf(container), null, "Escape shuts the sheet");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" }, "the session is pushed");
  }));

test("TileStrip: a pinned conversation's column draws no strip, so a tile always acts on the conversation it names", (t) =>
  withPhone(t.signal, onSession, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "|", bubbles: true }));
    });
    assert.ok(container.querySelector('section[aria-label="pinned reader"]') !== null, "the row is pinned");
    assert.equal(
      container.querySelector('section[aria-label="pinned reader"] .tilestrip'),
      null,
      "the pinned column draws no strip",
    );
    assert.equal(container.querySelectorAll(".tilestrip").length, 1, "only the cursor's conversation draws its strip");
  }));
