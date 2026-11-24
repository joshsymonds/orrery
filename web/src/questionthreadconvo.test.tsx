import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, fromJson, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import {
  type Design,
  DesignLogLineSchema,
  DesignQuestionSchema,
  DesignSchema,
  DesignsAnswerRequestSchema,
  type DesignsAnswerRequest,
  DesignsGetRequestSchema,
  DesignsGetResponseSchema,
  type DesignsGetResponse,
  type DesignQuestion,
  RecordMakerSchema,
} from "./gen/orrery/v1/designs_pb";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name begins QuestionThread: App draws a held design beside its conversation, whose items include an asked
// design item that names Q1 and Q2. A fake transport answers the Designs calls from its record of the design, and holds the
// conversation's stream open so a test writes the items the conversation holds. The checks read the DOM only. No clock runs:
// the seen clock's timers never fire, and each wait steps by one turn of the event loop.

const now = 1000;
const desk = 1600;
const session = "sess-tide";
const getPath = "/orrery.v1.Designs/Get";
const answerPath = "/orrery.v1.Designs/Answer";
const watchPath = "/orrery.v1.Panes/WatchConversation";
const conversationRoot = ".reader";
const designRoot = ".design-page";

// paneRow is the working pane sess-tide, which the design works.
const paneRow: Row = {
  tool: "",
  id: session,
  kind: "pane",
  key: session,
  label: "tide work",
  archiveId: session,
  project: "/work/tide",
  host: "",
  state: "working",
  summary: "",
  question: "",
  questionHash: "",
  changed: 300,
  seen: 300,
  unseen: false,
  archived: false,
  putOff: false,
  archiveKind: "session",
};

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// tide is the invented design the pane works, which has no contract.
const tide: Design = create(DesignSchema, {
  id: "tide",
  title: "Tide alerts",
  conversation: session,
  created: 10n,
  state: "Asking",
});

// askedItem is the design item an asked question writes into the conversation: it names Q1 and Q2.
const askedItem = {
  kind: "design",
  time: "1000",
  text: "asked\nBump the embedded tz database?",
  design: { design: "tide", what: "asked", ids: ["Q1", "Q2"] },
};

// answeredItem is a design item that is not asked: its what, the question it answered, and its words.
const answeredItem = {
  kind: "design",
  time: "1001",
  text: "answered\nKeep the pin",
  design: { design: "tide", what: "answered", ids: ["Q1"] },
};

// transcriptOf is the stream message that holds the items given, from the start of the conversation.
function transcriptOf(items: object[]): object {
  return { transcript: "t1", seq: "1", reset: true, items };
}

// deltaOf is the stream message that adds the items given after those the conversation holds.
function deltaOf(items: object[]): object {
  return { transcript: "t1", seq: "2", reset: false, items };
}

// makerOf is who made a record of the kind given.
function makerOf(kind: string) {
  return create(RecordMakerSchema, { kind });
}

// questionOf is the design's question id: open, or settled by the answer the owner took.
function questionOf(id: bigint, taken: DesignsAnswerRequest | undefined): DesignQuestion {
  const open = {
    id,
    text: `Question ${id}?`,
    options: ["Keep the pin", "Allow the bump"],
    recommendation: "Keep the pin",
    asked: 3n,
  };
  if (taken === undefined) {
    return create(DesignQuestionSchema, open);
  }
  return create(DesignQuestionSchema, {
    ...open,
    answer: taken.accept ? "Keep the pin" : taken.answer,
    accepted: taken.accept,
    answered: 5n,
    maker: makerOf("owner"),
  });
}

// recordOf is the design's record as the server holds it now: each question the owner has answered is settled. Q1 has one
// log line, the conversation's ask.
function recordOf(taken: ReadonlyMap<bigint, DesignsAnswerRequest>): DesignsGetResponse {
  return create(DesignsGetResponseSchema, {
    design: tide,
    stage: { questions: [questionOf(1n, taken.get(1n)), questionOf(2n, taken.get(2n))] },
    log: [
      create(DesignLogLineSchema, {
        seq: 1n,
        time: 1000n,
        maker: makerOf("conversation"),
        what: "asked",
        ids: ["Q1"],
        words: "",
      }),
    ],
  });
}

// Reads is how the fake server answers a Designs.Get: with the record, with a refusal, or with no answer at all.
type Reads = "record" | "refused" | "hang";

// refusal is the failure a Designs.Get is refused with, which the App reads as a design it could not read.
const refusal = { status: 503, code: "unavailable", message: "orreryd is not answering" };

// json is a response of the value given, as JSON.
function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
}

// Fake is the server the App calls through. It records each Designs.Get's design id and each Designs.Answer's request, in
// the order the calls came, and keeps the answers the owner has taken. Each conversation stream is held open until a test
// writes a message into it with emit.
class Fake {
  readonly gets: string[] = [];
  readonly answers: DesignsAnswerRequest[] = [];
  private readonly taken = new Map<bigint, DesignsAnswerRequest>();
  private readonly watches: ReadableStreamDefaultController<Uint8Array>[] = [];
  private readonly reads: Reads;

  constructor(reads: Reads) {
    this.reads = reads;
  }

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path === watchPath) {
      const body = new ReadableStream<Uint8Array>({
        start: (controller) => {
          this.watches.push(controller);
        },
      });
      return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
    }
    if (path === getPath) {
      const { id } = fromJson(DesignsGetRequestSchema, JSON.parse(await request.text()));
      this.gets.push(id);
      if (this.reads === "hang") {
        await new Promise<never>(() => {});
      }
      if (this.reads === "refused") {
        return json({ code: refusal.code, message: refusal.message }, refusal.status);
      }
      return json(toJson(DesignsGetResponseSchema, recordOf(this.taken)));
    }
    if (path === answerPath) {
      const answer = fromJson(DesignsAnswerRequestSchema, JSON.parse(await request.text()));
      this.answers.push(answer);
      this.taken.set(answer.question, answer);
      return json({});
    }
    return json({});
  };

  readonly clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  // watchCount is how many conversation streams the App has opened.
  watchCount(): number {
    return this.watches.length;
  }

  // emit writes one message down the newest conversation stream, as a Connect envelope.
  emit(message: object): void {
    const controller = this.watches.at(-1);
    assert.ok(controller !== undefined, "the App has not opened the conversation's stream");
    const payload = new TextEncoder().encode(JSON.stringify(message));
    const bytes = new Uint8Array(5 + payload.byteLength);
    new DataView(bytes.buffer).setUint32(1, payload.byteLength);
    bytes.set(payload, 5);
    controller.enqueue(bytes);
  }
}

// Rig is what a check reaches: App's container, the calls the fake server took, and settle, which waits until check holds
// while each turn lets App's calls and renders run.
interface Rig {
  container: HTMLElement;
  fake: Fake;
  settle: (check: () => boolean, what: string) => Promise<void>;
}

// withApp mounts App at the desk's width over the pane and the design tide, with a fake server that answers Designs.Get as
// reads says, runs check on it, and unmounts App however check ends.
async function withApp(t: TestContext, reads: Reads, check: (rig: Rig) => Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = desk;
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new Fake(reads);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(
        <App rows={[paneRow]} now={now} clients={fake.clients} contracts={[]} designs={[tide]} seenClock={stillClock} />,
        container,
      );
    });
    await check({
      container,
      fake,
      settle: (done, what) => until(t.signal, done, what, { step: () => act(turn) }),
    });
  } finally {
    await teardown();
  }
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// typeInto sets a box's words as the owner types them, and lets Preact draw what the input changed.
async function typeInto(box: HTMLTextAreaElement, words: string): Promise<void> {
  await act(() => {
    box.value = words;
    box.dispatchEvent(new Event("input"));
  });
}

// holdTide presses the design's tab, which holds the design's page at the desk and draws its conversation beside it.
function holdTide(container: HTMLElement): Promise<void> {
  const tab = container.querySelector<HTMLElement>('.list .design-tab[data-key="design:tide"]');
  assert.ok(tab !== null, "the design's tab is not drawn");
  return press(tab);
}

// buttonNamed is the button under root whose text is label, which must be drawn.
function buttonNamed(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>("button")].find((button) => button.textContent === label);
  assert.ok(found !== undefined, `no button reads ${label}`);
  return found;
}

// find is the question's thread under the place's root, or null when it is not drawn.
function find(container: HTMLElement, root: string, id: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`${root} section[aria-label="question ${id}"]`);
}

// thread is the question's thread under the place's root, which must be drawn.
function thread(container: HTMLElement, root: string, id: string): HTMLElement {
  const block = find(container, root, id);
  assert.ok(block !== null, `${root} draws no thread for ${id}`);
  return block;
}

// answerOf is the answer a Designs.Answer call carries, as the checks compare it.
function answerOf(answer: DesignsAnswerRequest) {
  return { design: answer.design, question: answer.question, answer: answer.answer, accept: answer.accept };
}

// asked writes the asked item into the conversation and waits until the question threads of both places are drawn.
async function drawAsked(container: HTMLElement, fake: Fake, settle: Rig["settle"]): Promise<void> {
  await settle(() => fake.watchCount() > 0, "the conversation's stream is open");
  fake.emit(transcriptOf([askedItem]));
  await settle(
    () => find(container, conversationRoot, "Q2") !== null && find(container, designRoot, "Q2") !== null,
    "both places draw the questions the conversation asked",
  );
}

test("QuestionThread: each asked question is its own thread, with its own box and Accept, in the conversation and in the design", (t) =>
  withApp(t, "record", async ({ container, fake, settle }) => {
    await holdTide(container);
    await drawAsked(container, fake, settle);
    for (const root of [conversationRoot, designRoot]) {
      for (const id of ["Q1", "Q2"]) {
        const block = thread(container, root, id);
        assert.ok(block.classList.contains("qthread"), `${root} does not draw ${id} open`);
        const box = block.querySelector<HTMLTextAreaElement>("textarea");
        assert.equal(box?.getAttribute("placeholder"), `Answer ${id}, or ask about it`, `${root}'s ${id} has no box of its own`);
        buttonNamed(block, "Accept");
      }
    }
    assert.deepEqual(
      [...thread(container, conversationRoot, "Q1").querySelectorAll(".qt-what")].map((what) => what.textContent),
      ["asked"],
      "the conversation's Q1 thread does not draw the log line that names it",
    );
  }));

test("QuestionThread: typing in Q1's box in the conversation answers Q1 once, and both places draw Q1 settled and Q2 open", (t) =>
  withApp(t, "record", async ({ container, fake, settle }) => {
    await holdTide(container);
    await drawAsked(container, fake, settle);
    const box = thread(container, conversationRoot, "Q1").querySelector<HTMLTextAreaElement>("textarea");
    assert.ok(box !== null, "the conversation's Q1 thread has no box");
    await typeInto(box, "Keep the pin");
    await press(buttonNamed(thread(container, conversationRoot, "Q1"), "Send"));
    await settle(
      () =>
        find(container, conversationRoot, "Q1")?.classList.contains("settled") === true &&
        find(container, designRoot, "Q1")?.classList.contains("settled") === true,
      "Q1 is drawn settled in both places",
    );
    assert.deepEqual(
      fake.answers.map(answerOf),
      [{ design: "tide", question: 1n, answer: "Keep the pin", accept: false }],
      "the answer in the conversation does not answer Q1 once, for Q1 only",
    );
    for (const root of [conversationRoot, designRoot]) {
      assert.ok(thread(container, root, "Q2").classList.contains("qthread"), `${root} does not draw Q2 open`);
      buttonNamed(thread(container, root, "Q2"), "Accept");
    }
  }));

test("QuestionThread: Accept on Q2 in the design answers Q2 once, and the conversation draws Q2 settled and Q1 open", (t) =>
  withApp(t, "record", async ({ container, fake, settle }) => {
    await holdTide(container);
    await drawAsked(container, fake, settle);
    await press(buttonNamed(thread(container, designRoot, "Q2"), "Accept"));
    await settle(
      () =>
        find(container, conversationRoot, "Q2")?.classList.contains("settled") === true &&
        find(container, designRoot, "Q2")?.classList.contains("settled") === true,
      "Q2 is drawn settled in both places",
    );
    assert.deepEqual(
      fake.answers.map(answerOf),
      [{ design: "tide", question: 2n, answer: "", accept: true }],
      "the Accept in the design does not answer Q2 once, with accept",
    );
    for (const root of [conversationRoot, designRoot]) {
      assert.ok(thread(container, root, "Q1").classList.contains("qthread"), `${root} does not draw Q1 open`);
      buttonNamed(thread(container, root, "Q1"), "Accept");
    }
  }));

test("QuestionThread: an asked question whose design has not loaded draws the loading line in the conversation, and no thread", (t) =>
  withApp(t, "hang", async ({ container, fake, settle }) => {
    await holdTide(container);
    await settle(() => fake.watchCount() > 0, "the conversation's stream is open");
    fake.emit(transcriptOf([askedItem]));
    await settle(
      () =>
        [...container.querySelectorAll(`${conversationRoot} p.inote`)].some(
          (line) => line.textContent === "the design is loading",
        ),
      "the conversation draws the loading line",
    );
    assertNone(container.querySelector(`${conversationRoot} section.qthread`), "a question with no record draws a thread");
  }));

test("QuestionThread: an asked question whose design read failed draws the error in the conversation, and no thread", (t) =>
  withApp(t, "refused", async ({ container, fake, settle }) => {
    await holdTide(container);
    await settle(() => fake.watchCount() > 0, "the conversation's stream is open");
    fake.emit(transcriptOf([askedItem]));
    await settle(
      () => container.querySelector(`${conversationRoot} p.inote.error`) !== null,
      "the conversation draws the error line",
    );
    assert.equal(
      container.querySelector(`${conversationRoot} p.inote.error`)?.textContent,
      "The server could not read this design.",
      "the conversation does not draw the error the read failed with",
    );
    assertNone(container.querySelector(`${conversationRoot} section.qthread`), "a question whose read failed draws a thread");
  }));

test("QuestionThread: a design item that is not asked draws its what, its ids and its words as one line", (t) =>
  withApp(t, "record", async ({ container, fake, settle }) => {
    await holdTide(container);
    await settle(() => fake.watchCount() > 0, "the conversation's stream is open");
    fake.emit(transcriptOf([askedItem, answeredItem]));
    await settle(
      () => container.querySelector(`${conversationRoot} .design-line`) !== null,
      "the conversation draws the answered line",
    );
    assert.equal(
      container.querySelector(`${conversationRoot} .design-line`)?.textContent,
      "answered · Q1 · Keep the pin",
      "the answered item does not draw its what, its id and its words",
    );
    assert.equal(
      container.querySelectorAll(`${conversationRoot} section.qthread`).length,
      2,
      "the answered item draws a thread, or the asked item draws a number of threads other than its two questions",
    );
  }));

test("QuestionThread: each design item the conversation receives reads its design again, once, and the asked one reads it too", (t) =>
  withApp(t, "record", async ({ container, fake, settle }) => {
    await holdTide(container);
    await drawAsked(container, fake, settle);
    assert.deepEqual(fake.gets, ["tide", "tide"], "the asked item does not read its design, beside the held design's read");
    const before = fake.gets.length;
    fake.emit(deltaOf([answeredItem]));
    await settle(() => fake.gets.length > before, "the answered item does not read its design again");
    assert.equal(fake.gets.length, before + 1, "a design item reads its design more than once");
  }));
