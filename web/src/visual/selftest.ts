// The synthetic page pairs of the visual self-test, and the verdict that says whether each pair came out as it should. A
// case is a wireframe page, a live page, a map of one scenario on one page at widths 1600 and 1000, and its expected
// outcome: pass, or the one defect its live page plants. Apart from that defect each page is clean. The pages use only the
// fonts the visual profile carries and generic families, and no URL leaves the page's own origin.
//
// This module is pure data and logic, with no playwright-core, DOM or file access. selftest-main serves the pages and runs
// them through the report's measurement and decide(); judge checks each run against its case.

import type { Decision, Failure } from "./decide";
import type { FaultKind } from "./faults";
import type { VisualMap } from "./report";

// Defect is the one defect a live page plants: a listed property that differs, a live selector that matches nothing or
// matches twice, a navigation that never completes, or one of the five layout faults.
export type Defect = "property" | "selector-none" | "selector-twice" | "navigation" | FaultKind;

// Expected is what a case must come out as: pass, or the defect its live page plants.
export type Expected = "pass" | Defect;

// SelfTestCase is one synthetic page pair with its map and its expected outcome. id names the case, and the page the
// case's map gives its scenario.
export interface SelfTestCase {
  readonly id: string;
  readonly expected: Expected;
  readonly wireframe: string;
  readonly live: string;
  readonly map: VisualMap;
}

// Verdict says whether one case came out as expected, and why, in one line.
export interface Verdict {
  readonly right: boolean;
  readonly explanation: string;
}

// plantedProperty is the listed property the property case changes on its live page.
const plantedProperty = "color";

// base is the style both pages of every case share, so a case differs from its match only where it plants its defect.
const base = [
  'html { font-family: "Atkinson Hyperlegible Next", sans-serif; font-size: 16px; line-height: 24px; }',
  "body { margin: 0; padding: 16px; }",
  "h2 { margin: 0 0 16px; font-size: 20px; line-height: 24px; }",
  "p { margin: 0 0 16px; }",
  ".pic { display: block; width: 40px; height: 40px; }",
].join("\n");

// page is one synthetic page: the shared style, the case's own style, its body, and the ready element the navigation waits for.
function page(css: string, body: string, ready = '<div id="ready">Ready</div>'): string {
  return [
    "<!doctype html>",
    '<html lang="en">',
    "<head>",
    '<meta charset="utf-8">',
    '<link rel="icon" href="data:,">',
    `<style>\n${base}\n${css}\n</style>`,
    "</head>",
    "<body>",
    body,
    ready,
    "</body>",
    "</html>",
  ].join("\n");
}

// titled is the body every case shares: a heading and a paragraph after it, both listed as components.
const titled = '<main><h2 class="title">Lighthouse</h2><p class="note">Rota export is ready.</p></main>';

// mapOf is a case's map: one scenario on the page the case is named for, at widths 1600 and 1000, with two components
// whose selectors each match one element on both sides of a clean pair. Its liveOnly list is empty.
function mapOf(id: string): VisualMap {
  return {
    version: 1,
    widths: [1600, 1000],
    scenarios: [{ id, page: id, wireframe: "", describes: `synthetic pair for ${id}` }],
    components: [
      {
        page: id,
        scenario: id,
        name: "title",
        grade: "synthetic",
        wireframe: ".title",
        live: ".title",
        props: ["color", "font-size"],
      },
      {
        page: id,
        scenario: id,
        name: "note",
        grade: "synthetic",
        wireframe: ".note",
        live: ".note",
        props: ["font-size", "line-height"],
      },
    ],
    liveOnly: [],
  };
}

// caseOf builds a case whose id is its name and whose map is mapOf(id).
function caseOf(id: string, expected: Expected, wireframe: string, live: string): SelfTestCase {
  return { id, expected, wireframe, live, map: mapOf(id) };
}

// selfTestCases are the match case and one planted case for each defect, in the order the self-test runs them.
export const selfTestCases: readonly SelfTestCase[] = [
  caseOf("match", "pass", page("", titled), page("", titled)),
  caseOf(
    "property",
    "property",
    page(".title { color: rgb(20, 40, 60); }", titled),
    page(".title { color: rgb(200, 40, 60); }", titled),
  ),
  caseOf(
    "selector-none",
    "selector-none",
    page("", titled),
    page("", '<main><h2 class="title">Lighthouse</h2><p class="caption">Rota export is ready.</p></main>'),
  ),
  caseOf(
    "selector-twice",
    "selector-twice",
    page("", titled),
    page(
      "",
      '<main><h2 class="title">Lighthouse</h2><p class="note">Rota export is ready.</p>' +
        '<p class="note">Rota export is ready.</p></main>',
    ),
  ),
  caseOf("navigation", "navigation", page("", titled), page("", titled, '<div id="ready" hidden>Ready</div>')),
  caseOf("narrow-wrap", "narrow-wrap", page("", titled), page(".note { width: 30px; }", titled)),
  caseOf(
    "overlap",
    "overlap",
    page("", titled),
    page(
      ".stamp { position: absolute; top: 300px; left: 300px; } .stamp.bravo { top: 304px; left: 310px; }",
      titled + '<span class="stamp">Alpha</span><span class="stamp bravo">Bravo</span>',
    ),
  ),
  caseOf(
    "overflow",
    "overflow",
    page("", titled),
    page(".wide { width: 1800px; }", titled + '<div class="wide">Wide</div>'),
  ),
  caseOf(
    "empty-heading",
    "empty-heading",
    page("", titled),
    page("", '<main><h2 class="title">Lighthouse</h2></main><p class="note">Rota export is ready.</p>'),
  ),
  caseOf(
    "request-failed",
    "request-failed",
    page("", '<div class="pic"></div>' + titled),
    page("", '<img class="pic" src="/missing.png" alt="">' + titled),
  ),
];

// propertyReason is a property failure's reason: the property and the two computed values, as decide() words them.
const propertyReason = /^property (\S+) differs: wireframe "([^"]*)", live "([^"]*)"$/;

// selectorReason is a selector failure's reason, with the number of elements the selector matched.
const selectorReason = /^live selector ".*" matched (-?\d+) elements/;

// namesDefect is true when a failure is the defect: a fault of the defect's kind naming its selector, or a component
// failure whose reason is the defect's. A layout fault names only fault failures; the others name component failures.
function namesDefect(defect: Defect, failure: Failure): boolean {
  if (failure.target === "fault") {
    return failure.fault === defect && failure.selector !== "";
  }
  switch (defect) {
    case "property": {
      const match = propertyReason.exec(failure.reason);
      return failure.side === "live" && match !== null && match[1] === plantedProperty &&
        match[2] !== "" && match[3] !== "" && match[2] !== match[3];
    }
    case "selector-none":
    case "selector-twice": {
      const match = selectorReason.exec(failure.reason);
      return failure.side === "live" && match !== null && Number(match[1]) === (defect === "selector-none" ? 0 : 2);
    }
    case "navigation":
      return failure.reason.includes("navigation incomplete");
    default:
      return false;
  }
}

// oneLine is a failure's reason with its whitespace collapsed, so a verdict fits on one line.
function oneLine(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

// judge checks a decision against its case. A pass case is right only when the decision passes with no failures. A
// planted case is right only when the decision fails and every failure names the case's defect: a failure that names
// another defect, or none, makes the case wrong, and so does a decision that passes.
export function judge(testCase: SelfTestCase, decision: Decision): Verdict {
  const count = decision.failures.length;
  const failures = `${count} ${count === 1 ? "failure" : "failures"}`;
  if (testCase.expected === "pass") {
    return decision.pass && count === 0
      ? { right: true, explanation: "passed with no failures, as expected" }
      : { right: false, explanation: `expected pass, but the decision ${decision.pass ? "passed" : "failed"} with ${failures}` };
  }
  const defect = testCase.expected;
  if (decision.pass) {
    return { right: false, explanation: `expected ${defect}, but the decision passed` };
  }
  if (count === 0) {
    return { right: false, explanation: `expected ${defect}, but the decision failed with no failures` };
  }
  const stray = decision.failures.find((failure) => !namesDefect(defect, failure));
  if (stray !== undefined) {
    return { right: false, explanation: `a failure does not name ${defect}: ${oneLine(stray.reason)}` };
  }
  return { right: true, explanation: `failed with ${failures}, each naming ${defect}` };
}
