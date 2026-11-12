import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
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

type Clients = ReturnType<typeof createClients>;

type Seen = ReturnType<typeof fakeSeen>;

interface Props {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seen: Seen;
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

const keysHelpList = ["j", "k", "↓", "↑", "gg", "G", "a", "p", "w", "n", "x", "Enter", "e", "/", "i", "1", "2", "3", "o", "?", "Esc"];

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
