import type {
  Epic,
  EpicClosure,
  EpicLedgerFinding,
  EpicReleaseProof,
  EpicReleaseRun,
  EpicReviewVerdict,
  EpicsGetResponse,
} from "./gen/orrery/v1/epics_pb";

// The words and tones of an epic's Review, Release and Report, and the epics a superseded close may name. A component
// draws these and decides nothing.
export type ChipTone = "ok" | "bad" | "attention" | "live" | "quiet";
export type CellTone = "none" | "live" | "ok" | "bad";

export interface ReviewView {
  words: string;
  reviewers: { reviewer: string; reason: string; verdict: string; tone: ChipTone }[];
  findings: {
    id: string;
    what: string;
    where: string;
    result: string;
    tone: ChipTone;
    reason: string;
    closures: { text: string; tone: ChipTone }[];
  }[];
}

export interface ReleaseView {
  words: string;
  rows: {
    position: number;
    step: string;
    target: string;
    undo: string;
    state: string;
    tone: CellTone;
    reason: string;
    evidence: string;
    runs: string[];
  }[];
  then: string;
  offer: number;
}

export interface ReportView {
  sentence: string;
  why: string;
  shipped: string;
  notShipped: string;
  replacedBy: string;
  gaps: boolean;
}

// The phases Review is drawn in. Accepted and built are never reviewed on the page.
const reviewPhases = new Set([
  "reviewing",
  "correcting",
  "clean",
  "releasing",
  "stopped",
  "released",
  "done",
  "ended_with_gaps",
  "closed_superseded",
  "closed_shipped",
  "closed_abandoned",
]);

// The phases Release is drawn in, without a release run or a step that has left waiting.
const releasePhases = new Set(["clean", "releasing", "stopped", "released", "done"]);

// An undo is none when its text, trimmed, is none ignoring case: the store's rule.
export function isUndoNone(undo: string): boolean {
  return undo.trim().toLowerCase() === "none";
}

function phaseOf(detail: EpicsGetResponse): string {
  return detail.epic?.outcome ?? "";
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? `1 ${one}` : `${count} ${many}`;
}

function verdictTone(verdict: string): ChipTone {
  switch (verdict) {
    case "holds":
      return "ok";
    case "fails":
      return "bad";
    case "gap":
      return "attention";
    default:
      return "quiet";
  }
}

function reviewerLine(verdict: EpicReviewVerdict) {
  return { reviewer: verdict.reviewer, reason: verdict.reason, verdict: verdict.verdict, tone: verdictTone(verdict.verdict) };
}

function findingChip(finding: EpicLedgerFinding): { word: string; tone: ChipTone } {
  switch (finding.result) {
    case "confirmed":
      return { word: "confirmed", tone: "bad" };
    case "dropped":
      return { word: "dropped", tone: "quiet" };
    case "undetermined":
      return { word: "undetermined", tone: "attention" };
    case "":
      return finding.unverified
        ? { word: "left unverified", tone: "attention" }
        : { word: "being verified", tone: "live" };
    default:
      return { word: finding.result, tone: "quiet" };
  }
}

function closureLine(closure: EpicClosure) {
  const reason = closure.reason === "" ? "" : ` · ${closure.reason}`;
  return {
    text: `round ${Number(closure.round)} · ${closure.result}${reason}`,
    tone: closure.result === "closed" ? ("ok" as const) : ("attention" as const),
  };
}

function findingLine(finding: EpicLedgerFinding): ReviewView["findings"][number] {
  const chip = findingChip(finding);
  return {
    id: finding.id,
    what: finding.claim,
    where: finding.location,
    result: chip.word,
    tone: chip.tone,
    reason: finding.reason,
    closures: finding.closures.map(closureLine),
  };
}

// The Review section of an orreryd epic in a review phase, or null when the page does not reach it.
export function reviewOf(detail: EpicsGetResponse): ReviewView | null {
  const phase = phaseOf(detail);
  if (!reviewPhases.has(phase)) {
    return null;
  }
  const reviewing = phase === "reviewing" || phase === "correcting";
  if (!reviewing && detail.reviews.length === 0 && detail.ledger.length === 0) {
    return null;
  }
  const reviewers = detail.reviews.length === 0 ? "no verdict yet" : plural(detail.reviews.length, "reviewer", "reviewers");
  const findings = detail.ledger.length === 0 ? "no findings" : plural(detail.ledger.length, "finding", "findings");
  return {
    words: `${reviewers} · ${findings}`,
    reviewers: detail.reviews.map(reviewerLine),
    findings: detail.ledger.map(findingLine),
  };
}

function stateCell(state: string): { word: string; tone: CellTone } {
  switch (state) {
    case "waiting":
      return { word: "waiting", tone: "none" };
    case "running":
      return { word: "running", tone: "live" };
    case "passed":
      return { word: "passed", tone: "ok" };
    case "failed":
      return { word: "failed", tone: "bad" };
    default:
      return { word: state, tone: "none" };
  }
}

function runLine(run: EpicReleaseRun): string {
  const reason = run.reason === "" ? "" : ` · ${run.reason}`;
  return `attempt ${Number(run.attempt)} · ${run.passed ? "passed" : "failed"}${reason}`;
}

function rowOf(step: EpicReleaseProof, releases: EpicReleaseRun[]): ReleaseView["rows"][number] {
  const cell = stateCell(step.state);
  const runs = releases.filter((run) => (run.part === "release" || run.part === "named") && run.position === step.position);
  return {
    position: Number(step.position),
    step: step.step,
    target: step.target,
    undo: step.undo === "" ? "not given" : step.undo,
    state: cell.word,
    tone: cell.tone,
    reason: step.state === "failed" ? step.reason : "",
    evidence: step.evidence,
    runs: runs.map(runLine),
  };
}

// The first release step whose state is not passed, if any.
function firstUnpassed(steps: EpicReleaseProof[]): EpicReleaseProof | undefined {
  return steps.find((step) => step.state !== "passed");
}

// The count words of the Release section: the step the releasing or stopped epic is at, else the steps passed.
function releaseWords(phase: string, steps: EpicReleaseProof[]): string {
  const n = steps.length;
  switch (phase) {
    case "releasing": {
      const at = steps.find((step) => step.state === "running") ?? firstUnpassed(steps);
      return `step ${at === undefined ? n : Number(at.position)} of ${n}`;
    }
    case "stopped": {
      const at = firstUnpassed(steps);
      return `stopped at step ${at === undefined ? n : Number(at.position)} of ${n}`;
    }
    default:
      return `${steps.filter((step) => step.state === "passed").length} of ${n} steps passed`;
  }
}

// The one step Release from step N is offered for: the first unpassed step, when it failed with no undo in a stopped
// epic. Zero when no step is offered.
function offerOf(phase: string, steps: EpicReleaseProof[]): number {
  const step = firstUnpassed(steps);
  if (phase !== "stopped" || step === undefined || step.state !== "failed" || !isUndoNone(step.undo)) {
    return 0;
  }
  return Number(step.position);
}

// The Release section of an orreryd epic with release steps, reached in a release phase or once a step or run is in
// view. Null when the page does not reach it.
export function releaseOf(detail: EpicsGetResponse): ReleaseView | null {
  const phase = phaseOf(detail);
  const steps = detail.releaseSteps;
  if (steps.length === 0) {
    return null;
  }
  if (!releasePhases.has(phase) && detail.releases.length === 0 && steps.every((step) => step.state === "waiting")) {
    return null;
  }
  const then = detail.contract?.releaseThen ?? "";
  return {
    words: releaseWords(phase, steps),
    rows: steps.map((step) => rowOf(step, detail.releases)),
    then: then === "" ? "" : `Then: ${then}`,
    offer: offerOf(phase, steps),
  };
}

// The sentence of a report's outcome.
function sentenceOf(outcome: string): string {
  switch (outcome) {
    case "released":
      return "Released.";
    case "done":
      return "Done.";
    case "met_at_base":
      return "Already met at its base.";
    case "ended_with_gaps":
      return "Ended with gaps. It did not release.";
    case "closed_superseded":
      return "Closed as superseded.";
    case "closed_shipped":
      return "Closed as shipped outside orrery.";
    case "closed_abandoned":
      return "Closed as abandoned.";
    default:
      return `Ended: ${outcome}.`;
  }
}

// The part of an epic id after its last slash, or the whole id when it has none.
function slugOf(id: string): string {
  return id.slice(id.lastIndexOf("/") + 1);
}

// The Report section of an orreryd epic, reached when its report is set or its phase is a closed one.
export function reportOf(detail: EpicsGetResponse): ReportView | null {
  const phase = phaseOf(detail);
  const report = detail.report;
  if (report === undefined && !phase.startsWith("closed_")) {
    return null;
  }
  return {
    sentence: sentenceOf(report === undefined ? phase : report.outcome),
    why: report?.why ?? "",
    shipped: report === undefined ? "" : report.shipped.length === 0 ? "none" : report.shipped.join(", "),
    notShipped: report === undefined ? "" : report.notShipped.join(", "),
    replacedBy: slugOf(detail.supersededBy),
    gaps: phase === "ended_with_gaps",
  };
}

// The listed epics a superseded close may name: those on this epic's path, other than this epic, in listed order.
export function replacementsOf(detail: EpicsGetResponse, listed: Epic[]): { id: string; slug: string }[] {
  const self = detail.epic;
  const repoPath = self?.path ?? "";
  if (repoPath === "") {
    return [];
  }
  return listed
    .filter((epic) => epic.path === repoPath && epic.id !== self?.id)
    .map((epic) => ({ id: epic.id, slug: epic.slug }));
}
