import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import { act } from "preact/test-utils";

import { Toast } from "./toast";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the words InboxTree Toast: the toast renders on its own, outside App.

const text = "Archived “Tide table cache warm-up”.";
const actionLabel = "Undo";

async function withToast(
  onAction: () => void,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Toast text={text} actions={[{ label: actionLabel, onAction }]} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("InboxTree Toast: the text is drawn in a status region, and the action is one button labelled with it", () =>
  withToast(
    () => {},
    (container) => {
      const status = container.querySelector('[role="status"]');
      assert.ok(status !== null, "no status region is drawn");
      assert.equal(status.querySelector(".grow")?.textContent, text);
      const buttons = container.querySelectorAll("button");
      assert.equal(buttons.length, 1, `expected one button, got ${buttons.length}`);
      assert.equal(buttons[0].textContent, actionLabel);
      assert.ok(status.contains(buttons[0]), "the action button is not in the status region");
    },
  ));

test("InboxTree Toast: each action is a button after the text, in its order, and presses only its own onAction", async () => {
  const window = installTestDom();
  const container = document.createElement("div");
  const calls: string[] = [];
  try {
    render(
      <Toast
        text={text}
        actions={[
          { label: "Show", onAction: () => calls.push("Show") },
          { label: "Undo", onAction: () => calls.push("Undo") },
        ]}
      />,
      container,
    );
    const labels = [...container.querySelectorAll("button")].map((button) => button.textContent);
    assert.deepEqual(labels, ["Show", "Undo"], "the buttons are not the actions, in order");
    await act(() => {
      container.querySelectorAll("button")[1].dispatchEvent(new Event("click"));
    });
    assert.deepEqual(calls, ["Undo"], "pressing Undo did not call only its own onAction");
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
});

test("InboxTree Toast: pressing the action calls onAction once, and drawing the toast calls it not at all", async () => {
  let calls = 0;
  await withToast(
    () => {
      calls += 1;
    },
    async (container) => {
      assert.equal(calls, 0, "onAction ran while the toast was drawn");
      const button = container.querySelector("button");
      assert.ok(button !== null, "the action button is not drawn");
      await act(() => {
        button.dispatchEvent(new Event("click"));
      });
      assert.equal(calls, 1, `expected one call, got ${calls}`);
    },
  );
});
