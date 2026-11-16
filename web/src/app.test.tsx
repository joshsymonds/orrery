import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { type EpicsView, epicsView } from "./epics";
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
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { pause, until as waitUntil } from "./testwait";

const now = 1000;

const paneWatchPath = "/orrery.v1.Panes/WatchConversation";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const sendTextPath = "/orrery.v1.Panes/SendText";
const sayPath = "/orrery.v1.Control/Say";
const pullPath = "/orrery.v1.Panes/PullQueued";
const queuedText = "queued words";
const intervalMs = 2;
const markPanePath = "/orrery.v1.Panes/MarkSeen";
const markSessionPath = "/orrery.v1.Control/Seen";
const seenFailedText = "could not mark seen";
const sendKeysPath = "/orrery.v1.Panes/SendKeys";
const answerPath = "/orrery.v1.Control/Answer";
const answerFailedText = "no answer was sent";
const nudgePath = "/orrery.v1.Steps/Nudge";
const holdPath = "/orrery.v1.Steps/Hold";
const stopStepPath = "/orrery.v1.Steps/Stop";
const refusedNudgeText = "step is not running";
const getEpicPath = "/orrery.v1.Epics/Get";
const historyPath = "/orrery.v1.Epics/History";
const pauseEpicPath = "/orrery.v1.Epics/Pause";
const resumeEpicPath = "/orrery.v1.Epics/Resume";
const stopEpicPath = "/orrery.v1.Epics/Stop";
const retryEpicPath = "/orrery.v1.Epics/Retry";
const answerEpicPath = "/orrery.v1.Epics/Answer";
const commandFailedText = "epic command failed";
const startSessionPath = "/orrery.v1.Control/Start";

type Clients = ReturnType<typeof createClients>;

type Seen = ReturnType<typeof fakeSeen>;

interface Props {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seen: Seen;
  epics?: EpicsView;
  host?: HostStatusResponse | null;
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
    ...init,
  };
}

// Attention order: a (needs you), c (new), b (working), d (seen). Project order: a, d, c, b.
function fixture(): Row[] {
  return [
    row({
      key: "a",
      label: "api",
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
      label: "docs",
      project: "/work/docs",
      host: "laptop",
      state: "done",
      summary: "wrote the guide",
      changed: 150,
      seen: 50,
      unseen: true,
    }),
    row({ key: "b", label: "web", project: "/work/web", state: "working", summary: "bundling", changed: 200, seen: 200 }),
    row({
      key: "d",
      label: "old",
      project: "/work/api",
      state: "done",
      summary: "finished last week",
      changed: 10,
      seen: 10,
    }),
  ];
}

// fixtureCounts is what the brand shows for fixture(): one row needs you, one is working, and two are new.
const fixtureCounts = ["st-waiting: 1 need you", "st-working: 1 working", "st-new: ✱ 2 new"];

const keysHelpList = ["j", "k", "↓", "↑", "gg", "G", "a", "p", "w", "n", "x", "Enter", "e", "/", "i", "1", "2", "3", "o", "|", "m", "?", "Esc"];

async function mount(container: HTMLElement, props: Props): Promise<void> {
  await act(() => {
    render(
      <App
        rows={props.rows}
        error={props.error}
        now={props.now}
        clients={props.clients}
        seenClock={props.seen.clock}
        seenWindow={props.seen.window}
        epics={props.epics}
        host={props.host}
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
): Promise<void> {
  const window = installTestDom();
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
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, ...props });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

// until waits as testwait's until does, with each pause run inside act, so the renders it lets through are applied.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(pause) });
}

// settle lets pending promises run, so a write the test expects to be absent has time to start.
async function settle(): Promise<void> {
  await act(() => new Promise((resolve) => setTimeout(resolve, 10)));
}

async function pressIn(element: Element, key: string): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
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
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) .composer textarea");
  assert.ok(box, "the reader has a composer");
  return box;
}

// composerSendButton is the send button of the reply box the cursor's row shows.
function composerSendButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>('.reply:not([hidden]) .composer button[aria-label="send"]');
  assert.ok(button, "the reply box has a send button");
  return button;
}

// composerEdgeNames are the edge classes a composer or a step box carries.
const composerEdgeNames = ["edge-working", "edge-waiting", "edge-idle", "edge-held"];

// edgesOf names the edge classes the composer around a reply box carries, in the order of composerEdgeNames.
function edgesOf(box: HTMLTextAreaElement): string[] {
  const root = box.closest(".composer");
  return composerEdgeNames.filter((edge) => root?.classList.contains(edge) === true);
}

function readerText(container: HTMLElement): string {
  return container.querySelector(".reader")?.textContent ?? "";
}

// readerHead is the head the cursor's reader opens with, or null when that reader draws none.
function readerHead(container: HTMLElement): Element | null {
  return container.querySelector('section[aria-label="reader"] > .head');
}

// pinnedHead is the head the pinned reader opens with, or null when no row is pinned.
function pinnedHead(container: HTMLElement): Element | null {
  return container.querySelector('section[aria-label="pinned reader"] > .head');
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
    container.querySelectorAll('[aria-label="reader"] .send .queue li .text'),
    (text) => text.textContent ?? "",
  );
}

// pinnedQueueTexts is the text of each message in the pinned foot queue, oldest first, without its tag.
function pinnedQueueTexts(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('[aria-label="pinned reader"] .send .queue li .text'),
    (text) => text.textContent ?? "",
  );
}

// workingPane is the pane a stream sends while it runs a tool. The tool started at 940, a minute before the test's
// now of 1000, and the last turn ended at 700, five minutes before it.
function workingPane(tool: string) {
  return {
    id: "a",
    state: "working",
    tool: JSON.stringify({ id: "t1", name: tool, input: "ls", start: 940 }),
    changed: "900",
    lastTurnEnd: "700",
  };
}

const paneRow = row({
  key: "a",
  label: "api",
  state: "waiting",
  question: "Ship it?",
  changed: 300,
  seen: 100,
  unseen: true,
});

const sessionRow = row({
  key: "s1",
  id: "s1",
  kind: "session",
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
  const input = filterInput(container);
  await act(() => {
    input.value = text;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function filterInput(container: HTMLElement): HTMLInputElement {
  const input = container.querySelector("input");
  assert.ok(input, "the filter input is rendered");
  return input;
}

function rowKeys(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-row]")].map((element) => element.getAttribute("data-key") ?? "");
}

function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".b-row.cur")?.getAttribute("data-key") ?? null;
}

function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

function sectionTitles(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".b-sec")].map((element) => element.querySelector(".title")?.textContent ?? null);
}

// headCounts is the count each section head shows, in order.
function headCounts(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".b-sec")].map((element) => element.querySelector("i")?.textContent ?? null);
}

// headClasses is the class each section head carries, in order.
function headClasses(container: HTMLElement): (string | null)[] {
  return [...container.querySelectorAll(".b-sec")].map((element) => element.getAttribute("class"));
}

function rowElement(container: HTMLElement, key: string): Element {
  const element = container.querySelector(`[data-row][data-key="${key}"]`);
  assert.ok(element, `row ${key} is rendered`);
  return element;
}

// listChildren names the children of the list in order by class: the top block, then the rows.
function listChildren(container: HTMLElement): string[] {
  const list = container.querySelector(".list");
  assert.ok(list !== null, "the list is rendered");
  return [...list.children].map((element) => element.getAttribute("class") ?? "");
}

// topChildren names the children of the top block, when the top block is inside the list, in order by class.
function topChildren(container: HTMLElement): string[] {
  const top = container.querySelector(".list > .top");
  assert.ok(top !== null, "the top block is inside the list");
  return [...top.children].map((element) => element.getAttribute("class") ?? "");
}

// outsideList names the elements the selector matches that sit outside the list, by class.
function outsideList(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)]
    .filter((element) => element.closest(".list") === null)
    .map((element) => element.getAttribute("class") ?? "");
}

// countTexts names each count the brand shows, in order, by class and text.
function countTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".brand .counts > span")].map(
    (element) => `${element.getAttribute("class")}: ${element.textContent}`,
  );
}

// hostTexts names the hosts the footer lists, in order.
function hostTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".lfoot > .host")].map((element) => element.textContent ?? "");
}

// hostStatus is the host's status the footer's meters read: its host and three stats, two of them with a percent.
function hostStatus(): HostStatusResponse {
  return create(HostStatusResponseSchema, {
    host: "den",
    stats: [
      { label: "cpu", value: "12%", percent: 12, level: "ok" },
      { label: "uptime", value: "3d", level: "warn" },
      { label: "mem", value: "7.1 GB", percent: 88, level: "crit" },
    ],
  });
}

// footerChildren names the children of the footer in order, by class.
function footerChildren(container: HTMLElement): string[] {
  const footer = container.querySelector(".lfoot");
  assert.ok(footer !== null, "the footer is rendered");
  return [...footer.children].map((element) => element.getAttribute("class") ?? "");
}

// meterButtonShown is whether the footer draws the meters button.
function meterButtonShown(container: HTMLElement): boolean {
  return container.querySelector(".lfoot > .meters") !== null;
}

// meterText is the text of the element the selector picks inside the footer's meters button, or null when none is.
function meterText(container: HTMLElement, selector: string): string | null {
  return container.querySelector(`.lfoot > .meters ${selector}`)?.textContent ?? null;
}

// meterPanelShown is whether the panel of the host's stats is open.
function meterPanelShown(container: HTMLElement): boolean {
  return container.querySelector(".meters-panel") !== null;
}

// meterLabels is the label of each stat the open panel draws, in order.
function meterLabels(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".meters-panel .meter-label")].map((element) => element.textContent ?? "");
}

// clickMeterButton clicks the footer's meters button, which the footer must draw.
async function clickMeterButton(container: HTMLElement): Promise<void> {
  const button = container.querySelector(".lfoot > .meters");
  assert.ok(button, "the footer draws the meters button");
  await act(() => {
    button.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// newCount is the text of the brand's new count, or null when the brand shows none.
function newCount(container: HTMLElement): string | null {
  return container.querySelector(".brand .counts .st-new")?.textContent ?? null;
}

// chipTexts is the text of each option chip the row draws, in order. A row with no chips names none.
function chipTexts(container: HTMLElement, key: string): string[] {
  return [...rowElement(container, key).querySelectorAll(".opts > .opt")].map((chip) => chip.textContent ?? "");
}

// keyCaps is the key cap of each chip the row draws, in order, or null for a chip with none.
function keyCaps(container: HTMLElement, key: string): (string | null)[] {
  return [...rowElement(container, key).querySelectorAll(".opts > .opt")].map(
    (chip) => chip.querySelector("kbd")?.textContent ?? null,
  );
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

test("a shows rows by attention and keeps the cursor's row", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("p");
    await press("j");
    assert.equal(cursorKey(container), "d");
    await press("a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(cursorKey(container), "d");
    assert.deepEqual(sectionTitles(container), ["Needs you", "New since you looked", "Working", "Seen"]);
  }));

test("p shows rows by project and keeps the cursor's row", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("p");
    assert.deepEqual(rowKeys(container), ["a", "d", "c", "b"]);
    assert.equal(cursorKey(container), "c");
    assert.deepEqual(sectionTitles(container), ["/work/api", "/work/docs", "/work/web"]);
  }));

test("w goes to the first waiting row and does nothing when no row waits", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("w");
    assert.equal(cursorKey(container), "a");

    await mount(container, { rows: fixture().slice(1), error: "", now, clients: fake.clients, seen: fake.seen });
    await press("G");
    await press("w");
    assert.equal(cursorKey(container), "d");
  }));

test("n goes to the next unseen row after the cursor and wraps", (t) =>
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

test("? toggles a help panel listing exactly the keys", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
    await press("?");
    const help = container.querySelector('[aria-label="keys"]');
    assert.ok(help, "help panel is open");
    const listed = [...help.querySelectorAll("kbd")].map((element) => element.textContent);
    assert.deepEqual(listed, keysHelpList);
    await press("?");
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
  }));

test("Esc closes the help when the filter does not have focus", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("?");
    await press("Escape");
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
  }));

test("? opens the key list over the page, outside the list, and a second ? closes it", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("?");
    const help = container.querySelector('[aria-label="keys"]');
    assert.ok(help !== null, "help panel is open");
    assert.equal(help.closest(".list") === null, true, "the key list is not inside the list");
    assert.equal(container.querySelector(".list .keys") === null, true, "no key list is inside the list");
    assert.equal(help.parentElement?.classList.contains("app"), true, "the key list sits in the page");
    assert.equal(help.previousElementSibling?.classList.contains("panes"), true, "the key list follows the columns");
    await press("?");
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
  }));

test("the footer names the loaded rows' hosts once each, in the order first seen, and ends with the keys hint", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.deepEqual(hostTexts(container), ["box", "laptop"]);
    assert.equal(container.querySelector(".lfoot > .keys-hint")?.textContent, "? keys");
  }));

test("the footer lists hosts in the order the rows first show them", (t) =>
  withApp(t.signal, 
    {
      rows: [
        row({ key: "x", host: "laptop" }),
        row({ key: "y", host: "box" }),
        row({ key: "z", host: "laptop" }),
      ],
    },
    (container) => {
      assert.deepEqual(hostTexts(container), ["laptop", "box"]);
    },
  ));

test("a session row's empty host adds nothing to the footer", (t) =>
  withApp(t.signal, 
    {
      rows: [
        row({ key: "s1", id: "s1", kind: "session", host: "", label: "notes", changed: 30, seen: 30 }),
        row({ key: "x", host: "laptop" }),
      ],
    },
    (container) => {
      assert.deepEqual(hostTexts(container), ["laptop"]);
    },
  ));

test("the footer's meters button follows the host names and reads the status's host and first stat", (t) =>
  withApp(t.signal, { host: hostStatus() }, (container) => {
    assert.deepEqual(footerChildren(container), ["host", "host", "meters", "keys-hint"]);
    assert.equal(meterText(container, ".meters-host"), "den");
    assert.equal(meterText(container, ".meter-label"), "cpu");
  }));

test("m opens a panel with every stat, and a second m closes it", (t) =>
  withApp(t.signal, { host: hostStatus() }, async (container) => {
    assert.equal(meterPanelShown(container), false);
    await press("m");
    assert.deepEqual(meterLabels(container), ["cpu", "uptime", "mem"]);
    await press("m");
    assert.equal(meterPanelShown(container), false);
  }));

test("a click on the meters button opens the panel, and a second click closes it", (t) =>
  withApp(t.signal, { host: hostStatus() }, async (container) => {
    await clickMeterButton(container);
    assert.deepEqual(meterLabels(container), ["cpu", "uptime", "mem"]);
    await clickMeterButton(container);
    assert.equal(meterPanelShown(container), false);
  }));

test("with a null status the footer draws no meters button, and m opens no panel, even once a status arrives", (t) =>
  withApp(t.signal, { host: null }, async (container, fake) => {
    assert.equal(meterButtonShown(container), false);
    await press("m");
    assert.equal(meterPanelShown(container), false);
    await mount(container, {
      rows: fixture(),
      error: "",
      now,
      clients: fake.clients,
      seen: fake.seen,
      host: hostStatus(),
    });
    assert.equal(meterButtonShown(container), true);
    assert.equal(meterPanelShown(container), false);
  }));

test("with no status given the footer draws no meters button, and m opens no panel", (t) =>
  withApp(t.signal, {}, async (container) => {
    assert.equal(meterButtonShown(container), false);
    await press("m");
    assert.equal(meterPanelShown(container), false);
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
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "x and e in the filter write no seen");
    await press("Enter");
    assert.equal(document.activeElement === filterInput(container), false);
    assert.equal(fake.callsTo(markPanePath).length, 0, "Enter in the filter writes no seen");
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("Esc in the filter clears it and blurs it", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(rowKeys(container), ["c"]);
    await press("Escape");
    assert.equal(filterInput(container).value, "");
    assert.equal(document.activeElement === filterInput(container), false);
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("the filter keeps rows whose fields contain every typed word, case-insensitively", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "API WAITING");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "api done");
    assert.deepEqual(rowKeys(container), ["d"]);
    await typeFilter(container, "build");
    assert.deepEqual(rowKeys(container), ["a"]);
    await typeFilter(container, "laptop");
    assert.deepEqual(rowKeys(container), ["c"]);
    await typeFilter(container, "");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("the filter hides sections left empty", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(sectionTitles(container), ["New since you looked"]);
  }));

test("modifier keys leave the cursor alone", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("j", { ctrlKey: true });
    await press("j", { metaKey: true });
    await press("j", { altKey: true });
    await press("G", { ctrlKey: true });
    assert.equal(cursorKey(container), "a");
  }));

test("u and N outside the filter change nothing", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("u");
    await press("N");
    assert.equal(cursorKey(container), "a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
    assert.equal(filterInput(container).value, "");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "no seen was written");
  }));

test("the cursor keeps its row across new rows and clamps when its row leaves", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");

    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "b");

    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("an unmoved cursor keeps its row when a row sorts in above it", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(cursorKey(container), "a");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "a");
  }));

test("a clamped cursor keeps the row it moved to", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture().filter((item) => item.key !== "b"), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("only unseen rows carry the unseen mark", (t) =>
  withApp(t.signal, {}, (container) => {
    const marked = [...container.querySelectorAll("[data-unseen]")].map((element) => element.getAttribute("data-key"));
    assert.deepEqual(marked, ["a", "c"]);
    assert.equal(rowElement(container, "b").hasAttribute("data-unseen"), false);
    assert.equal(rowElement(container, "d").hasAttribute("data-unseen"), false);
  }));

test("a row shows its tag, label, project's last path element, age, and reply text", (t) =>
  withApp(t.signal, {}, (container) => {
    const element = rowElement(container, "a");
    assert.equal(element.querySelector(".label")?.textContent, "api");
    assert.equal(element.querySelector(".proj")?.textContent, "api");
    assert.equal(element.querySelector(".age")?.textContent, "11m");
    assert.equal(element.querySelector(".ty")?.textContent, "QUESTION");
    assert.equal(element.querySelector(".l2")?.textContent, "Ship it?", "the question replaces the summary");
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

test("a waiting row leads its first line with a QUESTION or WAITING tag, and no other row has a tag", (t) =>
  withApp(t.signal, { rows: stateRows() }, (container) => {
    const tagOf = (key: string) => rowElement(container, key).querySelector(".l1 .ty")?.textContent ?? null;
    assert.equal(tagOf("q"), "QUESTION", "a waiting row with a question");
    assert.equal(tagOf("w"), "WAITING", "a waiting row without one");
    assert.equal(tagOf("k"), null, "a working row");
    assert.equal(tagOf("d"), null, "a done row");
    assert.equal(tagOf("i"), null, "an interrupted row");
    assert.equal(rowElement(container, "q").querySelector(".l1")?.firstElementChild?.classList.contains("ty"), true);
  }));

test("no row draws a state element, and no row's first line holds its state word", (t) =>
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
      assert.equal(rowElement(container, key).querySelector(".l1")?.textContent?.includes(word), false, `row ${key} has no ${word}`);
    }
  }));

test("each row's glyph carries its state class", (t) =>
  withApp(t.signal, { rows: stateRows() }, (container) => {
    const glyphOf = (key: string) => rowElement(container, key).querySelector(".g")?.getAttribute("class") ?? null;
    assert.equal(glyphOf("q"), "g g-waiting");
    assert.equal(glyphOf("k"), "g g-working");
    assert.equal(glyphOf("d"), "g g-done");
    assert.equal(glyphOf("i"), "g g-interrupted");
  }));

test("an unseen row's reply text runs two lines, and a seen row's runs one", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.equal(rowElement(container, "a").querySelector(".l2")?.getAttribute("class"), "l2", "a is unseen");
    assert.equal(rowElement(container, "b").querySelector(".l2")?.getAttribute("class"), "l2 one", "b is seen");
  }));

test("a section head shows its title and its row count, and Needs you and New since you looked carry their marks", (t) =>
  withApp(t.signal, 
    {
      rows: [
        row({ key: "w1", state: "waiting", question: "Ship it?", changed: 300, seen: 300 }),
        row({ key: "w2", state: "waiting", changed: 200, seen: 200 }),
        row({ key: "n1", state: "done", changed: 400, seen: 100, unseen: true }),
        row({ key: "r1", state: "working", changed: 50, seen: 50 }),
        row({ key: "s1", state: "done", changed: 10, seen: 10 }),
      ],
    },
    (container) => {
      assert.deepEqual(sectionTitles(container), ["Needs you", "New since you looked", "Working", "Seen"]);
      assert.deepEqual(headCounts(container), ["2", "1", "1", "1"]);
      assert.deepEqual(headClasses(container), ["b-sec need", "b-sec fresh", "b-sec", "b-sec"]);
    },
  ));

test("a row that is not waiting shows its summary, not its question", (t) =>
  withApp(t.signal, 
    {
      rows: [row({ key: "s", state: "working", question: "stale?", summary: "compiling" })],
    },
    (container) => {
      assert.equal(rowElement(container, "s").querySelector(".l2")?.textContent, "compiling", "only a waiting row shows its question");
    },
  ));

test("reply text renders as text, never as markup", (t) =>
  withApp(t.signal, 
    {
      rows: [row({ key: "x", state: "working", summary: "<script>alert(1)</script> and <img src=x onerror=alert(1)>" })],
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

test("an empty project title shows the row's host", (t) =>
  withApp(t.signal, { rows: [row({ key: "p", project: "", host: "box", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["box"]);
  }));

test("an empty project title with no host shows no directory", (t) =>
  withApp(t.signal, { rows: [row({ key: "p", project: "", host: "", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["no directory"]);
  }));

test("the brand, filter, and views sit inside the list, above its rows, and no top block is outside it", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.deepEqual(listChildren(container), ["top", "rows", "lfoot"]);
    assert.deepEqual(topChildren(container), ["brand", "filter", "views"]);
    assert.deepEqual(outsideList(container, ".top, .error"), []);
  }));

test("a non-empty error shows one line in the top block, above the rows", (t) =>
  withApp(t.signal, { error: "list unavailable" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]')?.textContent, "list unavailable");
    assert.deepEqual(topChildren(container), ["brand", "filter", "views", "error"]);
    assert.deepEqual(listChildren(container), ["top", "rows", "lfoot"]);
    assert.deepEqual(outsideList(container, ".error"), []);
  }));

test("no error line shows when the error is empty", (t) =>
  withApp(t.signal, { error: "" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]') === null, true);
  }));

test("the brand draws the orrery mark as a 72-unit drawing hidden from assistive tech", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.equal(container.querySelector(".brand > svg.mark")?.getAttribute("viewBox"), "18 18 72 72");
    assert.equal(container.querySelector(".brand > svg.mark")?.getAttribute("aria-hidden"), "true");
    assert.equal(container.querySelector(".brand > svg.mark")?.getAttribute("style"), null);
    assert.equal(container.querySelectorAll(".brand > svg.mark > rect").length, 1);
    assert.equal(container.querySelectorAll(".brand > svg.mark > path").length, 5);
  }));

test("the brand reads orrery, and its counts stay in place when every count is zero", (t) =>
  withApp(t.signal, { rows: [] }, (container) => {
    assert.equal(container.querySelector(".brand")?.textContent, "orrery");
    assert.equal(container.querySelector(".brand .counts") === null, false, "the counts span stays");
    assert.deepEqual(countTexts(container), []);
  }));

test("the brand counts the loaded rows that need you, are working, and are new", (t) =>
  withApp(t.signal, {}, (container) => {
    assert.deepEqual(countTexts(container), fixtureCounts);
  }));

test("a count of zero is left out of the brand", (t) =>
  withApp(t.signal, { rows: [row({ key: "s", state: "working", changed: 1, seen: 1 })] }, (container) => {
    assert.deepEqual(countTexts(container), ["st-working: 1 working"]);
  }));

test("a successful seen write takes its row out of the new count", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.equal(newCount(container), "✱ 2 new");
    await press("x");
    await until(t.signal, () => newCount(container) === "✱ 1 new", "the new count to fall");
    assert.deepEqual(countTexts(container), ["st-waiting: 1 need you", "st-working: 1 working", "st-new: ✱ 1 new"]);
    assert.equal(fake.callsTo(markPanePath).length, 1);
  }));

test("typing a filter leaves the counts as they were", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(rowKeys(container), ["c"]);
    assert.deepEqual(countTexts(container), fixtureCounts);
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

test("the cursor's reader opens on its head: the label, the project and host, and the state's pill", (t) =>
  withApp(t.signal, { rows: [paneRow] }, (container) => {
    assert.equal(container.querySelector('section[aria-label="reader"]')?.firstElementChild?.tagName, "HEADER");
    assert.equal(readerHead(container)?.querySelector("h1")?.textContent, "api");
    assert.equal(readerHead(container)?.querySelector(".where")?.textContent, "app · box");
    assert.equal(readerHead(container)?.querySelector(".pill .word")?.textContent, "waiting");
    assert.equal(readerHead(container)?.querySelector(".pill .clock") === null, true);
  }));

test("a session's head has no where line and a pill of its state, with no tool or clock", (t) =>
  withApp(t.signal, { rows: [sessionRow] }, (container) => {
    assert.equal(readerHead(container)?.querySelector("h1")?.textContent, "notes");
    assert.equal(readerHead(container)?.querySelector(".where") === null, true);
    assert.equal(readerHead(container)?.querySelector(".pill .word")?.textContent, "working");
    assert.equal(readerHead(container)?.querySelector(".pill .tool") === null, true);
    assert.equal(readerHead(container)?.querySelector(".pill .clock") === null, true);
  }));

test("a step's head names the step, its epic, agent and round, and its pill runs from the step's start", (t) => {
  const running = makeStep("s1", { started: BigInt(now - 60) });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [running] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(readerHead(container)?.querySelector("h1")?.textContent, "s1");
    assert.equal(readerHead(container)?.querySelector(".where")?.textContent, "first › builder · step · round 1");
    assert.equal(readerHead(container)?.querySelector(".pill .word")?.textContent, "running");
    assert.equal(readerHead(container)?.querySelector(".pill .clock")?.textContent, "1:00");
  });
});

test("a held step's head pill reads held with no clock", (t) => {
  const held = makeStep("s1", { held: true });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [held] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(readerHead(container)?.querySelector(".pill .word")?.textContent, "held");
    assert.equal(readerHead(container)?.querySelector(".pill .clock") === null, true);
  });
});

test("each step box takes its own step's held flag, and a box the cursor left keeps its own", (t) => {
  const epics = epicsView(
    [makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1", { held: true }), makeStep("s2")] })],
    new Map(),
  );
  return withApp(t.signal, { epics }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.deepEqual(edgesOf(cursorStepInput(container)), ["edge-held"]);
    assert.equal(cursorStepInput(container).placeholder, "Nudge s1: it resumes the step with your text");
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    assert.deepEqual(edgesOf(cursorStepInput(container)), ["edge-working"]);
    assert.equal(cursorStepInput(container).placeholder, "Nudge s2: it interrupts the turn, then prompts");
    assert.deepEqual(edgesOf(stepInputNamed(container, "s1")), ["edge-held"], "s1's hidden box keeps its held edge");
  });
});

test("a working pane's delta puts its tool and time in the head's pill, and draws no note under the reader", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => readerHead(container)?.querySelector(".pill .word")?.textContent === "working", "the working pill");
    assert.equal(readerHead(container)?.querySelector(".pill .tool")?.textContent, "Bash");
    assert.equal(readerHead(container)?.querySelector(".pill .clock")?.textContent, "1:00");
    assert.equal(readerNote(container) === null, true, "the cursor's reader draws no working note");
    assert.equal(noteWords(container.querySelector('section[aria-label="reader"]')), false, "the reader's text names no note");
    assert.equal(container.querySelector(".work-line") === null, true, "no work line is drawn");
  }));

test("a later delta whose pane is done drops the pill's tool and draws no working note", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => readerHead(container)?.querySelector(".pill .tool")?.textContent === "Bash", "the working tool");
    fake.emit(stream, {
      transcript: "t1",
      seq: "2",
      items: [],
      state: { pane: { id: "a", state: "done", tool: "", changed: "1000", lastTurnEnd: "1000" } },
    });
    await until(t.signal, () => readerHead(container)?.querySelector(".pill .word")?.textContent === "done", "the done pill");
    assert.equal(readerHead(container)?.querySelector(".pill .tool") === null, true);
    assert.equal(readerNote(container) === null, true, "the cursor's reader draws no working note");
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
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="reader"]')), [
      "header.head",
      "div.reader",
      "div.send",
    ]);
  }));

test("a step's reader is its head, its conversation and its foot, and nothing else", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="reader"]')), [
      "header.head",
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
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    assert.deepEqual(sectionChildren(container.querySelector('section[aria-label="pinned reader"]')), [
      "header.head",
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
    assert.equal(container.querySelectorAll('[aria-label="reader"] .send .queue li.canceled').length, 1);
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
    assert.equal(container.querySelector(".reply[hidden] .queue") === null, true, "a's hidden composer draws no queue");
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

test("a draft survives moving the cursor away and back", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "half a thought");
    await pressIn(box, "Escape");
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
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the failed send's line");
    await pressIn(box, "Escape");
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
    await until(t.signal, () => container.querySelector('[role="alert"]') !== null, "the failed send's line");
    await pressIn(composer(container), "Escape");
    // A poll whose Sessions.List failed lists the panes only; the next poll lists the session again.
    await mount(container, { rows: [paneRow], error: "", now, clients: fake.clients, seen: fake.seen });
    await mount(container, { rows: [paneRow, sessionRow], error: "", now, clients: fake.clients, seen: fake.seen });
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
  withApp(t.signal, {}, async (container) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "j");
    await pressIn(box, "G");
    await pressIn(box, "?");
    await pressIn(box, "/");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
    assert.equal(document.activeElement === filterInput(container), false);
    assert.equal(document.activeElement === box, true);
    await pressIn(box, "Escape");
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

test("e writes seen for the cursor's row, then rests on the next unseen row", (t) =>
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
    await mount(container, { rows: moved, error: "", now, clients: fake.clients, seen: fake.seen });
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

    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), false, "the server has not caught up yet");

    const moved = fixture().map((item) => (item.key === "a" ? { ...item, changed: 400 } : item));
    await mount(container, { rows: moved, error: "", now, clients: fake.clients, seen: fake.seen });
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
  kind: "session",
  label: "docs",
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
  kind: "session",
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
  kind: "session",
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

// openPermissionRequest waits for the cursor's session stream, sends the permission request, and waits for its chips.
async function openPermissionRequest(signal: AbortSignal, container: HTMLElement, fake: FakeServer): Promise<void> {
  await until(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
  fake.emit(fake.callsTo(sessionWatchPath)[0], permissionStream);
  await until(signal, () => chipTexts(container, "s1").length === 2, "the chips");
}

test("a waiting pane row's options are numbered chips, the fourth unnumbered, and 2 sends keys [\"2\"]", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    assert.deepEqual(chipTexts(container, "f"), ["1Alpha", "2Beta", "3Gamma", "Delta"]);
    assert.deepEqual(keyCaps(container, "f"), ["1", "2", "3", null]);
    const chipIsButton = rowElement(container, "f").querySelector(".opts button") !== null;
    assert.equal(chipIsButton, false, "chips are labels, not buttons");
    await press("2");
    await until(t.signal, () => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "f", keys: ["2"], expectQuestion: "qh-f" });
  }));

test("a pane permission question's chips read 1Yes, 2Yes, don't ask again, and 3No", (t) =>
  withApp(t.signal, { rows: [permissionRow] }, (container) => {
    assert.deepEqual(chipTexts(container, "p"), ["1Yes", "2Yes, don't ask again", "3No"]);
    assert.deepEqual(keyCaps(container, "p"), ["1", "2", "3"]);
  }));

test("a landed pane answer leaves one line naming its choice in place of the chips", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container) => {
    await press("2");
    await until(t.signal, () => answerLine(container, "f") !== null, "the answer line");
    assert.equal(answerLine(container, "f"), "You answered 2 · Beta");
    assert.deepEqual(chipTexts(container, "f"), [], "the chips give way to the line");
  }));

test("a failed answer leaves no line, and the chips stay", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    fake.failNext.add(sendKeysPath);
    await press("2");
    await until(t.signal, () => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(answerLine(container, "f"), null, "no line for an answer that did not land");
    assert.equal(chipTexts(container, "f").length, 4, "the chips stay");
  }));

test("a waiting session row's held permission request is its chips, and 2 answers deny by its request id", (t) =>
  withApp(t.signal, { rows: [waitingSession] }, async (container, fake) => {
    await openPermissionRequest(t.signal, container, fake);
    assert.deepEqual(chipTexts(container, "s1"), ["1allow", "2deny"]);
    assert.deepEqual(keyCaps(container, "s1"), ["1", "2"]);
    await press("2");
    await until(t.signal, () => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "deny" });
    await until(t.signal, () => answerLine(container, "s1") !== null, "the answer line");
    assert.equal(answerLine(container, "s1"), "You answered 2 · deny");
    assert.deepEqual(chipTexts(container, "s1"), [], "no chips once answered");
  }));

test("a landed session answer's line goes when its row turns working", (t) =>
  withApp(t.signal, { rows: [waitingSession] }, async (container, fake) => {
    await openPermissionRequest(t.signal, container, fake);
    await press("2");
    await until(t.signal, () => answerLine(container, "s1") !== null, "the answer line");
    await mount(container, { rows: [workingSession], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(answerLine(container, "s1"), null, "the line goes with the waiting");
    assert.deepEqual(chipTexts(container, "s1"), [], "a working row shows no chips");
  }));

test("a landed answer's line goes when its row waits on another question, and the new question's chips show", (t) =>
  withApp(t.signal, { rows: [fourOptionRow] }, async (container, fake) => {
    await press("2");
    await until(t.signal, () => answerLine(container, "f") !== null, "the answer line");
    const nextQuestion = JSON.stringify({
      questions: [{ question: "Next?", options: [{ label: "Yes", description: "" }] }],
    });
    await mount(container, {
      rows: [row({ ...fourOptionRow, question: nextQuestion })],
      error: "",
      now,
      clients: fake.clients,
      seen: fake.seen,
    });
    assert.equal(answerLine(container, "f"), null, "the line does not follow the row to its next question");
    assert.deepEqual(chipTexts(container, "f"), ["1Yes"]);
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

test("only the waiting row of the fixture draws chips", (t) =>
  withApp(t.signal, {}, (container) => {
    const drawsChips = ["a", "b", "c", "d"].map((key) => chipTexts(container, key).length > 0);
    assert.deepEqual(drawsChips, [true, false, false, false]);
  }));

// pinnedColumn is the second reader column that | pins beside the list, or null when no row is pinned. The column
// stays mounted while closed, so its composers keep their drafts; a closed column is hidden.
function pinnedColumn(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('[aria-label="pinned reader"]:not([hidden])');
}

// pinnedComposer is the reply box of the open pin: each pinned row keeps its own, and only the open pin's is not hidden.
function pinnedComposer(container: HTMLElement): HTMLTextAreaElement {
  const box = pinnedColumn(container)?.querySelector<HTMLTextAreaElement>(".pin-reply:not([hidden]) .composer textarea");
  assert.ok(box, "the pinned column has a composer");
  return box;
}

// pinnedNote is the working note under the pinned reader, or null when the pinned column draws none.
function pinnedNote(container: HTMLElement): Element | null {
  return pinnedColumn(container)?.querySelector(".work-note") ?? null;
}

test("the pinned head names the pinned row while the cursor rests on another, and no .pin-head is drawn", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(pinnedHead(container)?.querySelector("h1")?.textContent, "api");
    assert.equal(pinnedHead(container)?.querySelector(".where")?.textContent, "api · box");
    assert.equal(readerHead(container)?.querySelector("h1")?.textContent, "docs");
    assert.equal(readerHead(container)?.querySelector(".where")?.textContent, "docs · laptop");
    assert.equal(container.querySelector(".pin-head") === null, true);
  }));

test("the pinned column draws the pill of its own stream and no note while the cursor rests on another pane", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
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
    await until(t.signal, () => pinnedHead(container)?.querySelector(".pill .word")?.textContent === "working", "the pinned pill");
    assert.equal(pinnedHead(container)?.querySelector(".pill .tool")?.textContent, "Bash");
    assert.equal(pinnedHead(container)?.querySelector(".pill .clock")?.textContent, "1:00");
    assert.equal(pinnedNote(container) === null, true, "the pinned reader draws no working note");
    assert.equal(
      noteWords(container.querySelector('section[aria-label="pinned reader"]')),
      false,
      "the pinned reader's text names no note",
    );
    assert.equal(readerNote(container) === null, true, "the cursor's reader on c draws no note");
    assert.equal(readerHead(container)?.querySelector(".pill .tool") === null, true, "c's pill has no tool");
  }));

test("the reader's composer on a working row has the working edge and says it queues", (t) =>
  withApp(t.signal, {}, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    assert.deepEqual(edgesOf(composer(container)), ["edge-working"]);
    assert.equal(composer(container).placeholder, "Queue for web; it lands at Claude's next step");
  }));

test("a working pane turns a waiting row's composer from answering to queueing", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    assert.deepEqual(edgesOf(composer(container)), ["edge-waiting"]);
    assert.equal(composer(container).placeholder, "Answer api");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => composer(container).placeholder.startsWith("Queue for"), "the working placeholder");
    assert.deepEqual(edgesOf(composer(container)), ["edge-working"]);
    assert.equal(composer(container).placeholder, "Queue for api; it lands at Claude's next step");
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
    await until(t.signal, () => composer(container).placeholder.startsWith("Queue for"), "the shown composer's working placeholder");
    assert.deepEqual(edgesOf(composer(container)), ["edge-working"]);
    assert.equal(composer(container).placeholder, "Queue for docs; it lands at Claude's next step");
    const hidden = [...container.querySelectorAll<HTMLTextAreaElement>('[aria-label="reader"] .reply[hidden] textarea')];
    const api = hidden.find((box) => box.placeholder === "Answer api");
    assert.equal(api !== undefined, true, "the api row's hidden composer still answers");
    assert.deepEqual(api === undefined ? [] : edgesOf(api), ["edge-waiting"]);
  }));

test("the pinned composer follows its own stream's pane while the cursor rests on another row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(t.signal, () => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(t.signal, () => pinnedComposer(container).placeholder.startsWith("Queue for"), "the pinned working placeholder");
    assert.deepEqual(edgesOf(pinnedComposer(container)), ["edge-working"]);
    assert.equal(pinnedComposer(container).placeholder, "Queue for api; it lands at Claude's next step");
    assert.deepEqual(edgesOf(composer(container)), ["edge-idle"]);
    assert.equal(composer(container).placeholder, "Reply to docs");
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
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "|");
    assert.ok(pinnedColumn(container), "a | typed in the pinned reply box does not close the pin");
    await pressIn(pinnedComposer(container), "Escape");
    assert.equal(document.activeElement === pinnedComposer(container), false, "Escape leaves the pinned reply box");
    await press("i");
    assert.equal(document.activeElement === composer(container), true, "i focuses the cursor's reply box");
  }));

test("a pinned column's draft survives closing the pin and reopening it on the same row", (t) =>
  withApp(t.signal, {}, async (container, fake) => {
    await press("|");
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await typeInto(pinnedComposer(container), "half a thought");
    await pressIn(pinnedComposer(container), "Escape");
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
    await until(t.signal, () => pinnedColumn(container)?.querySelector('[role="alert"]') !== null, "the failed send's line");
    await pressIn(pinnedComposer(container), "Escape");
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

test("a click on the view labels switches the view as a and p do", (t) =>
  withApp(t.signal, {}, async (container) => {
    const labels = container.querySelectorAll<HTMLElement>(".views .view");
    assert.equal(labels.length, 2);
    await act(() => {
      labels[1].dispatchEvent(clickEvent());
    });
    assert.equal(container.querySelector(".rows")?.getAttribute("data-list"), "project");
    assert.deepEqual(rowKeys(container), ["a", "d", "c", "b"]);
    await act(() => {
      labels[0].dispatchEvent(clickEvent());
    });
    assert.equal(container.querySelector(".rows")?.getAttribute("data-list"), "attention");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

const linkedRows = fixture().map((item) =>
  item.key === "c" ? { ...item, summary: "see https://example.com/notes" } : item,
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
function makeEpic(repository: string, slug: string, init: MessageInitShape<typeof EpicSchema> = {}): Epic {
  return create(EpicSchema, { id: `${repository}/${slug}`, repository, slug, outcome: "", live: true, ...init });
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

function sectionOf(container: HTMLElement, title: string): HTMLElement {
  const section = [...container.querySelectorAll<HTMLElement>(".section")].find(
    (element) => element.querySelector(".b-sec .title")?.textContent === title,
  );
  assert.ok(section, `section ${title} is rendered`);
  return section;
}

test("the Epics group follows Needs you, and an epic reads its outcome and count, or tasks unavailable", (t) => {
  const first = makeEpic(widgetsRepo, "first", { outcome: "build" });
  const second = makeEpic(gadgetsRepo, "second", { outcome: "build" });
  const epics = epicsView([first, second], new Map([[first.id, detailOf(first, ["done", "integrated", "pending"])]]));
  return withApp(t.signal, { epics }, (container) => {
    assert.deepEqual(sectionTitles(container), ["Needs you", "Epics", "New since you looked", "Working", "Seen"]);
    assert.equal(sectionOf(container, "Epics").querySelector(".b-sec i")?.textContent, "2", "the Epics head counts its two epics");
    assert.deepEqual(rowKeys(container), ["a", epicKey(first), epicKey(second), "c", "b", "d"]);

    const known = rowElement(container, epicKey(first));
    assert.ok(known.classList.contains("b-row") && known.classList.contains("epic"));
    assert.equal(known.querySelector(".label")?.textContent, "first");
    assert.equal(known.querySelector(".proj")?.textContent, widgetsRepo);
    assert.equal(known.querySelector(".rt")?.textContent, "build · 2 of 3", "the integrated task counts as done");
    assert.equal(known.querySelector(".count") === null, true, "no count element is drawn");
    assert.equal(known.querySelector("svg.gauge") !== null, true, "the epic draws its gauge");
    assert.equal(known.querySelector("svg.gauge")?.getAttribute("aria-hidden") ?? null, "true");
    assert.equal(known.querySelector("svg.gauge circle.gauge-rest") !== null, true, "the gauge draws its rest circle");
    const dasharray = known.querySelector("svg.gauge circle.gauge-done")?.getAttribute("stroke-dasharray") ?? null;
    assert.equal(dasharray !== null, true, "the gauge draws its done arc");
    const [arc, circle] = (dasharray ?? "").split(/\s+/).map(Number);
    assert.ok(Math.abs(arc / circle - 2 / 3) < 1e-9, "the arc covers two of three tasks");

    const unknown = rowElement(container, epicKey(second));
    assert.equal(unknown.querySelector(".rt")?.textContent, "build · tasks unavailable");
    assert.equal(unknown.querySelector("svg") === null, true, "no gauge is drawn without detail");
  });
});

test("the Epics head counts every live epic, drawn or folded, and a seventh quiet epic folds", (t) => {
  const quiet = ["a", "b", "c", "d", "e", "f", "g"].map((slug) => makeEpic(widgetsRepo, slug));
  const epics = epicsView(quiet, new Map());
  return withApp(t.signal, { epics }, (container) => {
    const section = sectionOf(container, "Epics");
    assert.equal(section.querySelector(".b-sec i")?.textContent, "7", "the head counts the seven live epics");
    assert.equal(section.querySelectorAll(".b-row.epic").length, 6, "six quiet epics draw one line each");
    assert.deepEqual(
      [...section.querySelectorAll(".b-more")].map((line) => line.textContent),
      ["1 folded"],
    );
  });
});

test("accepted, waiting epics fold into one line that opens to their names, and closes again", (t) => {
  const live = makeEpic(widgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const first = makeEpic(widgetsRepo, "queued", { waiting: true });
  const second = makeEpic(gadgetsRepo, "next", { waiting: true });
  const epics = epicsView([live, first, second], new Map());
  return withApp(t.signal, { epics }, async (container) => {
    const section = sectionOf(container, "Epics");
    const labels = () => [...section.querySelectorAll(".label")].map((label) => label.textContent);
    const waitingLine = () => section.querySelector<HTMLElement>(".b-more");
    assert.equal(waitingLine()?.textContent, "2 accepted, waiting to start", "the two waiting epics fold into one line");
    assert.equal(labels().includes("queued"), false, "the line starts closed: the first waiting slug is not drawn");
    assert.equal(labels().includes("next"), false, "the line starts closed: the second waiting slug is not drawn");
    assert.equal(rowElement(container, epicKey(live)).querySelector(".label")?.textContent, "busy", "the live epic shows");

    await act(() => {
      waitingLine()?.dispatchEvent(clickEvent());
    });
    assert.equal(labels().includes("queued"), true, "opened, the line shows the first waiting slug");
    assert.equal(labels().includes("next"), true, "opened, the line shows the second waiting slug");
    assert.equal(rowElement(container, epicKey(first)).querySelector(".proj")?.textContent, widgetsRepo);
    assert.equal(rowElement(container, epicKey(second)).querySelector(".proj")?.textContent, gadgetsRepo);
    assert.equal(rowElement(container, epicKey(live)).querySelector(".label")?.textContent, "busy", "the live epic still shows");

    await act(() => {
      waitingLine()?.dispatchEvent(clickEvent());
    });
    assert.equal(labels().includes("queued"), false, "a second click closes the line: the first slug is gone");
    assert.equal(labels().includes("next"), false, "a second click closes the line: the second slug is gone");
  });
});

test("an epic shows four running steps with the held one marked, then its more, tail and folded lines", (t) => {
  const steps = ["one", "two", "three", "four", "five"].map((name) => makeStep(name, { held: name === "two" }));
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: steps, endedSteps: 3 });
  const folded = [makeEpic(widgetsRepo, "old", { live: false }), makeEpic(gadgetsRepo, "older", { live: false })];
  const epics = epicsView([busy, ...folded], new Map());
  return withApp(t.signal, { epics }, (container) => {
    const section = sectionOf(container, "Epics");
    assert.equal(section.querySelector(".b-sec i")?.textContent, "1", "the head counts the live epic, not the folded ones");
    assert.deepEqual(
      [...section.children].map((child) => child.className),
      [
        "b-sec",
        "b-row epic",
        "b-row step",
        "b-row step held",
        "b-row step",
        "b-row step",
        "b-more",
        "b-more",
        "b-more",
      ],
    );
    const entries = [...section.querySelectorAll(".b-row.step")];
    assert.deepEqual(
      entries.map((entry) => entry.querySelector(".label")?.textContent),
      ["one", "two", "three", "four"],
    );
    assert.deepEqual(
      entries.map((entry) => entry.querySelector(".held")?.textContent ?? null),
      [null, "held", null, null],
    );
    assert.deepEqual(
      [...section.querySelectorAll(".b-more")].map((line) => line.textContent),
      ["1 more", "3 steps ended", "2 folded"],
    );
  });
});

test("a folded epic opens from its line, and its page draws the handover its one Epics.Get answers", (t) => {
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const released = makeEpic(gadgetsRepo, "shipped", { live: false, outcome: "released" });
  // The poll loaded the live epic's detail, so only the released epic is asked for.
  const epics = epicsView([busy, released], new Map([[busy.id, detailOf(busy, [])]]));
  return withApp(t.signal, { rows: [], epics }, async (container, fake) => {
    fake.replies.set(getEpicPath, [
      {
        epic: { id: released.id, repository: gadgetsRepo, slug: "shipped", outcome: "released" },
        ledger: [{ id: "reviewer-one-1", reviewer: "reviewer-one", claim: "the ledger claim", result: "holds" }],
      },
    ]);
    const foldedLine = () =>
      [...sectionOf(container, "Epics").querySelectorAll<HTMLElement>(".b-more")].find(
        (line) => line.textContent === "1 folded",
      );
    const handoverText = () =>
      container.querySelector('section.page[aria-label="epic"] section.handover')?.textContent ?? "";
    assert.ok(foldedLine(), "the Epics group folds the released epic into a line");
    assert.equal(container.querySelector(`[data-key="${epicKey(released)}"]`), null, "the line starts closed");

    await act(() => {
      foldedLine()?.dispatchEvent(clickEvent());
    });
    assert.equal(
      rowElement(container, epicKey(released)).querySelector(".label")?.textContent,
      "shipped",
      "opened, the line shows the released epic",
    );

    await act(() => {
      rowElement(container, epicKey(released)).dispatchEvent(clickEvent());
    });
    await until(t.signal, () => handoverText().includes("the ledger claim"), "the handover the answer holds");
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

test("the folded epics draw in list order after the rows, and keys reach them while the line is open", (t) => {
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: [makeStep("s1")] });
  const older = makeEpic(gadgetsRepo, "older", { live: false });
  const newer = makeEpic(gadgetsRepo, "newer", { live: false });
  const epics = epicsView([busy, older, newer], new Map());
  return withApp(t.signal, { rows: [], epics }, async (container) => {
    const foldedLine = () =>
      [...sectionOf(container, "Epics").querySelectorAll<HTMLElement>(".b-more")].find(
        (line) => line.textContent === "2 folded",
      );
    assert.deepEqual(rowKeys(container), [epicKey(busy), "step:s1"], "closed, the folded epics are not drawn");

    await act(() => {
      foldedLine()?.dispatchEvent(clickEvent());
    });
    assert.deepEqual(
      rowKeys(container),
      [epicKey(busy), "step:s1", epicKey(older), epicKey(newer)],
      "opened, the folded epics draw in list order",
    );
    assert.equal(cursorKey(container), epicKey(busy));
    await pressEach(["j", "j"]);
    assert.equal(cursorKey(container), epicKey(older), "the cursor reaches the first folded epic by keys");
    await press("j");
    assert.equal(cursorKey(container), epicKey(newer), "the cursor reaches the last folded epic by keys");
  });
});

test("an epic the poll loaded draws its page from the poll, and Epics.Get is not asked", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    assert.equal(epicPageDrawn(container), true, "the page draws from the poll's detail");
    assert.equal(fake.callsTo(getEpicPath).length, 0, "no Epics.Get is asked for an epic the poll loaded");
  }));

test("an epic with no outcome reads its count alone, and tasks unavailable alone", (t) => {
  const known = makeEpic(widgetsRepo, "first");
  const unknown = makeEpic(gadgetsRepo, "second");
  const epics = epicsView([known, unknown], new Map([[known.id, detailOf(known, ["done", "integrated", "pending"])]]));
  return withApp(t.signal, { epics }, (container) => {
    assert.equal(rowElement(container, epicKey(known)).querySelector(".rt")?.textContent, "2 of 3");
    assert.equal(rowElement(container, epicKey(unknown)).querySelector(".rt")?.textContent, "tasks unavailable");
  });
});

test("a full epic draws a strip with one segment per record task, in each task's tone", (t) => {
  const slices = [
    { task: "task-0", hue: "done" },
    { task: "task-1", hue: "done" },
    { task: "task-2", hue: "gap", errorAt: 3 },
    { task: "task-3", hue: "dispatched", at: 2 },
    { task: "task-4", hue: "dispatched", at: 1 },
    { task: "task-5", hue: "dispatched", at: 4, held: true },
  ];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")] });
  const statuses = Array.from({ length: 7 }, () => "pending");
  const epics = epicsView([epic], new Map([[epic.id, detailOf(epic, statuses, [], slices)]]));
  return withApp(t.signal, { epics }, (container) => {
    const strip = sectionOf(container, "Epics").querySelector(".strip");
    assert.ok(strip !== null, "the full epic draws its strip");
    assert.equal(strip.className, "strip", "seven segments are not dense");
    assert.deepEqual(
      [...strip.children].map((segment) => segment.className),
      ["k-passed", "k-passed", "k-errored", "k-now", "k-now", "k-held", "k-none"],
    );
  });
});

// fullEpicOf is a live epic with a running step and the given number of record tasks, none of them sliced.
function fullEpicOf(count: number): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")] });
  const statuses = Array.from({ length: count }, () => "pending");
  return epicsView([epic], new Map([[epic.id, detailOf(epic, statuses)]]));
}

test("a full epic of 61 tasks draws a dense strip of 61 segments", (t) =>
  withApp(t.signal, { epics: fullEpicOf(61) }, (container) => {
    const strip = sectionOf(container, "Epics").querySelector(".strip");
    assert.ok(strip !== null, "the full epic draws its strip");
    assert.equal(strip.className, "strip dense");
    assert.equal(strip.children.length, 61);
    assert.equal([...strip.children].every((segment) => segment.className === "k-none"), true);
  }));

test("a full epic of 60 tasks draws a strip that is not dense", (t) =>
  withApp(t.signal, { epics: fullEpicOf(60) }, (container) => {
    const strip = sectionOf(container, "Epics").querySelector(".strip");
    assert.ok(strip !== null, "the full epic draws its strip");
    assert.equal(strip.className, "strip");
    assert.equal(strip.children.length, 60);
  }));

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

test("a running step draws its spinner, its label, its five pips and its doing", (t) => {
  const running = makeStep("s1", { task: "task-0", doing: "writing the test" });
  const slices = [{ task: "task-0", hue: "dispatched", at: 4, redTest: true, working: true, testsPass: true }];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [running] });
  const epics = epicsView([epic], new Map([[epic.id, detailOf(epic, ["pending"], [running], slices)]]));
  return withApp(t.signal, { epics }, (container) => {
    const step = rowElement(container, "step:s1");
    assert.equal(step.className, "b-row step");
    const spinner = step.querySelector(".g");
    assert.equal(spinner?.className, "g g-working");
    assert.equal(spinner?.getAttribute("aria-hidden"), "true");
    assert.equal(step.querySelector(".um") === null, true, "a running step draws no held mark");
    assert.equal(step.querySelector(".label")?.textContent, "task-0");
    assert.deepEqual(
      [...step.querySelectorAll(".pips i")].map((pip) => pip.className),
      ["k-passed", "k-passed", "k-passed", "k-now", "k-track"],
    );
    assert.equal(step.querySelector(".held") === null, true, "a running step reads no held text");
    assert.equal(step.querySelector(".reply")?.textContent, "writing the test");
  });
});

test("a held step draws its mark, and reads held with its reason, or held alone without one", (t) => {
  const withReason = makeStep("s1", { doing: "writing the test", held: true, reason: "waiting on review" });
  const withoutReason = makeStep("s2", { doing: "writing the test", held: true });
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [withReason, withoutReason] });
  return withApp(t.signal, { epics: epicsView([epic], new Map()) }, (container) => {
    const reasoned = rowElement(container, "step:s1");
    assert.equal(reasoned.className, "b-row step held");
    assert.equal(reasoned.querySelector(".um")?.textContent, "‖");
    assert.equal(reasoned.querySelector(".um")?.getAttribute("aria-hidden"), "true");
    assert.equal(reasoned.querySelector(".g") === null, true, "a held step draws no spinner");
    assert.equal(reasoned.querySelector(".held")?.textContent, "held · waiting on review");
    assert.equal(reasoned.querySelector(".reply") === null, true, "a held step shows no doing text");
    assert.equal(rowElement(container, "step:s2").querySelector(".held")?.textContent, "held");
  });
});

test("by project, an epic sits atop its folder, and an epic with no folder gets a section of its own", (t) => {
  const rows = [
    row({ key: "p", project: "/work/orrery", state: "working" }),
    row({ key: "q", project: "/work/api", state: "working" }),
  ];
  const shipping = makeEpic("orrery-5d0c1a7", "ship");
  const stray = makeEpic(gadgetsRepo, "second");
  const epics = epicsView([shipping, stray], new Map());
  return withApp(t.signal, { rows, epics }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["/work/api", "/work/orrery", gadgetsRepo]);
    assert.deepEqual(rowKeys(container), ["q", epicKey(shipping), "p", epicKey(stray)]);
  });
});

test("an unmatched epic with no repository name is titled no directory in project view", (t) => {
  const epics = epicsView([makeEpic("", "loose")], new Map());
  return withApp(t.signal, { rows: [], epics }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["no directory"]);
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

test("w and n skip the epic and step entries", (t) => {
  return withApp(t.signal, { epics: oneEpic() }, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    await press("w");
    assert.equal(cursorKey(container), "a", "w goes to the waiting row");
    await press("n");
    assert.equal(cursorKey(container), "c", "n goes past the epic and its steps to the next unseen row");
    await press("n");
    assert.equal(cursorKey(container), "a", "n wraps to the unseen row a");
  });
});

test("x, Enter, e, i, 1, 2, 3 and | on an epic entry call nothing and pin nothing", (t) => {
  return withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const before = fake.calls.length;
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    await pressEach(["x", "Enter", "e", "i", "1", "2", "3", "|"]);
    await settle();
    assert.deepEqual(
      fake.calls.slice(before).map((call) => call.path),
      [getEpicPath],
      "the only call is the Epics.Get the page asks for, and no key reaches the server",
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
    assert.equal(step.querySelector(".reply")?.textContent, text);
  });
});

test("a click on a step moves the cursor to it, and a click on a link in its text moves nothing", (t) => {
  const doing = "see https://example.com/notes";
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1", { doing })] })], new Map());
  return withApp(t.signal, { epics }, async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const step = rowElement(container, "step:s1");
    const link = step.querySelector("a");
    assert.ok(link, "the step shows its link");
    await act(() => {
      link.dispatchEvent(clickEvent());
    });
    assert.equal(cursorKey(container), "a", "a link click moves nothing");
    await act(() => {
      step.dispatchEvent(clickEvent());
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
function pageEpic(init: MessageInitShape<typeof EpicsGetResponseSchema> = {}, changed = 0n): EpicsView {
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1")], changed });
  return epicsView([epic], new Map([[epic.id, create(EpicsGetResponseSchema, { epic, ...init })]]));
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
      container.querySelector('.panes > section.page[aria-label="epic"]') !== null,
      true,
      "the page sits in the panes beside the list",
    );
    assert.equal(container.querySelector("aside.side") === null, true, "no aside.side remains");
    assert.equal(pageSlug(container), "first", "the page names the epic");
  }));

test("an epic page shows the handover of its verdicts and ledger findings, with their texts", (t) =>
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
      const handover = container.querySelector('section.page[aria-label="epic"] section.handover');
      assert.ok(handover, "the epic page shows the handover section");
      const text = handover.textContent ?? "";
      assert.equal(text.includes("the verdict reason"), true, "the handover shows the verdict's reason");
      assert.equal(text.includes("the ledger claim"), true, "the handover shows the ledger's claim");
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
    assert.equal(container.querySelector("aside.side") === null, true, "no aside.side remains");
  }));

test("a pin splits the panes around a step's reader, and the epic page stays hidden", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await press("g");
    await press("g");
    await press("|");
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

test("an epic with no detail shows epic unavailable in its page, and a step of it shows no page", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    fake.failNext.add(getEpicPath);
    await press("j");
    assert.equal(epicPageShown(container), true);
    assert.equal(epicPageDrawn(container), false, "no epic page is drawn without detail");
    assert.equal(
      container.querySelector('section.page[aria-label="epic"]')?.textContent,
      "epic unavailable",
    );
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(epicPageShown(container), false, "a step shows no page");
  }));

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

test("the summary opens the contract page in place, and the page moves deeper", (t) =>
  withApp(t.signal, { epics: pageEpic({ summary: "Ship the demo", contract: acceptedContract }) }, async (container) => {
    await press("j");
    assert.equal(pageClass(container), "page");
    await clickButton(container, ".epic-page", "Ship the demo");
    assert.equal(epicPageShown(container), true, "the page stays shown");
    assert.equal(contractDrawn(container), true, "the contract page is drawn");
    assert.equal(epicPageDrawn(container), false, "the epic page is not");
    assert.equal(pageClass(container), "page drill", "opening the contract moves the page deeper");
    assert.equal(pageSlug(container), "contract", "the contract is the current page");
  }));

test("back and the slug crumb restore the epic page's scroll place, and the page moves back with leave", (t) =>
  withApp(t.signal, { epics: pageEpic({ summary: "Ship the demo", contract: acceptedContract }) }, async (container) => {
    await press("j");
    moveScroll(container, 120);
    await clickButton(container, ".epic-page", "Ship the demo");
    assert.equal(pageScroll(container), 0, "the contract opens at its top");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "‹");
    assert.equal(epicPageDrawn(container), true, "back returns to the epic page");
    assert.equal(pageClass(container), "page leave", "back moves the page back");
    assert.equal(pageScroll(container), 120, "back restores the scroll place");
    await clickButton(container, ".epic-page", "Ship the demo");
    assert.equal(pageScroll(container), 0, "the contract opens at its top again");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "first");
    assert.equal(epicPageDrawn(container), true, "the slug crumb returns to the epic page");
    assert.equal(pageClass(container), "page leave", "the slug crumb moves the page back");
    assert.equal(pageScroll(container), 120, "the slug crumb restores the scroll place");
  }));

test("the orrery crumb closes the page from the contract page too", (t) =>
  withApp(t.signal, { epics: pageEpic({ summary: "Ship the demo", contract: acceptedContract }) }, async (container) => {
    await press("j");
    await clickButton(container, ".epic-page", "Ship the demo");
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "orrery");
    assert.equal(epicPageShown(container), false, "the orrery crumb closes the page");
  }));

test("back and the orrery crumb close the epic page until the cursor moves", (t) =>
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
    await clickButton(container, 'section.page[aria-label="epic"] .title-row', "orrery");
    assert.equal(epicPageShown(container), false, "the orrery crumb closes the page");
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

test("a click on a proof card opens the task that names its requirement at implementing", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await press("j");
    await clickFirst(container, 'section.page[aria-label="epic"] .proof.red article.proof-card.open');
    assert.equal(phaseOutputShown(container), true, "the card opens the phase output");
    assert.equal(phaseOpenName(container), "implementing", "a red card opens implementing");
    assert.equal(pageSlug(container), "T1 · implementing");
    assert.equal(pageClass(container), "page drill");
  }));

test("a click on a Now row opens its task at the phase it is in", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container) => {
    await press("j");
    await clickFirst(container, 'section.page[aria-label="epic"] .happen.now li.open');
    assert.equal(phaseOutputShown(container), true, "the row opens the phase output");
    assert.equal(phaseOpenName(container), "checking", "the row opens the phase it is in");
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

test("the orrery crumb closes the page from the session, and the session's stream stops", (t) =>
  withApp(t.signal, { epics: phaseEpic() }, async (container, fake) => {
    await openCheckingPhase(container);
    await clickButton(container, phaseBody, "Open the session ›");
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    await clickButton(container, pageTitleRow, "orrery");
    assert.equal(epicPageShown(container), false, "the orrery crumb closes the page");
    assert.equal(fake.callsTo(sessionWatchPath)[0].signal.aborted, true, "the closed page's stream stops");
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
    const box = container.querySelector<HTMLTextAreaElement>('section.page[aria-label="epic"] .stepbox textarea');
    assert.ok(box, "the session's step box has an input");
    await typeInto(box, "try the other route");
    await clickFirst(container, 'section.page[aria-label="epic"] .stepbox button[aria-label="send"]');
    await until(t.signal, () => fake.callsTo(nudgePath).length === 1, "the nudge");
    assert.deepEqual(fake.callsTo(nudgePath).map((call) => [call.body.id, call.body.text]), [
      ["run1", "try the other route"],
    ]);
  }));

test("Pause sends Epics.Pause with the epic's id", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => fake.callsTo(pauseEpicPath).length === 1, "the Pause call");
    assert.equal(fake.callsTo(pauseEpicPath)[0].body.id, widgetsFirst);
  }));

test("Resume sends Epics.Resume with the epic's id on a paused epic", (t) =>
  withApp(t.signal, { epics: pageEpic({ paused: true }) }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Resume");
    await until(t.signal, () => fake.callsTo(resumeEpicPath).length === 1, "the Resume call");
    assert.equal(fake.callsTo(resumeEpicPath)[0].body.id, widgetsFirst);
  }));

test("Stop asks first: Keep it running sends nothing, and the confirm sends Epics.Stop", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Stop…");
    await clickButton(container, ".epic-page .title-row .acts", "Keep it running");
    await clickButton(container, ".epic-page .title-row .acts", "Stop…");
    await clickButton(container, ".epic-page .title-row .acts", "Stop first");
    await until(t.signal, () => fake.callsTo(stopEpicPath).length >= 1, "the Stop call");
    assert.equal(fake.callsTo(stopEpicPath).length, 1, "only the confirm sends: Keep it running sent nothing");
    assert.equal(fake.callsTo(stopEpicPath)[0].body.id, widgetsFirst);
  }));

test("Retry on a parked epic sends Epics.Retry with the epic's id", (t) =>
  withApp(t.signal, { epics: pageEpic({ parked: "rate limit" }) }, async (container, fake) => {
    await press("j");
    await clickButton(container, ".now-block", "Retry");
    await until(t.signal, () => fake.callsTo(retryEpicPath).length === 1, "the Retry call");
    assert.equal(fake.callsTo(retryEpicPath)[0].body.id, widgetsFirst);
  }));

test("Answer sends Epics.Answer with the epic, a fresh decision id, the trimmed text and the question's id", (t) =>
  withApp(t.signal, { epics: pageEpic({ questions: [{ id: "Q1", text: "Which database?" }] }) }, async (container, fake) => {
    await press("j");
    const box = container.querySelector<HTMLTextAreaElement>('.now-block textarea[aria-label="answer"]');
    assert.ok(box, "the open question shows its answer box");
    await typeInto(box, " postgres ");
    await submitIn(container, ".now-block form.answer");
    await until(t.signal, () => fake.callsTo(answerEpicPath).length === 1, "the Answer call");
    const body = fake.callsTo(answerEpicPath)[0].body;
    assert.equal(body.epicId, widgetsFirst);
    assert.equal(body.text, "postgres", "the answer is sent trimmed");
    assert.equal(body.question, "Q1");
    assert.equal(typeof body.id === "string" && body.id !== "", true, "the decision has an id");
    assert.equal(body.id !== widgetsFirst && body.id !== "Q1", true, "the decision id is its own, not the epic's or the question's");
  }));

test("a refused answer shows the server's message under the answer form", (t) =>
  withApp(t.signal, { epics: pageEpic({ questions: [{ id: "Q1", text: "Which database?" }] }) }, async (container, fake) => {
    fake.refuseNext.set(answerEpicPath, "question is closed");
    await press("j");
    const box = container.querySelector<HTMLTextAreaElement>('.now-block textarea[aria-label="answer"]');
    assert.ok(box, "the open question shows its answer box");
    await typeInto(box, "postgres");
    await submitIn(container, ".now-block form.answer");
    await until(t.signal, 
      () => alertTexts(container).some((text) => text.includes("question is closed")),
      "the refusal line",
    );
  }));

test("keys typed in the answer box are text: j and p do nothing there, and Escape leaves it", (t) =>
  withApp(t.signal, { epics: pageEpic({ questions: [{ id: "Q1", text: "Which database?" }] }) }, async (container) => {
    await press("j");
    const box = container.querySelector<HTMLTextAreaElement>('.now-block textarea[aria-label="answer"]');
    assert.ok(box, "the open question shows its answer box");
    const viewOn = () => container.querySelector(".views .view.on")?.textContent ?? null;
    const cursor = cursorKey(container);
    assert.equal(viewOn(), "aBy attention", "the view starts by attention");
    await act(() => {
      box.focus();
    });
    assert.equal(document.activeElement === box, true);
    const jEvent = await pressIn(box, "j");
    assert.equal(cursorKey(container), cursor, "j in the answer box moves no cursor");
    assert.equal(epicPageShown(container), true, "j in the answer box leaves the epic page open");
    assert.equal(jEvent.defaultPrevented, false, "j in the answer box is text, not a handled key");
    await pressIn(box, "p");
    assert.equal(viewOn(), "aBy attention", "p in the answer box leaves the view");
    assert.equal(document.activeElement === box, true, "the answer box keeps focus through list keys");
    await pressIn(box, "Escape");
    assert.equal(document.activeElement === box, false, "Escape blurs the answer box");
  }));

test("a command that fails shows a fixed line, and one that lands clears it", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    fake.failNext.add(pauseEpicPath);
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => alertTexts(container).includes(commandFailedText), "the fixed line");
    await clickButton(container, ".epic-page .title-row .acts", "Pause");
    await until(t.signal, () => alertTexts(container).length === 0, "the line to clear");
  }));

test("Just happened shows the epic's history, read from its first entry", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
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
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
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
  withApp(t.signal, { epics: pageEpic({}, 1n) }, async (container, fake) => {
    fake.replies.set(historyPath, [
      { entries: [{ cursor: "c1", decision: { id: "d1", text: "before the change" } }], next: "" },
      { entries: [{ cursor: "c2", decision: { id: "d2", text: "after the change" } }], next: "" },
    ]);
    await press("j");
    await until(t.signal, () => historyText(container).includes("before the change"), "the first history");
    await mount(container, {
      rows: fixture(),
      error: "",
      now,
      clients: fake.clients,
      seen: fake.seen,
      epics: pageEpic({}, 2n),
    });
    await until(t.signal, () => historyText(container).includes("after the change"), "the reloaded history");
    assert.equal(fake.callsTo(historyPath).length, 2);
  }));

test("a history that fails draws no history and no error line, and the page stays open", (t) =>
  withApp(t.signal, { epics: pageEpic() }, async (container, fake) => {
    fake.failNext.add(historyPath);
    await press("j");
    await until(t.signal, () => fake.callsTo(historyPath).length === 1, "the History call");
    await act(async () => {});
    assert.equal(historyText(container).includes("decision"), false, "no entries are drawn");
    assert.deepEqual(alertTexts(container), [], "the failed read shows no error line");
    assert.equal(epicPageShown(container), true, "the epic page stays open");
  }));

// onStepOne moves the cursor from row a down to the first step of oneEpic, s1, and waits for its stream to start.
async function onStepOne(signal: AbortSignal, container: HTMLElement, fake: FakeServer): Promise<void> {
  await press("j");
  await press("j");
  assert.equal(cursorKey(container), "step:s1");
  await until(signal, () => fake.callsTo(sessionWatchPath).length === 1, "the step stream");
}

// stepButton is the button of the cursor's step box with the given label: Interrupt and ⋯ on the line, and Stop,
// Confirm stop or Cancel in the menu ⋯ opens.
function stepButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = [...container.querySelectorAll<HTMLButtonElement>(".reply:not([hidden]) .stepbox button")].find(
    (element) => element.textContent === label,
  );
  assert.ok(button, `the step box has a ${label} button`);
  return button;
}

// cursorStepInput is the textarea of the cursor's step box.
function cursorStepInput(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>(".reply:not([hidden]) .stepbox textarea");
  assert.ok(box, "the cursor's step box has an input");
  return box;
}

// stepInputNamed is the textarea of the step box for the step with the given label, shown or hidden.
function stepInputNamed(container: HTMLElement, label: string): HTMLTextAreaElement {
  const box = [...container.querySelectorAll<HTMLTextAreaElement>('[aria-label="reader"] .stepbox textarea')].find(
    (element) => element.placeholder.startsWith(`Nudge ${label}:`),
  );
  assert.ok(box, `the step box for ${label} has an input`);
  return box;
}

// sendButton is the send button of the cursor's step box.
function sendButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>('.reply:not([hidden]) .stepbox button[aria-label="send"]');
  assert.ok(button, "the step box has a send button");
  return button;
}

// stepButtonAbsent reports that the cursor's step box has no button with the given label.
function stepButtonAbsent(container: HTMLElement, label: string): boolean {
  return [...container.querySelectorAll<HTMLButtonElement>(".reply:not([hidden]) .stepbox button")].every(
    (element) => element.textContent !== label,
  );
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
    await clickIn(sendButton(container));
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 0, "a blank box sends nothing");
    await typeInto(composer(container), "try the other route");
    await clickIn(sendButton(container));
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
    await clickIn(sendButton(container));
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

test("Stop sits behind ⋯; Cancel sends nothing, and Confirm stop calls Steps.Stop with the step's id", (t) =>
  withApp(t.signal, { epics: oneEpic() }, async (container, fake) => {
    await onStepOne(t.signal, container, fake);
    assert.equal(stepButtonAbsent(container, "Stop"), true, "no Stop until ⋯ is pressed");
    await clickIn(stepButton(container, "⋯"));
    await clickIn(stepButton(container, "Stop"));
    assert.equal(fake.callsTo(stopStepPath).length, 0, "Stop alone sends nothing");
    await clickIn(stepButton(container, "Cancel"));
    await settle();
    assert.equal(fake.callsTo(stopStepPath).length, 0, "Cancel sends nothing");
    assert.equal(stepButtonAbsent(container, "Stop"), true, "Cancel closes the menu");
    await clickIn(stepButton(container, "⋯"));
    await clickIn(stepButton(container, "Stop"));
    await clickIn(stepButton(container, "Confirm stop"));
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
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, epics: withoutSteps });
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, epics: oneEpic() });
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
    assert.equal(container.querySelector('[aria-label="keys"]') === null, true);
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
    await clickIn(stepButton(container, "⋯"));
    await clickIn(stepButton(container, "Stop"));
    await clickIn(stepButton(container, "Confirm stop"));
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

// The repository Talk starts sessions in, and the session Talk lists for the epic.
const talkRepository = "/work/widgets";
const talkSession = { id: "sess-1", pane: "", label: "auth work", call: "epic_get", at: 1000n };
const talkFirstPrompt = `This session is about orrery epic ${widgetsFirst}. Read it with epic_get, then wait for my words.`;

// titleCommands are the labels of the epic page's title-row commands, in the order they show.
function titleCommands(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".epic-page .title-row .acts button")].map(
    (button) => button.textContent ?? "",
  );
}

// talkItems are the labels of the buttons Talk's list shows, in order: a session's line, then the new session.
function talkItems(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".talk button")].map((button) => button.textContent ?? "");
}

// dockShown is whether the epic section holds the docked conversation as a child of its own.
function dockShown(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"] > aside.dock[aria-label="talk"]') !== null;
}

// dockLabel is the name the docked conversation's head shows, or "" when no conversation is docked.
function dockLabel(container: HTMLElement): string {
  return (
    container.querySelector('section.page[aria-label="epic"] > aside.dock[aria-label="talk"] h1')?.textContent ?? ""
  );
}

// pageDocked is whether the epic section carries the class docked, which lays the dock beside the epic page.
function pageDocked(container: HTMLElement): boolean {
  return container.querySelector('section.page[aria-label="epic"].docked') !== null;
}

// chooseTalkLine clicks the line Talk's list shows for the session with the label. A line reads as its label, its call
// and its clock.
async function chooseTalkLine(container: HTMLElement, label: string): Promise<void> {
  const line = [...container.querySelectorAll<HTMLButtonElement>(".talk button")].find((button) =>
    (button.textContent ?? "").startsWith(`${label} · `),
  );
  assert.ok(line, `Talk lists a line for ${label}`);
  await act(() => {
    line.dispatchEvent(new Event("click"));
  });
}

// closeDock clicks the × of the docked conversation.
async function closeDock(container: HTMLElement): Promise<void> {
  const close = container.querySelector<HTMLButtonElement>('aside.dock[aria-label="talk"] button[aria-label="close"]');
  assert.ok(close, "the dock has its × button");
  await act(() => {
    close.dispatchEvent(new Event("click"));
  });
}

test("Talk is the first command in the epic page's title row", (t) =>
  withApp(t.signal, { epics: pageEpic({ repository: talkRepository }) }, async (container) => {
    await press("j");
    assert.equal(titleCommands(container)[0], "Talk", "Talk leads the title row's commands");
  }));

test("choosing a session in Talk docks its conversation beside the epic page, and × closes it", (t) =>
  withApp(t.signal, { epics: pageEpic({ repository: talkRepository, sessions: [talkSession] }) }, async (container, fake) => {
    await press("j");
    assert.equal(dockShown(container), false, "no conversation is docked before a choice");
    await clickButton(container, ".epic-page .title-row .acts", "Talk");
    await chooseTalkLine(container, "auth work");
    assert.equal(dockShown(container), true, "the session's conversation docks beside the epic page");
    assert.equal(dockLabel(container), "auth work", "the dock is named for the session");
    assert.equal(pageDocked(container), true, "the epic section is docked");
    await until(t.signal, 
      () => fake.callsTo(sessionWatchPath).some((call) => call.body.id === "sess-1"),
      "the docked session's stream",
    );
    await closeDock(container);
    assert.equal(dockShown(container), false, "× removes the dock");
    assert.equal(pageDocked(container), false, "× removes the docked class");
    assert.equal(fake.callsTo(sessionWatchPath)[0].signal.aborted, true, "× stops the session's stream");
  }));

test("New session in the repository starts a session there with the first prompt, and docks it", (t) =>
  withApp(t.signal, { epics: pageEpic({ repository: talkRepository }) }, async (container, fake) => {
    fake.replies.set(startSessionPath, [{ id: "sess-new" }]);
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Talk");
    await clickButton(container, ".talk", "New session in widgets");
    await until(t.signal, () => fake.callsTo(startSessionPath).length === 1, "the Control.Start call");
    const body = fake.callsTo(startSessionPath)[0].body;
    assert.equal(body.dir, talkRepository, "the session starts in the epic's repository");
    assert.equal(body.prompt, talkFirstPrompt, "the session's first prompt names the epic");
    await until(t.signal, () => dockShown(container), "the docked session");
    assert.equal(dockLabel(container), "widgets", "the dock is named for the repository");
    await until(t.signal, 
      () => fake.callsTo(sessionWatchPath).some((call) => call.body.id === "sess-new"),
      "the new session's stream",
    );
  }));

test("an epic with no repository lists its sessions in Talk and offers no new session", (t) =>
  withApp(t.signal, { epics: pageEpic({ sessions: [talkSession] }) }, async (container) => {
    await press("j");
    await clickButton(container, ".epic-page .title-row .acts", "Talk");
    assert.equal(
      talkItems(container).some((item) => item.startsWith("auth work · ")),
      true,
      "the session is listed",
    );
    assert.equal(
      talkItems(container).some((item) => item.startsWith("New session in")),
      false,
      "no new session is offered",
    );
  }));

test("showing another epic closes the docked conversation, and the first epic's dock does not come back", (t) => {
  const first = makeEpic(widgetsRepo, "first");
  const second = makeEpic(gadgetsRepo, "second");
  const epics = epicsView(
    [first, second],
    new Map([
      [
        first.id,
        create(EpicsGetResponseSchema, { epic: first, repository: talkRepository, sessions: [talkSession] }),
      ],
      [second.id, detailOf(second, [])],
    ]),
  );
  return withApp(t.signal, { rows: [], epics }, async (container) => {
    await press("g");
    await press("g");
    assert.equal(cursorKey(container), epicKey(first), "the cursor rests on the first epic");
    await clickButton(container, ".epic-page .title-row .acts", "Talk");
    await chooseTalkLine(container, "auth work");
    assert.equal(dockShown(container), true, "the conversation docks on the first epic");
    await press("G");
    assert.equal(cursorKey(container), epicKey(second), "the cursor rests on the second epic");
    assert.equal(dockShown(container), false, "showing the second epic closes the dock");
    await press("g");
    await press("g");
    assert.equal(cursorKey(container), epicKey(first), "the cursor returns to the first epic");
    assert.equal(dockShown(container), false, "the first epic's dock stays closed");
  });
});

