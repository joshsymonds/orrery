import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import { HostStatusResponseSchema, type HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

const now = 1000;
const apiDir = "/work/api";
const webDir = "/work/web";
const askPath = "/orrery.v1.Picker/Ask";
const proposePath = "/orrery.v1.Picker/Propose";
const startPath = "/orrery.v1.Control/Start";

// The serving host is box. dock answers, and brine, which is listed, does not.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: "box",
  hosts: [
    { name: "box", reachable: true },
    { name: "dock", reachable: true },
    { name: "brine", reachable: false },
  ],
});

// row is a conversation as the list holds it, with the given fields.
function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    tool: "",
    id: init.key,
    kind: "pane",
    label: init.key,
    project: apiDir,
    host: "",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "none",
    archiveId: "",
    ...init,
  };
}

// apiRows is the one conversation in the api project, which runs on dock. It is seen and done, so no timer runs.
function apiRows(): Row[] {
  return [
    row({
      key: "a",
      label: "api one",
      host: "dock",
      state: "done",
      archiveKind: "session",
      archiveId: "a",
      changed: 300,
      seen: 300,
    }),
  ];
}

// Call is one unary call the App sent: its path, and its body as JSON.
interface Call {
  path: string;
  body: Record<string, unknown>;
}

// FakeServer answers the App's unary calls with the reply a test sets for the path, once, and with an empty object
// otherwise. A WatchConversation stream stays open, so no conversation arrives. Every unary call is recorded.
class FakeServer {
  readonly calls: Call[] = [];
  readonly replies = new Map<string, object>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    const bytes = new Uint8Array(await request.arrayBuffer());
    this.calls.push({ path, body: bytes.byteLength === 0 ? {} : JSON.parse(new TextDecoder().decode(bytes)) });
    const reply = this.replies.get(path) ?? {};
    this.replies.delete(path);
    return new Response(JSON.stringify(reply), { status: 200, headers: { "content-type": "application/json" } });
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }
}

// Mounted is the App on screen: its container, the fake server it talks to, and show, which renders the App again over
// rows.
interface Mounted {
  container: HTMLElement;
  server: FakeServer;
  show: (rows: Row[]) => Promise<void>;
}

// withApp mounts App over a fake server, with the serving host status and the api conversation, in a window of width
// (the default when none is given), and runs check. App is unmounted and the DOM removed when the test ends, or as soon
// as signal aborts first.
async function withApp(signal: AbortSignal, check: (mounted: Mounted) => Promise<void>, width?: number): Promise<void> {
  const window = installTestDom();
  if (width !== undefined) {
    window.innerWidth = width;
  }
  const container = document.createElement("div");
  document.body.append(container);
  const server = new FakeServer();
  const show = (rows: Row[]) =>
    act(() => {
      render(<App rows={rows} now={now} clients={server.clients} answered={true} host={status} />, container);
    });
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await show(apiRows());
    await check({ container, server, show });
  } finally {
    await teardown();
  }
}

// untilTurn waits for check one turn at a time inside act, so the renders each turn lets through are applied. It reads
// no clock and arms no timer.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

// press sends a key to the document, as the app's key listener takes it.
async function press(key: string, modifiers: KeyboardEventInit = {}): Promise<KeyboardEvent> {
  const event = new KeyboardEvent("keydown", { key, cancelable: true, ...modifiers });
  await act(() => {
    document.dispatchEvent(event);
  });
  return event;
}

// pressIn sends a key to an element, which the key listener on the document also takes.
async function pressIn(element: Element, key: string): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

// typeIn types text into a text field.
async function typeIn(field: HTMLInputElement, text: string): Promise<void> {
  await act(() => {
    field.value = text;
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// click presses an element.
async function click(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// one is the element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found, `no element matches ${selector}`);
  return found;
}

// buttonIn is the button under scope whose text is label, which must be there.
function buttonIn(container: HTMLElement, scope: string, label: string): HTMLElement {
  const found = [...container.querySelectorAll<HTMLElement>(`${scope} button`)].find(
    (button) => button.textContent.trim() === label,
  );
  assert.ok(found !== undefined, `no ${label} button in ${scope}`);
  return found;
}

// newButton is the inbox head's New button.
function newButton(container: HTMLElement): HTMLElement {
  const found = one(container, ".inbox-head button:not(.icon-only)");
  assert.equal(found.textContent.trim(), "New", "the inbox head's button reads New");
  return found;
}

// headOf is the head button of the project whose name is name, the press that opens its page.
function headOf(container: HTMLElement, name: string): HTMLElement {
  return one(container, `section.dir[aria-label="${name}"] button.proj`);
}

// cursorKey is the key of the entry the cursor rests on.
function cursorKey(container: HTMLElement): string | null {
  return container.querySelector(".row.cur")?.getAttribute("data-key") ?? null;
}

// finderField is the finder's text field, which is drawn while the finder is open.
function finderField(container: HTMLElement): HTMLInputElement {
  const field = dialogNamed(container, "New session")?.querySelector<HTMLInputElement>("input");
  assert.ok(field, "the finder has its text field");
  return field;
}

// dialogNamed is the dialog with the label, when it is drawn.
function dialogNamed(container: HTMLElement, label: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`[role="dialog"][aria-label="${label}"]`);
}

// starterOf is the Starter's dialog, which is drawn while the Starter is open.
function starterOf(container: HTMLElement): HTMLElement {
  const starter = dialogNamed(container, "Start a session");
  assert.ok(starter, "the Starter is open");
  return starter;
}

// starterField is the Starter's text field with the label.
function starterField(container: HTMLElement, label: string): HTMLInputElement {
  const field = starterOf(container).querySelector<HTMLInputElement>(`input[aria-label="${label}"]`);
  assert.ok(field, `the Starter has its ${label} field`);
  return field;
}

// hostChoice is the Starter's host choice, which is drawn once a place is set.
function hostChoice(container: HTMLElement): HTMLSelectElement {
  const choice = starterOf(container).querySelector<HTMLSelectElement>('select[aria-label="Host"]');
  assert.ok(choice, "the Starter has its host choice");
  return choice;
}

// proposalRows are the Starter's proposals, each a row of the listbox.
function proposalRows(container: HTMLElement): HTMLElement[] {
  return [...starterOf(container).querySelectorAll<HTMLElement>(".starter-list .pick-row")];
}

// proposeFor types the prompt into the Starter and sends it to Picker.Propose, then waits until count proposals are
// listed.
async function proposeFor(signal: AbortSignal, container: HTMLElement, prompt: string, count: number): Promise<void> {
  await typeIn(starterField(container, "Prompt"), prompt);
  await pressIn(starterField(container, "Prompt"), "Enter");
  await untilTurn(signal, () => proposalRows(container).length === count, "the proposals are listed");
}

test("HostsWebStarter app: N and New open the Starter with the prompt first, and no folder or host yet", (t) =>
  withApp(t.signal, async ({ container, server }) => {
    await press("N");
    const starter = starterOf(container);
    assert.equal(document.activeElement, starterField(container, "Prompt"), "the prompt has focus");
    assertNone(starter.querySelector('input[aria-label="Folder"]'), "no folder is drawn before a Propose");
    assertNone(starter.querySelector('select[aria-label="Host"]'), "no host is drawn before a Propose");
    assert.equal(starter.querySelector(".inote")?.textContent, "Press ⏎ and Orrery proposes where to run it.");
    assertNone(dialogNamed(container, "New session"), "N opens the Starter and not the finder");
    assert.deepEqual(server.callsTo(proposePath), [], "opening the Starter proposes nothing");
    assert.deepEqual(server.callsTo(startPath), [], "opening the Starter starts nothing");

    await pressIn(starterField(container, "Prompt"), "Escape");
    assertNone(dialogNamed(container, "Start a session"), "Escape closes the Starter");

    await click(newButton(container));
    assert.equal(
      starterOf(container).querySelector(".inote")?.textContent,
      "Press ⏎ and Orrery proposes where to run it.",
      "New opens the prompt first",
    );
    assertNone(starterOf(container).querySelector('input[aria-label="Folder"]'), "New draws no folder yet");
  }));

test("HostsWebStarter app: a finder folder row opens the Starter with that folder typed on the serving host", (t) =>
  withApp(t.signal, async ({ container, server }) => {
    server.replies.set(askPath, {
      rows: [{ token: "tok-web", kind: "fresh", dir: webDir, pane: "", label: "", note: "start fresh", prompt: false }],
    });
    await press("k", { metaKey: true });
    await typeIn(finderField(container), "web");
    await pressIn(finderField(container), "Enter");
    await untilTurn(t.signal, () => container.querySelectorAll(".pick-row").length === 1, "the folder row is listed");
    await click(one(container, ".pick-row"));

    assertNone(dialogNamed(container, "New session"), "the finder closes");
    assert.equal(starterField(container, "Folder").value, webDir, "the folder row's folder is typed");
    assert.equal(hostChoice(container).value, "box", "the serving host is chosen");
    assert.deepEqual(server.callsTo(startPath), [], "choosing the folder row starts nothing");
  }));

test("HostsWebStarter app: New session here opens the Starter with the project's folder typed on the project's host", (t) =>
  withApp(t.signal, async ({ container }) => {
    await click(headOf(container, "api"));
    await click(buttonIn(container, 'section[aria-label="project"]', "New session here"));

    assert.equal(starterField(container, "Folder").value, apiDir, "the project's folder is typed");
    assert.equal(hostChoice(container).value, "dock", "the project's host is chosen, not the serving host");
  }));

test("HostsWebStarter app: Start sends the chosen host and folder to Control.Start, and opens the started session", (t) =>
  withApp(t.signal, async ({ container, server, show }) => {
    server.replies.set(proposePath, { proposals: [{ host: "dock", dir: webDir }] });
    server.replies.set(startPath, { id: "sess-new" });
    await press("N");
    await proposeFor(t.signal, container, "build the web page", 1);
    await click(buttonIn(container, '[role="dialog"][aria-label="Start a session"]', "Start the session⌘⏎"));
    await untilTurn(t.signal, () => dialogNamed(container, "Start a session") === null, "the start closes the Starter");

    const [start] = server.callsTo(startPath);
    assert.ok(start, "Control.Start is called once");
    assert.equal(start.body.dir, webDir, "the folder is sent");
    assert.equal(start.body.host, "dock", "the host is sent");
    assert.equal(start.body.prompt, "build the web page", "the prompt is sent");
    assert.match(String(start.body.id), /^[0-9a-f]{32}$/, "the start id is 32 lowercase hex digits");
    assert.equal(cursorKey(container), "a", "the started session's row is not in the list yet");

    const started = row({
      key: "session:sess-new",
      id: "sess-new",
      archiveId: "sess-new",
      kind: "session",
      label: "web",
      host: "dock",
      project: webDir,
      state: "working",
      changed: 500,
      seen: 500,
    });
    await show([...apiRows(), started]);
    assert.equal(cursorKey(container), "session:sess-new", "the started session takes the cursor");
  }));

test("HostsWebStarter app: a host that does not answer is in neither the proposals nor the host choice", (t) =>
  withApp(t.signal, async ({ container, server }) => {
    server.replies.set(proposePath, {
      proposals: [
        { host: "brine", dir: "/work/ops" },
        { host: "dock", dir: webDir },
      ],
    });
    await press("N");
    await proposeFor(t.signal, container, "build the web page", 1);

    assert.equal(
      one(proposalRows(container)[0], ".t").textContent,
      webDir,
      "the proposal on the reachable host is listed",
    );
    assert.deepEqual(
      [...hostChoice(container).options].map((option) => option.value),
      ["box", "dock"],
      "brine is not offered as a host",
    );
  }));

test("HostsWebStarter app: under 760px the Starter is a sheet", (t) =>
  withApp(
    t.signal,
    async ({ container }) => {
      await press("N");
      assert.equal(starterOf(container).className, "sheet", "the Starter is a sheet");
    },
    600,
  ));
