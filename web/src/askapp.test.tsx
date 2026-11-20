import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import type { Row } from "./rows";
import { installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until as waitUntil } from "./testwait";

// now is the App's clock in unix seconds. It is far enough from zero that a question two days old has waited a day or
// more.
const now = 2_000_000_000;
const day = 86400;
const answerPath = "/orrery.v1.Control/Answer";
const notNowPath = "/orrery.v1.Control/NotNow";
const bringBackPath = "/orrery.v1.Control/BringBack";
const declinePath = "/orrery.v1.Control/Decline";
const archivePath = "/orrery.v1.Epics/Archive";
const epicGetPath = "/orrery.v1.Epics/Get";
const epicAnswerPath = "/orrery.v1.Epics/Answer";
const sendKeysPath = "/orrery.v1.Panes/SendKeys";
const paneWatchPath = "/orrery.v1.Panes/WatchConversation";
const sessionWatchPath = "/orrery.v1.Sessions/WatchConversation";
const refusedWords = "Not answered: the question changed before your answer arrived. This is what it asks now.";
// The turns a test lets a write it started run to its end before it reads that nothing was sent.
const flushRounds = 10;

// Call is one request the fake server received: its path and its JSON body, and for a stream, the controller that feeds
// it.
interface Call {
  path: string;
  body: Record<string, unknown>;
  controller: ReadableStreamDefaultController<Uint8Array> | null;
}

// FakeServer answers the App's calls on the Connect wire. Each unary call is recorded and answers an empty message,
// unless refuseNext queues a refusal for its path once, or holdNext holds its next answer until the returned release
// runs. Each WatchConversation is a stream the test feeds through emit, and closeStreams ends the ones still open.
class FakeServer {
  readonly calls: Call[] = [];
  readonly refuseNext = new Map<string, string>();
  // replies holds the body a unary call to a path answers with, which is an empty message when none is set.
  readonly replies = new Map<string, object>();
  private readonly holds = new Map<string, Promise<void>>();

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    const bytes = new Uint8Array(await request.arrayBuffer());
    if (path.endsWith("/WatchConversation")) {
      const call: Call = { path, body: {}, controller: null };
      this.calls.push(call);
      const body = new ReadableStream<Uint8Array>({
        start: (controller) => {
          call.controller = controller;
        },
      });
      return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
    }
    const body = bytes.byteLength === 0 ? {} : JSON.parse(new TextDecoder().decode(bytes));
    const call: Call = { path, body, controller: null };
    this.calls.push(call);
    const gate = this.holds.get(path);
    if (gate !== undefined) {
      this.holds.delete(path);
      await gate;
    }
    const refusal = this.refuseNext.get(path);
    if (refusal !== undefined) {
      this.refuseNext.delete(path);
      return new Response(JSON.stringify({ code: "failed_precondition", message: refusal }), {
        status: 400,
        headers: { "content-type": "application/json" },
      });
    }
    const reply = this.replies.get(path) ?? {};
    return new Response(JSON.stringify(reply), { status: 200, headers: { "content-type": "application/json" } });
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  callsTo(path: string): Call[] {
    return this.calls.filter((call) => call.path === path);
  }

  // holdNext makes the next call to path wait until the returned release runs, so a test acts while the call is
  // pending.
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

  // emit sends one message down a stream the App opened.
  emit(call: Call, message: object): void {
    if (call.controller === null) {
      throw new Error(`the stream to ${call.path} has ended`);
    }
    const payload = new TextEncoder().encode(JSON.stringify(message));
    const bytes = new Uint8Array(5 + payload.byteLength);
    new DataView(bytes.buffer).setUint32(1, payload.byteLength);
    bytes.set(payload, 5);
    call.controller.enqueue(bytes);
  }

  closeStreams(): void {
    for (const call of this.calls) {
      call.controller?.close();
      call.controller = null;
    }
  }
}

// withApp mounts the app over a fake server in a fresh DOM and runs check. The app is unmounted, its streams closed and
// the DOM removed when the test ends, or as soon as signal aborts if it times out first.
async function withApp(
  signal: AbortSignal,
  rows: Row[],
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
    await act(() => {
      render(<App rows={rows} now={now} clients={fake.clients} />, container);
    });
    await check(container, fake);
  } finally {
    await teardown();
  }
}

function row(init: Partial<Row> & Pick<Row, "key">): Row {
  return {
    putOff: false,
    tool: "",
    id: init.key,
    archiveId: "",
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
    ...init,
  };
}

// headless is a headless session row that is waiting on its owner, and archives as a session.
function headless(init: Partial<Row> & Pick<Row, "key">): Row {
  return row({
    kind: "session",
    archiveKind: "session",
    archiveId: init.key,
    state: "waiting",
    changed: now - 60,
    seen: now - 60,
    ...init,
  });
}

// paneQuestion is a waiting pane whose stored question offers two answers, Apple and Pear.
const paneQuestion = JSON.stringify({
  questions: [
    {
      question: "Which fruit?",
      options: [
        { label: "Apple", description: "red" },
        { label: "Pear", description: "green" },
      ],
    },
  ],
});

// pane is a waiting pane with paneQuestion, which carries no orrery id and so no archive kind.
function pane(init: Partial<Row> & Pick<Row, "key">): Row {
  return row({
    label: "api",
    state: "waiting",
    question: paneQuestion,
    questionHash: "qh-1",
    changed: now - 60,
    seen: now - 60,
    ...init,
  });
}

// requestStream is a headless session's stream holding one question with two options, Yes and No.
const requestStream = {
  transcript: "t1",
  seq: "1",
  reset: true,
  items: [],
  state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"] } },
};

// until waits for check, one turn of the event loop at a time, so the renders it lets through are applied and the wait
// reads no clock.
function until(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return waitUntil(signal, check, what, { step: () => act(turn) });
}

// flush lets the writes a key or click started run to their end before a test reads that nothing was sent.
async function flush(): Promise<void> {
  for (let round = 0; round < flushRounds; round++) {
    await act(turn);
  }
}

async function press(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

async function click(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

// askOf is the question the reader draws, or null when it draws none.
function askOf(container: HTMLElement): HTMLElement | null {
  return container.querySelector<HTMLElement>("section.ask");
}

// optionsOf is the Ask's option buttons, in order.
function optionsOf(container: HTMLElement): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".ask .opts button.opt")];
}

// buttonIn finds the Ask's button whose text includes text.
function buttonIn(container: HTMLElement, text: string): HTMLButtonElement {
  const button = [...container.querySelectorAll<HTMLButtonElement>(".ask button")].find((candidate) =>
    (candidate.textContent ?? "").includes(text),
  );
  assert.ok(button, `the Ask draws a button reading ${text}`);
  return button;
}

// moreButton is the Ask's More for this question button.
function moreButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>('.ask button[aria-label="More for this question"]');
  assert.ok(button, "the Ask draws More for this question");
  return button;
}

// declineItem is the More menu's Decline and end the turn item, once More is open.
function declineItem(container: HTMLElement): HTMLButtonElement {
  const item = [...container.querySelectorAll<HTMLButtonElement>('.ask [role="menuitem"]')].find(
    (candidate) => candidate.querySelector("span")?.textContent === "Decline and end the turn",
  );
  assert.ok(item, "More holds Decline and end the turn");
  return item;
}

// errorNote is the Ask's error note's text, or "" when it draws none.
function errorNote(container: HTMLElement): string {
  return container.querySelector(".ask p.inote.error")?.textContent ?? "";
}

// sendingNow says whether the Ask is sending an answer: its foot reads sending.
function sendingNow(container: HTMLElement): boolean {
  return [...container.querySelectorAll(".ask .inote")].some((note) => note.textContent === "sending…");
}

test("AskAnswer: a tap on a session's option answers by Control.Answer with its request id and words", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(optionsOf(container)[1]);
    await flush();
    assert.equal(fake.callsTo(answerPath).length, 1, "the answer was sent");
    assert.deepEqual(fake.callsTo(answerPath)[0].body, { id: "s1", requestId: "req-7", answer: "No" });
  }));

test("AskAnswer: a tap on a pane's option sends its digit with the question hash by Panes.SendKeys", (t) =>
  withApp(t.signal, [pane({ key: "a" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(optionsOf(container)[1]);
    await flush();
    assert.equal(fake.callsTo(sendKeysPath).length, 1, "the answer was sent");
    assert.deepEqual(fake.callsTo(sendKeysPath)[0].body, { pane: "a", keys: ["2"], expectQuestion: "qh-1" });
  }));

// digits are the keys a conversation answers with: 1 to 9.
const digits = [1, 2, 3, 4, 5, 6, 7, 8, 9];
// nineOptions are the labels of a question with nine options, Option 1 to Option 9.
const nineOptions = digits.map((digit) => `Option ${digit}`);
// nineOptionStream is a headless session's stream holding one question with nine options.
const nineOptionStream = {
  ...requestStream,
  state: { request: { id: "req-7", kind: "question", question: "Pick one", options: nineOptions } },
};
// nineOptionPane is a waiting pane whose stored question offers nine options.
const nineOptionPane = JSON.stringify({
  questions: [
    {
      question: "Which fruit?",
      options: nineOptions.map((label) => ({ label, description: "" })),
    },
  ],
});

for (const digit of digits) {
  test(`AskAnswer: key ${digit} answers the conversation's question with option ${digit} in one press`, (t) =>
    withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
      await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
      fake.emit(fake.callsTo(sessionWatchPath)[0], nineOptionStream);
      await flush();
      assert.ok(askOf(container) !== null, "the Ask is drawn");
      await press(String(digit));
      await flush();
      assert.deepEqual(
        fake.callsTo(answerPath).map((call) => call.body),
        [{ id: "s1", requestId: "req-7", answer: nineOptions[digit - 1] }],
        "the answer with that option was sent once",
      );
    }));

  test(`AskAnswer: key ${digit} on a pane sends that digit with the question hash by Panes.SendKeys`, (t) =>
    withApp(t.signal, [pane({ key: "a", question: nineOptionPane })], async (container, fake) => {
      await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
      fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
      await flush();
      assert.ok(askOf(container) !== null, "the Ask is drawn");
      await press(String(digit));
      await flush();
      assert.deepEqual(
        fake.callsTo(sendKeysPath).map((call) => call.body),
        [{ pane: "a", keys: [String(digit)], expectQuestion: "qh-1" }],
        "the digit was sent once with the question hash",
      );
    }));
}

// epicQuestionBlock is the transcript's event that an epic asked its question Q1 with, as the server writes it.
const epicQuestionBlock = `<orrery-event kind="question" id="epic:orrery-1a2b3c4/planner-runner:Q1">
orrery-1a2b3c4/planner-runner (Plan the runner) asks a question.
Q1 which-store: which store?
</orrery-event>`;

// epicGetReply is the epic's read: it is live and holds Q1 open, with two choices.
const epicGetReply = {
  epic: {
    id: "orrery-1a2b3c4/planner-runner",
    repository: "orrery-1a2b3c4",
    slug: "planner-runner",
    outcome: "accepted",
    live: true,
    source: "orreryd",
  },
  questions: [
    {
      id: "Q1",
      text: "Which index should it add?",
      turn: "1",
      asked: "1",
      label: "which-store",
      choices: [
        { id: "composite", does: "Add a composite index." },
        { id: "none", does: "Leave the table as it is." },
      ],
    },
  ],
};

test("AskAnswer: with an epic question's Ask in the transcript, key 7 answers the conversation's question and picks nothing in the epic's Ask", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    fake.replies.set(epicGetPath, epicGetReply);
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      ...nineOptionStream,
      items: [{ kind: "prompt", time: "1", text: epicQuestionBlock }],
    });
    await flush();
    assert.ok(container.querySelector(".now-block .ask") !== null, "the epic's Ask is drawn in the transcript");
    assert.ok(askOf(container) !== null, "the conversation's Ask is drawn");
    await press("7");
    await flush();
    assert.deepEqual(
      fake.callsTo(answerPath).map((call) => call.body),
      [{ id: "s1", requestId: "req-7", answer: "Option 7" }],
      "the conversation's question was answered with option 7",
    );
    assert.deepEqual(fake.callsTo(epicAnswerPath), [], "no epic answer was sent");
    assert.equal(container.querySelector(".now-block .opt.picked"), null, "the epic's Ask picked nothing");
  }));

test("AskAnswer: a digit past the last option sends nothing", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await press("3");
    await flush();
    assert.deepEqual(fake.callsTo(answerPath), []);
  }));

test("AskAnswer: a press while an answer is in flight sends nothing", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    const release = fake.holdNext(answerPath);
    await click(optionsOf(container)[0]);
    await flush();
    assert.ok(sendingNow(container), "the sending foot is drawn");
    await click(optionsOf(container)[1]);
    await press("2");
    await flush();
    release();
    await flush();
    assert.equal(fake.callsTo(answerPath).length, 1, "only the first press was sent");
  }));

test("AskAnswer: a refused answer keeps the Ask open with the moved-on words, and its options answer again", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    fake.refuseNext.set(answerPath, "the question moved on");
    await click(optionsOf(container)[0]);
    await flush();
    assert.ok(errorNote(container) !== "", "the refusal's note is drawn");
    assert.equal(errorNote(container), refusedWords);
    assert.ok(askOf(container) !== null, "the Ask stays open");
    assert.equal(container.querySelector(".ask .opt.picked"), null, "no option holds the focus ring");
    await click(optionsOf(container)[0]);
    await flush();
    assert.equal(fake.callsTo(answerPath).length, 2, "the second answer was sent");
  }));

test("AskAnswer: the answered line draws only once the stream drops the question", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    fake.emit(stream, requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(optionsOf(container)[0]);
    await flush();
    assert.equal(fake.callsTo(answerPath).length, 1, "the answer was sent");
    await flush();
    assert.equal(container.querySelector(".ask.folded"), null, "the stream still holds the question");
    fake.emit(stream, { transcript: "t1", seq: "2", items: [], state: {} });
    await flush();
    assert.ok(container.querySelector(".ask.folded") !== null, "the answered line is drawn");
    const line = container.querySelector(".ask.folded")?.textContent ?? "";
    assert.ok(line.includes("You answered"));
    assert.ok(line.includes("Yes"));
    assert.ok(line.includes("Pick one"));
    assert.equal(askOf(container), null, "the Ask is folded into its line");
  }));

test("AskAnswer: a stream that drops the question before the answer's reply lands still folds to the answered line", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    const stream = fake.callsTo(sessionWatchPath)[0];
    fake.emit(stream, requestStream);
    await flush();
    const release = fake.holdNext(answerPath);
    await click(optionsOf(container)[0]);
    await flush();
    fake.emit(stream, { transcript: "t1", seq: "2", items: [], state: {} });
    await flush();
    assert.equal(container.querySelector(".ask.folded"), null, "no answered line before the server took the answer");
    release();
    await flush();
    await flush();
    assert.ok(container.querySelector(".ask.folded") !== null, "the answered line is drawn once the reply lands");
  }));

test("NotNow: Not now on a pane's question calls Control.NotNow with its kind, id, hash and changed time", (t) =>
  withApp(t.signal, [pane({ key: "a" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(buttonIn(container, "Not now"));
    await flush();
    assert.equal(fake.callsTo(notNowPath).length, 1, "the Not now call was sent");
    assert.deepEqual(fake.callsTo(notNowPath)[0].body, {
      kind: "pane",
      id: "a",
      questionHash: "qh-1",
      changed: String(now - 60),
    });
  }));

test("NotNow: Not now on a headless session's question calls Control.NotNow by its request id", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(buttonIn(container, "Not now"));
    await flush();
    assert.equal(fake.callsTo(notNowPath).length, 1, "the Not now call was sent");
    assert.deepEqual(fake.callsTo(notNowPath)[0].body, { kind: "headless", id: "s1", requestId: "req-7" });
  }));

test("NotNow: a put-off question is drawn from the stream, and Bring it back calls Control.BringBack", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], {
      ...requestStream,
      state: { request: { id: "req-7", kind: "question", question: "Pick one", options: ["Yes", "No"], putOff: true } },
    });
    await flush();
    assert.ok(container.querySelector(".ask.later") !== null, "the put-off Ask is drawn");
    assert.ok(askOf(container)?.textContent?.includes("Put off"));
    await click(buttonIn(container, "Bring it back"));
    await flush();
    assert.equal(fake.callsTo(bringBackPath).length, 1, "the Bring back call was sent");
    assert.deepEqual(fake.callsTo(bringBackPath)[0].body, { kind: "headless", id: "s1", requestId: "req-7" });
  }));

test("NotNow: a refused Not now leaves the Ask as it was and shows the server's reason as its note", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    fake.refuseNext.set(notNowPath, "the question was already answered");
    await click(buttonIn(container, "Not now"));
    await flush();
    assert.ok(errorNote(container) !== "", "the reason is drawn");
    assert.equal(errorNote(container), "the question was already answered");
    assert.equal(askOf(container)?.classList.contains("later"), false, "the Ask is not put off");
  }));

test("Decline: More's Decline calls Control.Decline with the request id and kind", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    await click(moreButton(container));
    await click(declineItem(container));
    await flush();
    assert.equal(fake.callsTo(declinePath).length, 1, "the Decline call was sent");
    assert.deepEqual(fake.callsTo(declinePath)[0].body, { kind: "headless", id: "s1", requestId: "req-7" });
  }));

test("Decline: a refused Decline shows the server's reason as the Ask's note", (t) =>
  withApp(t.signal, [headless({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    fake.refuseNext.set(declinePath, "the turn already ended");
    await click(moreButton(container));
    await click(declineItem(container));
    await flush();
    assert.ok(errorNote(container) !== "", "the reason is drawn");
    assert.equal(errorNote(container), "the turn already ended");
  }));

// staleRow is a headless session asked two days before now: a question that has waited a day or more.
function staleRow(init: Partial<Row> & Pick<Row, "key">): Row {
  return headless({ changed: now - 2 * day, seen: now - 2 * day, ...init });
}

test("Decline: Archive this session on a stale question calls Epics.Archive with its kind and id", (t) =>
  withApp(t.signal, [staleRow({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    assert.ok(askOf(container)?.textContent?.includes("Asked 2 days ago and still waiting."));
    await click(buttonIn(container, "Archive this session"));
    await flush();
    assert.equal(fake.callsTo(archivePath).length, 1, "the archive call was sent");
    assert.deepEqual(fake.callsTo(archivePath)[0].body, { kind: "session", id: "s1" });
  }));

test("Decline: a pane with no orrery id draws its stale note and no Archive this session button", (t) =>
  withApp(t.signal, [pane({ key: "a", changed: now - 2 * day, seen: now - 2 * day })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(paneWatchPath).length === 1, "the pane stream");
    fake.emit(fake.callsTo(paneWatchPath)[0], { transcript: "t1", seq: "1", reset: true, items: [] });
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    assert.ok(askOf(container)?.textContent?.includes("Asked 2 days ago and still waiting."));
    const archive = [...container.querySelectorAll(".ask button")].find((button) =>
      (button.textContent ?? "").includes("Archive this session"),
    );
    assert.equal(archive, undefined, "no archive button");
    assert.deepEqual(fake.callsTo(archivePath), []);
  }));

test("Decline: a refused Archive shows the server's reason as the Ask's note", (t) =>
  withApp(t.signal, [staleRow({ key: "s1", id: "s1", label: "notes" })], async (container, fake) => {
    await until(t.signal, () => fake.callsTo(sessionWatchPath).length === 1, "the session stream");
    fake.emit(fake.callsTo(sessionWatchPath)[0], requestStream);
    await flush();
    assert.ok(askOf(container) !== null, "the Ask is drawn");
    fake.refuseNext.set(archivePath, "the session is running");
    await click(buttonIn(container, "Archive this session"));
    await flush();
    assert.ok(errorNote(container) !== "", "the reason is drawn");
    assert.equal(errorNote(container), "the session is running");
  }));
