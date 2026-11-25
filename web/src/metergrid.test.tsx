import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";

import { type HostStatusResponse, HostStatusResponseSchema } from "./gen/orrery/v1/orrery_pb";
import { PaneStrip } from "./strip";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name starts with MeterGrid. The strip's meters are checked by the values style.css declares for them, read
// as text with comments cut, by the values tokens.css and tokens.json give the tokens they name, and by the markup
// PaneStrip renders.

interface Rule {
  selectors: string[];
  declarations: Map<string, string>;
}

function withoutComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function rulesOf(css: string): Rule[] {
  const rules: Rule[] = [];
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1]
      .split(",")
      .map((selector) => selector.trim().replace(/\s+/g, " "))
      .filter((selector) => selector !== "");
    const declarations = new Map<string, string>();
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0) {
        declarations.set(declaration.slice(0, colon).trim(), declaration.slice(colon + 1).trim());
      }
    }
    rules.push({ selectors, declarations });
  }
  return rules;
}

// blocksOf is the text inside each block that opens with opener, such as "@container (width < 136px) {", matched by its
// braces.
function blocksOf(css: string, opener: string): string[] {
  const bodies: string[] = [];
  let from = css.indexOf(opener);
  while (from !== -1) {
    const start = from + opener.length;
    let depth = 1;
    let end = start;
    while (depth > 0) {
      assert.ok(end < css.length, `${opener} closes`);
      if (css[end] === "{") {
        depth += 1;
      } else if (css[end] === "}") {
        depth -= 1;
      }
      end += 1;
    }
    bodies.push(css.slice(start, end - 1));
    from = css.indexOf(opener, end);
  }
  return bodies;
}

// declaredValue is the value a property takes from the last rule that names the selector, or undefined when none does.
function declaredValue(rules: Rule[], selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const rule of rules) {
    const declared = rule.declarations.get(property);
    if (declared !== undefined && rule.selectors.includes(selector)) {
      value = declared;
    }
  }
  return value;
}

const sheet = withoutComments(readFileSync(new URL("../src/style.css", import.meta.url), "utf8"));
const rules = rulesOf(sheet);
const tokensCss = withoutComments(readFileSync(new URL("../src/tokens.css", import.meta.url), "utf8"));
const tokensJson = JSON.parse(readFileSync(new URL("../visual/system/tokens.json", import.meta.url), "utf8")) as {
  layout: { meterCellLine: number };
};

// tokenValue is the value tokens.css gives a custom property.
function tokenValue(name: string): string {
  const match = tokensCss.match(new RegExp(`${name}:\\s*([^;]+);`));
  assert.ok(match !== null, `tokens.css defines ${name}`);
  return match[1].trim();
}

// px reads a length in whole pixels, such as 48px.
function px(value: string): number {
  assert.match(value, /^\d+px$/);
  return Number.parseInt(value, 10);
}

// withMeters renders the strip for host into a fresh DOM, runs check on its container, and removes the DOM when check
// ends.
async function withMeters(host: HostStatusResponse, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<PaneStrip host={host} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("MeterGrid: the strip's meters are one grid of equal columns, each at most meterCell wide", () => {
  assert.equal(declaredValue(rules, ".pane-strip .meters", "display"), "grid");
  assert.equal(
    declaredValue(rules, ".pane-strip .meters", "grid-template-columns"),
    "repeat(auto-fill, minmax(calc(var(--o-size-meter-bar-width) * 2), var(--o-layout-meter-cell)))",
    "every column is one minmax, capped at the meterCell token",
  );
  assert.equal(tokenValue("--o-layout-meter-cell"), "160px", "meterCell is 160px");
});

test("MeterGrid: a cell's declared minimum is twice the bar's width, 96px, and the cells wrap to further rows of the same columns", async () => {
  assert.equal(px(tokenValue("--o-size-meter-bar-width")) * 2, 96, "two bar widths make the 96px minimum cell");
  const host = create(HostStatusResponseSchema, {
    host: "den",
    stats: [
      { label: "cpu", value: "12%", percent: 12, level: "ok" },
      { label: "disk", value: "65%", percent: 65, level: "warn" },
      { label: "mem", value: "7.1 GB", percent: 88, level: "crit" },
      { label: "net", value: "↓1M", level: "ok" },
      { label: "load", value: "0.4", percent: 40, level: "ok" },
    ],
  });
  await withMeters(host, (container) => {
    assert.equal(container.querySelectorAll(".pane-strip > .meters").length, 1, "one grid holds the strip's meters");
    assert.equal(container.querySelectorAll(".meters > .meter").length, 5, "each stat is a cell of that one grid");
  });
});

test("MeterGrid: each meter is a cell with min-width 0 and an inline-size container", () => {
  assert.equal(declaredValue(rules, ".meter", "min-width"), "0");
  assert.equal(declaredValue(rules, ".meter", "container-type"), "inline-size");
});

test("MeterGrid: a value wraps inside its cell, and no meter rule ellipsizes, clips or stops its text", () => {
  assert.equal(declaredValue(rules, ".meter .value", "min-width"), "0", "a value never widens its cell");
  assert.equal(declaredValue(rules, ".meter .value", "overflow-wrap"), "anywhere", "a value wraps inside its cell");
  const cellRules = rules.filter((rule) =>
    rule.selectors.some((selector) => /\.meter(?![\w-])/.test(selector) && !selector.includes(".bar")),
  );
  assert.ok(cellRules.length >= 3, "the cell's name, value and meter rules are read");
  for (const rule of cellRules) {
    const named = rule.selectors.join(", ");
    assert.equal(rule.declarations.get("text-overflow"), undefined, `${named} does not ellipsize`);
    assert.ok(!["hidden", "clip"].includes(rule.declarations.get("overflow") ?? ""), `${named} does not clip`);
    assert.notEqual(rule.declarations.get("white-space"), "nowrap", `${named} does not stop its text`);
  }
});

test("MeterGrid: a cell under meterCellLine stacks its name, then its value, then its bar across the cell", () => {
  const { meterCellLine } = tokensJson.layout;
  assert.equal(`${meterCellLine}px`, tokenValue("--o-layout-meter-cell-line"), "meterCellLine is the token's 136px");
  const bodies = blocksOf(sheet, `@container (width < ${meterCellLine}px) {`);
  assert.equal(bodies.length, 1, `style.css has one @container (width < ${meterCellLine}px) query`);
  const stacked = rulesOf(bodies[0]);
  assert.equal(declaredValue(stacked, ".meter .name", "flex-basis"), "100%", "the name takes its own line");
  assert.equal(declaredValue(stacked, ".meter .value", "flex-basis"), "100%", "the value takes its own line");
  assert.equal(declaredValue(stacked, ".meter .value", "order"), "2", "the value follows the name");
  assert.equal(declaredValue(stacked, ".meter .bar", "order"), "3", "the bar follows the value");
  assert.equal(declaredValue(stacked, ".meter .bar", "width"), "100%", "the bar runs across the cell");
});

test("MeterGrid: a stat with no percent draws an empty bar line of the bar's height", async () => {
  assert.equal(declaredValue(rules, ".meter .bar", "height"), "var(--o-size-meter-bar-height)");
  assert.equal(tokenValue("--o-size-meter-bar-height"), "4px", "the bar's height token is 4px");
  assert.equal(declaredValue(rules, ".meter .bar", "background"), "var(--o-line-soft)", "the empty line is the track");
  const host = create(HostStatusResponseSchema, {
    host: "den",
    stats: [
      { label: "up", value: "3", level: "ok" },
      { label: "cpu", value: "20%", percent: 20, level: "ok" },
    ],
  });
  await withMeters(host, (container) => {
    const bars = [...container.querySelectorAll<HTMLElement>(".meter .bar")];
    assert.equal(bars.length, 2, "each stat draws a bar, filled or empty");
    assert.equal(bars[0]?.children.length, 0, "the bar of a stat with no percent holds no fill");
    assert.equal(bars[1]?.querySelector<HTMLElement>("i")?.style.width, "20%", "the bar of a stat with a percent is filled to it");
  });
});

test("MeterGrid: the phone's four-column strip is gone, so no phone rule sets the strip's columns or ellipsizes a meter", () => {
  const phone = blocksOf(sheet, "@media (width < 760px) {").flatMap((body) => rulesOf(body));
  assert.ok(phone.length > 0, "style.css has a phone block");
  for (const rule of phone) {
    if (rule.selectors.some((selector) => /pane-strip|\.meter(?![\w-])/.test(selector))) {
      const named = rule.selectors.join(", ");
      assert.equal(rule.declarations.get("grid-template-columns"), undefined, `${named} sets no columns of its own`);
      assert.equal(rule.declarations.get("text-overflow"), undefined, `${named} does not ellipsize`);
    }
  }
});
