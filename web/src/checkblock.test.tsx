import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { CheckBlock } from "./reader";
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

test("CheckBlock: the cap names the round and says the checks come from the engine", () =>
  withBlock({ text: "gate failed", round: 2, checks: [checkOf(2, "gate", false)] }, (container) => {
    const cap = container.querySelector(".cap");
    assert.equal(cap?.textContent?.includes("Checks · round 2"), true, "the cap names the round");
    assert.equal(cap?.querySelector("span")?.textContent, "from the engine");
  }));

test("CheckBlock: one chip per check of its round only, marked ✓ or ✗", () =>
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
      assert.deepEqual(chipTexts(container), ["schema ✓", "gate ✗"], "round 2's check is not drawn");
      assert.equal(container.querySelectorAll(".chip.bad").length, 1, "only the failed check is bad");
    },
  ));

test("CheckBlock: the edge is bad when one of its round's checks failed, and ok when none did", async () => {
  await withBlock(
    {
      text: "gate failed",
      round: 1,
      checks: [checkOf(1, "gate", false), checkOf(2, "lint", true)],
    },
    (container) => {
      const card = container.querySelector("div.card");
      assert.equal(card?.classList.contains("bad"), true, "a failed check of the round makes the edge bad");
      assert.equal(card?.classList.contains("ok"), false);
    },
  );
  await withBlock(
    {
      text: "all checks passed",
      round: 1,
      checks: [checkOf(1, "gate", true), checkOf(2, "lint", false)],
    },
    (container) => {
      const card = container.querySelector("div.card");
      assert.equal(card?.classList.contains("ok"), true, "a failure of another round leaves this edge ok");
      assert.equal(card?.classList.contains("bad"), false);
    },
  );
});

test("CheckBlock: a one-line note draws as the note with no fold", () =>
  withBlock({ text: "all checks passed", round: 1, checks: [checkOf(1, "gate", true)] }, (container) => {
    assert.equal(container.querySelector("p.note")?.textContent, "all checks passed");
    assert.equal(container.querySelector("button.note"), null, "a one-line note has no fold button");
    assert.equal(container.querySelector(".rest"), null);
  }));

test("CheckBlock: a multi-line note folds its rest under the first line until clicked", () =>
  withBlock(
    { text: "gate failed: exit 1\nthe gate's output\nline three", round: 1, checks: [checkOf(1, "gate", false)] },
    async (container) => {
      assert.equal(container.querySelector("p.note"), null, "a multi-line note has no p.note");
      const edge = container.querySelector<HTMLElement>("button.note");
      assert.equal(edge?.textContent, "gate failed: exit 1", "the first line is the fold's button");
      assert.equal(container.querySelector(".rest"), null, "the rest is folded until clicked");

      assert.ok(edge !== null && edge !== undefined);
      await clickControl(edge);
      const rest = container.querySelector(".rest");
      assert.equal(rest?.textContent?.includes("line three"), true, "the rest is shown after a click");
      assert.equal(rest?.textContent?.includes("gate failed"), false, "the rest leaves out the first line");
    },
  ));
