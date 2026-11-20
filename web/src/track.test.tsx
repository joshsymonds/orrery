import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";

import type { Stage } from "./recordwords";
import { Track } from "./track";
import { installTestDom, removeTestDom } from "./testdom";

async function withRender(stages: Stage[], check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Track stages={stages} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

const stages: Stage[] = [
  { name: "Contract", state: "done", tone: "ok", words: "accepted 14:02" },
  { name: "Build", state: "done", tone: "ok", words: "3 of 3 tasks done" },
  { name: "Review", state: "now", tone: "live", words: "reviewing" },
  { name: "Release", state: "ahead", tone: "quiet", words: "not reached" },
  { name: "Report", state: "ahead", tone: "quiet", words: "not reached" },
];

// stageElements reads the track's stage marks in the order they sit.
function stageElements(container: HTMLElement): Element[] {
  return Array.from(container.querySelectorAll("nav.track > div.stage"));
}

// nameOf reads a stage's name, without the check glyph a done stage carries.
function nameOf(stage: Element): string {
  return (stage.querySelector("span.nm")?.textContent ?? "").replace("✓", "");
}

test("EpicRecord: Track draws the five stages in order, each with its name and its words", () =>
  withRender(stages, (container) => {
    const nav = container.querySelector("nav.track");
    assert.equal(nav?.getAttribute("aria-label"), "where this epic is");
    const marks = stageElements(container);
    assert.deepEqual(marks.map(nameOf), ["Contract", "Build", "Review", "Release", "Report"]);
    assert.deepEqual(
      marks.map((mark) => mark.querySelector("span.ds")?.textContent),
      ["accepted 14:02", "3 of 3 tasks done", "reviewing", "not reached", "not reached"],
    );
  }));

test("EpicRecord: a done stage is marked done and draws a check glyph hidden from assistive tech", () =>
  withRender(stages, (container) => {
    const [contract, build, review] = stageElements(container);
    assert.equal(contract?.classList.contains("done"), true);
    assert.equal(build?.classList.contains("done"), true);
    assert.equal(review?.classList.contains("done"), false);
    assert.equal(contract?.querySelector("span.nm > span[aria-hidden='true']")?.textContent, "✓");
  }));

test("EpicRecord: exactly one stage is aria-current, the now stage, carrying its tone class", () =>
  withRender(stages, (container) => {
    const current = container.querySelectorAll("[aria-current='step']");
    assert.equal(current.length, 1);
    assert.equal(current[0], stageElements(container)[2]);
    assert.equal(current[0]?.classList.contains("now"), true);
    assert.equal(current[0]?.classList.contains("t-live"), true);
  }));

test("EpicRecord: an ahead stage reads not reached and is neither done nor current", () =>
  withRender(stages, (container) => {
    const [, , , release, report] = stageElements(container);
    for (const mark of [release, report]) {
      assert.equal(mark?.classList.contains("ahead"), true);
      assert.equal(mark?.hasAttribute("aria-current"), false);
      assert.equal(mark?.querySelector("span.ds")?.textContent, "not reached");
    }
  }));

test("EpicRecord: a now stage held on a usage limit keeps its quiet tone, not live", () =>
  withRender(
    [
      { name: "Contract", state: "done", tone: "ok", words: "accepted 14:02" },
      { name: "Build", state: "now", tone: "quiet", words: "1 of 3 tasks done · waiting on a limit" },
      { name: "Review", state: "ahead", tone: "quiet", words: "not reached" },
      { name: "Release", state: "ahead", tone: "quiet", words: "not reached" },
      { name: "Report", state: "ahead", tone: "quiet", words: "not reached" },
    ],
    (container) => {
      const current = container.querySelector("[aria-current='step']");
      assert.equal(current?.classList.contains("t-quiet"), true);
      assert.equal(current?.classList.contains("t-live"), false);
    },
  ));
