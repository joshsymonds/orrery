import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { NewPanel } from "./picker";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

const askPath = "/orrery.v1.Picker/Ask";
const runPath = "/orrery.v1.Picker/Run";
const startPath = "/orrery.v1.Control/Start";

// Synthetic rows only: made-up tokens, panes, directories and words. A jump row carries its pane's label; a folder row
// carries no label, as the server sends it.
const jumpRow = {
  token: "tok-jump",
  kind: "jump",
  dir: "/work/api",
  pane: "%3",
  label: "auth refactor",
  note: "waiting on you",
  prompt: false,
};
const resumeRow = {
  token: "tok-resume",
  kind: "resume",
  dir: "/work/docs",
  pane: "",
  label: "",
  note: "resume sess-7",
  prompt: true,
};
const freshRow = {
  token: "tok-fresh",
  kind: "fresh",
  dir: "/work/web",
  pane: "",
  label: "",
  note: "start fresh",
  prompt: false,
};
const promptRow = {
  token: "tok-prompt",
  kind: "pane",
  dir: "/work/docs",
  pane: "%4",
  label: "",
  note: "pane in docs",
  prompt: true,
};

// listedPane is a listed pane row, which a picker row with the same pane id takes its title from.
function listedPane(pane: string, project: string, label: string): Row {
  return {
    key: `pane:${pane}`,
    id: pane,
    kind: "pane",
    label,
    project,
    host: "",
    state: "waiting",
    summary: label,
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    archiveId: "",
  };
}

interface Call {
  path: string;
  body: Record<string, unknown>;
}

// Reply is the status and JSON body a unary call answers with.
interface Reply {
  status: number;
  body: object;
}

// FakeServer answers Picker.Ask, Picker.Run and Control.Start through TransportOptions.fetch. A path answers with the
// reply a test queues for it, once, and an unqueued call answers an empty reply. Every call is recorded with its body.
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
  onStart: (session: string) => void = () => {},
  rows: Row[] = [],
): Promise<void> {
  await act(() => {
    render(
      <NewPanel clients={server.clients} rows={rows} onRun={onRun} onStart={onStart} onClose={onClose} />,
      container,
    );
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
      ["api auth refactor", "waiting on you"],
      ["web", "start fresh"],
    ]);
  }));

test("TitleParts: a pane row joined to a listed pane draws the listed row's directory and summary, then its note", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow] } });
    await mountPalette(container, server, () => {}, () => {}, () => {}, [
      listedPane("%3", "/work/billing", "retry the webhook"),
    ]);
    await typeWords(container, "billing");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the row is listed");
    const [row] = rowButtons(container);
    assert.equal(row.querySelector(".picker-label .title-dir")?.textContent, "billing", "the listed directory");
    assert.equal(row.querySelector(".picker-label .title-line")?.textContent, "retry the webhook", "the listed summary");
    assert.equal(row.querySelector(".picker-note")?.textContent, "waiting on you", "the note follows the title");
    assert.deepEqual(rowTexts(container), [["billing retry the webhook", "waiting on you"]]);
  }));

test("TitleParts: a folder row draws its directory and note, with no line", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await typeWords(container, "web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the row is listed");
    const [row] = rowButtons(container);
    assert.equal(row.querySelector(".picker-label .title-dir")?.textContent, "web", "the folder's directory");
    assertNone(row.querySelector(".picker-label .title-line"), "a folder row has no line");
    assert.equal(row.querySelector(".picker-note")?.textContent, "start fresh");
  }));

test("TitleParts: no picker row shows a pane id, and a pane row with no listed pane draws its own directory and label", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, promptRow] } });
    await mountPalette(container, server, () => {}, () => {}, () => {}, [
      listedPane("%3", "/work/billing", "retry the webhook"),
    ]);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows are listed");
    const [listed, unlisted] = rowButtons(container);
    assert.equal(listed.querySelector(".title-dir")?.textContent, "billing", "the listed pane's directory");
    assert.equal(unlisted.querySelector(".title-dir")?.textContent, "docs", "the unlisted pane's own directory");
    assertNone(unlisted.querySelector(".title-line"), "an unlisted pane with no label draws no line");
    for (const button of rowButtons(container)) {
      assert.equal(/%\d/.test(button.textContent ?? ""), false, "a row shows no pane id");
    }
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

// startForm is the start form the palette draws for a folder row, which it draws only while the form is open.
function startForm(container: HTMLElement): HTMLElement {
  const form = container.querySelector<HTMLElement>(".picker-start");
  assert.ok(form, "the start form is open");
  return form;
}

function messageBox(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>('[aria-label="message"]');
  assert.ok(box, "the start form has its message box");
  return box;
}

function startButton(container: HTMLElement): HTMLButtonElement {
  const found = [...container.querySelectorAll<HTMLButtonElement>("button")].find(
    (button) => button.textContent === "Start",
  );
  assert.ok(found, "the start form has its Start button");
  return found;
}

async function typeMessage(container: HTMLElement, text: string): Promise<void> {
  const box = messageBox(container);
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

// askAndChoose asks for words, once the answer lists the row at index, and chooses that row.
async function askAndChoose(signal: AbortSignal, container: HTMLElement, words: string, index: number): Promise<void> {
  await typeWords(container, words);
  await pressKey(input(container), "Enter");
  await untilTurn(signal, () => rowButtons(container).length > index, "the row is listed");
  await click(rowButtons(container)[index]);
}

test("PickerStartForm: choosing a folder row opens the start form naming the folder, with an empty message", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await askAndChoose(t.signal, container, "web", 0);
    assert.equal(startForm(container).querySelector(".picker-folder")?.textContent, "/work/web");
    assert.equal(messageBox(container).value, "", "a row without a prompt starts the message empty");
    assert.deepEqual(server.callsTo(runPath), [], "a folder row runs nothing");
  }));

test("PickerStartForm: a prompt row's message starts with the words the palette last asked", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [promptRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api  web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the row is listed");
    await typeWords(container, "typed but not asked");
    await click(rowButtons(container)[0]);
    assert.equal(startForm(container).querySelector(".picker-folder")?.textContent, "/work/docs");
    assert.equal(messageBox(container).value, "api  web", "the message starts with the asked words, not typed ones");
  }));

test("PickerStartForm: Start sends Control.Start with the folder, message and a 32-hex id", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    server.replies.set(startPath, { status: 200, body: { id: "sess-new" } });
    const started: string[] = [];
    await mountPalette(container, server, () => {}, () => {}, (session) => started.push(session));
    await askAndChoose(t.signal, container, "web", 0);
    await typeMessage(container, "fix the build");
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the start reports its session");
    assert.deepEqual(started, ["sess-new"]);
    const [call] = server.callsTo(startPath);
    assert.match(String(call.body.id), /^[0-9a-f]{32}$/, "the start id is 32 lowercase hex digits");
    assert.deepEqual(
      call.body,
      { dir: "/work/web", prompt: "fix the build", id: call.body.id },
      "the call names the folder, the message and the id, with no model",
    );
  }));

test("PickerStartForm: a retry after a failed Start sends the same id, also after the message was edited", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    server.replies.set(startPath, { status: 503, body: { code: "unavailable", message: "queue unreachable" } });
    const started: string[] = [];
    await mountPalette(container, server, () => {}, () => {}, (session) => started.push(session));
    await askAndChoose(t.signal, container, "web", 0);
    await typeMessage(container, "first words");
    await click(startButton(container));
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the failed start shows its line");

    await typeMessage(container, "edited words");
    server.replies.set(startPath, { status: 200, body: { id: "sess-new" } });
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the retry reports its session");
    assert.deepEqual(started, ["sess-new"]);

    const [first, retry] = server.callsTo(startPath);
    assert.equal(first.body.prompt, "first words");
    assert.equal(retry.body.prompt, "edited words");
    assert.match(String(first.body.id), /^[0-9a-f]{32}$/, "the start id is 32 lowercase hex digits");
    assert.equal(retry.body.id, first.body.id, "the retry names the same start id");
  }));

test("PickerStartForm: a refused Start keeps the message and shows the fixed line, none of the server's text", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    server.replies.set(startPath, {
      status: 400,
      body: { code: "failed_precondition", message: "directory is refused: secret detail" },
    });
    await mountPalette(container, server);
    await askAndChoose(t.signal, container, "web", 0);
    await typeMessage(container, "keep this");
    await click(startButton(container));
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the refusal shows its line");
    assert.deepEqual(alertTexts(container), ["the picker failed"]);
    assert.equal(container.textContent?.includes("secret detail"), false, "the server's text is not shown");
    assert.equal(messageBox(container).value, "keep this", "the form keeps the message as typed");
  }));
