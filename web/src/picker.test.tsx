import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import { PickerPalette } from "./picker";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

const askPath = "/orrery.v1.Picker/Ask";
const runPath = "/orrery.v1.Picker/Run";

// Synthetic rows only: made-up tokens, panes, directories and words.
const jumpRow = {
  token: "tok-jump",
  kind: "jump",
  dir: "/work/api",
  pane: "%3",
  label: "api",
  note: "waiting on you",
  prompt: false,
};
const resumeRow = {
  token: "tok-resume",
  kind: "resume",
  dir: "/work/docs",
  pane: "",
  label: "docs",
  note: "resume sess-7",
  prompt: true,
};
const freshRow = {
  token: "tok-fresh",
  kind: "fresh",
  dir: "/work/web",
  pane: "",
  label: "web",
  note: "start fresh",
  prompt: false,
};

interface Call {
  path: string;
  body: Record<string, unknown>;
}

// Reply is the status and JSON body a unary call answers with.
interface Reply {
  status: number;
  body: object;
}

// FakeServer answers Picker.Ask and Picker.Run through TransportOptions.fetch. A path answers with the reply a test
// queues for it, once, and an unqueued call answers an empty reply. Every call is recorded with its body.
class FakeServer {
  readonly calls: Call[] = [];
  readonly replies = new Map<string, Reply>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    this.calls.push({ path, body: JSON.parse(new TextDecoder().decode(bytes)) });
    const reply = this.replies.get(path) ?? { status: 200, body: {} };
    this.replies.delete(path);
    return new Response(JSON.stringify(reply.body), {
      status: reply.status,
      headers: { "content-type": "application/json" },
    });
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }
}

type DomWindow = ReturnType<typeof installTestDom>;

// withDom runs check in a fresh DOM over a fake server. The palette is unmounted and the DOM removed when the test
// ends, or as soon as signal aborts if it times out first.
async function withDom(
  signal: AbortSignal,
  check: (container: HTMLElement, server: FakeServer) => Promise<void>,
): Promise<void> {
  const window: DomWindow = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await check(container, new FakeServer());
  } finally {
    await teardown();
  }
}

async function mountPalette(
  container: HTMLElement,
  server: FakeServer,
  onRun: (pane: string) => void = () => {},
  onClose: () => void = () => {},
): Promise<void> {
  await act(() => {
    render(<PickerPalette clients={server.clients} onRun={onRun} onClose={onClose} />, container);
  });
}

// untilTurn waits for check one setImmediate turn at a time inside act, so the renders each turn lets through are
// applied. It reads no clock and arms no timer.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

function input(container: HTMLElement): HTMLInputElement {
  const box = container.querySelector<HTMLInputElement>("input");
  assert.ok(box, "the palette has its text box");
  return box;
}

function rowButtons(container: HTMLElement): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".picker-row")];
}

// rowTexts is the label and note of each row the palette lists, in order.
function rowTexts(container: HTMLElement): string[][] {
  return rowButtons(container).map((button) => [
    button.querySelector(".picker-label")?.textContent ?? "",
    button.querySelector(".picker-note")?.textContent ?? "",
  ]);
}

function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

async function typeWords(container: HTMLElement, text: string): Promise<void> {
  const box = input(container);
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function pressKey(element: Element, key: string): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
  });
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

test("PickerPalette: Enter sends the typed words to Picker.Ask, and the rows come back with label and note", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, freshRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api  web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows are listed");
    assert.deepEqual(server.callsTo(askPath).map((call) => call.body), [{ text: "api  web" }]);
    assert.deepEqual(rowTexts(container), [
      ["api", "waiting on you"],
      ["web", "start fresh"],
    ]);
  }));

test("PickerPalette: choosing a jump or resume row sends its token to Picker.Run and reports the pane", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, resumeRow] } });
    const panes: string[] = [];
    await mountPalette(container, server, (pane) => panes.push(pane));
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows are listed");

    server.replies.set(runPath, { status: 200, body: { pane: "%3" } });
    await click(rowButtons(container)[0]);
    await untilTurn(t.signal, () => panes.length === 1, "the jump reports its pane");
    assert.deepEqual(panes, ["%3"]);
    assert.deepEqual(server.callsTo(runPath)[0].body, { token: "tok-jump" });

    server.replies.set(runPath, { status: 200, body: { pane: "%5" } });
    await click(rowButtons(container)[1]);
    await untilTurn(t.signal, () => panes.length === 2, "the resume reports its pane");
    assert.deepEqual(panes, ["%3", "%5"]);
    assert.deepEqual(server.callsTo(runPath)[1].body, { token: "tok-resume" });
  }));

test("PickerPalette: Escape closes the palette", (t) =>
  withDom(t.signal, async (container, server) => {
    let closes = 0;
    await mountPalette(container, server, () => {}, () => {
      closes += 1;
    });
    await pressKey(input(container), "Escape");
    assert.equal(closes, 1);
  }));

test("PickerPalette: a failed Ask shows the fixed error line and none of the server's text", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, {
      status: 400,
      body: { code: "invalid_argument", message: "text is refused: secret detail" },
    });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the failure shows its line");
    assert.deepEqual(alertTexts(container), ["the picker failed"]);
    assert.equal(container.textContent?.includes("secret detail"), false, "the server's text is not shown");
  }));
