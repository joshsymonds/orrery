import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { Reply } from "./reply";
import { installTestDom, removeTestDom } from "./testdom";

// withReply renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withReply(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

test("Shapes: a reply draws its Markdown inside one reply element, with nothing around it", () =>
  withReply(<Reply text={"- one\n- two"} />, (container) => {
    const replies = container.querySelectorAll<HTMLElement>('[data-shape="reply"]');
    assert.equal(replies.length, 1, "the reply is one element");
    assert.equal(replies[0].className, "reply", "the element is the reply class and no other");
    assert.equal(replies[0].querySelectorAll(":scope > ul > li").length, 2, "its list is drawn inside it");
  }));

test("Shapes: a live reply draws its caret after its Markdown, inside the reply element", () =>
  withReply(<Reply text={"streamed text"} live />, (container) => {
    const reply = container.querySelector<HTMLElement>('[data-shape="reply"]');
    assert.ok(reply !== null, "the reply is drawn");
    assert.equal(reply.lastElementChild?.classList.contains("caret"), true, "the caret is the reply's last element");
    assert.equal(reply.querySelectorAll(".caret").length, 1, "one caret is drawn");
  }));

test("Shapes: a reply that is not live draws no caret", () =>
  withReply(<Reply text="finished text" />, (container) => {
    assert.equal(container.querySelector(".caret"), null, "a finished reply has no caret");
  }));

test("Shapes: a reply marks each occurrence of its query, and the one at current as current", () => {
  const text = "one build, two build";
  // current is the offset in the text where the current match starts, so the second occurrence starts at its last one.
  return withReply(<Reply text={text} query="build" current={text.lastIndexOf("build")} />, (container) => {
    const marks = [...container.querySelectorAll("mark")];
    assert.equal(marks.length, 2, "each occurrence is a mark");
    assert.equal(marks[1].classList.contains("current"), true, "the second occurrence is the current match");
    assert.equal(marks[0].classList.contains("current"), false, "the first occurrence is not current");
  });
});
