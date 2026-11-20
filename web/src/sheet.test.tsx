import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { Sheet } from "./sheet";
import { assertSame, installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token Sheet: the sheet renders on its own, outside App, and closes on its own.

// withSheet mounts the element in a container on the document, so an event dispatched inside it bubbles to the document,
// and removes it again however the check ends.
async function withSheet(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    act(() => {
      render(element, container);
    });
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

test("Sheet: draws its children in a dialog over a scrim, headed by its title and sub", () => {
  return withSheet(
    <Sheet title="Pick a session" sub="3 sessions" onClose={() => undefined}>
      <button class="pick-row">first session</button>
    </Sheet>,
    (container) => {
      const scrim = container.querySelector(".sheet-scrim");
      const dialog = container.querySelector("section.sheet");
      assert.ok(scrim !== null && dialog !== null);
      assertSame(dialog.parentElement, scrim);
      assert.equal(dialog.getAttribute("role"), "dialog");
      assert.equal(dialog.getAttribute("aria-label"), "Pick a session");
      assert.equal(dialog.querySelector(".sheet-head b")?.textContent, "Pick a session");
      assert.equal(dialog.querySelector(".sheet-head")?.textContent, "Pick a session3 sessions");
      assert.equal(dialog.querySelector("button.pick-row")?.textContent, "first session");
    },
  );
});

test("Sheet: takes focus when it mounts, and Escape inside it calls onClose once and never reaches the document", () => {
  let closes = 0;
  let documentKeys = 0;
  const onDocumentKey = () => {
    documentKeys += 1;
  };
  return withSheet(
    <Sheet title="Pick a session" onClose={() => (closes += 1)}>
      <button class="pick-row">first session</button>
    </Sheet>,
    (container) => {
      document.addEventListener("keydown", onDocumentKey);
      try {
        const dialog = container.querySelector("section.sheet");
        assert.ok(dialog !== null);
        assertSame(document.activeElement, dialog);
        dialog.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        assert.equal(closes, 1);
        assert.equal(documentKeys, 0);
      } finally {
        document.removeEventListener("keydown", onDocumentKey);
      }
    },
  );
});

test("Sheet: a press on the scrim calls onClose, and a press inside the sheet does not", () => {
  let closes = 0;
  return withSheet(
    <Sheet title="Pick a session" onClose={() => (closes += 1)}>
      <button class="pick-row">first session</button>
    </Sheet>,
    (container) => {
      const scrim = container.querySelector(".sheet-scrim");
      const row = container.querySelector("button.pick-row");
      assert.ok(scrim !== null && row !== null);
      row.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      assert.equal(closes, 0);
      scrim.dispatchEvent(new Event("pointerdown", { bubbles: true }));
      assert.equal(closes, 1);
    },
  );
});
