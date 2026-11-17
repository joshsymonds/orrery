import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { createClients, createTransport } from "./client";
import type { Conversation } from "./conversation";
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
const speakLabel = "arrives when you next speak";
const freeLabel = "arrives when the session is free";
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
// opens. The calls make no epic call of their own; a step card opens a page and reads nothing.
interface Openers {
  calls: EpicCalls;
  epics: string[];
  steps: string[][];
}

function openersOf(): Openers {
  const epics: string[] = [];
  const steps: string[][] = [];
  const unused = async (): Promise<never> => {
    throw new Error("a step card makes no epic call");
  };
  return {
    epics,
    steps,
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
      tick: 1,
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

// StepFields are the fields of a step's end as its body and its pending event name them.
interface StepFields {
  step: string;
  agent?: string;
  epic?: string;
  end: string;
  status: string;
  reason: string;
  summary: string;
}

// stepBlock is the block a step's end carries, as enginehost's CallerEventBlock and stepEndBody write it. An agent or an
// epic line is written only when the field is given, as an older body has no agent line.
function stepBlock(fields: StepFields): string {
  const lines = [`Step ${fields.step} ended: end ${fields.end}, status ${fields.status}, reason ${fields.reason}.`];
  if (fields.agent !== undefined) {
    lines.push(`Agent: ${fields.agent}`);
  }
  if (fields.epic !== undefined) {
    lines.push(`Epic: ${fields.epic}`);
  }
  lines.push(`Summary: ${fields.summary}`, "step_status gives the full result.");
  return `<orrery-event kind="step_end" id="step:${fields.step}">\n${lines.join("\n")}\n</orrery-event>`;
}

// eventPrompt is the prompt a caller event is delivered as: sent by an event, under the event's id.
function eventPrompt(step: string, text: string, time: number) {
  return { kind: "prompt", time: String(time), text, sender: "event", promptId: `step:${step}` };
}

// pendingStep is a step's end the session holds and has not taken, as the server lists it.
function pendingStep(fields: StepFields, waits: string) {
  return {
    id: `step:${fields.step}`,
    kind: "step_end",
    step: fields.step,
    agent: fields.agent ?? "",
    end: fields.end,
    status: fields.status,
    reason: fields.reason,
    summary: fields.summary,
    epic: fields.epic ?? "",
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
    reason: "",
    summary: "",
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
  reason: "tests pass",
  summary: "Added the index.\nRan the suite.",
};

test("StepEventCard: a step_end prompt draws a card with its agent, end, status and summary line, and no EVENT block", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const body = stepBlock({ ...doneStep, epic: undefined });
    fake.emit(delta([eventPrompt("s1", body, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the step card is drawn");
    const [card] = cardsOf(container);
    assert.deepEqual(textsOf(card, ".step-agent"), ["builder"]);
    assert.deepEqual(textsOf(card, ".step-end"), ["end done"]);
    assert.deepEqual(textsOf(card, ".step-status"), ["status done"]);
    assert.deepEqual(textsOf(card, ".step-reason"), ["reason tests pass"]);
    assert.deepEqual(textsOf(card, ".step-summary"), ["Added the index."], "the card draws the summary's first line");
    assert.equal(countOf(container, ".event"), 0, "no EVENT block is drawn");
    assert.equal(container.textContent?.includes("EVENT"), false, "no EVENT label is drawn");
  });
});

test("StepEventCard: an older step_end body with no Agent line draws its card with no agent", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const body = stepBlock({ ...doneStep, agent: undefined, epic: undefined });
    fake.emit(delta([eventPrompt("s1", body, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the step card is drawn");
    assert.equal(countOf(container, ".step-agent"), 0, "the card names no agent");
    assert.deepEqual(textsOf(container, ".step-end"), ["end done"]);
  });
});

test("StepEventCard: a step whose event names an epic opens its page through openStep, and one with no epic expands in place to its summary", async (t) => {
  await withPage(t, async ({ container, fake, opened }) => {
    const withEpicBody = stepBlock(doneStep);
    const withoutEpicBody = stepBlock({
      step: "s2",
      agent: "reviewer",
      end: "returned",
      status: "gap",
      reason: "the cache is stale",
      summary: "Refreshed the cache.\nNo change needed.",
    });
    fake.emit(
      delta([eventPrompt("s1", withEpicBody, 100), eventPrompt("s2", withoutEpicBody, 101)], { seq: 2, reset: true }),
    );
    await settle(t, () => cardsOf(container).length === 2, "both step cards are drawn");
    const [withEpic, withoutEpic] = cardsOf(container);

    await click(headOf(withEpic));
    assert.deepEqual(opened.steps, [[epic, "s1"]], "the click opens the step's page through openStep");
    assert.deepEqual(opened.epics, [], "the click opens no epic page of its own");
    assert.deepEqual(textsOf(withEpic, ".step-summary"), ["Added the index."], "the card does not expand");

    await click(headOf(withoutEpic));
    assert.deepEqual(opened.steps, [[epic, "s1"]], "a card with no epic opens nothing");
    assert.deepEqual(
      textsOf(withoutEpic, ".step-summary"),
      ["Refreshed the cache.\nNo change needed."],
      "the click expands the card to its whole summary",
    );
  });
});

test("StepEventCard: two step_end prompts delivered one after another draw two cards", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const first = stepBlock({ ...doneStep, epic: undefined });
    fake.emit(delta([eventPrompt("s1", first, 100)], { seq: 2, reset: true }));
    await settle(t, () => cardsOf(container).length === 1, "the first step card is drawn");
    const second = stepBlock({ step: "s2", agent: "reviewer", end: "done", status: "done", reason: "", summary: "Ok." });
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

test("StepEventCard: a pending event draws a waiting card labelled for speak and for free", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const waiting: StepFields = { step: "s3", agent: "builder", end: "done", status: "done", reason: "", summary: "Done." };
    const pending = [pendingStep(waiting, "speak"), pendingEpic("release", "free")];
    fake.emit(delta([], { seq: 2, reset: true, pending }));
    await settle(t, () => countOf(container, ".waiting") === 2, "both waiting cards are drawn");
    assert.deepEqual(textsOf(container, ".wait-label"), [speakLabel, freeLabel], "each waits by its own label");
    assert.equal(countOf(container, ".step-card.waiting"), 1, "the step's end draws as a step card");
    assert.equal(countOf(container, ".event-ring.waiting"), 1, "the epic's event draws as its line");
    assert.deepEqual(textsOf(container, ".ring-sentence"), [epicTitle], "the epic line names the epic's title");
    assert.deepEqual(textsOf(container, ".step-agent"), ["builder"], "the step's card names its agent");
  });
});

test("StepEventCard: a waiting card gives way to an ordinary card once its prompt arrives", async (t) => {
  await withPage(t, async ({ container, fake }) => {
    const step: StepFields = { step: "s4", agent: "builder", end: "done", status: "done", reason: "", summary: "Done." };
    const pending = [pendingStep(step, "speak")];
    fake.emit(delta([], { seq: 2, reset: true, pending }));
    await settle(t, () => countOf(container, ".step-card.waiting") === 1, "the waiting card is drawn");
    assert.deepEqual(textsOf(container, ".wait-label"), [speakLabel]);

    // The prompt arrives while the pending list still holds its event, as it does until the next poll takes it out.
    fake.emit(delta([eventPrompt("s4", stepBlock(step), 102)], { seq: 3, pending }));
    await settle(t, () => countOf(container, ".step-card:not(.waiting)") === 1, "the prompt is drawn as its card");
    assert.equal(countOf(container, ".waiting"), 0, "the card is no longer dimmed");
    assert.equal(countOf(container, ".wait-label"), 0, "no waiting label is drawn");
    assert.deepEqual(textsOf(container, ".step-agent"), ["builder"]);
  });
});
