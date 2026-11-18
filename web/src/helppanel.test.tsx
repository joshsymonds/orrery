import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { HelpPanel } from "./helppanel";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token AppSplit: the help panel renders on its own, outside App.

async function withPanel(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// keysOf returns the key caps of the entry whose text is the one given, or null when the panel has no such entry.
function keysOf(container: HTMLElement, text: string): string[] | null {
  const entry = [...container.querySelectorAll(".key")].find((item) => item.querySelector("dd")?.textContent === text);
  return entry === undefined ? null : [...entry.querySelectorAll("kbd")].map((cap) => cap.textContent);
}

test("AppSplit: HelpPanel draws the keys section outside App, with each entry's keys and what they do", () =>
  withPanel(<HelpPanel />, (container) => {
    assert.equal(container.querySelector("section.keys")?.getAttribute("aria-label"), "keys");
    assert.deepEqual(keysOf(container, "move the cursor one row up or down"), ["j", "k", "↓", "↑"]);
    assert.deepEqual(keysOf(container, "mark the cursor's row seen; Enter also opens the row into its reply box"), [
      "x",
      "Enter",
    ]);
    assert.deepEqual(keysOf(container, "close this help"), ["Esc"]);
  }));
