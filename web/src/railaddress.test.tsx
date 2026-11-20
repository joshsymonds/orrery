import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import {
  type Epic,
  EpicSchema,
  EpicStepSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token RailAddress, SidePanel or WorkRail: RailAddress is an epic's or a task's address
// drawing its steerer's conversation beside the panel, SidePanel is the panel and its tile, and WorkRail is the rail the
// conversation's things are drawn in. Each test mounts App at a window width of its own, since the rail and the layout
// read the width when App mounts.

const now = 1000;
const repository = "widgets-5d0c1a7";

type Clients = ReturnType<typeof createClients>;

// probe records what the page asked of the server, which session each conversation stream followed, and what App
// reported as its address, with the mode each report named. waiting holds the checks waiting for a stream to follow.
interface Probe {
  followed: string[];
  calls: string[];
  waiting: { id: string; ready: () => void }[];
  reported: { address: Address; mode: string }[];
}

// Mount is what a check gets: the container App draws into, the probe, and rerender, which draws App again with the
// setup given.
interface Mount {
  container: HTMLElement;
  probe: Probe;
  rerender: (setup: Setup) => Promise<void>;
}

interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  address?: Address;
  host?: HostStatusResponse;
}

// followed resolves once a conversation stream has been asked for the session id. A check awaits it rather than reading
// the probe at once, since the stream's request is made after the effect that starts it.
function followed(probe: Probe, id: string): Promise<void> {
  return new Promise((ready) => {
    if (probe.followed.includes(id)) {
      ready();
    } else {
      probe.waiting.push({ id, ready });
    }
  });
}

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open. A stream's request is
// one connect envelope, a flag byte and a length, then its JSON, which names the session it follows.
async function fakeFetch(probe: Probe, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const request = new Request(input, init);
  const path = new URL(request.url).pathname;
  if (path.endsWith("/WatchConversation")) {
    const body = JSON.parse((await request.text()).slice(5)) as { id?: string; pane?: string };
    const id = body.id ?? body.pane ?? "";
    probe.followed.push(id);
    probe.waiting = probe.waiting.filter((waiter) => {
      if (waiter.id !== id) {
        return true;
      }
      waiter.ready();
      return false;
    });
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  probe.calls.push(path);
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

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

// steererRow is the session row s1, which steers every epic these tests name with steering s1.
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

// epicOf is a live epic in the repository, steered by the session steering, with the fields init gives.
function epicOf(slug: string, steering: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering,
    ...init,
  });
}

// view is the epics view of the epics, each with a detail whose summary is "Ship" and its slug. init gives an epic's
// detail by its slug.
function view(epics: Epic[], init: Record<string, MessageInitShape<typeof EpicsGetResponseSchema>> = {}): EpicsView {
  const details = epics.map((epic): [string, EpicsGetResponse] => [
    epic.id,
    create(EpicsGetResponseSchema, { epic, summary: `Ship ${epic.slug}`, ...(init[epic.slug] ?? {}) }),
  ]);
  return epicsView(epics, new Map(details));
}

function makeStep(id: string, init: MessageInitShape<typeof EpicStepSchema> = {}) {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false, ...init });
}

// epicAddress is the address of an epic's page, or of one of its sub-pages.
function epicAddress(
  slug: string,
  page: Extract<Address["entry"], { kind: "epic" }>["page"] = { kind: "epic" },
): Address {
  return { view: "inbox", entry: { kind: "epic", id: `${repository}/${slug}`, page } };
}

// steererAddress is the address of the steerer's session row.
const steererAddress: Address = { view: "inbox", entry: { kind: "session", id: "s1" } };

// agentsAddress is the address of the steerer's session row with its agents list open in the panel.
const agentsAddress: Address = { view: "inbox", entry: { kind: "session", id: "s1", panel: "agents" } };

// draw renders App over the setup, with the probe recording its address reports.
function draw(container: HTMLElement, clients: Clients, probe: Probe, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={now}
        clients={clients}
        epics={setup.epics}
        address={setup.address}
        host={setup.host ?? null}
        onAddress={(address, mode) => probe.reported.push({ address, mode })}
      />,
      container,
    );
  });
}

// withRail mounts App at width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed when the
// test ends, or as soon as signal aborts first.
async function withRail(
  signal: AbortSignal,
  width: number,
  setup: Setup,
  check: (mount: Mount) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  window.innerWidth = width;
  const container = document.createElement("div");
  document.body.append(container);
  const probe: Probe = { followed: [], calls: [], waiting: [], reported: [] };
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
    await draw(container, clients, probe, setup);
    await check({ container, probe, rerender: (next) => draw(container, clients, probe, next) });
  } finally {
    await teardown();
  }
}

// railTiles is each tile the right rail draws, in order.
function railTiles(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".rail .tile-w")];
}

// tileNamed is the rail's tile whose name is short.
function tileNamed(container: HTMLElement, short: string): HTMLElement {
  const tile = railTiles(container).find((candidate) => candidate.querySelector(".cap")?.textContent === short);
  assert.ok(tile !== undefined, `no rail tile named ${short}`);
  return tile;
}

// litTile is the rail's one lit tile, or null when none is lit.
function litTile(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(".rail .tile-w.on");
}

// panelOf is the epic panel, or null when none is drawn.
function panelOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('aside.rpanel[aria-label="epic"]');
}

// panelHolds is whether the epic panel draws the epic with the slug, which its page names in its title row.
function panelHolds(container: HTMLElement, slug: string): boolean {
  return panelOf(container)?.textContent?.includes(slug) === true;
}

// readerShown is whether the cursor's reader section is drawn.
function readerShown(container: HTMLElement): boolean {
  return container.querySelector('section[aria-label="reader"]:not([hidden])') !== null;
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// cssDeclarations is the declarations of the one rule in style.css whose selector is selector, with comments removed.
function cssDeclarations(selector: string): string {
  const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].filter(([, selectors]) => selectors.trim() === selector);
  assert.equal(rules.length, 1, `style.css has one ${selector} rule`);
  return rules[0][2];
}

// declared is the value of property in a rule's declarations, or undefined when the rule does not set it.
function declared(declarations: string, property: string): string | undefined {
  return declarations
    .split(";")
    .map((declaration) => declaration.trim())
    .find((declaration) => declaration.startsWith(`${property}:`))
    ?.slice(property.length + 1)
    .trim();
}

// lastReport is the address App reported last, with the mode it named.
function lastReport(probe: Probe): { address: Address; mode: string } {
  const report = probe.reported.at(-1);
  assert.ok(report !== undefined, "App reported no address");
  return report;
}

// The two epics the steerer s1 steers, alpha and beta.
const alpha = epicOf("alpha", "s1");
const beta = epicOf("beta", "s1");

test("RailAddress: an epic's address draws the conversation that steers it in the middle, the epic in the panel, and its tile lit", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async (mount) => {
    const { container, probe } = mount;
    await followed(probe, "s1");
    assert.equal(readerShown(container), true, "the steerer's conversation is drawn in the middle");
    assert.ok(panelOf(container) !== null, "the epic is drawn in aside.rpanel");
    assert.equal(panelHolds(container, "alpha"), true, "the panel holds the epic");
    assert.equal(litTile(container)?.querySelector(".cap")?.textContent, "alpha", "the epic's tile is lit");
  }));

test("RailAddress: a task's address draws its steerer's conversation in the middle, with the phase page in the panel", (t) => {
  const run = makeStep("run1", { task: "T1", stage: "gate", doing: "checking the parser" });
  const phase = view([alpha], {
    alpha: {
      buildTasks: [{ id: "T1", title: "Parser", status: "dispatched", requirements: ["R1"] }],
      requirements: [{ id: "R1", text: "The parser parses" }],
      steps: [run],
    },
  });
  return withRail(
    t.signal,
    1600,
    {
      rows: [steererRow()],
      epics: phase,
      address: epicAddress("alpha", { kind: "phase", task: "T1", phase: "checking" }),
    },
    async (mount) => {
      const { container, probe } = mount;
      await followed(probe, "s1");
      assert.equal(readerShown(container), true, "the steerer's conversation is drawn in the middle");
      assert.equal(panelOf(container)?.querySelector(".phase-output") !== null, true, "the phase page is in the panel");
      const crumbs = panelOf(container)?.querySelector(".title-row .crumbs")?.textContent ?? "";
      assert.equal(crumbs.includes("alpha"), true, "the panel's crumbs name the epic");
      assert.equal(crumbs.includes("T1 · checking"), true, "the panel's crumbs name the task and its phase");
      assert.ok(panelOf(container)?.querySelector(".epic-page") === null, "the epic record is not drawn in the panel");
      assert.equal(litTile(container)?.querySelector(".cap")?.textContent, "alpha", "the epic's tile is lit");
    },
  );
});

test("RailAddress: an epic no conversation steers draws the no-conversation line in the middle, and no reader", (t) =>
  withRail(
    t.signal,
    1600,
    { rows: [steererRow()], epics: view([epicOf("alpha", "s9")]), address: epicAddress("alpha") },
    (mount) => {
      const { container } = mount;
      assert.equal(readerShown(container), false, "no reader is drawn");
      assert.equal(container.textContent?.includes("No conversation steers this epic."), true, "the line is drawn");
      assert.equal(panelOf(container) !== null, true, "the epic is still drawn in the panel");
    },
  ));

test("SidePanel: pressing an epic tile opens its panel, with the address of that epic pushed", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha, beta]), address: steererAddress }, async (mount) => {
    const { container, probe } = mount;
    assert.equal(panelOf(container), null, "no panel before the press");
    await press(tileNamed(container, "alpha"));
    assert.equal(panelHolds(container, "alpha"), true, "the panel holds alpha");
    assert.deepEqual(lastReport(probe), { address: epicAddress("alpha"), mode: "push" });
  }));

test("SidePanel: pressing another epic's tile switches the panel to that epic, with its address pushed", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha, beta]), address: epicAddress("alpha") }, async (mount) => {
    const { container, probe } = mount;
    await press(tileNamed(container, "beta"));
    assert.equal(panelHolds(container, "beta"), true, "the panel holds beta");
    assert.equal(panelHolds(container, "alpha"), false, "the panel no longer holds alpha");
    assert.equal(litTile(container)?.querySelector(".cap")?.textContent, "beta", "beta's tile is lit");
    assert.deepEqual(lastReport(probe), { address: epicAddress("beta"), mode: "push" });
  }));

test("SidePanel: pressing the lit tile closes the panel and moves to the steerer's conversation, with the address pushed", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async (mount) => {
    const { container, probe } = mount;
    assert.ok(panelOf(container) !== null, "the panel is open before the press");
    await press(tileNamed(container, "alpha"));
    assert.equal(panelOf(container), null, "the panel is closed");
    assert.equal(litTile(container), null, "no tile is lit");
    assert.deepEqual(lastReport(probe), { address: steererAddress, mode: "push" });
  }));

test("SidePanel: Close closes the panel and moves to the steerer's conversation, with the address pushed", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async (mount) => {
    const { container, probe } = mount;
    const close = container.querySelector<HTMLElement>('aside.rpanel .panel-ctl button[aria-label="Close the panel"]');
    assert.ok(close !== null, "the panel has a Close button");
    await press(close);
    assert.equal(panelOf(container), null, "the panel is closed");
    assert.deepEqual(lastReport(probe), { address: steererAddress, mode: "push" });
  }));

test("SidePanel: the panel holds the lit tile's epic, and its card takes the tile's tone", (t) =>
  withRail(
    t.signal,
    1600,
    {
      rows: [steererRow()],
      epics: view([alpha], { alpha: { questions: [{ id: "Q1", text: "Use Postgres?" }] } }),
      address: epicAddress("alpha"),
    },
    (mount) => {
      const { container } = mount;
      assert.equal(litTile(container)?.querySelector(".tile")?.classList.contains("attention"), true, "the tile asks");
      assert.equal(container.querySelector("aside.rpanel .rcard")?.classList.contains("attention"), true, "the card asks too");
    },
  ));

test("SidePanel: the layout takes the overlay class at 1000 wide, with the panel open", (t) =>
  withRail(t.signal, 1000, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, (mount) => {
    assert.equal(mount.container.querySelector(".panes")?.classList.contains("overlay"), true);
  }));

test("SidePanel: the layout takes no overlay class at 1600 wide, with the panel open", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, (mount) => {
    assert.equal(mount.container.querySelector(".panes")?.classList.contains("overlay"), false);
  }));

test("SidePanel: Back, the address moved to the steerer's conversation, closes the panel", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async (mount) => {
    const { container, rerender } = mount;
    assert.ok(panelOf(container) !== null, "the panel is open before Back");
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: steererAddress });
    assert.equal(panelOf(container), null, "the panel is closed");
    assert.equal(readerShown(container), true, "the steerer's conversation is drawn");
  }));

test("SidePanel: a remount at the epic's address draws the panel again", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async (mount) => {
    const { container, rerender } = mount;
    await act(() => {
      render(null, container);
    });
    assert.equal(panelOf(container), null, "no panel once App is unmounted");
    await rerender({ rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") });
    assert.equal(panelHolds(container, "alpha"), true, "the panel is drawn again");
  }));

test("WorkRail: an epic the conversation steers that the epics prop adds becomes a tile in the rail", (t) =>
  withRail(t.signal, 1600, { rows: [steererRow()], epics: view([alpha]), address: steererAddress }, async (mount) => {
    const { container, rerender } = mount;
    assert.equal(railTiles(container).length, 1, "one epic is one tile");
    await rerender({ rows: [steererRow()], epics: view([alpha, beta]), address: steererAddress });
    assert.equal(railTiles(container).length, 2, "the added epic is a tile");
    assert.equal(tileNamed(container, "beta").querySelector(".cap")?.textContent, "beta", "the added tile is beta");
  }));

test("SidePanel: a digit with an epic's question open in the panel answers nothing in the steering conversation", (t) => {
  const waitingSteerer = row({
    key: "pane:box:%3",
    id: "%3",
    state: "waiting",
    question: "Ship it?",
    questionHash: "qh-ship",
    archiveId: "s1",
    archiveKind: "session",
  });
  const epics = view([epicOf("alpha", "s1")], {
    alpha: {
      questions: [
        {
          id: "Q1",
          text: "Which database?",
          choices: [
            { id: "postgres", does: "Use PostgreSQL for the store." },
            { id: "sqlite", does: "Use SQLite for the store." },
          ],
        },
      ],
    },
  });
  return withRail(t.signal, 1600, { rows: [waitingSteerer], epics, address: epicAddress("alpha") }, async ({ container, probe }) => {
    assert.ok(panelOf(container) !== null, "the epic is open in the panel");
    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "1", bubbles: true }));
    });
    assert.deepEqual(
      probe.calls.filter((path) => path.endsWith("/SendKeys") || path.endsWith("/Answer")),
      [],
      "the steerer's waiting question is not answered by the panel's digit",
    );
  });
});

// arrived is the epic's address over the steerer's rows and epic, which arrive after App mounts, as the page loads them: the
// address is pending until the rows and epics hold its epic, and then opens it.
const arrived: Setup = { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") };

test("RailAddress: an epic's address hides the inbox at 1600, as a row's address does", (t) =>
  withRail(t.signal, 1600, { address: epicAddress("alpha") }, async (mount) => {
    await mount.rerender(arrived);
    assert.ok(mount.container.querySelector('.panes[data-inbox="hidden"]') !== null, "the inbox is hidden");
  }));

test("RailAddress: an epic's address hides the inbox at 1000, where the panel overlays the page", (t) =>
  withRail(t.signal, 1000, { address: epicAddress("alpha") }, async (mount) => {
    await mount.rerender(arrived);
    await followed(mount.probe, "s1");
    assert.ok(mount.container.querySelector(".panes.overlay") !== null, "the layout is overlaid");
    assert.ok(mount.container.querySelector('.panes[data-inbox="hidden"]') !== null, "the inbox is hidden");
  }));

test("RailAddress: at 1000 the overlaid panel starts one rail width past the inbox's track, leaving the reader beside it", (t) =>
  withRail(t.signal, 1000, { address: epicAddress("alpha") }, async (mount) => {
    await mount.rerender(arrived);
    await followed(mount.probe, "s1");
    assert.equal(readerShown(mount.container), true, "the steerer's conversation is drawn");
    assert.equal(
      declared(cssDeclarations(".panes.overlay > .rpanel"), "left"),
      "calc(var(--inbox-track) + var(--o-layout-rail-width))",
      "the panel's left edge is one rail width past the inbox's track",
    );
  }));

test("RailAddress: at a phone's width the conversation's meters draw above its tile strip", (t) =>
  withRail(
    t.signal,
    412,
    {
      rows: [steererRow()],
      epics: view([alpha]),
      address: steererAddress,
      host: create(HostStatusResponseSchema, { host: "box" }),
    },
    (mount) => {
      const meters = mount.container.querySelector(".pane-strip");
      const strip = mount.container.querySelector(".tilestrip");
      assert.ok(meters !== null, "the meters are drawn");
      assert.ok(strip !== null, "the tile strip is drawn");
      assert.equal(
        (meters.compareDocumentPosition(strip) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0,
        true,
        "the meters come before the tile strip",
      );
    },
  ));

test("RailAddress: a list's sheet names the list once, in its title, and draws no head of its own", (t) =>
  withRail(t.signal, 412, { rows: [steererRow()], address: agentsAddress }, (mount) => {
    const sheet = mount.container.querySelector('section.sheet[role="dialog"]');
    assert.ok(sheet !== null, "the list is drawn in a sheet");
    assert.equal(sheet.getAttribute("aria-label"), "Sub-agents", "the sheet is titled with the list");
    assert.ok(sheet.querySelector(".plist h2") === null, "the list draws no head of its own");
  }));

test("WorkRail: a list tile's icon is sized and stroked as round 6's icon, from the tokens", () => {
  const rule = cssDeclarations(".tile > .icon");
  assert.equal(declared(rule, "width"), "var(--o-size-icon)", "the icon is the icon size wide");
  assert.equal(declared(rule, "height"), "var(--o-size-icon)", "the icon is the icon size high");
  assert.equal(declared(rule, "fill"), "none", "the icon is not filled");
  assert.equal(declared(rule, "stroke"), "currentColor", "the icon is stroked in the current colour");
  assert.equal(declared(rule, "stroke-linecap"), "round", "the stroke's ends are round");
  assert.equal(declared(rule, "stroke-linejoin"), "round", "the stroke's joins are round");
});
