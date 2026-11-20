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
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name carries the token WorkLists or SidePanel: WorkLists is the agents and running tiles a session's stream
// gives the rail and the rows each list's panel holds, and SidePanel is the list's panel, its address and the keys that
// shut and reopen it. A session's stream is a fake the test feeds, and every time is a fixture's instant, so no test reads
// the wall clock. Each test mounts App at a window width of its own.

const now = Date.parse(liveEpoch) / 1000;
const repository = "widgets-5d0c1a7";
const watchPath = "/orrery.v1.Sessions/WatchConversation";
const promptText = "a message the stream holds";
const tones = ["attention", "live", "quiet"];

type Clients = ReturnType<typeof createClients>;

// Report is one address App reported, with the mode it named.
interface Report {
  address: Address;
  mode: string;
}

// FakeServer answers WatchConversation with a stream per followed conversation, which the test feeds through emit. Each
// unary call answers with an empty message. A conversation's stream is keyed by its session id, or by its pane id.
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

// envelope frames a stream message as Connect's JSON streaming frames it: a flag of 0, the payload's length, the payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = 0;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// delta is a WatchConversation message that holds one prompt, which the reader draws as text, and the background tasks
// the session reports. The server sends the whole background list with each state it sends.
function delta(background: BackgroundTask[]): object {
  const prompt: ConversationItem = create(ConversationItemSchema, { kind: "prompt", text: promptText });
  const state = create(ConversationStateSchema, { background });
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

// background holds two running agents, one ended agent, one running shell and one failed shell.
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
  task({ id: "a3", description: "Old scan", type: "agent", status: "done", started: at("@14:30:00"), ended: at("@14:31:00") }),
  task({
    id: "s1",
    description: "Watch the build",
    type: "shell",
    status: "running",
    activity: "tail -f build.log",
    started: at("@14:37:58"),
  }),
  task({ id: "s2", description: "Dead server", type: "shell", status: "failed", started: at("@14:30:00"), ended: at("@14:31:00") }),
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

// steererRow is the headless session s1, which steers every epic these tests name with steering s1.
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

// Mount is what a check gets: the container App draws into, the server it follows, the addresses App reported, and
// rerender, which draws App again with the setup given.
interface Mount {
  container: HTMLElement;
  server: FakeServer;
  reported: Report[];
  rerender: (setup: Setup) => Promise<void>;
}

// draw renders App over the setup, with its address reports recorded.
function draw(container: HTMLElement, clients: Clients, reported: Report[], setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        error=""
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

// withLists mounts App at width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed when the
// test ends, or as soon as signal aborts first.
async function withLists(
  signal: AbortSignal,
  width: number,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
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
    await check({ container, server, reported, rerender: (next) => draw(container, server.clients, reported, next) });
  } finally {
    await teardown();
  }
}

// settle waits until check holds, each round inside act, so the renders a message causes are applied before the next check.
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

// pressKey sends one keydown to the document, as a key press does.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  });
}

// railTiles is each tile the right rail draws, in order.
function railTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .tile-w")];
}

// tileTitled is the rail's tile whose title starts with prefix: an epic's short name, or a list's key and a colon.
function tileTitled(container: HTMLElement, prefix: string): HTMLElement {
  const tile = railTiles(container).find((candidate) => candidate.getAttribute("title")?.startsWith(prefix) === true);
  assert.ok(tile !== undefined, `no rail tile titled ${prefix}`);
  return tile;
}

// litTitles are the titles of the rail's lit tiles, in order.
function litTitles(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .tile-w.on")].map((tile) => tile.getAttribute("title") ?? "");
}

// litOnly asserts that exactly one rail tile is lit, and that its title starts with prefix.
function litOnly(container: HTMLElement, prefix: string): void {
  const lit = litTitles(container);
  assert.equal(lit.length, 1, `one lit tile, not ${lit.length}`);
  assert.ok(lit[0].startsWith(prefix), `the lit tile is ${prefix}, not ${lit[0]}`);
}

// listPanelOf is the list panel named label, or null when it is not drawn.
function listPanelOf(container: HTMLElement, label: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`aside.rpanel[aria-label="${label}"]`);
}

// epicPanelOf is the epic panel, or null when it is not drawn.
function epicPanelOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('aside.rpanel[aria-label="epic"]');
}

// panelRows are the titles of the rows the one drawn panel lists.
function panelRows(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>("aside.rpanel .plist .row .title")].map(
    (title) => title.textContent ?? "",
  );
}

// frameClasses are the classes of a panel's frame and of its card, without the card's tone, so two panels compare as
// the same frame.
function frameClasses(panel: HTMLElement): string {
  const card = panel.querySelector(".rcard");
  const cardFrame = card === null ? "" : [...card.classList].filter((name) => !tones.includes(name)).join(" ");
  return `${panel.className} | ${cardFrame}`;
}

// lastReport is the address App reported last, with the mode it named.
function lastReport(reported: Report[]): Report {
  const report = reported.at(-1);
  assert.ok(report !== undefined, "App reported no address");
  return report;
}

// listTile is the rail's tile of a list, which is lit while its panel is open.
function listTile(container: HTMLElement, kind: "agents" | "running"): HTMLElement {
  return tileTitled(container, `${kind}:`);
}

// settleTiles waits until the rail draws the two list tiles the stream's background gives.
function settleTiles(signal: AbortSignal, container: HTMLElement): Promise<void> {
  return settle(signal, () => railTiles(container).length === 2, "the two list tiles");
}

test("WorkLists: a session's agents and running tiles come from its stream, one tile each, with the running count and no ended task", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    assert.deepEqual(
      railTiles(container).map((tile) => tile.getAttribute("title")),
      ["agents: 2 running", "running: 1 left running"],
      "the agents tile counts the running agents, and the running tile the running shell",
    );
  }));

test("WorkLists: a list's panel holds its running tasks, with the ended and failed ones absent", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(listTile(container, "agents"));
    assert.deepEqual(panelRows(container), ["Map the rota", "Check the holds"], "the agents panel lists its two running agents");
    await press(listTile(container, "running"));
    assert.deepEqual(panelRows(container), ["Watch the build"], "the running panel lists its one running shell");
  }));

test("WorkLists: a pane's rail draws neither group, though its stream holds running tasks", (t) =>
  withLists(t.signal, 1600, { rows: [row({ key: "pane:box:%3", id: "%3", kind: "pane" })] }, async ({ container, server }) => {
    await feed(t.signal, server, "%3", background);
    await settle(t.signal, () => container.textContent?.includes(promptText) === true, "the pane's stream arrives");
    assert.equal(railTiles(container).length, 0, "a pane draws no list tile");
  }));

test("SidePanel: pressing a list's tile opens its panel, with the address pushed to the session's list segment", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    assert.ok(listPanelOf(container, "Sub-agents") === null, "no panel before the press");
    await press(listTile(container, "agents"));
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "the agents panel is drawn");
    assert.deepEqual(lastReport(reported), { address: agentsAddress, mode: "push" });
  }));

test("SidePanel: a list's panel draws in the epic panel's frame, and its card takes the live tone of agents", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container, server }) => {
    await feed(t.signal, server, "s1", background);
    await settle(t.signal, () => railTiles(container).length === 3, "the two list tiles and the epic's");
    const epicPanel = epicPanelOf(container);
    assert.ok(epicPanel !== null, "the epic panel is drawn");
    await press(listTile(container, "agents"));
    const listPanel = listPanelOf(container, "Sub-agents");
    assert.ok(listPanel !== null, "the agents panel is drawn");
    assert.equal(frameClasses(listPanel), frameClasses(epicPanel), "the two panels draw in the same frame");
    assert.equal(listPanel.querySelector(".rcard")?.classList.contains("live"), true, "agents take the live tone");
  }));

test("SidePanel: the lit tile shuts the list panel, and the address goes back to the session's own", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(listTile(container, "agents"));
    assert.equal(listTile(container, "agents").classList.contains("on"), true, "the agents tile is lit");
    await press(listTile(container, "agents"));
    assert.ok(listPanelOf(container, "Sub-agents") === null, "the panel is shut");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" });
  }));

test("SidePanel: Close shuts the list panel, and the address goes back to the session's own", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(listTile(container, "running"));
    const close = container.querySelector<HTMLElement>('aside.rpanel .panel-ctl button[aria-label="Close the panel"]');
    assert.ok(close !== null, "the panel has a Close button");
    await press(close);
    assert.ok(listPanelOf(container, "Left running") === null, "the panel is shut");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" });
  }));

test("SidePanel: ] shuts an open list panel, and with none open reopens the list it held", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(listTile(container, "agents"));
    await pressKey("]");
    assert.ok(listPanelOf(container, "Sub-agents") === null, "] shuts the panel");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" });
    await pressKey("]");
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "] reopens the agents panel it held");
    assert.deepEqual(lastReport(reported), { address: agentsAddress, mode: "push" });
  }));

test("SidePanel: ] with the epic panel open shuts it, and with none open reopens the epic it held", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container, reported }) => {
    assert.ok(epicPanelOf(container) !== null, "the epic panel is open before the key");
    await pressKey("]");
    assert.ok(epicPanelOf(container) === null, "] shuts the epic panel");
    assert.deepEqual(lastReport(reported), { address: sessionAddress, mode: "push" });
    await pressKey("]");
    assert.ok(epicPanelOf(container) !== null, "] reopens the epic panel it held");
    assert.deepEqual(lastReport(reported), { address: epicAddress("alpha"), mode: "push" });
  }));

test("SidePanel: Back, the address moved to the session's own, shuts the list panel", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()] }, async ({ container, server, rerender }) => {
    await feed(t.signal, server, "s1", background);
    await settleTiles(t.signal, container);
    await press(listTile(container, "agents"));
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "the panel is open before Back");
    await rerender({ rows: [steererRow()], address: sessionAddress });
    assert.ok(listPanelOf(container, "Sub-agents") === null, "the panel is shut");
  }));

test("SidePanel: a remount at the list's address draws its panel again", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()], address: agentsAddress }, async ({ container, rerender }) => {
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "the agents panel is drawn at the address");
    await act(() => {
      render(null, container);
    });
    assert.ok(listPanelOf(container, "Sub-agents") === null, "no panel once App is unmounted");
    await rerender({ rows: [steererRow()], address: agentsAddress });
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "the agents panel is drawn again");
  }));

test("SidePanel: an epic tile press while a list is open switches the panel to the epic, and a list's tile switches back", (t) =>
  withLists(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container, server, reported }) => {
    await feed(t.signal, server, "s1", background);
    await settle(t.signal, () => railTiles(container).length === 3, "the two list tiles and the epic's");
    await press(listTile(container, "agents"));
    assert.ok(epicPanelOf(container) === null, "the epic panel is shut while the list is open");
    litOnly(container, "agents:");
    await press(tileTitled(container, "alpha:"));
    assert.ok(listPanelOf(container, "Sub-agents") === null, "the list panel is shut for the epic");
    assert.ok(epicPanelOf(container) !== null, "the epic panel is open");
    litOnly(container, "alpha:");
    assert.deepEqual(lastReport(reported), { address: epicAddress("alpha"), mode: "push" });
    await press(listTile(container, "agents"));
    assert.ok(epicPanelOf(container) === null, "the epic panel is shut for the list");
    assert.ok(listPanelOf(container, "Sub-agents") !== null, "the agents panel is open again");
    litOnly(container, "agents:");
    assert.deepEqual(lastReport(reported), { address: agentsAddress, mode: "push" });
  }));

test("SidePanel: ] reopens an epic shut by Close when no conversation steers it", (t) => {
  const unsteered = epicOf("beta", "");
  return withLists(t.signal, 1600, { rows: [], epics: view([unsteered]), address: epicAddress("beta") }, async ({ container }) => {
    assert.ok(epicPanelOf(container) !== null, "the epic opens beside the no-conversation middle");
    const close = container.querySelector<HTMLElement>('aside.rpanel button[aria-label="Close the panel"]');
    assert.ok(close !== null, "the panel has its Close");
    await press(close);
    assert.ok(epicPanelOf(container) === null, "Close shuts the panel");
    await pressKey("]");
    assert.ok(epicPanelOf(container) !== null, "] reopens the epic it held");
  });
});
