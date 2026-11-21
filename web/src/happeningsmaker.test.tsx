import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";

import { type EpicHistoryEntry, EpicHistoryEntrySchema } from "./gen/orrery/v1/epics_pb";
import { JustHappened } from "./happenings";
import { installTestDom, removeTestDom } from "./testdom";

function entry(init: MessageInitShape<typeof EpicHistoryEntrySchema>): EpicHistoryEntry {
  return create(EpicHistoryEntrySchema, init);
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

// decision is a history entry of case decision, recorded by the maker its by and session name.
function decision(init: { by: string; session?: string }): EpicHistoryEntry {
  return entry({
    time: 400n,
    cursor: "d1",
    item: { case: "decision", value: { id: "d1", seq: 1n, text: "use the table", ...init } },
  });
}

// titlesOf names the sessions a map gives a title to; a session it does not name has no title.
function titlesOf(titles: Record<string, string>): (session: string) => string | undefined {
  return (session) => titles[session];
}

test("a decision's maker: the owner reads as you", () => {
  const entries = [decision({ by: "owner" })];
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), ["decision by you · use the table"]);
  });
});

test("a decision's maker: a conversation reads as its title, or as its session id when titleOf does not name it", async () => {
  const entries = [decision({ by: "conversation", session: "sess-maker" })];
  await withRender(
    <JustHappened entries={entries} steps={[]} titleOf={titlesOf({ "sess-maker": "Maker" })} />,
    (container) => {
      assert.deepEqual(textsOf(container, "section.happen.just .what"), ["decision by “Maker” · use the table"]);
    },
  );
  return withRender(
    <JustHappened entries={entries} steps={[]} titleOf={titlesOf({ "sess-other": "Other" })} />,
    (container) => {
      assert.deepEqual(textsOf(container, "section.happen.just .what"), [
        "decision by session sess-maker · use the table",
      ]);
    },
  );
});

test("a decision's maker: orreryd reads as orreryd", () => {
  const entries = [decision({ by: "orreryd" })];
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), ["decision by orreryd · use the table"]);
  });
});

test("a decision's maker: a decision with no maker reads as unknown", () => {
  const entries = [decision({ by: "" })];
  return withRender(<JustHappened entries={entries} steps={[]} />, (container) => {
    assert.deepEqual(textsOf(container, "section.happen.just .what"), ["decision by unknown · use the table"]);
  });
});
