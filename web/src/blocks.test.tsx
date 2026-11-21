import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { at } from "./fixtures/clock";
import { Block, CheckBlock, EventBlock, YouBlock } from "./blocks";
import { EpicStepCheckSchema, type EpicStepCheck } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

// withBlock renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withBlock(element: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

// checkOf is a check a step holds, of the round it ran in.
function checkOf(round: number, name: string, ok: boolean): EpicStepCheck {
  return create(EpicStepCheckSchema, { round, name, ok });
}

// iconMarkup is the markup Icon draws for a name, so a test can compare the icon a block draws with it.
async function iconMarkup(name: IconName): Promise<string> {
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

test("Shapes: a CheckBlock draws one block of kind checks: CHECKS, round N from the engine, and its time", async () => {
  await withBlock(
    <CheckBlock round={2} checks={[checkOf(2, "tests", true)]} text="All checks pass." time={at("@14:14")} />,
    (container) => {
      const roots = container.querySelectorAll<HTMLElement>('[data-shape="block"]');
      assert.equal(roots.length, 1, "a CheckBlock draws more or fewer than one block root");
      assert.equal(roots[0].classList.contains("checks"), true, "the block is not of kind checks");
      assert.equal(textOf(roots[0], ".label"), "CHECKS");
      assert.equal(textOf(roots[0], ".words"), "round 2 from the engine");
      assert.equal(textOf(roots[0], ".at"), clock(at("@14:14")));
    },
  );
});

test("Shapes: a CheckBlock draws a chip per check of its round, with the check icon when it passed and the cross when it failed, and tone bad when one failed", async () => {
  const checkIcon = await iconMarkup("check");
  const crossIcon = await iconMarkup("cross");
  await withBlock(
    <CheckBlock
      round={2}
      checks={[checkOf(1, "lint", false), checkOf(2, "tests", true), checkOf(2, "owned files", false)]}
      text="owned files: the change touches go.mod."
      time={at("@14:14")}
    />,
    (container) => {
      const root = container.querySelector<HTMLElement>('[data-shape="block"]');
      assert.ok(root, "the CheckBlock has no block root");
      assert.equal(root.dataset.tone, "bad");
      const chips = [...root.querySelectorAll<HTMLElement>(".chip")];
      assert.deepEqual(
        chips.map((chip) => chip.textContent),
        ["tests", "owned files"],
        "a chip per check of round 2, and none of round 1",
      );
      assert.deepEqual(
        chips.map((chip) => chip.classList.contains("bad")),
        [false, true],
        "only the failed check's chip is bad",
      );
      assert.deepEqual(
        chips.map((chip) => chip.classList.contains("ok")),
        [true, false],
        "the passed check's chip is ok, so it draws in the ok tone",
      );
      assert.equal(chips[0].innerHTML.includes(checkIcon), true, "the passed check does not draw the check icon");
      assert.equal(chips[1].innerHTML.includes(crossIcon), true, "the failed check does not draw the cross icon");
    },
  );
});

test("Shapes: a CheckBlock whose checks of its round all passed is tone ok", async () => {
  await withBlock(
    <CheckBlock
      round={3}
      checks={[checkOf(3, "tests", true), checkOf(3, "lint", true)]}
      text="All checks pass."
      time={at("@14:19")}
    />,
    (container) => {
      const root = container.querySelector<HTMLElement>('[data-shape="block"]');
      assert.ok(root, "the CheckBlock has no block root");
      assert.equal(root.dataset.tone, "ok");
      assert.equal(root.querySelectorAll(".chip").length, 2, "a chip per check of round 3");
      assert.equal(root.querySelector(".chip.bad"), null, "no check failed, so no chip is bad");
    },
  );
});

test("Shapes: a CheckBlock given no check of its round draws no chips and the engine tone", async () => {
  await withBlock(<CheckBlock round={2} checks={[]} text="The checks did not run." time={at("@14:14")} />, (container) => {
    const root = container.querySelector<HTMLElement>('[data-shape="block"]');
    assert.ok(root, "the CheckBlock has no block root");
    assert.equal(root.dataset.tone, "engine");
    assert.equal(root.querySelectorAll(".chip").length, 0, "a CheckBlock with no checks draws chips");
  });
  await withBlock(
    <CheckBlock round={2} checks={[checkOf(1, "tests", false)]} text="The checks did not run." time={at("@14:14")} />,
    (container) => {
      const root = container.querySelector<HTMLElement>('[data-shape="block"]');
      assert.ok(root, "the CheckBlock has no block root");
      assert.equal(root.dataset.tone, "engine", "a failed check of another round sets the tone");
      assert.equal(root.querySelectorAll(".chip").length, 0, "a check of another round draws a chip");
    },
  );
});

test("Shapes: a CheckBlock's note is its text's first line, and a chevron in its end opens the rest under it, and folds it again", async () => {
  const text = "owned files: the change touches go.mod.\nRevert go.mod, or ask the planner to widen the task.";
  await withBlock(
    <CheckBlock round={2} checks={[checkOf(2, "owned files", false)]} text={text} time={at("@14:14")} />,
    async (container) => {
      const root = container.querySelector<HTMLElement>('[data-shape="block"]');
      assert.ok(root, "the CheckBlock has no block root");
      assert.equal(root.querySelector(".opened"), null, "the rest is open before the chevron is clicked");
      assert.equal(root.textContent.includes("owned files"), true, "the note is not drawn");
      assert.equal(root.textContent.includes("Revert go.mod"), false, "the rest is drawn before it is opened");
      const chevron = root.querySelector<HTMLElement>(".end button");
      assert.ok(chevron, "the chevron is not in the end");
      await act(() => {
        chevron.click();
      });
      assert.equal(
        root.querySelector(".opened")?.textContent?.includes("Revert go.mod"),
        true,
        "the rest does not open under the note",
      );
      await act(() => {
        chevron.click();
      });
      assert.equal(root.querySelector(".opened"), null, "the rest does not fold again");
    },
  );
});

test("Shapes: a CheckBlock given open draws the rest under its note from the start", async () => {
  const text = "owned files: the change touches go.mod.\nRevert go.mod, or ask the planner to widen the task.";
  await withBlock(
    <CheckBlock round={2} checks={[checkOf(2, "owned files", false)]} text={text} time={at("@14:14")} open />,
    (container) => {
      const root = container.querySelector<HTMLElement>('[data-shape="block"]');
      assert.ok(root, "the CheckBlock has no block root");
      assert.equal(
        root.querySelector(".opened")?.textContent?.includes("Revert go.mod"),
        true,
        "the rest is not drawn under the note",
      );
    },
  );
});
