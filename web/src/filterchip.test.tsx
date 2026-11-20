import assert from "node:assert/strict";
import { test } from "node:test";

import type { JSX } from "preact";
import { render } from "preact";

import { FilterChip } from "./filterchip";
import { installTestDom, removeTestDom } from "./testdom";

// withChip draws element into a fresh DOM and hands check the chip's button, which the element must draw.
async function withChip(element: JSX.Element, check: (chip: HTMLButtonElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    const chip = container.querySelector<HTMLButtonElement>("button");
    assert.ok(chip !== null, "the chip draws no button");
    check(chip);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("InboxTree: the chip draws its count in bold, then its label", () =>
  withChip(<FilterChip count={6} label="wait on you" tone="attention" on={false} onPress={() => {}} />, (chip) => {
    assert.equal(chip.querySelector("b")?.textContent, "6");
    assert.equal(chip.textContent, "6wait on you");
  }));

test("InboxTree: the chip carries its tone as a class, beside fchip, and no other tone", async () => {
  await withChip(<FilterChip count={6} label="wait on you" tone="attention" on={false} onPress={() => {}} />, (chip) => {
    assert.ok(chip.classList.contains("fchip"), "the chip lacks the fchip class");
    assert.ok(chip.classList.contains("attention"), "the attention chip lacks the attention tone");
    assert.ok(!chip.classList.contains("new"), "the attention chip carries the new tone");
  });
  await withChip(<FilterChip count={2} label="new" tone="new" on={false} onPress={() => {}} />, (chip) => {
    assert.ok(chip.classList.contains("new"), "the new chip lacks the new tone");
    assert.ok(!chip.classList.contains("attention"), "the new chip carries the attention tone");
  });
});

test("InboxTree: aria-pressed is true only when the chip is on, and the on class follows it", async () => {
  await withChip(<FilterChip count={6} label="wait on you" tone="attention" on={true} onPress={() => {}} />, (chip) => {
    assert.equal(chip.getAttribute("aria-pressed"), "true");
    assert.ok(chip.classList.contains("on"), "an on chip lacks the on class");
  });
  await withChip(<FilterChip count={6} label="wait on you" tone="attention" on={false} onPress={() => {}} />, (chip) => {
    assert.equal(chip.getAttribute("aria-pressed"), "false");
    assert.ok(!chip.classList.contains("on"), "an off chip carries the on class");
  });
});

test("InboxTree: each click on the chip calls onPress once", async () => {
  let presses = 0;
  await withChip(
    <FilterChip count={6} label="wait on you" tone="attention" on={false} onPress={() => (presses += 1)} />,
    (chip) => {
      assert.equal(chip.type, "button");
      chip.click();
      assert.equal(presses, 1, "one click calls onPress once");
      chip.click();
      assert.equal(presses, 2, "a second click calls onPress once more");
    },
  );
});
