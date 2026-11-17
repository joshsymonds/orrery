import type { Epic, EpicQuestion, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The one blocker an epic waits on: its text, and the unix second it began. A question carries the question, a held
// or limited step carries the step.
export type Blocker =
  | { kind: "question"; text: string; since: number; question: EpicQuestion }
  | { kind: "parked"; text: string; since: number }
  | { kind: "held"; text: string; since: number; step: EpicStep };

// The state word of an epic. An orreryd epic, whose outcome is its phase, reads paused, parked, waiting, then the part
// of its track its phase is in, and the phase as written off the track. A record epic reads its outcome as written,
// then paused, parked, waiting, then planning or building while it is live. A missing epic and an epic with no state
// give the empty string.
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

// orrerydStateOf is the state word of an orreryd epic: paused, parked, waiting, then its track part or its phase.
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
  return trackWord(detail, epic.outcome) ?? epic.outcome;
}

// trackWord is the track part of an orreryd epic in its phase: planning or building while accepted, building while
// built, reviewing or correcting, releasing while clean or releasing. Undefined for a phase off the track.
function trackWord(detail: EpicsGetResponse, phase: string): string | undefined {
  switch (phase) {
    case "accepted":
      return planningOrBuilding(detail);
    case "built":
    case "reviewing":
    case "correcting":
      return "building";
    case "clean":
    case "releasing":
      return "releasing";
    default:
      return undefined;
  }
}

// planningOrBuilding is planning while the epic has no task, and building once it has one.
function planningOrBuilding(detail: EpicsGetResponse): string {
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
      stop: trackWord(detail, epic.outcome) !== undefined,
    };
  }
  const open = epic.outcome === "";
  return { pause: open && !detail.paused, resume: open && detail.paused, stop: open };
}

// The blocker an epic waits on, first match: its first open question, its park, then its first step that has not
// ended and is held or limited. Null when none of them holds.
export function blockerOf(detail: EpicsGetResponse): Blocker | null {
  const question = detail.questions[0];
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
