import type { EpicRequirement, EpicsGetResponse, EpicStep, EpicTask } from "./gen/orrery/v1/epics_pb";
import { taskStateOf } from "./taskstatus";

export const PHASES = ["implementing", "checking", "reviewing", "integrating"] as const;
export type Phase = (typeof PHASES)[number];
export type Place = "planned" | Phase | "integrated";
export type Tone = "plan" | "run" | "mine" | "limited" | "bad" | "acc" | "ok";
export type SliceState = "passed" | "now" | "ahead";

export interface PhaseSlice {
  phase: Phase;
  state: SliceState;
  summary: string;
  since: number;
  seconds: number;
}

export interface Wedge {
  task: string;
  title: string;
  requirements: string[];
  place: Place;
  tone: Tone;
  slices: PhaseSlice[];
}

// A task as a wedge reads it: a planned build task, or a record task, which has no stage.
export interface Task {
  task: string;
  title: string;
  status: string;
  stage: string;
  requirements: string[];
}

// The build tasks when the epic has any, else the record's tasks.
export function tasksOf(detail: EpicsGetResponse): Task[] {
  if (detail.buildTasks.length > 0) {
    return detail.buildTasks.map((build) => ({
      task: build.id,
      title: build.title,
      status: build.status,
      stage: build.stage,
      requirements: build.requirements,
    }));
  }
  return detail.tasks.map(recordTaskOf);
}

// A record task as a wedge reads it: its slug names it, and it has no stage.
export function recordTaskOf(record: EpicTask): Task {
  return {
    task: record.slug,
    title: record.subject,
    status: record.status,
    stage: "",
    requirements: record.requirements,
  };
}

// The stages whose running step is checking.
const CHECK_STAGES: readonly string[] = ["checks", "gate", "judge"];

// The phase a step is in.
function phaseOf(step: EpicStep): Phase {
  if (step.readOnly) {
    return "reviewing";
  }
  if ((step.end === "" && CHECK_STAGES.includes(step.stage)) || step.end === "checks_failed" || step.end === "goal_unmet") {
    return "checking";
  }
  return "implementing";
}

interface Placement {
  place: Place;
  tone: Tone;
}

// The tone of a running step: held wins over limited, and a limited step's task is grey, not peach.
function toneOfRunning(step: EpicStep): Tone {
  if (step.held) {
    return "mine";
  }
  return step.limited ? "limited" : "run";
}

// The place and tone of a task, by the first rule that matches.
export function placementOf(task: Task, steps: EpicStep[]): Placement {
  if (taskStateOf(task.status) === "done") {
    return { place: "integrated", tone: "ok" };
  }
  const latest = steps.at(-1);
  if (latest !== undefined && latest.end === "") {
    return { place: phaseOf(latest), tone: toneOfRunning(latest) };
  }
  if (latest !== undefined && latest.end === "done") {
    return { place: "integrating", tone: "acc" };
  }
  if (latest !== undefined) {
    return { place: phaseOf(latest), tone: "bad" };
  }
  switch (task.stage) {
    case "red":
    case "implement":
    case "orchestrate":
      return { place: "implementing", tone: "run" };
    case "green":
      return { place: "checking", tone: "run" };
    case "review":
    case "verify":
      return { place: "reviewing", tone: "run" };
  }
  switch (taskStateOf(task.status)) {
    case "dispatched":
      return { place: "implementing", tone: "run" };
    case "gated":
      return { place: "integrating", tone: "acc" };
    case "gap":
      return { place: "implementing", tone: "bad" };
  }
  return { place: "planned", tone: "plan" };
}

// The index of a place among PHASES: -1 before the first phase, and the count of phases once integrated.
function reachedOf(place: Place): number {
  if (place === "planned") {
    return -1;
  }
  if (place === "integrated") {
    return PHASES.length;
  }
  return PHASES.indexOf(place);
}

function stateOf(index: number, reached: number): SliceState {
  if (index < reached) {
    return "passed";
  }
  if (index === reached) {
    return "now";
  }
  return "ahead";
}

// The summary of a phase's latest step: doing while the step runs.
function summaryOf(latest: EpicStep | undefined): string {
  if (latest === undefined) {
    return "";
  }
  return latest.end === "" ? latest.doing : latest.summary;
}

// The time a now slice has been in its phase: from the stage start of its running step, else from its start.
function sinceOf(latest: EpicStep | undefined): number {
  if (latest === undefined) {
    return 0;
  }
  return Number(latest.end === "" ? latest.stageStarted : latest.started);
}

// The time a phase's ended steps took, in seconds.
function secondsOf(steps: EpicStep[]): number {
  return steps
    .filter((step) => step.end !== "")
    .reduce((sum, step) => sum + Number(step.ended - step.started), 0);
}

function slicesOf(place: Place, steps: EpicStep[]): PhaseSlice[] {
  const reached = reachedOf(place);
  return PHASES.map((phase, index) => {
    const ofPhase = steps.filter((step) => phaseOf(step) === phase);
    const latest = ofPhase.at(-1);
    const state = stateOf(index, reached);
    return {
      phase,
      state,
      summary: summaryOf(latest),
      since: state === "now" ? sinceOf(latest) : 0,
      seconds: state === "passed" ? secondsOf(ofPhase) : 0,
    };
  });
}

// The wedges of an epic, one per task, in task order.
export function wedgesOf(detail: EpicsGetResponse): Wedge[] {
  return tasksOf(detail).map((task) => {
    const steps = detail.steps.filter((step) => step.task === task.task);
    const { place, tone } = placementOf(task, steps);
    return {
      task: task.task,
      title: task.title,
      requirements: task.requirements,
      place,
      tone,
      slices: slicesOf(place, steps),
    };
  });
}

// Whether a requirement is proven: its own green run passed, or, with no requirement of that id, every wedge naming
// it is integrated.
export function provenOf(id: string, wedges: Wedge[], requirements: EpicRequirement[]): boolean {
  const requirement = requirements.find((candidate) => candidate.id === id);
  if (requirement !== undefined) {
    return requirement.runs.some((run) => run.phase === "green" && run.passed);
  }
  return wedges.filter((wedge) => wedge.requirements.includes(id)).every((wedge) => wedge.place === "integrated");
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
