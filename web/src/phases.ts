import type { EpicRequirement, EpicsGetResponse, EvidenceRun } from "./gen/orrery/v1/epics_pb";

export const PHASES = ["implementing", "checking", "reviewing", "integrating"] as const;
export type Phase = (typeof PHASES)[number];
export type Tone = "plan" | "run" | "mine" | "limited" | "bad" | "acc" | "ok";

// The five rings of a task's lifecycle, outside in, and the places it stands at: planned before the first ring, one of
// the rings, or integrated past the last.
export const PLACES = ["red", "implement", "green", "review", "integrate"] as const;
export type RingPlace = (typeof PLACES)[number];
export type TaskPlace = RingPlace | "planned" | "integrated";
export type Cell = Tone | "none";
export type SliceState = "passed" | "now" | "ahead";

// A task as the record reads it: a planned build task. place and tone are where the server stands it in its lifecycle,
// and labels are the task labels of its steps.
export interface Task {
  task: string;
  title: string;
  status: string;
  stage: string;
  requirements: string[];
  place: TaskPlace;
  tone: Tone;
  labels: string[];
}

// The places a task's lifecycle has, as the server names them.
const TASK_PLACES: readonly string[] = ["planned", ...PLACES, "integrated"];

// The tones a task can stand in, as the server names them.
const TONES: readonly string[] = ["plan", "run", "mine", "limited", "bad", "acc", "ok"];

// The task place the server sent, read as planned when it names no place this page knows.
function placeOf(place: string): TaskPlace {
  return TASK_PLACES.includes(place) ? (place as TaskPlace) : "planned";
}

// The tone the server sent, read as plan when it names no tone this page knows.
function toneOf(tone: string): Tone {
  return TONES.includes(tone) ? (tone as Tone) : "plan";
}

// The label an evidence run's judge step carries: the run's id after evidence-.
function evidenceLabel(run: EvidenceRun | undefined): string[] {
  return run === undefined || run.id === "" ? [] : [`evidence-${run.id}`];
}

// The build tasks of the epic, in task order. A build task's steps are labelled with its id, and the judge steps of its
// red and green runs with their evidence labels.
export function tasksOf(detail: EpicsGetResponse): Task[] {
  return detail.buildTasks.map((build) => ({
    task: build.id,
    title: build.title,
    status: build.status,
    stage: build.stage,
    requirements: build.requirements,
    place: placeOf(build.place),
    tone: toneOf(build.tone),
    labels: [build.id, ...evidenceLabel(build.red), ...evidenceLabel(build.green)],
  }));
}

// The task of an epic whose steps a step's label names, if any.
export function taskOfLabel<T extends { labels: string[] }>(tasks: T[], label: string): T | undefined {
  return label === "" ? undefined : tasks.find((task) => task.labels.includes(label));
}

// Whether a requirement is proven: its own green run passed, or, with no requirement of that id, every task naming it
// is integrated.
export function provenOf(
  id: string,
  tasks: { requirements: string[]; place: TaskPlace }[],
  requirements: EpicRequirement[],
): boolean {
  const requirement = requirements.find((candidate) => candidate.id === id);
  if (requirement !== undefined) {
    return requirement.runs.some((run) => run.phase === "green" && run.passed);
  }
  return tasks.filter((task) => task.requirements.includes(id)).every((task) => task.place === "integrated");
}

// A duration as text: seconds under a minute, whole minutes under an hour, then hours with any minutes left over.
export function durationText(seconds: number): string {
  if (seconds < 60) {
    return `${Math.floor(seconds)} s`;
  }
  if (seconds < 3600) {
    return `${Math.floor(seconds / 60)} min`;
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return minutes === 0 ? `${hours} h` : `${hours} h ${minutes} min`;
}
