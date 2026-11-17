import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import type { Conversation } from "./conversation";
import { type EpicsGetResponse, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type ConversationItem, ConversationItemSchema, ConversationStateSchema } from "./gen/orrery/v1/orrery_pb";
import { Reader } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";
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
    held: [],
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

// FakeEpics answers the card's reads with its detail, or refuses them when given an error, and records each call the
// card makes. calls makes the epic calls over it at a tick.
class FakeEpics {
  readonly reads: string[] = [];
  readonly pauses: string[] = [];
  readonly resumes: string[] = [];
  readonly opened: string[] = [];
  private readonly answer: EpicsGetResponse | Error;

  constructor(answer: EpicsGetResponse | Error) {
    this.answer = answer;
  }

  calls(tick = 1): EpicCalls {
    return {
      get: async (id) => {
        this.reads.push(id);
        if (this.answer instanceof Error) {
          throw this.answer;
        }
        return this.answer;
      },
      pause: async (id) => {
        this.pauses.push(id);
      },
      resume: async (id) => {
        this.resumes.push(id);
      },
      answer: async () => "",
      openEpic: (id) => {
        this.opened.push(id);
      },
      tick,
    };
  }
}

// settle waits until check holds, each round running inside act, so the renders a read or a command causes are applied
// before the next check. It ends with the test's own signal, so a wait that never holds fails the test by name.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

// drawn waits until the card has read its epic: its track is drawn, or its unavailable line is.
function drawn(t: TestContext, container: HTMLElement): Promise<void> {
  return settle(t, () => container.querySelector(".track, .unavailable") !== null, "the card reads its epic");
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

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// trackStates returns the state of each part of the track in order: done, now or ahead.
function trackStates(container: HTMLElement): string[] {
  return [...container.querySelectorAll(".track .part")].map(
    (part) => ["done", "now", "ahead"].find((state) => part.classList.contains(state)) ?? "",
  );
}

function hasButton(container: HTMLElement, label: string): boolean {
  return [...container.querySelectorAll("button")].some((button) => button.textContent === label);
}

function buttonNamed(container: HTMLElement, label: string): HTMLElement {
  const buttons = [...container.querySelectorAll<HTMLElement>("button")];
  const button = buttons.find((candidate) => candidate.textContent === label);
  if (button === undefined) {
    throw new Error(`a ${label} button is drawn`);
  }
  return button;
}

async function click(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

// trackCases are the phases the track draws: the pill word the card shows, which is also the part the track marks now,
// and the state of each of the five parts in order.
const trackCases = [
  {
    name: "an accepted epic with no task",
    detail: orreryd("accepted"),
    pill: "planning",
    states: ["done", "now", "ahead", "ahead", "ahead"],
  },
  {
    name: "an accepted epic with a task",
    detail: orreryd("accepted", {
      buildTasks: [{ id: "t1", title: "Login", status: "dispatched", requirements: ["R1"] }],
    }),
    pill: "building",
    states: ["done", "done", "now", "ahead", "ahead"],
  },
  {
    name: "a built epic",
    detail: orreryd("built"),
    pill: "building",
    states: ["done", "done", "now", "ahead", "ahead"],
  },
  {
    name: "a reviewing epic",
    detail: orreryd("reviewing"),
    pill: "building",
    states: ["done", "done", "now", "ahead", "ahead"],
  },
  {
    name: "a releasing epic",
    detail: orreryd("releasing"),
    pill: "releasing",
    states: ["done", "done", "done", "now", "ahead"],
  },
  {
    name: "a stopped epic",
    detail: orreryd("stopped"),
    pill: "releasing",
    states: ["done", "done", "done", "now", "ahead"],
  },
  {
    name: "a released epic",
    detail: orreryd("released", {}, false),
    pill: "report",
    states: ["done", "done", "done", "done", "now"],
  },
];

for (const trackCase of trackCases) {
  test(`EpicWidget: ${trackCase.name} draws the track at ${trackCase.pill}`, async (t) => {
    const fake = new FakeEpics(trackCase.detail);
    await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
      await drawn(t, container);
      assert.deepEqual(fake.reads, [epicId]);
      assert.deepEqual(trackStates(container), trackCase.states);
      assert.equal(textOf(container, ".track .part.now"), trackCase.pill);
      assert.equal(textOf(container, ".pill"), trackCase.pill);
    });
  });
}

test("EpicWidget: the pill reads paused, and a parked epic's pill reads parked", async (t) => {
  const paused = new FakeEpics(orreryd("accepted", { paused: true }));
  await withReader([createCall(), resultItem(createId, "created")], paused.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(textOf(container, ".pill"), "paused");
  });
  const parked = new FakeEpics(orreryd("built", { parked: "waiting on the owner" }));
  await withReader([createCall(), resultItem(createId, "created")], parked.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(textOf(container, ".pill"), "parked");
  });
});

test("EpicWidget: the now sentence reads the running wedge's task, phase and doing", async (t) => {
  const fake = new FakeEpics(
    orreryd("accepted", {
      buildTasks: [{ id: "t1", title: "Login", status: "dispatched", requirements: ["R1"] }],
      steps: [
        {
          id: "s1",
          task: "t1",
          agent: "builder",
          stage: "implement",
          started: 9_000n,
          stageStarted: 9_500n,
          doing: "writing the login",
        },
      ],
    }),
  );
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(textOf(container, ".now-sentence"), "t1 · implementing · writing the login");
  });
});

test("EpicWidget: a requirement row is green when proven, red when its red test fails, grey otherwise", async (t) => {
  const fake = new FakeEpics(
    orreryd("built", {
      requirements: [
        { id: "R1", text: "A user signs in", runs: [{ phase: "green", passed: true, verdict: "holds" }] },
        { id: "R2", text: "A user signs out", runs: [{ phase: "red", passed: true, verdict: "fails" }] },
        { id: "R3", text: "A user resets a password", runs: [] },
        { id: "R4", text: "A user deletes an account", runs: [{ phase: "red", passed: false, verdict: "holds" }] },
      ],
    }),
  );
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    const rows = [...container.querySelectorAll(".reqs li")].map((row) => row.className);
    assert.deepEqual(rows, ["green", "red", "grey", "grey"]);
    assert.equal(textOf(container, ".count"), "1 of 4 proven");
  });
});

test("EpicWidget: the open question's title draws under the requirements", async (t) => {
  const fake = new FakeEpics(
    orreryd("accepted", { questions: [{ id: "Q1", text: "Which cache should it use?", turn: 2n, asked: 9_000n }] }),
  );
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(textOf(container, ".question"), "Which cache should it use?");
  });
});

test("EpicWidget: Pause calls pause on an accepted epic, Resume calls resume on a paused one", async (t) => {
  const accepted = new FakeEpics(orreryd("accepted"));
  await withReader([createCall(), resultItem(createId, "created")], accepted.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(hasButton(container, "Resume"), false);
    await click(buttonNamed(container, "Pause"));
    assert.deepEqual(accepted.pauses, [epicId]);
  });
  const paused = new FakeEpics(orreryd("accepted", { paused: true }));
  await withReader([createCall(), resultItem(createId, "created")], paused.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(hasButton(container, "Pause"), false);
    await click(buttonNamed(container, "Resume"));
    assert.deepEqual(paused.resumes, [epicId]);
  });
});

test("EpicWidget: a pause the epic refuses shows the command's fixed line", async (t) => {
  const fake = new FakeEpics(orreryd("accepted"));
  const calls: EpicCalls = {
    ...fake.calls(),
    pause: async () => {
      throw new Error("the epic refused the pause");
    },
  };
  await withReader([createCall(), resultItem(createId, "created")], calls, async (container) => {
    await drawn(t, container);
    await click(buttonNamed(container, "Pause"));
    await settle(t, () => container.querySelector(".error") !== null, "the refused pause draws its line");
    assert.equal(textOf(container, ".error"), "epic command failed");
  });
});

test("EpicWidget: the open-the-epic control in the head opens the epic by its id", async (t) => {
  const fake = new FakeEpics(orreryd("built"));
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    await click(buttonNamed(container, "open the epic ›"));
    assert.deepEqual(fake.opened, [epicId]);
  });
});

test("EpicWidget: an epic that cannot be read shows its slug and not available", async (t) => {
  const fake = new FakeEpics(new Error("no such epic"));
  await withReader([createCall(), resultItem(createId, "created")], fake.calls(), async (container) => {
    await drawn(t, container);
    assert.equal(textOf(container, ".slug"), "demo-epic");
    assert.equal(textOf(container, ".unavailable"), "not available");
    assert.equal(container.querySelector(".track"), null);
  });
});

test("EpicWidget: a refused create and a create with no result yet keep their tool rows", async () => {
  const fake = new FakeEpics(orreryd("accepted"));
  await withReader([createCall(), resultItem(createId, "refused", true)], fake.calls(), (container) => {
    assert.equal(container.querySelector(".epic-card"), null);
    assert.equal(textOf(container, ".call b"), "epic_create");
    assert.equal(textOf(container, ".call .fail"), "✗");
  });
  await withReader([createCall()], fake.calls(), (container) => {
    assert.equal(container.querySelector(".epic-card"), null);
    assert.equal(textOf(container, ".call b"), "epic_create");
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
    assert.equal(textOf(rows[0], ".fold.calls b"), "3 tool calls");
    const card = rows[1].querySelector(".epic-card");
    assert.notEqual(card, null, "the card is the middle row");
    assert.equal(card?.closest(".fold, .calls") ?? null, null, "the card sits in no fold");
    assert.equal(textOf(rows[2], ".fold.calls b"), "3 tool calls");
  });
});

test("EpicWidget: the card reads its epic again only when the tick changes", async (t) => {
  const fake = new FakeEpics(orreryd("accepted"));
  const items = [createCall(), resultItem(createId, "created")];
  const window = installTestDom();
  const container = document.createElement("div");
  const draw = (epicCalls: EpicCalls) => (
    <Reader conversation={conversationOf(items)} seenAt={seenAt} open={false} epicCalls={epicCalls} />
  );
  try {
    await act(() => {
      render(draw(fake.calls(1)), container);
    });
    await drawn(t, container);
    assert.deepEqual(fake.reads, [epicId]);
    await act(() => {
      render(draw(fake.calls(1)), container);
    });
    await act(turn);
    assert.deepEqual(fake.reads, [epicId], "a new calls object at the same tick reads nothing");
    await act(() => {
      render(draw(fake.calls(2)), container);
    });
    await settle(t, () => fake.reads.length === 2, "the card reads its epic at the new tick");
    assert.deepEqual(fake.reads, [epicId, epicId]);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
});
