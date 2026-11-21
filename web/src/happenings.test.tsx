import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicHistoryEntry, EpicHistoryEntrySchema, type EpicStep, EpicStepSchema } from "./gen/orrery/v1/epics_pb";
import { JustHappened, OutputTails } from "./happenings";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

function entry(init: MessageInitShape<typeof EpicHistoryEntrySchema>): EpicHistoryEntry {
  return create(EpicHistoryEntrySchema, init);
}

function step(init: MessageInitShape<typeof EpicStepSchema>): EpicStep {
  return create(EpicStepSchema, init);
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

async function withRender(
  element: JSX.Element,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("Just happened shows the newest three, and +n earlier opens the rest in place and goes", async () => {
  const entries = [
    entry({ time: 100n, cursor: "c1", item: { case: "turn", value: { seq: 1n, state: "succeeded" } } }),
    entry({ time: 200n, cursor: "c2", item: { case: "trigger", value: { cause: "owner asked" } } }),
    entry({ time: 300n, cursor: "c3", item: { case: "step", value: { id: "s1", task: "b1", end: "done" } } }),
    entry({ time: 400n, cursor: "c4", item: { case: "decision", value: { text: "use the table" } } }),
    entry({ time: 500n, cursor: "c5", item: { case: "tell", value: { text: "build one is in" } } }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, async (container) => {
    assert.equal(textOf(container, "section.happen.just h3"), "Just happened");
    assert.deepEqual(textsOf(container, "section.happen.just .at"), [clock(500n), clock(400n), clock(300n)]);
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "build one is in",
      "decision by unknown · use the table",
      "b1 · done",
    ]);
    assert.equal(textOf(container, "section.happen.just button.more"), "+2 earlier");
    assert.equal(container.querySelector("section.happen.just button.more")?.getAttribute("type") ?? null, "button");

    await act(() => {
      container.querySelector<HTMLButtonElement>("section.happen.just button.more")?.click();
    });
    assert.equal(container.querySelectorAll("section.happen.just button.more").length, 0);
    assert.deepEqual(textsOf(container, "section.happen.just .at"), [
      clock(500n),
      clock(400n),
      clock(300n),
      clock(200n),
      clock(100n),
    ]);
  });
});

test("each kind of entry reads its own text, and a history of three or fewer has no +n button", async () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "k1",
      item: { case: "turn", value: { seq: 3n, state: "succeeded", outcome: "shipped" } },
    }),
    entry({ time: 110n, cursor: "k2", item: { case: "turn", value: { seq: 4n, state: "running" } } }),
    entry({ time: 120n, cursor: "k3", item: { case: "trigger", value: { cause: "owner asked" } } }),
    entry({ time: 130n, cursor: "k4", item: { case: "step", value: { id: "s1", task: "b1", end: "" } } }),
    entry({ time: 140n, cursor: "k5", item: { case: "step", value: { id: "s2", agent: "builder", end: "done" } } }),
    entry({
      time: 150n,
      cursor: "k6",
      item: { case: "evidence", value: { item: "R1", phase: "red", state: "capturing" } },
    }),
    entry({
      time: 160n,
      cursor: "k7",
      item: { case: "evidence", value: { item: "R1", phase: "green", state: "ended", passed: true } },
    }),
    entry({
      time: 170n,
      cursor: "k8",
      item: { case: "evidence", value: { item: "R2", phase: "green", state: "ended", passed: false } },
    }),
    entry({ time: 180n, cursor: "k9", item: { case: "decision", value: { text: "use the table" } } }),
    entry({ time: 190n, cursor: "k10", item: { case: "tell", value: { text: "build one is in" } } }),
  ];
  await withRender(<JustHappened entries={entries} steps={[]} />, async (container) => {
    assert.equal(textOf(container, "section.happen.just button.more"), "+7 earlier");
    await act(() => {
      container.querySelector<HTMLButtonElement>("section.happen.just button.more")?.click();
    });
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "build one is in",
      "decision by unknown · use the table",
      "R2 green run · failed",
      "R1 green run · passed",
      "R1 red run · running",
      "builder · done",
      "b1 · started",
      "trigger · owner asked",
      "turn 4 · running",
      "turn 3 · succeeded · shipped",
    ]);
  });

  const few = entries.slice(0, 3);
  return withRender(<JustHappened entries={few} steps={[]} />, (container) => {
    assert.equal(container.querySelectorAll("section.happen.just button.more").length, 0);
    assert.equal(container.querySelectorAll("section.happen.just li").length, 3);
  });
});

test("a failed check's output tails show under its step entry, and a passed check's do not", () => {
  const entries = [
    entry({
      time: 100n,
      cursor: "s1",
      item: { case: "step", value: { id: "s1", task: "b1", end: "checks_failed" } },
    }),
  ];
  const steps = [
    step({
      id: "s2",
      task: "b2",
      end: "checks_failed",
      checks: [{ round: 1, name: "lint", ok: false, stdout: "other step out" }],
    }),
    step({
      id: "s1",
      task: "b1",
      end: "checks_failed",
      checks: [
        { round: 1, name: "lint", ok: false, stdout: "lint out", stderr: "lint err" },
        { round: 1, name: "unit", ok: true, stdout: "unit out" },
        { round: 1, name: "vet", ok: false },
      ],
    }),
  ];
  return withRender(<JustHappened entries={entries} steps={steps} />, (container) => {
    const row = container.querySelector("section.happen.just li");
    assert.equal(textOf(container, "section.happen.just .what"), "b1 · checks_failed");
    assert.equal(row?.querySelectorAll(".tails").length, 1);
    assert.deepEqual(textsOf(container, "section.happen.just .tail-label"), ["stdout", "stderr"]);
    assert.deepEqual(textsOf(container, "section.happen.just .log"), ["lint out", "lint err"]);
  });
});

test("OutputTails draws each non-empty tail under its own label", async () => {
  await withRender(<OutputTails stdout={"FAIL\n"} stderr="" />, (container) => {
    assert.deepEqual(textsOf(container, ".tail-label"), ["stdout"]);
    assert.deepEqual(textsOf(container, ".log"), ["FAIL\n"]);
  });
  await withRender(<OutputTails stdout="" stderr={"boom\n"} />, (container) => {
    assert.deepEqual(textsOf(container, ".tail-label"), ["stderr"]);
    assert.deepEqual(textsOf(container, ".log"), ["boom\n"]);
  });
});

// move is an epic history entry of case action whose kind is move, from the session it left to the one it went to.
function move(init: { by: string; session?: string; from: string; to: string }): EpicHistoryEntry {
  return entry({
    time: 400n,
    cursor: "m1",
    item: { case: "action", value: { seq: 1n, kind: "move", time: 400n, session: "", ...init } },
  });
}

// titlesOf names the sessions a map gives a title to; a session it does not name has no title.
function titlesOf(titles: Record<string, string>): (session: string) => string | undefined {
  return (session) => titles[session];
}

test("MoveLines: a move by the owner names both sessions by their titles", () => {
  const titleOf = titlesOf({ "sess-lamp": "Retry backoff", "sess-rota": "Planned the rota export" });
  const entries = [move({ by: "owner", from: "sess-rota", to: "sess-lamp" })];
  return withRender(<JustHappened entries={entries} steps={[]} titleOf={titleOf} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "moved to “Retry backoff” from “Planned the rota export”, by you",
    ]);
  });
});

test("MoveLines: a session titleOf does not name is drawn as its id, and so is every session with no titleOf", async () => {
  const entries = [move({ by: "owner", from: "sess-rota", to: "sess-lamp" })];
  await withRender(
    <JustHappened entries={entries} steps={[]} titleOf={titlesOf({ "sess-rota": "Planned the rota export" })} />,
    (container) => {
      assert.deepEqual(textsOf(container, "section.happen.just .what"), [
        "moved to session sess-lamp from “Planned the rota export”, by you",
      ]);
    },
  );
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "moved to session sess-lamp from session sess-rota, by you",
    ]);
  });
});

test("MoveLines: a move by a conversation is named by its title, or by its id when titleOf does not name it", async () => {
  const entries = [move({ by: "conversation", session: "sess-mover", from: "sess-rota", to: "sess-lamp" })];
  await withRender(
    <JustHappened
      entries={entries}
      steps={[]}
      titleOf={titlesOf({ "sess-mover": "Mover", "sess-rota": "Planned the rota export" })}
    />,
    (container) => {
      assert.deepEqual(textsOf(container, "section.happen.just .what"), [
        "moved to session sess-lamp from “Planned the rota export”, by “Mover”",
      ]);
    },
  );
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "moved to session sess-lamp from session sess-rota, by session sess-mover",
    ]);
  });
});

test("MoveLines: a move by orreryd is named orreryd", () => {
  const entries = [move({ by: "orreryd", from: "sess-rota", to: "sess-lamp" })];
  return withRender(<JustHappened entries={entries} steps={[]} titleOf={titlesOf({})} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [
      "moved to session sess-lamp from session sess-rota, by orreryd",
    ]);
  });
});

test("MoveLines: a move of an epic no conversation steered names no session it came from", () => {
  const entries = [move({ by: "owner", from: "", to: "sess-lamp" })];
  return withRender(<JustHappened entries={entries} steps={[]} titleOf={titlesOf({ "sess-lamp": "Retry backoff" })} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), ["moved to “Retry backoff”, by you"]);
  });
});

test("MoveLines: an action of any other kind draws no text, as before", () => {
  const entries = [
    entry({
      time: 400n,
      cursor: "a1",
      item: { case: "action", value: { seq: 2n, kind: "pause", time: 400n, by: "owner" } },
    }),
  ];
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), [""]);
  });
});
