import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { type EpicEvent, EventRing, epicEventOf } from "./eventring";
import { liveEpoch } from "./fixtures/clock";
import { noEpics } from "./epics";
import { installTestDom, removeTestDom } from "./testdom";

const ringEpic = "orrery-1a2b3c4/rota-export";

// moveBlock is the block a move event carries, as orreryd writes it: the opening tag, the body and the closing tag.
function moveBlock(body: string): string {
  return `<orrery-event kind="moved" id="epic:${ringEpic}:A1">\n${body}\n</orrery-event>`;
}

// leftBody is the body the conversation the epic left gets: the session it moved to and the mover as orreryd names it.
function leftBody(by: string): string {
  return `Epic ${ringEpic} (Rota export) moved to session sess-lamp-11, by ${by}.`;
}

// takerBody is the steering prompt the conversation that took the epic gets.
const takerBody = [
  "orreryd builds this epic; this session steers it by answering its questions and events, and changes nothing in the repository.",
  `Epic: ${ringEpic}`,
].join("\n");

// eventOf reads the event a block is. The tests give it only blocks epicEventOf reads.
function eventOf(text: string): EpicEvent {
  const event = epicEventOf(text);
  if (event === null) {
    throw new Error(`${text} is read as an epic's event`);
  }
  return event;
}

// movedCalls makes the epic calls a moved line reads: sessionTitle names the sessions titles lists, and openEpic records
// each epic it opens. A moved line reads nothing else, so every other call refuses.
function movedCalls(titles: Record<string, string | undefined>, opened: string[]): EpicCalls {
  const refused = () => Promise.reject(new Error("a moved line reads no more than its session titles"));
  return {
    get: refused,
    pause: refused,
    resume: refused,
    answer: refused,
    openEpic: (id) => {
      opened.push(id);
    },
    openStep: () => {},
    stepResult: refused,
    sessionTitle: (session) => titles[session],
    now: Date.parse(liveEpoch) / 1000,
    epics: noEpics,
  };
}

// withRing draws the ring of event with calls into a fresh container, runs check on it, then removes it.
async function withRing(
  event: EpicEvent,
  calls: EpicCalls,
  check: (container: HTMLElement) => void | Promise<void>,
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

test("MoveLines: a left block reads as a moved event on side left, with the session it went to and its mover", () => {
  assert.deepEqual(eventOf(moveBlock(leftBody("the owner"))), {
    kind: "moved",
    epic: ringEpic,
    tail: "A1",
    sentence: "",
    moved: { side: "left", to: "sess-lamp-11", by: "the owner" },
  });
});

test("MoveLines: the steering block a taker gets reads as a moved event on side took", () => {
  assert.deepEqual(eventOf(moveBlock(takerBody)), {
    kind: "moved",
    epic: ringEpic,
    tail: "A1",
    sentence: "",
    moved: { side: "took" },
  });
});

test("MoveLines: a moved block whose id is not epic:<id>:<tail> is no event", () => {
  for (const id of [`epic:${ringEpic}`, `epic::A1`, `step:${ringEpic}:A1`, `other:${ringEpic}:A1`]) {
    const text = `<orrery-event kind="moved" id="${id}">\n${leftBody("the owner")}\n</orrery-event>`;
    assert.equal(epicEventOf(text), null, `${id} is no moved event`);
  }
});

// leftCases are the left line's three namings: the owner's move names the session it went to by its title, a
// conversation mover that lists no title is named by its session id, and orreryd names itself.
const leftCases = [
  {
    by: "the owner",
    titles: { "sess-lamp-11": "Lamp work" },
    sentence: "rota-export moved to “Lamp work”, by you. Its questions go there now.",
  },
  {
    by: "session sess-rota-14",
    titles: { "sess-rota-14": "Rota plan" },
    sentence: "rota-export moved to session sess-lamp-11, by “Rota plan”. Its questions go there now.",
  },
  {
    by: "orreryd",
    titles: {},
    sentence: "rota-export moved to session sess-lamp-11, by orreryd. Its questions go there now.",
  },
];

test("MoveLines: the line the epic left names the epic, where it went and who moved it, and a click opens the epic", async () => {
  for (const kase of leftCases) {
    const event = eventOf(moveBlock(leftBody(kase.by)));
    const opened: string[] = [];
    await withRing(event, movedCalls(kase.titles, opened), async (container) => {
      const lines = container.querySelectorAll<HTMLButtonElement>(".ring-line");
      assert.equal(lines.length, 1, `${kase.by} draws one ringed line`);
      const icon = container.querySelector(".ring-icon");
      assert.equal(icon?.classList.contains("moved"), true, `${kase.by} draws the moved icon`);
      assert.equal(icon?.textContent, "⇄", `${kase.by} draws the moved glyph`);
      assert.equal(container.querySelector(".ring-sentence")?.textContent, kase.sentence, `${kase.by} names where and who`);
      assert.equal(container.querySelector(".ring-slug")?.textContent, "rota-export", `${kase.by} draws the epic's slug`);
      await act(() => {
        lines[0].click();
      });
      assert.deepEqual(opened, [ringEpic], `${kase.by} opens the epic on click`);
    });
  }
});

test("MoveLines: the line the conversation that took the epic gets says it now steers it, and a click opens the epic", async () => {
  const opened: string[] = [];
  await withRing(eventOf(moveBlock(takerBody)), movedCalls({}, opened), async (container) => {
    const lines = container.querySelectorAll<HTMLButtonElement>(".ring-line");
    assert.equal(lines.length, 1, "the taker draws one ringed line");
    assert.equal(
      container.querySelector(".ring-sentence")?.textContent,
      "Took over rota-export. Its questions and its ending come here now.",
    );
    assert.equal(container.querySelector(".ring-slug")?.textContent, "rota-export");
    await act(() => {
      lines[0].click();
    });
    assert.deepEqual(opened, [ringEpic], "the taker's line opens the epic on click");
  });
});
