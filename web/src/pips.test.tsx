import assert from "node:assert/strict";
import { test } from "node:test";

import { render } from "preact";
import type { JSX } from "preact";

import { Pips, type PipTone } from "./pips";
import { installTestDom, removeTestDom } from "./testdom";

async function withRender(node: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(node, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// tonesOf reads the tone of each mark the pips draw, in the order the marks sit.
function tonesOf(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("span.pips > i"), (mark) => mark.getAttribute("data-tone") ?? "");
}

// labelOf reads the pips' accessible label.
function labelOf(container: HTMLElement): string | null | undefined {
  return container.querySelector("span.pips")?.getAttribute("aria-label");
}

test("EpicWords: Pips draws one mark per cell, in the order of the cells, each carrying its tone", () =>
  withRender(<Pips cells={["ok", "ok", "run", "mine", "none"]} />, (container) => {
    assert.deepEqual(tonesOf(container), ["ok", "ok", "run", "mine", "none"]);
  }));

test("EpicWords: a limited cell's mark carries limited, apart from a held cell's mine", () =>
  withRender(<Pips cells={["limited", "mine", "none", "none", "none"]} />, (container) => {
    assert.deepEqual(tonesOf(container), ["limited", "mine", "none", "none", "none"]);
  }));

test("EpicWords: the pips are an image whose label names each ring, outside in, with its cell's words", () =>
  withRender(<Pips cells={["ok", "run", "none", "none", "none"]} />, (container) => {
    const pips = container.querySelector("span.pips");
    assert.equal(pips?.getAttribute("role"), "img");
    assert.equal(
      labelOf(container),
      "red passed, implement running, green not reached, review not reached, integrate not reached",
    );
  }));

test("EpicWords: a limited cell is labelled waiting on a limit, and a held cell held", () =>
  withRender(<Pips cells={["limited", "mine", "none", "none", "none"]} />, (container) => {
    assert.equal(
      labelOf(container),
      "red waiting on a limit, implement held, green not reached, review not reached, integrate not reached",
    );
  }));

test("EpicWords: each tone's label is its words, in the ring the cell sits in", async () => {
  const words: [PipTone, string][] = [
    ["none", "not reached"],
    ["plan", "planned"],
    ["run", "running"],
    ["mine", "held"],
    ["limited", "waiting on a limit"],
    ["bad", "failed"],
    ["acc", "accepted"],
    ["ok", "passed"],
  ];
  for (const [tone, label] of words) {
    await withRender(<Pips cells={[tone, "none", "none", "none", "none"]} />, (container) => {
      assert.equal(labelOf(container)?.startsWith(`red ${label}, `), true, `${tone} reads ${label}`);
    });
  }
});
