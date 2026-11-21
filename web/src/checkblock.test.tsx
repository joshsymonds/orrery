import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { CheckBlock } from "./blocks";
import { type EpicStepCheck, EpicStepCheckSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

// withBlock renders the block into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withBlock(
  props: { text: string; round: number; checks: EpicStepCheck[] },
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<CheckBlock text={props.text} round={props.round} checks={props.checks} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// checkOf is a check of a round, passed or failed.
function checkOf(round: number, name: string, ok: boolean): EpicStepCheck {
  return create(EpicStepCheckSchema, { round, name, ok });
}

// clickControl clicks a control inside act, so the render its click causes has run before the next check.
async function clickControl(control: HTMLElement): Promise<void> {
  await act(() => {
    control.click();
  });
}

function chipTexts(block: HTMLElement): string[] {
  return [...block.querySelectorAll(".chip")].map((chip) => chip.textContent ?? "");
}

// noteOf is the text of the note: the paragraph the block's body draws after its head.
function noteOf(root: HTMLElement): string | undefined {
  return [...root.children].find((child) => child.tagName === "P")?.textContent ?? undefined;
}

// blockOf is the block root the CheckBlock draws.
function blockOf(container: HTMLElement): HTMLElement {
  const root = container.querySelector<HTMLElement>('[data-shape="block"]');
  assert.ok(root, "the CheckBlock draws no block root");
  return root;
}

test("CheckBlock: the head names the round and says the checks come from the engine", () =>
  withBlock({ text: "gate failed", round: 2, checks: [checkOf(2, "gate", false)] }, (container) => {
    const head = blockOf(container).querySelector(".head");
    assert.equal(head?.querySelector(".label")?.textContent, "CHECKS");
    assert.equal(head?.querySelector(".words")?.textContent, "round 2 from the engine", "the head names the round");
  }));

test("CheckBlock: one chip per check of its round only, the failed one bad", () =>
  withBlock(
    {
      text: "gate failed",
      round: 1,
      checks: [
        checkOf(1, "schema", true),
        checkOf(1, "gate", false),
        checkOf(2, "lint", true),
      ],
    },
    (container) => {
      assert.deepEqual(chipTexts(container), ["schema", "gate"], "round 2's check is not drawn");
      assert.equal(container.querySelectorAll(".chip.bad").length, 1, "only the failed check is bad");
    },
  ));

test("CheckBlock: the tone is bad when one of its round's checks failed, and ok when none did", async () => {
  await withBlock(
    {
      text: "gate failed",
      round: 1,
      checks: [checkOf(1, "gate", false), checkOf(2, "lint", true)],
    },
    (container) => {
      assert.equal(blockOf(container).dataset.tone, "bad", "a failed check of the round makes the tone bad");
    },
  );
  await withBlock(
    {
      text: "all checks passed",
      round: 1,
      checks: [checkOf(1, "gate", true), checkOf(2, "lint", false)],
    },
    (container) => {
      assert.equal(blockOf(container).dataset.tone, "ok", "a failure of another round leaves this tone ok");
    },
  );
});

test("CheckBlock: a one-line note draws as the note with no fold", () =>
  withBlock({ text: "all checks passed", round: 1, checks: [checkOf(1, "gate", true)] }, (container) => {
    const root = blockOf(container);
    assert.equal(noteOf(root), "all checks passed");
    assert.equal(root.querySelector(".end button"), null, "a one-line note has no fold button");
    assert.equal(root.querySelector(".opened"), null);
  }));

test("CheckBlock: a multi-line note folds its rest under the first line until clicked", () =>
  withBlock(
    { text: "gate failed: exit 1\nthe gate's output\nline three", round: 1, checks: [checkOf(1, "gate", false)] },
    async (container) => {
      const root = blockOf(container);
      assert.equal(noteOf(root), "gate failed: exit 1", "the first line is the note");
      const edge = root.querySelector<HTMLElement>(".end button");
      assert.ok(edge, "a multi-line note has a fold button in the end");
      assert.equal(root.querySelector(".opened"), null, "the rest is folded until clicked");

      await clickControl(edge);
      const rest = root.querySelector(".opened");
      assert.equal(rest?.textContent?.includes("line three"), true, "the rest is shown after a click");
      assert.equal(rest?.textContent?.includes("gate failed"), false, "the rest leaves out the first line");
    },
  ));
