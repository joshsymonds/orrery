import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { type EpicEvent, EventRing, epicEventOf } from "./eventring";
import { EpicQuestionSchema, EpicSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

const ringEpic = "orrery-1a2b3c4/planner-runner";
const title = `Epic ${ringEpic} (Plan the runner)`;
// threeChoices are the choices of the open question: the recommended one is composite, the first.
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

const questionEvent = eventOf(eventBlock("question", "Q1", `${title} asks a question.\nQ1 which-store: which store?`));

// epicReading reads the epic as a live orreryd epic that holds the questions given open.
function epicReading(questions: MessageInitShape<typeof EpicQuestionSchema>[]): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic: create(EpicSchema, {
      id: ringEpic,
      slug: "planner-runner",
      repository: "orrery-1a2b3c4",
      outcome: "accepted",
      live: true,
      source: "orreryd",
    }),
    questions: questions.map((question) => create(EpicQuestionSchema, question)),
  });
}

const openQuestion: MessageInitShape<typeof EpicQuestionSchema> = {
  id: "Q1",
  text: "Which index should it add?",
  choices: threeChoices,
  recommendation: { choice: "composite", why: "open shifts are the only rows the backfill scans", confidence: "high" },
};

// FakeEpics answers the ring's reads with its reading, or refuses them when given an error, and records each call the
// ring makes. calls makes the epic calls over it at a tick.
class FakeEpics {
  readonly reads: string[] = [];
  readonly answers: string[][] = [];
  readonly opened: string[] = [];
  private readonly reading: EpicsGetResponse | Error;

  constructor(reading: EpicsGetResponse | Error) {
    this.reading = reading;
  }

  calls(tick = 1): EpicCalls {
    return {
      get: async (id) => {
        this.reads.push(id);
        if (this.reading instanceof Error) {
          throw this.reading;
        }
        return this.reading;
      },
      pause: () => Promise.resolve(),
      resume: () => Promise.resolve(),
      answer: async (id, question, choice, text) => {
        this.answers.push([id, question, choice, text]);
        return "";
      },
      openEpic: (id) => {
        this.opened.push(id);
      },
      tick,
    };
  }
}

// withRing draws the ring of event with calls into a fresh container, runs check on it, then removes it.
async function withRing(
  event: EpicEvent,
  calls: EpicCalls,
  check: (container: HTMLElement) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  // The container is attached, since a form submits on its confirm button only while it is in the document.
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

// settle waits until check holds, each round running inside act, so the renders a read or an answer causes are applied
// before the next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

function cards(root: ParentNode): HTMLButtonElement[] {
  return [...root.querySelectorAll<HTMLButtonElement>("form.answer .opts > .opt")];
}

// click clicks a control inside act, so the render its click causes has run before the next check.
async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

test("EventRing: epicEventOf reads a block's kind, epic, event tail and first sentence, and no other text", () => {
  assert.deepEqual(eventOf(eventBlock("release", "released", `${title} is released. epic_get gives its report.`)), {
    kind: "release",
    epic: ringEpic,
    tail: "released",
    sentence: `${title} is released.`,
  });
  assert.equal(
    epicEventOf(`<orrery-event kind="step_end" id="step:step-1">\nThe step ended.\n</orrery-event>`),
    null,
    "a step's event is not an epic's",
  );
  assert.equal(epicEventOf(eventBlock("exploded", "Q1", "a body")), null, "an unknown kind is not an epic's event");
  assert.equal(epicEventOf("Plain prompt text"), null, "a prompt that is no block is not an epic's event");
});

// kindCases are the four kinds an epic's event takes, each with its block's body and the sentence the line shows.
const kindCases = [
  {
    kind: "question",
    tail: "Q1",
    body: `${title} asks a question.\nQ1 which-store: which store?`,
    sentence: `${title} asks a question.`,
    label: "question",
  },
  {
    kind: "park",
    tail: "Q1",
    body: `${title} is parked.\nQ1 park:\n  retry: end the park`,
    sentence: `${title} is parked.`,
    label: "parked",
  },
  {
    kind: "release",
    tail: "released",
    body: `${title} is released. epic_get gives its report.`,
    sentence: `${title} is released.`,
    label: "released",
  },
  {
    kind: "end",
    tail: "done",
    body: `${title} is done. epic_get gives its report.`,
    sentence: `${title} is done.`,
    label: "ended",
  },
];

test("EventRing: each kind draws one ringed line with its icon, first sentence and slug, and a click opens the epic", async (t) => {
  for (const kindCase of kindCases) {
    const event = eventOf(eventBlock(kindCase.kind, kindCase.tail, kindCase.body));
    const fake = new FakeEpics(epicReading([]));
    await withRing(event, fake.calls(), async (container) => {
      const lines = container.querySelectorAll<HTMLButtonElement>(".ring-line");
      assert.equal(lines.length, 1, `${kindCase.kind} draws one ringed line`);
      const icon = container.querySelector(".ring-icon");
      assert.equal(icon?.classList.contains(kindCase.kind), true, `${kindCase.kind} draws its kind's icon`);
      assert.equal(icon?.getAttribute("aria-label"), kindCase.label);
      assert.equal(container.querySelector(".ring-sentence")?.textContent, kindCase.sentence);
      assert.equal(container.querySelector(".ring-slug")?.textContent, "planner-runner");
      await click(lines[0]);
      assert.deepEqual(fake.opened, [ringEpic], `${kindCase.kind} opens the epic on click`);
      if (kindCase.kind === "question") {
        await settle(t, () => fake.reads.length === 1, "the question's epic is read");
      } else {
        assert.deepEqual(fake.reads, [], `${kindCase.kind} reads no epic`);
      }
    });
  }
});

test("EventRing: a question event reads its epic on mount and again each time the tick changes", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(1), async (container) => {
    await settle(t, () => fake.reads.length === 1, "the ring reads its epic on mount");
    await act(() => {
      render(<EventRing event={questionEvent} calls={fake.calls(1)} />, container);
    });
    await act(turn);
    assert.equal(fake.reads.length, 1, "a render at the same tick does not read the epic again");
    await act(() => {
      render(<EventRing event={questionEvent} calls={fake.calls(2)} />, container);
    });
    await settle(t, () => fake.reads.length === 2, "the ring reads its epic again when the tick changes");
  });
});

test("EventRing: an open question draws its form, the recommended option is chosen by click, and a digit key chooses nothing", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => container.querySelector("form.answer") !== null, "the open question's form is drawn");
    assert.equal(container.querySelector("form.answer p.question")?.textContent, "Which index should it add?");
    assert.equal(cards(container).length, 4, "three choices and Something else");
    assert.equal(container.querySelector(".opt em")?.textContent, "recommended", "the recommended option is marked");
    assert.equal(container.querySelector(".opt.sel b")?.textContent, "composite", "the recommended option starts chosen");

    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "2", cancelable: true }));
    });
    assert.equal(container.querySelector(".opt.sel b")?.textContent, "composite", "a digit key chooses nothing");

    const [recommended, , none] = cards(container);
    await click(none);
    assert.equal(container.querySelector(".opt.sel b")?.textContent, "none", "a click chooses the card it lands on");
    await click(recommended);
    assert.equal(container.querySelector(".opt.sel b")?.textContent, "composite", "a click chooses the recommended card");
    const confirm = container.querySelector<HTMLButtonElement>("form.answer .act.primary");
    assert.ok(confirm !== null, "the confirm button is drawn");
    assert.equal(confirm.textContent, "Answer with composite");
    await click(confirm);
    await settle(t, () => fake.answers.length === 1, "the answer is sent");
    assert.deepEqual(fake.answers, [
      [ringEpic, "Q1", "composite", "Add a composite index on keeper and shift."],
    ]);
  });
});

test("EventRing: a question event whose question is no longer open draws the settled line and no form", async (t) => {
  const fake = new FakeEpics(epicReading([]));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => container.querySelector(".settled") !== null, "the settled line is drawn");
    assert.equal(container.querySelector(".settled")?.textContent, "This question is settled.");
    assertNone(container.querySelector("form.answer"), "a settled question draws no form");
    assert.equal(container.querySelectorAll(".ring-line").length, 1, "the ring line stays");
  });
});

test("EventRing: a question event whose epic cannot be read draws neither the form nor the settled line", async (t) => {
  const fake = new FakeEpics(new Error("the epic is gone"));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => fake.reads.length === 1, "the ring reads its epic");
    await act(turn);
    assertNone(container.querySelector("form.answer"), "an unreadable epic draws no form");
    assertNone(container.querySelector(".settled"), "an unreadable epic draws no settled line");
    assert.equal(container.querySelectorAll(".ring-line").length, 1, "the ring line stays");
  });
});
