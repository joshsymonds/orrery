import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { AskFirst } from "./blocker";
import { installTestDom, removeTestDom } from "./testdom";

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

function findButton(root: ParentNode, selector: string, text: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>(selector)].find((button) => button.textContent === text);
}

// press clicks the button that matches selector and reads text, and reports whether one was drawn.
async function press(root: ParentNode, selector: string, text: string): Promise<boolean> {
  const button = findButton(root, selector, text);
  if (button === undefined) {
    return false;
  }
  await act(() => {
    button.click();
  });
  return true;
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

function askFirst(onConfirm: () => void): JSX.Element {
  return <AskFirst label="Remove" question="Remove it?" confirm="Yes, remove" keep="Keep" onConfirm={onConfirm} />;
}

test("AskFirst draws its label, and its first click asks and calls nothing", () => {
  let confirms = 0;
  return withTree(
    askFirst(() => {
      confirms++;
    }),
    async (container) => {
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.equal(await press(container, "button.act", "Remove"), true);
      assert.equal(confirms, 0);
      assert.equal(textOf(container, "span.sure"), "Remove it?");
      assert.deepEqual(textsOf(container, "button.act.primary"), ["Yes, remove"]);
      assert.deepEqual(textsOf(container, "button.act"), ["Yes, remove", "Keep"]);
      assert.equal(await press(container, "button.act.primary", "Yes, remove"), true);
      assert.equal(confirms, 1);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
    },
  );
});

test("AskFirst's keep returns to its label and calls nothing", () => {
  let confirms = 0;
  return withTree(
    askFirst(() => {
      confirms++;
    }),
    async (container) => {
      assert.equal(await press(container, "button.act", "Remove"), true);
      assert.equal(await press(container, "button.act", "Keep"), true);
      assert.equal(confirms, 0);
      assert.equal(container.querySelector(".sure") === null, true);
      assert.deepEqual(textsOf(container, "button.act"), ["Remove"]);
    },
  );
});
