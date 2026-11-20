import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { type EpicsView, epicsView, noEpics } from "./epics";
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
// ring makes. A test may change the reading between reads. calls makes the epic calls over it, with the poll's epics,
// which hold none by default.
class FakeEpics {
  readonly reads: string[] = [];
  readonly answers: string[][] = [];
  readonly opened: string[] = [];
  reading: EpicsGetResponse | Error;

  constructor(reading: EpicsGetResponse | Error) {
    this.reading = reading;
  }

  calls(epics: EpicsView = noEpics): EpicCalls {
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
      openStep: () => {},
      stepResult: () => Promise.resolve(""),
      epics,
    };
  }
}

// polledView is the poll's epics when it holds reading as the ring epic's detail.
function polledView(reading: EpicsGetResponse): EpicsView {
  const epic = reading.epic;
  if (epic === undefined) {
    throw new Error("the reading holds no epic");
  }
  return epicsView([epic], new Map([[ringEpic, reading]]));
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

// options are the options of the question's Ask in order: each choice, then Something else.
function options(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(".ask .opts > .opt")];
}

// pickedName is the name of the option that holds the pick, or undefined when none does.
function pickedName(root: ParentNode): string | undefined {
  return root.querySelector(".ask .opts > .opt.picked .nm")?.textContent ?? undefined;
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
  const stepEnd = [
    '<orrery-event kind="step_end" id="step:step-1" end="done" status="done">',
    "The step ended.",
    "</orrery-event>",
  ].join("\n");
  assert.equal(epicEventOf(stepEnd), null, "a step's event is not an epic's");
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

test("EventRing: a question event whose epic the poll does not hold reads it on mount, and not again on a later render", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => fake.reads.length === 1, "the ring reads its epic on mount");
    await act(() => {
      render(<EventRing event={questionEvent} calls={fake.calls()} />, container);
    });
    await act(turn);
    await act(turn);
    assert.equal(fake.reads.length, 1, "a later render read the epic again");
  });
});

test("EventRing: a question event whose epic the poll holds draws the poll's question and reads nothing", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(polledView(epicReading([openQuestion]))), async (container) => {
    await settle(t, () => container.querySelector(".ask") !== null, "the open question's Ask is drawn");
    await act(() => {
      render(<EventRing event={questionEvent} calls={fake.calls(polledView(epicReading([])))} />, container);
    });
    await settle(t, () => container.querySelector(".settled") !== null, "the poll's next detail settles the question");
    assert.deepEqual(fake.reads, [], "a ring over a polled epic read it");
  });
});

test("EventRing: an answer that lands reads the epic once more, and the settled question draws its line", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(polledView(epicReading([openQuestion]))), async (container) => {
    await settle(t, () => container.querySelector(".ask") !== null, "the open question's Ask is drawn");
    fake.reading = epicReading([]);
    const confirm = container.querySelector<HTMLButtonElement>(".ask .btn.primary");
    assert.ok(confirm !== null, "the confirm button is drawn");
    await click(confirm);
    await settle(t, () => container.querySelector(".settled") !== null, "the reread settles the question");
    assert.equal(fake.answers.length, 1);
    assert.deepEqual(fake.reads, [ringEpic], "the landed answer did not read the epic once");
  });
});

test("AskAnswer: an epic question's Ask in a transcript draws no key caps and a digit picks nothing", async (t) => {
  const fake = new FakeEpics(epicReading([openQuestion]));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => container.querySelector(".ask") !== null, "the open question's Ask is drawn");
    assert.equal(container.querySelector(".ask .q")?.textContent, "Which index should it add?");
    assert.equal(options(container).length, 4, "three choices and Something else");
    assert.equal(container.querySelector("kbd"), null, "the transcript draws a key cap");
    assert.equal(pickedName(container), "composite", "the recommended option starts picked");

    await act(() => {
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "4", cancelable: true }));
    });
    assert.equal(pickedName(container), "composite", "a digit key picked an option in a transcript");

    await click(options(container)[3]);
    assert.equal(pickedName(container), "Something else", "a click picks the option it lands on");
    await click(options(container)[0]);
    assert.equal(pickedName(container), "composite", "a click picks the recommended option");
    const confirm = container.querySelector<HTMLButtonElement>(".ask .btn.primary");
    assert.ok(confirm !== null, "the confirm button is drawn");
    assert.equal(confirm.textContent, "Answer with composite");
    await click(confirm);
    await settle(t, () => fake.answers.length === 1, "the answer is sent");
    assert.deepEqual(fake.answers, [
      [ringEpic, "Q1", "composite", "Add a composite index on keeper and shift."],
    ]);
  });
});

test("AskAnswer: a settled question draws the settled line, with its project and slug, and no Ask", async (t) => {
  const fake = new FakeEpics(epicReading([]));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => container.querySelector(".ask.folded.settled") !== null, "the settled line is drawn");
    assert.deepEqual(
      [...container.querySelectorAll(".ask.folded.settled span")].map((span) => span.textContent),
      ["Settled", "This question is settled.", "· orrery-1a2b3c4 · planner-runner"],
    );
    assertNone(container.querySelector(".ask:not(.folded)"), "a settled question draws an open Ask");
    assert.equal(container.querySelectorAll(".ring-line").length, 1, "the ring line stays");
  });
});

test("EventRing: a question event whose epic cannot be read draws neither the Ask nor the settled line", async (t) => {
  const fake = new FakeEpics(new Error("the epic is gone"));
  await withRing(questionEvent, fake.calls(), async (container) => {
    await settle(t, () => fake.reads.length === 1, "the ring reads its epic");
    await act(turn);
    assertNone(container.querySelector(".ask"), "an unreadable epic draws no Ask");
    assertNone(container.querySelector(".settled"), "an unreadable epic draws no settled line");
    assert.equal(container.querySelectorAll(".ring-line").length, 1, "the ring line stays");
  });
});

// collapsedChoices are the thirty choices the question declares: the two the planner lists, then the rest it counts.
const collapsedChoices = [
  ...threeChoices.slice(0, 2),
  ...Array.from({ length: 28 }, (_, index) => ({ id: `choice-${index + 3}`, does: `Choice ${index + 3} does its part.` })),
];
const collapsedQuestion: MessageInitShape<typeof EpicQuestionSchema> = { ...openQuestion, choices: collapsedChoices };
const questionLine = "epic_get with part question and question Q1 reads it whole; epic_answer answers it.";
const reportLine = "epic_get with part report reads its report.";

// plannedBlocks are the four kinds' blocks as the planner writes them: the question with its choices collapsed to two
// listed and a count of the rest, the park with its one choice, and the release and end with their report line.
const plannedBlocks = [
  {
    kind: "question",
    tail: "Q1",
    body: [
      `${title} asks a question.`,
      "Q1 which-index: Which index should it add?",
      "  composite: Add a composite index on keeper and shift.",
      "  partial: Add a partial index on open shifts only.",
      "  and 28 more choices",
      "  other: answer in your own words",
      "  recommended: composite (high): open shifts are the only rows the backfill scans",
      questionLine,
    ].join("\n"),
    sentence: `${title} asks a question.`,
  },
  {
    kind: "park",
    tail: "Q1",
    body: [
      `${title} is parked.`,
      "Q1 park:",
      "  retry: end the park and start a turn on the waiting triggers, as orrery epic retry does",
      "  other: answer in your own words",
      "  recommended: retry (low): no model judges a park; retry runs the epic again on its waiting triggers",
      "parked: the reason",
      questionLine,
    ].join("\n"),
    sentence: `${title} is parked.`,
  },
  {
    kind: "release",
    tail: "released",
    body: [`${title} is released.`, reportLine].join("\n"),
    sentence: `${title} is released.`,
  },
  {
    kind: "end",
    tail: "done",
    body: [`${title} is done.`, reportLine].join("\n"),
    sentence: `${title} is done.`,
  },
];

test("EventRing: the planner's bounded question, park, release and end bodies each draw their card, a question whose choices collapsed among them", async (t) => {
  for (const planned of plannedBlocks) {
    const event = eventOf(eventBlock(planned.kind, planned.tail, planned.body));
    assert.deepEqual(
      event,
      { kind: planned.kind, epic: ringEpic, tail: planned.tail, sentence: planned.sentence },
      `${planned.kind} block reads its kind, epic, tail and epic line`,
    );
    const fake = new FakeEpics(epicReading([collapsedQuestion]));
    await withRing(event, fake.calls(), async (container) => {
      assert.equal(container.querySelectorAll(".ring-line").length, 1, `${planned.kind} draws one ringed line`);
      assert.equal(container.querySelector(".ring-sentence")?.textContent, planned.sentence, `${planned.kind} draws its epic line`);
      assert.equal(container.querySelector(".ring-icon")?.classList.contains(planned.kind), true, `${planned.kind} draws its icon`);
      assert.equal(container.querySelector(".ring-slug")?.textContent, "planner-runner", `${planned.kind} draws the epic's slug`);
      if (planned.kind === "question") {
        await settle(t, () => container.querySelector(".ask") !== null, "the question's Ask is drawn");
        const drawn = options(container).map((option) => option.querySelector(".nm")?.textContent);
        assert.deepEqual(
          drawn,
          [...collapsedChoices.map((choice) => choice.id), "Something else"],
          "the Ask draws all 30 choices from the epic reading, then Something else",
        );
      }
    });
  }
});
