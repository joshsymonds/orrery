import type { Epic, EpicQuestion, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The one blocker an epic waits on: its text, and the unix second it began. A question carries the question, a held
// or limited step carries the step.
export type Blocker =
  | { kind: "question"; text: string; since: number; question: EpicQuestion }
  | { kind: "parked"; text: string; since: number }
  | { kind: "held"; text: string; since: number; step: EpicStep };

// The parts of an orreryd epic's track, in order. Contract is always passed: no phase of an epic reads as contract.
export const trackParts = ["contract", "planning", "building", "releasing", "report"] as const;
export type TrackPart = (typeof trackParts)[number];

// The state word of an epic. An orreryd epic, whose outcome is its phase, reads paused, parked, waiting, then the part
// of its track its phase is in while the phase is a track phase, and the phase as written otherwise. A record epic
// reads its outcome as written, then paused, parked, waiting, then planning or building while it is live. A missing
// epic and an epic with no state give the empty string.
export function stateOf(detail: EpicsGetResponse): string {
  const epic = detail.epic;
  if (!epic) {
    return "";
  }
  if (epic.source === "orreryd") {
    return orrerydStateOf(detail, epic);
  }
  if (epic.outcome !== "") {
    return epic.outcome;
  }
  if (detail.paused) {
    return "paused";
  }
  if (detail.parked !== "") {
    return "parked";
  }
  if (epic.waiting) {
    return "waiting";
  }
  if (epic.live) {
    return planningOrBuilding(detail);
  }
  return "";
}

// trackPart is the part of the track an orreryd epic's phase is in, or undefined for a phase the track does not name.
// It is the phase mapping the state word uses, for every phase the track names.
export function trackPart(detail: EpicsGetResponse): TrackPart | undefined {
  return partOf(detail, detail.epic?.outcome ?? "");
}

// orrerydStateOf is the state word of an orreryd epic: paused, parked, waiting, then its track part for a track phase,
// or its phase as written.
function orrerydStateOf(detail: EpicsGetResponse, epic: Epic): string {
  if (detail.paused) {
    return "paused";
  }
  if (detail.parked !== "") {
    return "parked";
  }
  if (epic.waiting) {
    return "waiting";
  }
  const part = partOf(detail, epic.outcome);
  return part !== undefined && trackPhases.has(epic.outcome) ? part : epic.outcome;
}

// The phases an orreryd epic reads as its track part, and can be stopped in: its track before stopped, which reads as
// written.
const trackPhases = new Set(["accepted", "built", "reviewing", "correcting", "clean", "releasing"]);

// partOf is the track part of an orreryd epic in its phase: planning or building while accepted, building while built,
// reviewing or correcting, releasing while clean, releasing or stopped, and report once released, done, met_at_base,
// ended_with_gaps or closed. Undefined for any other phase.
function partOf(detail: EpicsGetResponse, phase: string): TrackPart | undefined {
  switch (phase) {
    case "accepted":
      return planningOrBuilding(detail);
    case "built":
    case "reviewing":
    case "correcting":
      return "building";
    case "clean":
    case "releasing":
    case "stopped":
      return "releasing";
    case "released":
    case "done":
    case "met_at_base":
    case "ended_with_gaps":
    case "closed_superseded":
    case "closed_shipped":
    case "closed_abandoned":
      return "report";
    default:
      return undefined;
  }
}

// planningOrBuilding is planning while the epic has no task, and building once it has one.
function planningOrBuilding(detail: EpicsGetResponse): "planning" | "building" {
  return detail.tasks.length === 0 && detail.buildTasks.length === 0 ? "planning" : "building";
}

// The orreryd phases an epic can be paused in: its track before clean.
const pausablePhases = new Set(["accepted", "built", "reviewing", "correcting"]);

// Controls are the epic's commands that show on its page: Pause or Resume, and Stop.
export interface Controls {
  pause: boolean;
  resume: boolean;
  stop: boolean;
}

// controlsOf is the one control rule, shared by the epic page and the card: nothing shows once the epic is not live. An
// orreryd epic resumes whenever it is paused, pauses while not paused in accepted, built, reviewing or correcting, and
// stops on its track. A record epic pauses or resumes, and stops, only while it has no outcome.
export function controlsOf(detail: EpicsGetResponse): Controls {
  const epic = detail.epic;
  if (!epic?.live) {
    return { pause: false, resume: false, stop: false };
  }
  if (epic.source === "orreryd") {
    return {
      pause: !detail.paused && pausablePhases.has(epic.outcome),
      resume: detail.paused,
      stop: trackPhases.has(epic.outcome),
    };
  }
  const open = epic.outcome === "";
  return { pause: open && !detail.paused, resume: open && detail.paused, stop: open };
}

// The blocker an epic waits on, first match: its first open question the owner is asked (one its steerer does not
// hold), its park, then its first step that has not ended and is held or limited. Null when none of them holds.
export function blockerOf(detail: EpicsGetResponse): Blocker | null {
  const question = detail.questions.find((candidate) => !candidate.steererHeld);
  if (question !== undefined) {
    return { kind: "question", text: "waits on your answer", since: Number(question.asked), question };
  }
  if (detail.parked !== "") {
    return { kind: "parked", text: `parked · ${detail.parked}`, since: 0 };
  }
  const step = detail.steps.find((s) => s.end === "" && (s.held || s.limited));
  if (step) {
    const name = step.task !== "" ? step.task : step.agent;
    return {
      kind: "held",
      text: name + stepWait(step),
      since: Number(step.stageStarted) || Number(step.started),
      step,
    };
  }
  return null;
}

// The wait a held or limited step shows after its name: held wins over limited, and a limit with no kind reads as a
// plain limit.
function stepWait(step: EpicStep): string {
  if (step.held) {
    return " held";
  }
  if (step.limitKind === "") {
    return " waits on a limit";
  }
  return ` waits on a ${step.limitKind} limit`;
}
