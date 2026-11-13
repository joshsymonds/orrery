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
  EpicSchema,
  EpicSliceSchema,
  type EpicStep,
  EpicStepSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import type { Row } from "./rows";
import type { SeenClock, SeenWindow } from "./seen";
import { installTestDom, removeTestDom } from "./testdom";

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

type Clients = ReturnType<typeof createClients>;

type Seen = ReturnType<typeof fakeSeen>;

interface Props {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seen: Seen;
  epics?: EpicsView;
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
  // seen is the page the seen watch reads: the test emits its input, sets its focus, and advances its clock.
  readonly seen = fakeSeen();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    if (!path.endsWith("/WatchConversation")) {
      this.calls.push({ path, body: decodeUnary(bytes), signal: request.signal, controller: null });
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
      return json(path === pullPath ? { text: queuedText } : {});
    }
    const call: Call = { path, body: decodeEnvelope(bytes), signal: request.signal, controller: null };
    this.calls.push(call);
    request.signal.addEventListener("abort", () => {
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

const keysHelpList = ["j", "k", "↓", "↑", "gg", "G", "a", "p", "w", "n", "x", "Enter", "e", "/", "i", "1", "2", "3", "o", "|", "?", "Esc"];

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
      />,
      container,
    );
  });
}

async function withApp(
  props: Partial<Omit<Props, "clients" | "seen">>,
  check: (container: HTMLElement, fake: FakeServer) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeServer();
  try {
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, ...props });
    await check(container, fake);
  } finally {
    await act(() => {
      render(null, container);
    });
    fake.closeStreams();
    container.remove();
    await removeTestDom(window);
  }
}

async function until(check: () => boolean, what: string): Promise<void> {
  const deadline = Date.now() + 2000;
  while (!check()) {
    if (Date.now() > deadline) {
      throw new Error(`timed out waiting for ${what}`);
    }
    await act(() => new Promise((resolve) => setTimeout(resolve, 2)));
  }
}

// settle lets pending promises run, so a write the test expects to be absent has time to start.
async function settle(): Promise<void> {
  await act(() => new Promise((resolve) => setTimeout(resolve, 10)));
}

async function pressIn(element: Element, key: string): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
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

function readerText(container: HTMLElement): string {
  return container.querySelector(".reader")?.textContent ?? "";
}

// readerWorkLine is the working line under the cursor's reader, or null when that column draws none.
function readerWorkLine(container: HTMLElement): Element | null {
  return container.querySelector('[aria-label="reader"] .work-line');
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
  return [...container.querySelectorAll(".b-sec")].map((element) => element.textContent);
}

function rowElement(container: HTMLElement, key: string): Element {
  const element = container.querySelector(`[data-row][data-key="${key}"]`);
  assert.ok(element, `row ${key} is rendered`);
  return element;
}

test("j and ArrowDown move the cursor one row down and clamp at the last row", () =>
  withApp({}, async (container) => {
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

test("k and ArrowUp move the cursor one row up and clamp at the first row", () =>
  withApp({}, async (container) => {
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

test("gg goes to the first row, and a lone g or a g broken by another key does not", () =>
  withApp({}, async (container) => {
    await press("G");
    await press("g");
    assert.equal(cursorKey(container), "d", "one g moved the cursor");
    await press("k");
    await press("g");
    assert.equal(cursorKey(container), "b", "g after another key moved the cursor");
    await press("g");
    assert.equal(cursorKey(container), "a");
  }));

test("G goes to the last row", () =>
  withApp({}, async (container) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
  }));

test("a shows rows by attention and keeps the cursor's row", () =>
  withApp({}, async (container) => {
    await press("p");
    await press("j");
    assert.equal(cursorKey(container), "d");
    await press("a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(cursorKey(container), "d");
    assert.deepEqual(sectionTitles(container), ["Needs you", "New since you looked", "Working", "Seen"]);
  }));

test("p shows rows by project and keeps the cursor's row", () =>
  withApp({}, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), "c");
    await press("p");
    assert.deepEqual(rowKeys(container), ["a", "d", "c", "b"]);
    assert.equal(cursorKey(container), "c");
    assert.deepEqual(sectionTitles(container), ["/work/api", "/work/docs", "/work/web"]);
  }));

test("w goes to the first waiting row and does nothing when no row waits", () =>
  withApp({}, async (container, fake) => {
    await press("G");
    assert.equal(cursorKey(container), "d");
    await press("w");
    assert.equal(cursorKey(container), "a");

    await mount(container, { rows: fixture().slice(1), error: "", now, clients: fake.clients, seen: fake.seen });
    await press("G");
    await press("w");
    assert.equal(cursorKey(container), "d");
  }));

test("n goes to the next unseen row after the cursor and wraps", () =>
  withApp({}, async (container) => {
    assert.equal(cursorKey(container), "a");
    await press("n");
    assert.equal(cursorKey(container), "c");
    await press("n");
    assert.equal(cursorKey(container), "a");
  }));

test("/ focuses the filter input and does not type the slash", () =>
  withApp({}, async (container) => {
    const event = await press("/");
    assert.equal(event.defaultPrevented, true, "the slash was left to the input");
    assert.equal(document.activeElement, filterInput(container));
    assert.equal(filterInput(container).value, "");
  }));

test("? toggles a help panel listing exactly the keys", () =>
  withApp({}, async (container) => {
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    await press("?");
    const help = container.querySelector('[aria-label="keys"]');
    assert.ok(help, "help panel is open");
    const listed = [...help.querySelectorAll("kbd")].map((element) => element.textContent);
    assert.deepEqual(listed, keysHelpList);
    await press("?");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
  }));

test("Esc closes the help when the filter does not have focus", () =>
  withApp({}, async (container) => {
    await press("?");
    await press("Escape");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
  }));

test("with the filter focused, shortcut keys do nothing and Enter blurs it", () =>
  withApp({}, async (container, fake) => {
    await press("/");
    await press("j");
    await press("G");
    await press("?");
    await press("x");
    await press("e");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "x and e in the filter write no seen");
    await press("Enter");
    assert.notEqual(document.activeElement, filterInput(container));
    assert.equal(fake.callsTo(markPanePath).length, 0, "Enter in the filter writes no seen");
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("Esc in the filter clears it and blurs it", () =>
  withApp({}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(rowKeys(container), ["c"]);
    await press("Escape");
    assert.equal(filterInput(container).value, "");
    assert.notEqual(document.activeElement, filterInput(container));
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
  }));

test("the filter keeps rows whose fields contain every typed word, case-insensitively", () =>
  withApp({}, async (container) => {
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

test("the filter hides sections left empty", () =>
  withApp({}, async (container) => {
    await press("/");
    await typeFilter(container, "docs");
    assert.deepEqual(sectionTitles(container), ["New since you looked"]);
  }));

test("modifier keys leave the cursor alone", () =>
  withApp({}, async (container) => {
    await press("j", { ctrlKey: true });
    await press("j", { metaKey: true });
    await press("j", { altKey: true });
    await press("G", { ctrlKey: true });
    assert.equal(cursorKey(container), "a");
  }));

test("u and N outside the filter change nothing", () =>
  withApp({}, async (container, fake) => {
    await press("u");
    await press("N");
    assert.equal(cursorKey(container), "a");
    assert.deepEqual(rowKeys(container), ["a", "c", "b", "d"]);
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    assert.equal(filterInput(container).value, "");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "no seen was written");
  }));

test("the cursor keeps its row across new rows and clamps when its row leaves", () =>
  withApp({}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");

    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "b");

    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("an unmoved cursor keeps its row when a row sorts in above it", () =>
  withApp({}, async (container, fake) => {
    assert.equal(cursorKey(container), "a");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture(), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "a");
  }));

test("a clamped cursor keeps the row it moved to", () =>
  withApp({}, async (container, fake) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "b");
    await mount(container, { rows: fixture().filter((item) => item.key !== "b"), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
    const added = row({ key: "e", label: "new", state: "waiting", changed: 900, seen: 1, unseen: true });
    await mount(container, { rows: [...fixture().filter((item) => item.key !== "b"), added], error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(cursorKey(container), "d");
  }));

test("only unseen rows carry the unseen mark", () =>
  withApp({}, (container) => {
    const marked = [...container.querySelectorAll("[data-unseen]")].map((element) => element.getAttribute("data-key"));
    assert.deepEqual(marked, ["a", "c"]);
    assert.equal(rowElement(container, "b").hasAttribute("data-unseen"), false);
    assert.equal(rowElement(container, "d").hasAttribute("data-unseen"), false);
  }));

test("a row shows its state, label, project's last path element, age, and reply text", () =>
  withApp({}, (container) => {
    const element = rowElement(container, "a");
    assert.equal(element.querySelector(".label")?.textContent, "api");
    assert.equal(element.querySelector(".proj")?.textContent, "api");
    assert.equal(element.querySelector(".age")?.textContent, "11m");
    assert.ok(element.textContent?.includes("waiting"));
    assert.equal(element.querySelector(".l2")?.textContent, "Ship it?", "the question replaces the summary");
  }));

test("a row that is not waiting shows its summary, not its question", () =>
  withApp(
    {
      rows: [row({ key: "s", state: "working", question: "stale?", summary: "compiling" })],
    },
    (container) => {
      assert.equal(rowElement(container, "s").querySelector(".l2")?.textContent, "compiling", "only a waiting row shows its question");
    },
  ));

test("reply text renders as text, never as markup", () =>
  withApp(
    {
      rows: [row({ key: "x", state: "working", summary: "<script>alert(1)</script> and <img src=x onerror=alert(1)>" })],
    },
    (container) => {
      assert.equal(container.querySelector("script"), null);
      assert.equal(container.querySelector("img"), null);
      assert.equal(
        rowElement(container, "x").querySelector(".l2")?.textContent,
        "<script>alert(1)</script> and <img src=x onerror=alert(1)>",
      );
    },
  ));

test("an empty project title shows the row's host", () =>
  withApp({ rows: [row({ key: "p", project: "", host: "box", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["box"]);
  }));

test("an empty project title with no host shows no directory", () =>
  withApp({ rows: [row({ key: "p", project: "", host: "", state: "working" })] }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["no directory"]);
  }));

test("a non-empty error shows one line above the list", () =>
  withApp({ error: "list unavailable" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]')?.textContent, "list unavailable");
  }));

test("no error line shows when the error is empty", () =>
  withApp({ error: "" }, (container) => {
    assert.equal(container.querySelector('[role="alert"]'), null);
  }));

test("the cursor's pane row opens its conversation, and the reader shows the streamed reply as text", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    assert.equal(stream.body.pane, "a");
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed reply" }],
    });
    await until(() => readerText(container).includes("the streamed reply"), "the streamed reply");
  }));

test("a working pane's delta draws its line under the cursor's reader, with the tool, its time, and where a message lands", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(() => readerWorkLine(container) !== null, "the working line");
    const line = readerWorkLine(container);
    assert.ok(line);
    assert.equal(line.querySelector(".work-label")?.textContent, "working");
    assert.equal(line.querySelector(".work-tool")?.textContent, "running a command");
    assert.equal(line.querySelector(".work-time")?.textContent, "1:00");
    assert.equal(
      line.querySelector(".work-note")?.textContent,
      "a message sent now lands at Claude's next step · last turn ended 5m ago",
    );
    assert.equal(line.previousElementSibling?.classList.contains("reader"), true, "the line is under the reader");
    assert.equal(line.nextElementSibling?.classList.contains("reply"), true, "the line is above the reply boxes");
  }));

test("a later delta whose pane is done removes the working line", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, { transcript: "t1", seq: "1", reset: true, items: [], state: { pane: workingPane("Bash") } });
    await until(() => readerWorkLine(container) !== null, "the working line");
    fake.emit(stream, {
      transcript: "t1",
      seq: "2",
      items: [],
      state: { pane: { id: "a", state: "done", tool: "", changed: "1000", lastTurnEnd: "1000" } },
    });
    await until(() => readerWorkLine(container) === null, "the working line removed");
  }));

test("a step_wait tool draws a background line that reads as waiting on background work", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { pane: workingPane("mcp__orrery__step_wait") },
    });
    await until(() => readerWorkLine(container) !== null, "the working line");
    const line = readerWorkLine(container);
    assert.ok(line);
    assert.equal(line.classList.contains("background"), true);
    assert.equal(line.querySelector(".work-label")?.textContent, "background");
    assert.equal(line.querySelector(".work-tool")?.textContent, "waiting on background work");
  }));

test("moving the cursor to a session row aborts the pane's stream and opens the session's", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [pane] = fake.callsTo(paneWatchPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(pane.signal.aborted, true, "the pane stream was aborted");
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    assert.equal(fake.callsTo(sessionWatchPath)[0].body.id, "s1");
  }));

test("leaving the page stops the cursor's stream", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    const [stream] = fake.callsTo(paneWatchPath);
    await act(() => {
      render(null, container);
    });
    assert.equal(stream.signal.aborted, true, "the stream was aborted on unmount");
  }));

test("Enter in the composer sends the text to the pane with Panes.SendText", () =>
  withApp({}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement, box);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(() => fake.callsTo(sendTextPath).length === 1, "the send");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    await until(() => box.value === "", "the sent draft cleared");
  }));

test("Enter in a session's composer sends the text with Control.Say and a prompt id", () =>
  withApp({ rows: [sessionRow] }, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(() => fake.callsTo(sayPath).length === 1, "the say");
    const [say] = fake.callsTo(sayPath);
    assert.equal(say.body.id, "s1");
    assert.equal(say.body.text, "keep going");
    assert.match(String(say.body.promptId), /^[0-9a-f-]{36}$/);
  }));

test("a draft survives moving the cursor away and back", () =>
  withApp({}, async (container) => {
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

test("a failed send keeps its prompt id when the cursor moves away and back", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
    fake.failNext.add(sayPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "Enter");
    await until(() => container.querySelector('[role="alert"]') !== null, "the failed send's line");
    await pressIn(box, "Escape");
    await press("k");
    assert.equal(cursorKey(container), "a");
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(composer(container).value, "keep going", "the draft survived");
    await pressIn(composer(container), "Enter");
    await until(() => fake.callsTo(sayPath).length === 2, "the second say");
    const [first, second] = fake.callsTo(sayPath);
    assert.equal(second.body.text, "keep going");
    assert.equal(second.body.promptId, first.body.promptId, "the retry reused the prompt id");
  }));

test("a failed send keeps its draft and prompt id through a poll that lists no sessions", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
    fake.failNext.add(sayPath);
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await until(() => container.querySelector('[role="alert"]') !== null, "the failed send's line");
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
    await until(() => fake.callsTo(sayPath).length === 2, "the second say");
    const [first, second] = fake.callsTo(sayPath);
    assert.equal(second.body.promptId, first.body.promptId, "the retry reused the prompt id");
  }));

test("keys typed in the composer never act as list keys, and Escape leaves it", () =>
  withApp({}, async (container) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "j");
    await pressIn(box, "G");
    await pressIn(box, "?");
    await pressIn(box, "/");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    assert.notEqual(document.activeElement, filterInput(container));
    assert.equal(document.activeElement, box);
    await pressIn(box, "Escape");
    assert.notEqual(document.activeElement, box);
    await press("j");
    assert.equal(cursorKey(container), "c");
  }));

test("i focuses the composer, and o opens and closes a compaction's summary", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "compaction", text: "the summary text", trigger: "auto", tokens: "611000" }],
    });
    await until(() => container.querySelector(".compaction") !== null, "the compaction");
    assert.equal(container.querySelector(".summary"), null, "the summary is hidden at first");

    await press("o");
    assert.equal(container.querySelector(".summary")?.textContent, "the summary text");
    await press("o");
    assert.equal(container.querySelector(".summary"), null);

    await press("i");
    assert.equal(document.activeElement, composer(container));
  }));

test("ArrowUp in an empty pane composer pulls the queued text back", () =>
  withApp({}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "ArrowUp");
    await until(() => box.value === queuedText, "the pulled text");
    assert.equal(fake.callsTo(pullPath).length, 1);
  }));

test("ArrowUp in a session's composer pulls nothing", () =>
  withApp({ rows: [sessionRow] }, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await pressIn(box, "ArrowUp");
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    assert.equal(fake.callsTo(pullPath).length, 0, "a session has no queue to pull");
    assert.equal(box.value, "");
  }));

test("Enter on the list writes Panes.MarkSeen for the cursor's pane row", () =>
  withApp({}, async (container, fake) => {
    await press("Enter");
    await until(() => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    assert.equal(cursorKey(container), "a");
  }));

test("x on the list writes Panes.MarkSeen for a pane row and Control.Seen for a session row", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
    await press("x");
    await until(() => fake.callsTo(markPanePath).length === 1, "the pane write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    await press("j");
    assert.equal(cursorKey(container), "s1");
    await press("x");
    await until(() => fake.callsTo(markSessionPath).length === 1, "the session write");
    assert.deepEqual(fake.callsTo(markSessionPath)[0].body, { id: "s1" });
  }));

test("e writes seen for the cursor's row, then rests on the next unseen row", () =>
  withApp({}, async (container, fake) => {
    assert.equal(cursorKey(container), "a");
    await press("e");
    await until(() => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    assert.equal(cursorKey(container), "c");
  }));

test("Enter in a composer sends its text and writes no seen, even for keys typed as shortcuts", () =>
  withApp({}, async (container, fake) => {
    await press("i");
    const box = composer(container);
    await typeInto(box, "keep going");
    await pressIn(box, "x");
    await pressIn(box, "e");
    await pressIn(box, "Enter");
    await until(() => fake.callsTo(sendTextPath).length === 1, "the send");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
    assert.equal(fake.callsTo(markSessionPath).length, 0);
  }));

test("resting one second on an unseen row with recent input writes seen once", () =>
  withApp({}, async (container, fake) => {
    fake.seen.input.emit("keydown");
    fake.seen.advance(999);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "not yet a second");
    fake.seen.advance(1);
    await until(() => fake.callsTo(markPanePath).length === 1, "the dwell write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "a" });
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 1, "the row is written once");
  }));

test("a hidden page writes no seen on the fake clock", () =>
  withApp({}, async (container, fake) => {
    fake.seen.state.visibilityState = "hidden";
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
  }));

test("an unfocused page writes no seen on the fake clock", () =>
  withApp({}, async (container, fake) => {
    fake.seen.state.focused = false;
    fake.seen.input.emit("keydown");
    fake.seen.advance(5000);
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0);
  }));

test("a page refocused with no input in the last 30 s writes no seen until input comes", () =>
  withApp({}, async (container, fake) => {
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
    await until(() => fake.callsTo(markPanePath).length === 1, "the write after fresh input");
  }));

test("a row that changes while the cursor rests on it is written again after one second", () =>
  withApp({}, async (container, fake) => {
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await until(() => fake.callsTo(markPanePath).length === 1, "the first dwell write");
    await until(() => !rowElement(container, "a").hasAttribute("data-unseen"), "the row to read as seen");

    const moved = fixture().map((item) => (item.key === "a" ? { ...item, changed: 400 } : item));
    await mount(container, { rows: moved, error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true, "the newer change is unseen");
    fake.seen.input.emit("keydown");
    fake.seen.advance(1000);
    await until(() => fake.callsTo(markPanePath).length === 2, "the second dwell write");
  }));

test("a written row stops showing unseen until its changed time moves past the one it had", () =>
  withApp({}, async (container, fake) => {
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true);
    await press("x");
    await until(() => !rowElement(container, "a").hasAttribute("data-unseen"), "the row to read as seen");

    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), false, "the server has not caught up yet");

    const moved = fixture().map((item) => (item.key === "a" ? { ...item, changed: 400 } : item));
    await mount(container, { rows: moved, error: "", now, clients: fake.clients, seen: fake.seen });
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true, "a newer change is unseen again");
  }));

test("a failed write leaves the row unseen and shows a fixed line", () =>
  withApp({}, async (container, fake) => {
    fake.failNext.add(markPanePath);
    await press("x");
    await until(() => container.querySelector('[role="alert"]') !== null, "the seen failure line");
    assert.equal(container.querySelector('[role="alert"]')?.textContent, seenFailedText);
    assert.equal(rowElement(container, "a").hasAttribute("data-unseen"), true);
  }));

test("leaving the page stops the seen watch before its dwell writes", () =>
  withApp({}, async (container, fake) => {
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

test("2 on a waiting pane row sends its digit with the row's question hash", () =>
  withApp({ rows: [answerableRow] }, async (container, fake) => {
    await press("2");
    await until(() => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "a", keys: ["2"], expectQuestion: "qh-1" });
    assert.deepEqual(alertTexts(container), [], "no line shows for a sent answer");
  }));

test("1 on a session row answers the request its stream holds with the first option", () =>
  withApp({ rows: [sessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed question" }],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(() => readerText(container).includes("the streamed question"), "the streamed question");
    await press("1");
    await until(() => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "Yes" });
  }));

test("a stream held for another row never answers the cursor's row", () =>
  withApp({ rows: [sessionRow, otherSessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the streamed question" }],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(() => readerText(container).includes("the streamed question"), "the streamed question");
    await press("j");
    assert.equal(cursorKey(container), "s2");
    await press("1");
    await until(() => alertTexts(container).includes(answerFailedText), "the fixed line");
    assert.equal(fake.callsTo(answerPath).length, 0, "the other row's request was not answered");
  }));

test("3 on a row with no answerable question sends nothing and shows the fixed line", () =>
  withApp({}, async (container, fake) => {
    await press("3");
    await until(() => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 0);
    assert.equal(fake.callsTo(answerPath).length, 0);
  }));

test("a rejected answer shows the fixed line, never the server's text, and is not retried", () =>
  withApp({ rows: [answerableRow] }, async (container, fake) => {
    fake.failNext.add(sendKeysPath);
    await press("2");
    await until(() => alertTexts(container).includes(answerFailedText), "the fixed line");
    await settle();
    assert.equal(fake.callsTo(sendKeysPath).length, 1, "the answer was not retried");
    assert.equal(container.textContent?.includes("queue unreachable"), false, "the server's text is not shown");
  }));

test("1 2 and 3 do nothing while the filter or a composer has focus", () =>
  withApp({ rows: [answerableRow] }, async (container, fake) => {
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

// pinnedWorkLine is the working line under the pinned reader, or null when the pinned column draws none.
function pinnedWorkLine(container: HTMLElement): Element | null {
  return pinnedColumn(container)?.querySelector(".work-line") ?? null;
}

test("the pinned column draws the line of its own stream while the cursor rests on another pane", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(() => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { pane: workingPane("Bash") },
    });
    await until(() => pinnedWorkLine(container) !== null, "the pinned working line");
    assert.equal(pinnedWorkLine(container)?.querySelector(".work-tool")?.textContent, "running a command");
    assert.equal(readerWorkLine(container), null, "the cursor's reader on c draws no line");
  }));

test("| pins the cursor's row in a second column that streams it while the cursor moves on", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the cursor's stream");
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    assert.ok(pinnedColumn(container), "the pinned column is shown");
    const [cursorStream, pinnedStream] = fake.callsTo(paneWatchPath);
    assert.equal(pinnedStream.body.pane, "a");
    fake.emit(pinnedStream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "pinned reply" }],
    });
    await until(() => pinnedColumn(container)?.textContent?.includes("pinned reply") === true, "the pinned reply");

    await press("j");
    assert.equal(cursorKey(container), "c");
    await until(() => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the cursor's stream on c");
    assert.equal(cursorStream.signal.aborted, true, "the cursor's stream on a was aborted");
    assert.equal(pinnedStream.signal.aborted, false, "the pinned stream on a stays open");
    assert.ok(pinnedColumn(container)?.textContent?.includes("api"), "the pinned column still names row a");
  }));

test("the pinned column's draft survives the cursor's moves, and Enter sends it to the pinned row only", () =>
  withApp({}, async (container, fake) => {
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await typeInto(pinnedComposer(container), "keep going");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(pinnedComposer(container).value, "keep going", "the draft survives the cursor's move");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "Enter");
    await until(() => fake.callsTo(sendTextPath).length === 1, "the send");
    assert.deepEqual(fake.callsTo(sendTextPath)[0].body, { pane: "a", text: "keep going" });
    await until(() => pinnedComposer(container).value === "", "the sent draft cleared");
    assert.equal(fake.callsTo(markPanePath).length, 0, "the send writes no seen for the cursor's row");
  }));

test("| again removes the pinned column and aborts its stream, and the cursor's stream stays", () =>
  withApp({}, async (container, fake) => {
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [cursorStream, pinnedStream] = fake.callsTo(paneWatchPath);
    await press("|");
    assert.equal(pinnedColumn(container), null, "the pinned column is removed");
    assert.equal(pinnedStream.signal.aborted, true, "the pinned stream was aborted");
    assert.equal(cursorStream.signal.aborted, false, "the cursor's stream stays open");
  }));

test("leaving the page stops the pinned stream as well", () =>
  withApp({}, async (container, fake) => {
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    const [, pinnedStream] = fake.callsTo(paneWatchPath);
    await act(() => {
      render(null, container);
    });
    assert.equal(pinnedStream.signal.aborted, true, "the pinned stream was aborted on unmount");
  }));

test("with the pinned reply box focused, | is text; after it blurs, i focuses the cursor's reply box", () =>
  withApp({}, async (container, fake) => {
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await pressIn(pinnedComposer(container), "|");
    assert.ok(pinnedColumn(container), "a | typed in the pinned reply box does not close the pin");
    await pressIn(pinnedComposer(container), "Escape");
    assert.notEqual(document.activeElement, pinnedComposer(container), "Escape leaves the pinned reply box");
    await press("i");
    assert.equal(document.activeElement, composer(container), "i focuses the cursor's reply box");
  }));

test("a pinned column's draft survives closing the pin and reopening it on the same row", () =>
  withApp({}, async (container, fake) => {
    await press("|");
    await until(() => fake.callsTo(paneWatchPath).length === 2, "the pinned stream");
    await act(() => {
      pinnedComposer(container).focus();
    });
    await typeInto(pinnedComposer(container), "half a thought");
    await pressIn(pinnedComposer(container), "Escape");
    await press("|");
    assert.equal(pinnedColumn(container), null, "the pin is closed");
    await press("|");
    assert.equal(cursorKey(container), "a");
    assert.equal(pinnedComposer(container).value, "half a thought", "the draft survived the close");
  }));

test("a failed send in the pinned column keeps its prompt id through a close and reopen", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
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
    await until(() => fake.callsTo(sayPath).length === 1, "the failed say");
    await until(() => pinnedColumn(container)?.querySelector('[role="alert"]') !== null, "the failed send's line");
    await pressIn(pinnedComposer(container), "Escape");
    await press("|");
    await press("|");
    assert.equal(pinnedComposer(container).value, "keep going", "the draft survived the close");
    await pressIn(pinnedComposer(container), "Enter");
    await until(() => fake.callsTo(sayPath).length === 2, "the second say");
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

test("a waiting pane row shows its question's text, never its raw JSON, and a button click answers it", () =>
  withApp({ rows: [askRow] }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
    assert.equal(rowElement(container, "a").querySelector(".l2")?.textContent, "Which fruit?");
    await until(() => container.querySelector(".question-card") !== null, "the question card");
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
    await until(() => fake.callsTo(sendKeysPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "a", keys: ["2"], expectQuestion: "qh-2" });
  }));

test("a session row's reader draws the question its stream holds, and a button click answers by request id", () =>
  withApp({ rows: [sessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [],
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
    });
    await until(() => container.querySelector(".question-card") !== null, "the question card");
    assert.ok(readerText(container).includes("Pick one"));
    const options = container.querySelectorAll<HTMLButtonElement>(".question-card button.option");
    assert.equal(options.length, 2);
    await act(() => {
      options[0].click();
    });
    await until(() => fake.callsTo(answerPath).length === 1, "the answer");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "Yes" });
  }));

test("a click on the compaction rule shows its summary, and a second click hides it, as o does", () =>
  withApp({ rows: [sessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "compaction", text: "the summary text", trigger: "auto", tokens: "611000" }],
    });
    await until(() => container.querySelector(".reader .compaction button.rule") !== null, "the compaction rule");
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
    assert.equal(container.querySelector(".summary"), null);
  }));

function clickEvent(): Event {
  return new Event("click", { bubbles: true, cancelable: true });
}

test("a click on a row moves the cursor to it, and the reader streams that row", () =>
  withApp({}, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const [first] = fake.callsTo(paneWatchPath);
    await act(() => {
      rowElement(container, "c").dispatchEvent(clickEvent());
    });
    assert.equal(cursorKey(container), "c");
    assert.equal(first.signal.aborted, true, "the stream for a was aborted");
    await until(() => fake.callsTo(paneWatchPath).some((call) => call.body.pane === "c"), "the pane stream for c");
    await settle();
    assert.equal(fake.callsTo(markPanePath).length, 0, "a click alone writes no seen");
  }));

test("a double click on a row writes seen for that row, as Enter does for the cursor's row", () =>
  withApp({}, async (container, fake) => {
    const target = rowElement(container, "c");
    await act(() => {
      target.dispatchEvent(clickEvent());
      target.dispatchEvent(clickEvent());
      target.dispatchEvent(new Event("dblclick", { bubbles: true, cancelable: true }));
    });
    assert.equal(cursorKey(container), "c");
    await until(() => fake.callsTo(markPanePath).length === 1, "the seen write");
    assert.deepEqual(fake.callsTo(markPanePath)[0].body, { pane: "c" });
  }));

test("a click on the view labels switches the view as a and p do", () =>
  withApp({}, async (container) => {
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

test("a click or double click on a link in a row opens the link and moves nothing else", () =>
  withApp({ rows: linkedRows }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
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

test("a click on a link in the reader opens the link and moves the cursor nowhere", () =>
  withApp({ rows: [paneRow, sessionRow] }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "read https://example.com/doc" }],
    });
    await until(() => container.querySelector(".reader a") !== null, "the reader's link");
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

function readerHidden(container: HTMLElement): boolean {
  const reader = container.querySelector('section[aria-label="reader"]');
  assert.ok(reader, "the reader is rendered");
  return reader.hasAttribute("hidden");
}

function sectionOf(container: HTMLElement, title: string): HTMLElement {
  const section = [...container.querySelectorAll<HTMLElement>(".section")].find(
    (element) => element.querySelector(".b-sec")?.textContent === title,
  );
  assert.ok(section, `section ${title} is rendered`);
  return section;
}

test("the Epics group follows Needs you, and an epic shows its gauge and count, or tasks unavailable", () => {
  const first = makeEpic(widgetsRepo, "first");
  const second = makeEpic(gadgetsRepo, "second");
  const epics = epicsView([first, second], new Map([[first.id, detailOf(first, ["done", "integrated", "pending"])]]));
  return withApp({ epics }, (container) => {
    assert.deepEqual(sectionTitles(container), ["Needs you", "Epics", "New since you looked", "Working", "Seen"]);
    assert.deepEqual(rowKeys(container), ["a", epicKey(first), epicKey(second), "c", "b", "d"]);

    const known = rowElement(container, epicKey(first));
    assert.ok(known.classList.contains("b-row") && known.classList.contains("epic"));
    assert.equal(known.querySelector(".label")?.textContent, "first");
    assert.equal(known.querySelector(".proj")?.textContent, widgetsRepo);
    assert.equal(known.querySelector(".count")?.textContent, "2/3", "the integrated task counts as done");
    const gauge = known.querySelector("svg.gauge");
    assert.ok(gauge, "the epic draws its gauge");
    assert.equal(gauge.getAttribute("aria-hidden"), "true");
    assert.ok(gauge.querySelector("circle.gauge-rest"), "the gauge draws its rest circle");
    const done = gauge.querySelector("circle.gauge-done");
    assert.ok(done, "the gauge draws its done arc");
    const [arc, circle] = (done.getAttribute("stroke-dasharray") ?? "").split(/\s+/).map(Number);
    assert.ok(Math.abs(arc / circle - 2 / 3) < 1e-9, "the arc covers two of three tasks");

    const unknown = rowElement(container, epicKey(second));
    assert.equal(unknown.querySelector(".count")?.textContent, "tasks unavailable");
    assert.equal(unknown.querySelector("svg"), null, "no gauge is drawn without detail");
  });
});

test("an epic shows four running steps with the held one marked, then its more, ended and folded lines", () => {
  const steps = ["one", "two", "three", "four", "five"].map((name) => makeStep(name, { held: name === "two" }));
  const busy = makeEpic(widgetsRepo, "busy", { runningSteps: steps, endedSteps: 3 });
  const folded = [makeEpic(widgetsRepo, "old", { live: false }), makeEpic(gadgetsRepo, "older", { live: false })];
  const epics = epicsView([busy, ...folded], new Map());
  return withApp({ epics }, (container) => {
    const section = sectionOf(container, "Epics");
    assert.deepEqual(
      [...section.children].map((child) => child.className),
      ["b-sec", "b-row epic", "b-row step", "b-row step", "b-row step", "b-row step", "b-more", "b-more", "b-more"],
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
      ["1 more", "3 ended", "2 folded"],
    );
  });
});

test("by project, an epic sits atop its folder, and an epic with no folder gets a section of its own", () => {
  const rows = [
    row({ key: "p", project: "/work/orrery", state: "working" }),
    row({ key: "q", project: "/work/api", state: "working" }),
  ];
  const shipping = makeEpic("orrery-5d0c1a7", "ship");
  const stray = makeEpic(gadgetsRepo, "second");
  const epics = epicsView([shipping, stray], new Map());
  return withApp({ rows, epics }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["/work/api", "/work/orrery", gadgetsRepo]);
    assert.deepEqual(rowKeys(container), ["q", epicKey(shipping), "p", epicKey(stray)]);
  });
});

test("an unmatched epic with no repository name is titled no directory in project view", () => {
  const epics = epicsView([makeEpic("", "loose")], new Map());
  return withApp({ rows: [], epics }, async (container) => {
    await press("p");
    assert.deepEqual(sectionTitles(container), ["no directory"]);
  });
});

test("j and k reach epic and step entries; the reader is hidden on an epic and shown on a step", () => {
  return withApp({ epics: oneEpic() }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
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

test("w and n skip the epic and step entries", () => {
  return withApp({ epics: oneEpic() }, async (container) => {
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

test("x, Enter, e, i, 1, 2, 3 and | on an epic entry call nothing and pin nothing", () => {
  return withApp({ epics: oneEpic() }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
    const before = fake.calls.length;
    await press("j");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    await pressEach(["x", "Enter", "e", "i", "1", "2", "3", "|"]);
    await settle();
    assert.equal(fake.calls.length, before, "no call reaches the server");
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), false, "| pinned nothing");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
  });
});

test("resting on an epic or step writes no seen, even with input on the page", () => {
  return withApp({ epics: oneEpic() }, async (_container, fake) => {
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

test("a step's doing text draws as text, never as markup", () => {
  const text = "<img src=x onerror=alert(1)> and <b>bold</b>";
  const running = makeStep("s1", { doing: text });
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [running] })], new Map());
  return withApp({ epics }, (container) => {
    const step = rowElement(container, "step:s1");
    assert.equal(step.querySelector("img, b"), null);
    assert.equal(step.querySelector(".reply")?.textContent, text);
  });
});

test("a click on a step moves the cursor to it, and a click on a link in its text moves nothing", () => {
  const doing = "see https://example.com/notes";
  const epics = epicsView([makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1", { doing })] })], new Map());
  return withApp({ epics }, async (container, fake) => {
    await until(() => fake.callsTo(paneWatchPath).length === 1, "the pane stream for a");
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

// shownEpicColumn is the right column for the cursor's epic or step while it is shown, or null while it is hidden.
function shownEpicColumn(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>('aside.side[aria-label="epic"]:not([hidden])');
}

function requireEpicColumn(container: HTMLElement): HTMLElement {
  const column = shownEpicColumn(container);
  assert.ok(column, "the epic column is shown");
  return column;
}

// columnSlug is the slug the epic column names, which tells one epic's column from another's.
function columnSlug(column: HTMLElement): string | null {
  return column.querySelector(".epic-head .slug")?.textContent ?? null;
}

// sliceClasses is the class of each slice the column draws, in order.
function sliceClasses(column: HTMLElement): (string | null)[] {
  return [...column.querySelectorAll(".chart .slice")].map((slice) => slice.getAttribute("class"));
}

// markedStep is the label of the step the column marks, or null when it marks none.
function markedStep(column: HTMLElement): string | null {
  return column.querySelector(".step.cur .label")?.textContent ?? null;
}

// twoStepEpic is a live epic with two steps, each on one of its tasks: the first done, the second pending. Each of its
// three tasks has a slice.
function twoStepEpic(): { epic: Epic; detail: EpicsGetResponse } {
  const steps = [makeStep("s1", { task: "task-0" }), makeStep("s2", { task: "task-1" })];
  const epic = makeEpic(widgetsRepo, "first", { runningSteps: steps });
  const slices = [
    { task: "task-0", hue: "done" },
    { task: "task-1", hue: "dispatched" },
    { task: "task-2", hue: "gap" },
  ];
  return { epic, detail: detailOf(epic, ["done", "pending", "gap"], steps, slices) };
}

test("on an epic entry the right column draws a slice per task and marks no step", () => {
  const { epic, detail } = twoStepEpic();
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withApp({ epics }, async (container) => {
    await press("j");
    assert.equal(cursorKey(container), epicKey(epic));
    const column = requireEpicColumn(container);
    assert.equal(
      column.previousElementSibling?.getAttribute("aria-label"),
      "pinned reader",
      "the column follows the pinned reader",
    );
    assert.equal(column.parentElement?.classList.contains("panes"), true, "the column sits in the panes");
    assert.equal(columnSlug(column), "first");
    assert.deepEqual(sliceClasses(column), ["slice", "slice", "slice"], "one slice per task, and none is outlined");
    assert.equal(column.querySelector(".step.cur"), null, "no step is marked");
  });
});

test("on a step entry the right column draws the same epic and marks that step", () => {
  const { epic, detail } = twoStepEpic();
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withApp({ epics }, async (container) => {
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(markedStep(requireEpicColumn(container)), "task-0");
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    const column = requireEpicColumn(container);
    assert.equal(columnSlug(column), "first");
    assert.deepEqual(sliceClasses(column), ["slice", "slice cur", "slice"], "the step's task slice is outlined");
    assert.equal(markedStep(column), "task-1");
  });
});

test("a pane row hides the right column, and an epic shows it again", () => {
  const { epic, detail } = twoStepEpic();
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withApp({ epics }, async (container) => {
    await press("j");
    assert.ok(shownEpicColumn(container), "the column shows on an epic entry");
    await press("j");
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(shownEpicColumn(container), null, "a pane row hides the column");
    await press("k");
    await press("k");
    await press("k");
    assert.equal(cursorKey(container), epicKey(epic));
    assert.ok(shownEpicColumn(container), "the column shows again");
  });
});

test("a session row hides the right column", () => {
  const { epic, detail } = twoStepEpic();
  const epics = epicsView([epic], new Map([[epic.id, detail]]));
  return withApp({ rows: [sessionRow], epics }, async (container) => {
    assert.equal(cursorKey(container), epicKey(epic));
    assert.ok(shownEpicColumn(container), "the column shows on an epic entry");
    await press("j");
    await press("j");
    await press("j");
    assert.equal(cursorKey(container), "s1");
    assert.equal(shownEpicColumn(container), null, "a session row hides the column");
  });
});

test("an epic with no detail shows epic unavailable in the right column", () => {
  return withApp({ epics: oneEpic() }, async (container) => {
    await press("j");
    assert.equal(requireEpicColumn(container).textContent, "epic unavailable");
    await press("j");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(
      requireEpicColumn(container).textContent,
      "epic unavailable",
      "a step of that epic is unavailable too",
    );
  });
});

test("with a row pinned, the right column follows the cursor from one epic's steps to another's", () => {
  const first = makeEpic(widgetsRepo, "first", { runningSteps: [makeStep("s1"), makeStep("s2")] });
  const second = makeEpic(gadgetsRepo, "second", { runningSteps: [makeStep("s3")] });
  const epics = epicsView(
    [first, second],
    new Map([
      [
        first.id,
        detailOf(first, ["done", "pending"], [makeStep("s1", { task: "task-0" }), makeStep("s2", { task: "task-1" })]),
      ],
      [second.id, detailOf(second, ["gap"], [makeStep("s3", { task: "task-0" })])],
    ]),
  );
  return withApp({ epics }, async (container) => {
    await press("|");
    assert.ok(pinnedColumn(container), "row a is pinned");
    await press("j");
    assert.equal(cursorKey(container), epicKey(first));
    assert.equal(columnSlug(requireEpicColumn(container)), "first");
    await press("j");
    assert.equal(markedStep(requireEpicColumn(container)), "task-0");
    await press("j");
    assert.equal(markedStep(requireEpicColumn(container)), "task-1");
    await press("j");
    assert.equal(cursorKey(container), epicKey(second));
    assert.equal(columnSlug(requireEpicColumn(container)), "second");
    assert.equal(markedStep(requireEpicColumn(container)), null);
    await press("j");
    assert.equal(cursorKey(container), "step:s3");
    assert.equal(columnSlug(requireEpicColumn(container)), "second");
    assert.equal(markedStep(requireEpicColumn(container)), "task-0");
    assert.ok(pinnedColumn(container), "the pinned column stays open");
  });
});

// onStepOne moves the cursor from row a down to the first step of oneEpic, s1, and waits for its stream to start.
async function onStepOne(container: HTMLElement, fake: FakeServer): Promise<void> {
  await press("j");
  await press("j");
  assert.equal(cursorKey(container), "step:s1");
  await until(() => fake.callsTo(sessionWatchPath).length === 1, "the step stream");
}

// stepButton is the button of the cursor's step box with the given label.
function stepButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = [...container.querySelectorAll<HTMLButtonElement>(".reply:not([hidden]) .stepbox-actions button")].find(
    (element) => element.textContent === label,
  );
  assert.ok(button, `the step box has a ${label} button`);
  return button;
}

async function clickIn(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(clickEvent());
  });
}

test("a step entry streams Sessions.WatchConversation by its id, and the reader draws its reply as text", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    const [stream] = fake.callsTo(sessionWatchPath);
    assert.equal(stream.body.id, "s1");
    fake.emit(stream, {
      transcript: "t1",
      seq: "1",
      reset: true,
      items: [{ kind: "assistant", text: "the step reply" }],
    });
    await until(() => readerText(container).includes("the step reply"), "the step reply");
    assert.equal(readerHidden(container), false, "the reader shows on a step");
    assert.equal(container.querySelector(".question-card"), null, "a step draws no question");
    assert.equal(container.querySelector(".reader .option"), null, "a step draws no answer");
  }));

test("Enter in a step's box calls Steps.Nudge once with its id and text, and clears the draft when it lands", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement, box);
    await typeInto(box, "try the other route");
    await pressIn(box, "Enter");
    await until(() => fake.callsTo(nudgePath).length === 1, "the nudge");
    assert.deepEqual(fake.callsTo(nudgePath)[0].body, { id: "s1", text: "try the other route" });
    await until(() => box.value === "", "the landed draft cleared");
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 1, "the nudge was sent once");
  }));

test("a refused nudge keeps the step's draft and shows the refusal", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    fake.refuseNext.set(nudgePath, refusedNudgeText);
    await onStepOne(container, fake);
    await press("i");
    await typeInto(composer(container), "try again");
    await pressIn(composer(container), "Enter");
    await until(() => alertTexts(container).includes(`nudge refused: ${refusedNudgeText}`), "the refusal line");
    assert.equal(composer(container).value, "try again", "the draft is kept");
  }));

test("an unknown nudge outcome shows not known, keeps the draft, and Enter does not resend it", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    fake.failNext.add(nudgePath);
    await onStepOne(container, fake);
    await press("i");
    await typeInto(composer(container), "try again");
    await pressIn(composer(container), "Enter");
    await until(() => alertTexts(container).some((text) => text.startsWith("nudge not known")), "the unknown line");
    assert.equal(composer(container).value, "try again", "the draft is kept");
    await pressIn(composer(container), "Enter");
    await settle();
    assert.equal(fake.callsTo(nudgePath).length, 1, "the nudge was not resent");
  }));

test("Interrupt calls Steps.Hold with the step's id", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await clickIn(stepButton(container, "Interrupt"));
    await until(() => fake.callsTo(holdPath).length === 1, "the hold");
    assert.deepEqual(fake.callsTo(holdPath)[0].body, { id: "s1" });
  }));

test("Stop asks first: Cancel sends nothing, and Confirm stop calls Steps.Stop with the step's id", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await clickIn(stepButton(container, "Stop"));
    assert.equal(fake.callsTo(stopStepPath).length, 0, "Stop alone sends nothing");
    await clickIn(stepButton(container, "Cancel"));
    await settle();
    assert.equal(fake.callsTo(stopStepPath).length, 0, "Cancel sends nothing");
    await clickIn(stepButton(container, "Stop"));
    await clickIn(stepButton(container, "Confirm stop"));
    await until(() => fake.callsTo(stopStepPath).length === 1, "the stop");
    assert.deepEqual(fake.callsTo(stopStepPath)[0].body, { id: "s1" });
  }));

test("a draft stays with its step across cursor moves, and each step keeps its own box", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
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

test("a step's draft survives a poll that no longer lists the step", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Escape");
    const withoutSteps = epicsView([makeEpic(widgetsRepo, "first")], new Map());
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, epics: withoutSteps });
    await mount(container, { rows: fixture(), error: "", now, clients: fake.clients, seen: fake.seen, epics: oneEpic() });
    await press("g");
    await press("g");
    await onStepOne(container, fake);
    assert.equal(composer(container).value, "keep going", "the draft survived the poll");
  }));

test("i focuses the step's box, its keys are text, and Escape leaves it", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await press("i");
    const box = composer(container);
    assert.equal(document.activeElement, box);
    await pressIn(box, "j");
    await pressIn(box, "?");
    assert.equal(cursorKey(container), "step:s1");
    assert.equal(container.querySelector('[aria-label="keys"]'), null);
    await pressIn(box, "Escape");
    assert.notEqual(document.activeElement, box);
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
  }));

test("1, x, e, Enter and | on a step entry call nothing and pin nothing", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    const before = fake.calls.length;
    await pressEach(["x", "Enter", "e", "1", "2", "3", "|"]);
    await settle();
    assert.equal(fake.calls.length, before, "no call reaches the server");
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), false, "| pinned nothing");
    assert.equal(cursorKey(container), "step:s1");
  }));

test("no Control or Panes call names a step, and no call starts a step", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    await pressEach(["x", "Enter", "e", "1", "2", "3", "|"]);
    await press("i");
    await typeInto(composer(container), "keep going");
    await pressIn(composer(container), "Enter");
    await until(() => fake.callsTo(nudgePath).length === 1, "the nudge");
    await clickIn(stepButton(container, "Interrupt"));
    await until(() => fake.callsTo(holdPath).length === 1, "the hold");
    await clickIn(stepButton(container, "Stop"));
    await clickIn(stepButton(container, "Confirm stop"));
    await until(() => fake.callsTo(stopStepPath).length === 1, "the stop");
    for (const call of fake.calls) {
      assert.equal(call.path.includes("StartStep"), false, `${call.path} is not a start`);
      if (call.path.startsWith("/orrery.v1.Panes/") || call.path.startsWith("/orrery.v1.Control/")) {
        const names = Object.values(call.body).some((value) => value === "s1" || value === "s2");
        assert.equal(names, false, `${call.path} names no step`);
      }
    }
  }));

test("leaving a step stops its stream, and the stream follows the cursor's step", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    const [first] = fake.callsTo(sessionWatchPath);
    await press("j");
    assert.equal(cursorKey(container), "step:s2");
    assert.equal(first.signal.aborted, true, "moving to s2 stops s1's stream");
    await until(() => fake.callsTo(sessionWatchPath).length === 2, "the s2 stream");
    assert.equal(fake.callsTo(sessionWatchPath)[1].body.id, "s2");
    await press("j");
    assert.equal(cursorKey(container), "c");
    assert.equal(fake.callsTo(sessionWatchPath)[1].signal.aborted, true, "moving to a row stops s2's stream");
  }));

test("a step entry shows the reader beside the epic column, and a pin adds its column with the step class", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await press("g");
    await press("g");
    await press("|");
    await onStepOne(container, fake);
    const panes = container.querySelector(".panes");
    assert.equal(panes?.classList.contains("split"), true, "row a is pinned");
    assert.equal(panes?.classList.contains("with-step"), true, "the reader and column share the panes");
    assert.ok(shownEpicColumn(container), "the epic column shows");
    assert.equal(readerHidden(container), false, "the reader shows");
    await press("k");
    assert.equal(cursorKey(container), `epic:${widgetsRepo}/first`);
    assert.equal(
      container.querySelector(".panes")?.classList.contains("with-step"),
      false,
      "an epic entry is not a step",
    );
  }));

test("without a pin, a step entry's panes carry the step class; a row's do not", () =>
  withApp({ epics: oneEpic() }, async (container, fake) => {
    await onStepOne(container, fake);
    assert.equal(container.querySelector(".panes")?.classList.contains("with-step"), true);
    assert.equal(container.querySelector(".panes")?.classList.contains("split"), false);
    await press("g");
    await press("g");
    assert.equal(cursorKey(container), "a");
    assert.equal(container.querySelector(".panes")?.classList.contains("with-step"), false);
  }));
