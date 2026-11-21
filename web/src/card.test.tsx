import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import type { Conversation } from "./conversation";
import { type EpicsView, epicsView, noEpics } from "./epics";
import { liveEpoch } from "./fixtures/clock";
import { type EpicsGetResponse, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type ConversationItem, ConversationItemSchema, ConversationStateSchema } from "./gen/orrery/v1/orrery_pb";
import { Reader } from "./reader";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// epicId is the id the create call names, in the form a contract names: its repository's id and the epic's slug.
const epicId = "demo-0abcdef/demo-epic";
// createId is the id of the epic_create call and of its result.
const createId = "c1";
// seenAt is past every item's time, so the reader draws no since-you-looked rule among the rows the tests count.
const seenAt = 1_000;

function item(init: MessageInitShape<typeof ConversationItemSchema>): ConversationItem {
  return create(ConversationItemSchema, init);
}

// createCall is the epic_create call that creates the epic, as the reader draws it.
function createCall(): ConversationItem {
  return item({ kind: "tool", time: 100n, tool: "epic_create", input: "demo-epic", id: createId, epic: epicId });
}

// resultItem is the result of the call under id, refused when error is set.
function resultItem(id: string, text: string, error = false): ConversationItem {
  return item({ kind: "result", time: 100n, id, text, error });
}

// toolCall is a Bash call that creates no epic, under id.
function toolCall(id: string): ConversationItem {
  return item({ kind: "tool", time: 100n, tool: "Bash", input: `echo ${id}`, id });
}

function conversationOf(items: ConversationItem[]): Conversation {
  return {
    transcript: "t1",
    seq: 1n,
    items,
    oldest: 0n,
    more: false,
    state: create(ConversationStateSchema, {}),
    unfinished: [],
    queued: [],
    unsent: [],
  };
}

// orreryd makes the detail of an orreryd epic in phase, live unless it has ended, with the fields init sets.
function orreryd(
  phase: string,
  init: MessageInitShape<typeof EpicsGetResponseSchema> = {},
  live = true,
): EpicsGetResponse {
  const detail = create(EpicsGetResponseSchema, init);
  detail.epic = create(EpicSchema, {
    id: epicId,
    slug: "demo-epic",
    repository: "demo-0abcdef",
    outcome: phase,
    live,
    source: "orreryd",
  });
  return detail;
}

// FakeEpics answers the card's reads with its detail, or refuses them when given an error, and records each read the
// card makes and each epic it opens. A test may change the answer between reads. calls makes the epic calls over it, with
// the poll's epics, which hold none by default.
class FakeEpics {
  readonly reads: string[] = [];
  readonly opened: string[] = [];
  answer: EpicsGetResponse | Error;

  constructor(answer: EpicsGetResponse | Error) {
    this.answer = answer;
  }

  calls(epics: EpicsView = noEpics): EpicCalls {
    return {
      get: async (id) => {
        this.reads.push(id);
        if (this.answer instanceof Error) {
          throw this.answer;
        }
        return this.answer;
      },
      answer: async () => "",
      openEpic: (id) => {
        this.opened.push(id);
      },
      openStep: () => {},
      stepResult: () => Promise.resolve(""),
      now: Date.parse(liveEpoch) / 1000,
      epics,
    };
  }
}

// polledView is the poll's epics when it holds detail of the card's epic, listed as detail's epic.
function polledView(detail: EpicsGetResponse): EpicsView {
  const epic = detail.epic;
  if (epic === undefined) {
    throw new Error("the detail holds no epic");
  }
  return epicsView([epic], new Map([[epicId, detail]]));
}

// listedView is the poll's epics when it lists the card's epic as listed and holds no detail of it, as for an epic that
// has ended.
function listedView(listed: EpicsGetResponse): EpicsView {
  const epic = listed.epic;
  if (epic === undefined) {
    throw new Error("the detail holds no epic");
  }
  return epicsView([epic], new Map());
}

// settle waits until check holds, each round running inside act, so the renders a read causes are applied before the next
// check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// drawn waits until the card's Created block is drawn.
function drawn(t: TestContext, container: HTMLElement): Promise<void> {
  return settle(t, () => container.querySelector(".block.epicevent") !== null, "the card draws its block");
}

// gaugeLabel is the label of the card's gauge, which says how many of the epic's tasks are done.
function gaugeLabel(container: ParentNode): string | null | undefined {
  return container.querySelector(".gauge")?.getAttribute("aria-label");
}

// withReader draws the reader over items with the epic calls, runs the check on its container, then removes the DOM.
async function withReader(
  items: ConversationItem[],
  epicCalls: EpicCalls,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(
        <Reader conversation={conversationOf(items)} seenAt={seenAt} open={false} epicCalls={epicCalls} />,
        container,
      );
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// withRedraws draws the reader over the create call and its result, and gives check the function that draws it again
// with other epic calls, as each render of the page's reader does.
async function withRedraws(
  first: EpicCalls,
  check: (container: HTMLElement, redraw: (calls: EpicCalls) => Promise<void>) => Promise<void>,
): Promise<void> {
  const items = [createCall(), resultItem(createId, "created")];
  const window = installTestDom();
  const container = document.createElement("div");
  const redraw = (epicCalls: EpicCalls) =>
    act(() => {
      render(
        <Reader conversation={conversationOf(items)} seenAt={seenAt} open={false} epicCalls={epicCalls} />,
        container,
      );
    });
  try {
    await redraw(first);
    await check(container, redraw);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

test("EpicWidget: an epic that cannot be read draws its Created block with no gauge", async (t) => {
  const fake = new FakeEpics(new Error("no such epic"));
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    await settle(t, () => fake.reads.length === 1, "the card reads its epic");
    await act(turn);
    assert.equal(container.querySelector(".block.epicevent .ring-sentence")?.textContent, "Created the epic from the accepted contract.");
    assertNone(container.querySelector(".gauge"), "an unreadable epic draws no gauge");
  });
});

test("EpicWidget: a refused create and a create with no result yet keep their tool rows", async () => {
  const fake = new FakeEpics(orreryd("accepted"));
  await withReader([createCall(), resultItem(createId, "refused", true)], fake.calls(), (container) => {
    assertNone(container.querySelector(".epic-card"));
    assert.equal(container.querySelector(".call b")?.textContent, "epic_create");
    assert.equal(container.querySelector(".call .fail")?.textContent, "✗");
  });
  await withReader([createCall()], fake.calls(), (container) => {
    assertNone(container.querySelector(".epic-card"));
    assert.equal(container.querySelector(".call b")?.textContent, "epic_create");
  });
  assert.deepEqual(fake.reads, []);
});

test("EpicWidget: an epic_create call between other tool calls draws its card outside any fold", async (t) => {
  const items = [
    toolCall("b1"),
    toolCall("b2"),
    toolCall("b3"),
    createCall(),
    resultItem(createId, "created"),
    toolCall("b4"),
    toolCall("b5"),
    toolCall("b6"),
  ];
  const fake = new FakeEpics(orreryd("accepted"));
  await withReader(items, fake.calls(), async (container) => {
    await drawn(t, container);
    const rows = [...container.querySelectorAll(".reader > .t")];
    assert.equal(rows.length, 3, "the calls before and after the card fold apart, and the card is a row of its own");
    assert.equal(rows[0].querySelector(".fold.calls b")?.textContent, "3 tool calls");
    const card = rows[1].querySelector(".epic-card");
    assert.notEqual(card, null, "the card is the middle row");
    assert.equal(card?.closest(".fold, .calls") ?? null, null, "the card sits in no fold");
    assert.equal(rows[2].querySelector(".fold.calls b")?.textContent, "3 tool calls");
  });
});

test("EpicWidget: a card whose epic the poll holds draws the poll's detail, reads nothing, and draws the poll's next detail", async (t) => {
  const fake = new FakeEpics(orreryd("accepted"));
  await withRedraws(fake.calls(polledView(orreryd("accepted"))), async (container, redraw) => {
    await drawn(t, container);
    assert.equal(gaugeLabel(container), "no tasks yet");
    await redraw(fake.calls(polledView(orreryd("accepted", { tasks: [{ slug: "t1", status: "" }] }))));
    await settle(t, () => gaugeLabel(container) === "0 of 1 tasks done", "the poll's next detail draws");
    await act(turn);
    assert.deepEqual(fake.reads, [], "a card over a polled epic read it");
  });
});

test("EpicWidget: a card whose epic the poll does not hold reads it on mount, and again only when its listed message changes", async (t) => {
  const ended = orreryd("released", {}, false);
  const fake = new FakeEpics(ended);
  await withRedraws(fake.calls(listedView(ended)), async (container, redraw) => {
    await drawn(t, container);
    await settle(t, () => fake.reads.length === 1, "the card reads its epic on mount");
    // A later poll lists the same message in a new view, as each epics load does.
    await redraw(fake.calls(listedView(orreryd("released", {}, false))));
    await act(turn);
    await act(turn);
    assert.deepEqual(fake.reads, [epicId], "a new view over the same listed message read the epic");
    const reseen = orreryd("released", {}, false);
    if (reseen.epic !== undefined) {
      reseen.epic.seen = 500n;
    }
    await redraw(fake.calls(listedView(reseen)));
    await settle(t, () => fake.reads.length === 2, "the card reads its epic when its listed message changes");
    await act(turn);
    assert.deepEqual(fake.reads, [epicId, epicId]);
  });
});

test("EpicWidget: a card whose epic is not listed at all reads it once on mount", async (t) => {
  const fake = new FakeEpics(orreryd("accepted"));
  await withRedraws(fake.calls(), async (container, redraw) => {
    await drawn(t, container);
    await settle(t, () => fake.reads.length === 1, "the card reads its epic on mount");
    await redraw(fake.calls());
    await act(turn);
    await act(turn);
    assert.deepEqual(fake.reads, [epicId]);
  });
});

test("EpicWidget: the link in the Created block opens the epic by its id", async (t) => {
  const fake = new FakeEpics(orreryd("built"));
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    await act(() => {
      container.querySelector<HTMLButtonElement>(".block.epicevent .end button")?.click();
    });
    assert.deepEqual(fake.opened, [epicId]);
  });
});
