import assert from "node:assert/strict";
import { test } from "node:test";

import { decide, type FaultFailure } from "./decide";
import { type FaultKind, type FaultRecord, faultKinds, type Side } from "./faults";
import type { ComponentRecord, NavigationRecord, Report, SideMatch } from "./report";

const scenario = "list-home";
const widths = [1600, 1000];

// match is one side's selector and the number of elements it matched.
function match(selector: string, matches = 1, error?: string): SideMatch {
  return error === undefined ? { selector, matches } : { selector, matches, error };
}

// component is one component at one width whose two sides match one element each, with one equal property.
function component(name: string, width: number, overrides: Partial<ComponentRecord> = {}): ComponentRecord {
  return {
    scenario,
    page: "list",
    name,
    grade: "1",
    width,
    wireframe: match(`.wire-${name}`),
    live: match(`.live-${name}`),
    properties: [{ name: "font-size", wireframe: "14px", live: "14px", equal: true }],
    ...overrides,
  };
}

// outcome is one side's navigation at one width: completed, or stopped with the error.
function outcome(side: Side, width: number, completed = true, error = ""): NavigationRecord {
  return { scenario, side, width, completed, error };
}

// fault is one layout fault on the live side at 1600 px, unless overridden.
function fault(kind: FaultKind, overrides: Partial<FaultRecord> = {}): FaultRecord {
  return {
    scenario,
    side: "live",
    width: 1600,
    kind,
    selector: `.live-${kind}`,
    detail: `${kind} detail`,
    reference: false,
    ...overrides,
  };
}

// report is a report of two components at each width, both sides navigated completely, with nothing else in it.
function report(overrides: Partial<Report> = {}): Report {
  return {
    version: 1,
    pages: ["list"],
    widths,
    navigation: widths.flatMap((width) => [outcome("wireframe", width), outcome("live", width)]),
    components: widths.flatMap((width) => [component("need", width), component("step", width)]),
    faults: [],
    liveOnly: [],
    ...overrides,
  };
}

test("a report whose components match one element with equal properties and no live faults passes", () => {
  const decision = decide(report());
  assert.equal(decision.pass, true);
  assert.deepEqual(decision.failures, []);
});

test("wireframe reference faults and a liveOnly entry with count 0 leave a passing report passing", () => {
  const decision = decide(report({
    faults: [
      fault("narrow-wrap", { side: "wireframe", reference: true }),
      fault("request-failed", { side: "wireframe", reference: true, selector: "link", detail: "GET fonts failed" }),
    ],
    liveOnly: [{ page: "list", live: ".gone", note: "", counts: [{ scenario, width: 1600, matches: 0 }] }],
  }));
  assert.equal(decision.pass, true);
  assert.deepEqual(decision.failures, []);
});

test("a property that differs fails the component, naming the property and both values", () => {
  const decision = decide(report({
    components: [
      component("need", 1600, { properties: [{ name: "font-size", wireframe: "14px", live: "15px", equal: false }] }),
    ],
  }));
  assert.equal(decision.pass, false);
  assert.deepEqual(decision.failures, [
    {
      target: "component",
      scenario,
      width: 1600,
      component: "need",
      side: "live",
      selector: ".live-need",
      reason: 'property font-size differs: wireframe "14px", live "15px"',
    },
  ]);
});

test("a live selector matching 0 elements fails the component, naming the live side and its selector", () => {
  const decision = decide(report({ components: [component("need", 1600, { live: match(".live-need", 0) })] }));
  assert.equal(decision.pass, false);
  assert.deepEqual(decision.failures, [
    {
      target: "component",
      scenario,
      width: 1600,
      component: "need",
      side: "live",
      selector: ".live-need",
      reason: 'live selector ".live-need" matched 0 elements, want exactly 1',
    },
  ]);
});

test("a wireframe selector matching 2 elements fails the component, naming the wireframe side and its selector", () => {
  const decision = decide(report({ components: [component("need", 1600, { wireframe: match(".wire-need", 2) })] }));
  assert.equal(decision.pass, false);
  assert.deepEqual(decision.failures, [
    {
      target: "component",
      scenario,
      width: 1600,
      component: "need",
      side: "wireframe",
      selector: ".wire-need",
      reason: 'wireframe selector ".wire-need" matched 2 elements, want exactly 1',
    },
  ]);
});

test("a selector that would not run, with count -1 and its error, fails the component, naming side and selector", () => {
  const decision = decide(report({
    components: [component("need", 1600, { live: match(".live-[", -1, "SyntaxError: bad selector") })],
  }));
  assert.equal(decision.pass, false);
  assert.deepEqual(decision.failures, [
    {
      target: "component",
      scenario,
      width: 1600,
      component: "need",
      side: "live",
      selector: ".live-[",
      reason: 'live selector ".live-[" matched -1 elements, want exactly 1: SyntaxError: bad selector',
    },
  ]);
});

test("an incomplete live navigation fails every component of that scenario at that width, and none at the other width", () => {
  const decision = decide(report({
    navigation: [
      outcome("wireframe", 1600),
      outcome("live", 1600, false, "waiting for .list: timeout"),
      outcome("wireframe", 1000),
      outcome("live", 1000),
    ],
  }));
  assert.equal(decision.pass, false);
  assert.deepEqual(decision.failures, [
    {
      target: "component",
      scenario,
      width: 1600,
      component: "need",
      side: "live",
      selector: ".live-need",
      reason: "live navigation incomplete: waiting for .list: timeout",
    },
    {
      target: "component",
      scenario,
      width: 1600,
      component: "step",
      side: "live",
      selector: ".live-step",
      reason: "live navigation incomplete: waiting for .list: timeout",
    },
  ]);
});

for (const kind of faultKinds) {
  test(`a live ${kind} fault fails, naming its kind and selector`, () => {
    const overlap = kind === "overlap";
    const decision = decide(report({
      faults: [fault(kind, overlap ? { other: ".live-other" } : {})],
    }));
    assert.equal(decision.pass, false);
    const expected: FaultFailure = {
      target: "fault",
      scenario,
      width: 1600,
      fault: kind,
      side: "live",
      selector: `.live-${kind}`,
      ...(overlap ? { other: ".live-other" } : {}),
      reason: `${kind} detail`,
    };
    assert.deepEqual(decision.failures, [expected]);
  });
}
