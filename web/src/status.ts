import type { EpicQuestion, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// The one blocker an epic waits on: its text, and the unix second it began. A question carries the question, a held
// or limited step carries the step.
export type Blocker =
  | { kind: "question"; text: string; since: number; question: EpicQuestion }
  | { kind: "parked"; text: string; since: number }
  | { kind: "held"; text: string; since: number; step: EpicStep };

// The state word of an epic, first match: its outcome as written, paused, parked, waiting, then planning or building
// while it is live. A missing epic and an epic with no state give the empty string.
export function stateOf(detail: EpicsGetResponse): string {
  const epic = detail.epic;
  if (!epic) {
    return "";
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
    return detail.tasks.length === 0 && detail.buildTasks.length === 0 ? "planning" : "building";
  }
  return "";
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
