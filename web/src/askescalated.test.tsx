import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import { Ask, type AskProps } from "./ask";
import { epicAsk } from "./askmodel";
import { EpicQuestionSchema, EpicSchema, type Epic, type EpicQuestion } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

// now is the instant every Ask in these tests is drawn at.
const now = 1_794_700_000;

const epic: Epic = create(EpicSchema, {
  id: "harbour-rota/rota-export",
  repository: "harbour-rota",
  path: "/home/owner/Code/harbour",
  slug: "rota-export",
  phaseAt: 1794700000n,
});

// question is an epic's question, asked when the epic was, with two choices and no escalation.
function question(init: MessageInitShape<typeof EpicQuestionSchema> = {}): EpicQuestion {
  return create(EpicQuestionSchema, {
    id: "Q5",
    text: "Which index should the backfill add?",
    asked: 1794700000n,
    choices: [
      { id: "partial", does: "Add a partial index on open shifts only." },
      { id: "composite", does: "Add a composite index on keeper and shift." },
    ],
    ...init,
  });
}

const reason = "Both indexes cost the same to keep, and only the owner can weigh the write load.";

function baseProps(init: Partial<AskProps>): AskProps {
  return {
    model: epicAsk(epic, question()),
    now,
    phase: "open",
    picked: null,
    note: "",
    keyCaps: true,
    narrow: false,
    numberKeys: false,
    ...init,
  };
}

// withTree renders a tree into a fresh container, runs check on it, and removes it.
async function withTree(tree: JSX.Element, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(tree, container);
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

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

test("an escalated question: epicAsk names its maker and reason", () => {
  const model = epicAsk(
    epic,
    question({
      escalated: 1794700100n,
      escalationReason: reason,
      escalationBy: "conversation",
      escalationSession: "s-example-1",
    }),
  );

  assert.equal(model.escalation, `escalated by session s-example-1: ${reason}`);
});

test("an escalated question: an owner's escalation names its maker you", () => {
  const model = epicAsk(
    epic,
    question({
      escalated: 1794700100n,
      escalationReason: reason,
      escalationBy: "owner",
    }),
  );

  assert.equal(model.escalation, `escalated by you: ${reason}`);
});

test("an escalated question: a question not escalated carries no escalation", () => {
  const model = epicAsk(epic, question());

  assert.equal("escalation" in model, false, "a question not escalated carries an escalation line");
});

test("an escalated question: Ask draws the escalated chip and its maker and reason line", async () => {
  const escalated = epicAsk(
    epic,
    question({
      escalated: 1794700100n,
      escalationReason: reason,
      escalationBy: "conversation",
      escalationSession: "s-example-1",
    }),
  );

  await withTree(<Ask {...baseProps({ model: escalated })} />, async (container) => {
    assert.equal(textOf(container, ".head .end .chip.attention"), "escalated", "the escalated chip is not drawn");
    assert.equal(
      textOf(container, ".ask .q + p.inote"),
      `escalated by session s-example-1: ${reason}`,
      "the maker and reason line is not drawn below the question",
    );
  });

  await withTree(<Ask {...baseProps({ model: epicAsk(epic, question()) })} />, async (container) => {
    assert.equal(textOf(container, ".head .end .chip.attention"), null, "an unescalated Ask draws the escalated chip");
    assert.equal(textOf(container, ".ask .q + p.inote"), null, "an unescalated Ask draws a maker and reason line");
  });
});
