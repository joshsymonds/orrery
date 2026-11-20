import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { at } from "./fixtures/clock";
import { Block, EventBlock, YouBlock } from "./blocks";
import { Icon } from "./glyphs";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

// withBlock renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withBlock(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// iconMarkup is the markup Icon draws for a name, so a test can compare the icon a block draws with it.
async function iconMarkup(name: "person" | "gear" | "clock"): Promise<string> {
  let markup = "";
  await withBlock(<Icon name={name} />, (container) => {
    markup = container.innerHTML;
  });
  return markup;
}

const sentAt = at("@14:02");

test("Shapes: an owner prompt draws a YouBlock: the you tone, the person icon, YOU, its time, and its text as Markdown", async () => {
  const personIcon = await iconMarkup("person");
  await withBlock(<YouBlock text="The **tide** table is late." time={sentAt} />, (container) => {
    const root = container.querySelector<HTMLElement>('[data-shape="block"]');
    assert.ok(root, "the YouBlock has no block root");
    assert.equal(root.dataset.tone, "you");
    assert.equal(root.classList.contains("you"), true);
    assert.equal(root.classList.contains("pending"), false);
    assert.equal(textOf(root, ".label"), "YOU");
    assert.equal(textOf(root, ".at"), clock(sentAt));
    assert.equal(root.querySelector(".head")?.innerHTML.includes(personIcon), true, "the head does not draw the person icon");
    assert.equal(root.querySelector("strong")?.textContent, "tide");
  });
});

test("Shapes: a YouBlock's find query marks each occurrence in its Markdown text, and the current one carries class current", async () => {
  await withBlock(
    // The second "tide" starts at offset 32 of the text as drawn, so current is that offset.
    <YouBlock text="The tide table is late, and the **tide** is out." time={sentAt} query="tide" current={32} />,
    (container) => {
      const marks = container.querySelectorAll("mark");
      assert.equal(marks.length, 2);
      assert.equal(marks[0].classList.contains("current"), false);
      assert.equal(marks[1].classList.contains("current"), true);
    },
  );
});

test("Shapes: a message the page sent draws a YouBlock while sending: pending, labelled SENDING, with no time", async () => {
  await withBlock(<YouBlock text="Keep the pin." sending />, (container) => {
    const root = container.querySelector<HTMLElement>('[data-shape="block"]');
    assert.ok(root, "the sending YouBlock has no block root");
    assert.equal(root.classList.contains("pending"), true);
    assert.equal(root.dataset.tone, "you");
    assert.equal(textOf(root, ".label"), "SENDING");
    assert.equal(root.querySelector(".at"), null);
  });
});

test("Shapes: an engine event draws an EventBlock: the engine tone, the gear icon, EVENT, from the engine, its time, and its text as Markdown", async () => {
  const gear = await iconMarkup("gear");
  await withBlock(<EventBlock text="Two tasks **start** now." time={at("@13:58")} />, (container) => {
    const root = container.querySelector<HTMLElement>('[data-shape="block"]');
    assert.ok(root, "the EventBlock has no block root");
    assert.equal(root.dataset.tone, "engine");
    assert.equal(root.classList.contains("engine"), true);
    assert.equal(textOf(root, ".label"), "EVENT");
    assert.equal(textOf(root, ".words"), "from the engine");
    assert.equal(root.querySelector(".head")?.innerHTML.includes(gear), true, "the head does not draw the gear icon");
    assert.equal(root.querySelector("strong")?.textContent, "start");
    assert.equal(textOf(root, ".at"), clock(at("@13:58")));
  });
});

test("Shapes: a cron prompt draws an EventBlock labelled CRON with the clock icon", async () => {
  const clockIcon = await iconMarkup("clock");
  await withBlock(<EventBlock text="Warm the cache." cron />, (container) => {
    const root = container.querySelector<HTMLElement>('[data-shape="block"]');
    assert.ok(root, "the cron EventBlock has no block root");
    assert.equal(root.dataset.tone, "engine");
    assert.equal(textOf(root, ".label"), "CRON");
    assert.equal(root.querySelector(".head")?.innerHTML.includes(clockIcon), true, "the head does not draw the clock icon");
  });
});

test("Shapes: a Block draws exactly one block root holding its head and its children as the body", async () => {
  const personIcon = await iconMarkup("person");
  await withBlock(
    <Block tone="you" kind="you" icon="person" label="YOU" who="owner" words="from your phone" time="14:02" end="done">
      <p class="child">body</p>
    </Block>,
    (container) => {
      const roots = container.querySelectorAll('[data-shape="block"]');
      assert.equal(roots.length, 1, "a Block draws more or fewer than one block root");
      const root = roots[0];
      assert.equal(container.querySelectorAll(".block").length, 1, "a Block draws more than one rail holder");
      assert.equal(textOf(root, ".head .who"), "owner");
      assert.equal(textOf(root, ".head .words"), "from your phone");
      assert.equal(textOf(root, ".head .at"), "14:02");
      assert.equal(textOf(root, ".head .end"), "done");
      assert.equal(root.querySelector(".head")?.innerHTML.includes(personIcon), true, "the head does not draw the icon");
      assert.equal(root.querySelector(".child")?.textContent, "body");
      assert.equal(root.querySelector(".head .child"), null, "the body is drawn outside the head");
    },
  );
});
