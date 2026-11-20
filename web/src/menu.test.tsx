import assert from "node:assert/strict";
import { test } from "node:test";

import type { JSX } from "preact";
import { render } from "preact";

import { Icon, type IconName } from "./glyphs";
import { type MenuEntry, Menu } from "./menu";
import { installTestDom, removeTestDom } from "./testdom";

// withMenu draws element into a fresh DOM and hands check the container it draws into.
async function withMenu(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

// rowItems are a row's menu as round 6 draws it, each press counted into counts.
function rowItems(counts: number[]): MenuEntry[] {
  return [
    { label: "Open in a new window", icon: "open", onSelect: () => (counts[0] += 1) },
    { label: "Mark seen", icon: "check", kbd: "x", onSelect: () => (counts[1] += 1) },
    "sep",
    { label: "Archive", icon: "archive", kbd: "A", onSelect: () => (counts[2] += 1) },
  ];
}

test("InboxTree Menu: items draw in order, each with its label and key cap, and a separator draws as one", () =>
  withMenu(<Menu items={rowItems([0, 0, 0])} />, (container) => {
    const menu = container.querySelector<HTMLElement>("[role='menu']");
    assert.ok(menu !== null, "the menu draws no element with role menu");
    const children = Array.from(menu.children);
    assert.deepEqual(
      children.map((child) => `${child.tagName}.${child.className}`),
      ["BUTTON.menu-item", "BUTTON.menu-item", "DIV.menu-sep", "BUTTON.menu-item"],
      "the items and the separator do not draw in order",
    );
    const buttons = children.filter((child) => child.tagName === "BUTTON");
    assert.deepEqual(
      buttons.map((button) => button.querySelector("span")?.textContent),
      ["Open in a new window", "Mark seen", "Archive"],
      "an item does not draw its label",
    );
    assert.equal(buttons[0].querySelector("kbd"), null, "an item with no key cap draws one");
    assert.equal(buttons[1].querySelector("kbd")?.textContent, "x", "Mark seen does not draw its key cap x");
    assert.equal(buttons[2].querySelector("kbd")?.textContent, "A", "Archive does not draw its key cap A");
    assert.equal(menu.querySelectorAll(".menu-sep").length, 1, "the separator does not draw as exactly one");
  }));

test("InboxTree Menu: each item draws its own icon before its label", () =>
  withMenu(<Menu items={rowItems([0, 0, 0])} />, (container) => {
    const buttons = Array.from(container.querySelectorAll<HTMLElement>("button.menu-item"));
    const expected = ["open", "check", "archive"].map((name) => {
      const drawing = document.createElement("div");
      render(<Icon name={name as IconName} />, drawing);
      return drawing.innerHTML;
    });
    assert.deepEqual(
      buttons.map((button) => button.querySelector("svg")?.outerHTML),
      expected,
      "an item does not draw its own icon first",
    );
  }));

test("InboxTree Menu: a press on an item calls that item's onSelect once and no other's", async () => {
  const counts = [0, 0, 0];
  await withMenu(<Menu items={rowItems(counts)} />, (container) => {
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>("button.menu-item"));
    buttons[1].click();
    assert.deepEqual(counts, [0, 1, 0], "a press on Mark seen did not call only its own onSelect once");
    buttons[2].click();
    assert.deepEqual(counts, [0, 1, 1], "a press on Archive did not call only its own onSelect once");
    buttons[0].click();
    assert.deepEqual(counts, [1, 1, 1], "a press on Open in a new window did not call only its own onSelect once");
  });
});
