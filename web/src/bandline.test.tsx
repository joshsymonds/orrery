import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { BandLine } from "./bandline";
import { assertNone, installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token OpenOnRight: the line renders on its own, outside the band and the page.

// withLine mounts the element in a container on the document and removes it again however the check ends.
async function withLine(element: Parameters<typeof render>[0], check: (container: HTMLElement) => void): Promise<void> {
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

test("OpenOnRight: closed, the line is a button that reads its act and calls onOpen once when pressed", () => {
  let opens = 0;
  return withLine(
    <BandLine name="tide-alerts" asks="waits on which alert rule wins" act="Answer" open={false} onOpen={() => (opens += 1)} />,
    (container) => {
      const button = container.querySelector<HTMLButtonElement>("button.subj");
      assert.ok(button !== null);
      assert.equal(button.querySelector(".what b")?.textContent, "tide-alerts");
      assert.equal(button.querySelector(".st")?.textContent, "waits on which alert rule wins");
      assert.equal(button.querySelector(".end")?.textContent, "Answer ›");
      assert.equal(opens, 0);
      button.click();
      assert.equal(opens, 1);
    },
  );
});

test("OpenOnRight: open, the line says open on the right, with no button, no act and nothing to press", () => {
  let opens = 0;
  return withLine(
    <BandLine name="tide-alerts" asks="waits on which alert rule wins" act="Answer" open onOpen={() => (opens += 1)} />,
    (container) => {
      const line = container.querySelector<HTMLDivElement>("div.subj.attention.here");
      assert.ok(line !== null);
      assertNone(container.querySelector("button"));
      assert.equal(line.querySelector(".what b")?.textContent, "tide-alerts");
      assert.equal(line.querySelector(".end")?.textContent, "open on the right ›");
      assert.equal(line.textContent?.includes("Answer"), false);
      line.click();
      assert.equal(opens, 0);
    },
  );
});
