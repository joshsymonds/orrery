import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { EpicRow } from "./epicrow";
import { blockOf } from "./epics";
import {
  type Epic,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
  EpicStepSchema,
} from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";
import type { EpicNode, Hung } from "./tree";

// now is the clock the rows read their times against, in unix seconds.
const now = 100000;

function epicOf(init: MessageInitShape<typeof EpicSchema>): Epic {
  return create(EpicSchema, {
    id: "widgets-5d0c1a7/first",
    repository: "widgets-5d0c1a7",
    slug: "first",
    outcome: "",
    live: true,
    source: "record",
    ...init,
  });
}

// nodeOf hangs the listed epic under a conversation as the tree does, with its block read from the detail it is given.
function nodeOf(listed: Epic, detail: EpicsGetResponse | undefined): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, detail), listed, contract: null };
}

// drawn draws the row for the Hung in a fresh DOM, runs check on the container, and removes the DOM again.
async function drawn(hung: Hung, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const row: JSX.Element = <EpicRow hung={hung} now={now} current={false} onSelect={() => {}} />;
  try {
    render(row, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

test("RowWords: the first line reads the slug, with proven of total at its end, from the requirement states", () => {
  const epic = epicOf({});
  const detail = create(EpicsGetResponseSchema, {
    epic,
    requirements: [
      { id: "R1", state: "proven" },
      { id: "R2", state: "failed" },
      { id: "R3", state: "proven" },
      { id: "R4", state: "not_reached" },
    ],
  });
  return drawn(nodeOf(epic, detail), (container) => {
    const row = container.querySelector(".row.epic");
    assert.ok(row, "the row draws");
    assert.equal(textOf(container, ".title"), "first");
    assert.equal(textOf(container, ".age"), "2 of 4 proven");
    assert.deepEqual(
      [...row.children].filter((child) => child.tagName === "SPAN").map((child) => child.className),
      ["title", "age", "l2"],
      "the count comes straight after the slug, and before the second line",
    );
  });
});

test("RowWords: the second line reads the stage word and its clock, and no count", () => {
  const epic = epicOf({ phaseAt: 97060n, outcome: "accepted" });
  const detail = create(EpicsGetResponseSchema, { epic, requirements: [{ id: "R1", state: "proven" }] });
  return drawn(nodeOf(epic, detail), (container) => {
    assert.equal(textOf(container, ".l2"), "planning · 49 min");
  });
});

test("RowWords: the blocker takes the third line with its question mark, and the newest sentence does not", () => {
  const epic = epicOf({ phaseAt: 97060n });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    questions: [{ id: "Q1", text: "Which way?", asked: 99880n }],
    newest: "Task t0 is being built.",
  });
  return drawn(nodeOf(epic, detail), (container) => {
    assert.equal(textOf(container, ".l3"), "waits on your answer · 2 min");
    assert.equal(container.querySelectorAll(".l3 svg.icon").length, 1);
    assert.equal(container.textContent?.includes("Task t0 is being built."), false);
  });
});

test("RowWords: the newest sentence takes the third line when nothing blocks the epic, with no question mark", () => {
  const epic = epicOf({ phaseAt: 97060n });
  const detail = create(EpicsGetResponseSchema, { epic, newest: "Task t0 is being built." });
  return drawn(nodeOf(epic, detail), (container) => {
    assert.equal(textOf(container, ".l3"), "Task t0 is being built.");
    assert.equal(container.querySelectorAll(".l3 svg.icon").length, 0);
  });
});

test("RowWords: no text says steps ended, and a full block's fold line counts only gaps and unstarted tasks", () => {
  const steps = ["s1", "s2", "s3", "s4", "s5"].map((id) => create(EpicStepSchema, { id, agent: "builder" }));
  const epic = epicOf({ runningSteps: steps, endedSteps: 3 });
  const detail = create(EpicsGetResponseSchema, {
    epic,
    tasks: [{ effort: 1, slug: "t0", subject: "t0", status: "pending" }],
  });
  return drawn(nodeOf(epic, detail), (container) => {
    assert.equal(textOf(container, ".fold-line"), "1 more · 1 not started");
    assert.equal(container.textContent?.includes("steps ended"), false);
  });
});
