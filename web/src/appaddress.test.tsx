import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { EpicSchema, EpicStepSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address, View } from "./router";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";

const now = 1000;

type Clients = ReturnType<typeof createClients>;

// What one test renders App over: the rows and epics the poll holds, the clock it reads, and the address the prop
// holds.
interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  now?: number;
  address?: Address;
}

// fakeFetch answers each unary call with an empty message and holds each stream open, so no call ends or retries during
// a test.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>(), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
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

// waitingRow is a session row that needs its answer, which the inbox draws first. The cursor rests on the
// first entry it draws, so a test's address is never the entry App shows with no address at all.
function waitingRow(): Row {
  return row({
    key: "session:s0",
    id: "s0",
    kind: "session",
    host: "",
    label: "waiting work",
    project: "/work/api",
    state: "waiting",
  });
}

// The waiting row, then a session row whose id is s1, and a pane row whose pane id is %3 on host box.
function rows(): Row[] {
  return [
    waitingRow(),
    row({ key: "session:s1", id: "s1", kind: "session", host: "", label: "auth work", project: "/work/api" }),
    row({ key: "pane:box:%3", id: "%3", kind: "pane", host: "box", label: "pane work", project: "/work/web" }),
  ];
}

const widgetsRepo = "widgets-5d0c1a7";
const widgetsFirst = `${widgetsRepo}/first`;
const widgetsFirstKey = `epic:${widgetsFirst}`;
const endedAt = 99280n;
const endedNow = 100000;

// makeEpic is a synthetic epic whose id is its repository and slug joined, as the record names it.
function makeEpic(repository: string, slug: string, init: MessageInitShape<typeof EpicSchema> = {}) {
  return create(EpicSchema, { id: `${repository}/${slug}`, repository, slug, outcome: "", live: true, ...init });
}

function makeStep(id: string, init: MessageInitShape<typeof EpicStepSchema> = {}) {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false, ...init });
}

// firstEpic is a live epic whose running step is s1, with the detail given. A step the detail lacks still reads from
// the epic's running steps, so the detail is only what the epic page draws.
function firstEpic(init: MessageInitShape<typeof EpicsGetResponseSchema> = {}): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")] });
  return epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, ...init })]]));
}

// acceptedContract is an accepted contract, whose summary the epic page draws with the Contract door that opens it.
const acceptedContract = { title: "Show the accepted contract", approved: "approved line", intent: "the intent" };

// summaryEpic is a live orreryd epic accepted with its summary and contract, so its page draws the Contract door.
function summaryEpic(): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { source: "orreryd", outcome: "accepted", runningSteps: [makeStep("s1")] });
  return epicsView(
    [epic],
    new Map([[epic.id, create(EpicsGetResponseSchema, { epic, summary: "Ship the demo", contract: acceptedContract })]]),
  );
}

// phaseEpic is a live epic whose task T1 runs its gate step run1, which puts T1 in checking.
function phaseEpic(): EpicsView {
  const run = makeStep("run1", { task: "T1", stage: "gate", doing: "checking the parser" });
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [run] });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    buildTasks: [{ id: "T1", title: "Parser", status: "dispatched", requirements: ["R1"] }],
    requirements: [{ id: "R1", text: "The parser parses" }],
    steps: [run],
  });
  return epicsView([epic], new Map([[epic.id, detail]]));
}

// shippedEpic is an orreryd epic that is not live, released and seen after its ending, so it sits in the ended earlier
// line.
function shippedEpic(): EpicsView {
  const shipped = makeEpic(widgetsRepo, "shipped", {
    source: "orreryd",
    live: false,
    outcome: "released",
    phaseAt: endedAt,
    seen: endedAt + 60n,
  });
  return epicsView([shipped], new Map());
}

// show renders App over the setup's rows and epics, holding address as its prop.
function show(container: HTMLElement, clients: Clients, setup: Setup, address?: Address): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={setup.now ?? now}
        clients={clients}
        epics={setup.epics}
        address={address}
      />,
      container,
    );
  });
}

// withApp mounts App over a fake transport in a fresh DOM and runs check, which gets rerender to render App again with
// another address, and with the rows and epics next when given. App is unmounted and the DOM removed when the test
// ends, or as soon as signal aborts first.
async function withApp(
  signal: AbortSignal,
  setup: Setup,
  check: (container: HTMLElement, rerender: (address: Address, next?: Setup) => Promise<void>) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch }));
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(container, clients, setup, setup.address);
    await check(container, (address, next = setup) => show(container, clients, next, address));
  } finally {
    await teardown();
  }
}

// shownView is the view the list draws, which its rows name in data-list.
function shownView(container: HTMLElement): string | null {
  return container.querySelector(".inbox-rows")?.getAttribute("data-list") ?? null;
}

// cursorKey is the key of the entry the cursor rests on, or null when no entry is drawn as the cursor's.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// readerShown is whether the cursor's reader section is drawn.
function readerShown(container: HTMLElement): boolean {
  return container.querySelector('section[aria-label="reader"]:not([hidden])') !== null;
}

// epicPageShown is whether the page section is drawn: the cursor rests on an epic entry and the page is not closed.
function epicPageShown(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"]:not([hidden])') !== null;
}

// epicPageDrawn is whether the page section holds the epic page itself, rather than a page over it.
function epicPageDrawn(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] .epic-page') !== null;
}

// pageClass is the class of the page section, which names the move the page made last: drill, leave, or none.
function pageClass(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"]')?.getAttribute("class") ?? null;
}

// clickButton clicks the button in scope whose text is label, and lets Preact draw what the click changed.
async function clickButton(container: HTMLElement, scope: string, label: string): Promise<void> {
  const target = [...container.querySelectorAll<HTMLButtonElement>(`${scope} button`)].find(
    (button) => button.textContent === label,
  );
  assert.ok(target !== undefined, `no ${label} button in ${scope}`);
  await act(() => {
    target.dispatchEvent(new Event("click"));
  });
}

// clickContractDoor presses the Contract door of the epic page.
async function clickContractDoor(container: HTMLElement): Promise<void> {
  const door = container.querySelector<HTMLButtonElement>(".epic-page .door.contract");
  assert.ok(door !== null, "the epic page draws its Contract door");
  await act(() => {
    door.dispatchEvent(new Event("click"));
  });
}

// backFromPage presses the back button of the page over the epic page.
async function backFromPage(container: HTMLElement): Promise<void> {
  await clickButton(container, 'section.page[aria-label="epic"] .title-row', "‹");
}

// The inbox is App's default view, so its test reaches it from the archived view. The archived view mounts on its own
// address.
test("InboxTree: an archived address with no entry shows the archived view", (t) =>
  withApp(t.signal, { rows: rows(), address: { view: "archived", entry: null } }, (container) => {
    assert.equal(shownView(container), "archived");
  }));

test("InboxTree: an inbox address with no entry shows the inbox from the archived view", (t) =>
  withApp(t.signal, { rows: rows(), address: { view: "archived", entry: null } }, async (container, rerender) => {
    assert.equal(shownView(container), "archived");
    await rerender({ view: "inbox", entry: null });
    assert.equal(shownView(container), "inbox");
  }));

test("HashRoute: a session address puts the cursor on that session's row and shows its reader", (t) =>
  withApp(
    t.signal,
    { rows: rows(), address: { view: "inbox", entry: { kind: "session", id: "s1" } } },
    (container) => {
      assert.equal(cursorKey(container), "session:s1");
      assert.equal(readerShown(container), true);
    },
  ));

test("HashRoute: a pane address with pane id %3 puts the cursor on that pane's row and shows its reader", (t) =>
  withApp(
    t.signal,
    { rows: rows(), address: { view: "inbox", entry: { kind: "pane", host: "box", pane: "%3" } } },
    (container) => {
      assert.equal(cursorKey(container), "pane:box:%3");
      assert.equal(readerShown(container), true);
    },
  ));

test("HashRoute: a step address puts the cursor on that step", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: firstEpic(),
      address: { view: "inbox", entry: { kind: "step", id: "s1" } },
    },
    (container) => {
      assert.equal(cursorKey(container), "step:s1");
    },
  ));

test("HashRoute: an epic address whose id holds a slash shows the epic page", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: firstEpic(),
      address: { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "epic" } } },
    },
    (container) => {
      assert.equal(cursorKey(container), widgetsFirstKey);
      assert.equal(epicPageShown(container), true);
      assert.equal(epicPageDrawn(container), true);
    },
  ));

test("HashRoute: a contract address shows the contract page over the epic page", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: summaryEpic(),
      address: { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "contract" } } },
    },
    async (container) => {
      assert.equal(epicPageShown(container), true);
      assert.equal(container.querySelector('section.page[aria-label="epic"] article.contract') !== null, true);
      await backFromPage(container);
      assert.equal(epicPageDrawn(container), true, "the epic page is under the contract");
    },
  ));

test("HashRoute: a phase output address shows that task's record over the epic page", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: phaseEpic(),
      address: {
        view: "inbox",
        entry: { kind: "epic", id: widgetsFirst, page: { kind: "phase", task: "T1", phase: "checking" } },
      },
    },
    async (container) => {
      assert.equal(container.querySelector('section.page[aria-label="epic"] .phase-output') !== null, true);
      assert.equal(
        container.querySelector('section.page[aria-label="epic"] .phase-head[aria-expanded="true"] .nm')?.textContent,
        "green",
      );
      await backFromPage(container);
      assert.equal(epicPageDrawn(container), true, "the epic page is under the phase output");
    },
  ));

test("HashRoute: a worker session address shows that session over the epic page", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: phaseEpic(),
      address: {
        view: "inbox",
        entry: {
          kind: "epic",
          id: widgetsFirst,
          page: { kind: "session", task: "T1", phase: "checking", step: "run1" },
        },
      },
    },
    async (container) => {
      assert.equal(container.querySelector('section[aria-label="session"] .worker-session') !== null, true);
      await clickButton(container, 'section[aria-label="session"] .title-row', "‹");
      assert.equal(epicPageDrawn(container), true, "the epic page is under the worker session");
    },
  ));

test("HashRoute: a worker session address whose step the epic lacks stays in the panel, with its close, and draws no session in the middle", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: phaseEpic(),
      address: {
        view: "inbox",
        entry: {
          kind: "epic",
          id: widgetsFirst,
          page: { kind: "session", task: "T1", phase: "checking", step: "no-such-step" },
        },
      },
    },
    async (container) => {
      assert.equal(container.querySelector('section[aria-label="session"]'), null, "no session is drawn in the middle");
      assert.equal(container.querySelector(".rpanel .side-note")?.textContent, "session unavailable");
    },
  ));

test("HashRoute: an epic in the ended earlier line opens its line and shows its page", (t) =>
  withApp(
    t.signal,
    {
      epics: shippedEpic(),
      now: endedNow,
      address: { view: "inbox", entry: { kind: "epic", id: `${widgetsRepo}/shipped`, page: { kind: "epic" } } },
    },
    (container) => {
      assert.equal(cursorKey(container), `epic:${widgetsRepo}/shipped`);
      assert.equal(epicPageShown(container), true);
    },
  ));

test("HashRoute: an address whose session the list does not hold yet opens once the rows arrive", (t) =>
  withApp(
    t.signal,
    { rows: [], address: { view: "inbox", entry: { kind: "session", id: "s1" } } },
    async (container, rerender) => {
      assert.equal(readerShown(container), false, "no row yet to open");
      await rerender({ view: "inbox", entry: { kind: "session", id: "s1" } }, { rows: rows() });
      assert.equal(cursorKey(container), "session:s1");
      assert.equal(readerShown(container), true);
    },
  ));

test("HashRoute: a rerender with a different address moves App to it", (t) =>
  withApp(t.signal, { rows: rows(), address: { view: "archived", entry: null } }, async (container, rerender) => {
    assert.equal(shownView(container), "archived");
    await rerender({ view: "inbox", entry: { kind: "session", id: "s1" } });
    assert.equal(shownView(container), "inbox");
    assert.equal(cursorKey(container), "session:s1");
  }));

test("HashRoute: a rerender with the address App already shows changes nothing", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: summaryEpic(),
      address: { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "epic" } } },
    },
    async (container, rerender) => {
      await clickContractDoor(container);
      assert.equal(pageClass(container), "page drill", "the page drills into the contract");
      await rerender({ view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "contract" } } });
      assert.equal(pageClass(container), "page drill", "the address App shows is not opened again");
      assert.equal(container.querySelector('section.page[aria-label="epic"] article.contract') !== null, true);
    },
  ));

test("HashRoute: a rerender to an address with no entry closes an open epic page", (t) =>
  withApp(
    t.signal,
    {
      rows: [waitingRow()],
      epics: firstEpic(),
      address: { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "epic" } } },
    },
    async (container, rerender) => {
      assert.equal(epicPageShown(container), true);
      await rerender({ view: "inbox", entry: null });
      assert.equal(epicPageShown(container), false);
    },
  ));
