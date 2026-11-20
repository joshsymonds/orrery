import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
import { noEpics } from "./epics";
import { follow } from "./follow";
import { Reader } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// The step event cards over the reader and the fake transport: a reader follows a session's conversation through a fake
// WatchConversation stream that the test feeds, so its prompts and pending events arrive as the server sends them. Each
// test's name carries the token StepEventCard.

const watchPath = "/orrery.v1.Sessions/WatchConversation";
const sessionId = "s1";
const epic = "orrery-0668c87/orrery-conversation-model";
const epicTitle = "Epic orrery-0668c87/orrery-conversation-model (A conversation shows what is happening)";
// seenAt is past every item's time, so the reader draws no since-you-looked rule among the cards the tests count.
const seenAt = Number.POSITIVE_INFINITY;

type Clients = ReturnType<typeof createClients>;

// envelope frames a stream message as Connect's JSON streaming frames it: a flag of 0, the payload's length, the payload.
function envelope(message: object): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const bytes = new Uint8Array(5 + payload.byteLength);
  bytes[0] = 0;
  new DataView(bytes.buffer).setUint32(1, payload.byteLength);
  bytes.set(payload, 5);
  return bytes;
}

// FakeSessions answers the session's WatchConversation with one stream the test feeds through emit. Its clients are the
// transport the reader's follow reads through.
class FakeSessions {
  private controller: ReadableStreamDefaultController<Uint8Array> | null = null;

  readonly fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const path = new URL(new Request(input, init).url).pathname;
    if (path !== watchPath) {
      throw new Error(`the test answers no call to ${path}`);
    }
    const body = new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
    });
    return new Response(body, { status: 200, headers: { "content-type": "application/connect+json" } });
  };

  readonly clients: Clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: this.fetch }));

  get open(): boolean {
    return this.controller !== null;
  }

  emit(message: object): void {
    if (this.controller === null) {
      throw new Error("the watch stream is not open");
    }
    this.controller.enqueue(envelope(message));
  }

  close(): void {
    this.controller?.close();
    this.controller = null;
  }
}

// Openers records what the cards' epic calls open: each epic a call opens, and the epic and step of each step a call
// opens, and each step whose return a card's fold reads. The calls make no epic call of their own.
interface Openers {
  calls: EpicCalls;
  epics: string[];
  steps: string[][];
  returned: string[];
}

// fullReturns are the full returns the steps' stepResult answers with, by step id. Each has a blank line, so a return
// cut short would not match.
const fullReturns: Record<string, string> = {
  s1: "Added the index.\n\nRan the suite: 12 passed.\n",
  s2: "Refreshed the cache.\nNo change needed.",
};

function openersOf(): Openers {
  const epics: string[] = [];
  const steps: string[][] = [];
  const returned: string[] = [];
  const unused = async (): Promise<never> => {
    throw new Error("a step card makes no epic call");
  };
  return {
    epics,
    steps,
    returned,
    calls: {
      get: unused,
      pause: unused,
      resume: unused,
      answer: unused,
      openEpic: (id) => {
        epics.push(id);
      },
      openStep: (epicId, step) => {
        steps.push([epicId, step]);
      },
      stepResult: async (id) => {
        returned.push(id);
        return fullReturns[id] ?? "";
      },
      epics: noEpics,
    },
  };
}

// Following draws a reader over the session's conversation as the app's follow holds it, with the epic calls.
function Following({ clients, epicCalls }: { clients: Clients; epicCalls: EpicCalls }) {
  const [conversation, setConversation] = useState<Conversation | null>(null);
  useEffect(() => {
    const followed = follow(clients, { kind: "session", id: sessionId }, setConversation);
    return followed.stop;
  }, [clients]);
  return <Reader conversation={conversation} seenAt={seenAt} open={false} epicCalls={epicCalls} />;
}

interface Page {
  container: HTMLElement;
  fake: FakeSessions;
  opened: Openers;
}

// withPage draws the following reader in a fresh DOM once its stream is open, runs check on it, then unmounts the reader,
// closes the stream and removes the DOM, in that order, so the follow has stopped before its stream ends.
async function withPage(t: TestContext, check: (page: Page) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const fake = new FakeSessions();
  const opened = openersOf();
  try {
    await act(() => {
      render(<Following clients={fake.clients} epicCalls={opened.calls} />, container);
    });
    await settle(t, () => fake.open, "the session's stream opens");
    await check({ container, fake, opened });
  } finally {
    await act(() => {
      render(null, container);
    });
    fake.close();
    container.remove();
    await removeTestDom(window);
  }
}

// settle waits until check holds, each round running inside act, so the renders a message causes are applied before the
// next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// StepFields are the fields of a step's end as its block names them. An agent or an epic is named only when the field
// is given.
interface StepFields {
  step: string;
  agent?: string;
  epic?: string;
  end: string;
  status: string;
  brief: string[];
}

// stepBlock is the block a step's end carries, as enginehost's CallerEventBlock writes it: the step's agent, end,
// status and epic as attributes of the opening tag, then its brief on lines of its own, then the line that points to
// the full return.
function stepBlock(fields: StepFields): string {
  const attributes = [`kind="step_end"`, `id="step:${fields.step}"`];
  if (fields.agent !== undefined) {
    attributes.push(`agent="${fields.agent}"`);
  }
  attributes.push(`end="${fields.end}"`, `status="${fields.status}"`);
  if (fields.epic !== undefined) {
    attributes.push(`epic="${fields.epic}"`);
  }
  const lines = [...fields.brief, "More: step_status gives the full result."];
  return `<orrery-event ${attributes.join(" ")}>\n${lines.join("\n")}\n</orrery-event>`;
}

// eventPrompt is the prompt a caller event is delivered as: sent by an event, under the event's id.
function eventPrompt(step: string, text: string, time: number) {
  return { kind: "prompt", time: String(time), text, sender: "event", promptId: `step:${step}` };
}

// PendingStepFields are the fields of a step's end the session holds, as the server lists it: its brief is its lines,
// ending with the More line.
interface PendingStepFields {
  step: string;
  agent: string;
  end: string;
  status: string;
  brief: string;
}

// pendingStep is a step's end the session holds and has not taken, as the server lists it.
function pendingStep(fields: PendingStepFields, waits: string) {
  return {
    id: `step:${fields.step}`,
    kind: "step_end",
    step: fields.step,
    agent: fields.agent,
    end: fields.end,
    status: fields.status,
    brief: fields.brief,
    epic: "",
    title: "",
    waits,
  };
}

// pendingEpic is an epic's event the session holds and has not taken, as the server lists it.
function pendingEpic(kind: string, waits: string) {
  return {
    id: `epic:${epic}:released`,
    kind,
    step: "",
    agent: "",
    end: "",
    status: "",
    brief: "",
    epic,
    title: epicTitle,
    waits,
  };
}

// delta is a WatchConversation message holding items, and the state's pending list when one is given.
function delta(items: object[], options: { seq: number; reset?: boolean; pending?: object[] }): object {
  return {
    transcript: `${sessionId}.jsonl`,
    seq: String(options.seq),
    reset: options.reset ?? false,
    items,
    state: options.pending === undefined ? undefined : { pending: options.pending },
  };
}

function cardsOf(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".step-card")];
}

function headOf(card: HTMLElement): HTMLElement {
  const head = card.querySelector<HTMLElement>(".step-head");
  if (head === null) {
    throw new Error("the card draws its head");
  }
  return head;
}

// summaryOf is the summary of the card's return fold, the control that opens it.
function summaryOf(card: HTMLElement): HTMLElement {
  const summary = card.querySelector<HTMLElement>("details > summary");
  if (summary === null) {
    throw new Error("the card draws its return fold");
  }
  return summary;
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function countOf(root: ParentNode, selector: string): number {
  return root.querySelectorAll(selector).length;
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

const doneStep: StepFields = {
  step: "s1",
  agent: "builder",
  epic,
  end: "done",
  status: "done",
  brief: ["Added the index.", "Ran the suite."],
};

test("StepEventCard: a step_end prompt draws a card with its agent, end, status and brief lines, and no EVENT block", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const body = stepBlock({ ...doneStep, epic: undefined });
    fake.emit(delta([eventPrompt("s1", body, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the step card is drawn");
    const [card] = cardsOf(container);
    assert.deepEqual(textsOf(card, ".step-agent"), ["builder"]);
    assert.deepEqual(textsOf(card, ".step-end"), ["end done"]);
    assert.deepEqual(textsOf(card, ".step-status"), ["status done"]);
    assert.deepEqual(textsOf(card, ".step-summary"), ["Added the index.", "Ran the suite."], "the card draws its brief");
    assert.equal(countOf(container, ".event"), 0, "no EVENT block is drawn");
    assert.equal(container.textContent?.includes("EVENT"), false, "no EVENT label is drawn");
  });
});

test("StepEventCard: a step_end block with no agent attribute draws its card with no agent", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const body = stepBlock({ ...doneStep, agent: undefined, epic: undefined });
    fake.emit(delta([eventPrompt("s1", body, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the step card is drawn");
    assert.equal(countOf(container, ".step-agent"), 0, "the card names no agent");
    assert.deepEqual(textsOf(container, ".step-end"), ["end done"]);
  });
});

test("StepEventCard: a step whose event names an epic opens its page through openStep, and one with no epic opens nothing", async (t) => {
  await withPage(t, async ({ container, fake, opened }) => {
    const withEpicBody = stepBlock(doneStep);
    const withoutEpicBody = stepBlock({
      step: "s2",
      agent: "reviewer",
      end: "returned",
      status: "gap",
      brief: ["Refreshed the cache.", "No change needed."],
    });
    fake.emit(
      delta([eventPrompt("s1", withEpicBody, 100), eventPrompt("s2", withoutEpicBody, 101)], { seq: 2, reset: true }),
    );
    await settle(t, () => cardsOf(container).length === 2, "both step cards are drawn");
    const [withEpic, withoutEpic] = cardsOf(container);

    await click(headOf(withEpic));
    assert.deepEqual(opened.steps, [[epic, "s1"]], "the click opens the step's page through openStep");
    assert.deepEqual(opened.epics, [], "the click opens no epic page of its own");
    assert.deepEqual(
      textsOf(withEpic, ".step-summary"),
      ["Added the index.", "Ran the suite."],
      "the card still draws its brief",
    );

    await click(headOf(withoutEpic));
    assert.deepEqual(opened.steps, [[epic, "s1"]], "a card with no epic opens nothing");
    assert.deepEqual(opened.returned, [], "a click on the head reads no return");
    assert.equal(withoutEpic.querySelector("details")?.open, false, "a click on the head leaves the return folded");

    await click(summaryOf(withoutEpic));
    await settle(t, () => textsOf(withoutEpic, ".step-return pre").length === 1, "the opened fold draws the return");
    assert.deepEqual(opened.returned, ["s2"], "opening the fold reads the step's return once");
    assert.deepEqual(textsOf(withoutEpic, ".step-return pre"), [fullReturns.s2], "the fold holds the whole return");
  });
});

test("StepEventCard: two step_end prompts delivered one after another draw two cards", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const first = stepBlock({ ...doneStep, epic: undefined });
    fake.emit(delta([eventPrompt("s1", first, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the first step card is drawn");
    const second = stepBlock({ step: "s2", agent: "reviewer", end: "done", status: "done", brief: ["Ok."] });
    fake.emit(delta([eventPrompt("s2", second, 101)], { seq: 3 }));
    await settle(t, () => cardsOf(container).length === 2, "the second step card is drawn");
    assert.deepEqual(
      cardsOf(container).map((card) => textsOf(card, ".step-agent")),
      [["builder"], ["reviewer"]],
      "each event draws its own card, in delivery order",
    );
    assert.equal(countOf(container, ".event"), 0, "no EVENT block is drawn");
  });
});

test("DockWaits: a session's held events draw no card in the transcript, and a delivered prompt still draws its card", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const step: StepFields = { step: "s7", agent: "builder", end: "done", status: "done", brief: ["Done."] };
    const held: PendingStepFields = {
      step: "s8",
      agent: "builder",
      end: "done",
      status: "done",
      brief: "Held.\nMore: step_result id s8",
    };
    const pending = [
      pendingStep(held, "speak"),
      pendingStep({ ...held, step: "s9" }, "request"),
      pendingEpic("release", "free"),
    ];
    fake.emit(delta([eventPrompt("s7", stepBlock(step), 102)], { seq: 2, reset: true, pending }));
    await settle(t, () => textsOf(container, ".step-summary").includes("Done."), "the delivered prompt's card is drawn");
    assert.equal(cardsOf(container).length, 1, "only the delivered prompt draws a card");
    assert.deepEqual(textsOf(container, ".step-summary"), ["Done."], "only the delivered step draws its brief");
    assert.equal(countOf(container, ".waiting"), 0, "no held event is dimmed in the transcript");
    assert.equal(countOf(container, ".wait-label"), 0, "no held event is labelled in the transcript");
    assert.equal(countOf(container, ".event-ring"), 0, "the held epic event draws no line in the transcript");
  });
});
