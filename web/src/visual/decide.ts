// The pass/fail decision of the visual check. It reads a report and nothing else: no playwright-core, no DOM, no file
// access. visual-check exits by it, and the self-test runs planted reports through it.
//
// A scenario whose navigation did not complete on a side fails each of its components at that width, once for each
// incomplete side, and nothing else about those components is compared. Otherwise a component fails once for each side
// that matched other than exactly one element, and once for each listed property that differs when both sides matched
// one. A live fault fails. Wireframe reference faults and liveOnly entries never fail.

import type { FaultKind, FaultRecord, Side } from "./faults";
import type { ComponentRecord, NavigationRecord, Report, SideMatch } from "./report";

// ComponentFailure is one component failing at one width. side and selector name the side whose selector failed; a
// property failure names the live side, whose selector is the one under test.
export interface ComponentFailure {
  readonly target: "component";
  readonly scenario: string;
  readonly width: number;
  readonly component: string;
  readonly side: Side;
  readonly selector: string;
  readonly reason: string;
}

// FaultFailure is one live layout fault. other is the second element of an overlap.
export interface FaultFailure {
  readonly target: "fault";
  readonly scenario: string;
  readonly width: number;
  readonly fault: FaultKind;
  readonly side: Side;
  readonly selector: string;
  readonly other?: string;
  readonly reason: string;
}

export type Failure = ComponentFailure | FaultFailure;

// Decision is the verdict on one report. pass is true exactly when failures is empty.
export interface Decision {
  readonly pass: boolean;
  readonly failures: readonly Failure[];
}

const sides: readonly Side[] = ["wireframe", "live"];

// decide returns the failures of a report in its order: each component's, then each live fault's.
export function decide(report: Report): Decision {
  const failures: Failure[] = [
    ...report.components.flatMap((record) => componentFailures(report.navigation, record)),
    ...report.faults.filter((fault) => !fault.reference).map(faultFailure),
  ];
  return { pass: failures.length === 0, failures };
}

// componentFailures returns the failures of one component at one width.
function componentFailures(navigation: readonly NavigationRecord[], record: ComponentRecord): Failure[] {
  const incomplete = sides.flatMap((side): Failure[] => {
    const outcome = navigation.find(
      (candidate) => candidate.scenario === record.scenario && candidate.width === record.width && candidate.side === side,
    );
    if (outcome?.completed === true) {
      return [];
    }
    const reason = outcome === undefined
      ? `${side} navigation was not recorded`
      : `${side} navigation incomplete: ${outcome.error}`;
    return [componentFailure(record, side, reason)];
  });
  if (incomplete.length > 0) {
    return incomplete;
  }

  const failures: Failure[] = [];
  for (const side of sides) {
    const match = record[side];
    if (match.matches !== 1) {
      failures.push(componentFailure(record, side, countReason(side, match)));
    }
  }
  if (record.wireframe.matches === 1 && record.live.matches === 1 && record.properties !== null) {
    for (const property of record.properties) {
      if (!property.equal) {
        const reason = `property ${property.name} differs: wireframe "${property.wireframe}", live "${property.live}"`;
        failures.push(componentFailure(record, "live", reason));
      }
    }
  }
  return failures;
}

// countReason says how many elements a side's selector matched, against the one it must match.
function countReason(side: Side, match: SideMatch): string {
  const error = match.error === undefined ? "" : `: ${match.error}`;
  return `${side} selector "${match.selector}" matched ${match.matches} elements, want exactly 1${error}`;
}

// componentFailure builds the failure of one component on one side, with the selector that side measured.
function componentFailure(record: ComponentRecord, side: Side, reason: string): ComponentFailure {
  return {
    target: "component",
    scenario: record.scenario,
    width: record.width,
    component: record.name,
    side,
    selector: record[side].selector,
    reason,
  };
}

// faultFailure builds the failure of one live fault, which names its other element when it has one.
function faultFailure(fault: FaultRecord): FaultFailure {
  return {
    target: "fault",
    scenario: fault.scenario,
    width: fault.width,
    fault: fault.kind,
    side: fault.side,
    selector: fault.selector,
    ...(fault.other === undefined ? {} : { other: fault.other }),
    reason: fault.detail,
  };
}
