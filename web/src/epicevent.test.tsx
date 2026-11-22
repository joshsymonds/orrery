import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { EpicWidget } from "./card";
import { noEpics } from "./epics";
import { type EpicEvent, EventRing, epicEventOf } from "./eventring";
import { liveEpoch } from "./fixtures/clock";
import { EpicQuestionSchema, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

const ringEpic = "orrery-1a2b3c4/planner-runner";
// ringNow is the instant the epic calls hold, in unix seconds: the fixtures' instant.
const ringNow = Date.parse(liveEpoch) / 1000;
const title = `Epic ${ringEpic} (Plan the runner)`;
const threeChoices = [
  { id: "composite", does: "Add a composite index on keeper and shift." },
  { id: "partial", does: "Add a partial index on open shifts only." },
  { id: "none", does: "Leave the table as it is." },
];

// eventBlock is the block an epic's event carries, as the server writes it: the opening tag, the body, and the closing tag.
function eventBlock(kind: string, tail: string, body: string): string {
  return `<orrery-event kind="${kind}" id="epic:${ringEpic}:${tail}">\n${body}\n</orrery-event>`;
}

// eventOf reads the event a block is. The tests give it only blocks epicEventOf reads.
function eventOf(text: string): EpicEvent {
  const event = epicEventOf(text);
  if (event === null) {
    throw new Error(`${text} is read as an epic's event`);
  }
  return event;
}

// epicReading reads the epic as a live orreryd epic that holds the questions given open, with the tasks given, and paused
// when paused is set.
function epicReading(
  questions: MessageInitShape<typeof EpicQuestionSchema>[],
  tasks: MessageInitShape<typeof EpicsGetResponseSchema>["tasks"] = [],
  paused = false,
): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: create(EpicSchema, {
      id: ringEpic,
      slug: "planner-runner",
      repository: "orrery-1a2b3c4",
      outcome: "accepted",
      live: true,
      paused,
      source: "orreryd",
    }),
    questions: questions.map((question) => create(EpicQuestionSchema, question)),
    tasks,
  });
}

const openQuestion: MessageInitShape<typeof EpicQuestionSchema> = {
  id: "Q1",
  text: "Which index should it add?",
  choices: threeChoices,
  recommendation: { choice: "composite", why: "open shifts are the only rows the backfill scans", confidence: "high" },
};

// callsReading makes the epic calls over reading: get answers it, each epic read is recorded in reads and each opened
// epic in opened. The calls hold none of the poll's epics.
function callsReading(reading: EpicsGetResponse, opened: string[], reads: string[] = []): EpicCalls {
  return {
    get: async (id) => {
      reads.push(id);
      return reading;
    },
    answer: async () => "",
    openEpic: (id) => {
      opened.push(id);
    },
    openStep: () => {},
    stepResult: () => Promise.resolve(""),
    now: ringNow,
    epics: noEpics,
  };
}

// settle waits until check holds, each round running inside act. It ends with the test's own signal, so a wait that never
// holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement | null): Promise<void> {
  await act(() => {
    control?.click();
  });
}

// withRing draws the ring of event with calls into a fresh container, runs check on it, then removes it.
async function withRing(
  event: EpicEvent,
  calls: EpicCalls,
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<EventRing event={event} calls={calls} />, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// withCard draws the epic's Created card with calls into a fresh container, runs check on it, then removes it.
async function withCard(epic: string, calls: EpicCalls, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(<EpicWidget epic={epic} calls={calls} />, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// iconMarkup is the markup Icon draws for name, so a block's icon can be compared with the icon it should draw.
function iconMarkup(name: IconName): string {
  const holder = document.createElement("div");
  render(<Icon name={name} />, holder);
  return holder.innerHTML;
}

// kindCases are the four kinds an epic's event takes as its block draws them: the tone of the rail, the icon, and the
// sentence the block's body holds.
const kindCases: { kind: string; tail: string; body: string; tone: string; icon: IconName; sentence: string }[] = [
  {
    kind: "question",
    tail: "Q1",
    body: `${title} asks a question.\nQ1 which-store: which store?`,
    tone: "attention",
    icon: "question",
    sentence: `${title} asks a question.`,
  },
  {
    kind: "park",
    tail: "Q1",
    body: `${title} is parked.\nQ1 park:\n  retry: end the park`,
    tone: "attention",
    icon: "pause",
    sentence: `${title} is parked.`,
  },
  {
    kind: "release",
    tail: "released",
    body: `${title} is released. epic_get gives its report.`,
    tone: "ok",
    icon: "open",
    sentence: `${title} is released.`,
  },
  {
    kind: "end",
    tail: "done",
    body: `${title} is done. epic_get gives its report.`,
    tone: "quiet",
    icon: "stop",
    sentence: `${title} is done.`,
  },
];

test("Shapes: each of an epic's question, park, release and end draws one EpicEventBlock in its tone, with its icon, the label EPIC and its slug link, and the link opens the epic, and only a question reads its epic", async (t) => {
  for (const kindCase of kindCases) {
    const event = eventOf(eventBlock(kindCase.kind, kindCase.tail, kindCase.body));
    const opened: string[] = [];
    const reads: string[] = [];
    await withRing(event, callsReading(epicReading([]), opened, reads), async (container) => {
      if (kindCase.kind === "question") {
        await settle(t, () => reads.length === 1, "a question reads its epic once");
      } else {
        assert.deepEqual(reads, [], `${kindCase.kind} reads no epic`);
      }
      const blocks = container.querySelectorAll(".block.epicevent");
      assert.equal(blocks.length, 1, `${kindCase.kind} draws one EpicEventBlock`);
      const block = blocks[0];
      assert.equal(block.getAttribute("data-tone"), kindCase.tone, `${kindCase.kind} draws its tone`);
      assert.equal(block.querySelector(".head svg")?.outerHTML, iconMarkup(kindCase.icon), `${kindCase.kind} draws its icon`);
      assert.equal(block.querySelector(".head .label")?.textContent, "EPIC", `${kindCase.kind} is labelled EPIC`);
      assert.equal(block.querySelector(".ring-sentence")?.textContent, kindCase.sentence, `${kindCase.kind} draws its sentence`);
      const link = block.querySelector<HTMLButtonElement>(".end button");
      assert.equal(link?.textContent, "planner-runner", `${kindCase.kind} links the epic's slug`);
      await click(link);
      assert.deepEqual(opened, [ringEpic], `${kindCase.kind}'s link opens the epic`);
    });
  }
});

test("Shapes: an epic's Created draws one EpicEventBlock in the engine tone, its head led by the epic's mark before the plus icon, with the sentence and the slug link, and the link opens the epic", async (t) => {
  const opened: string[] = [];
  await withCard(ringEpic, callsReading(epicReading([], [{ slug: "t1", status: "" }]), opened), async (container) => {
    await settle(t, () => container.querySelector(".epicmark") !== null, "the card reads its epic's mark");
    const block = container.querySelector(".block.epicevent");
    assert.notEqual(block, null, "the card draws one EpicEventBlock");
    assert.equal(block?.getAttribute("data-tone"), "engine", "the Created block is in the engine tone");
    assert.equal(block?.querySelector(".head .label")?.textContent, "EPIC");
    const head = block?.querySelector(".head");
    assert.equal(head?.children[0]?.getAttribute("class"), "epicmark", "the head is led by the epic's mark");
    assert.equal(head?.children[1]?.outerHTML, iconMarkup("plus"), "the mark comes before the plus icon");
    assert.equal(block?.querySelector(".ring-sentence")?.textContent, "Created the epic from the accepted contract.");
    const link = block?.querySelector<HTMLButtonElement>(".end button");
    assert.equal(link?.textContent, "planner-runner", "the Created block links the epic's slug");
    await click(link ?? null);
    assert.deepEqual(opened, [ringEpic], "the link opens the epic");
  });
});

test("Shapes: a paused epic's Created mark draws its centre in ink, and a running epic's in live, as the epic list draws them", async (t) => {
  const tasks = [{ slug: "t1", status: "" }];
  const paused = { ...epicReading([], tasks, true), centre: "paused" };
  await withCard(ringEpic, callsReading(paused, []), async (container) => {
    await settle(t, () => container.querySelector(".epicmark") !== null, "the paused card reads its epic's mark");
    assert.equal(container.querySelector(".block.epicevent .epicmark g.centre")?.getAttribute("data-tone"), "ink.3", "a paused centre draws in ink");
  });
  const running = { ...epicReading([], tasks), centre: "running" };
  await withCard(ringEpic, callsReading(running, []), async (container) => {
    await settle(t, () => container.querySelector(".epicmark") !== null, "the live card reads its epic's mark");
    assert.equal(container.querySelector(".block.epicevent .epicmark g.centre")?.getAttribute("data-tone"), "live", "a running centre draws live");
  });
});

test("Shapes: an open question's Ask draws inside its EpicEventBlock with no number keys or key caps, and a settled question draws its Settled line inside it", async (t) => {
  await withRing(eventOf(eventBlock("question", "Q1", `${title} asks a question.`)), callsReading(epicReading([openQuestion]), []), async (container) => {
    await settle(t, () => container.querySelector(".block.epicevent .ask") !== null, "the open question's Ask is drawn");
    assert.equal(container.querySelector(".block.epicevent kbd"), null, "the Ask draws a key cap in the block");
    assertNone(container.querySelector(".block.epicevent .ask .opts .key"), "the Ask draws a number key in the block");
  });
  await withRing(
    eventOf(eventBlock("question", "Q1", `${title} asks a question.`)),
    callsReading(epicReading([]), []),
    async (container) => {
      await settle(t, () => container.querySelector(".block.epicevent .ask.folded.settled") !== null, "the Settled line is drawn");
      assert.equal(container.querySelector(".block.epicevent .ask:not(.folded)"), null, "a settled question draws no open Ask");
    },
  );
});

test("Shapes: a moved event still draws the MoveLine and no EpicEventBlock", async () => {
  const body = `Epic ${ringEpic} (Rota export) moved to session sess-lamp-11, by the owner.`;
  const event = eventOf(`<orrery-event kind="moved" id="epic:${ringEpic}:A1">\n${body}\n</orrery-event>`);
  await withRing(event, callsReading(epicReading([]), []), async (container) => {
    assert.equal(container.querySelectorAll(".ring-line").length, 1, "the move draws its ringed line");
    assert.equal(container.querySelectorAll(".block").length, 0, "the move draws no block");
  });
});
