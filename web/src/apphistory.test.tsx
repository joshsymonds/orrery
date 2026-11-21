import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { EpicSchema, EpicStepSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type InAppNotice, InAppNoticeSchema, NoticeKeySchema } from "./gen/orrery/v1/notice_pb";
import { type Address, formatAddress } from "./router";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

const now = 1000;

// wideWidth is the window the tests run at, past the 760px the wide layout starts at, where a row's page draws beside the
// inbox from the start. narrowWidth is a window under 760px: there an opened row's page draws alone, and the list's keys
// still move the cursor, as the tests that open a row and press a key check.
const wideWidth = 1024;
const narrowWidth = 700;

type Clients = ReturnType<typeof createClients>;

// Report is one address App reported: its hash, and whether it pushed or replaced the entry it shows.
type Report = [string, "push" | "replace"];

// Setup is what one test renders App over: the rows and epics the poll holds, the notices, the conversation the reader
// streams, and the address the prop holds.
interface Setup {
  rows?: Row[];
  epics?: EpicsView;
  notices?: InAppNotice[];
  items?: object[];
  address?: Address;
  // width is the window's width: the wide window unless the test runs narrow.
  width?: number;
}

// Shown is what a test's check reads: the DOM App draws into, the reports App has made, and a render of App over
// another setup, on the same clients and DOM.
interface Shown {
  container: HTMLElement;
  reports: Report[];
  rerender: (next: Setup) => Promise<void>;
}

// envelope frames one message of a streamed answer: a flag byte, a 4-byte length, and the JSON payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// fakeFetch answers each unary call with an empty message. WatchConversation holds its stream open, and first sends
// items as one reset delta when a test gives them, so the reader draws them as the conversation it follows.
function fakeFetch(items: object[]) {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    if (path.endsWith("/WatchConversation")) {
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          if (items.length > 0) {
            controller.enqueue(envelope({ transcript: "t1", seq: "1", reset: true, items, state: {} }));
          }
        },
      });
      return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  };
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

// session is a session row of the api project, whose id is the session's id.
function session(id: string, init: Partial<Row> = {}): Row {
  return row({ key: `session:${id}`, id, kind: "session", host: "", project: "/work/api", ...init });
}

// historyRows are the rows the tests share, in the inbox order: s0 needs you, s1 and s3 are unseen, s2 is working,
// and the pane is seen. From any of these rows a cursor key reaches another row.
function historyRows(): Row[] {
  return [
    session("s0", { label: "waiting work", state: "waiting" }),
    session("s1", { label: "auth work", unseen: true, changed: 300 }),
    session("s3", { label: "docs work", unseen: true, changed: 250 }),
    session("s2", { label: "build work", state: "working", changed: 200 }),
    row({ key: "pane:box:%3", id: "%3", kind: "pane", host: "box", label: "pane work", project: "/work/web" }),
  ];
}

const widgetsRepo = "widgets-5d0c1a7";
const widgetsFirst = `${widgetsRepo}/first`;
const widgetsFirstKey = `epic:${widgetsFirst}`;
const widgetsSecond = `${widgetsRepo}/second`;
const firstAddress = `#/inbox/epic/${encodeURIComponent(widgetsFirst)}`;
const secondAddress = `#/inbox/epic/${encodeURIComponent(widgetsSecond)}`;
const inboxAddress = "#/inbox";

// makeEpic is a synthetic epic whose id is its repository and slug joined, as the record names it.
function makeEpic(repository: string, slug: string, init: MessageInitShape<typeof EpicSchema> = {}) {
  return create(EpicSchema, { id: `${repository}/${slug}`, repository, slug, outcome: "", live: true, ...init });
}

function makeStep(id: string, init: MessageInitShape<typeof EpicStepSchema> = {}) {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false, ...init });
}

// firstEpic is a live epic whose running step is run1, with the detail given. The inbox draws the epic, then
// its step, before the rows.
function firstEpic(init: MessageInitShape<typeof EpicsGetResponseSchema> = {}): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("run1")] });
  return epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, ...init })]]));
}

// acceptedContract is an accepted contract, whose summary the epic page draws with the Contract door that opens it.
const acceptedContract = { title: "Show the accepted contract", approved: "approved line", intent: "the intent" };

// contractEpic is a live orreryd epic accepted with its summary and contract, so its page draws the Contract door that
// drills into the contract.
function contractEpic(): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { source: "orreryd", outcome: "accepted", runningSteps: [makeStep("run1")] });
  return epicsView(
    [epic],
    new Map([[epic.id, create(EpicsGetResponseSchema, { epic, summary: "Ship the demo", contract: acceptedContract })]]),
  );
}

// twoEpics are two live epics with no steps and the first with an accepted contract, so the cursor walks from the
// first straight to the second.
function twoEpics(): EpicsView {
  const first = makeEpic(widgetsRepo, "first", { source: "orreryd", outcome: "accepted" });
  const second = makeEpic(widgetsRepo, "second");
  const detail = create(EpicsGetResponseSchema, { epic: first, summary: "Ship the demo", contract: acceptedContract });
  return epicsView(
    [first, second],
    new Map([
      [first.id, detail],
      [second.id, create(EpicsGetResponseSchema, { epic: second })],
    ]),
  );
}

// phaseEpic is a live epic whose task T1 runs its gate step run1, which puts T1 in checking.
function phaseEpic(): EpicsView {
  const run = makeStep("run1", { task: "T1", stage: "gate", doing: "checking the parser", role: "implementer" });
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [run] });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    buildTasks: [{ id: "T1", title: "Parser", status: "dispatched", requirements: ["R1"] }],
    requirements: [{ id: "R1", text: "The parser parses" }],
    steps: [run],
  });
  return epicsView([epic], new Map([[epic.id, detail]]));
}

// cardItems are the conversation items of a session whose epic_create call made the epic, and the call's result.
function cardItems(epic: string): object[] {
  return [
    { kind: "tool", time: "100", tool: "epic_create", input: "demo-epic", id: "c1", epic },
    { kind: "result", time: "100", id: "c1", text: "created", error: false },
  ];
}

// show renders App over the setup, with onAddress recording each address it reports.
function show(container: HTMLElement, clients: Clients, setup: Setup, reports: Report[]): Promise<void> {
  return act(() => {
    render(
      <App
        rows={setup.rows ?? []}
        now={now}
        clients={clients}
        epics={setup.epics}
        notices={setup.notices}
        address={setup.address}
        onAddress={(address, mode) => {
          reports.push([formatAddress(address), mode]);
        }}
      />,
      container,
    );
  });
}

// withApp mounts App in a fresh DOM over a fake transport, and runs check. The timers App arms run on the mocked clock,
// so a test advances none of them. App is unmounted and the DOM removed when the test ends.
async function withApp(t: TestContext, setup: Setup, check: (shown: Shown) => void | Promise<void>): Promise<void> {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  window.happyDOM.setViewport({ width: setup.width ?? wideWidth, height: 800 });
  const container = document.createElement("div");
  document.body.append(container);
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch(setup.items ?? []) });
  const clients = createClients(transport);
  const reports: Report[] = [];
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(container, clients, setup, reports);
    await check({ container, reports, rerender: (next) => show(container, clients, next, reports) });
  } finally {
    await teardown();
  }
}

// press sends one keydown to the document, as a key press does, and lets Preact draw what it changed.
async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// unfocus takes the focus off the focused element, so the list's keys reach the list again. A click or Enter on a row
// focuses its reply box, and the keys would type into it.
async function unfocus(): Promise<void> {
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

// click clicks the first element the selector names whose text is text, when one is given, and lets Preact draw what
// the click changed. Then it unfocuses, as a click that focuses a reply box leaves the keys to the list.
async function click(container: HTMLElement, selector: string, text?: string): Promise<void> {
  const target = [...container.querySelectorAll<HTMLElement>(selector)].find(
    (element) => text === undefined || element.textContent?.trim() === text,
  );
  assert.ok(target !== undefined, `no ${selector} reads ${text ?? "anything"}`);
  await act(() => {
    target.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
  await unfocus();
}

// openEpicLine clicks the epic's line, which moves the cursor to the epic and opens its page.
async function openEpicLine(container: HTMLElement, key: string): Promise<void> {
  await click(container, `[data-key="${key}"]`);
}

// typeFilter types text into the list's filter, as its input event does.
async function typeFilter(container: HTMLElement, text: string): Promise<void> {
  // The field opens with / and takes the focus; typing leaves it unfocused, as typing into a drawn field does.
  const opened = container.querySelector(".inbox-search input") === null;
  if (opened) {
    await press("/");
  }
  await act(() => {
    const input = container.querySelector<HTMLInputElement>(".inbox-search input");
    assert.ok(input, "the filter input is drawn");
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    if (opened) {
      input.blur();
    }
  });
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// cursorPresses are the presses of the keys that move the cursor, in order: j, k, gg, G, w, n and e.
const cursorPresses: string[][] = [["j"], ["k"], ["g", "g"], ["G"], ["w"], ["n"], ["e"]];

// cursorKeyModes is the mode each cursor press reports with when each one moves the cursor: the first pushes, and the
// rest replace.
const cursorKeyModes = ["push", "replace", "replace", "replace", "replace", "replace", "replace"];

// cursorModes presses each cursor key once, and returns the mode of each address they report, in order.
async function cursorModes(reports: Report[]): Promise<string[]> {
  const from = reports.length;
  for (const keys of cursorPresses) {
    for (const key of keys) {
      await press(key);
    }
  }
  return reports.slice(from).map(([, mode]) => mode);
}

test("HashRoute: a row click pushes its address, and the first cursor key after it pushes while the rest replace", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await click(container, '[data-key="session:s1"]');
    assert.deepEqual(reports, [["#/inbox/session/s1", "push"]]);
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: Enter on a row the cursor moved to pushes the address it opens, and the first cursor key after it pushes", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ reports }) => {
    await press("G");
    await press("k");
    const before = reports.length;
    await press("Enter");
    await unfocus();
    assert.deepEqual(reports.slice(before), [["#/inbox/session/s2", "push"]], "Enter opens the row, so its address pushes");
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: a click on a row under 760px pushes the address it opens, and the next j pushes", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await click(container, '[data-key="session:s0"]');
    assert.deepEqual(reports, [["#/inbox/session/s0", "push"]], "the click opens the row, so its address pushes");
    await press("j");
    assert.deepEqual(reports.slice(1), [["#/inbox/session/s1", "push"]]);
  }));

test("HashRoute: Enter on the row the cursor rests on pushes the address it opens, and the next j pushes", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await press("Enter");
    await unfocus();
    assert.deepEqual(reports, [["#/inbox/session/s0", "push"]], "Enter opens the row, so its address pushes");
    await press("j");
    assert.deepEqual(reports.slice(1), [["#/inbox/session/s1", "push"]]);
    assert.equal(cursorKey(container), "session:s1");
  }));

test("HashRoute: a notice pushes the address of its row, and the first cursor key after it pushes", (t) =>
  withApp(
    t,
    {
      rows: historyRows(),
      notices: [
        create(InAppNoticeSchema, {
          key: create(NoticeKeySchema, { session: "s1" }),
          state: "open",
          title: "auth work needs you",
          body: "",
        }),
      ],
    },
    async ({ container, reports }) => {
      await click(container, ".notice .acts .btn", "Open");
      assert.deepEqual(reports, [["#/inbox/session/s1", "push"]]);
      assert.deepEqual(await cursorModes(reports), cursorKeyModes);
    },
  ));

test("HashRoute: a card's epic link pushes the address of its epic, and the first cursor key after it pushes", (t) =>
  withApp(t, { rows: historyRows(), epics: firstEpic(), items: cardItems(widgetsFirst) }, async ({ container, reports }) => {
    await until(t.signal, () => container.querySelector(".epic-card .epic-link") !== null, "the card", {
      step: () => act(turn),
    });
    await click(container, ".epic-card .epic-link");
    assert.deepEqual(reports, [[firstAddress, "push"]]);
    assert.equal(cursorKey(container), widgetsFirstKey);
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: the v key pushes the archived view", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await click(container, '[data-key="session:s1"]');
    await press("v");
    assert.deepEqual(reports.slice(1), [["#/archived", "push"]]);
  }));

test("HashRoute: the foot's Archived and Back to the inbox push their views, and the first cursor key after them pushes", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await click(container, '[data-key="session:s1"]');
    await click(container, ".inbox-foot button", "Archivedv");
    assert.deepEqual(reports.slice(1), [["#/archived", "push"]]);
    await click(container, ".inbox-foot button", "Back to the inbox");
    assert.deepEqual(reports.slice(2), [["#/inbox/session/s0", "push"]]);
    // The cursor comes back on s0, where gg moves nothing, so one press fewer reports; the first that moves pushes.
    const modes = await cursorModes(reports);
    assert.equal(modes[0], "push");
    assert.deepEqual(modes.slice(1), modes.slice(1).map(() => "replace"));
  }));

test("HashRoute: a drill into the contract pushes its page, and the first cursor key after it pushes", (t) =>
  withApp(t, { rows: historyRows(), epics: contractEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    assert.deepEqual(reports, [
      [firstAddress, "push"],
      [`${firstAddress}/contract`, "push"],
    ]);
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: a drill into a phase output pushes its page, and the first cursor key after it pushes", (t) =>
  withApp(t, { rows: historyRows(), epics: phaseEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, 'section.page[aria-label="epic"] path[data-task="T1"][data-phase="checking"]');
    assert.deepEqual(reports.slice(1), [[`${firstAddress}/phase/T1/checking`, "push"]]);
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: a drill into a worker session pushes its page, and the first cursor key after it pushes", (t) =>
  withApp(t, { rows: historyRows(), epics: phaseEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, 'section.page[aria-label="epic"] path[data-task="T1"][data-phase="checking"]');
    await click(container, 'section.page[aria-label="epic"] .phase-head .nm', "implement");
    await click(container, 'section.page[aria-label="epic"] .phase-body .door', "Open the session›");
    assert.deepEqual(reports.slice(2), [[`${firstAddress}/session/T1/checking/run1`, "push"]]);
    assert.deepEqual(await cursorModes(reports), cursorKeyModes);
  }));

test("HashRoute: h pushes the epic page a contract returns to", (t) =>
  withApp(t, { rows: historyRows(), epics: contractEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    const before = reports.length;
    await press("h");
    assert.deepEqual(reports.slice(before), [[firstAddress, "push"]]);
  }));

test("HashRoute: ArrowLeft pushes the epic page a contract returns to", (t) =>
  withApp(t, { rows: historyRows(), epics: contractEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    const before = reports.length;
    await press("ArrowLeft");
    assert.deepEqual(reports.slice(before), [[firstAddress, "push"]]);
  }));

test("HashRoute: the back caret of a contract pushes the epic page it returns to", (t) =>
  withApp(t, { rows: historyRows(), epics: contractEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    const before = reports.length;
    await click(container, 'section.page[aria-label="epic"] .title-row .back', "‹");
    assert.deepEqual(reports.slice(before), [[firstAddress, "push"]]);
  }));

test("HashRoute: the epic crumb of a contract pushes the epic page", (t) =>
  withApp(t, { rows: historyRows(), epics: contractEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    const before = reports.length;
    await click(container, 'section.page[aria-label="epic"] .crumb', "first");
    assert.deepEqual(reports.slice(before), [[firstAddress, "push"]]);
  }));

test("HashRoute: the orrery crumb of the epic page closes it and pushes the inbox address", (t) =>
  withApp(t, { rows: historyRows(), epics: firstEpic() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    const before = reports.length;
    await click(container, 'section.page[aria-label="epic"] .title-row .back', "‹");
    assert.deepEqual(reports.slice(before), [[inboxAddress, "push"]]);
  }));

test("HashRoute: a cursor key onto another epic from a contract pushes that epic once, not the contract", (t) =>
  withApp(t, { rows: historyRows(), epics: twoEpics() }, async ({ container, reports }) => {
    await openEpicLine(container, widgetsFirstKey);
    await click(container, ".epic-page .door.contract");
    const before = reports.length;
    await press("j");
    assert.deepEqual(reports.slice(before), [[secondAddress, "push"]]);
  }));

test("HashRoute: a bare load with its rows arriving records nothing", (t) =>
  withApp(t, { rows: [] }, async ({ reports, rerender }) => {
    await rerender({ rows: historyRows() });
    assert.deepEqual(reports, []);
  }));

test("HashRoute: an applied address prop records nothing, and the next j pushes", (t) =>
  withApp(
    t,
    { rows: historyRows(), width: narrowWidth, address: { view: "inbox", entry: { kind: "session", id: "s1" } } },
    async ({ container, reports }) => {
      assert.deepEqual(reports, []);
      assert.equal(cursorKey(container), "session:s1");
      await press("j");
      assert.deepEqual(reports, [["#/inbox/session/s3", "push"]]);
    },
  ));

test("HashRoute: an applied address with no entry, on the order already shown, records nothing and the next j pushes", (t) =>
  withApp(t, { rows: historyRows() }, async ({ reports, rerender }) => {
    await rerender({ rows: historyRows(), address: { view: "inbox", entry: null } });
    assert.deepEqual(reports, [], "the applied order is not reported");
    await press("j");
    assert.deepEqual(reports, [["#/inbox/session/s1", "push"]]);
  }));

// At 760px and wider an epic's address hides the inbox, so a list key would pop it out; under 760px the opened page
// draws alone and the list's keys still move the cursor, as this test's j needs.
test("HashRoute: an applied contract address lands its page and records nothing, and the next j pushes", (t) =>
  withApp(
    t,
    {
      rows: historyRows(),
      epics: contractEpic(),
      address: { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "contract" } } },
      width: narrowWidth,
    },
    async ({ container, reports }) => {
      assert.equal(container.querySelector('section.page[aria-label="epic"] article.contract') !== null, true);
      assert.deepEqual(reports, [], "the applied page is not reported");
      await press("j");
      assert.deepEqual(reports, [["#/inbox/step/run1", "push"]]);
    },
  ));

test("HashRoute: a crumb return that changes no address is not carried to a later change, which replaces", (t) =>
  withApp(t, { rows: historyRows() }, async ({ container, reports }) => {
    await press("h");
    assert.deepEqual(reports, [], "h changes no address at the inbox");
    await typeFilter(container, "auth");
    assert.deepEqual(reports, [["#/inbox/session/s1", "replace"]], "the filter's change replaces");
  }));

test("HashRoute: an address prop echoing the one App reported records nothing, and later cursor keys replace", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports, rerender }) => {
    await click(container, '[data-key="session:s1"]');
    await press("j");
    assert.deepEqual(reports, [
      ["#/inbox/session/s1", "push"],
      ["#/inbox/session/s3", "push"],
    ]);
    await rerender({ rows: historyRows(), address: { view: "inbox", entry: { kind: "session", id: "s3" } } });
    assert.equal(reports.length, 2, "the echo reports nothing");
    await press("k");
    assert.deepEqual(reports.slice(2), [["#/inbox/session/s1", "replace"]]);
    assert.equal(cursorKey(container), "session:s1");
  }));

test("HashRoute: an address whose entry is pending records nothing until the entry arrives", (t) =>
  withApp(
    t,
    { rows: [], width: narrowWidth, address: { view: "inbox", entry: { kind: "session", id: "s1" } } },
    async ({ container, reports, rerender }) => {
      assert.deepEqual(reports, [], "nothing while the entry is pending");
      const address: Address = { view: "inbox", entry: { kind: "session", id: "s1" } };
      await rerender({ rows: historyRows(), address });
      assert.deepEqual(reports, [], "nothing once the entry arrives and is applied");
      assert.equal(cursorKey(container), "session:s1");
      await press("j");
      assert.deepEqual(reports, [["#/inbox/session/s3", "push"]]);
    },
  ));

// archivableRows are historyRows with s0 given the archive kind a session row has, so the A key archives it.
function archivableRows(): Row[] {
  return [
    session("s0", { label: "waiting work", state: "waiting", archiveKind: "session", archiveId: "s0" }),
    ...historyRows().slice(1),
  ];
}

test("HashRoute: a filter that hides an opened entry drops the open, and clearing it does not move the cursor", (t) =>
  withApp(
    t,
    { rows: historyRows(), address: { view: "inbox", entry: null } },
    async ({ container, reports, rerender }) => {
      await typeFilter(container, "waiting");
      await rerender({ rows: historyRows() });
      const address: Address = { view: "inbox", entry: { kind: "session", id: "s1" } };
      await rerender({ rows: historyRows(), address });
      assert.equal(cursorKey(container), "session:s0", "the cursor stays on its row");
      assert.deepEqual(reports, [["#/inbox/session/s0", "replace"]], "the shown address replaces");
      await typeFilter(container, "");
      assert.equal(cursorKey(container), "session:s0", "clearing the filter does not move the cursor to s1");
      await press("j");
      assert.deepEqual(reports.slice(1), [["#/inbox/session/s1", "push"]], "the next cursor key pushes");
      await press("j");
      assert.deepEqual(reports.slice(2), [["#/inbox/session/s3", "replace"]], "the key after it replaces");
    },
  ));

test("HashRoute: an opened row a landed archive marked archived drops the open, and the next key reports", (t) => {
  const rows = archivableRows();
  return withApp(t, { rows }, async ({ container, reports, rerender }) => {
    await press("A");
    const gone = () => container.querySelector('[data-key="session:s0"]') === null;
    await until(t.signal, gone, "the row leaving", { step: () => act(turn) });
    const before = reports.length;
    const address: Address = { view: "inbox", entry: { kind: "session", id: "s0" } };
    await rerender({ rows, address });
    assert.equal(cursorKey(container), "session:s1", "the cursor rests on the row that moved into s0's place");
    assert.deepEqual(reports.slice(before), [["#/inbox/session/s1", "replace"]], "the shown address replaces");
    await press("j");
    assert.deepEqual(reports.slice(before + 1), [["#/inbox/session/s3", "push"]], "the next cursor key pushes");
  });
});

test("HashRoute: an epic address a local archive mark moved off the inbox line drops, and reports resume", (t) => {
  const rows = historyRows().slice(1);
  const epics = firstEpic();
  return withApp(t, { rows, epics }, async ({ container, reports, rerender }) => {
    for (let step = 0; step < 4 && cursorKey(container) !== widgetsFirstKey; step++) {
      await press("j");
    }
    assert.equal(cursorKey(container), widgetsFirstKey, "the cursor walks to the epic");
    await press("A");
    const gone = () => container.querySelector(`[data-key="${widgetsFirstKey}"]`) === null;
    await until(t.signal, gone, "the epic leaving", { step: () => act(turn) });
    const before = reports.length;
    const address: Address = { view: "inbox", entry: { kind: "epic", id: widgetsFirst, page: { kind: "epic" } } };
    await rerender({ rows, epics, address });
    assert.deepEqual(reports.slice(before), [["#/inbox/pane/box/%253", "replace"]], "the shown address replaces");
    await press("k");
    assert.deepEqual(reports.slice(before + 1), [["#/inbox/session/s2", "push"]], "the next cursor key pushes");
  });
});

test("HashRoute: an address under archived naming an archived row opens it and records nothing", (t) => {
  const rows = [...historyRows(), session("s9", { label: "old work", archived: true })];
  return withApp(t, { rows }, async ({ container, reports, rerender }) => {
    const address: Address = { view: "archived", entry: { kind: "session", id: "s9" } };
    await rerender({ rows, address });
    assert.equal(cursorKey(container), "session:s9", "the archived row is opened");
    assert.deepEqual(reports, [], "the applied address records nothing");
  });
});

// listHiddenIn is whether the inbox's list is hidden, which the narrow layout does while a page is drawn alone.
function listHiddenIn(container: HTMLElement): boolean {
  const list = container.querySelector(".list");
  assert.ok(list, "the list is rendered");
  return list.hasAttribute("hidden");
}

// readerHiddenIn is whether the reader is hidden.
function readerHiddenIn(container: HTMLElement): boolean {
  const reader = container.querySelector('section[aria-label="reader"]');
  assert.ok(reader, "the reader is rendered");
  return reader.hasAttribute("hidden");
}

test("HashRoute: under 760px the page's Back pushes the inbox's address, which names no row", (t) =>
  withApp(t, { rows: historyRows(), width: narrowWidth }, async ({ container, reports }) => {
    await click(container, '[data-key="session:s1"]');
    assert.deepEqual(reports, [["#/inbox/session/s1", "push"]]);
    await click(container, 'section[aria-label="reader"] button.back');
    assert.equal(listHiddenIn(container), false, "Back draws the inbox");
    assert.deepEqual(reports.slice(1), [[inboxAddress, "push"]], "Back reports the inbox's address");
  }));

test("HashRoute: under 760px a popped inbox address draws the inbox alone, and records nothing", (t) =>
  withApp(
    t,
    { rows: historyRows(), width: narrowWidth, address: { view: "inbox", entry: { kind: "session", id: "s1" } } },
    async ({ container, reports, rerender }) => {
      assert.equal(listHiddenIn(container), true, "the applied row's page draws alone");
      const before = reports.length;
      await rerender({ rows: historyRows(), address: { view: "inbox", entry: null } });
      assert.equal(listHiddenIn(container), false, "the popped inbox address draws the list");
      assert.equal(readerHiddenIn(container), true, "the popped inbox address leaves no page beside it");
      assert.deepEqual(reports.slice(before), [], "the applied address records nothing");
    },
  ));
