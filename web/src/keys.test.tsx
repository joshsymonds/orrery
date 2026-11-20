import assert from "node:assert/strict";
import { isDeepStrictEqual } from "node:util";
import { type TestContext, test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { Dock } from "./dock";
import type { DockFacts } from "./dockwords";
import { EpicQuestionSchema, EpicSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { type Chord, keyGroups, type Where } from "./keys";
import { type EscapeTarget, handleKey, type KeyContext } from "./keymap";
import { NowSection } from "./nowsection";
import type { Row } from "./rows";
import { mockTimers } from "./testclock";
import { assertSame, installTestDom, removeTestDom } from "./testdom";
import { type Call, type Fake, fakeContext } from "./testkeys";

// The sweep presses a fixed set of keys through each context's handlers, bare and with each one modifier, and checks
// that the chords which act are the table's chords for that context. Each test's name carries the token KeyList.

// Modifier is a modifier a press holds, one at a time.
type Modifier = "shift" | "ctrl" | "alt" | "meta";

const modifiers: Modifier[] = ["shift", "ctrl", "alt", "meta"];

const modifierInit: Record<Modifier, KeyboardEventInit> = {
  shift: { shiftKey: true },
  ctrl: { ctrlKey: true },
  alt: { altKey: true },
  meta: { metaKey: true },
};

// sweptKeys are the keys every context is pressed with: letters in both cases, digits, every ASCII punctuation
// character, the arrows, Enter, Escape, Tab, and the page keys.
const letters = "abcdefghijklmnopqrstuvwxyz";
const punctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~";
const sweptKeys: string[] = [
  ...letters,
  ...letters.toUpperCase(),
  ..."0123456789",
  ...punctuation,
  "ArrowUp",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "Enter",
  "Escape",
  "Tab",
  "PageUp",
  "PageDown",
];

// eventFor is a cancelable keydown for key, with the modifier held when one is given.
function eventFor(key: string, modifier: Modifier | null): KeyboardEvent {
  return new KeyboardEvent("keydown", {
    key,
    cancelable: true,
    ...(modifier === null ? {} : modifierInit[modifier]),
  });
}

// Outcome is what one press did: the calls its handler made, whether it prevented the default, the text its box held
// after the press, and the card left selected, -1 for none. A press acts when it made a call, prevented the default, or
// selected a card.
interface Outcome {
  acted: boolean;
  prevented: boolean;
  calls: Call[];
  text: string;
  picked: number;
}

function outcome(prevented: boolean, calls: Call[], text = "", picked = -1): Outcome {
  return { acted: prevented || calls.length > 0 || picked !== -1, prevented, calls, text, picked };
}

// sameEffect reports whether two presses did the same: the same calls, default, text and selected card.
function sameEffect(a: Outcome, b: Outcome): boolean {
  return isDeepStrictEqual([a.prevented, a.calls, a.text, a.picked], [b.prevented, b.calls, b.text, b.picked]);
}

// Press makes one key press in a context, bare or with a modifier, and reports what it did.
type Press = (key: string, modifier: Modifier | null) => Promise<Outcome>;

// chordName names a chord as its key and then each modifier it holds, in the order shift, ctrl, alt, meta.
function chordName(chord: Chord): string {
  return [chord.key, ...modifiers.filter((name) => chord[name] === true)].join("+");
}

// sweep presses every swept key bare and then with each modifier, and returns the chords that act, sorted. A modifier
// press whose effect equals the bare key's is the bare key, so it adds no chord of its own.
async function sweep(press: Press): Promise<string[]> {
  const acting = new Set<string>();
  for (const key of sweptKeys) {
    const bare = await press(key, null);
    if (bare.acted) {
      acting.add(key);
    }
    for (const modifier of modifiers) {
      const modified = await press(key, modifier);
      if (modified.acted && !sameEffect(modified, bare)) {
        const chord: Chord = { key };
        chord[modifier] = true;
        acting.add(chordName(chord));
      }
    }
  }
  return [...acting].sort();
}

// chordsIn is the table's chords for a context, sorted and without repeats.
function chordsIn(where: Where): string[] {
  const names = new Set<string>();
  for (const group of keyGroups) {
    for (const line of group.lines) {
      for (const act of line.acts) {
        if (act.in.includes(where)) {
          for (const chord of act.chords) {
            names.add(chordName(chord));
          }
        }
      }
    }
  }
  return [...names].sort();
}

// expectTable fails unless the chords the sweep finds acting in where are the table's chords for it.
async function expectTable(where: Where, press: Press): Promise<void> {
  assert.deepEqual(await sweep(press), chordsIn(where), `the ${where} context's acting chords are the table's`);
}

// withDom runs check with a fresh DOM installed, and removes it after.
async function withDom(check: () => Promise<void>): Promise<void> {
  const window = installTestDom();
  try {
    await check();
  } finally {
    await removeTestDom(window);
  }
}

// row is a pane row with the key, as the list holds it.
function row(key: string): Row {
  return {
    putOff: false,
    tool: "",
    key,
    id: key,
    archiveId: "",
    kind: "pane",
    label: key,
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
  };
}

// listRows are the rows the list shows in its flat order, and the cursor rests on the middle one.
const listRows = [row("a"), row("b"), row("c")];

// listFake is a context for the list with its cursor on the middle row, with init's changes made.
function listFake(init: Partial<KeyContext> = {}): Fake {
  const cursor = listRows[1];
  return fakeContext({
    index: 1,
    flat: listRows,
    cursorItem: cursor,
    cursorRow: cursor,
    cursorRowKey: cursor.key,
    ...init,
  });
}

// handlerPress presses a key through handleKey against a fresh context from fake, which runs before each press.
function handlerPress(fake: () => Fake): Press {
  return async (key, modifier) => {
    const { context, calls } = fake();
    const event = eventFor(key, modifier);
    handleKey(event, context);
    return outcome(event.defaultPrevented, calls);
  };
}

// pressTextarea mounts the tree that tree builds, with its textarea holding text when text is given, presses the key
// in the textarea, and reports what the calls the tree made and the textarea's text after the press came to.
async function pressTextarea(
  tree: (calls: Call[]) => JSX.Element,
  text: string,
  key: string,
  modifier: Modifier | null,
): Promise<Outcome> {
  const calls: Call[] = [];
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(tree(calls), container);
    });
    const box = container.querySelector("textarea");
    assert.ok(box, "the box renders a textarea");
    if (text !== "") {
      await act(() => {
        box.value = text;
        box.dispatchEvent(new Event("input", { bubbles: true }));
      });
    }
    const event = eventFor(key, modifier);
    await act(() => {
      box.dispatchEvent(event);
    });
    return outcome(event.defaultPrevented, calls, box.value);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
  }
}

// dockFacts are the facts of a dock in the sweep: a session's done, or a running step's.
function dockFacts(step: DockFacts["step"]): DockFacts {
  return {
    kind: step === null ? "session" : "step",
    name: step === null ? "web" : step.label,
    state: step === null ? "done" : "working",
    interrupting: false,
    interruptingSince: 0,
    turnSince: 0,
    tool: null,
    changed: 0,
    reason: "",
    interruptedAt: 0,
    putOff: false,
    step,
  };
}

// composerPress presses in a Dock whose box holds text. Its send and pull never settle, so the box keeps its text.
function composerPress(text: string): Press {
  return (key, modifier) =>
    pressTextarea(
      (calls) => (
        <Dock
          facts={dockFacts(null)}
          now={0}
          send={(sent, promptId) => {
            calls.push(["send", sent, promptId]);
            return new Promise<void>(() => {});
          }}
          pull={() => {
            calls.push(["pull"]);
            return new Promise<string>(() => {});
          }}
          newPromptId={() => "prompt"}
          onPage={(direction) => {
            calls.push(["onPage", direction]);
            return true;
          }}
          onFind={() => {
            calls.push(["onFind"]);
          }}
        />
      ),
      text,
      key,
      modifier,
    );
}

// stepPress presses in a step's Dock whose box holds text. Its nudge never settles, so the box keeps its text.
function stepPress(text: string): Press {
  return (key, modifier) =>
    pressTextarea(
      (calls) => (
        <Dock
          facts={dockFacts({ label: "api", held: false, doing: "" })}
          now={0}
          send={(sent) => {
            calls.push(["nudge", sent]);
            return new Promise<void>(() => {});
          }}
          pull={null}
          newPromptId={() => "prompt"}
          stepCalls={{ hold: () => Promise.resolve(), stop: () => Promise.resolve() }}
        />
      ),
      text,
      key,
      modifier,
    );
}

// eightChoices are the choices of the epic page's question, which recommends none.
const eightChoices = Array.from({ length: 8 }, (_, index) => ({
  id: `choice-${index + 1}`,
  does: `Does thing ${index + 1}.`,
}));

// pickedCard is the index of the option that holds the pick, or -1 when none does.
function pickedCard(container: HTMLElement): number {
  return [...container.querySelectorAll(".ask .opts > .opt")].findIndex((card) => card.classList.contains("picked"));
}

// questionPress presses in a fresh epic page's question, whose digit keys listen on the document.
async function questionPress(key: string, modifier: Modifier | null): Promise<Outcome> {
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(
        <NowSection
          detail={create(EpicsGetResponseSchema, {
            epic: create(EpicSchema, { id: "orrery/lanes", slug: "lanes", repository: "orrery", live: true }),
            questions: [create(EpicQuestionSchema, { id: "Q1", text: "Which lane should it use?", choices: eightChoices })],
          })}
          now={0}
          narrow={false}
          numberKeys
          onAnswer={() => Promise.resolve("")}
          onRetry={() => {}}
          onStop={() => {}}
          onResume={() => {}}
        />,
        container,
      );
    });
    assert.equal(pickedCard(container), -1, "a fresh question selects no card");
    const event = eventFor(key, modifier);
    await act(() => {
      document.dispatchEvent(event);
    });
    return outcome(event.defaultPrevented, [], "", pickedCard(container));
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
  }
}

test("KeyList: the table has its five groups in order", () => {
  assert.deepEqual(
    keyGroups.map((group) => group.title),
    ["Moving", "The inbox", "Reading", "Answering and stopping", "Everywhere"],
  );
});

test("KeyList: the list's keys act exactly as the table says", async () => {
  await withDom(() => expectTable("list", handlerPress(() => listFake())));
});

test("KeyList: the inbox at rest's keys act exactly as the table says", async () => {
  await withDom(() => expectTable("inbox-at-rest", handlerPress(() => listFake({ inboxHidden: true }))));
});

test("KeyList: the help panel's keys act exactly as the table says", async () => {
  await withDom(() => expectTable("help", handlerPress(() => listFake({ helpOpen: true }))));
});

test("KeyList: the palette's keys act exactly as the table says", async () => {
  await withDom(() => expectTable("palette", handlerPress(() => listFake({ pickerOpen: true }))));
});

test("KeyList: the reply box's keys act exactly as the table says", async () => {
  await withDom(async () => {
    const box = document.createElement("textarea");
    document.body.append(box);
    const target: EscapeTarget = { row: listRows[1], column: "cursor", state: "working" };
    await expectTable(
      "reply-box",
      handlerPress(() => {
        box.focus();
        assertSame(document.activeElement, box, "the reply box holds focus for the press");
        return listFake({ replyBoxTarget: () => target });
      }),
    );
  });
});

test("KeyList: the epic page's digit keys act exactly as the table says", async () => {
  await withDom(() => expectTable("epic-page", questionPress));
});

test("KeyList: the empty reply box's keys act exactly as the table says", async (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  await withDom(() => expectTable("reply-empty", composerPress("")));
});

test("KeyList: the typed reply box's keys act exactly as the table says", async (t: TestContext) => {
  mockTimers(t, ["setTimeout"]);
  await withDom(() => expectTable("reply-typed", composerPress("typed")));
});

test("KeyList: the step box's keys act exactly as the table says", async () => {
  await withDom(() => expectTable("step-box", stepPress("typed")));
});
