import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";

import { sessionTotals, subscriptionTotals, unsummedTotals } from "./fixtures/usage";
import { installTestDom, removeTestDom } from "./testdom";
import { UsagePanel } from "./usagepanel";

// Each subtest's name reads the panel's figures in words. The panel is drawn from the fixtures in ./fixtures/usage, and
// every figure it shows is read back from the DOM and compared with the value the fixture's totals sum to.

async function withDom(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

// textOf is the text of the first element that matches selector, and fails the test when none does.
function textOf(root: ParentNode, selector: string): string {
  const element = root.querySelector(selector);
  assert.ok(element !== null, `no element matches ${selector}`);
  return (element.textContent ?? "").trim();
}

// cellsOf is the text of each child of element, in order.
function cellsOf(element: Element): string[] {
  return [...element.children].map((child) => (child.textContent ?? "").trim());
}

// widthsOf is the percentage width each element matching selector is drawn at, in document order.
function widthsOf(root: ParentNode, selector: string): number[] {
  return [...root.querySelectorAll(selector)].map((element) => {
    const match = /width:\s*([\d.]+)%/.exec(element.getAttribute("style") ?? "");
    assert.ok(match !== null, `${selector} draws no width`);
    return Number(match[1]);
  });
}

// assertWidths checks each drawn width is within tolerance of the percentage it should be, in order.
function assertWidths(actual: number[], expected: number[], tolerance: number): void {
  assert.equal(actual.length, expected.length, `drew ${actual.length} widths, expected ${expected.length}`);
  expected.forEach((want, i) => {
    assert.ok(
      Math.abs(actual[i] - want) <= tolerance,
      `width ${i} is ${actual[i]}%, expected ${want}% within ${tolerance}`,
    );
  });
}

test("SessionUsagePanel", async (t) => {
  await t.test("the list total and the requests read the session's sums", () =>
    withDom(<UsagePanel totals={sessionTotals} />, (container) => {
      assert.equal(textOf(container, ".usage-list"), "$3.77");
      assert.equal(textOf(container, ".usage-requests"), "at list price · 420 requests");
    }));

  await t.test("billed reads what the session was billed, and no subscription when billed is above 0", () =>
    withDom(<UsagePanel totals={sessionTotals} />, (container) => {
      assert.equal(textOf(container, ".usage-billed"), "billed $2.79");
    }));

  await t.test("billed 0 with a list cost above 0 reads subscription after billed", () =>
    withDom(<UsagePanel totals={subscriptionTotals} />, (container) => {
      assert.equal(textOf(container, ".usage-list"), "$1.40");
      assert.equal(textOf(container, ".usage-billed"), "billed $0.00 · subscription");
    }));

  await t.test("with dollars known, the kind legend reads each kind's tokens and dollars, and the bar weighs by dollars", () =>
    withDom(<UsagePanel totals={sessionTotals} />, (container) => {
      assert.equal(container.querySelector(".usage-legend.tokens-only"), null, "the legend is drawn tokens alone");
      const legend = [...container.querySelectorAll(".usage-legend .usage-kind")].map(cellsOf);
      assert.deepEqual(legend, [
        ["", "input", "1.5M", "$0.45"],
        ["", "cache write", "2.6M", "$1.56"],
        ["", "cache read", "44M", "$1.10"],
        ["", "output", "220k", "$0.66"],
      ]);
      assertWidths(widthsOf(container, ".kindbar i"), [11.94, 41.38, 29.18, 17.51], 0.005);
    }));

  await t.test("with dollars unknown, the kind legend reads tokens alone, and the bar weighs by tokens", () =>
    withDom(<UsagePanel totals={unsummedTotals} />, (container) => {
      assert.ok(container.querySelector(".usage-legend.tokens-only") !== null, "the legend is not drawn tokens alone");
      const legend = [...container.querySelectorAll(".usage-legend .usage-kind")].map(cellsOf);
      assert.deepEqual(legend, [
        ["", "input", "500k"],
        ["", "cache write", "150k"],
        ["", "cache read", "2.4M"],
        ["", "output", "35k"],
      ]);
      assertWidths(widthsOf(container, ".kindbar i"), [16.21, 4.86, 77.8, 1.13], 0.01);
    }));

  await t.test("the model-and-agent rows read model and agent, main for an empty agent type, largest first, with billed beside list", () =>
    withDom(<UsagePanel totals={sessionTotals} />, (container) => {
      const rows = [...container.querySelectorAll(".usage-row")].map((row) => ({
        who: textOf(row, ".usage-who"),
        billed: textOf(row, ".usage-row-billed"),
        list: textOf(row, ".usage-row-list"),
      }));
      assert.deepEqual(rows, [
        { who: "opus-5-5 main", billed: "billed $1.90", list: "$2.67" },
        { who: "opus-5-5 steelman", billed: "billed $0.41", list: "$0.57" },
        { who: "haiku-5-5 Explore", billed: "billed $0.28", list: "$0.31" },
        { who: "sonnet-5-5 task-reviewer", billed: "billed $0.20", list: "$0.22" },
      ]);
      assertWidths(widthsOf(container, ".usage-bar i"), [70.82, 15.12, 8.22, 5.84], 0.01);
    }));

  await t.test("the head reads Usage, and headless leaves it out", async () => {
    await withDom(<UsagePanel totals={sessionTotals} />, (container) => {
      assert.equal(textOf(container, "h2"), "Usage");
    });
    await withDom(<UsagePanel totals={sessionTotals} headless />, (container) => {
      assert.equal(container.querySelector("h2"), null, "headless draws a head");
    });
  });
});
