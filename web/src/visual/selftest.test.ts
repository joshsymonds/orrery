import assert from "node:assert/strict";
import { test } from "node:test";

import type { Failure } from "./decide";
import { type Defect, type Expected, judge, type SelfTestCase, selfTestCases } from "./selftest";

const defects: readonly Defect[] = [
  "property",
  "selector-none",
  "selector-twice",
  "navigation",
  "narrow-wrap",
  "overlap",
  "overflow",
  "empty-heading",
  "request-failed",
];

// caseOf is the case the self-test runs for an expected outcome.
function caseOf(expected: Expected): SelfTestCase {
  const found = selfTestCases.find((testCase) => testCase.expected === expected);
  if (found === undefined) {
    throw new Error(`no case expects ${expected}`);
  }
  return found;
}

// failureOf is the failure a planted defect makes, worded as decide() words it.
function failureOf(defect: Defect): Failure {
  const scenario = defect;
  switch (defect) {
    case "property":
      return {
        target: "component",
        scenario,
        width: 1600,
        component: "title",
        side: "live",
        selector: ".title",
        reason: 'property color differs: wireframe "rgb(20, 40, 60)", live "rgb(200, 40, 60)"',
      };
    case "selector-none":
      return {
        target: "component",
        scenario,
        width: 1600,
        component: "note",
        side: "live",
        selector: ".note",
        reason: 'live selector ".note" matched 0 elements, want exactly 1',
      };
    case "selector-twice":
      return {
        target: "component",
        scenario,
        width: 1600,
        component: "note",
        side: "live",
        selector: ".note",
        reason: 'live selector ".note" matched 2 elements, want exactly 1',
      };
    case "navigation":
      return {
        target: "component",
        scenario,
        width: 1600,
        component: "title",
        side: "live",
        selector: ".title",
        reason: "live navigation incomplete: #ready did not show within 3000 ms",
      };
    default:
      return {
        target: "fault",
        scenario,
        width: 1600,
        fault: defect,
        side: "live",
        selector: "body > p.note:nth-child(2)",
        reason: `${defect} detail`,
      };
  }
}

test("match with no failures is right", () => {
  assert.equal(judge(caseOf("pass"), { pass: true, failures: [] }).right, true);
});

test("match with one failure is wrong", () => {
  assert.equal(judge(caseOf("pass"), { pass: false, failures: [failureOf("overlap")] }).right, false);
});

test("each planted case with only its own defect's failures is right", () => {
  for (const defect of defects) {
    const verdict = judge(caseOf(defect), { pass: false, failures: [failureOf(defect)] });
    assert.equal(verdict.right, true, `${defect}: ${verdict.explanation}`);
  }
});

test("a planted case whose decision passes is wrong", () => {
  for (const defect of defects) {
    assert.equal(judge(caseOf(defect), { pass: true, failures: [] }).right, false, defect);
  }
});

test("a planted case whose failures include another defect is wrong", () => {
  defects.forEach((defect, index) => {
    const other = defects[(index + 1) % defects.length];
    const verdict = judge(caseOf(defect), { pass: false, failures: [failureOf(defect), failureOf(other)] });
    assert.equal(verdict.right, false, `${defect} with a ${other} failure: ${verdict.explanation}`);
  });
});

test("the cases hold match and one planted case for each of the nine defects", () => {
  assert.deepEqual(selfTestCases.map((testCase) => testCase.expected).sort(), ["pass", ...defects].sort());
  assert.equal(new Set(selfTestCases.map((testCase) => testCase.id)).size, selfTestCases.length);
});
