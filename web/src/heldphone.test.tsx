import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { contracts as contractList } from "./fixtures/contracts";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import { type Epic, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import type { Address } from "./router";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";

// Each test's name carries the token HeldLayout: under 760px an opened epic, contract or design is the phone's sheet, set under
// its conversation's head, and the head's work link for it is lit. The checks read the DOM only, and each test mounts App at
// a phone's width, since the layout reads the width when App mounts. No clock runs: the seen clock's timers never fire.

const now = 1000;
const phone = 400;
const repository = "widgets-5d0c1a7";
const epicSession = "s1";
const contractSession = "sess-tide-16";
const designSession = "sess-tide";

type Clients = ReturnType<typeof createClients>;

// Setup is what App is drawn with: its rows, its epics, its contracts, its designs and its address.
interface Setup {
  rows: Row[];
  epics?: EpicsView;
  contracts?: Contract[];
  designs?: Design[];
  address?: Address;
}

// Mount is what a check gets: the container App draws into.
interface Mount {
  container: HTMLElement;
}

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// fakeFetch answers each unary call with an empty message, and holds each conversation stream open with no message.
async function fakeFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const path = new URL(new Request(input, init).url).pathname;
  if (path.endsWith("/WatchConversation")) {
    return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
      status: 200,
      headers: { "content-type": "application/connect+json" },
    });
  }
  return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
}

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

// steererRow is the session row s1, which steers the epic alpha.
function steererRow(): Row {
  return row({
    key: `session:${epicSession}`,
    id: epicSession,
    kind: "session",
    host: "",
    label: "auth work",
    project: "/work/api",
    archiveId: epicSession,
    archiveKind: "session",
  });
}

// conversationRow is the session row of the conversation that proposed the tide-alerts contract.
function conversationRow(): Row {
  return row({
    key: `session:${contractSession}`,
    id: contractSession,
    label: "tide work",
    archiveId: contractSession,
    archiveKind: "session",
  });
}

// designRow is the session row of the conversation the tide design works.
function designRow(): Row {
  return row({
    key: `session:${designSession}`,
    id: designSession,
    kind: "session",
    host: "",
    label: "tide work",
    project: "/work/tide",
    archiveId: designSession,
    archiveKind: "session",
  });
}

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

// view is the epics view of the epics, each with a detail whose summary names its slug.
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

const sessionAddress: Address = { view: "inbox", entry: { kind: "session", id: designSession } };
const contractAddress: Address = { view: "inbox", entry: { kind: "contract", id: "tidepool/tide-alerts" } };

const alpha = epicOf("alpha", epicSession);

// tide is the design the session works, with two contracts in position order.
const tide: Design = create(DesignSchema, {
  id: "tide",
  title: "Tide alerts",
  conversation: designSession,
  created: 10n,
  state: "One contract accepted, one proposed",
  contracts: [
    { id: "widgets/alpha", title: "alpha", repository, state: "accepted", position: 1n },
    { id: "widgets/beta", title: "beta", repository, state: "proposed", position: 2n },
  ],
});

// draw renders App over the setup in the container.
function draw(container: HTMLElement, clients: Clients, setup: Setup): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows}
        now={now}
        clients={clients}
        epics={setup.epics}
        contracts={setup.contracts}
        designs={setup.designs}
        address={setup.address}
        seenClock={stillClock}
      />,
      container,
    );
  });
}

// mountApp mounts App at a phone's width in a fresh DOM and runs check with the mount. App is unmounted and the DOM removed
// when the test ends, or as soon as signal aborts first.
async function mountApp(signal: AbortSignal, setup: Setup, check: (mount: Mount) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = phone;
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
    await draw(container, clients, setup);
    await check({ container });
  } finally {
    await teardown();
  }
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// required is the element that matches selector under root, which must be drawn.
function required(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// sheetOf is the phone's sheet holding the open thing, or null when none is drawn.
function sheetOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("section.held-sheet");
}

// litLink is the head's work link that names the open thing, or null when none is lit.
function litLink(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('.convo-head .wlink[aria-current="true"]');
}

// assertLitOnly asserts the head's work link titled title is the one lit, and that no other link is.
function assertLitOnly(container: HTMLElement, title: string): void {
  const lit = litLink(container);
  assert.ok(lit !== null, "the work link naming the open thing is lit");
  assert.equal(lit.getAttribute("title"), title, "the lit link is the open thing's");
  const lits = container.querySelectorAll('.convo-head .wlink[aria-current="true"]');
  assert.equal(lits.length, 1, "only the open thing's link is lit");
}

// cursorKeyOf is the key of the inbox row the cursor rests on, or "" when no row is marked.
function cursorKeyOf(container: HTMLElement): string {
  return container.querySelector('[data-row][aria-current="true"]')?.getAttribute("data-key") ?? "";
}

// checkUnderHead asserts the sheet is set into the conversation's column directly under its head, with the head's band between
// them, and that no reader is drawn in the column.
function checkUnderHead(container: HTMLElement, sheet: HTMLElement): void {
  const column = required(container, 'section[aria-label="reader"]');
  assert.equal(sheet.parentElement, column, "the sheet is set into the conversation's column");
  const head = required(column, ":scope > .convo-head");
  assert.equal(head.nextElementSibling, sheet, "the sheet comes directly after the conversation's head");
  assert.ok(head.lastElementChild?.classList.contains("head-band"), "a band of the sheet's ground runs under the head");
  assertNone(container.querySelector(".reader"), "the conversation's reader is not drawn");
}

test("HeldLayout: under 760px an opened epic is the sheet under its conversation's head, its work link lit, and its conversation's reader not drawn", (t) =>
  mountApp(t.signal, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, ({ container }) => {
    const sheet = sheetOf(container);
    assert.ok(sheet !== null, "the epic is held as the sheet");
    assert.equal(sheet.dataset.kind, "epic", "the sheet holds the epic");
    assert.ok(sheet.querySelector('section[aria-label="epic"]') !== null, "the sheet holds the epic's page");
    checkUnderHead(container, sheet);
    assertLitOnly(container, "Open alpha");
  }));

test("HeldLayout: under 760px a contract opens the same way, as the sheet under its conversation's head, its work link lit, and no reader", (t) =>
  mountApp(
    t.signal,
    { rows: [conversationRow()], contracts: contractList.contracts, address: contractAddress },
    ({ container }) => {
      const sheet = sheetOf(container);
      assert.ok(sheet !== null, "the contract is held as the sheet");
      assert.equal(sheet.dataset.kind, "contract", "the sheet holds the contract");
      assert.ok(sheet.querySelector(".contract-record") !== null, "the sheet holds the contract's record");
      checkUnderHead(container, sheet);
      assertLitOnly(container, "Open tide-alerts");
    },
  ));

test("HeldLayout: under 760px a design opens the same way, as the sheet under its conversation's head with its contracts drawn in it, its work link lit", (t) =>
  mountApp(t.signal, { rows: [designRow()], designs: [tide], address: sessionAddress }, async ({ container }) => {
    assertNone(sheetOf(container), "no design is held before the press");
    await press(required(container, '.convo-head .wlink[title="Open Tide alerts"]'));
    const sheet = sheetOf(container);
    assert.ok(sheet !== null, "the design is held as the sheet");
    assert.equal(sheet.dataset.kind, "design", "the sheet holds the design");
    assert.equal(sheet.querySelectorAll(".design-card-contract").length, 2, "the sheet draws the design's two contracts");
    checkUnderHead(container, sheet);
    assertLitOnly(container, "Open Tide alerts");
  }));

test("HeldLayout: under 760px the sheet's X closes an epic back to its conversation, with the reader drawn, its link unlit, and the cursor on the conversation's row", (t) =>
  mountApp(t.signal, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    await press(required(container, ".held-sheet .held-close"));
    assertNone(sheetOf(container), "the X closes the sheet");
    assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is drawn again");
    assert.ok(container.querySelector(".convo-head .wlink") !== null, "the head still names the epic's link");
    assertNone(litLink(container), "the link is no longer lit");
    assert.equal(cursorKeyOf(container), `session:${epicSession}`, "the cursor rests on the conversation's row");
  }));

test("HeldLayout: under 760px the sheet's X closes a contract back to its conversation, with the reader drawn, its link unlit, and the cursor on the conversation's row", (t) =>
  mountApp(
    t.signal,
    { rows: [conversationRow()], contracts: contractList.contracts, address: contractAddress },
    async ({ container }) => {
      await press(required(container, ".held-sheet .held-close"));
      assertNone(sheetOf(container), "the X closes the sheet");
      assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is drawn again");
      assertNone(litLink(container), "the link is no longer lit");
      assert.equal(cursorKeyOf(container), `session:${contractSession}`, "the cursor rests on the conversation's row");
    },
  ));

test("HeldLayout: under 760px the sheet's X closes a design back to its conversation, with the reader drawn and its link unlit", (t) =>
  mountApp(t.signal, { rows: [designRow()], designs: [tide], address: sessionAddress }, async ({ container }) => {
    await press(required(container, '.convo-head .wlink[title="Open Tide alerts"]'));
    await press(required(container, ".held-sheet .held-close"));
    assertNone(sheetOf(container), "the X closes the sheet");
    assert.ok(container.querySelector(".reader") !== null, "the conversation's reader is drawn again");
    assertNone(litLink(container), "the link is no longer lit");
    assert.equal(cursorKeyOf(container), `session:${designSession}`, "the cursor rests on the conversation's row");
  }));

test("HeldLayout: under 760px the head's Back while the sheet is up returns to the inbox, and the sheet shuts with it", (t) =>
  mountApp(t.signal, { rows: [steererRow()], epics: view([alpha]), address: epicAddress("alpha") }, async ({ container }) => {
    assert.ok(sheetOf(container) !== null, "the epic is held as the sheet before the press");
    await press(required(container, ".convo-head .back"));
    assertNone(sheetOf(container), "the sheet shuts with the Back");
    assertNone(container.querySelector('section[aria-label="epic"]'), "no epic page is drawn");
    assert.equal(required(container, ".list").hidden, false, "the inbox draws again");
  }));

test("HeldLayout: under 760px an epic no conversation steers keeps its page alone, with no sheet and no reader", (t) =>
  mountApp(t.signal, { rows: [], epics: view([epicOf("beta", "")]), address: epicAddress("beta") }, ({ container }) => {
    assert.ok(container.querySelector('section[aria-label="epic"]') !== null, "the epic is drawn as the page");
    assertNone(sheetOf(container), "no sheet is drawn");
    assertNone(container.querySelector(".reader"), "no reader is drawn");
  }));
