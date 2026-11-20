import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { NewPanel } from "./picker";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { mockTimers } from "./testclock";
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
const continueRow = {
  token: "tok-continue",
  kind: "continue",
  dir: "/work/ops",
  pane: "",
  label: "",
  note: "continue sess-9",
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
    tool: "",
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
// A path that silence names never answers: its call ends only when the call's deadline aborts it. A path that hold
// names waits for the release hold returns before it answers, once.
class FakeServer {
  readonly calls: Call[] = [];
  readonly replies = new Map<string, Reply>();
  readonly silent = new Set<string>();
  readonly held = new Map<string, Promise<void>>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    this.calls.push({ path, body: JSON.parse(new TextDecoder().decode(bytes)) });
    if (this.silent.has(path)) {
      await new Promise((_, reject) => {
        request.signal.addEventListener("abort", () => reject(request.signal.reason), { once: true });
      });
    }
    const release = this.held.get(path);
    if (release !== undefined) {
      this.held.delete(path);
      await release;
    }
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

  // hold makes the next call to path wait until the release it returns is called.
  hold(path: string): () => void {
    let release = () => {};
    this.held.set(
      path,
      new Promise<void>((resolve) => {
        release = resolve;
      }),
    );
    return release;
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
  sheet = false,
): Promise<void> {
  await act(() => {
    render(
      <NewPanel
        clients={server.clients}
        rows={rows}
        sheet={sheet}
        onRun={onRun}
        onStart={onStart}
        onClose={onClose}
      />,
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
  return [...container.querySelectorAll<HTMLButtonElement>(".pick-row")];
}

// rowTexts is the title and note of each row the palette lists, in order.
function rowTexts(container: HTMLElement): string[][] {
  return rowButtons(container).map((button) => [
    button.querySelector(".t")?.textContent ?? "",
    button.querySelector(".n")?.textContent ?? "",
  ]);
}

// dialogOf is the New session dialog, which is drawn as a panel or a sheet.
function dialogOf(container: HTMLElement): HTMLElement {
  const dialog = container.querySelector<HTMLElement>('[role="dialog"][aria-label="New session"]');
  assert.ok(dialog, "the New session dialog is drawn");
  return dialog;
}

// hintOf is the one note in the hint's place: the hint, a nothing-found line or a failure.
function hintOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("p.inote");
}

// countOf is the count the field ends with: asking… while an ask runs, else the Enter key.
function countOf(container: HTMLElement): string | null {
  return container.querySelector(".field > :last-child")?.textContent ?? null;
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

async function pressKey(element: Element, key: string, init: KeyboardEventInit = {}): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
  });
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

// pressArrow sends an arrow key and resolves whether a handler prevented it, so the caret is left where it was.
async function pressArrow(element: Element, key: string): Promise<boolean> {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  await act(() => {
    element.dispatchEvent(event);
  });
  return event.defaultPrevented;
}

// currentIndex is the index of the row marked current, or -1 when none is.
function currentIndex(container: HTMLElement): number {
  return rowButtons(container).findIndex((button) => button.classList.contains("cur"));
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
    assert.equal(row.querySelector(".t .title-dir")?.textContent, "billing", "the listed directory");
    assert.equal(row.querySelector(".t .title-line")?.textContent, "retry the webhook", "the listed summary");
    assert.equal(row.querySelector(".n")?.textContent, "waiting on you", "the note follows the title");
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
    assert.equal(row.querySelector(".t .title-dir")?.textContent, "web", "the folder's directory");
    assertNone(row.querySelector(".t .title-line"), "a folder row has no line");
    assert.equal(row.querySelector(".n")?.textContent, "start fresh");
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

test("NewPanel: at 760px and wider it draws a panel titled New session, with the N or ⌘K hint and a Close button", (t) =>
  withDom(t.signal, async (container, server) => {
    let closes = 0;
    await mountPalette(container, server, () => {}, () => {
      closes += 1;
    });
    const dialog = dialogOf(container);
    assert.equal(dialog.className, "panel", "the dialog is a panel");
    assert.equal(dialog.parentElement?.className, "scrim", "the panel dims the page behind it");
    assert.equal(dialog.querySelector("h2.t-title")?.textContent, "New session");
    const hint = dialog.querySelector(".head .t-small");
    assert.equal(hint?.textContent, "N or ⌘K", "the hint names both keys");
    assert.deepEqual([...(hint?.querySelectorAll("kbd") ?? [])].map((key) => key.textContent), ["N", "⌘K"]);
    const close = dialog.querySelector<HTMLButtonElement>('.head button[aria-label="Close"]');
    assert.ok(close, "the panel has its Close button");
    assert.equal(close.title, "Close  esc", "the Close button's tip names esc");
    await click(close);
    assert.equal(closes, 1, "Close closes the panel");
  }));

test("NewPanel: as a sheet it draws a sheet titled New session with its body, and no head or Close button", (t) =>
  withDom(t.signal, async (container, server) => {
    await mountPalette(container, server, () => {}, () => {}, () => {}, [], true);
    const dialog = dialogOf(container);
    assert.equal(dialog.className, "sheet", "the dialog is a sheet");
    assert.equal(dialog.parentElement?.className, "sheet-scrim");
    assert.equal(dialog.querySelector(".sheet-head b")?.textContent, "New session");
    assert.ok(dialog.querySelector(".sheet-body .field input"), "the field is in the sheet's body");
    assertNone(dialog.querySelector("h2"), "a sheet has no heading");
    assertNone(dialog.querySelector('button[aria-label="Close"]'), "a sheet has no Close button");
  }));

test("NewPanel: Escape closes the sheet as it closes the panel", (t) =>
  withDom(t.signal, async (container, server) => {
    let closes = 0;
    await mountPalette(container, server, () => {}, () => {
      closes += 1;
    }, () => {}, [], true);
    await pressKey(input(container), "Escape");
    assert.equal(closes, 1);
  }));

test("NewPanel: the field has focus, its placeholder and label, and the hint shows before anything is asked", (t) =>
  withDom(t.signal, async (container, server) => {
    await mountPalette(container, server);
    const box = input(container);
    assert.equal(document.activeElement, box, "the field has focus on open");
    assert.equal(box.placeholder, "A directory, a session, or what you want to start");
    assert.equal(box.getAttribute("aria-label"), "New session");
    assert.equal(countOf(container), "⏎", "the field ends with the Enter key before an ask");
    assert.equal(hintOf(container)?.textContent, "Name a directory to start a session there, or say a few words about a session to find it.");
    assert.equal(hintOf(container)?.className, "inote", "the hint is not an error");
    assert.equal(rowButtons(container).length, 0, "nothing is listed before an ask");
    assert.deepEqual(server.calls, [], "opening the panel asks nothing");
  }));

test("NewPanel: Enter sends the words to Picker.Ask with a 90 second deadline, and never a blank field", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await pressKey(input(container), "Enter");
    await typeWords(container, "   ");
    await pressKey(input(container), "Enter");
    assert.deepEqual(server.calls, [], "a blank field sends nothing");

    await typeWords(container, "api  web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the answer is listed");
    assert.deepEqual(server.callsTo(askPath).map((call) => call.body), [{ text: "api  web" }]);
  }));

test("NewPanel: while an ask runs the field reads asking…, nothing is listed or chosen, and Enter sends nothing", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, freshRow] } });
    const release = server.hold(askPath);
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => server.callsTo(askPath).length === 1, "the ask is sent");
    assert.equal(countOf(container), "asking…", "the field says asking… while the ask runs");
    assert.equal(hintOf(container), null, "no hint while the ask runs");
    assert.equal(rowButtons(container).length, 0, "no row is listed while the ask runs");

    await pressKey(input(container), "Enter");
    assert.equal(server.callsTo(askPath).length, 1, "Enter while the ask runs sends nothing");

    release();
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows arrive");
    assert.equal(countOf(container), "⏎", "the field ends with the Enter key again");
    assert.equal(server.callsTo(askPath).length, 1);
  }));

test("NewPanel: an ask that starts while a start form is open closes the form, and no Start is offered while it runs", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await askAndChoose(t.signal, container, "web", 0);
    startForm(container);

    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    const release = server.hold(askPath);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => server.callsTo(askPath).length === 2, "the second ask is sent");
    try {
      assertNone(container.querySelector(".picker-start"), "the start form closes while the ask runs");
      assert.equal(startButtonOf(container), undefined, "no Start button is drawn while the ask runs");
    } finally {
      release();
    }
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the rows arrive");
    assertNone(container.querySelector(".picker-start"), "the start form stays closed after the answer");
  }));

test("NewPanel: an ask the server does not answer in 90 seconds ends as DeadlineExceeded with its sentence", (t) => {
  mockTimers(t, ["setTimeout"]);
  return withDom(t.signal, async (container, server) => {
    server.silent.add(askPath);
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => server.callsTo(askPath).length === 1, "the ask is sent");

    t.mock.timers.tick(89_999);
    assert.equal(countOf(container), "asking…", "the ask still runs one millisecond short of 90 seconds");
    assert.deepEqual(alertTexts(container), [], "nothing has failed yet");

    t.mock.timers.tick(1);
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the deadline shows its sentence");
    assert.deepEqual(alertTexts(container), [
      "The search did not finish: the server did not answer in 90 seconds.",
    ]);
    assert.equal(countOf(container), "⏎", "the field is no longer asking");
  });
});

// askFailures are the sentences a failed Ask shows, by the code the server answers with, and the status it answers with.
const askFailures: [string, number, string][] = [
  ["deadline_exceeded", 504, "The search did not finish: the server did not answer in 90 seconds."],
  ["unavailable", 503, "The search did not finish: the server could not answer."],
  ["not_found", 404, "New is not set up on this server."],
  ["internal", 500, "The search did not finish."],
];

for (const [code, status, sentence] of askFailures) {
  test(`NewPanel: a failed Ask with ${code} shows its sentence, none of the server's text, and keeps the words`, (t) =>
    withDom(t.signal, async (container, server) => {
      server.replies.set(askPath, { status, body: { code, message: "text is refused: secret detail" } });
      await mountPalette(container, server);
      await typeWords(container, "api");
      await pressKey(input(container), "Enter");
      await untilTurn(t.signal, () => alertTexts(container).length === 1, "the failure shows its sentence");
      assert.deepEqual(alertTexts(container), [sentence]);
      assert.equal(hintOf(container)?.className, "inote error", "the failure takes the hint's place");
      assert.equal(hintOf(container)?.getAttribute("role"), "alert");
      assert.equal(container.textContent?.includes("secret detail"), false, "the server's text is not shown");
      assert.equal(input(container).value, "api", "the field keeps the words after a failure");
    }));
}

test("NewPanel: an ask that finds nothing says so with the words asked, and keeps the field's words", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [] } });
    await mountPalette(container, server);
    await typeWords(container, "api  web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => hintOf(container)?.textContent?.startsWith("Nothing found") === true, "the nothing-found line shows");
    assert.equal(hintOf(container)?.textContent, "Nothing found for “api  web”. Try a directory name.");
    assert.equal(hintOf(container)?.className, "inote", "nothing found is not an error");
    assert.equal(hintOf(container)?.getAttribute("role") ?? null, null, "nothing found is not an alert");
    assert.deepEqual(alertTexts(container), []);
    assert.equal(input(container).value, "api  web", "the field keeps the words");
  }));

// startForm is the start form the palette draws for a folder row, which it draws only while the form is open.
function startForm(container: HTMLElement): HTMLElement {
  const form = container.querySelector<HTMLElement>(".picker-start");
  assert.ok(form, "the start form is open");
  return form;
}

// directoryBox is the start form's directory field, which holds the folder read-only.
function directoryBox(container: HTMLElement): HTMLInputElement {
  const box = container.querySelector<HTMLInputElement>('[aria-label="Directory"]');
  assert.ok(box, "the start form has its directory field");
  return box;
}

function messageBox(container: HTMLElement): HTMLTextAreaElement {
  const box = container.querySelector<HTMLTextAreaElement>('[aria-label="message"]');
  assert.ok(box, "the start form has its message box");
  return box;
}

// startButtonOf is the Start the session button of the start form, or undefined when the form draws none.
function startButtonOf(container: HTMLElement): HTMLButtonElement | undefined {
  return [...container.querySelectorAll<HTMLButtonElement>("button")].find((button) =>
    button.textContent?.startsWith("Start the session"),
  );
}

function startButton(container: HTMLElement): HTMLButtonElement {
  const found = startButtonOf(container);
  assert.ok(found, "the start form has its Start the session button");
  return found;
}

function otherResultsButton(container: HTMLElement): HTMLButtonElement {
  const found = [...container.querySelectorAll<HTMLButtonElement>("button")].find(
    (button) => button.textContent === "Other results",
  );
  assert.ok(found, "the start form has its Other results button");
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

test("NewPanel: choosing a folder row opens the start form in place of the results, with the folder read-only and an empty message", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await askAndChoose(t.signal, container, "web", 0);
    assert.equal(directoryBox(container).value, "/work/web", "the form names the folder");
    assert.equal(directoryBox(container).readOnly, true, "the folder is a read-only field");
    assert.equal(messageBox(container).value, "", "a row without a prompt starts the message empty");
    assertNone(container.querySelector(".pick-row"), "the form replaces the results");
    assert.equal(otherResultsButton(container).textContent, "Other results");
    assert.deepEqual(server.callsTo(runPath), [], "a folder row runs nothing");
  }));

test("NewPanel: a prompt row's message starts with the words the palette last asked", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [promptRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api  web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the row is listed");
    await typeWords(container, "typed but not asked");
    await click(rowButtons(container)[0]);
    assert.equal(directoryBox(container).value, "/work/docs");
    assert.equal(messageBox(container).value, "api  web", "the message starts with the asked words, not typed ones");
  }));

test("NewPanel: Start the session sends Control.Start with the folder, message and a 32-hex id", (t) =>
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

test("NewPanel: a retry after a failed Start sends the same id, also after the message was edited", (t) =>
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

test("NewPanel: flipping between the panel and the sheet keeps the start form, its message and its start id", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    server.replies.set(startPath, { status: 503, body: { code: "unavailable", message: "queue unreachable" } });
    const started: string[] = [];
    const onStart = (session: string) => started.push(session);
    await mountPalette(container, server, () => {}, () => {}, onStart);
    await askAndChoose(t.signal, container, "web", 0);
    await typeMessage(container, "keep this");
    await click(startButton(container));
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the refused start shows its line");

    await mountPalette(container, server, () => {}, () => {}, onStart, [], true);
    assert.equal(messageBox(container).value, "keep this", "the sheet keeps the message");
    await mountPalette(container, server, () => {}, () => {}, onStart, [], false);
    assert.equal(messageBox(container).value, "keep this", "the panel keeps the message");

    server.replies.set(startPath, { status: 200, body: { id: "sess-new" } });
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the retry reports its session");
    const [first, retry] = server.callsTo(startPath);
    assert.equal(retry.body.prompt, "keep this", "the retry sends the kept message");
    assert.equal(retry.body.id, first.body.id, "the retry after the flips names the same start id");
  }));

test("NewPanel: a refused Start keeps the message and shows the fixed line, none of the server's text", (t) =>
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
    assert.deepEqual(alertTexts(container), ["The session did not start."]);
    assert.equal(container.textContent?.includes("secret detail"), false, "the server's text is not shown");
    assert.equal(messageBox(container).value, "keep this", "the form keeps the message as typed");
    assert.equal(startButton(container).disabled, false, "the form can be started again");
  }));

test("NewPanel: Other results goes back to the results as they were, with the same current result", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, freshRow, promptRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 3, "the rows are listed");
    const before = rowTexts(container);
    await pressArrow(input(container), "ArrowDown");
    await click(rowButtons(container)[1]);
    assertNone(container.querySelector(".pick-row"), "the form replaces the results");

    await click(otherResultsButton(container));
    assertNone(container.querySelector(".picker-start"), "the form closes");
    assert.deepEqual(rowTexts(container), before, "the same results are listed");
    assert.equal(currentIndex(container), 1, "the current result is the one chosen before");
    assert.deepEqual(server.callsTo(startPath), [], "Other results starts nothing");
  }));

test("NewPanel: Start the session is disabled while the message is blank, and enabled once it has words", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server);
    await askAndChoose(t.signal, container, "web", 0);
    assert.equal(startButton(container).disabled, true, "a blank message cannot start");
    await typeMessage(container, "   ");
    assert.equal(startButton(container).disabled, true, "a message of spaces cannot start");
    await pressKey(messageBox(container), "Enter", { metaKey: true });
    assert.deepEqual(server.callsTo(startPath), [], "⌘⏎ over a blank message starts nothing");

    await typeMessage(container, "fix the build");
    assert.equal(startButton(container).disabled, false, "words make the form startable");
  }));

for (const [name, modifier] of [
  ["⌘⏎", { metaKey: true }],
  ["Ctrl+⏎", { ctrlKey: true }],
] as const) {
  test(`NewPanel: ${name} in the message box starts the session at the desk, and a plain Enter does not`, (t) =>
    withDom(t.signal, async (container, server) => {
      server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
      server.replies.set(startPath, { status: 200, body: { id: "sess-new" } });
      const started: string[] = [];
      await mountPalette(container, server, () => {}, () => {}, (session) => started.push(session));
      await askAndChoose(t.signal, container, "web", 0);
      assert.equal(startButton(container).querySelector("kbd")?.textContent, "⌘⏎", "the desk names the shortcut");
      await typeMessage(container, "fix the build");
      await pressKey(messageBox(container), "Enter");
      assert.deepEqual(server.callsTo(startPath), [], "a plain Enter starts nothing");

      await pressKey(messageBox(container), "Enter", modifier);
      await untilTurn(t.signal, () => started.length === 1, "the shortcut reports its session");
      assert.deepEqual(started, ["sess-new"]);
      assert.equal(server.callsTo(startPath).length, 1, "the shortcut starts once");
    }));
}

test("NewPanel: at a narrow sheet the start form has no ⌘⏎ key and ⌘⏎ starts nothing", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await mountPalette(container, server, () => {}, () => {}, () => {}, [], true);
    await askAndChoose(t.signal, container, "web", 0);
    assertNone(startButton(container).querySelector("kbd"), "the sheet names no shortcut");
    await typeMessage(container, "fix the build");
    await pressKey(messageBox(container), "Enter", { metaKey: true });
    assert.deepEqual(server.callsTo(startPath), [], "⌘⏎ starts nothing at a sheet");
  }));

test("NewPanel: while Start runs the form reads starting…, Start is disabled, and a second Start sends nothing", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    server.replies.set(startPath, { status: 200, body: { id: "sess-new" } });
    const release = server.hold(startPath);
    const started: string[] = [];
    await mountPalette(container, server, () => {}, () => {}, (session) => started.push(session));
    await askAndChoose(t.signal, container, "web", 0);
    await typeMessage(container, "fix the build");
    await click(startButton(container));
    await untilTurn(t.signal, () => server.callsTo(startPath).length === 1, "the start is sent");
    assert.equal(container.querySelector(".inote")?.textContent, "starting…", "the form says starting… while Start runs");
    assert.equal(startButton(container).disabled, true, "Start is disabled while it runs");
    const back = [...container.querySelectorAll<HTMLButtonElement>(".foot button")].find((button) =>
      (button.textContent ?? "").includes("Other results"),
    );
    assert.equal(back?.disabled, true, "Other results is disabled while Start runs, so no second form starts a second session");
    await click(startButton(container));
    await pressKey(messageBox(container), "Enter", { metaKey: true });
    assert.equal(server.callsTo(startPath).length, 1, "a second Start sends nothing while it runs");

    release();
    await untilTurn(t.signal, () => started.length === 1, "the start reports its session");
    assert.deepEqual(started, ["sess-new"]);
    assertNone(container.querySelector(".inote"), "starting… is gone once the start has answered");
  }));

test("NewPanel: each result leads with its kind's word, then its title, its chip when it has one, and its note", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, {
      status: 200,
      body: { rows: [jumpRow, resumeRow, continueRow, freshRow, promptRow] },
    });
    await mountPalette(container, server, () => {}, () => {}, () => {}, [
      listedPane("%3", "/work/billing", "retry the webhook"),
    ]);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 5, "the rows are listed");
    assert.deepEqual(
      rowButtons(container).map((button) => [
        button.querySelector(".k")?.textContent ?? "",
        button.querySelector(".t")?.textContent ?? "",
        button.querySelector(".n")?.textContent ?? "",
      ]),
      [
        ["go to", "billing retry the webhook", "waiting on you"],
        ["resume", "docs", "resume sess-7"],
        ["continue", "ops", "continue sess-9"],
        ["start in", "web", "start fresh"],
        ["start in", "docs", "pane in docs"],
      ],
    );
    assert.deepEqual(
      rowButtons(container).map((button) => [...button.children].map((child) => child.className)),
      [
        ["k", "t", "n"],
        ["k", "t", "chip live", "n"],
        ["k", "t", "n"],
        ["k", "t", "n"],
        ["k", "t", "chip live", "n"],
      ],
    );
  }));

test("NewPanel: the chip says starts with your words on a prompt row, and on no other", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, promptRow, freshRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 3, "the rows are listed");
    assert.deepEqual(
      rowButtons(container).map((button) => button.querySelector(".chip")?.textContent ?? null),
      [null, "starts with your words", null],
    );
  }));

test("NewPanel: the first result is the current one, marked cur and aria-selected", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, resumeRow, freshRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 3, "the rows are listed");
    assert.deepEqual(
      rowButtons(container).map((button) => [button.className, button.getAttribute("aria-selected")]),
      [
        ["pick-row cur", "true"],
        ["pick-row", "false"],
        ["pick-row", "false"],
      ],
    );
  }));

test("NewPanel: ↓ and ↑ in the field move the current result, wrap at both ends, and are prevented", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, resumeRow, freshRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 3, "the rows are listed");
    const field = input(container);

    assert.equal(await pressArrow(field, "ArrowDown"), true, "↓ is prevented");
    assert.equal(currentIndex(container), 1, "↓ moves the current result down");
    await pressArrow(field, "ArrowDown");
    assert.equal(currentIndex(container), 2);
    await pressArrow(field, "ArrowDown");
    assert.equal(currentIndex(container), 0, "↓ from the last result wraps to the first");
    assert.equal(await pressArrow(field, "ArrowUp"), true, "↑ is prevented");
    assert.equal(currentIndex(container), 2, "↑ from the first result wraps to the last");
    await pressArrow(field, "ArrowUp");
    assert.equal(currentIndex(container), 1, "↑ moves the current result up");
    assert.equal(
      rowButtons(container).filter((button) => button.getAttribute("aria-selected") === "true").length,
      1,
      "exactly one result is selected",
    );
  }));

test("NewPanel: Enter chooses the current result while the field holds the words last asked", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, resumeRow] } });
    const panes: string[] = [];
    await mountPalette(container, server, (pane) => panes.push(pane));
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows are listed");

    server.replies.set(runPath, { status: 200, body: { pane: "%5" } });
    await pressArrow(input(container), "ArrowDown");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => panes.length === 1, "Enter reports the pane");
    assert.deepEqual(panes, ["%5"]);
    assert.deepEqual(server.callsTo(runPath).map((call) => call.body), [{ token: "tok-resume" }]);
    assert.equal(server.callsTo(askPath).length, 1, "Enter chose the result and did not ask again");
  }));

test("NewPanel: Enter asks again once the words change, and chooses nothing", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow] } });
    await mountPalette(container, server);
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 1, "the first answer is listed");

    server.replies.set(askPath, { status: 200, body: { rows: [freshRow] } });
    await typeWords(container, "api web");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowTexts(container)[0]?.[0] === "web", "the second answer is listed");
    assert.deepEqual(server.callsTo(askPath).map((call) => call.body), [{ text: "api" }, { text: "api web" }]);
    assert.deepEqual(server.callsTo(runPath), [], "no result is chosen");
  }));

test("NewPanel: a click chooses the row clicked, whichever result is current", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [jumpRow, resumeRow] } });
    const panes: string[] = [];
    await mountPalette(container, server, (pane) => panes.push(pane));
    await typeWords(container, "api");
    await pressKey(input(container), "Enter");
    await untilTurn(t.signal, () => rowButtons(container).length === 2, "the rows are listed");

    server.replies.set(runPath, { status: 200, body: { pane: "%5" } });
    await click(rowButtons(container)[1]);
    await untilTurn(t.signal, () => panes.length === 1, "the click reports the pane");
    assert.deepEqual(panes, ["%5"]);
    assert.deepEqual(server.callsTo(runPath).map((call) => call.body), [{ token: "tok-resume" }]);
  }));

test("NewPanel: choosing a continue row sends its token to Picker.Run and reports the pane", (t) =>
  withDom(t.signal, async (container, server) => {
    server.replies.set(askPath, { status: 200, body: { rows: [continueRow] } });
    server.replies.set(runPath, { status: 200, body: { pane: "%7" } });
    const panes: string[] = [];
    await mountPalette(container, server, (pane) => panes.push(pane));
    await askAndChoose(t.signal, container, "ops", 0);
    await untilTurn(t.signal, () => panes.length === 1, "the continue reports its pane");
    assert.deepEqual(panes, ["%7"]);
    assert.deepEqual(server.callsTo(runPath).map((call) => call.body), [{ token: "tok-continue" }]);
    assertNone(container.querySelector(".picker-start"), "a continue row opens no start form");
  }));

// runFailures are the sentences a failed Run shows, by the code the server answers with, and the status it answers with.
const runFailures: [string, number, string][] = [
  ["not_found", 404, "That result has gone stale. Ask again."],
  ["failed_precondition", 400, "It did not open: the result expired, or tmux is not running on the server."],
  ["unavailable", 503, "It did not open: the server could not run it."],
  ["internal", 500, "It did not open."],
];

for (const [code, status, sentence] of runFailures) {
  test(`NewPanel: a failed Run with ${code} shows its sentence, none of the server's text, and reports no pane`, (t) =>
    withDom(t.signal, async (container, server) => {
      server.replies.set(askPath, { status: 200, body: { rows: [jumpRow] } });
      server.replies.set(runPath, { status, body: { code, message: "text is refused: secret detail" } });
      const panes: string[] = [];
      await mountPalette(container, server, (pane) => panes.push(pane));
      await askAndChoose(t.signal, container, "api", 0);
      await untilTurn(t.signal, () => alertTexts(container).length === 1, "the failed run shows its sentence");
      assert.deepEqual(alertTexts(container), [sentence]);
      assert.equal(hintOf(container)?.className, "inote error", "the failure takes the hint's place");
      assert.equal(hintOf(container)?.getAttribute("role"), "alert");
      assert.deepEqual(panes, [], "a failed run reports no pane");
      assert.equal(container.textContent?.includes("secret detail"), false, "the server's text is not shown");
    }));
}
