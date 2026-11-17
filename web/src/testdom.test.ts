import assert from "node:assert/strict";
import { test } from "node:test";

import { assertNone, assertSame, describeNode, installTestDom, removeTestDom } from "./testdom";

// withDom runs body with a fresh test DOM installed, and removes it after.
async function withDom(body: (document: Document) => void): Promise<void> {
  const window = installTestDom();
  try {
    body(window.document as unknown as Document);
  } finally {
    await removeTestDom(window);
  }
}

// failureOf returns the message of the AssertionError run throws, and fails when it throws none.
function failureOf(run: () => void): string {
  try {
    run();
  } catch (error) {
    assert.ok(error instanceof assert.AssertionError, "the failure is an AssertionError");
    return error.message;
  }
  assert.fail("expected an assertion failure");
}

test("describeNode names an element by the first 200 characters of its outerHTML", () =>
  withDom((document) => {
    const div = document.createElement("div");
    div.className = "calls";
    div.textContent = "x".repeat(500);
    const shown = describeNode(div);
    assert.equal(shown.length, 200);
    assert.equal(shown.startsWith('<div class="calls">xxx'), true);
    assert.equal(describeNode(null), "null");
    assert.equal(describeNode(undefined), "undefined");
  }));

test("assertNone passes on null and fails on an element, naming it without formatting the node", () =>
  withDom((document) => {
    const container = document.createElement("section");
    container.innerHTML = '<button class="fold calls running">3 calls</button>';
    document.body.appendChild(container);
    assertNone(container.querySelector(".missing"));
    const message = failureOf(() => assertNone(container.querySelector(".calls"), "no call line is drawn"));
    assert.equal(message, 'no call line is drawn: found <button class="fold calls running">3 calls</button>');
  }));

test("assertNone fails on undefined, as assert.equal(found, null) does", () => {
  const message = failureOf(() => assertNone(undefined));
  assert.equal(message, "expected no element: found undefined");
});

test("assertSame passes on the same element and fails on another, naming both", () =>
  withDom((document) => {
    const first = document.createElement("li");
    first.textContent = "one";
    const second = document.createElement("li");
    second.textContent = "two";
    document.body.append(first, second);
    assertSame(first, first);
    assertSame(null, null);
    const message = failureOf(() => assertSame(second, first, "the head opens the queue"));
    assert.equal(message, "the head opens the queue: got <li>two</li>, want <li>one</li>");
    assert.equal(failureOf(() => assertSame(null, first)), "expected the same element: got null, want <li>one</li>");
  }));
