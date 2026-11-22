import type { EpicsGetResponse, EpicStep } from "./gen/orrery/v1/epics_pb";
import { type Cell, PLACES, type Phase, type TaskPlace, taskOfLabel, tasksOf, type Tone } from "./phases";

// A task as the record reads it: its place among the rings and its tone there, as the server stands it, its five
// cells and its words, and the labels of its steps.
export interface RecordTask {
  task: string;
  title: string;
  requirements: string[];
  place: TaskPlace;
  tone: Tone;
  cells: Cell[];
  words: string;
  since: number;
  labels: string[];
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

// The running step of a task: its latest step that has not ended, as the server reads it for the task's tone.
function runningOf(steps: EpicStep[]): EpicStep | undefined {
  return steps.filter((step) => step.end === "").at(-1);
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

// The record tasks of an epic, in task order: the build tasks when there are any, else the record's tasks. Each stands
// where the server stands it; its running step is read from the steps its labels name.
export function recordTasksOf(detail: EpicsGetResponse): RecordTask[] {
  return tasksOf(detail).map((task) => {
    const running = runningOf(detail.steps.filter((step) => task.labels.includes(step.task)));
    return {
      task: task.task,
      title: task.title,
      requirements: task.requirements,
      place: task.place,
      tone: task.tone,
      cells: cellsOf(task.place, task.tone),
      words: wordsOf(task.place, task.tone, running),
      since: running === undefined ? 0 : sinceOf(running),
      labels: task.labels,
    };
  });
}

// A step's five cells: the cells of the task its label names, a build task's evidence judge naming it by its run, or
// five none when no task of the epic is its.
export function pipsOf(label: string, tasks: RecordTask[]): Cell[] {
  return taskOfLabel(tasks, label)?.cells ?? PLACES.map(() => "none");
}
