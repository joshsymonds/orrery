import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
import { type Contract, ContractSchema } from "./gen/orrery/v1/contracts_pb";
import {
  type Epic,
  EpicLedgerFindingSchema,
  EpicReviewVerdictSchema,
  EpicSchema,
  EpicSliceSchema,
  type EpicStep,
  EpicStepSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import type { Viewing } from "./presence";
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";
import { assertNone, assertSame, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { pause, turn, until as waitUntil } from "./testwait";

const now = 1000;

const paneWatchPath = "/orrery.v1.Panes/WatchConversation";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const sessionOlderPath = "/orrery.v1.Sessions/Older";
const sendTextPath = "/orrery.v1.Panes/SendText";
const sayPath = "/orrery.v1.Control/Say";
const pullPath = "/orrery.v1.Panes/PullQueued";
const queuedText = "queued words";
const intervalMs = 2;
const markPanePath = "/orrery.v1.Panes/MarkSeen";
const markSessionPath = "/orrery.v1.Control/Seen";
const seenEpicPath = "/orrery.v1.Epics/Seen";
const archivePath = "/orrery.v1.Epics/Archive";
const unarchivePath = "/orrery.v1.Epics/Unarchive";
const seenFailedText = "could not mark seen";
const sendKeysPath = "/orrery.v1.Panes/SendKeys";
const answerPath = "/orrery.v1.Control/Answer";
const answerFailedText = "no answer was sent";
const nudgePath = "/orrery.v1.Steps/Nudge";
const holdPath = "/orrery.v1.Steps/Hold";
const stopStepPath = "/orrery.v1.Steps/Stop";
const refusedNudgeText = "step is not running";
const getEpicPath = "/orrery.v1.Epics/Get";
const moveEpicPath = "/orrery.v1.Epics/Move";
const historyPath = "/orrery.v1.Epics/History";
const pauseEpicPath = "/orrery.v1.Epics/Pause";
const resumeEpicPath = "/orrery.v1.Epics/Resume";
const stopEpicPath = "/orrery.v1.Epics/Stop";
const retryEpicPath = "/orrery.v1.Epics/Retry";
const answerEpicPath = "/orrery.v1.Epics/Answer";
const startSessionPath = "/orrery.v1.Control/Start";
const pickerAskPath = "/orrery.v1.Picker/Ask";
const pickerRunPath = "/orrery.v1.Picker/Run";
const withdrawPath = "/orrery.v1.Control/Withdraw";
const interruptPath = "/orrery.v1.Control/Interrupt";

type Clients = ReturnType<typeof createClients>;

type Seen = ReturnType<typeof fakeSeen>;

interface Props {
  rows: Row[];
  now: number;
  clients: Clients;
  seen: Seen;
  epics?: EpicsView;
  contracts?: Contract[];
  host?: HostStatusResponse | null;
  onViewing?: (viewing: Viewing) => void;
}

// One request the fake server received. A unary call has no controller; a stream call has one until it ends.
interface Call {
  path: string;
  body: Record<string, unknown>;
  signal: AbortSignal;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire: unary calls with JSON, and WatchConversation with a stream
// the test feeds through emit. Every call is recorded, and closeStreams ends the ones still open.
class FakeServer {
  readonly calls: Call[] = [];
  // failNext holds the unary paths whose next call the server refuses with a Connect error, once each.
  readonly failNext = new Set<string>();
  // refuseNext maps a unary path to the message its next call is refused with as failed_precondition, once each.
  readonly refuseNext = new Map<string, string>();
  // replies queues the bodies a unary path answers with, one per call in order. A path with none queued answers as
  // it did.
  readonly replies = new Map<string, object[]>();
  // holds maps a unary path to the gate its next call waits on, once: the call answers only after the gate opens.
  readonly holds = new Map<string, Promise<void>>();
  // seen is the page the seen watch reads: the test emits its input, sets its focus, and advances its clock.
  readonly seen = fakeSeen();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    // A call's signal is the one the transport passed: a Request's own signal stops following it once the garbage
    // collector takes the Request, and would then never abort.
    const signal = init?.signal ?? request.signal;
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    if (!path.endsWith("/WatchConversation")) {
      this.calls.push({ path, body: decodeUnary(bytes), signal, controller: null });
      const gate = this.holds.get(path);
      if (gate !== undefined) {
        this.holds.delete(path);
        await gate;
      }
      if (this.failNext.delete(path)) {
        return new Response(JSON.stringify({ code: "unavailable", message: "queue unreachable" }), {
          status: 503,
          headers: { "content-type": "application/json" },
        });
      }
      const refusal = this.refuseNext.get(path);
      if (refusal !== undefined) {
        this.refuseNext.delete(path);
        return new Response(JSON.stringify({ code: "failed_precondition", message: refusal }), {
          status: 400,
          headers: { "content-type": "application/json" },
        });
      }
      return json(this.replies.get(path)?.shift() ?? (path === pullPath ? { text: queuedText } : {}));
    }
    const call: Call = { path, body: decodeEnvelope(bytes), signal, controller: null };
    this.calls.push(call);
    signal.addEventListener("abort", () => {
      call.controller = null;
    });
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        call.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  readonly clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }

  // holdNext makes the next call to path wait until the returned release runs, so a test draws the page while the call
  // is still pending.
  holdNext(path: string): () => void {
    let release: () => void = () => {};
    this.holds.set(
      path,
      new Promise<void>((resolve) => {
        release = resolve;
      }),
    );
    return release;
  }

  emit(call: Call, message: object): void {
    if (call.controller === null) {
      throw new Error(`the stream to ${call.path} has ended`);
    }
    call.controller.enqueue(envelope(0, message));
  }

  closeStreams(): void {
    for (const call of this.calls) {
      if (call.controller !== null) {
        call.controller.enqueue(envelope(2, {}));
        call.controller.close();
        call.controller = null;
      }
    }
  }
}

// A clock whose timers fire only when the test advances time.
function fakeClock() {
  let now = 0;
  let nextId = 1;
  const timers = new Map<unknown, { at: number; fn: () => void }>();
  const clock: SeenClock = {
    now: () => now,
    setTimeout(fn, ms) {
      const id = nextId++;
      timers.set(id, { at: now + ms, fn });
      return id;
    },
    clearTimeout(handle) {
      timers.delete(handle);
    },
  };
  return {
    clock,
    advance(ms: number) {
      const end = now + ms;
      for (;;) {
        let next: { id: unknown; at: number; fn: () => void } | undefined;
        for (const [id, timer] of timers) {
          if (timer.at <= end && (next === undefined || timer.at < next.at)) {
            next = { id, at: timer.at, fn: timer.fn };
          }
        }
        if (next === undefined) break;
        now = next.at;
        timers.delete(next.id);
        next.fn();
      }
      now = end;
    },
  };
}

// A listener registry for one event target, as seen.test.ts keeps one.
function listeners() {
  const byType = new Map<string, Set<() => void>>();
  return {
    addEventListener(type: string, fn: () => void) {
      const set = byType.get(type) ?? new Set<() => void>();
      set.add(fn);
      byType.set(type, set);
    },
    removeEventListener(type: string, fn: () => void) {
      byType.get(type)?.delete(fn);
    },
    emit(type: string) {
      for (const fn of [...(byType.get(type) ?? [])]) fn();
    },
    count() {
      let total = 0;
      for (const set of byType.values()) total += set.size;
      return total;
    },
  };
}

// fakeSeen is the page the seen watch reads: a document whose visibility and focus the test sets, input events the
// test emits, and a clock the test advances, so a dwell fires only when the test says so.
function fakeSeen() {
  const clock = fakeClock();
  const input = listeners();
  const page = listeners();
  const state = { visibilityState: "visible", focused: true };
  const seenWindow: SeenWindow = {
    document: {
      get visibilityState() {
        return state.visibilityState;
      },
      hasFocus: () => state.focused,
      addEventListener: input.addEventListener,
      removeEventListener: input.removeEventListener,
    },
    addEventListener: page.addEventListener,
    removeEventListener: page.removeEventListener,
  };
  return { clock: clock.clock, advance: clock.advance, input, page, state, window: seenWindow };
}

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

function decodeUnary(bytes: Uint8Array): Record<string, unknown> {
  return bytes.byteLength === 0 ? {} : JSON.parse(new TextDecoder().decode(bytes));
}

// decodeEnvelope reads the one message of a streamed request: a flag byte, a 4-byte length, and the JSON payload.
function decodeEnvelope(bytes: Uint8Array): Record<string, unknown> {
  const length = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength).getUint32(1);
  return JSON.parse(new TextDecoder().decode(bytes.subarray(5, 5 + length)));
}

function envelope(flag: number, message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = flag;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: "/work/app",
    host: "box",
    state: "working",
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

// Inbox order: a (needs you), c (new), b (working), d (seen), each in its own project's card, ranked as the tree ranks them.
function fixture(): Row[] {
  return [
    row({
      key: "a",
      archiveId: "sess-a",
      label: "build green",
      project: "/work/api",
      state: "waiting",
      question: "Ship it?",
      summary: "build green",
      changed: 300,
      seen: 100,
      unseen: true,
    }),
    row({
      key: "c",
      label: "wrote the guide",
      project: "/work/docs",
      host: "laptop",
      state: "done",
      summary: "wrote the guide",
      changed: 150,
      seen: 50,
      unseen: true,
    }),
    row({ key: "b", label: "bundling", project: "/work/web", state: "working", summary: "bundling", changed: 200, seen: 200 }),
    row({
      key: "d",
      label: "old",
      project: "/work/archive",
      state: "done",
      summary: "finished last week",
      changed: 10,
      seen: 10,
    }),
  ];
}

async function mount(container: HTMLElement, props: Props): Promise<void> {
  await act(() => {
    render(
      <App
        rows={props.rows}
        now={props.now}
        clients={props.clients}
        seenClock={props.seen.clock}
        seenWindow={props.seen.window}
        epics={props.epics}
        contracts={props.contracts}
        host={props.host}
        onViewing={props.onViewing}
      />,
      container,
    );
  });
}

// withApp mounts the app over a fake server in a fresh DOM and runs check. The app is unmounted, its streams closed and
// the DOM removed when the test ends, or as soon as signal aborts if it times out first, so the app never runs on into
// the next test (see teardownOnce).
async function withApp(
  signal: AbortSignal,
  props: Partial<Omit<Props, "clients" | "seen">>,
  check: (container: HTMLElement, fake: FakeServer) => void | Promise<void>,
  width?: number,
): Promise<void> {
  const window = installTestDom();
  if (width !== undefined) {
    window.innerWidth = width;
  }
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeServer();
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    fake.closeStreams();
    container.remove();
    await removeTestDom(window);
  });
  try {
    await mount(container, { rows: fixture(), now, clients: fake.clients, seen: fake.seen, ...props });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

// until waits as testwait's until does, with each pause run inside act, so the renders it lets through are applied.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(pause) });
}

// untilTurn waits as until does, but each round steps by turn inside act, so the wait reads no clock and arms no timer.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// settle lets pending promises run, so a write the test expects to be absent has time to start.
async function settle(): Promise<void> {
  await act(() => new Promise((resolve) => setTimeout(resolve, 10)));
}

async function pressIn(element: Element, key: string, modifiers: KeyboardEventInit = {}): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...modifiers });
  await act(() => {
    element.dispatchEvent(event);
  });
  return event;
}

async function typeInto(box: HTMLTextAreaElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// composer is the reply box the cursor's row shows: each row keeps its own, and only the cursor's is not hidden.
function composer(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) textarea");
  assert.ok(box, "the reader has a composer");
  return box;
}

// composerSendButton is the send button of the reply box the cursor's row shows.
function composerSendButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>('.reply:not([hidden]) button[aria-label="send"]');
  assert.ok(button, "the reply box has a send button");
  return button;
}

// toneOf is the state line's tone the dock around a reply box carries as data-state.
function toneOf(box: HTMLTextAreaElement): string {
  return box.closest(".dock-box")?.getAttribute("data-state") ?? "";
}

function readerText(container: HTMLElement): string {
  return container.querySelector(".reader")?.textContent ?? "";
}

// readerHead is the title row the cursor's reader opens with: a row's title row inside its head, or a step's title row.
// It is null when that reader draws neither.
function readerHead(container: HTMLElement): Element | null {
  return container.querySelector(
    'section[aria-label="reader"] > .convo-head > .title-row, section[aria-label="reader"] > .title-row',
  );
}

// pinnedHead is the title row the pinned reader opens with, or null when no row is pinned.
function pinnedHead(container: HTMLElement): Element | null {
  return container.querySelector('section[aria-label="pinned reader"] > .convo-head > .title-row');
}

// dockBox is the box of the dock a reader column shows: the visible dock under the column's reader, or null. Its state
// line says the row's state.
function dockBox(container: HTMLElement, section = "reader"): Element | null {
  return container.querySelector(`section[aria-label="${section}"] .send > div:not([hidden]) .dock-box`);
}

// dockWord is the word of the state line the column's dock draws, or empty when the column draws no dock.
function dockWord(container: HTMLElement, section = "reader"): string {
  return dockBox(container, section)?.querySelector(".statebar .word")?.textContent ?? "";
}

// crumbTexts are the labels of a title row's crumbs, in order: the directory's label first, the page's label last. The
// mark is not a label.
function crumbTexts(head: Element | null): string[] {
  return Array.from(head?.querySelectorAll(".crumbs .crumb") ?? [], (crumb) => crumb.textContent ?? "");
}

// readerNote is the working note under the cursor's reader, or null when that column draws none.
function readerNote(container: HTMLElement): Element | null {
  return container.querySelector('[aria-label="reader"] .work-note');
}

// noteWords is true when a reader section's text holds the working note's words, which no reader draws now.
function noteWords(section: Element | null): boolean {
  const text = section?.textContent ?? "";
  return text.includes("lands at Claude's next step") || text.includes("last turn ended");
}

// sectionChildren names each child of a reader section as its tag and first class, in order: a head, a conversation
// and a foot is ["header.head", "div.reader", "div.send"].
function sectionChildren(section: Element | null): string[] {
  return Array.from(section?.children ?? [], (child) => `${child.tagName.toLowerCase()}.${child.classList[0]}`);
}

// queueTexts is the text of each message in the cursor's foot queue, oldest first, without its tag.
function queueTexts(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('[aria-label="reader"] .send .tray-row .text'),
    (text) => text.textContent ?? "",
  );
}

// pinnedQueueTexts is the text of each message in the pinned foot queue, oldest first, without its tag.
function pinnedQueueTexts(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('[aria-label="pinned reader"] .send .tray-row .text'),
    (text) => text.textContent ?? "",
  );
}

// sendingRows are the sending rows a column's reader draws: the cursor's by default, or the one named, such as
// "pinned reader".
function sendingRows(container: HTMLElement, column = "reader"): Element[] {
  return Array.from(container.querySelectorAll(`[aria-label="${column}"] .sending-row`));
}

// ordinaryPromptRows are the prompt rows a column's reader draws for stored prompts, with no sending row among them.
function ordinaryPromptRows(container: HTMLElement, column = "reader"): Element[] {
  return Array.from(container.querySelectorAll(`[aria-label="${column}"] .reader > .t:not(.sending-row) .you`));
}

// sendingMark is the mark a sending row draws, or null when the element holds none.
function sendingMark(row: Element): string | null {
  return row.querySelector(".gut.sending")?.textContent ?? null;
}

// workingPane is the pane a stream sends while it runs a tool. The tool started at 940, a minute before the test's
// now of 1000, and the last turn ended at 700, five minutes before it.
function workingPane(tool: string) {
  return {
    id: "a",
    state: "working",
    claudeSession: "5e0e0000-0000-4000-8000-000000000001",
    tool: JSON.stringify({ id: "t1", name: tool, input: "ls", start: 940 }),
    changed: "900",
    lastTurnEnd: "700",
  };
}

const paneRow = row({
  key: "a",
  label: "build green",
  state: "waiting",
  question: "Ship it?",
  changed: 300,
  seen: 100,
  unseen: true,
});

const sessionRow = row({
  key: "s1",
  id: "s1",
  archiveId: "s1",
  kind: "session",
  archiveKind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  changed: 30,
  seen: 30,
});

async function press(key: string, modifiers: KeyboardEventInit = {}): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, cancelable: true, ...modifiers });
  await act(() => {
    document.dispatchEvent(event);
  });
  return event;
}

async function pressEach(keys: string[]): Promise<void> {
  for (const key of keys) {
    await press(key);
  }
}

async function typeFilter(container: HTMLElement, text: string): Promise<void> {
  if (container.querySelector(".inbox-search input") === null) {
    await press("/");
  }
  const input = filterInput(container);
  await act(() => {
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function filterInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>(".inbox-search input");
  assert.ok(input, "the filter input is rendered");
  return input;
}

function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".row[data-key]:not(.standin)")].map((element) => element.getAttribute("data-key") ?? "");
}

function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

function rowElement(container: HTMLElement, key: string): Element {
  const element = container.querySelector(`[data-key="${key}"]`);
  assert.ok(element, `row ${key} is rendered`);
  return element;
}

// listChildren names the children of the list in order by class: the top block, then the rows.
function listChildren(container: HTMLElement): string[] {
  const list = container.querySelector(".list");
  assert.ok(list !== null, "the list is rendered");
  return [...list.children].map((element) => element.getAttribute("class") ?? "");
}

// inboxChildren names the children of the inbox, in order by class.
function inboxChildren(container: HTMLElement): string[] {
  const inbox = container.querySelector(".list > nav.inbox");
  assert.ok(inbox !== null, "the inbox is inside the list");
  return [...inbox.children].map((element) => element.getAttribute("class") ?? "");
}

// outsideList names the elements the selector matches that sit outside the list, by class.
function outsideList(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)]
    .filter((element) => element.closest(".list") === null)
    .map((element) => element.getAttribute("class") ?? "");
}

// chipTexts is the text of each option chip the row draws, in order. Round 6's row draws none, so this is empty.
function chipTexts(container: HTMLElement, key: string): string[] {
  return [...rowElement(container, key).querySelectorAll(".opts > .opt")].map((chip) => chip.textContent ?? "");
}

// answerLine is the green line a landed answer leaves on the row, or null when the row shows none.
function answerLine(container: HTMLElement, key: string): string | null {
  return rowElement(container, key).querySelector(".ans")?.textContent ?? null;
}

test("j and ArrowDown move the cursor one row down and clamp at the last row", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("ArrowDown");
    assert.equal(cursorKey(container), "b");
    await press("ArrowDown");
    assert.equal(cursorKey(container), "d");
    await press("j");
    assert.equal(cursorKey(container), "d");
  }));

test("k and ArrowUp move the cursor one row up and clamp at the first row", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("k");
    assert.equal(cursorKey(container), "b");
    await press("ArrowUp");
    assert.equal(cursorKey(container), "c");
    await press("ArrowUp");
    assert.equal(cursorKey(container), "a");
    await press("k");
    assert.equal(cursorKey(container), "a");
  }));

test("gg goes to the first row, and a lone g or a g broken by another key does not", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("G");
    await press("g");
    assert.equal(cursorKey(container), "d", "one g moved the cursor");
    await press("k");
    await press("g");
    assert.equal(cursorKey(container), "b", "g after another key moved the cursor");
    await press("g");
    assert.equal(cursorKey(container), "a");
  }));

test("G goes to the last row", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
  }));

test("w goes to the first waiting row and does nothing when no row waits", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("w");
    assert.equal(cursorKey(container), "a");

    await mount(container, { rows: fixture().slice(1), now, clients: fake.clients, seen: fake.seen });
    await press("G");
    await press("w");
    assert.equal(cursorKey(container), "d");
  }));

test("InboxTree: n goes to the next unseen row after the cursor and wraps", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("n");
    assert.equal(cursorKey(container), "c");
    await press("n");
    assert.equal(cursorKey(container), "a");
  }));

test("/ focuses the filter input and does not type the slash", (t) =>
  withApp(t.signal, {}, async (container) => {
    const event = await press("/");
    assert.equal(event.defaultPrevented, true, "the slash was left to the input");
    assert.equal(document.activeElement === filterInput(container), true);
    assert.equal(filterInput(container).value, "");
  }));

test("Esc closes the help when the filter does not have focus", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("?");
    await press("Escape");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
  }));

test("? opens the key list over the page, outside the list, and a second ? closes it", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true, "the key list starts closed");
    await press("?");
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help !== null, "help panel is open");
    assert.equal(help.closest(".list") === null, true, "the key list is not inside the list");
    assert.equal(container.querySelector('.list [role="dialog"][aria-label="Keys"]') === null, true, "no key list is inside the list");
    const scrim = help.parentElement;
    assert.equal(scrim?.classList.contains("scrim"), true, "the key list sits over the page in a scrim");
    assert.equal(scrim?.parentElement?.classList.contains("app"), true, "the scrim sits in the page");
    assert.equal(scrim?.previousElementSibling?.classList.contains("panes"), true, "the scrim follows the columns");
    await press("?");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
  }));

test("with the filter focused, shortcut keys do nothing and Enter blurs it", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("/");
    await press("j");
    await press("G");
    await press("?");
    await press("x");
    await press("e");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "x and e in the filter write no seen");
    await press("Enter");
    assert.equal(container.querySelector(".inbox-search input") === document.activeElement, false, "the search field has no focus");
    assert.equal(fake.callsTo(markPanePath).length, 0, "Enter in the filter writes no seen");
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("Esc in the filter clears it, closes it and blurs it", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(rowKeys(container), ["c"]);
    await press("Escape");
    assert.equal(container.querySelector(".inbox-search") === null, true, "Escape closes the search field");
    assert.equal(document.activeElement?.tagName === "INPUT", false, "the field is not focused");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    await press("/");
    assert.equal(filterInput(container).value, "", "the reopened field is empty");
  }));

test("the filter keeps rows whose fields contain every typed word, case-insensitively", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "API WAITING");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "archive done");
    assert.deepEqual(rowKeys(container), ["d"]);
    await typeFilter(container, "build");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "laptop");
    assert.deepEqual(rowKeys(container), ["c"]);
    await typeFilter(container, "");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("modifier keys leave the cursor alone", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("j", { ctrlKey: true });
    await press("j", { metaKey: true });
    await press("j", { altKey: true });
    await press("G", { ctrlKey: true });
    assert.equal(cursorKey(container), "a");
  }));

test("u outside the filter changes nothing", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("u");
    assert.equal(cursorKey(container), "a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
    assert.equal(container.querySelector(".inbox-search") === null, true, "no search field opened");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "no seen was written");
  }));

test("the cursor keeps its row across new rows and clamps when its row leaves", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");

    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "b");

    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("an unmoved cursor keeps its row when a row sorts in above it", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(cursorKey(container), "a");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "a");
  }));

test("a clamped cursor keeps the row it moved to", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture().filter((item) => item.key !== "b"), added], now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("only unseen rows carry the unseen mark", (t) =>
  withApp(t.signal, {}, (container) => {
    const marked = [...container.querySelectorAll("[data-unseen]")].map((element) => element.getAttribute("data-key"));
    assert.deepEqual(marked, ["a", "c"]);
    assert.equal(rowElement(container, "b").hasAttribute("data-unseen"), false);
    assert.equal(rowElement(container, "d").hasAttribute("data-unseen"), false);
  }));

test("a row shows its label as its title, its age, and its reply text led by a question mark, and no directory", (t) =>
  withApp(t.signal, {}, (container) => {
    const element = rowElement(container, "a");
    assert.equal(element.querySelector(".title")?.textContent, "build green");
    assertNone(element.querySelector(".title-dir"), "the row draws no directory");
    assert.equal(element.querySelector(".label") === null && element.querySelector(".proj") === null, true, "no label or project span");
    assert.equal(element.querySelector(".age")?.textContent, "11m");
    assert.equal(element.querySelector(".l2")?.textContent, "Ship it?", "the question is the second line");
    assert.ok(element.querySelector(".l2.asks svg"), "the question is led by a question mark");
  }));

// stateRows has a row in each state a row can carry, one waiting with a question, one without, and two seen.
function stateRows(): Row[] {
  return [
    row({ key: "q", state: "waiting", question: "Ship it?", changed: 300, seen: 300 }),
    row({ key: "w", state: "waiting", changed: 250, seen: 250 }),
    row({ key: "k", state: "working", changed: 200, seen: 200 }),
    row({ key: "d", state: "done", changed: 150, seen: 150 }),
    row({ key: "i", state: "interrupted", changed: 100, seen: 100 }),
  ];
}

test("a waiting row with a question leads its second line with a question mark, and no row draws a type tag", (t) =>
  withApp(t.signal, { rows: stateRows() }, (container) => {
    const leadsWithMark = (key: string) => rowElement(container, key).querySelector(".l2.asks svg") !== null;
    assert.equal(leadsWithMark("q"), true, "a waiting row with a question");
    assert.equal(leadsWithMark("w"), false, "a waiting row without one");
    assert.equal(container.querySelector(".ty"), null, "no row draws a type tag");
  }));

test("no row draws a state element, and no row's title holds its state word", (t) =>
  withApp(t.signal, { rows: stateRows() }, (container) => {
    const rows = [...container.querySelectorAll("[data-row]")];
    assert.equal(
      rows.some((element) => element.querySelector(".state") !== null),
      false,
      "no row draws a .state element",
    );
    for (const [key, word] of [
      ["k", "working"],
      ["d", "done"],
      ["i", "interrupted"],
    ]) {
      assert.equal(rowElement(container, key).querySelector(".title")?.textContent?.includes(word), false, `row ${key} has no ${word}`);
    }
  }));

test("each row's glyph carries its state", (t) =>
  withApp(t.signal, { rows: stateRows() }, (container) => {
    const glyphOf = (key: string) => rowElement(container, key).querySelector("svg.glyph")?.getAttribute("data-state") ?? null;
    assert.equal(glyphOf("q"), "waiting");
    assert.equal(glyphOf("k"), "working");
    assert.equal(glyphOf("d"), "done");
    assert.equal(glyphOf("i"), "interrupted");
  }));

test("an unseen row's reply text runs two lines, and a seen row's runs one", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({ key: "u", kind: "session", label: "u", state: "done", summary: "unseen summary", changed: 20, seen: 10, unseen: true }),
        row({ key: "v", kind: "session", label: "v", state: "done", summary: "seen summary", changed: 5, seen: 5 }),
      ],
    },
    (container) => {
      assert.equal(rowElement(container, "u").querySelector(".l2")?.getAttribute("class"), "l2", "u is unseen");
      assert.equal(rowElement(container, "v").querySelector(".l2")?.getAttribute("class"), "l2 one", "v is seen");
    },
  ));

test("a session row that is not waiting shows its summary, not its question", (t) =>
  withApp(t.signal,
    {
      rows: [row({ key: "s", kind: "session", state: "working", question: "stale?", summary: "compiling" })],
    },
    (container) => {
      assert.equal(rowElement(container, "s").querySelector(".l2")?.textContent, "compiling", "only a waiting row shows its question");
    },
  ));

test("reply text renders as text, never as markup", (t) =>
  withApp(t.signal,
    {
      rows: [row({ key: "x", kind: "session", state: "working", summary: "<script>alert(1)</script> and <img src=x onerror=alert(1)>" })],
    },
    (container) => {
      assert.equal(container.querySelector("script") === null, true);
      assert.equal(container.querySelector("img") === null, true);
      assert.equal(
        rowElement(container, "x").querySelector(".l2")?.textContent,
        "<script>alert(1)</script> and <img src=x onerror=alert(1)>",
      );
    },
  ));

test("the head, the chips, the rows and the foot sit inside the list, in that order, and no head block is outside it", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.deepEqual(listChildren(container), ["inbox"]);
    assert.deepEqual(inboxChildren(container), ["inbox-head", "inbox-attn", "inbox-rows", "inbox-foot"]);
    assert.deepEqual(outsideList(container, ".inbox-head, .inbox-foot"), []);
  }));

test("a failed write's line shows in the list, under the head and above the rows", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.failNext.add(markPanePath);
    await press("x");
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the seen failure line");
    assert.deepEqual(inboxChildren(container), ["inbox-head", "error", "inbox-attn", "inbox-rows", "inbox-foot"]);
    assert.deepEqual(outsideList(container, ".error"), []);
  }));

test("no error line shows when nothing failed", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.equal(container.querySelector('[role="alert"]') === null, true);
  }));

test("the brand draws the orrery mark as a 72-unit drawing hidden from assistive tech", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.equal(container.querySelector(".inbox-head > svg.mark")?.getAttribute("viewBox"), "18 18 72 72");
    assert.equal(container.querySelector(".inbox-head > svg.mark")?.getAttribute("aria-hidden"), "true");
    assert.equal(container.querySelector(".inbox-head > svg.mark")?.getAttribute("style"), null);
    assert.equal(container.querySelectorAll(".inbox-head > svg.mark > rect").length, 1);
    assert.equal(container.querySelectorAll(".inbox-head > svg.mark > path").length, 5);
  }));

test("the brand reads orrery, and holds the New button", (t) =>
  withApp(t.signal, { rows: [] }, (container) => {
    assert.equal(container.querySelector(".inbox-head")?.textContent, "orreryNew", "the head holds the name and the New button");
  }));

test("NewPanel: no control in the page reads Ask", (t) =>
  withApp(t.signal, {}, (container) => {
    const labels = [...container.querySelectorAll("button")].map((button) =>
      [button.textContent, button.getAttribute("aria-label"), button.title].join(" "),
    );
    assert.deepEqual(
      labels.filter((label) => /\bAsk\b/.test(label)),
      [],
      "no button reads Ask",
    );
  }));

test("NewPanel: the head's New button has a plus icon and the tip Start a session, or find one  N", (t) =>
  withApp(t.signal, {}, (container) => {
    const button = newButton(container);
    assert.equal(button.getAttribute("title"), "Start a session, or find one  N");
    assert.equal(button.querySelector("svg.icon > path")?.getAttribute("d"), "M8 3v10M3 8h10");
    assert.equal(button.querySelector("svg")?.getAttribute("aria-hidden"), "true");
  }));

test("KeyList: the foot's Keys control is icon-only, has the tip Keys  ?, and opens the Keys panel", (t) =>
  withApp(t.signal, {}, async (container) => {
    const keys = keysButton(container);
    assert.equal(keys.tagName, "BUTTON");
    assert.equal(keys.getAttribute("aria-label"), "Keys");
    assert.equal(keys.getAttribute("title"), "Keys  ?");
    assert.equal(keys.textContent, "", "the Keys control has no text");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
    await clickElement(keys);
    const help = container.querySelector<HTMLElement>('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "clicking Keys opens the Keys panel");
    assert.equal(help.querySelector("h2.t-title")?.textContent, "Keys");
  }));

test("the cursor's pane row opens its conversation, and the reader shows the streamed reply as text", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    assert.equal(stream.body.pane, "a");
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed reply" }],
    });
    await until(t.signal, () => readerText(container).includes("the streamed reply"), "the streamed reply");
  }));

test("the cursor's reader opens on the title row: a back button and the crumb of the label, and its state on the dock", (t) =>
  withApp(t.signal, { rows: [paneRow] }, (container) => {
    assert.equal(
      container.querySelector('section[aria-label="reader"]')?.firstElementChild?.getAttribute("class"),
      "convo-head",
    );
    assert.equal(readerHead(container)?.getAttribute("class"), "title-row");
    assert.equal(readerHead(container)?.querySelector(".back")?.getAttribute("aria-label"), "back");
    assert.deepEqual(crumbTexts(readerHead(container)), ["app", "build green"]);
    assert.equal(dockWord(container), "waiting on you");
    assert.equal(dockBox(container)?.querySelector(".statebar .clock") === null, true);
  }));

test("a session's conversation draws the title row: a back button and the crumb of the session's label, and its state on the dock with no tool", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, (container) => {
    assert.equal(readerHead(container)?.getAttribute("class"), "title-row");
    assert.equal(readerHead(container)?.querySelector(".back")?.getAttribute("aria-label"), "back");
    assert.deepEqual(crumbTexts(readerHead(container)), ["notes"]);
    assert.equal(dockWord(container), "working");
    assert.equal(dockBox(container)?.querySelector(".statebar .name") === null, true);
  }));

test("a step's head names its epic, agent and round, then the step, and its dock's line runs from the step's start", (t) => {
  const running = makeStep("s1", { started: BigInt(now - 60) });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [running] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.deepEqual(crumbTexts(readerHead(container)), ["first › builder · step · round 1", "s1"]);
    assert.equal(dockWord(container), "running");
    assert.equal(dockBox(container)?.querySelector(".statebar .clock")?.textContent, "1:00");
  });
});

test("a held step's dock line reads held with no clock", (t) => {
  const held = makeStep("s1", { held: true });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [held] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(dockWord(container), "held");
    assert.equal(dockBox(container)?.querySelector(".statebar .clock") === null, true);
  });
});

test("each step box takes its own step's held flag, and a box the cursor left keeps its own", (t) => {
  const epics = epicsView(
    [makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1", { held: true }), makeStep("s2")] })],
    new Map(),
  );
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(toneOf(composer(container)), "held");
    assert.equal(composer(container).placeholder, "Nudge s1. It resumes with your words");
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    assert.equal(toneOf(composer(container)), "working");
    assert.equal(composer(container).placeholder, "Nudge s2. It interrupts the turn, then gives it your words");
    assert.equal(toneOf(stepInputNamed(container, "s1")), "held", "s1's hidden box keeps its held tone");
  });
});

test("a working pane's delta puts its tool and time on the dock's line, and draws no note under the reader", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => dockWord(container) === "working", "the working line");
    assert.equal(dockBox(container)?.querySelector(".statebar .name")?.textContent, "Bash");
    assert.equal(dockBox(container)?.querySelector(".statebar .clock")?.textContent, "1:00");
    assert.equal(readerNote(container) === null, true, "the cursor's reader draws no working note");
    assert.equal(noteWords(container.querySelector('section[aria-label="reader"]')), false, "the reader's text names no note");
    assert.equal(container.querySelector(".work-line") === null, true, "no work line is drawn");
  }));

test("a later delta whose pane is done draws no working note", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => dockBox(container)?.querySelector(".statebar .name")?.textContent === "Bash", "the working tool");
    fake.emit(stream, {
      transcript: "t1",
      seq: "2",
      items: [],
      state: { pane: { id: "a", state: "done", tool: "", changed: "1000", lastTurnEnd: "1000" } },
    });
    await until(t.signal, () => dockWord(container) === "done", "the done line");
    assert.equal(dockBox(container)?.querySelector(".statebar .name") === null, true);
    assert.equal(readerNote(container) === null, true, "the cursor's reader draws no working note");
  }));

test("DockState: a working pane's head draws no state word or pill, and its dock's line reads working", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => dockWord(container) === "working", "the working line");
    assert.equal(readerHead(container)?.querySelector(".state") === null, true, "the head draws no state word");
    assert.equal(readerHead(container)?.querySelector(".pill") === null, true, "the head draws no pill");
  }));

test("DockState: a waiting row's head draws no state word or pill, and its dock's line reads waiting on you", (t) =>
  withApp(t.signal, { rows: [paneRow] }, (container) => {
    assert.equal(readerHead(container)?.querySelector(".state") === null, true, "the head draws no state word");
    assert.equal(readerHead(container)?.querySelector(".pill") === null, true, "the head draws no pill");
    assert.equal(dockWord(container), "waiting on you");
  }));

test("moving the cursor to a session row aborts the pane's stream and opens the session's", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [pane] = fake.callsTo(paneWatchPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(pane.signal.aborted, true, "the pane stream was aborted");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    assert.equal(fake.callsTo(sessionWatchPath)[0].body.id, "s1");
  }));

test("leaving the page stops the cursor's stream", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    await act(() => {
      render(null, container);
    });
    assert.equal(stream.signal.aborted, true, "the stream was aborted on unmount");
  }));

test("Enter in the composer sends the text to the pane with Panes.SendText", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement === box, true);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    await until(t.signal, () => box.value === "", "the sent draft cleared");
  }));

test("a click on the send button sends the cursor's text once with Panes.SendText", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await act(() => {
      composerSendButton(container).dispatchEvent(clickEvent());
    });
    await until(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    await settle();
    assert.equal(fake.callsTo(sendTextPath).length, 1, "the click sent once");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    await until(t.signal, () => box.value === "", "the sent draft cleared");
  }));

test("the cursor's reader is its head, its conversation and its foot, and nothing else", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="reader"]')), [
      "div.convo-head",
      "div.reader",
      "div.send",
    ]);
  }));

test("a step's reader is its title row, its conversation and its foot, and nothing else", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="reader"]')), [
      "header.title-row",
      "div.reader",
      "div.send",
    ]);
  }));

test("a step's reader draws its brief as a fold, its round's checks as a card, and the owner's nudge as YOU", (t) =>
  withApp(t.signal, { epics: checkedEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    const [stream] = fake.callsTo(sessionWatchPath);
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [
        { kind: "prompt", text: "the brief", sender: "brief" },
        { kind: "prompt", text: "lint failed", sender: "engine", round: 1 },
        { kind: "prompt", text: "please hurry", sender: "owner" },
      ],
    });
    const reader = '[aria-label="reader"] > .reader';
    await until(t.signal, () => container.querySelector(`${reader} .you`) !== null, "the owner's nudge");
    assert.equal(container.querySelectorAll(`${reader} button.fold.brief`).length, 1, "one brief fold");
    assert.equal(container.querySelector(`${reader} button.fold.brief b`)?.textContent, "Brief");
    assert.equal(container.querySelectorAll(`${reader} .card`).length, 1, "one round card");
    assert.equal(container.querySelectorAll(`${reader} .card.bad`).length, 1, "a bad edge");
    const cap = container.querySelector(`${reader} .card .cap`)?.textContent ?? "";
    assert.equal(cap.startsWith("Checks · round 1"), true, "the card names round 1");
    const chips = [...container.querySelectorAll(`${reader} .card .chips .chip`)].map((chip) => chip.textContent);
    assert.deepEqual(chips, ["lint ✗", "unit ✓"], "round 1's checks only, in the step's order");
    assert.equal(container.querySelector(`${reader} .card .note`)?.textContent, "lint failed");
    assert.equal(container.querySelectorAll(`${reader} .you`).length, 1, "one YOU bubble");
    const you = container.querySelector(`${reader} .you`)?.textContent ?? "";
    assert.equal(you.includes("please hurry"), true, "the YOU bubble holds the nudge");
    assert.equal(you.includes("lint failed"), false, "the YOU bubble holds no engine text");
    assert.equal(you.includes("the brief"), false, "the YOU bubble holds no brief");
  }));

test("a pane row's reader draws its prompts as YOU, whatever their sender and round", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "prompt", text: "lint failed", sender: "engine", round: 1 }],
    });
    const reader = '[aria-label="reader"] > .reader';
    await until(t.signal, () => container.querySelector(`${reader} .you`) !== null, "the prompt");
    assert.equal(container.querySelectorAll(`${reader} .card`).length, 0, "no round card");
    assert.equal(container.querySelectorAll(`${reader} .fold`).length, 0, "no fold");
    const you = container.querySelector(`${reader} .you`)?.textContent ?? "";
    assert.equal(you.includes("lint failed"), true, "the YOU bubble holds the prompt");
  }));

test("the pinned reader is its head, its conversation and its foot, and nothing else", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="pinned reader"]')), [
      "div.convo-head",
      "div.reader",
      "div.send",
    ]);
  }));

test("the cursor's queued messages dock in its foot, oldest first, and draw nowhere in its reader", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "a reply" }],
      state: {
        waiting: [
          { text: "first waiting", canceled: false },
          { text: "second waiting", canceled: true },
        ],
      },
    });
    await until(t.signal, () => queueTexts(container).length === 2, "the queued messages");
    assert.deepEqual(queueTexts(container), ["first waiting", "second waiting"]);
    assert.equal(container.querySelectorAll('[aria-label="reader"] .send .tray-row.cancelled').length, 1);
    const reader = container.querySelector('[aria-label="reader"] > .reader')?.textContent ?? "";
    assert.equal(reader.includes("first waiting"), false, "the reader draws no queued message");
    assert.equal(reader.includes("second waiting"), false, "the reader draws no canceled message");
    assert.ok(reader.includes("a reply"));
  }));

test("a row's queued messages draw in no other foot: the pin keeps its own, and a hidden composer shows none", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    fake.emit(pinnedStream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { waiting: [{ text: "pinned waiting", canceled: false }] },
    });
    await until(t.signal, () => pinnedQueueTexts(container).length === 1, "the pinned queue");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const cursorStream = fake.callsTo(paneWatchPath).find((call) => call.body.pane === "c");
    assert.ok(cursorStream !== undefined, "the cursor's stream on c");
    fake.emit(cursorStream, {
      transcript: "t2",
      seq: "1",
      reset: true,
      items: [],
      state: { waiting: [{ text: "c waiting", canceled: false }] },
    });
    await until(t.signal, () => queueTexts(container).includes("c waiting"), "c's queue");
    assert.deepEqual(queueTexts(container), ["c waiting"], "only c's composer draws c's queue");
    assert.deepEqual(pinnedQueueTexts(container), ["pinned waiting"]);
    assert.equal(container.querySelector(".reply[hidden] .tray") === null, true, "a's hidden composer draws no tray");
  }));

test("Enter in a session's composer sends the text with Control.Say and a prompt id", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(t.signal, () => fake.callsTo(sayPath).length === 1, "the say");
    const [say] = fake.callsTo(sayPath);
    assert.equal(say.body.id, "s1");
    assert.equal(say.body.text, "keep going");
    assert.match(String(say.body.promptId), /^[0-9a-f-]{36}$/);
  }));

const sendUnknownNote = "No answer from the server. It may have been sent: check the conversation before sending again.";

test("a message sent to a headless session draws at once as a sending row, and its stored prompt replaces it", (t) =>
  withApp(t.signal, { rows: [{ ...sessionRow, state: "done" }] }, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const [stream] = fake.callsTo(sessionWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [] });
    const releaseSay = fake.holdNext(sayPath);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await untilTurn(t.signal, () => fake.callsTo(sayPath).length === 1, "the say");
    const [say] = fake.callsTo(sayPath);
    assert.equal(sendingRows(container).length, 1, "the row draws while the say is pending");
    assert.equal(sendingRows(container)[0].textContent?.includes("keep going"), true, "the row holds the text");
    assert.equal(sendingMark(sendingRows(container)[0]), "sending", "the row is marked sending");

    releaseSay();
    await untilTurn(t.signal, () => composer(container).value === "", "the say was answered");
    assert.equal(sendingRows(container).length, 1, "the row stays until the stored prompt arrives");

    fake.emit(stream, {
      transcript: "t1",
      seq: "2",
      reset: false,
      items: [{ kind: "prompt", text: "keep going", promptId: String(say.body.promptId) }],
    });
    await untilTurn(t.signal, () => sendingRows(container).length === 0, "the sending row leaves");
    assert.equal(ordinaryPromptRows(container).length, 1, "the stored prompt is the one ordinary prompt row");
    assert.equal(ordinaryPromptRows(container)[0].textContent?.includes("keep going"), true);

    fake.emit(stream, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "a later reply" }] });
    await untilTurn(t.signal, () => readerText(container).includes("a later reply"), "the reset that drops the prompt");
    assert.equal(sendingRows(container).length, 0, "a sent message that left never draws again");
  }));

test("a message sent to a pane draws as a sending row until the pane's stream delivers its prompt", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [] });
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await untilTurn(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    await untilTurn(t.signal, () => composer(container).value === "", "the sent draft cleared");
    assert.equal(sendingRows(container).length, 1, "the row draws while the stream has not shown the prompt");
    assert.equal(sendingMark(sendingRows(container)[0]), "sending");

    fake.emit(stream, { transcript: "t1", seq: "2", items: [{ kind: "prompt", text: "keep going" }] });
    await untilTurn(t.signal, () => sendingRows(container).length === 0, "the sending row leaves");
    assert.equal(ordinaryPromptRows(container).length, 1, "the stored prompt is the one ordinary prompt row");

    fake.emit(stream, { transcript: "t1", seq: "3", reset: true, items: [{ kind: "assistant", text: "a later reply" }] });
    await untilTurn(t.signal, () => readerText(container).includes("a later reply"), "the reset that drops the prompt");
    assert.equal(sendingRows(container).length, 0, "a sent message that left never draws again");
  }));

test("a message sent from the pinned composer draws as a sending row in the pinned reader until its prompt arrives there", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    fake.emit(pinnedStream, { transcript: "t1", seq: "1", reset: true, items: [] });
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c", "the cursor moves off the pinned row");
    await typeInto(pinnedComposer(container), "keep going");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "Enter");
    await untilTurn(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    assert.equal(sendingRows(container, "pinned reader").length, 1, "the pinned reader draws the sending row");
    assert.equal(sendingRows(container).length, 0, "the cursor's reader on c draws none");

    fake.emit(pinnedStream, { transcript: "t1", seq: "2", items: [{ kind: "prompt", text: "keep going" }] });
    await untilTurn(t.signal, () => sendingRows(container, "pinned reader").length === 0, "the pinned sending row leaves");
    assert.equal(ordinaryPromptRows(container, "pinned reader").length, 1, "the stored prompt is the one pinned prompt row");
  }));

test("a failed send to a session leaves no sending row, and keeps the draft and its error line", (t) =>
  withApp(t.signal, { rows: [{ ...sessionRow, state: "done" }] }, async (container, fake) => {
    const releaseSay = fake.holdNext(sayPath);
    fake.failNext.add(sayPath);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await untilTurn(t.signal, () => fake.callsTo(sayPath).length === 1, "the say");
    assert.equal(sendingRows(container).length, 1, "the row draws while the say is pending");
    releaseSay();
    await untilTurn(t.signal, () => container.querySelector('.dock .inote') !== null, "the failed send's line");
    assert.equal(container.querySelector('.dock .inote')?.textContent, sendUnknownNote);
    assert.equal(sendingRows(container).length, 0, "no sending row is left");
    assert.equal(composer(container).value, "keep going", "the draft is kept");
  }));

test("a message sent to a working session draws above the box as queued, and leaves when the row's turn ends", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await press("i");
    await typeInto(composer(container), "after the turn");
    await pressIn(composer(container), "Enter");
    await untilTurn(t.signal, () => queueTexts(container).length === 1, "the queued send");
    assert.deepEqual(queueTexts(container), ["after the turn"]);
    assert.equal(container.querySelector('[aria-label="reader"] .send .tray-head .lbl')?.textContent, "Queued 1");
    const queued = container.querySelector('[aria-label="reader"] .send .tray-row');
    assertNone(queued?.querySelector(".lbl"), "a waiting row carries no label, since the head names it");
    assertNone(queued?.querySelector(".note"), "a waiting row carries no note on when it sends");
    await mount(container, {
      rows: [{ ...sessionRow, state: "done" }],
      now,
      clients: fake.clients,
      seen: fake.seen,
    });
    await untilTurn(t.signal, () => queueTexts(container).length === 0, "the queue after the turn");
  }));

test("a message sent to a working pane draws above the box as queued, and leaves when the pane's turn ends", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await untilTurn(t.signal, () => composer(container).placeholder.startsWith("Queue a message for"), "the working placeholder");
    await press("i");
    await typeInto(composer(container), "after the turn");
    await pressIn(composer(container), "Enter");
    await untilTurn(t.signal, () => queueTexts(container).length === 1, "the queued send");
    assert.deepEqual(queueTexts(container), ["after the turn"]);
    assertNone(
      container.querySelector('[aria-label="reader"] .send .tray-row .note'),
      "a waiting row carries no note on when it sends",
    );

    fake.emit(stream, { transcript: "t1", seq: "2", items: [], state: { pane: { id: "a", state: "idle" } } });
    await untilTurn(t.signal, () => queueTexts(container).length === 0, "the queue after the turn");
  }));

test("a draft survives moving the cursor away and back", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pullPath, [{ text: "" }]);
    await press("i");
    const box = composer(container);
    await typeInto(box, "half a thought");
    await pressIn(box, "Escape");
    await until(t.signal, () => document.activeElement !== box, "the blur after the pull");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("k");
    assert.equal(cursorKey(container), "a");
    assert.equal(composer(container).value, "half a thought");
  }));

test("a failed send keeps its prompt id when the cursor moves away and back", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    fake.failNext.add(sayPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(t.signal, () => container.querySelector('.dock .inote') !== null, "the failed send's line");
    await act(() => {
      box.blur();
    });
    await press("k");
    assert.equal(cursorKey(container), "a");
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(composer(container).value, "keep going", "the draft survived");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => fake.callsTo(sayPath).length === 2, "the second say");
    const [first, second] = fake.callsTo(sayPath);
    assert.equal(second.body.text, "keep going");
    assert.equal(second.body.promptId, first.body.promptId, "the retry reused the prompt id");
  }));

test("a failed send keeps its draft and prompt id through a poll that lists no sessions", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    fake.failNext.add(sayPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => container.querySelector('.dock .inote') !== null, "the failed send's line");
    await act(() => {
      composer(container).blur();
    });
    // A poll whose Sessions.List failed lists the panes only; the next poll lists the session again.
    await mount(container, { rows: [paneRow], now, clients: fake.clients, seen: fake.seen });
    await mount(container, { rows: [paneRow, sessionRow], now, clients: fake.clients, seen: fake.seen });
    if (cursorKey(container) !== "s1") {
      await press("j");
    }
    assert.equal(cursorKey(container), "s1");
    assert.equal(composer(container).value, "keep going", "the draft survived the poll");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => fake.callsTo(sayPath).length === 2, "the second say");
    const [first, second] = fake.callsTo(sayPath);
    assert.equal(second.body.promptId, first.body.promptId, "the retry reused the prompt id");
  }));

test("keys typed in the composer never act as list keys, and Escape leaves it", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pullPath, [{ text: "" }]);
    await press("i");
    const box = composer(container);
    await pressIn(box, "j");
    await pressIn(box, "G");
    await pressIn(box, "?");
    await pressIn(box, "/");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
    assert.equal(container.querySelector(".inbox-search input") === document.activeElement, false, "the search field has no focus");
    const bar = container.querySelector<HTMLInputElement>(".find input");
    assert.ok(bar, "/ in the empty box opens the find bar");
    assert.equal(document.activeElement === bar, true, "the find bar's input has focus");
    await pressIn(bar, "Escape");
    assert.equal(document.activeElement === box, true, "Escape in the find bar gives the empty box its focus back");
    await pressIn(box, "Escape");
    await until(t.signal, () => document.activeElement !== box, "the blur after the pull");
    assert.equal(document.activeElement === box, false);
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("i focuses the composer, and o opens and closes a compaction's summary", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "compaction", text: "the summary text", trigger: "auto", tokens: "611000" }],
    });
    await until(t.signal, () => container.querySelector(".compaction") !== null, "the compaction");
    assert.equal(container.querySelector(".summary") === null, true, "the summary is hidden at first");

    await press("o");
    assert.equal(container.querySelector(".summary")?.textContent, "the summary text");
    await press("o");
    assert.equal(container.querySelector(".summary") === null, true);

    await press("i");
    assert.equal(document.activeElement === composer(container), true);
  }));

test("ArrowUp in an empty pane composer pulls the queued text back", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "ArrowUp");
    await until(t.signal, () => box.value === queuedText, "the pulled text");
    assert.equal(fake.callsTo(pullPath).length, 1);
  }));

test("ArrowUp in a session's composer pulls nothing", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "ArrowUp");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    assert.equal(fake.callsTo(pullPath).length, 0, "a session has no queue to pull");
    assert.equal(box.value, "");
  }));

// focusedTextBox is whether a text box has focus: a reply box or a step's box.
function focusedTextBox(): boolean {
  return document.activeElement?.tagName === "TEXTAREA";
}

// clickRow clicks the list row with the key, as a pointer click on it does, and lets Preact draw what the click changed.
async function clickRow(container: HTMLElement, key: string): Promise<void> {
  await act(() => {
    rowElement(container, key).dispatchEvent(clickEvent());
  });
}

test("ComposerFocus: a click on a session row focuses its reply box, and the caret goes to the end of its text", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container) => {
    await clickRow(container, "s1");
    const box = composer(container);
    assert.equal(document.activeElement === box, true, "the session's reply box has focus");
    await typeInto(box, "half a thought");
    box.setSelectionRange(0, 0);
    await pressIn(box, "Tab", { shiftKey: true });
    assert.equal(focusedTextBox(), false, "Shift+Tab left the box");
    await clickRow(container, "s1");
    assert.equal(document.activeElement === box, true, "the click focuses the box again");
    assert.equal(box.selectionStart, box.value.length, "the caret is at the end of the draft");
  }));

test("ComposerFocus: Enter on the cursor's pane row focuses its reply box, with the caret at its end, and still marks the row seen", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await typeInto(composer(container), "half a thought");
    composer(container).setSelectionRange(0, 0);
    await press("j");
    await press("k");
    assert.equal(cursorKey(container), "a");
    await press("Enter");
    const box = composer(container);
    assert.equal(document.activeElement === box, true, "Enter focuses the cursor's reply box");
    assert.equal(box.selectionStart, box.value.length, "the caret is at the end of the draft");
    await untilTurn(t.signal, () => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
  }));

test("ComposerFocus: a click or Enter on an epic line or a step focuses no reply box", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container) => {
    await clickRow(container, `epic:${widgetsRepo}/first`);
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    assert.equal(focusedTextBox(), false, "the epic line focuses no box");
    await press("Enter");
    assert.equal(focusedTextBox(), false, "Enter on the epic line focuses no box");
    await clickRow(container, "step:s1");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(focusedTextBox(), false, "the step focuses no box");
    await press("Enter");
    assert.equal(focusedTextBox(), false, "Enter on the step focuses no box");
  }));

test("ComposerFocus: j, k, G, gg, n, w and e move the cursor and focus no reply box", (t) =>
  withApp(t.signal, {}, async (container) => {
    const moves: [string[], string][] = [
      [["j"], "c"],
      [["k"], "a"],
      [["G"], "d"],
      [["g", "g"], "a"],
      [["n"], "c"],
      [["w"], "a"],
      [["e"], "c"],
    ];
    for (const [keys, target] of moves) {
      await pressEach(keys);
      assert.equal(cursorKey(container), target, `${keys.join("")} moves the cursor to ${target}`);
      assert.equal(focusedTextBox(), false, `${keys.join("")} focuses no reply box`);
    }
  }));

test("ComposerFocus: Escape in the focused reply box of an idle session leaves the box, and interrupts nothing", (t) =>
  withApp(t.signal, { rows: [{ ...sessionRow, state: "done" }] }, async (container, fake) => {
    await clickRow(container, "s1");
    const box = composer(container);
    assert.equal(document.activeElement === box, true, "the reply box has focus");
    await pressIn(box, "Escape");
    await untilTurn(t.signal, () => document.activeElement !== box, "the blur");
    assert.equal(fake.callsTo(withdrawPath).length, 0, "an idle session asks for no withdraw");
    assert.equal(fake.callsTo(interruptPath).length, 0, "an idle session is not interrupted");
  }));

test("ComposerFocus: Shift+Tab in the reply box of a working session leaves it for the cursor's row, takes nothing back and interrupts nothing", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    await clickRow(container, "s1");
    const box = composer(container);
    assert.equal(document.activeElement === box, true, "the reply box has focus");
    const event = await pressIn(box, "Tab", { shiftKey: true });
    assert.equal(event.defaultPrevented, true, "Shift+Tab is prevented");
    assert.equal(document.activeElement === box, false, "the box lost focus");
    assert.equal(document.activeElement, rowElement(container, "s1"), "the cursor's row has focus");
    await letTurnsRun();
    assert.equal(fake.callsTo(withdrawPath).length, 0, "nothing is taken back");
    assert.equal(fake.callsTo(interruptPath).length, 0, "nothing is interrupted");
    await press("k");
    assert.equal(cursorKey(container), "a", "the list's keys work again on the working row");
  }));

test("ComposerFocus: | focuses the pinned row's reply box, and Shift+Tab leaves it for the cursor's row with no pull or key", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await press("|");
    assert.equal(document.activeElement === pinnedComposer(container), true, "the pinned reply box has focus");
    const event = await pressIn(pinnedComposer(container), "Tab", { shiftKey: true });
    assert.equal(event.defaultPrevented, true, "Shift+Tab is prevented");
    assert.equal(document.activeElement, rowElement(container, "b"), "the cursor's row has focus");
    await letTurnsRun();
    assert.equal(fake.callsTo(pullPath).length, 0, "nothing is pulled back");
    assert.equal(fake.callsTo(sendKeysPath).length, 0, "no Escape key is sent");
    await press("k");
    assert.equal(cursorKey(container), "c", "the list's keys work again");
  }));

test("ComposerFocus: closing the pin with | focuses no reply box", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("|");
    assert.equal(document.activeElement === pinnedComposer(container), true, "the pinned reply box has focus");
    await pressIn(pinnedComposer(container), "Tab", { shiftKey: true });
    await press("|");
    assert.equal(pinnedColumn(container), null, "the pin is closed");
    assert.equal(focusedTextBox(), false, "closing the pin focuses no box");
  }));

test("ComposerFocus: the keys help says Enter opens the row into its reply box, and names ⇧ ⇥, ⇞, ⇟ and /", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("?");
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "help panel is open");
    const described = (cap: string) =>
      [...help.querySelectorAll(".keyline")]
        .filter((line) => [...(line.querySelector(".ks")?.children ?? [])].some((item) => item.textContent === cap))
        .map((line) => line.children[1]?.textContent ?? "")
        .join(" | ");
    assert.match(described("⏎"), /open the row and go to its reply box/);
    assert.match(described("⇧ ⇥"), /from a reply box, back to the inbox/);
    assert.match(described("⇞"), /in an empty reply box: page the conversation/);
    assert.match(described("⇟"), /in an empty reply box: page the conversation/);
    assert.match(described("/"), /in an empty reply box: find in the conversation/);
  }));

// The stubbed reader is a box 400 px tall over 2000 px of content, and a page moves it the box less the 40 px it keeps on
// screen. pagingWidth is the narrowest window a reader pages at, the width its paging query starts at.
const readerBoxPixels = 400;
const readerContentPixels = 2000;
const pageOverlapPixels = 40;
const pageScreenPixels = readerBoxPixels - pageOverlapPixels;
const pagingWidth = 760;

// stubReaderBox gives a reader the box and content heights a browser would lay out, which happy-dom does not.
function stubReaderBox(reader: HTMLElement): void {
  Object.defineProperties(reader, {
    clientHeight: { configurable: true, get: () => readerBoxPixels },
    scrollHeight: { configurable: true, get: () => readerContentPixels },
  });
}

// cursorReaderOf is the conversation root of the cursor's reader, and pinnedReaderOf of the pinned reader.
function cursorReaderOf(container: HTMLElement): HTMLElement {
  const reader = container.querySelector<HTMLElement>('section[aria-label="reader"] > .reader');
  assert.ok(reader !== null, "the cursor's reader is drawn");
  return reader;
}

function pinnedReaderOf(container: HTMLElement): HTMLElement {
  const reader = container.querySelector<HTMLElement>('section[aria-label="pinned reader"] > .reader');
  assert.ok(reader !== null, "the pinned reader is drawn");
  return reader;
}

test("ReaderPaging: Page Down and Page Up in a session's empty reply box page the cursor's reader a screen, and are prevented", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container) => {
    window.innerWidth = pagingWidth;
    await clickRow(container, "s1");
    const box = composer(container);
    assert.equal(document.activeElement === box, true, "the session's reply box has focus");
    const reader = cursorReaderOf(container);
    stubReaderBox(reader);

    const down = await pressIn(box, "PageDown");
    assert.equal(down.defaultPrevented, true, "Page Down is prevented");
    assert.equal(reader.scrollTop, pageScreenPixels, "Page Down moves the cursor's reader down a page");

    const up = await pressIn(box, "PageUp");
    assert.equal(up.defaultPrevented, true, "Page Up is prevented");
    assert.equal(reader.scrollTop, 0, "Page Up moves the cursor's reader back up a page");
  }));

test("ReaderPaging: with text in a session's reply box, Page Down leaves the cursor's reader alone and is not prevented", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container) => {
    window.innerWidth = pagingWidth;
    await clickRow(container, "s1");
    const box = composer(container);
    const reader = cursorReaderOf(container);
    stubReaderBox(reader);
    await typeInto(box, "half a thought");

    const event = await pressIn(box, "PageDown");
    assert.equal(event.defaultPrevented, false, "Page Down in a box with text is left to the box");
    assert.equal(reader.scrollTop, 0, "the cursor's reader does not scroll");
  }));

test("ReaderPaging: Page Down in the pinned column's empty reply box pages the pinned reader, and not the cursor's", (t) =>
  withApp(t.signal, {}, async (container) => {
    window.innerWidth = pagingWidth;
    await press("|");
    const box = pinnedComposer(container);
    assert.equal(document.activeElement === box, true, "the pinned reply box has focus");
    const cursor = cursorReaderOf(container);
    const pinned = pinnedReaderOf(container);
    stubReaderBox(cursor);
    stubReaderBox(pinned);

    const event = await pressIn(box, "PageDown");
    assert.equal(event.defaultPrevented, true, "Page Down is prevented");
    assert.equal(pinned.scrollTop, pageScreenPixels, "the pinned reader pages down a screen");
    assert.equal(cursor.scrollTop, 0, "the cursor's reader stays where it was");
  }));

test("Enter on the list writes Panes.MarkSeen for the cursor's pane row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("Enter");
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    assert.equal(cursorKey(container), "a");
  }));

test("x on the list writes Panes.MarkSeen for a pane row and Control.Seen for a session row", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    await press("x");
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the pane write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("x");
    await until(t.signal, () => fake.callsTo(markSessionPath).length === 1, "the session write");
    assert.deepEqual(fake.callsTo(markSessionPath)[0].body, { id: "s1" });
  }));

test("InboxTree: e writes seen for the cursor's row, then rests on the next unseen row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(cursorKey(container), "a");
    await press("e");
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    assert.equal(cursorKey(container), "c");
  }));

test("Enter in a composer sends its text and writes no seen, even for keys typed as shortcuts", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "x");
    await pressIn(box, "e");
    await pressIn(box, "Enter");
    await until(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
    assert.equal(fake.callsTo(markSessionPath).length, 0);
  }));

test("resting one second on an unseen row with recent input writes seen once", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.input.emit("keydown");
    fake.seen.advance(999);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "not yet a second");
    fake.seen.advance(1);
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the dwell write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 1, "the row is written once");
  }));

test("a hidden page writes no seen on the fake clock", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.state.visibilityState = "hidden";
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
  }));

test("an unfocused page writes no seen on the fake clock", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.state.focused = false;
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
  }));

test("a page refocused with no input in the last 30 s writes no seen until input comes", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.state.focused = false;
    fake.seen.input.emit("keydown");
    fake.seen.advance(31000);
    fake.seen.state.focused = true;
    fake.seen.page.emit("focus");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "the last input is over 30 s old");
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the write after fresh input");
  }));

test("a row that changes while the cursor rests on it is written again after one second", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the first dwell write");
    await until(t.signal, () => !rowElement(container, "a").hasAttribute("data-unseen"), "the row to read as seen");

    const moved = fixture().map((item) => (item.key === "a" ? { ...item, changed: 400 } : item));
    await mount(container, { rows: moved, now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true, "the newer change is unseen");
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await until(t.signal, () => fake.callsTo(markPanePath).length === 2, "the second dwell write");
  }));

test("a written row stops showing unseen until its changed time moves past the one it had", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true);
    await press("x");
    await until(t.signal, () => !rowElement(container, "a").hasAttribute("data-unseen"), "the row to read as seen");

    await mount(container, { rows: fixture(), now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), false, "the server has not caught up yet");

    const moved = fixture().map((item) => (item.key === "a" ? { ...item, changed: 400 } : item));
    await mount(container, { rows: moved, now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true, "a newer change is unseen again");
  }));

test("a failed write leaves the row unseen and shows a fixed line", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.failNext.add(markPanePath);
    await press("x");
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the seen failure line");
    assert.equal(container.querySelector('[role="alert"]')?.textContent, seenFailedText);
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true);
  }));

test("leaving the page stops the seen watch before its dwell writes", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.seen.input.emit("keydown");
    await act(() => {
      render(null, container);
    });
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "the pending dwell was cancelled on unmount");
    assert.equal(fake.seen.input.count(), 0, "the input listeners were removed on unmount");
  }));

const answerableRow = row({
  key: "a",
  label: "api",
  state: "waiting",
  question: "Pick one",
  questionHash: "qh-1",
  changed: 300,
  seen: 100,
  unseen: true,
});

const otherSessionRow = row({
  key: "s2",
  id: "s2",
  archiveId: "s2",
  kind: "session",
  archiveKind: "session",
  label: "docs",
  project: "",
  host: "",
  state: "done",
  changed: 10,
  seen: 10,
});

test("2 on a waiting pane row sends its digit with the row's question hash", (t) =>
  withApp(t.signal, { rows: [answerableRow] }, async (container, fake) => {
    await press("2");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "a", keys: ["2"], expectQuestion: "qh-1" });
    assert.deepEqual(alertTexts(container), [], "no line shows for a sent answer");
  }));

test("1 on a session row answers the request its stream holds with the first option", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed question" }],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(t.signal, () => readerText(container).includes("the streamed question"), "the streamed question");
    await press("1");
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "Yes" });
  }));

test("a stream held for another row never answers the cursor's row", (t) =>
  withApp(t.signal, { rows: [sessionRow, otherSessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed question" }],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(t.signal, () => readerText(container).includes("the streamed question"), "the streamed question");
    await press("j");
    assert.equal(cursorKey(container), "s2");
    await press("1");
    await until(t.signal, () => alertTexts(container).includes(answerFailedText), "the fixed line");
    assert.equal(fake.callsTo(answerPath).length, 0, "the other row's request was not answered");
  }));

test("3 on a row with no answerable question sends nothing and shows the fixed line", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("3");
    await until(t.signal, () => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 0);
    assert.equal(fake.callsTo(answerPath).length, 0);
  }));

test("a rejected answer shows the fixed line, never the server's text, and is not retried", (t) =>
  withApp(t.signal, { rows: [answerableRow] }, async (container, fake) => {
    fake.failNext.add(sendKeysPath);
    await press("2");
    await until(t.signal, () => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 1, "the answer was not retried");
    assert.equal(container.textContent?.includes("queue unreachable"), false, "the server's text is not shown");
  }));

test("1 2 and 3 do nothing while the filter or a composer has focus", (t) =>
  withApp(t.signal, { rows: [answerableRow] }, async (container, fake) => {
    await press("/");
    await press("2");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 0, "a digit in the filter answers nothing");
    await press("Escape");
    await press("i");
    await pressIn(composer(container), "2");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 0, "a digit in the reply box answers nothing");
  }));

test("1 on the list answers the cursor's session row and selects no epic option", (t) =>
  withApp(t.signal, { rows: [sessionRow], epics: pageEpic(optionsQuestion) }, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "s1", "the cursor rests on the session row, past the epic and its step");
    assert.equal(epicPageShown(container), false, "the list draws no epic page");
    // The step entry the cursor passed through streamed the same session, so the row's stream is the last one.
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length > 0, "the session stream");
    const stream = fake.callsTo(sessionWatchPath).at(-1);
    assert.ok(stream, "the session row streams its session");
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed question" }],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(t.signal, () => readerText(container).includes("the streamed question"), "the streamed question");
    await press("1");
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "Yes" });
    assert.equal(fake.callsTo(answerEpicPath).length, 0, "no epic answer is sent");
    assert.equal(optionCards(container).length, 0, "no epic option is drawn to select");
  }));

const fourOptionQuestion = JSON.stringify({
  questions: [
    {
      question: "Which one?",
      options: [
        { label: "Alpha", description: "" },
        { label: "Beta", description: "" },
        { label: "Gamma", description: "" },
        { label: "Delta", description: "" },
      ],
    },
  ],
});

const fourOptionRow = row({
  key: "f",
  label: "api",
  state: "waiting",
  question: fourOptionQuestion,
  questionHash: "qh-f",
  changed: 300,
  seen: 100,
  unseen: true,
});

const permissionRow = row({
  key: "p",
  label: "shell",
  state: "waiting",
  question: JSON.stringify({ tool: "Bash", input: { command: "ls" } }),
  questionHash: "qh-p",
  changed: 300,
  seen: 100,
  unseen: true,
});

const waitingSession = row({
  key: "s1",
  id: "s1",
  archiveId: "s1",
  kind: "session",
  archiveKind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "waiting",
  changed: 30,
  seen: 30,
});

const workingSession = row({
  key: "s1",
  id: "s1",
  archiveId: "s1",
  kind: "session",
  archiveKind: "session",
  label: "notes",
  project: "",
  host: "",
  state: "working",
  changed: 40,
  seen: 30,
});

// permissionStream is the session's stream holding a permission request with two options, allow and deny.
const permissionStream = {
  transcript: "t1",
  seq: "1",
  reset: true,
  items: [{ kind: "assistant", text: "the streamed question" }],
  state: { request: { id: "req-7", kind: "question", question: "Allow the command?", options: ["allow", "deny"] } },
};

// openPermissionRequest waits for the cursor's session stream, then sends the permission request and waits for the
// reader to draw its question card.
async function openPermissionRequest(signal: AbortSignal, container: HTMLElement, fake: FakeServer): Promise<void> {
  await until(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
  fake.emit(fake.callsTo(sessionWatchPath)[0], permissionStream);
  await until(signal, () => container.querySelector(".question-card") !== null, "the question card");
}

test("a waiting pane row draws no option chips, and 2 still sends keys [\"2\"] to its question", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    assert.deepEqual(chipTexts(container, "f"), [], "the row draws no option chips");
    await press("2");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "f", keys: ["2"], expectQuestion: "qh-f" });
  }));

test("a pane permission question draws no option chips on its row", (t) =>
  withApp(t.signal, { rows: [permissionRow] }, (container) => {
    assert.deepEqual(chipTexts(container, "p"), []);
  }));

test("a landed pane answer leaves no line on its row, since the conversation holds the answer", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    await press("2");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    await settle();
    assert.equal(answerLine(container, "f"), null, "no line names the choice");
    assert.deepEqual(chipTexts(container, "f"), [], "the row draws no chips");
  }));

test("a failed answer shows the fixed line and leaves the row with no chips or line", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    fake.failNext.add(sendKeysPath);
    await press("2");
    await until(t.signal, () => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(answerLine(container, "f"), null, "no line for an answer that did not land");
    assert.deepEqual(chipTexts(container, "f"), [], "the row draws no chips");
  }));

test("a waiting session row's held permission request answers deny by its request id, and its row draws no chips", (t) =>
  withApp(t.signal, { rows: [waitingSession] }, async (container, fake) => {
    await openPermissionRequest(t.signal, container, fake);
    await press("2");
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "deny" });
    await settle();
    assert.deepEqual(chipTexts(container, "s1"), [], "the row draws no chips");
    assert.equal(answerLine(container, "s1"), null, "the row draws no answer line");
  }));

test("a session row that turns working after an answer draws no chips and no answer line", (t) =>
  withApp(t.signal, { rows: [waitingSession] }, async (container, fake) => {
    await openPermissionRequest(t.signal, container, fake);
    await press("2");
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    await mount(container, { rows: [workingSession], now, clients: fake.clients, seen: fake.seen });
    assert.equal(answerLine(container, "s1"), null, "no answer line");
    assert.deepEqual(chipTexts(container, "s1"), [], "a working row shows no chips");
  }));

test("a row that waits on another question after an answer draws no chips for it and no answer line", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    await press("2");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    const nextQuestion = JSON.stringify({
      questions: [{ question: "Next?", options: [{ label: "Yes", description: "" }] }],
    });
    await mount(container, {
      rows: [row({ ...fourOptionRow, question: nextQuestion })],
      now,
      clients: fake.clients,
      seen: fake.seen,
    });
    assert.equal(answerLine(container, "f"), null, "no line follows the row to its next question");
    assert.deepEqual(chipTexts(container, "f"), [], "the next question's options are the conversation's, not the row's");
  }));

test("a waiting session row with no held request shows no chips", (t) =>
  withApp(t.signal, { rows: [waitingSession] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    await settle();
    assert.deepEqual(chipTexts(container, "s1"), []);
  }));

test("a working session row shows no chips, though its held stream carries a request", (t) =>
  withApp(t.signal, { rows: [workingSession] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], permissionStream);
    await until(t.signal, () => readerText(container).includes("the streamed question"), "the streamed question");
    assert.deepEqual(chipTexts(container, "s1"), []);
  }));

test("no row of the fixture draws option chips, the waiting one included", (t) =>
  withApp(t.signal, {}, (container) => {
    const drawsChips = ["a", "b", "c", "d"].map((key) => chipTexts(container, key).length > 0);
    assert.deepEqual(drawsChips, [false, false, false, false]);
  }));

// pinnedColumn is the second reader column that | pins beside the list, or null when no row is pinned. The column
// stays mounted while closed, so its composers keep their drafts; a closed column is hidden.
function pinnedColumn(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('[aria-label="pinned reader"]:not([hidden])');
}

// pinnedComposer is the reply box of the open pin: each pinned row keeps its own, and only the open pin's is not hidden.
function pinnedComposer(container: HTMLElement): HTMLTextAreaElement {
  const box = pinnedColumn(container)?.querySelector<HTMLTextAreaElement>(".pin-reply:not([hidden]) textarea");
  assert.ok(box, "the pinned column has a composer");
  return box;
}

// pinnedNote is the working note under the pinned reader, or null when the pinned column draws none.
function pinnedNote(container: HTMLElement): Element | null {
  return pinnedColumn(container)?.querySelector(".work-note") ?? null;
}

test("the pinned title row names the pinned row while the cursor rests on another, and no .pin-head is drawn", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.deepEqual(crumbTexts(pinnedHead(container)), ["api", "build green"]);
    assert.equal(dockWord(container, "pinned reader"), "waiting on you");
    assert.deepEqual(crumbTexts(readerHead(container)), ["docs", "wrote the guide"]);
    assert.equal(dockWord(container), "done");
    assert.equal(container.querySelector(".pin-head") === null, true);
  }));

test("‹ on the cursor's title row moves focus to the list's current row, and i moves it back to the reply box", (t) =>
  withApp(t.signal, {}, async (container) => {
    const current = () => container.querySelector(".row.cur");
    assert.ok(current() !== null, "the list has a current row");
    await press("i");
    assert.equal(document.activeElement === composer(container), true, "the reply box has focus");
    await clickButton(container, 'section[aria-label="reader"] .title-row', "‹");
    assertSame(document.activeElement, current(), "‹ moves focus to the current row");
    await press("i");
    assert.equal(document.activeElement === composer(container), true, "the reply box has focus again");
  }));

test("‹ on the pinned title row closes the pin, as | does", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await untilTurn(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    assert.ok(pinnedColumn(container) !== null, "the pin is open");
    await clickButton(container, 'section[aria-label="pinned reader"] .title-row', "‹");
    assert.equal(pinnedColumn(container), null, "the pin is closed");
    assert.equal(container.querySelector(".read.pinned")?.hasAttribute("hidden"), true, "the pinned reader is hidden");
  }));

test("the pinned column's dock line draws its own stream's state and no note while the cursor rests on another pane", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { pane: workingPane("Bash") },
    });
    await until(t.signal, () => dockWord(container, "pinned reader") === "working", "the pinned line");
    assert.equal(dockBox(container, "pinned reader")?.querySelector(".statebar .name")?.textContent, "Bash");
    assert.equal(dockBox(container, "pinned reader")?.querySelector(".statebar .clock")?.textContent, "1:00");
    assert.equal(pinnedNote(container) === null, true, "the pinned reader draws no working note");
    assert.equal(
      noteWords(container.querySelector('section[aria-label="pinned reader"]')),
      false,
      "the pinned reader's text names no note",
    );
    assert.equal(readerNote(container) === null, true, "the cursor's reader on c draws no note");
    assert.equal(dockBox(container)?.querySelector(".statebar .name") === null, true, "c's line has no tool");
  }));

test("the reader's composer on a working row has the working edge and says it queues", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    assert.equal(toneOf(composer(container)), "working");
    assert.equal(composer(container).placeholder, "Queue a message for web. Claude Code takes it when it can");
  }));

test("a working pane turns a waiting row's composer from answering to queueing", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(toneOf(composer(container)), "waiting");
    assert.equal(composer(container).placeholder, "Answer api");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => composer(container).placeholder.startsWith("Queue a message for"), "the working placeholder");
    assert.equal(toneOf(composer(container)), "working");
    assert.equal(composer(container).placeholder, "Queue a message for api. Claude Code takes it when it can");
  }));

test("a pane on the cursor's stream turns only the shown composer; a visited row's hidden composer keeps its row's state", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream on a");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const stream = fake.callsTo(paneWatchPath).find((call) => call.body.pane === "c");
    assert.ok(stream, "the cursor's stream on c is open");
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => composer(container).placeholder.startsWith("Queue a message for"), "the shown composer's working placeholder");
    assert.equal(toneOf(composer(container)), "working");
    assert.equal(composer(container).placeholder, "Queue a message for docs. Claude Code takes it when it can");
    const hidden = [...container.querySelectorAll<HTMLTextAreaElement>('[aria-label="reader"] .reply[hidden] textarea')];
    const api = hidden.find((box) => box.placeholder === "Answer api");
    assert.equal(api !== undefined, true, "the api row's hidden composer still answers");
    assert.equal(api === undefined ? "" : toneOf(api), "waiting");
  }));

test("the pinned composer follows its own stream's pane while the cursor rests on another row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => pinnedComposer(container).placeholder.startsWith("Queue a message for"), "the pinned working placeholder");
    assert.equal(toneOf(pinnedComposer(container)), "working");
    assert.equal(pinnedComposer(container).placeholder, "Queue a message for api. Claude Code takes it when it can");
    assert.equal(toneOf(composer(container)), "done");
    assert.equal(composer(container).placeholder, "Message docs");
  }));

test("| pins the cursor's row in a second column that streams it while the cursor moves on", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    assert.ok(pinnedColumn(container), "the pinned column is shown");
    const [cursorStream, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "pinned reply" }],
    });
    await until(t.signal, () => pinnedColumn(container)?.textContent?.includes("pinned reply") === true, "the pinned reply");

    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    assert.equal(cursorStream.signal.aborted, true, "the cursor's stream on a was aborted");
    assert.equal(pinnedStream.signal.aborted, false, "the pinned stream on a stays open");
    assert.ok(pinnedColumn(container)?.textContent?.includes("api"), "the pinned column still names row a");
  }));

test("the pinned column's draft survives the cursor's moves, and Enter sends it to the pinned row only", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await typeInto(pinnedComposer(container), "keep going");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(pinnedComposer(container).value, "keep going", "the draft survives the cursor's move");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "Enter");
    await until(t.signal, () => fake.callsTo(sendTextPath).length === 1, "the send");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    await until(t.signal, () => pinnedComposer(container).value === "", "the sent draft cleared");
    assert.equal(fake.callsTo(markPanePath).length, 0, "the send writes no seen for the cursor's row");
  }));

test("| again removes the pinned column and aborts its stream, and the cursor's stream stays", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [cursorStream, pinnedStream] = fake.callsTo(paneWatchPath);
    await press("Tab", { shiftKey: true });
    await press("|");
    assert.equal(pinnedColumn(container) === null, true, "the pinned column is removed");
    assert.equal(pinnedStream.signal.aborted, true, "the pinned stream was aborted");
    assert.equal(cursorStream.signal.aborted, false, "the cursor's stream stays open");
  }));

test("leaving the page stops the pinned stream as well", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    await act(() => {
      render(null, container);
    });
    assert.equal(pinnedStream.signal.aborted, true, "the pinned stream was aborted on unmount");
  }));

test("with the pinned reply box focused, | is text; after it blurs, i focuses the cursor's reply box", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pullPath, [{ text: "" }]);
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "|");
    assert.ok(pinnedColumn(container), "a | typed in the pinned reply box does not close the pin");
    await pressIn(pinnedComposer(container), "Escape");
    await until(
      t.signal,
      () => document.activeElement !== pinnedComposer(container),
      "the blur after the pull",
    );
    assert.equal(document.activeElement === pinnedComposer(container), false, "Escape leaves the pinned reply box");
    await press("i");
    assert.equal(document.activeElement === composer(container), true, "i focuses the cursor's reply box");
  }));

test("a pinned column's draft survives closing the pin and reopening it on the same row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pullPath, [{ text: "" }]);
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await typeInto(pinnedComposer(container), "half a thought");
    await pressIn(pinnedComposer(container), "Escape");
    await until(t.signal, () => document.activeElement !== pinnedComposer(container), "the blur after the pull");
    await press("|");
    assert.equal(pinnedColumn(container) === null, true, "the pin is closed");
    await press("|");
    assert.equal(cursorKey(container), "a");
    assert.equal(pinnedComposer(container).value, "half a thought", "the draft survived the close");
  }));

test("a failed send in the pinned column keeps its prompt id through a close and reopen", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    await press("j");
    assert.equal(cursorKey(container), "s1");
    fake.failNext.add(sayPath);
    await press("|");
    assert.ok(pinnedColumn(container), "the pinned column is shown");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await typeInto(pinnedComposer(container), "keep going");
    await pressIn(pinnedComposer(container), "Enter");
    await until(t.signal, () => fake.callsTo(sayPath).length === 1, "the failed say");
    await until(t.signal, () => pinnedColumn(container)?.querySelector('.dock .inote') !== null, "the failed send's line");
    await act(() => {
      pinnedComposer(container).blur();
    });
    await press("|");
    await press("|");
    assert.equal(pinnedComposer(container).value, "keep going", "the draft survived the close");
    await pressIn(pinnedComposer(container), "Enter");
    await until(t.signal, () => fake.callsTo(sayPath).length === 2, "the second say");
    const [first, second] = fake.callsTo(sayPath);
    assert.equal(second.body.text, "keep going");
    assert.equal(second.body.promptId, first.body.promptId, "the retry reused the prompt id");
  }));

const askRow = row({
  key: "a",
  label: "api",
  state: "waiting",
  question: JSON.stringify({
    questions: [
      {
        question: "Which fruit?",
        options: [
          { label: "Apple", description: "red" },
          { label: "Pear", description: "green" },
        ],
      },
    ],
  }),
  questionHash: "qh-2",
  changed: 300,
  seen: 100,
  unseen: true,
});

test("a waiting pane row shows its question's text, never its raw JSON, and a button click answers it", (t) =>
  withApp(t.signal, { rows: [askRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
    assert.equal(rowElement(container, "a").querySelector(".l2")?.textContent, "Which fruit?");
    await until(t.signal, () => container.querySelector(".question-card") !== null, "the question card");
    const reader = container.querySelector(".reader")?.textContent ?? "";
    assert.ok(reader.includes("Which fruit?"));
    for (const text of [container.querySelector(".l2")?.textContent ?? "", reader]) {
      assert.equal(text.includes("{"), false, "no raw JSON");
      assert.equal(text.includes('"questions"'), false, "no raw JSON key");
    }
    const options = container.querySelectorAll<HTMLButtonElement>(".question-card button.option");
    assert.equal(options.length, 2);
    assert.ok(options[1].textContent?.includes("Pear"));
    await act(() => {
      options[1].click();
    });
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "a", keys: ["2"], expectQuestion: "qh-2" });
  }));

test("a session row's reader draws the question its stream holds, and a button click answers by request id", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(t.signal, () => container.querySelector(".question-card") !== null, "the question card");
    assert.ok(readerText(container).includes("Pick one"));
    const options = container.querySelectorAll<HTMLButtonElement>(".question-card button.option");
    assert.equal(options.length, 2);
    await act(() => {
      options[0].click();
    });
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "Yes" });
  }));

test("a click on the compaction rule shows its summary, and a second click hides it, as o does", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "compaction", text: "the summary text", trigger: "auto", tokens: "611000" }],
    });
    await until(t.signal, () => container.querySelector(".reader .compaction button.rule") !== null, "the compaction rule");
    const rule = (): HTMLButtonElement => {
      const button = container.querySelector<HTMLButtonElement>(".reader .compaction button.rule");
      assert.ok(button, "the rule is a button");
      return button;
    };
    assert.equal(readerText(container).includes("the summary text"), false, "the summary is hidden at first");
    await act(() => {
      rule().click();
    });
    assert.equal(container.querySelector(".summary")?.textContent, "the summary text");
    await act(() => {
      rule().click();
    });
    assert.equal(container.querySelector(".summary") === null, true);
  }));

function clickEvent(): Event {
  return new Event("click", { bubbles: true, cancelable: true });
}

test("a click on a row moves the cursor to it, and the reader streams that row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const [first] = fake.callsTo(paneWatchPath);
    await act(() => {
      rowElement(container, "c").dispatchEvent(clickEvent());
    });
    assert.equal(cursorKey(container), "c");
    assert.equal(first.signal.aborted, true, "the stream for a was aborted");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the pane stream for c");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "a click alone writes no seen");
  }));

test("a double click on a row writes seen for that row, as Enter does for the cursor's row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    const target = rowElement(container, "c");
    await act(() => {
      target.dispatchEvent(clickEvent());
      target.dispatchEvent(clickEvent());
      target.dispatchEvent(new Event("dblclick", { bubbles: true, cancelable: true }));
    });
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "c" });
  }));

test("InboxTree: the list draws no view tabs, and v opens the archived view", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(container.querySelector(".views"), null, "the list draws no view tabs");
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "inbox");
    await press("v");
    assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "archived");
  }));

const linkedRows = fixture().map((item) =>
  item.key === "c" ? { ...item, label: "see https://example.com/notes", summary: "see https://example.com/notes" } : item,
);

test("a click or double click on a link in a row opens the link and moves nothing else", (t) =>
  withApp(t.signal, { rows: linkedRows }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const link = rowElement(container, "c").querySelector("a");
    assert.ok(link, "row c shows its link");
    const click = clickEvent();
    await act(() => {
      link.dispatchEvent(click);
    });
    assert.equal(click.defaultPrevented, false, "the link keeps its default action");
    await act(() => {
      link.dispatchEvent(clickEvent());
      link.dispatchEvent(new Event("dblclick", { bubbles: true, cancelable: true }));
    });
    await settle();
    assert.equal(cursorKey(container), "a");
    assert.equal(fake.callsTo(paneWatchPath).length, 1, "no stream for c was opened");
    assert.equal(fake.callsTo(markPanePath).length, 0, "no seen was written");
  }));

test("a click on a link in the reader opens the link and moves the cursor nowhere", (t) =>
  withApp(t.signal, { rows: [paneRow, sessionRow] }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "read https://example.com/doc" }],
    });
    await until(t.signal, () => container.querySelector(".reader a") !== null, "the reader's link");
    const link = container.querySelector(".reader a");
    assert.ok(link);
    const click = clickEvent();
    await act(() => {
      link.dispatchEvent(click);
    });
    assert.equal(click.defaultPrevented, false, "the link keeps its default action");
    assert.equal(cursorKey(container), "a");
  }));

const widgetsRepo = "widgets-5d0c1a7";
const gadgetsRepo = "gadgets-1a2b3c4";

// makeEpic is a synthetic epic whose id is its repository and slug joined, as the record names it.
// makeEpic is a live epic that steers the fixture's first conversation, a, unless init names another.
function makeEpic(repository: string, slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, {
    id: `${repository}/${slug}`,
    repository,
    slug,
    outcome: "",
    live: true,
    steering: "sess-a",
    ...init,
  });
}

function makeStep(id: string, init: MessageInitShape<typeof EpicStepSchema> = {}): EpicStep {
  return create(EpicStepSchema, { id, task: id, agent: "builder", doing: "", held: false, ...init });
}

// detailOf is the Get response for an epic whose tasks carry the given statuses, one task per status, and its steps and
// slices.
function detailOf(
  epic: Epic,
  statuses: string[],
  steps: EpicStep[] = [],
  slices: MessageInitShape<typeof EpicSliceSchema>[] = [],
): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    tasks: statuses.map((status, index) => ({ effort: 1, slug: `task-${index}`, subject: `Subject ${index}`, status })),
    steps,
    slices,
  });
}

function epicKey(epic: Epic): string {
  return `epic:${epic.id}`;
}

// endedAt is when the ended epics released, and endedNow the clock they read at: 12 minutes later, unix seconds.
const endedAt = 99280n;
const endedNow = 100000;

// endedEpic is an orreryd epic that is not live, released at endedAt and last seen at seen.
function endedEpic(slug: string, seen = 0n): Epic {
  return makeEpic(widgetsRepo, slug, { source: "orreryd", live: false, outcome: "released", phaseAt: endedAt, seen });
}

// endedLine finds the line that opens the ended conversations of a project's card, when a card draws one.
function endedLine(container: HTMLElement): HTMLElement | undefined {
  return [...container.querySelectorAll<HTMLElement>(".dir .fold-line")].find((line) =>
    (line.textContent ?? "").endsWith(" ended"),
  );
}

// oneEpic is a live epic with two running steps, and no detail.
function oneEpic(): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1"), makeStep("s2")] });
  return epicsView([epic], new Map());
}

// checkedEpic is a live epic with no detail whose first running step carries checks of rounds 1 and 2.
function checkedEpic(): EpicsView {
  const checks = [
    { round: 1, name: "lint", ok: false },
    { round: 1, name: "unit", ok: true },
    { round: 2, name: "e2e", ok: true },
  ];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1", { checks }), makeStep("s2")] });
  return epicsView([epic], new Map());
}

function readerHidden(container: HTMLElement): boolean {
  const reader = container.querySelector('section[aria-label="reader"]');
  assert.ok(reader, "the reader is rendered");
  return reader.hasAttribute("hidden");
}

// treeRows are the fixture rows, with the conversation ids the tree tests' epics and contracts name: a steers the live
// epic and the contract, and d the ended one.
function treeRows(): Row[] {
  return fixture().map((item) => {
    if (item.key === "a") {
      return { ...item, archiveId: "sess-a" };
    }
    if (item.key === "d") {
      return { ...item, archiveId: "sess-d", project: "/work/api" };
    }
    return item;
  });
}

const treeHost = create(HostStatusResponseSchema, { host: "box" });
const firstEpic = makeEpic(widgetsRepo, "first", {
  steering: "sess-a",
  path: "/work/api",
  changed: 100n,
  runningSteps: [makeStep("s1")],
});
const oldEpic = makeEpic(widgetsRepo, "old", {
  source: "orreryd",
  live: false,
  outcome: "released",
  phaseAt: endedAt,
  steering: "sess-d",
  path: "/work/api",
});
const tideAlerts = `${widgetsRepo}/tide-alerts`;

// proposedTideAlerts is the contract a conversation proposed, which steers no listed epic of its id, changed at changed.
function proposedTideAlerts(conversation: string, changed = 0n): Contract {
  return create(ContractSchema, {
    id: tideAlerts,
    repository: widgetsRepo,
    title: "Tide alerts for the harbour office",
    care: "serious",
    state: "proposed",
    version: 2n,
    openDecisions: 0n,
    conversation,
    changed,
  });
}

// dirCard is the project card the name names.
function dirCard(container: HTMLElement, name: string): HTMLElement {
  const card = container.querySelector<HTMLElement>(`section.dir[aria-label="${name}"]`);
  assert.ok(card, `the card ${name} is drawn`);
  return card;
}

// drawnKeys lists the key of each drawn conversation, epic, contract and step under root, in the order the page draws
// them. A stand-in's line has no key of its own and is left out.
function drawnKeys(root: ParentNode): string[] {
  return [...root.querySelectorAll("[data-key]")]
    .filter((element) => !element.classList.contains("standin"))
    .map((element) => element.getAttribute("data-key") ?? "");
}

// chipNamed is the filter chip whose label reads label, or undefined when the head draws none.
function chipNamed(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return [...container.querySelectorAll<HTMLButtonElement>(".inbox-attn button")].find((chip) =>
    (chip.textContent ?? "").includes(label),
  );
}

// typeSearch sets the search field's text as typing does.
async function typeSearch(container: HTMLElement, text: string): Promise<void> {
  const input = container.querySelector<HTMLInputElement>(".inbox-search input");
  assert.ok(input, "the search field is open");
  await act(() => {
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

test("InboxTree: each project is a card named by its directory, holding its conversations in rank order", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost }, async (container) => {
    const cards = [...container.querySelectorAll("section.dir")].map((card) => card.getAttribute("aria-label"));
    assert.deepEqual(cards, ["api", "docs", "web"]);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), ["a", "d"]);
    assert.deepEqual(drawnKeys(dirCard(container, "docs")), ["c"]);
    assert.deepEqual(drawnKeys(dirCard(container, "web")), ["b"]);
    assert.equal(dirCard(container, "api").querySelector(".path")?.textContent, "/work/api");
    assert.equal(dirCard(container, "docs").querySelector(".chip")?.textContent, "laptop", "a project on another host names it");
    assert.equal(dirCard(container, "api").querySelector(".chip") === null, true, "a project on the serving host names no host");
  }));

test("InboxTree: an epic and a proposed contract hang under the conversation that steers them, each drawn once", (t) =>
  withApp(
    t.signal,
    { rows: treeRows(), host: treeHost, epics: epicsView([firstEpic], new Map()), contracts: [proposedTideAlerts("sess-a")] },
    async (container) => {
      const steers = dirCard(container, "api").querySelector(".steers");
      assert.ok(steers, "a's epic and contract hang under it");
      assert.deepEqual(drawnKeys(steers), [epicKey(firstEpic), "step:s1", `contract:${tideAlerts}`]);
      assert.equal(container.querySelectorAll(`[data-key="${epicKey(firstEpic)}"]`).length, 1, "the epic is drawn once");
      assert.equal(container.querySelectorAll(`[data-key="contract:${tideAlerts}"]`).length, 1, "the contract is drawn once");
    },
  ));

test("InboxTree: folding a project hides its conversations and its head says how many wait on you", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost }, async (container) => {
    await clickElement(dirCard(container, "api").querySelector(".fold") ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), [], "the folded card draws no conversation");
    assert.equal(dirCard(container, "api").querySelector(".is-attention")?.textContent, "1 waiting");
    await clickElement(dirCard(container, "api").querySelector(".fold") ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), ["a", "d"], "unfolded, the card draws its conversations again");
  }));

// partlyNewRows are the tree rows with a's waiting question seen, and an unseen working conversation e added to api. Under
// the new chip api keeps only e, while a still waits in api.
function partlyNewRows(): Row[] {
  return [
    ...treeRows().map((item) => (item.key === "a" ? { ...item, unseen: false, seen: item.changed } : item)),
    row({ key: "e", label: "fresh", project: "/work/api", state: "working", summary: "fresh", changed: 400, seen: 100, unseen: true }),
  ];
}

test("InboxTree: a folded project under the new chip says what waits in the whole project", (t) =>
  withApp(t.signal, { rows: partlyNewRows(), host: treeHost }, async (container) => {
    await clickElement(chipNamed(container, "new") ?? container);
    await clickElement(dirCard(container, "api").querySelector(".fold") ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), [], "the folded card draws no conversation");
    assert.equal(dirCard(container, "api").querySelector(".is-attention")?.textContent, "1 waiting", "the new chip narrowed the folded count");
  }));

test("InboxTree: a folded project under a search says what waits in the whole project, not what the search keeps", (t) =>
  withApp(t.signal, { rows: partlyNewRows(), host: treeHost }, async (container) => {
    await clickElement(container.querySelector<HTMLElement>('button[aria-label="Search sessions and epics"]') ?? container);
    await typeSearch(container, "fresh");
    await clickElement(dirCard(container, "api").querySelector(".fold") ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), [], "the folded card draws no conversation");
    assert.equal(dirCard(container, "api").querySelector(".is-attention")?.textContent, "1 waiting", "the search narrowed the folded count");
  }));

test("InboxTree: a project's ended line opens to the conversations that ended, with their epics", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost, epics: epicsView([oldEpic], new Map()) }, async (container) => {
    assert.deepEqual(drawnKeys(dirCard(container, "api")), ["a"], "the ended conversation is behind the ended line");
    const line = dirCard(container, "api").querySelector<HTMLElement>(".fold-line");
    assert.equal(line?.textContent, "1 ended");
    await clickElement(line ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "api")), ["a", "d", epicKey(oldEpic)]);
  }));

test("InboxTree: the chips count what waits and what is new, narrow the tree, and turn off when their count reaches zero", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost }, async (container, fake) => {
    const waiting = chipNamed(container, "waits on you");
    const fresh = chipNamed(container, "new");
    assert.ok(waiting && fresh, "both chips draw while they count");
    await clickElement(waiting);
    assert.equal(chipNamed(container, "waits on you")?.getAttribute("aria-pressed"), "true");
    assert.deepEqual([...container.querySelectorAll("section.dir")].map((card) => card.getAttribute("aria-label")), ["api"]);
    assert.deepEqual(drawnKeys(container), ["a"], "waiting narrows to the conversation that waits");
    await clickElement(waiting);
    await clickElement(chipNamed(container, "new") ?? container);
    assert.deepEqual([...container.querySelectorAll("section.dir")].map((card) => card.getAttribute("aria-label")), ["api", "docs"]);
    assert.deepEqual(drawnKeys(container), ["a", "c"], "new narrows to the unseen conversations");
    await mount(container, { rows: treeRows().map((row) => ({ ...row, state: "done", unseen: false, seen: row.changed })), now, clients: fake.clients, seen: fake.seen, host: treeHost });
    assert.equal(chipNamed(container, "waits on you") === undefined, true, "the chip with no count is not drawn");
    assert.equal(chipNamed(container, "new") === undefined, true, "the chip with no count is not drawn");
    assert.deepEqual([...container.querySelectorAll("section.dir")].map((card) => card.getAttribute("aria-label")), ["api", "web", "docs"], "the narrowing turned off");
  }));

test("InboxTree: search from the head's control or from /, matching words, closes on esc, and says when nothing matches", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost }, async (container) => {
    await clickElement(container.querySelector<HTMLElement>('button[aria-label="Search sessions and epics"]') ?? container);
    await typeSearch(container, "DOCS");
    assert.deepEqual([...container.querySelectorAll("section.dir")].map((card) => card.getAttribute("aria-label")), ["docs"]);
    assert.deepEqual(drawnKeys(container), ["c"], "a matching row keeps its project");
    const input = container.querySelector<HTMLInputElement>(".inbox-search input") ?? container;
    await pressIn(input, "Escape");
    assert.equal(container.querySelector(".inbox-search") === null, true, "esc closes the search");
    assert.equal(container.querySelectorAll("section.dir").length, 3, "esc clears it, so every project is drawn");
    await press("/");
    assert.ok(container.querySelector(".inbox-search input"), "/ opens the search");
    await typeSearch(container, "zzz");
    assert.equal(container.querySelectorAll("section.dir").length, 0);
    assert.match(container.querySelector(".blank")?.textContent ?? "", /Nothing matches/);
  }));

test("InboxTree: Archived opens from v and from the foot, and its foot returns to the inbox", (t) =>
  withApp(t.signal, { rows: [...treeRows(), row({ key: "x", label: "old chat", project: "/work/api", state: "done", archived: true, archiveKind: "session", archiveId: "sess-x", changed: 5, seen: 5 })], host: treeHost }, async (container) => {
    assert.deepEqual(drawnKeys(container), ["a", "d", "c", "b"], "the inbox draws no archived conversation");
    await press("v");
    assert.equal(container.querySelector(".inbox-attn .name")?.textContent, "Archived");
    assert.deepEqual(drawnKeys(container), ["x"], "Archived draws the archived conversations");
    await clickElement(container.querySelector<HTMLElement>(".inbox-foot button") ?? container);
    assert.deepEqual(drawnKeys(container), ["a", "d", "c", "b"], "Back to the inbox draws the inbox");
  }));

test("InboxTree: the cursor walks the drawn conversations, epics, contracts and steps in tree order", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost, epics: epicsView([firstEpic], new Map()), contracts: [proposedTideAlerts("sess-a")] }, async (container) => {
    const drawn = drawnKeys(container);
    assert.deepEqual(drawn, ["a", epicKey(firstEpic), "step:s1", `contract:${tideAlerts}`, "d", "c", "b"]);
    assert.equal(cursorKeyOf(container), "a");
    for (const key of drawn.slice(1)) {
      await press("j");
      assert.equal(cursorKeyOf(container), key);
    }
    await press("j");
    assert.equal(cursorKeyOf(container), "b", "j clamps at the last drawn entry");
  }));

test("InboxTree: with a project's ended line open the cursor walks its ended conversations and their epics in tree order", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost, epics: epicsView([firstEpic, oldEpic], new Map()) }, async (container) => {
    const line = [...dirCard(container, "api").querySelectorAll<HTMLElement>(".fold-line")].find(
      (item) => item.textContent === "1 ended",
    );
    assert.ok(line, "the api card draws its ended line");
    await clickElement(line);
    const drawn = drawnKeys(container);
    assert.deepEqual(drawn, ["a", epicKey(firstEpic), "step:s1", "d", epicKey(oldEpic), "c", "b"]);
    assert.equal(cursorKeyOf(container), "a");
    for (const key of drawn.slice(1)) {
      await press("j");
      assert.equal(cursorKeyOf(container), key);
    }
  }));

test("InboxTree: the inbox draws no Needs you, Epics or Just ended section and no need entry", (t) =>
  withApp(t.signal, { rows: treeRows(), host: treeHost, epics: epicsView([firstEpic, oldEpic], new Map()) }, async (container) => {
    assert.equal(container.querySelector(".b-sec") === null, true, "no section head is drawn");
    assert.equal(container.querySelector(".need") === null, true, "no need entry is drawn");
    assert.equal(container.textContent?.includes("Needs you"), false);
    assert.equal(container.textContent?.includes("Just ended"), false);
  }));

// waitingTree is the tree fixture with a's epic asking a question, and a contract the conversation b proposed, changed
// after b's own row. a and the epic wait; b does not, so the contract counts.
function waitingTree(): { rows: Row[]; epics: EpicsView; contract: Contract } {
  const rows = treeRows().map((item) => (item.key === "b" ? { ...item, archiveId: "sess-b" } : item));
  const asking = makeEpic(widgetsRepo, "asking", { changed: 100n });
  const detail = create(EpicsGetResponseSchema, { epic: asking, questions: [{ id: "Q1", text: "Which way?", asked: 880n }] });
  return { rows, epics: epicsView([asking], new Map([[asking.id, detail]])), contract: proposedTideAlerts("sess-b", 150n) };
}

const askingKey = `epic:${widgetsRepo}/asking`;
const tideKey = `contract:${tideAlerts}`;

test("InboxTree: w walks the waiting epic, contract and conversation once each, oldest first, then wraps, and from an entry that counts nothing goes to the first", (t) => {
  const { rows, epics, contract } = waitingTree();
  return withApp(t.signal, { rows, host: treeHost, epics, contracts: [contract] }, async (container) => {
    assert.equal(cursorKeyOf(container), "a");
    await press("w");
    assert.equal(cursorKeyOf(container), askingKey, "the epic asked at 100 comes first");
    await press("w");
    assert.equal(cursorKeyOf(container), tideKey, "then the contract, changed at 150");
    await press("w");
    assert.equal(cursorKeyOf(container), "a", "then the conversation a, changed at 300");
    await press("w");
    assert.equal(cursorKeyOf(container), askingKey, "w wraps to the epic");
    await press("j");
    assert.equal(cursorKeyOf(container), "d", "j rests on d, which counts nothing");
    await press("w");
    assert.equal(cursorKeyOf(container), askingKey, "w from d goes to the first");
  });
});

test("InboxTree: w reveals a counted contract that sits in a folded project", (t) => {
  const { rows, epics, contract } = waitingTree();
  return withApp(t.signal, { rows, host: treeHost, epics, contracts: [contract] }, async (container) => {
    await clickElement(dirCard(container, "web").querySelector(".fold") ?? container);
    assert.deepEqual(drawnKeys(dirCard(container, "web")), [], "the web card is folded");
    await press("w");
    await press("w");
    assert.equal(cursorKeyOf(container), tideKey, "w goes to the contract under b");
    assert.ok(drawnKeys(dirCard(container, "web")).includes(tideKey), "the web card is unfolded to show the contract");
  });
});

// cursorKeyOf is the key of the drawn entry the cursor rests on.
function cursorKeyOf(container: HTMLElement): string | null {
  return container.querySelector("[data-key].cur")?.getAttribute("data-key") ?? null;
}

test("TitleParts: a waiting pane with no question says in its second line that it waits on you, and draws its label once and no directory", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({
          key: "w",
          label: "build green",
          project: "/work/web",
          state: "waiting",
          summary: "build green",
          changed: 300,
          seen: 300,
        }),
      ],
    },
    (container) => {
      const element = rowElement(container, "w");
      assert.equal(element.querySelector(".l2")?.textContent, "waits on you");
      assertNone(element.querySelector(".why"), "no second summary line");
      assertNone(element.querySelector(".title-dir"), "the row draws no directory");
      assert.equal(element.textContent?.split("build green").length, 2, "the label is drawn once");
    },
  ));

test("TitleParts: an inbox row draws its label as its title, and no directory, session id, pane id or hash", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({
          key: "p",
          id: "%3",
          kind: "pane",
          label: "tidying imports",
          project: "/work/api",
          state: "waiting",
          question: "Ship it?",
          questionHash: "hash-of-question",
          summary: "tidying imports",
        }),
        row({
          key: "s",
          id: "0123456789abcdef",
          kind: "session",
          label: "Billing rounding plan",
          project: "/work/app",
          state: "working",
          summary: "reconciling invoices",
        }),
      ],
    },
    (container) => {
      const pane = rowElement(container, "p");
      assert.equal(pane.querySelector(".title")?.textContent, "tidying imports");
      assertNone(pane.querySelector(".title-dir"), "the pane draws no directory");
      const session = rowElement(container, "s");
      assert.equal(session.querySelector(".title")?.textContent, "Billing rounding plan");
      assertNone(session.querySelector(".title-dir"), "the session draws no directory");
      assert.equal(session.querySelector(".l2")?.textContent, "reconciling invoices", "a session keeps its summary as its second line");
      for (const [element, hidden] of [
        [pane, ["%3", "hash-of-question"]],
        [session, ["0123456789abcdef"]],
      ] as const) {
        for (const text of hidden) {
          assert.equal(element.textContent?.includes(text), false, `the row draws no ${text}`);
        }
      }
    },
  ));

test("TitleParts: a pane with no label and a session with an empty headline draw no title and no directory", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({ key: "p", kind: "pane", project: "/work/api", label: "", summary: "", state: "working" }),
        row({ key: "s", kind: "session", project: "/work/app", label: "", state: "done" }),
      ],
    },
    (container) => {
      assertNone(rowElement(container, "p").querySelector(".title-dir"), "the pane draws no directory");
      assertNone(rowElement(container, "s").querySelector(".title-dir"), "the session draws no directory");
      assert.equal(rowElement(container, "p").querySelector(".title")?.textContent, "");
      assert.equal(rowElement(container, "s").querySelector(".title")?.textContent, "");
      assertNone(container.querySelector(".title-line"), "no row draws a line");
    },
  ));

test("TitleParts: a Sessions.List row with no label shows the word session as its title, with no directory", (t) =>
  withApp(
    t.signal,
    { rows: [row({ key: "n", kind: "session", project: "", label: "session", state: "done" })] },
    (container) => {
      const element = rowElement(container, "n");
      assert.equal(element.querySelector(".title")?.textContent, "session");
      assertNone(element.querySelector(".title-dir"), "a Sessions.List row has no directory");
    },
  ));

test("TitleParts: in the inbox no row draws a directory, and no display: none rule in style.css hides .title-dir", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({ key: "p", kind: "pane", project: "/work/api", label: "building", summary: "building", state: "working" }),
        row({ key: "s", kind: "session", project: "/work/app", label: "Billing rounding plan", state: "working" }),
      ],
    },
    async (container) => {
      assert.equal(container.querySelector(".inbox-rows")?.getAttribute("data-list"), "inbox");
      assertNone(rowElement(container, "p").querySelector(".title-dir"), "the pane draws no directory");
      assertNone(rowElement(container, "s").querySelector(".title-dir"), "the session draws no directory");
      const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
      const hiding = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
        .filter(([, selectors, declarations]) => /display\s*:\s*none/.test(declarations) && selectors.includes("title-dir"))
        .map(([, selectors]) => selectors.trim());
      assert.deepEqual(hiding, [], "no display: none rule targets .title-dir");
    },
  ));

test("an epic with an open question draws its blocker in the list, with the time it has waited", (t) => {
  const epic = makeEpic(widgetsRepo, "asking");
  const detail = create(EpicsGetResponseSchema, { epic, questions: [{ id: "Q1", text: "Which way?", asked: 880n }] });
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withApp(t.signal, { epics }, (container) => {
    assert.equal(rowElement(container, epicKey(epic)).querySelector(".l3")?.textContent, "waits on your answer · 2 min");
  });
});

test("an epic shows four running steps with the held one marked, then its more and tail lines alone", (t) => {
  const steps = ["one", "two", "three", "four", "five"].map((name) => makeStep(name, { held: name === "two" }));
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: steps, endedSteps: 3 });
  const epics = epicsView([busy], new Map());
  return withApp(t.signal, { epics }, (container) => {
    const steers = container.querySelector(".steers");
    assert.ok(steers, "the epic hangs under the conversation it steers");
    const entries = [...steers.querySelectorAll(".row.step")];
    assert.deepEqual(
      entries.map((entry) => entry.querySelector(".label")?.textContent),
      ["one", "two", "three", "four"],
    );
    assert.deepEqual(
      entries.map((entry) => entry.classList.contains("held")),
      [false, true, false, false],
    );
    assert.deepEqual(
      [...steers.querySelectorAll(".fold-line")].map((line) => line.textContent),
      ["1 more · 3 steps ended"],
    );
  });
});

test("an ended epic opens from its project's ended line, and its page draws the epic its Epics.Get answers", (t) => {
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const released = makeEpic(gadgetsRepo, "shipped", { live: false, outcome: "released" });
  // The poll loaded the live epic's detail, so only the released epic is asked for.
  const epics = epicsView([busy, released], new Map([[busy.id, detailOf(busy, [])]]));
  return withApp(t.signal, { rows: [], epics }, async (container, fake) => {
    fake.replies.set(getEpicPath, [
      {
        epic: { id: released.id, repository: gadgetsRepo, slug: "shipped", outcome: "released" },
      },
    ]);
    assert.ok(endedLine(container), "the released epic sits behind its project's ended line");
    assertNone(container.querySelector(`[data-key="${epicKey(released)}"]`), "the line starts closed");

    await act(() => {
      endedLine(container)?.dispatchEvent(clickEvent());
    });
    assert.equal(
      rowElement(container, epicKey(released)).querySelector(".title")?.textContent,
      "shipped",
      "opened, the line shows the released epic",
    );

    await act(() => {
      rowElement(container, epicKey(released)).dispatchEvent(clickEvent());
    });
    await until(
      t.signal,
      () => epicPageDrawn(container) && pageSlug(container) === "shipped",
      "the page of the epic the answer holds",
    );
    assert.equal(fake.callsTo(getEpicPath).length, 1, "the epic is asked for once");
    assert.equal(fake.callsTo(getEpicPath)[0].body.id, released.id);

    await act(() => {
      rowElement(container, epicKey(busy)).dispatchEvent(clickEvent());
    });
    await act(() => {
      rowElement(container, epicKey(released)).dispatchEvent(clickEvent());
    });
    assert.equal(fake.callsTo(getEpicPath).length, 1, "opening the epic again asks for it no more");
  });
});

test("the ended earlier epics draw in list order once their line opens, and keys reach them while it is open", (t) => {
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const older = makeEpic(gadgetsRepo, "older", { live: false });
  const newer = makeEpic(gadgetsRepo, "newer", { live: false });
  const epics = epicsView([busy, older, newer], new Map());
  return withApp(t.signal, { rows: [], epics }, async (container) => {
    assert.deepEqual(rowKeys(container), [epicKey(busy), "step:s1"], "closed, the ended earlier epics are not drawn");

    await act(() => {
      endedLine(container)?.dispatchEvent(clickEvent());
    });
    assert.deepEqual(
      rowKeys(container),
      [epicKey(busy), "step:s1", epicKey(older), epicKey(newer)],
      "opened, the ended earlier epics draw in list order",
    );
    assert.equal(cursorKey(container), epicKey(busy));
    await pressEach(["j", "j"]);
    assert.equal(cursorKey(container), epicKey(older), "the cursor reaches the first ended earlier epic by keys");
    await press("j");
    assert.equal(cursorKey(container), epicKey(newer), "the cursor reaches the last ended earlier epic by keys");
  });
});

// openEndedLine opens the one ended line the container draws, which holds the ended epics of its project.
async function openEndedLine(container: HTMLElement): Promise<void> {
  const line = endedLine(container);
  assert.ok(line, "an ended line is drawn");
  await clickElement(line);
}

test("an unseen ended epic writes no Epics.Seen without presence", (t) => {
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    assert.equal(cursorKeyOf(container), epicKey(shipped), "the cursor rests on the ended epic");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 0, "no input has come, so the page is not present");
    fake.seen.state.visibilityState = "hidden";
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 0, "a hidden page is not present");
  });
});

test("resting on an unseen ended epic with presence writes Epics.Seen once, after the dwell", (t) => {
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    assert.equal(cursorKeyOf(container), epicKey(shipped), "the cursor rests on the ended epic");
    fake.seen.input.emit("keydown");
    fake.seen.advance(999);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 0, "not yet a second");
    fake.seen.advance(1);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the dwell writes the ending");
    assert.deepEqual(fake.callsTo(seenEpicPath)[0].body, { id: shipped.id });
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the ending is written once");
  });
});

test("resting on an ended epic that reads seen writes no Epics.Seen", (t) => {
  const busy = makeEpic(widgetsRepo, "busy");
  const shipped = endedEpic("shipped", endedAt + 60n);
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([busy, shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    await press("j");
    assert.equal(cursorKeyOf(container), epicKey(shipped), "the cursor rests on the seen epic");
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 0, "a seen epic is not written");
  });
});

test("a landed Epics.Seen reads the ending as seen in this page, and its ending is written once", (t) => {
  const busy = makeEpic(widgetsRepo, "busy");
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([busy, shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    await press("j");
    assert.equal(cursorKeyOf(container), epicKey(shipped));
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the dwell writes the ending");
    assert.deepEqual(fake.callsTo(seenEpicPath)[0].body, { id: shipped.id });
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the ending is not written again while the cursor rests");

    await press("k");
    await press("j");
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the landed write reads the ending as seen, so a later rest writes none");

    await mount(container, {
      rows: [],
      now: endedNow,
      clients: fake.clients,
      seen: fake.seen,
      epics: epicsView([busy, endedEpic("shipped")], new Map()),
    });
    await press("k");
    await press("j");
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "a poll that still reads the ending unseen keeps the write");
  });
});

test("a failed Epics.Seen shows could not mark seen and leaves the epic under the ended line, unseen", (t) => {
  const busy = makeEpic(widgetsRepo, "busy");
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([busy, shipped], new Map()) }, async (container, fake) => {
    fake.failNext.add(seenEpicPath);
    await openEndedLine(container);
    await press("j");
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the write was sent once");
    assert.deepEqual(alertTexts(container), [seenFailedText], "the failed write shows the seen line");
    await press("k");
    assert.deepEqual(drawnKeys(container), [epicKey(busy), epicKey(shipped)], "the epic stays under the ended line");
  });
});

test("a later ending of a seen epic reads unseen again and is written after its own dwell", (t) => {
  const busy = makeEpic(widgetsRepo, "busy");
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([busy, shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    await press("j");
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the first ending is written");
    await press("k");
    await settle();

    const later = makeEpic(widgetsRepo, "shipped", {
      source: "orreryd",
      live: false,
      outcome: "released",
      phaseAt: endedAt + 100n,
    });
    await mount(container, {
      rows: [],
      now: endedNow,
      clients: fake.clients,
      seen: fake.seen,
      epics: epicsView([busy, later], new Map()),
    });
    assert.deepEqual(drawnKeys(container), [epicKey(busy), epicKey(later)], "the later ending is drawn under the ended line");

    await press("j");
    assert.equal(cursorKeyOf(container), epicKey(later));
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 2, "the later ending is written after its own dwell");
    assert.deepEqual(fake.callsTo(seenEpicPath)[1].body, { id: later.id });
  });
});

test("an epic under the cursor that another client marks seen stays under the ended line until the cursor moves", (t) => {
  const busy = makeEpic(widgetsRepo, "busy");
  const shipped = endedEpic("shipped");
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([busy, shipped], new Map()) }, async (container, fake) => {
    await openEndedLine(container);
    await clickElement(rowElement(container, epicKey(shipped)));
    assert.equal(cursorKeyOf(container), epicKey(shipped), "the click rests the cursor on the ended epic");

    await mount(container, {
      rows: [],
      now: endedNow,
      clients: fake.clients,
      seen: fake.seen,
      epics: epicsView([busy, endedEpic("shipped", endedAt + 60n)], new Map()),
    });
    assert.equal(cursorKeyOf(container), epicKey(shipped), "the cursor keeps the epic another client marked seen");
    assert.deepEqual(drawnKeys(container), [epicKey(busy), epicKey(shipped)], "the ended line still draws the epic");

    await press("k");
    assert.equal(cursorKeyOf(container), epicKey(busy), "the cursor moves up to the live epic");
  });
});

test("an epic under the cursor that the next view gives released and unseen stays under the ended line while the cursor rests on it", (t) => {
  const live = makeEpic(widgetsRepo, "shipped");
  const busy = makeEpic(widgetsRepo, "busy");
  const older = endedEpic("older", endedAt + 60n);
  return withApp(t.signal, { rows: [], now: endedNow, epics: epicsView([live, busy, older], new Map()) }, async (container, fake) => {
    assert.equal(cursorKeyOf(container), epicKey(live), "the cursor rests on the live epic");
    await openEndedLine(container);
    assert.equal(cursorKeyOf(container), epicKey(live), "opening the ended line keeps the cursor");

    await mount(container, {
      rows: [],
      now: endedNow,
      clients: fake.clients,
      seen: fake.seen,
      epics: epicsView([endedEpic("shipped"), busy, older], new Map()),
    });
    assert.equal(cursorKeyOf(container), epicKey(live), "the cursor stays on the epic as it turns released");
    assert.deepEqual(drawnKeys(container), [epicKey(busy), epicKey(live), epicKey(older)], "the released epic draws under the line");

    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await settle();
    assert.equal(fake.callsTo(seenEpicPath).length, 1, "the unseen ending under the cursor is written after its dwell");
    assert.deepEqual(fake.callsTo(seenEpicPath)[0].body, { id: live.id });
  });
});

test("an epic the poll loaded draws its page from the poll, and Epics.Get is not asked", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    assert.equal(epicPageDrawn(container), true, "the page draws from the poll's detail");
    assert.equal(fake.callsTo(getEpicPath).length, 0, "no Epics.Get is asked for an epic the poll loaded");
  }));

test("an epic whose detail names no state reads its count alone, and epic unavailable without detail", (t) => {
  const known = makeEpic(widgetsRepo, "first");
  const unknown = makeEpic(gadgetsRepo, "second");
  const epics = epicsView([known, unknown], new Map([[known.id, detailOf(known, ["done", "integrated", "pending"])]]));
  return withApp(t.signal, { epics }, (container) => {
    assert.equal(rowElement(container, epicKey(known)).querySelector(".l2")?.textContent, "2 of 3 tasks done");
    assert.equal(rowElement(container, epicKey(unknown)).querySelector(".l2")?.textContent, "epic unavailable");
  });
});

test("a one-line epic draws no strip, and a full epic with no detail draws none", (t) => {
  const quiet = makeEpic(widgetsRepo, "quiet", { outcome: "build" });
  const busy = makeEpic(gadgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const detail = detailOf(quiet, ["done", "pending"], [], [{ task: "task-0", hue: "done" }]);
  const epics = epicsView([quiet, busy], new Map([[quiet.id, detail]]));
  return withApp(t.signal, { epics }, (container) => {
    assert.equal(
      rowElement(container, epicKey(quiet)).querySelector(".strip") === null,
      true,
      "a one-line epic has no strip",
    );
    assert.equal(rowElement(container, epicKey(busy)).querySelector(".strip") === null, true, "no detail, no strip");
  });
});

test("a running step draws its working glyph, its label, its five pips and its doing", (t) => {
  const running = makeStep("s1", { task: "task-0", doing: "writing the test" });
  const slices = [{ task: "task-0", hue: "dispatched", at: 4, redTest: true, working: true, testsPass: true }];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [running] });
  const epics = epicsView([epic], new Map([[epic.id, detailOf(epic, ["pending"], [running], slices)]]));
  return withApp(t.signal, { epics }, (container) => {
    const step = rowElement(container, "step:s1");
    assert.equal(step.className, "row step");
    assert.equal(step.querySelector("svg.glyph")?.getAttribute("data-state"), "working");
    assert.equal(step.querySelector(".label")?.textContent, "task-0");
    assert.equal(step.querySelectorAll(".pips i").length, 5, "five pips");
    assert.equal(step.querySelector(".doing")?.textContent, "writing the test");
  });
});

test("a held step draws the held glyph and the held row, and a running one does not", (t) => {
  const withReason = makeStep("s1", { doing: "writing the test", held: true, reason: "waiting on review" });
  const withoutReason = makeStep("s2", { doing: "writing the test", held: true });
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [withReason, withoutReason] });
  return withApp(t.signal, { epics: epicsView([epic], new Map()) }, (container) => {
    const reasoned = rowElement(container, "step:s1");
    assert.equal(reasoned.className, "row step held");
    assert.equal(reasoned.querySelector("svg.glyph")?.getAttribute("data-state"), "held");
    assert.equal(rowElement(container, "step:s2").querySelector("svg.glyph")?.getAttribute("data-state"), "held");
  });
});

test("j and k reach epic and step entries; the reader is hidden on an epic and shown on a step", (t) => {
  return withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const [stream] = fake.callsTo(paneWatchPath);
    const before = fake.calls.length;
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    assert.equal(readerHidden(container), true);
    assert.equal(stream.signal.aborted, true, "moving onto the epic stops a's stream");
    assert.equal(fake.calls.length, before, "no call is made for an epic entry");
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(readerHidden(container), false, "a step shows the reader");
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(readerHidden(container), false);
    await press("k");
    assert.equal(cursorKey(container), "step:s2");
    await press("k");
    assert.equal(cursorKey(container), "step:s1");
    await press("k");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    await press("k");
    assert.equal(cursorKey(container), "a");
    assert.equal(readerHidden(container), false);
  });
});

test("w stops on the waiting epic, and n skips the epic and step entries", (t) => {
  const asking = makeEpic(widgetsRepo, "asking", { runningSteps: [makeStep("s1"), makeStep("s2")] });
  const detail = create(EpicsGetResponseSchema, { epic: asking, questions: [{ id: "Q1", text: "Which way?", asked: 880n }] });
  return withApp(t.signal, { epics: epicsView([asking], new Map([[asking.id, detail]])) }, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), epicKey(asking));
    await press("w");
    assert.equal(cursorKey(container), "a", "w goes from the epic to the waiting row a");
    await press("n");
    assert.equal(cursorKey(container), "c", "n goes past the epic and its steps to the next unseen row");
    await press("n");
    assert.equal(cursorKey(container), "a", "n wraps to the unseen row a");
    await press("w");
    assert.equal(cursorKey(container), epicKey(asking), "w wraps from a to the waiting epic, which it stops on");
  });
});

test("x, Enter, e, i, 1, 2, 3 and | on an epic entry call nothing and pin nothing", (t) => {
  return withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const before = fake.calls.length;
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    await pressEach(["x", "Enter", "e", "i", "1", "2", "3", "|"]);
    await until(t.signal, () => fake.callsTo(historyPath).length === 1, "the History read the page asks for");
    await settle();
    assert.deepEqual(
      fake.calls.slice(before).map((call) => call.path),
      [getEpicPath, historyPath],
      "the only calls are the Epics.Get and Epics.History the page asks for, and no key reaches the server",
    );
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), false, "| pinned nothing");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
  });
});

test("resting on an epic or step writes no seen, even with input on the page", (t) => {
  return withApp(t.signal, { epics: oneEpic() }, async (_container, fake) => {
    fake.seen.input.emit("keydown");
    await press("j");
    fake.seen.advance(5000);
    await press("j");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "a's dwell was cancelled when the cursor left it");
    assert.equal(fake.callsTo(markSessionPath).length, 0);
  });
});

test("a step's doing text draws as text, never as markup", (t) => {
  const text = "<img src=x onerror=alert(1)> and <b>bold</b>";
  const running = makeStep("s1", { doing: text });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [running] })], new Map());
  return withApp(t.signal, { epics }, (container) => {
    const step = rowElement(container, "step:s1");
    assert.equal(step.querySelector("img, b") === null, true);
    assert.equal(step.querySelector(".doing")?.textContent, text);
  });
});

test("a click on a step moves the cursor to it, and writes no seen", (t) => {
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    await act(() => {
      rowElement(container, "step:s1").dispatchEvent(clickEvent());
    });
    assert.equal(cursorKey(container), "step:s1");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "a click writes no seen");
  });
});

// The widgets epic named first, whose id the page's commands and history are sent under.
const widgetsFirst = `${widgetsRepo}/first`;

// pageEpic is a live epic with one running step, s1, whose detail carries the fields given. changed is the epic's last
// write, which its history is read again for.
// An orreryd one is accepted, since only an orreryd epic draws its history.
function pageEpic(init: MessageInitShape<typeof EpicsGetResponseSchema> = {}, changed = 0n, orreryd = false): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", {
    runningSteps: [makeStep("s1")],
    changed,
    ...(orreryd ? { source: "orreryd", outcome: "accepted" } : {}),
  });
  return epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, ...init })]]));
}

// optionsQuestion is an open question with three choices, the second of which the planner recommends for its why.
const optionsQuestion = {
  questions: [
    {
      id: "Q1",
      text: "Which database?",
      choices: [
        { id: "postgres", does: "Use PostgreSQL for the store." },
        { id: "sqlite", does: "Use SQLite for the store." },
        { id: "memory", does: "Keep the store in memory." },
      ],
      recommendation: { choice: "sqlite", why: "one file is enough here", confidence: "medium" },
    },
  ],
};

// summaryEpic is a live orreryd epic accepted with the summary and contract given, whose summary section and Contract door draw.
function summaryEpic(): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { source: "orreryd", outcome: "accepted" });
  return epicsView(
    [epic],
    new Map([[epic.id, create(EpicsGetResponseSchema, { epic, summary: "Ship the demo", contract: acceptedContract })]]),
  );
}

// acceptedContract is an accepted contract whose title the summary shows.
const acceptedContract = { title: "Show the accepted contract", approved: "approved line", intent: "the intent" };

// epicPageShown is whether the page section is drawn: the cursor rests on an epic entry and the page is not closed.
function epicPageShown(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"]:not([hidden])') !== null;
}

// epicPageDrawn is whether the page section holds the epic page itself, rather than the contract page.
function epicPageDrawn(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] .epic-page') !== null;
}

// epicReadText is the line the epic page's section draws while the epic's detail is not drawn, or null.
function epicReadText(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"] .side-note')?.textContent ?? null;
}

// epicPageButtons lists the labels of the buttons the epic page's section draws outside its title row.
function epicPageButtons(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll('section.page[aria-label="epic"] button')]
    .filter((button) => button.closest(".title-row") === null)
    .map((button) => button.textContent);
}

// epicTitleSlug is the slug the epic page's title row names as the page it is on, or null when the section draws no
// title row.
function epicTitleSlug(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"] .title-row [aria-current="page"]')?.textContent ?? null;
}

// epicReply is the Epics.Get answer for an epic with the id and slug, with no detail but its own record.
function epicReply(id: string, slug: string): object {
  return { epic: { id, repository: widgetsRepo, slug, outcome: "", live: true } };
}

// contractDrawn is whether the page section holds the contract page.
function contractDrawn(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] article.contract') !== null;
}

// pageClass is the class of the page section, which names the move the page made last: drill or leave.
function pageClass(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"]')?.getAttribute("class") ?? null;
}

// pageScroll is the scroll place of the page section, which is the one scroll container of the epic page.
function pageScroll(container: HTMLElement): number {
  const page = container.querySelector<HTMLElement>('section.page[aria-label="epic"]');
  assert.ok(page, "the epic page section is rendered");
  return page.scrollTop;
}

// moveScroll sets the scroll place of the page section, as a scroll by the reader does.
function moveScroll(container: HTMLElement, top: number): void {
  const page = container.querySelector<HTMLElement>('section.page[aria-label="epic"]');
  assert.ok(page, "the epic page section is rendered");
  page.scrollTop = top;
}

// pageSlug is the crumb the title row marks as the current page, which the epic page names by its slug.
function pageSlug(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"] .title-row .crumb.here')?.textContent ?? null;
}

// historyText is the text of the Just happened section, which holds the history the epic page shows.
function historyText(container: HTMLElement): string {
  return container.querySelector(".happen.just")?.textContent ?? "";
}

// clickButton clicks the button in the scope whose text is the label, and lets Preact draw what the click changed.
async function clickButton(container: HTMLElement, scope: string, label: string): Promise<void> {
  const target = [...container.querySelectorAll<HTMLButtonElement>(`${scope} button`)].find(
    (button) => button.textContent === label,
  );
  assert.ok(target !== undefined, `no ${label} button in ${scope}`);
  await act(() => {
    target.dispatchEvent(new Event("click"));
  });
}

// submitIn submits the form the selector finds, and lets Preact draw what the submit changed.
async function submitIn(container: HTMLElement, selector: string): Promise<void> {
  const form = container.querySelector(selector);
  assert.ok(form, `no form matches ${selector}`);
  await act(() => {
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}

test("an epic entry shows the epic page beside the list, and no epic column remains", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsFirst}`);
    assert.equal(epicPageShown(container), true, "the epic page shows on an epic entry");
    assert.equal(epicPageDrawn(container), true, "the page draws the epic page");
    assert.equal(
      container.querySelector('aside.rpanel section.page[aria-label="epic"]') !== null,
      true,
      "the page sits in the panel beside the rail",
    );
    assert.equal(container.querySelector("aside.rail") !== null, true, "the rail is drawn");
    assert.equal(pageSlug(container), "first", "the page names the epic");
  }));

test("an epic page draws no handover, though the epic holds verdicts and ledger findings", (t) =>
  withApp(t.signal, 
    {
      epics: pageEpic({
        reviews: [
          create(EpicReviewVerdictSchema, { reviewer: "reviewer-one", verdict: "holds", reason: "the verdict reason" }),
        ],
        ledger: [
          create(EpicLedgerFindingSchema, {
            id: "reviewer-one-1",
            reviewer: "reviewer-one",
            claim: "the ledger claim",
            result: "holds",
          }),
        ],
      }),
    },
    async (container) => {
      await press("j");
      assert.equal(epicPageDrawn(container), true, "the page draws the epic page");
      assert.equal(
        container.querySelector('section.page[aria-label="epic"] section.handover') === null,
        true,
        "no handover section is drawn",
      );
      assert.equal(container.textContent?.includes("the verdict reason"), false, "the verdict's reason is not drawn");
      assert.equal(container.textContent?.includes("the ledger claim"), false, "the ledger's claim is not drawn");
    },
  ));

test("an epic page with no reviews, ledger findings or releases shows no handover section", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await press("j");
    assert.equal(epicPageDrawn(container), true, "the page draws the epic page");
    assert.equal(
      container.querySelector('section.page[aria-label="epic"] section.handover') === null,
      true,
      "no handover section is drawn",
    );
  }));

test("a step entry shows its reader alone, and no epic page", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(readerHidden(container), false, "the step shows its reader");
    assert.equal(epicPageShown(container), false, "no epic page shows beside a step");
    assert.equal(container.querySelector("aside.rpanel") === null, true, "no panel is drawn beside a step");
  }));

test("a pin splits the panes around a step's reader, and the epic page stays hidden", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await press("g");
    await press("g");
    await press("|");
    await press("Tab", { shiftKey: true });
    await onStepOne(t.signal, container, fake);
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), true, "row a is pinned");
    assert.equal(readerHidden(container), false, "the step's reader shows");
    assert.equal(epicPageShown(container), false, "no epic page shows beside the step");
    await press("k");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    assert.equal(epicPageShown(container), true, "the epic page shows on the epic entry");
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), true, "the pin stays open");
  }));

test("a pane row hides the epic page, and an epic shows it again", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await press("j");
    assert.equal(epicPageShown(container), true, "the page shows on an epic entry");
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(epicPageShown(container), false, "a pane row hides the page");
    await press("k");
    await press("k");
    assert.equal(cursorKey(container), `epic:${widgetsFirst}`);
    assert.equal(epicPageShown(container), true, "the page shows again");
  }));

test("a session row hides the epic page", (t) =>
  withApp(t.signal, { rows: [sessionRow], epics: pageEpic() }, async (container) => {
    assert.equal(cursorKey(container), `epic:${widgetsFirst}`);
    assert.equal(epicPageShown(container), true, "the page shows on an epic entry");
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(epicPageShown(container), false, "a session row hides the page");
  }));

test("EpicCommands: an epic whose read has not answered reads loading the epic, with no button", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    let answer: () => void = () => {};
    fake.holds.set(
      getEpicPath,
      new Promise<void>((resolve) => {
        answer = resolve;
      }),
    );
    await press("j");
    try {
      assert.equal(epicPageDrawn(container), false, "no epic page is drawn before the answer");
      assert.equal(epicReadText(container), "loading the epic…");
      assert.equal(epicTitleSlug(container), "first", "the title row names the listed epic above the line");
      assert.ok(
        container.querySelector('section.page[aria-label="epic"] .title-row .back'),
        "the title row has a back button",
      );
      assert.deepEqual(epicPageButtons(container), [], "no button while the epic loads");
    } finally {
      fake.replies.set(getEpicPath, [epicReply(widgetsFirst, "first")]);
      answer();
    }
    await until(t.signal, () => epicPageDrawn(container), "the page the answer holds");
  }));

test("EpicCommands: an epic the server could not read says so with Try again, which reads it once more, and a step of it shows no page", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    fake.failNext.add(getEpicPath);
    await press("j");
    await until(t.signal, () => epicReadText(container) === "The server could not read this epic.", "the could-not-read line");
    assert.equal(epicPageShown(container), true);
    assert.equal(epicPageDrawn(container), false, "no epic page is drawn without detail");
    assert.equal(epicTitleSlug(container), "first", "the title row names the listed epic above the line");
    assert.deepEqual(epicPageButtons(container), ["Try again"]);
    assert.equal(fake.callsTo(getEpicPath).length, 1, "the failed read is not repeated on its own");
    fake.replies.set(getEpicPath, [epicReply(widgetsFirst, "first")]);
    await clickButton(container, 'section.page[aria-label="epic"]', "Try again");
    await until(t.signal, () => epicPageDrawn(container), "the page the retry answers");
    assert.equal(fake.callsTo(getEpicPath).length, 2, "Try again reads the epic once");
    assert.equal(fake.callsTo(getEpicPath)[1].body.id, widgetsFirst);
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(epicPageShown(container), false, "a step shows no page");
  }));

test("EpicRecord: under 760px the loading epic's title row draws no host strip, as the page's does not", (t) =>
  withApp(
    t.signal,
    { epics: oneEpic(), host: create(HostStatusResponseSchema, { host: "box" }) },
    async (container, fake) => {
      let answer: () => void = () => {};
      fake.holds.set(
        getEpicPath,
        new Promise<void>((resolve) => {
          answer = resolve;
        }),
      );
      await clickRow(container, `epic:${widgetsFirst}`);
      try {
        assert.equal(epicReadText(container), "loading the epic…");
        const row = container.querySelector('section.page[aria-label="epic"] header.title-row');
        assert.ok(row, "the loading page draws its title row");
        assert.equal(row.lastElementChild?.className, "acts", "nothing follows the commands: no strip");
      } finally {
        fake.replies.set(getEpicPath, [epicReply(widgetsFirst, "first")]);
        answer();
      }
      await until(t.signal, () => epicPageDrawn(container), "the page the answer holds");
    },
    412,
  ));

test("EpicRecord: under 760px with the epic page shown, no inbox list and no reader column is drawn", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await clickRow(container, `epic:${widgetsFirst}`);
    assert.equal(epicPageDrawn(container), true, "the epic page is drawn");
    const list = container.querySelector<HTMLElement>(".list");
    assert.ok(list, "the inbox list stays mounted");
    assert.equal(list.hidden, true, "the inbox list is hidden");
    assert.equal(readerHidden(container), true, "the reader column is hidden");
  }, 412));

test("EpicRecord: at 412px a draft survives opening an epic page and coming back", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    fake.replies.set(pullPath, [{ text: "" }]);
    await clickRow(container, "a");
    const box = composer(container);
    await typeInto(box, "half a thought");
    await pressIn(box, "Escape");
    await until(t.signal, () => document.activeElement !== box, "the blur after the typing");
    await press("h");
    await clickRow(container, `epic:${widgetsFirst}`);
    assert.equal(epicPageShown(container), true, "the epic page shows on the epic entry");
    await press("h");
    await clickRow(container, "a");
    assert.equal(cursorKey(container), "a");
    assert.equal(composer(container).value, "half a thought", "the draft survived the epic page");
  }, 412));

test("with a row pinned, the epic page follows the cursor from one epic to another, and a step between them shows its reader", (t) => {
  const first = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1"), makeStep("s2")] });
  const second = makeEpic(gadgetsRepo, "second", { runningSteps: [makeStep("s3")] });
  const epics = epicsView(
    [first, second],
    new Map([
      [first.id, detailOf(first, ["done", "pending"])],
      [second.id, detailOf(second, ["gap"])],
    ]),
  );
  return withApp(t.signal, { epics }, async (container) => {
    await press("|");
    assert.ok(pinnedColumn(container), "row a is pinned");
    await press("Tab", { shiftKey: true });
    await press("j");
    assert.equal(cursorKey(container), epicKey(first));
    assert.equal(pageSlug(container), "first");
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(epicPageShown(container), false, "a step shows no epic page");
    assert.equal(readerHidden(container), false, "a step shows its reader");
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), epicKey(second));
    assert.equal(pageSlug(container), "second");
    await press("j");
    assert.equal(cursorKey(container), "step:s3");
    assert.equal(epicPageShown(container), false);
    assert.ok(pinnedColumn(container), "the pinned column stays open");
  });
});

test("the Contract door opens the contract page in place, and the page moves deeper", (t) =>
  withApp(t.signal, { epics: summaryEpic() }, async (container) => {
    await press("j");
    assert.equal(pageClass(container), "page");
    await clickFirst(container, ".epic-page .door.contract");
    assert.equal(epicPageShown(container), true, "the page stays shown");
    assert.equal(contractDrawn(container), true, "the contract page is drawn");
    assert.equal(epicPageDrawn(container), false, "the epic page is not");
    assert.equal(pageClass(container), "page drill", "opening the contract moves the page deeper");
    assert.equal(pageSlug(container), "contract", "the contract is the current page");
  }));

test("back and the slug crumb restore the epic page's scroll place, and the page moves back with leave", (t) =>
  withApp(t.signal, { epics: summaryEpic() }, async (container) => {
    await press("j");
    moveScroll(container, 120);
    await clickFirst(container, ".epic-page .door.contract");
    assert.equal(pageScroll(container), 0, "the contract opens at its top");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "‹");
    assert.equal(epicPageDrawn(container), true, "back returns to the epic page");
    assert.equal(pageClass(container), "page leave", "back moves the page back");
    assert.equal(pageScroll(container), 120, "back restores the scroll place");
    await clickFirst(container, ".epic-page .door.contract");
    assert.equal(pageScroll(container), 0, "the contract opens at its top again");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "first");
    assert.equal(epicPageDrawn(container), true, "the slug crumb returns to the epic page");
    assert.equal(pageClass(container), "page leave", "the slug crumb moves the page back");
    assert.equal(pageScroll(container), 120, "the slug crumb restores the scroll place");
  }));

test("back closes the epic page until the cursor moves", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container) => {
    await press("j");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "‹");
    assert.equal(epicPageShown(container), false, "back closes the page");
    assert.equal(cursorKey(container), `epic:${widgetsFirst}`, "the cursor stays on the epic");
    await press("j");
    await press("j");
    await press("k");
    await press("k");
    assert.equal(cursorKey(container), `epic:${widgetsFirst}`);
    assert.equal(epicPageShown(container), true, "the page opens again once the cursor has moved");
  }));

// phaseEpic is a live epic whose task T1, Parser, runs its gate step run1, which puts T1 in checking, and whose task
// T2, Logout, is planned. T1 names requirement R1, so R1's proof card opens T1.
function phaseEpic(): EpicsView {
  const run = makeStep("run1", { task: "T1", stage: "gate", doing: "checking the parser" });
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [run] });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    buildTasks: [
      { id: "T1", title: "Parser", status: "dispatched", requirements: ["R1"] },
      { id: "T2", title: "Logout", status: "", requirements: ["R2"] },
    ],
    requirements: [{ id: "R1", text: "The parser parses" }],
    steps: [run],
  });
  return epicsView([epic], new Map([[epic.id, detail]]));
}

const pageTitleRow = 'section.page[aria-label="epic"] .title-row';
const phaseBody = 'section.page[aria-label="epic"] .phase-body';
const checkingCell = 'section.page[aria-label="epic"] path[data-task="T1"][data-phase="checking"]';

// phaseOpenName is the name of the phase the phase output has open, or null when the page holds no phase output.
function phaseOpenName(container: HTMLElement): string | null {
  return container.querySelector('section.page[aria-label="epic"] .phase-row.open .phase-name')?.textContent ?? null;
}

// phaseOutputShown is whether the page section holds a task's phase output.
function phaseOutputShown(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] .phase-output') !== null;
}

// sessionPageShown is whether the page section holds a step's worker session.
function sessionPageShown(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] .worker-session') !== null;
}

// pageReaderText is the text of the reader the page section holds.
function pageReaderText(container: HTMLElement): string {
  return container.querySelector('section.page[aria-label="epic"] .reader')?.textContent ?? "";
}

// clickFirst clicks the first element the selector finds in the container, and lets Preact draw what the click changed.
async function clickFirst(container: HTMLElement, selector: string): Promise<void> {
  const target = container.querySelector(selector);
  assert.ok(target, `no element matches ${selector}`);
  await clickIn(target);
}

// optionCards are the option cards the epic page's question draws, in order, Something else last.
function optionCards(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".epic-page section.now form.answer .opts > .opt")];
}

// selectedCard is the position of the option card that is selected, or -1 when none is.
function selectedCard(container: HTMLElement): number {
  return optionCards(container).findIndex((card) => card.classList.contains("sel"));
}

// clickCard clicks the option card at index, as the owner's pointer does.
async function clickCard(container: HTMLElement, index: number): Promise<void> {
  const card = optionCards(container)[index];
  assert.ok(card, `the question draws card ${index + 1}`);
  await act(() => {
    card.dispatchEvent(new Event("click"));
  });
}

// clickPhaseHead clicks the head of the phase row the phase output names.
async function clickPhaseHead(container: HTMLElement, name: string): Promise<void> {
  const head = [...container.querySelectorAll<HTMLButtonElement>('section.page[aria-label="epic"] .phase-head')].find(
    (button) => button.querySelector(".phase-name")?.textContent === name,
  );
  assert.ok(head, `no ${name} phase head is drawn`);
  await clickIn(head);
}

// openCheckingPhase moves from row a to the phase epic's epic, and clicks T1's checking cell in the orrery.
async function openCheckingPhase(container: HTMLElement): Promise<void> {
  await press("j");
  await clickFirst(container, checkingCell);
}

test("a click on an orrery cell opens its task's phase output with that phase open, and the page moves deeper", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await openCheckingPhase(container);
    assert.equal(phaseOutputShown(container), true, "the task's phase output is drawn");
    assert.equal(phaseOpenName(container), "checking", "the phase the cell names is open");
    assert.equal(pageSlug(container), "T1 · checking");
    assert.equal(pageClass(container), "page drill", "opening the phase output moves the page deeper");
  }));

test("a click on a task row opens the task at the phase its row names", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await press("j");
    await clickFirst(container, 'section.page[aria-label="epic"] .tasks button.tr');
    assert.equal(phaseOutputShown(container), true, "the row opens the phase output");
    assert.equal(phaseOpenName(container), "checking", "the row opens the phase its task is in");
    assert.equal(pageSlug(container), "T1 · checking");
    assert.equal(pageClass(container), "page drill");
  }));

test("a phase head opens its phase in place, and no page moves", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await openCheckingPhase(container);
    assert.equal(pageClass(container), "page drill");
    await clickPhaseHead(container, "implementing");
    assert.equal(phaseOpenName(container), "implementing", "the head's phase is open");
    assert.equal(pageSlug(container), "T1 · implementing", "the page is still the task's phase output");
    assert.equal(pageClass(container), "page drill", "no page moves for a head");
    await clickButton(container, pageTitleRow, "‹");
    assert.equal(epicPageDrawn(container), true, "‹ goes straight to the epic page: the head pushed no page");
  }));

test("Open the session streams the step's worker session as text, and moves the page deeper", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container, fake) => {
    await openCheckingPhase(container);
    await clickButton(container, phaseBody, "Open the session ›");
    assert.equal(sessionPageShown(container), true, "the worker session is drawn");
    assert.equal(pageSlug(container), "session");
    assert.equal(pageClass(container), "page drill", "opening the session moves the page deeper");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    assert.deepEqual(
      fake.callsTo(sessionWatchPath).map((call) => call.body.id),
      ["run1"],
      "the stream follows the step's session",
    );
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the checking reply" }],
    });
    await until(t.signal, () => pageReaderText(container).includes("the checking reply"), "the session reply");
  }));

test("‹ on the session returns to the phase output, and ‹ again to the epic page at its scroll place", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container, fake) => {
    await press("j");
    moveScroll(container, 120);
    await clickFirst(container, checkingCell);
    await clickButton(container, phaseBody, "Open the session ›");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    await clickButton(container, pageTitleRow, "‹");
    assert.equal(phaseOutputShown(container), true, "‹ returns to the phase output");
    assert.equal(sessionPageShown(container), false, "the session is no longer drawn");
    assert.equal(pageClass(container), "page leave", "the phase output comes back with leave");
    assert.equal(fake.callsTo(sessionWatchPath)[0].signal.aborted, true, "leaving the session stops its stream");
    await clickButton(container, pageTitleRow, "‹");
    assert.equal(epicPageDrawn(container), true, "‹ again returns to the epic page");
    assert.equal(pageClass(container), "page leave", "the epic page comes back with leave");
    assert.equal(String(pageScroll(container)), "120", "the epic page's scroll place is restored");
  }));

test("the epic crumb on the phase output returns to the epic page at its scroll place", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await press("j");
    moveScroll(container, 120);
    await clickFirst(container, checkingCell);
    await clickButton(container, pageTitleRow, "first");
    assert.equal(epicPageDrawn(container), true, "the epic crumb returns to the epic page");
    assert.equal(pageClass(container), "page leave", "the epic page comes back with leave");
    assert.equal(String(pageScroll(container)), "120", "the epic page's scroll place is restored");
  }));

test("Hold on the phase output calls Steps.Hold with the running step's id", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container, fake) => {
    await openCheckingPhase(container);
    await clickButton(container, `${pageTitleRow} .acts`, "Hold");
    await until(t.signal, () => fake.callsTo(holdPath).length === 1, "the Hold call");
    assert.deepEqual(fake.callsTo(holdPath).map((call) => call.body.id), ["run1"]);
  }));

test("a nudge in the session calls Steps.Nudge with the step's id and the text", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container, fake) => {
    await openCheckingPhase(container);
    await clickButton(container, phaseBody, "Open the session ›");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const box = container.querySelector<HTMLTextAreaElement>('section.page[aria-label="epic"] .dock-box textarea');
    assert.ok(box, "the session's step box has an input");
    await typeInto(box, "try the other route");
    await clickFirst(container, 'section.page[aria-label="epic"] .dock-box button[aria-label="send"]');
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge");
    assert.deepEqual(fake.callsTo(nudgePath).map((call) => [call.body.id, call.body.text]), [
      ["run1", "try the other route"],
    ]);
  }));

test("EpicCommands: Pause sends one Epics.Pause with the epic's id", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => fake.callsTo(pauseEpicPath).length === 1, "the Pause call");
    await settle();
    assert.equal(fake.callsTo(pauseEpicPath).length, 1, "the press sent one Epics.Pause");
    assert.equal(fake.callsTo(pauseEpicPath)[0].body.id, widgetsFirst);
  }));

test("EpicCommands: Resume sends one Epics.Resume with the epic's id on a paused epic", (t) =>
  withApp(t.signal, { epics: pageEpic({ paused: true }) }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Resume");
    await until(t.signal, () => fake.callsTo(resumeEpicPath).length === 1, "the Resume call");
    await settle();
    assert.equal(fake.callsTo(resumeEpicPath).length, 1, "the press sent one Epics.Resume");
    assert.equal(fake.callsTo(resumeEpicPath)[0].body.id, widgetsFirst);
  }));

test("EpicCommands: Stop asks first, Keep it running sends no Epics.Stop, and the confirm sends one", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Stop…");
    await clickButton(container, ".epic-page .title-row .acts", "Keep it running");
    await settle();
    assert.equal(fake.callsTo(stopEpicPath).length, 0, "Keep it running sends no Epics.Stop");
    await clickButton(container, ".epic-page .title-row .acts", "Stop…");
    await clickButton(container, ".epic-page .title-row .acts", "Stop first");
    await until(t.signal, () => fake.callsTo(stopEpicPath).length === 1, "the Stop call");
    await settle();
    assert.equal(fake.callsTo(stopEpicPath).length, 1, "the confirm sent one Epics.Stop");
    assert.equal(fake.callsTo(stopEpicPath)[0].body.id, widgetsFirst);
  }));

test("EpicCommands: a Stop… confirm open on one epic does not carry over to the next epic", (t) => {
  const first = makeEpic(widgetsRepo, "first");
  const second = makeEpic(gadgetsRepo, "second");
  const epics = epicsView(
    [first, second],
    new Map([
      [first.id, detailOf(first, [])],
      [second.id, detailOf(second, [])],
    ]),
  );
  return withApp(t.signal, { rows: [], epics }, async (container, fake) => {
    assert.equal(cursorKey(container), epicKey(first));
    assert.equal(pageSlug(container), "first");
    await clickButton(container, ".epic-page .title-row .acts", "Stop…");
    assert.ok(container.querySelector(".epic-page .title-row .sure"), "the first epic's confirm shows");
    await press("j");
    assert.equal(cursorKey(container), epicKey(second));
    assert.equal(pageSlug(container), "second");
    assert.equal(container.querySelector(".epic-page .title-row .sure"), null, "the second epic shows no confirm");
    const labels = [...container.querySelectorAll(".epic-page .title-row .acts button")].map((button) => button.textContent);
    assert.ok(labels.includes("Stop…"), "the second epic offers Stop…");
    await settle();
    assert.equal(fake.callsTo(stopEpicPath).length, 0, "no Epics.Stop was sent");
  });
});

test("EpicAsk: a park's Retry sends exactly one Epics.Retry with the epic's id", (t) =>
  withApp(t.signal, { epics: pageEpic({ parked: "rate limit" }) }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page section.now", "Retry");
    await until(t.signal, () => fake.callsTo(retryEpicPath).length === 1, "the Retry call");
    await settle();
    assert.equal(fake.callsTo(retryEpicPath).length, 1, "the press sent exactly one Epics.Retry");
    assert.equal(fake.callsTo(retryEpicPath)[0].body.id, widgetsFirst);
  }));

test("Confirm sends Epics.Answer with the epic, a fresh decision id, the question's id, and the chosen card's id and what it does", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container, fake) => {
    await press("j");
    await clickCard(container, 0);
    await submitIn(container, ".epic-page section.now form.answer");
    await until(t.signal, () => fake.callsTo(answerEpicPath).length === 1, "the Answer call");
    const body = fake.callsTo(answerEpicPath)[0].body;
    assert.equal(body.epicId, widgetsFirst);
    assert.equal(body.question, "Q1");
    assert.equal(body.choice, "postgres");
    assert.equal(body.text, "Use PostgreSQL for the store.", "the text is what the choice does");
    assert.equal(typeof body.id === "string" && body.id !== "", true, "the decision has an id");
    assert.equal(body.id !== widgetsFirst && body.id !== "Q1", true, "the decision id is its own, not the epic's or the question's");
  }));

test("Something else sends Epics.Answer with the choice other and the typed text, trimmed", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container, fake) => {
    await press("j");
    await clickCard(container, 3);
    const box = container.querySelector<HTMLTextAreaElement>('.epic-page section.now textarea[aria-label="answer"]');
    assert.ok(box, "Something else shows its text box");
    await typeInto(box, "  use a file  ");
    await submitIn(container, ".epic-page section.now form.answer");
    await until(t.signal, () => fake.callsTo(answerEpicPath).length === 1, "the Answer call");
    const body = fake.callsTo(answerEpicPath)[0].body;
    assert.equal(body.question, "Q1");
    assert.equal(body.choice, "other");
    assert.equal(body.text, "use a file", "the typed text is sent trimmed");
  }));

test("a number key on the epic page selects its card, and confirm sends that choice", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container, fake) => {
    await press("j");
    assert.equal(selectedCard(container), 1, "the recommended card starts selected");
    await press("1");
    assert.equal(selectedCard(container), 0);
    await submitIn(container, ".epic-page section.now form.answer");
    await until(t.signal, () => fake.callsTo(answerEpicPath).length === 1, "the Answer call");
    assert.equal(fake.callsTo(answerEpicPath)[0].body.choice, "postgres");
    assert.equal(fake.callsTo(answerEpicPath)[0].body.text, "Use PostgreSQL for the store.");
  }));

test("a refused answer shows the server's message under the question's cards", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container, fake) => {
    fake.refuseNext.set(answerEpicPath, "question is closed");
    await press("j");
    await submitIn(container, ".epic-page section.now form.answer");
    await until(t.signal,
      () => alertTexts(container).some((text) => text.includes("question is closed")),
      "the refusal line",
    );
  }));

test("keys typed in the Something else box are text: j and p do nothing there, and Escape leaves it", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container) => {
    await press("j");
    await clickCard(container, 3);
    const box = container.querySelector<HTMLTextAreaElement>('.epic-page section.now textarea[aria-label="answer"]');
    assert.ok(box, "Something else shows its text box");
    const viewOn = () => container.querySelector(".inbox-rows")?.getAttribute("data-list") ?? null;
    const cursor = cursorKey(container);
    assert.equal(viewOn(), "inbox", "the view starts on the inbox");
    await act(() => {
      box.focus();
    });
    assert.equal(document.activeElement === box, true);
    const jEvent = await pressIn(box, "j");
    assert.equal(cursorKey(container), cursor, "j in the answer box moves no cursor");
    assert.equal(epicPageShown(container), true, "j in the answer box leaves the epic page open");
    assert.equal(jEvent.defaultPrevented, false, "j in the answer box is text, not a handled key");
    await pressIn(box, "p");
    assert.equal(viewOn(), "inbox", "p in the answer box leaves the view");
    assert.equal(document.activeElement === box, true, "the answer box keeps focus through list keys");
    await pressIn(box, "Escape");
    assert.equal(document.activeElement === box, false, "Escape blurs the answer box");
  }));

test("a digit typed in the Something else box selects no card", (t) =>
  withApp(t.signal, { epics: pageEpic(optionsQuestion) }, async (container) => {
    await press("j");
    await clickCard(container, 3);
    const box = container.querySelector<HTMLTextAreaElement>('.epic-page section.now textarea[aria-label="answer"]');
    assert.ok(box, "Something else shows its text box");
    await act(() => {
      box.focus();
    });
    await pressIn(box, "2");
    assert.equal(selectedCard(container), 3, "the digit is text, so Something else stays selected");
  }));

test("a command that fails shows its did-not-land line under the title row, and one that lands clears it", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    const noteText = () => container.querySelector('section.page[aria-label="epic"] .title-row + .page-note')?.textContent ?? "";
    fake.failNext.add(pauseEpicPath);
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => noteText().startsWith("Pause did not land: "), "the did-not-land line");
    assert.equal(noteText(), "Pause did not land: the server did not answer.", "the line gives the failure's plain reason");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => noteText() === "", "the line to clear");
  }));

test("Just happened shows the epic's history, read from its first entry", (t) =>
  withApp(t.signal, { epics: pageEpic({}, 0n, true) }, async (container, fake) => {
    fake.replies.set(historyPath, [
      { entries: [{ cursor: "c1", decision: { id: "d1", text: "use postgres" } }], next: "" },
    ]);
    await press("j");
    await until(t.signal, () => historyText(container).includes("decision · use postgres"), "the history entry");
    const [call] = fake.callsTo(historyPath);
    assert.equal(call.body.id, widgetsFirst);
    assert.equal(call.body.after, undefined, "the first page reads from the start");
    assert.equal(call.body.limit, 200);
  }));

test("the history reads page by page until a page names no next cursor", (t) =>
  withApp(t.signal, { epics: pageEpic({}, 0n, true) }, async (container, fake) => {
    fake.replies.set(historyPath, [
      { entries: [{ cursor: "c1", decision: { id: "d1", text: "first decision" } }], next: "c1" },
      { entries: [{ cursor: "c2", decision: { id: "d2", text: "second decision" } }], next: "" },
    ]);
    await press("j");
    await until(t.signal, () => historyText(container).includes("second decision"), "the second page");
    assert.equal(historyText(container).includes("first decision"), true, "the first page shows too");
    assert.deepEqual(
      fake.callsTo(historyPath).map((call) => call.body.after),
      [undefined, "c1"],
      "the second page is read after the first's next cursor, and no call follows a page with no next",
    );
  }));

test("a change to the epic's last write reads its history again", (t) =>
  withApp(t.signal, { epics: pageEpic({}, 1n, true) }, async (container, fake) => {
    fake.replies.set(historyPath, [
      { entries: [{ cursor: "c1", decision: { id: "d1", text: "before the change" } }], next: "" },
      { entries: [{ cursor: "c2", decision: { id: "d2", text: "after the change" } }], next: "" },
    ]);
    await press("j");
    await until(t.signal, () => historyText(container).includes("before the change"), "the first history");
    await mount(container, {
      rows: fixture(),
      now,
      clients: fake.clients,
      seen: fake.seen,
      epics: pageEpic({}, 2n, true),
    });
    await until(t.signal, () => historyText(container).includes("after the change"), "the reloaded history");
    assert.equal(fake.callsTo(historyPath).length, 2);
  }));

test("a history that fails draws no history and no error line, and the page stays open", (t) =>
  withApp(t.signal, { epics: pageEpic({}, 0n, true) }, async (container, fake) => {
    fake.failNext.add(historyPath);
    await press("j");
    await until(t.signal, () => fake.callsTo(historyPath).length === 1, "the History call");
    await act(async () => {});
    assert.equal(historyText(container).includes("decision"), false, "no entries are drawn");
    assert.deepEqual(alertTexts(container), [], "the failed read shows no error line");
    assert.equal(epicPageShown(container), true, "the epic page stays open");
  }));

// onStepOne moves the cursor from row a down to the first step of oneEpic, s1, and waits for its stream to start. It
// waits as wait does, so a test can pass untilTurn and the wait reads no clock.
async function onStepOne(
  signal: AbortSignal,
  container: HTMLElement,
  fake: FakeServer,
  wait: typeof until = until,
): Promise<void> {
  await press("j");
  await press("j");
  assert.equal(cursorKey(container), "step:s1");
  await wait(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the step stream");
}

// stepButtons are the buttons of the cursor's step dock: Interrupt on the state line, More, and, once More is pressed, the
// Stop question's Stop {label} and Keep it running.
function stepButtons(container: HTMLElement): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".reply:not([hidden]) .dock-box button")];
}

// stepButton is the cursor's step dock button named label: its text, or its accessible name for the icon-only More.
function stepButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = stepButtons(container).find(
    (element) => element.textContent === label || element.getAttribute("aria-label") === label,
  );
  assert.ok(button, `the step dock has a ${label} button`);
  return button;
}

// stepQuestion is the Stop question the cursor's step dock draws once More is pressed, and "" while it is closed.
function stepQuestion(container: HTMLElement): string {
  return container.querySelector(".reply:not([hidden]) .dock-box .confirm > span")?.textContent ?? "";
}

// stepInputNamed is the textarea of the step dock for the step with the given label, shown or hidden.
function stepInputNamed(container: HTMLElement, label: string): HTMLTextAreaElement {
  const box = [...container.querySelectorAll<HTMLTextAreaElement>(".reply textarea")].find((element) =>
    element.placeholder.startsWith(`Nudge ${label}.`),
  );
  assert.ok(box, `the step dock for ${label} has an input`);
  return box;
}

// stepButtonAbsent reports that the cursor's step dock has no button with the given text.
function stepButtonAbsent(container: HTMLElement, label: string): boolean {
  return stepButtons(container).every((element) => element.textContent !== label);
}

async function clickIn(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(clickEvent());
  });
}

test("a step entry streams Sessions.WatchConversation by its id, and the reader draws its reply as text", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    const [stream] = fake.callsTo(sessionWatchPath);
    assert.equal(stream.body.id, "s1");
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the step reply" }],
    });
    await until(t.signal, () => readerText(container).includes("the step reply"), "the step reply");
    assert.equal(readerHidden(container), false, "the reader shows on a step");
    assert.equal(container.querySelector(".question-card") === null, true, "a step draws no question");
    assert.equal(container.querySelector(".reader .option") === null, true, "a step draws no answer");
  }));

test("a step's reader at its top asks Sessions.Older for the page before its oldest item, and draws the page above", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake, untilTurn);
    const [stream] = fake.callsTo(sessionWatchPath);
    fake.replies.set(sessionOlderPath, [
      { items: [{ kind: "assistant", text: "the step's earlier reply" }], oldest: "4", more: false },
    ]);
    fake.emit(stream, {
      transcript: "t1",
      seq: "9",
      reset: true,
      oldest: "9",
      items: [{ kind: "assistant", text: "the step reply" }],
    });
    await untilTurn(t.signal, () => fake.callsTo(sessionOlderPath).length === 1, "the older request");
    assert.deepEqual(fake.callsTo(sessionOlderPath)[0].body, { id: "s1", before: "9" });
    await untilTurn(t.signal, () => readerText(container).includes("the step's earlier reply"), "the older page");
    const text = readerText(container);
    assert.ok(text.indexOf("the step's earlier reply") < text.indexOf("the step reply"), "the older page is drawn above");
  }));

test("Enter in a step's box calls Steps.Nudge once with its id and text, and clears the draft when it lands", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement === box, true);
    await typeInto(box, "try the other route");
    await pressIn(box, "Enter");
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge");
    assert.deepEqual(fake.callsTo(nudgePath)[0].body, { id: "s1", text: "try the other route" });
    await until(t.signal, () => box.value === "", "the landed draft cleared");
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 1, "the nudge was sent once");
  }));

test("the send button calls Steps.Nudge once with the step's id and text, and a blank box sends nothing", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("i");
    await typeInto(composer(container), "   ");
    await clickIn(composerSendButton(container));
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 0, "a blank box sends nothing");
    await typeInto(composer(container), "try the other route");
    await clickIn(composerSendButton(container));
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge");
    assert.deepEqual(fake.callsTo(nudgePath)[0].body, { id: "s1", text: "try the other route" });
    await until(t.signal, () => composer(container).value === "", "the landed draft cleared");
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 1, "the nudge was sent once");
  }));

test("a nudge on the cursor's step calls Steps.Nudge with that step's id, not the first step visited", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    await press("i");
    await typeInto(composer(container), "on two");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge on s2");
    assert.deepEqual(fake.callsTo(nudgePath)[0].body, { id: "s2", text: "on two" });
    await until(t.signal, () => composer(container).value === "", "the landed draft cleared");
    await typeInto(composer(container), "again on two");
    await clickIn(composerSendButton(container));
    await until(t.signal, () => fake.callsTo(nudgePath).length === 2, "the nudge on s2 by the send button");
    assert.deepEqual(fake.callsTo(nudgePath)[1].body, { id: "s2", text: "again on two" });
    await until(t.signal, () => composer(container).value === "", "the second landed draft cleared");
    await pressIn(composer(container), "Escape");
    await press("k");
    assert.equal(cursorKey(container), "step:s1");
    await press("i");
    await typeInto(composer(container), "back on one");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => fake.callsTo(nudgePath).length === 3, "the nudge on s1");
    assert.deepEqual(fake.callsTo(nudgePath)[2].body, { id: "s1", text: "back on one" });
  }));

test("a refused nudge keeps the step's draft and shows the refusal", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    fake.refuseNext.set(nudgePath, refusedNudgeText);
    await onStepOne(t.signal, container, fake);
    await press("i");
    await typeInto(composer(container), "try again");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => alertTexts(container).includes(`nudge refused: ${refusedNudgeText}`), "the refusal line");
    assert.equal(composer(container).value, "try again", "the draft is kept");
  }));

test("an unknown nudge outcome shows not known, keeps the draft, and Enter does not resend it", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    fake.failNext.add(nudgePath);
    await onStepOne(t.signal, container, fake);
    await press("i");
    await typeInto(composer(container), "try again");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => alertTexts(container).some((text) => text.startsWith("nudge not known")), "the unknown line");
    assert.equal(composer(container).value, "try again", "the draft is kept");
    await pressIn(composer(container), "Enter");
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 1, "the nudge was not resent");
  }));

test("Interrupt calls Steps.Hold with the step's id", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await clickIn(stepButton(container, "Interrupt"));
    await until(t.signal, () => fake.callsTo(holdPath).length === 1, "the hold");
    assert.deepEqual(fake.callsTo(holdPath)[0].body, { id: "s1" });
  }));

test("Stop this step… asks first: Keep it running sends nothing, and Stop s1 calls Steps.Stop with the step's id", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(stepButtonAbsent(container, "Stop s1"), true, "no Stop until More is pressed");
    await clickIn(stepButton(container, "Stop this step…"));
    assert.equal(stepQuestion(container), "Stop s1?", "More asks Stop s1?");
    assert.equal(fake.callsTo(stopStepPath).length, 0, "More alone sends nothing");
    await clickIn(stepButton(container, "Keep it running"));
    await settle();
    assert.equal(fake.callsTo(stopStepPath).length, 0, "Keep it running sends nothing");
    assert.equal(stepQuestion(container), "", "Keep it running closes the question");
    await clickIn(stepButton(container, "Stop this step…"));
    await clickIn(stepButton(container, "Stop s1"));
    await until(t.signal, () => fake.callsTo(stopStepPath).length === 1, "the stop");
    assert.deepEqual(fake.callsTo(stopStepPath)[0].body, { id: "s1" });
  }));

test("a draft stays with its step across cursor moves, and each step keeps its own box", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("i");
    await typeInto(composer(container), "for one");
    await pressIn(composer(container), "Escape");
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    assert.equal(composer(container).value, "", "s2 has its own empty box");
    await press("k");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(composer(container).value, "for one", "the draft survived");
  }));

test("a step's draft survives a poll that no longer lists the step", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Escape");
    const withoutSteps = epicsView([makeEpic(widgetsRepo, "first")], new Map());
    await mount(container, { rows: fixture(), now, clients: fake.clients, seen: fake.seen, epics: withoutSteps });
    await mount(container, { rows: fixture(), now, clients: fake.clients, seen: fake.seen, epics: oneEpic() });
    await press("g");
    await press("g");
    await onStepOne(t.signal, container, fake);
    assert.equal(composer(container).value, "keep going", "the draft survived the poll");
  }));

test("i focuses the step's box, its keys are text, and Escape leaves it", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement === box, true);
    await pressIn(box, "j");
    await pressIn(box, "?");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(container.querySelector('[role="dialog"][aria-label="Keys"]') === null, true);
    await pressIn(box, "Escape");
    assert.equal(document.activeElement === box, false);
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
  }));

test("1, x, e, Enter and | on a step entry call nothing and pin nothing", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    const before = fake.calls.length;
    await pressEach(["x", "Enter", "e", "1", "2", "3", "|"]);
    await settle();
    assert.equal(fake.calls.length, before, "no call reaches the server");
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), false, "| pinned nothing");
    assert.equal(cursorKey(container), "step:s1");
  }));

test("no Control or Panes call names a step, and no call starts a step", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    await pressEach(["x", "Enter", "e", "1", "2", "3", "|"]);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge");
    await clickIn(stepButton(container, "Interrupt"));
    await until(t.signal, () => fake.callsTo(holdPath).length === 1, "the hold");
    await clickIn(stepButton(container, "Stop this step…"));
    await clickIn(stepButton(container, "Stop s1"));
    await until(t.signal, () => fake.callsTo(stopStepPath).length === 1, "the stop");
    for (const call of fake.calls) {
      assert.equal(call.path.includes("StartStep"), false, `${call.path} is not a start`);
      if (call.path.startsWith("/orrery.v1.Panes/") || call.path.startsWith("/orrery.v1.Control/")) {
        const names = Object.values(call.body).some((value) => value === "s1" || value === "s2");
        assert.equal(names, false, `${call.path} names no step`);
      }
    }
  }));

test("leaving a step stops its stream, and the stream follows the cursor's step", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    const [first] = fake.callsTo(sessionWatchPath);
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    assert.equal(first.signal.aborted, true, "moving to s2 stops s1's stream");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 2, "the s2 stream");
    assert.equal(fake.callsTo(sessionWatchPath)[1].body.id, "s2");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(fake.callsTo(sessionWatchPath)[1].signal.aborted, true, "moving to a row stops s2's stream");
  }));

// steerRow is a listed conversation in the widgets epic's project, by its session id.
function steerRow(id: string, init: Partial<Row> = {}): Row {
  return row({
    key: `session:${id}`,
    id,
    archiveId: id,
    kind: "session",
    archiveKind: "headless",
    label: `${id} label`,
    project: widgetsRepo,
    state: "working",
    changed: 500,
    seen: 500,
    ...init,
  });
}

// deskWidth is a window wide enough for the desk layout, where the steering command is an icon in the title row.
const deskWidth = 1280;

test("SteerMenu: a choice sends the move as chosen, closes the menu, and moves the middle row to the session the re-read names", (t) => {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")], steering: "sess-old", source: "orreryd" });
  const epics = epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, repository: widgetsRepo })]]));
  const rows = [
    steerRow("sess-old", { kind: "session", archiveKind: "session", state: "done", label: "old steerer" }),
    steerRow("sess-new", { label: "next steerer" }),
  ];
  return withApp(t.signal, { rows: [...fixture(), ...rows], epics }, async (container, fake) => {
    await clickRow(container, epicKey(epic));
    const cursorBefore = cursorKey(container);
    fake.replies.set(getEpicPath, [
      {
        epic: { id: epic.id, repository: widgetsRepo, slug: "first", steering: "sess-new", live: true, source: "orreryd" },
        repository: widgetsRepo,
      },
    ]);
    await act(() => {
      container.querySelector<HTMLButtonElement>(".epic-page .title-row .icon.steer")?.dispatchEvent(new Event("click"));
    });
    assert.ok(container.querySelector(".steer-menu") !== null, "the steering menu opens under the title row's command");
    await act(() => {
      container.querySelector<HTMLButtonElement>('.steer-menu [data-session="sess-new"]')?.dispatchEvent(new Event("click"));
    });
    await until(t.signal, () => fake.callsTo(getEpicPath).length === 1, "the epic is read again");
    await until(t.signal, () => container.querySelector(".steer-menu") === null, "the menu closes once it steers");
    assert.deepEqual(fake.callsTo(moveEpicPath).map((call) => call.body), [{ id: epic.id, session: "sess-new" }], "one move, to the chosen session");
    assert.equal(cursorKey(container), cursorBefore, "the cursor stays on the epic");
    await until(
      t.signal,
      () => fake.callsTo(sessionWatchPath).some((call) => call.body.id === "sess-new"),
      "the middle row's conversation, the session the re-read names",
    );
  }, deskWidth);
});

test("SteerMenu: at 412 wide the foot bar's More sheet holds 'Steer it from another conversation', and that item opens the menu in a dialog in its place", (t) => {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")], steering: "sess-old", source: "orreryd" });
  const epics = epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, repository: widgetsRepo })]]));
  const rows = [steerRow("sess-old", { kind: "session", archiveKind: "session", state: "done", label: "old steerer" })];
  return withApp(t.signal, { rows: [...fixture(), ...rows], epics }, async (container) => {
    await clickRow(container, epicKey(epic));
    assert.equal(container.querySelector(".epic-page .title-row .icon"), null, "the title row holds no steering command");
    const more = [...container.querySelectorAll<HTMLButtonElement>(".epic-page .cmdbar button")].find(
      (button) => button.textContent === "More",
    );
    await act(() => {
      more?.dispatchEvent(new Event("click"));
    });
    const item = [...container.querySelectorAll<HTMLButtonElement>('[role="dialog"][aria-label="first"] button')].find(
      (button) => button.textContent === "Steer it from another conversation",
    );
    assert.ok(item !== undefined, "the More sheet holds the steering item");
    assert.equal(container.querySelector(".steer-anchor"), null, "no desk anchor is drawn at this width");
    await act(() => {
      item.dispatchEvent(new Event("click"));
    });
    assert.ok(
      container.querySelector('[role="dialog"][aria-label="Steer first from"] .steer-menu') !== null,
      "the item opens the steering menu in a dialog",
    );
    assert.equal(container.querySelector('[role="dialog"][aria-label="first"]'), null, "the More sheet closes");
  }, 412);
});

test("App reports the cursor's pane, epic, step and session to onViewing as the cursor moves", (t) => {
  const reported: Viewing[] = [];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("step-1")] });
  const session = row({
    key: "sess",
    id: "sess-9",
    archiveId: "sess-9",
    kind: "session",
    archiveKind: "session",
    label: "notes",
    project: "",
    host: "",
    state: "working",
    changed: 30,
    seen: 30,
  });
  const epics = epicsView([epic], new Map());
  return withApp(t.signal, { rows: [paneRow, session], epics, onViewing: (viewing) => reported.push(viewing) }, async (container) => {
    assert.equal(cursorKey(container), "a");
    assert.deepEqual(reported.at(-1), { case: "viewingPane", value: "a" }, "the cursor's pane is reported at mount");
    const atMount = reported.length;
    await press("j");
    assert.equal(cursorKey(container), epicKey(epic));
    assert.deepEqual(reported.at(-1), { case: "viewingEpic", value: epic.id }, "an epic entry reports its epic");
    await press("j");
    assert.equal(cursorKey(container), "step:step-1");
    assert.deepEqual(reported.at(-1), { case: "viewingSession", value: "step-1" }, "a step reports its session");
    await press("j");
    assert.equal(cursorKey(container), "sess");
    assert.deepEqual(reported.at(-1), { case: "viewingSession", value: "sess-9" }, "a session row reports its session");
    assert.equal(reported.length, atMount + 3, "each move reports once");
  });
});

// newButton is the New button in the inbox head.
function newButton(container: HTMLElement): HTMLButtonElement {
  const found = container.querySelector<HTMLButtonElement>(".inbox-head button:not(.icon-only)");
  assert.ok(found, "the inbox head has a button");
  assert.equal(found.textContent, "New", "the head's button reads New");
  return found;
}

// keysButton is the Keys control in the inbox foot, which holds the key list's icon and no text.
function keysButton(container: HTMLElement): HTMLButtonElement {
  const found = container.querySelector<HTMLButtonElement>('.inbox-foot button[aria-label="Keys"]');
  assert.ok(found, "the inbox foot has the Keys control");
  return found;
}

// pickerShown is whether the picker palette is open.
function pickerShown(container: HTMLElement): boolean {
  return container.querySelector('[role="dialog"][aria-label="New session"]') !== null;
}

// pickerInput is the palette's text box, which is drawn only while the palette is open.
function pickerInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>('[role="dialog"][aria-label="New session"] input');
  assert.ok(input, "the palette's text box is drawn");
  return input;
}

// typePicker types text into the palette's text box.
async function typePicker(container: HTMLElement, text: string): Promise<void> {
  const input = pickerInput(container);
  await act(() => {
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// pickerRows is the label of each row the palette lists, in order.
function pickerRows(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".pick-row .t")].map((element) => element.textContent ?? "");
}

// pickerRow is the button of the palette's row at index, which the palette must list.
function pickerRow(container: HTMLElement, index: number): HTMLButtonElement {
  const found = container.querySelectorAll<HTMLButtonElement>(".pick-row")[index];
  assert.ok(found, `the palette lists row ${index}`);
  return found;
}

async function clickElement(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(clickEvent());
  });
}

test("PickerPalette: the New button, ⌘K, Ctrl+K and N open the palette, and ⌘K opens it from a text box", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(pickerShown(container), false);
    await clickElement(newButton(container));
    assert.equal(pickerShown(container), true, "the New button opens the palette");
    await pressIn(pickerInput(container), "Escape");
    assert.equal(pickerShown(container), false, "Escape closes the palette");

    const meta = await press("k", { metaKey: true });
    assert.equal(pickerShown(container), true, "⌘K opens the palette");
    assert.equal(meta.defaultPrevented, true, "⌘K is not left to the browser");
    await pressIn(pickerInput(container), "Escape");

    await press("k", { ctrlKey: true });
    assert.equal(pickerShown(container), true, "Ctrl+K opens the palette");
    await pressIn(pickerInput(container), "Escape");

    await press("N");
    assert.equal(pickerShown(container), true, "N outside a text box opens the palette");
    await pressIn(pickerInput(container), "Escape");

    await act(() => composer(container).focus());
    await press("k", { metaKey: true });
    assert.equal(pickerShown(container), true, "⌘K opens the palette from inside the composer");
  }));

test("PickerPalette: N typed in the composer or the filter does not open the palette", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await press("N");
    assert.equal(pickerShown(container), false, "N typed in the filter does not open the palette");
    assert.equal(filterInput(container).value, "");

    await act(() => composer(container).focus());
    await press("N");
    assert.equal(pickerShown(container), false, "N typed in the composer does not open the palette");
  }));

test("PickerPalette: n still moves the cursor to the next unseen row", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("n");
    assert.equal(cursorKey(container), "c");
    assert.equal(pickerShown(container), false);
  }));

test("PickerPalette: the keys help lists ⌘K and N, each with what it opens", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("?");
    const help = container.querySelector('[role="dialog"][aria-label="Keys"]');
    assert.ok(help, "help panel is open");
    const lineFor = (cap: string) =>
      [...help.querySelectorAll(".keyline")].find((line) =>
        [...(line.querySelector(".ks")?.children ?? [])].some((item) => item.textContent === cap),
      );
    assert.match(lineFor("⌘K")?.children[1]?.textContent ?? "", /new session, or find one/);
    assert.match(lineFor("N")?.children[1]?.textContent ?? "", /new session, or find one/);
  }));

test("PickerPalette: a pane Picker.Run answers takes the cursor once its row arrives in a later render", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pickerAskPath, [
      { rows: [{ token: "tok-deploy", kind: "jump", pane: "%9", label: "deploy", note: "working" }] },
    ]);
    fake.replies.set(pickerRunPath, [{ pane: "%9" }]);
    await clickElement(newButton(container));
    await typePicker(container, "deploy");
    await pressIn(pickerInput(container), "Enter");
    await untilTurn(t.signal, () => pickerRows(container).length === 1, "the answer's row is listed");
    await clickElement(pickerRow(container, 0));
    await untilTurn(t.signal, () => !pickerShown(container), "the run closes the palette");
    assert.equal(cursorKey(container), "a", "the pane's row is not in the list yet");

    const deploy = row({ key: "pane:box:%9", id: "%9", label: "deploy", state: "working", changed: 500, seen: 500 });
    await mount(container, { rows: [...fixture(), deploy], now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "pane:box:%9");
  }));

test("PickerStartForm: a started session takes the cursor once its row arrives in a later render", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    fake.replies.set(pickerAskPath, [
      {
        rows: [
          {
            token: "tok-web",
            kind: "fresh",
            dir: "/work/web",
            pane: "",
            label: "web",
            note: "start fresh",
            prompt: false,
          },
        ],
      },
    ]);
    fake.replies.set(startSessionPath, [{ id: "sess-new" }]);
    await clickElement(newButton(container));
    await typePicker(container, "web");
    await pressIn(pickerInput(container), "Enter");
    await untilTurn(t.signal, () => pickerRows(container).length === 1, "the answer's row is listed");
    await clickElement(pickerRow(container, 0));
    const message = container.querySelector<HTMLTextAreaElement>('[role="dialog"][aria-label="New session"] textarea');
    assert.ok(message, "the start form has its first message box");
    await typeInto(message, "build the web page");
    await clickButton(container, '[role="dialog"][aria-label="New session"]', "Start the session⌘⏎");
    await untilTurn(t.signal, () => !pickerShown(container), "the start closes the palette");
    assert.equal(cursorKey(container), "a", "the started session's row is not in the list yet");

    const started = row({
      key: "session:sess-new",
      id: "sess-new",
      archiveId: "sess-new",
      kind: "session",
      label: "web",
      state: "working",
      changed: 500,
      seen: 500,
    });
    await mount(container, { rows: [...fixture(), started], now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "session:sess-new");
  }));

// letTurnsRun steps through a fixed run of turns inside act, so a write queued by a key has started before a test checks
// that none did. It reads no clock and arms no timer, unlike settle.
async function letTurnsRun(): Promise<void> {
  for (let round = 0; round < 50; round++) {
    await act(turn);
  }
}

// listDeployRow opens the palette, asks for deploy, and waits until the one row the answer holds is listed.
async function listDeployRow(signal: AbortSignal, container: HTMLElement, fake: FakeServer): Promise<void> {
  fake.replies.set(pickerAskPath, [
    { rows: [{ token: "tok-deploy", kind: "jump", pane: "%9", label: "deploy", note: "working" }] },
  ]);
  await clickElement(newButton(container));
  await typePicker(container, "deploy");
  await pressIn(pickerInput(container), "Enter");
  await untilTurn(signal, () => pickerRows(container).length === 1, "the answer's row is listed");
}

test("PickerPalette: Enter on a focused picker row is not defaultPrevented and writes no seen", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await listDeployRow(t.signal, container, fake);
    await act(() => pickerRow(container, 0).focus());
    const enter = await pressIn(pickerRow(container, 0), "Enter");
    assert.equal(enter.defaultPrevented, false, "Enter on a picker row keeps its native activation");
    await letTurnsRun();
    assert.equal(fake.callsTo(markPanePath).length, 0, "Enter behind the palette writes no seen");
  }));

test("PickerPalette: j on a focused picker row leaves the list's cursor where it was", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await listDeployRow(t.signal, container, fake);
    await act(() => pickerRow(container, 0).focus());
    assert.equal(cursorKey(container), "a");
    await pressIn(pickerRow(container, 0), "j");
    assert.equal(cursorKey(container), "a", "j behind the palette does not move the list's cursor");
  }));

test("PickerPalette: Enter on the focused New button is not defaultPrevented and writes no seen", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await act(() => newButton(container).focus());
    const enter = await pressIn(newButton(container), "Enter");
    assert.equal(enter.defaultPrevented, false, "Enter on New keeps its native activation");
    await letTurnsRun();
    assert.equal(fake.callsTo(markPanePath).length, 0, "Enter on New writes no seen");
  }));

test("NewPanel: at 760px and wider App opens the panel titled New session, with its hint", (t) =>
  withApp(
    t.signal,
    {},
    async (container) => {
      await clickElement(newButton(container));
      const dialog = container.querySelector<HTMLElement>('[role="dialog"][aria-label="New session"]');
      assert.ok(dialog, "the New session dialog is drawn");
      assert.equal(dialog.className, "panel", "the dialog is a panel");
      assert.equal(dialog.querySelector("h2.t-title")?.textContent, "New session");
      assert.equal(dialog.querySelector(".head .t-small")?.textContent, "N or ⌘K");
    },
    1024,
  ));

test("NewPanel: under 760px App opens the sheet titled New session, with no panel heading", (t) =>
  withApp(
    t.signal,
    {},
    async (container) => {
      await clickElement(newButton(container));
      const dialog = container.querySelector<HTMLElement>('[role="dialog"][aria-label="New session"]');
      assert.ok(dialog, "the New session dialog is drawn");
      assert.equal(dialog.className, "sheet", "the dialog is a sheet");
      assert.equal(dialog.querySelector(".sheet-head b")?.textContent, "New session");
      assertNone(dialog.querySelector("h2"), "a sheet has no heading");
    },
    600,
  ));

// listHidden is whether the inbox's list is hidden, which the narrow layout does while a page is drawn alone.
function listHidden(container: HTMLElement): boolean {
  const list = container.querySelector<HTMLElement>(".list");
  assert.ok(list, "the list is rendered");
  return list.hasAttribute("hidden");
}

// backButton is the reader's back button, the one that returns from a row's page.
function backButton(container: HTMLElement): HTMLButtonElement {
  const back = container.querySelector<HTMLButtonElement>('section[aria-label="reader"] button.back');
  assert.ok(back, "the reader has its back button");
  return back;
}

test("InboxTree: under 760px the inbox draws alone as the home page, with no row's page beside it", (t) =>
  withApp(
    t.signal,
    {},
    (container) => {
      assert.equal(listHidden(container), false, "the inbox is drawn");
      assert.equal(readerHidden(container), true, "no row's page is drawn beside the inbox");
    },
    600,
  ));

test("InboxTree: under 760px a row's page draws alone, and its Back returns to the inbox", (t) =>
  withApp(
    t.signal,
    {},
    async (container) => {
      await clickRow(container, "a");
      assert.equal(listHidden(container), true, "the opened row's page hides the inbox");
      assert.equal(readerHidden(container), false, "the opened row's page is drawn");
      await clickElement(backButton(container));
      assert.equal(listHidden(container), false, "Back draws the inbox again");
      assert.equal(readerHidden(container), true, "Back leaves no page beside the inbox");
      assert.equal(document.activeElement?.getAttribute("data-key"), "a", "Back puts the focus on the cursor's row");
    },
    600,
  ));

test("InboxTree: at 760px and wider the inbox stays beside a row's page, which draws from the start", (t) =>
  withApp(
    t.signal,
    {},
    async (container) => {
      assert.equal(listHidden(container), false, "the inbox is drawn");
      assert.equal(readerHidden(container), false, "the cursor's row page is drawn beside the inbox");
      await clickRow(container, "a");
      assert.equal(listHidden(container), false, "the inbox stays beside the page");
      assert.equal(readerHidden(container), false, "the page is drawn");
    },
    1024,
  ));

// archiveRows are the two rows the narrow archive tests archive: a waiting session row, three minutes older than now's
// four minutes, and a working one, each with its own archive id.
function archiveRows(): Row[] {
  return [
    row({
      key: "a",
      label: "tidepool work",
      project: "/work/tidepool",
      state: "waiting",
      changed: now - 240,
      seen: now - 240,
      archiveKind: "session",
      archiveId: "sess-a",
    }),
    row({
      key: "b",
      label: "bundling",
      project: "/work/web",
      state: "working",
      changed: now - 10,
      seen: now - 10,
      archiveKind: "session",
      archiveId: "sess-b",
    }),
  ];
}

// moreOf is the ⋯ button of the row under key.
function moreOf(container: HTMLElement, key: string): HTMLButtonElement {
  const more = rowElement(container, key).querySelector<HTMLButtonElement>(".more");
  assert.ok(more, `row ${key} has its More button`);
  return more;
}

// menuItem is the menu item, in a popover or a sheet, whose label is label.
function menuItem(container: HTMLElement, label: string): HTMLButtonElement {
  const item = [...container.querySelectorAll<HTMLButtonElement>(".menu-item")].find(
    (button) => button.querySelector("span")?.textContent === label,
  );
  assert.ok(item, `the menu has ${label}`);
  return item;
}

function sheetOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('section.sheet[role="dialog"]');
}

function toastOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>(".toast");
}

function toastText(container: HTMLElement): string | null | undefined {
  return toastOf(container)?.querySelector(".grow")?.textContent;
}

test("InboxTree: under 760px More opens a sheet whose line is the row's project, state in words and age", (t) =>
  withApp(
    t.signal,
    { rows: archiveRows() },
    async (container) => {
      await clickElement(moreOf(container, "a"));
      const sheet = sheetOf(container);
      assert.ok(sheet, "More opens a sheet");
      assert.equal(sheet.querySelector(".sheet-head b")?.textContent, "tidepool work", "the sheet is titled with the row");
      assert.equal(sheet.querySelector(".sheet-head")?.lastChild?.textContent, "tidepool · waiting on you · 4m", "the sheet's line");
    },
    600,
  ));

test("InboxTree: under 760px Archive from a row's sheet archives that row and shows Undo, which unarchives it", (t) =>
  withApp(
    t.signal,
    { rows: archiveRows() },
    async (container, fake) => {
      await clickElement(moreOf(container, "a"));
      await clickElement(menuItem(container, "Archive"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the Archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "sess-a" }, "Archive names the row");
      await until(t.signal, () => toastOf(container) !== null, "the toast");
      assert.equal(toastText(container), "Archived “tidepool work”.", "the toast names the row");
      await clickElement(toastButton(container));
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the Unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "session", id: "sess-a" }, "Undo names the same row");
      assert.equal(toastOf(container), null, "Undo takes the toast away");
      await until(t.signal, () => container.querySelector('[data-key="a"]') !== null, "the row back in the list");
    },
    600,
  ));

test("InboxTree: under 760px the archive toast goes five seconds after it shows, and a later archive restarts that count", (t) =>
  withApp(
    t.signal,
    { rows: archiveRows() },
    async (container, fake) => {
      await press("A");
      await until(t.signal, () => toastText(container) === "Archived “tidepool work”.", "the first toast");
      fake.seen.advance(3000);
      await press("A");
      await until(t.signal, () => toastText(container) === "Archived “bundling”.", "the second toast replaces the first");
      fake.seen.advance(4999);
      assert.equal(toastOf(container) !== null, true, "the toast stays under five seconds after the later archive");
      fake.seen.advance(1);
      await until(t.signal, () => toastOf(container) === null, "the toast goes five seconds after the later archive");
    },
    600,
  ));

test("InboxTree: at 760px and wider an archive shows no toast, and More opens the popover, not a sheet", (t) =>
  withApp(
    t.signal,
    { rows: archiveRows() },
    async (container, fake) => {
      await clickElement(moreOf(container, "a"));
      assert.equal(sheetOf(container), null, "More opens no sheet");
      assert.ok(container.querySelector(".row-menu-list"), "More opens the popover");
      await clickElement(menuItem(container, "Archive"));
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the Archive call");
      await until(t.signal, () => container.querySelector('[data-key="a"]') === null, "the archived row leaves the list");
      assert.equal(toastOf(container), null, "no toast draws at 760px");
    },
    760,
  ));

// toastButton is the toast's action button.
function toastButton(container: HTMLElement): HTMLButtonElement {
  const button = toastOf(container)?.querySelector<HTMLButtonElement>("button");
  assert.ok(button, "the toast has its action button");
  return button;
}

// unfocus takes the focus off the focused element, so the list's keys reach the list and not a reply box.
async function unfocus(): Promise<void> {
  await act(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });
}

test("InboxTree: under 760px a cursor on an epic draws no epic page until a click opens it, and the page's Back returns to the inbox", (t) =>
  withApp(
    t.signal,
    { epics: pageEpic() },
    async (container) => {
      await press("j");
      assert.equal(cursorKey(container), `epic:${widgetsFirst}`, "the cursor rests on the epic");
      assert.equal(listHidden(container), false, "the list stays drawn with the cursor on the epic");
      assert.equal(epicPageShown(container), false, "no epic page draws before the epic is opened");
      const line = container.querySelector(`[data-key="epic:${widgetsFirst}"]`);
      assert.ok(line, "the epic's line is drawn");
      await clickElement(line);
      assert.equal(listHidden(container), true, "the opened epic's page hides the list");
      assert.equal(epicPageShown(container), true, "the opened epic's page draws alone");
      const back = container.querySelector<HTMLButtonElement>('section[aria-label="epic"] button.back');
      assert.ok(back, "the epic page has its back button");
      await clickElement(back);
      assert.equal(listHidden(container), false, "Back draws the inbox again");
      assert.equal(epicPageShown(container), false, "Back leaves no epic page");
    },
    600,
  ));

test("InboxTree: under 760px Enter on the cursor's epic opens its page alone", (t) =>
  withApp(
    t.signal,
    { epics: pageEpic() },
    async (container) => {
      await press("j");
      assert.equal(cursorKey(container), `epic:${widgetsFirst}`, "the cursor rests on the epic");
      assert.equal(epicPageShown(container), false, "no epic page draws before Enter");
      await press("Enter");
      assert.equal(listHidden(container), true, "Enter's epic page hides the list");
      assert.equal(epicPageShown(container), true, "Enter draws the epic's page alone");
    },
    600,
  ));

test("InboxTree: under 760px an epic whose page Back closed opens again by a click and by Enter", (t) =>
  withApp(
    t.signal,
    { epics: pageEpic() },
    async (container) => {
      await press("j");
      const drawn = (found: Element | null, what: string): Element => {
        assert.ok(found, `${what} is drawn`);
        return found;
      };
      const line = () => drawn(container.querySelector(`[data-key="epic:${widgetsFirst}"]`), "the epic's line");
      const back = () => drawn(container.querySelector('section[aria-label="epic"] button.back'), "the epic page's back");
      await clickElement(line());
      await clickElement(back());
      assert.equal(epicPageShown(container), false, "Back leaves no epic page");
      await clickElement(line());
      assert.equal(epicPageShown(container), true, "a click opens the epic's page again");
      assert.equal(listHidden(container), true, "the reopened page draws alone");
      await clickElement(back());
      await press("Enter");
      assert.equal(epicPageShown(container), true, "Enter opens the epic's page again");
      assert.equal(listHidden(container), true, "Enter's reopened page draws alone");
    },
    600,
  ));

test("InboxTree: under 760px h and the left arrow on an opened row's page return to the inbox, as Back does", (t) =>
  withApp(
    t.signal,
    {},
    async (container) => {
      await clickRow(container, "a");
      await unfocus();
      assert.equal(listHidden(container), true, "the opened row's page hides the inbox");
      await press("h");
      assert.equal(listHidden(container), false, "h draws the inbox again");
      assert.equal(readerHidden(container), true, "h leaves no page beside the inbox");
      await clickRow(container, "a");
      await unfocus();
      await press("ArrowLeft");
      assert.equal(listHidden(container), false, "the left arrow draws the inbox again");
      assert.equal(readerHidden(container), true, "the left arrow leaves no page beside the inbox");
    },
    600,
  ));

// paneArchiveRow is a waiting pane row whose archive id is its orrery_id, which its own id does not hold.
function paneArchiveRow(): Row {
  return row({
    key: "pane:box:%3",
    id: "%3",
    kind: "pane",
    host: "box",
    label: "pane work",
    project: "/work/web",
    state: "waiting",
    changed: now - 240,
    seen: now - 240,
    archiveKind: "session",
    archiveId: "orr-p",
  });
}

test("InboxTree: under 760px A on a pane row archives the pane's orrery_id, and Undo unarchives that id", (t) =>
  withApp(
    t.signal,
    { rows: [paneArchiveRow()] },
    async (container, fake) => {
      assert.equal(cursorKey(container), "pane:box:%3", "the cursor rests on the pane row");
      await press("A");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the Archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "orr-p" }, "A names the pane's archive id");
      await until(t.signal, () => toastText(container) === "Archived “pane work”.", "the toast");
      await clickElement(toastButton(container));
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the Unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "session", id: "orr-p" }, "Undo names the same id");
      await until(t.signal, () => container.querySelector('[data-key="pane:box:%3"]') !== null, "the row back in the list");
    },
    600,
  ));

test("InboxTree: under 760px A on a headless row archives it as headless by its id, and Undo unarchives the same", (t) =>
  withApp(
    t.signal,
    {
      rows: [
        row({
          key: "session:h-9",
          id: "h-9",
          kind: "session",
          label: "headless work",
          project: "/work/web",
          state: "waiting",
          changed: now - 240,
          seen: now - 240,
          archiveKind: "headless",
          archiveId: "h-9",
        }),
      ],
    },
    async (container, fake) => {
      assert.equal(cursorKey(container), "session:h-9", "the cursor rests on the headless row");
      await press("A");
      await until(t.signal, () => fake.callsTo(archivePath).length === 1, "the Archive call");
      assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "headless", id: "h-9" }, "A names the row's kind and id");
      await until(t.signal, () => toastText(container) === "Archived “headless work”.", "the toast");
      await clickElement(toastButton(container));
      await until(t.signal, () => fake.callsTo(unarchivePath).length === 1, "the Unarchive call");
      assert.deepEqual(fake.callsTo(unarchivePath)[0].body, { kind: "headless", id: "h-9" }, "Undo names the same kind and id");
    },
    600,
  ));
