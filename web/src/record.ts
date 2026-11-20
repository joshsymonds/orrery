import type { EpicsGetResponse, EpicStep } from "./gen/orrery/v1/epics_pb";
import { type Phase, placementOf, type Task, tasksOf, type Tone } from "./phases";

// The five rings of the chart, outside in.
export const PLACES = ["red", "implement", "green", "review", "integrate"] as const;
export type RingPlace = (typeof PLACES)[number];
export type TaskPlace = RingPlace | "planned" | "integrated";
export type Cell = Tone | "none";

// A task as the record reads it: its place among the rings, its five cells and its words.
export interface RecordTask {
  task: string;
  title: string;
  requirements: string[];
  place: TaskPlace;
  tone: Tone;
  cells: Cell[];
  words: string;
  since: number;
}

// The ring a build task's stage places it in.
const STAGE_PLACES: ReadonlyMap<string, RingPlace> = new Map<string, RingPlace>([
  ["red", "red"],
  ["implement", "implement"],
  ["orchestrate", "implement"],
  ["green", "green"],
  ["review", "review"],
  ["verify", "review"],
]);

// The ring a phase places a task in when its build stage does not.
const PHASE_PLACES: Record<Exclude<Phase, "integrating">, RingPlace> = {
  implementing: "implement",
  checking: "green",
  reviewing: "review",
};

// A task's place: its phase's place when that is integrated, integrating or planned, else its build stage's ring, else
// the ring of its phase.
function placeOf(task: Task, steps: EpicStep[]): { place: TaskPlace; tone: Tone } {
  const phased = placementOf(task, steps);
  if (phased.place === "integrated" || phased.place === "planned") {
    return { place: phased.place, tone: phased.tone };
  }
  if (phased.place === "integrating") {
    return { place: "integrate", tone: phased.tone };
  }
  return { place: STAGE_PLACES.get(task.stage) ?? PHASE_PLACES[phased.place], tone: phased.tone };
}

// The five cells of a task, outside in: ok before its place, its tone at it, none after; all ok when integrated and all
// none when planned.
export function cellsOf(place: TaskPlace, tone: Tone): Cell[] {
  if (place === "integrated") {
    return PLACES.map(() => "ok");
  }
  if (place === "planned") {
    return PLACES.map(() => "none");
  }
  const at = PLACES.indexOf(place);
  return PLACES.map((_, index) => (index < at ? "ok" : index === at ? tone : "none"));
}

// The phase a place is in, which the drill-down opens; none before the first ring.
export function phaseOfPlace(place: TaskPlace): Phase | undefined {
  switch (place) {
    case "red":
    case "implement":
      return "implementing";
    case "green":
      return "checking";
    case "review":
      return "reviewing";
    case "integrate":
    case "integrated":
      return "integrating";
    case "planned":
      return undefined;
  }
}

// The running step of a task: its latest step, when that step has not ended.
function runningOf(steps: EpicStep[]): EpicStep | undefined {
  const latest = steps.at(-1);
  return latest !== undefined && latest.end === "" ? latest : undefined;
}

// The words of a task in the task table's now column.
function wordsOf(place: TaskPlace, tone: Tone, running: EpicStep | undefined): string {
  if (place === "planned") {
    return "not started";
  }
  if (place === "integrated") {
    return "done";
  }
  if (tone === "acc") {
    return "accepted · waits to integrate";
  }
  if (tone === "limited") {
    return running?.limitKind ? `waits on the ${running.limitKind} limit` : "waits on a usage limit";
  }
  if (tone === "mine") {
    return `${place} · held`;
  }
  if (tone === "bad") {
    return `${place} · failed`;
  }
  return running?.doing ? `${place} · ${running.doing}` : place;
}

// The time a running step has been in its stage: from its stage start, else from its start, in unix seconds.
function sinceOf(step: EpicStep): number {
  return Number(step.stageStarted !== 0n ? step.stageStarted : step.started);
}

// The record tasks of an epic, in task order: the build tasks when there are any, else the record's tasks.
export function recordTasksOf(detail: EpicsGetResponse): RecordTask[] {
  return tasksOf(detail).map((task) => {
    const steps = detail.steps.filter((step) => step.task === task.task);
    const { place, tone } = placeOf(task, steps);
    const running = runningOf(steps);
    return {
      task: task.task,
      title: task.title,
      requirements: task.requirements,
      place,
      tone,
      cells: cellsOf(place, tone),
      words: wordsOf(place, tone, running),
      since: running === undefined ? 0 : sinceOf(running),
    };
  });
}
