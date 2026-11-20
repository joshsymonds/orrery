import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { KeyList } from "./helppanel";
import { keyGroups } from "./keys";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token KeyList: the key list renders on its own, outside App.

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

test("KeyList: the panel is titled Keys, its hint says ? or esc closes, and its Close button calls onClose", () => {
  let closes = 0;
  return withPanel(
    <KeyList
      onClose={() => {
        closes += 1;
      }}
    />,
    (container) => {
      const panel = container.querySelector('section.panel[role="dialog"][aria-label="Keys"]');
      assert.ok(panel, "the panel is a dialog titled Keys");
      assert.equal(panel.querySelector("h2.t-title")?.textContent, "Keys");
      assert.equal(panel.querySelector(".head .t-small")?.textContent, "? or esc closes");
      const close = panel.querySelector<HTMLButtonElement>('.head button[aria-label="Close"]');
      assert.ok(close, "the panel has a Close button");
      close.click();
      assert.equal(closes, 1, "Close calls onClose once");
    },
  );
});

test("KeyList: the five groups are drawn in the table's order, each under its title, with each line's caps and text", () =>
  withPanel(<KeyList onClose={() => undefined} />, (container) => {
    const drawn = [...container.querySelectorAll(".keygroups > .keygroup")].map((group) => ({
      title: group.querySelector(".t-label")?.textContent,
      lines: [...group.querySelectorAll(".keyline")].map((line) => ({
        caps: [...(line.querySelector(".ks")?.children ?? [])].map((cap) => cap.textContent),
        text: line.children[1]?.textContent,
      })),
    }));
    assert.deepEqual(
      drawn,
      keyGroups.map((group) => ({
        title: group.title,
        lines: group.lines.map((line) => ({ caps: line.caps, text: line.text })),
      })),
    );
  }));

test("KeyList: the cap … is drawn dimmed, not as a key", () =>
  withPanel(<KeyList onClose={() => undefined} />, (container) => {
    const dimmed = [...container.querySelectorAll(".keyline .ks > span.ink-4")].map((cap) => cap.textContent);
    assert.deepEqual(dimmed, ["…", "…"], "one dimmed … per 1…9 line");
    const keyCaps = [...container.querySelectorAll(".keyline .ks > kbd")].map((cap) => cap.textContent);
    assert.equal(keyCaps.includes("…"), false, "no … is drawn as a key");
  }));
