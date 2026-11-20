import { blockerLine, stateLine } from "./epicpage";
import type { Epic, EpicSlice, EpicStep, EpicsGetResponse, EpicTask } from "./gen/orrery/v1/epics_pb";
import { type CellKind, cellKind, RINGS } from "./orbitcells";
import { durationText, placementOf, provenOf, recordTaskOf, type Tone, wedgesOf } from "./phases";
import type { Row } from "./rows";
import { blockerOf } from "./status";
import { taskStateOf } from "./taskstatus";

// A strip segment's tone: the tone its record task is placed at, over the epic's steps of every kind.
export type StripTone = "passed" | "accepted" | "now" | "held" | "errored" | "none";

// The strip tone each placement tone draws as.
const stripToneOf: Record<Tone, StripTone> = {
  ok: "passed",
  acc: "accepted",
  run: "now",
  mine: "held",
  limited: "held",
  bad: "errored",
  plan: "none",
};

export interface EpicItem {
  kind: "epic";
  key: string;
  id: string;
  slug: string;
  repository: string;
  outcome: string;
  phaseAt: number;
  done: number;
  total: number;
  strip: StripTone[];
  // Whether the owner archived the epic. An archived epic is set aside in EpicsView.archived, never drawn.
  archived: boolean;
  detail: EpicsGetResponse | undefined;
}

// An epic's line words: its count, its state and its blocker, each empty when it has nothing to say.
export interface EpicLine {
  count: string;
  state: string;
  blocker: string;
}

export interface StepItem {
  kind: "step";
  key: string;
  id: string;
  epic: string;
  epicSlug: string;
  label: string;
  doing: string;
  held: boolean;
  agent: string;
  started: number;
  round: number;
  pips: CellKind[];
  reason: string;
  step: EpicStep;
}

export interface EpicBlock {
  epic: EpicItem;
  full: boolean;
  steps: StepItem[];
  more: number;
  tail: string;
}

export interface EpicsView {
  blocks: EpicBlock[];
  waiting: EpicItem[];
  live: number;
  justEnded: EpicItem[];
  endedEarlier: EpicItem[];
  // The archived epics, live or not, in input order.
  archived: EpicItem[];
  details: Map<string, EpicsGetResponse>;
  // The Epics.List message of each listed epic, live or not, by id.
  listed: Map<string, Epic>;
}

// An epic block shows this many of its running steps; the rest are counted.
const shownSteps = 4;

// A repository's record name ends in a dash and seven lowercase hex digits; the folder it is named for does not.
const hexSuffix = /-[0-9a-f]{7}$/;

export const noEpics: EpicsView = {
  blocks: [],
  waiting: [],
  live: 0,
  justEnded: [],
  endedEarlier: [],
  archived: [],
  details: new Map(),
  listed: new Map(),
};

// Blocks every live epic that is not waiting, busy ones first and each group in input order, and collects the live
// epics waiting to start in input order. Each epic that is not live has ended: it sorts into justEnded while it is
// unseen, else into endedEarlier, both newest ending first. A record epic has no phase time, so it is never unseen. An
// archived epic, live or not, goes into archived in input order and into none of the other lists, nor the live count.
// An epic with no detail carries no detail, and its counts read zero.
export function epicsView(epics: Epic[], details: Map<string, EpicsGetResponse>): EpicsView {
  const busy: EpicBlock[] = [];
  const quiet: EpicBlock[] = [];
  const waiting: EpicItem[] = [];
  const justEnded: EpicItem[] = [];
  const endedEarlier: EpicItem[] = [];
  const archived: EpicItem[] = [];
  let live = 0;
  for (const epic of epics) {
    if (epic.archived) {
      archived.push(itemOf(epic, details.get(epic.id)));
      continue;
    }
    if (!epic.live) {
      const item = itemOf(epic, details.get(epic.id));
      if (epic.seen < epic.phaseAt) {
        justEnded.push(item);
      } else {
        endedEarlier.push(item);
      }
      continue;
    }
    live++;
    const block = blockOf(epic, details.get(epic.id));
    if (epic.waiting) {
      waiting.push(block.epic);
    } else if (block.full) {
      busy.push(block);
    } else {
      quiet.push(block);
    }
  }
  return {
    blocks: [...busy, ...quiet],
    waiting,
    live,
    justEnded: newestFirst(justEnded),
    endedEarlier: newestFirst(endedEarlier),
    archived,
    details,
    listed: new Map(epics.map((epic) => [epic.id, epic])),
  };
}

// Orders ended epics newest ending first. Equal endings keep their order, so an epic moved in goes after those it ties.
function newestFirst(items: EpicItem[]): EpicItem[] {
  return items.sort((a, b) => b.phaseAt - a.phaseAt);
}

// An ended epic's words: its outcome, then the time since it ended, never below zero when the browser's clock is behind
// the server's stamp. At phaseAt 0 there is no time to read, so the outcome stands alone.
export function endedWords(item: EpicItem, now: number): string {
  return item.phaseAt > 0 ? `${item.outcome} · ${durationText(Math.max(0, now - item.phaseAt))} ago` : item.outcome;
}

export function blockOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicBlock {
  const slices = detail?.slices ?? [];
  const item = itemOf(epic, detail);
  const full = epic.runningSteps.length > 0;
  return {
    epic: item,
    full,
    steps: epic.runningSteps.slice(0, shownSteps).map((step) => stepOf(epic, step, slices)),
    more: Math.max(0, epic.runningSteps.length - shownSteps),
    tail: full ? tailOf(epic.endedSteps, item.strip, detail) : "",
  };
}

// An epic's line: its identity, its task counts and strip from the detail when one is held, and the detail.
function itemOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicItem {
  const tasks = detail?.tasks ?? [];
  return {
    kind: "epic",
    key: `epic:${epic.id}`,
    id: epic.id,
    slug: epic.slug,
    repository: epic.repository,
    outcome: epic.outcome,
    phaseAt: Number(epic.phaseAt),
    done: doneOf(tasks),
    total: tasks.length,
    strip: stripOf(tasks, detail?.steps ?? []),
    archived: epic.archived,
    detail,
  };
}

// An epic's line words from its detail: the count, the state with the units integrated, and the blocker. An epic with
// no detail reads its count as unavailable, and its state and blocker stay empty.
export function epicLine(item: EpicItem, now: number): EpicLine {
  const detail = item.detail;
  if (detail === undefined) {
    return { count: "epic unavailable", state: "", blocker: "" };
  }
  return {
    count: countOf(item, detail),
    state: stateWords(detail, now),
    blocker: blockerLine(blockerOf(detail), now) ?? "",
  };
}

// The count: the requirements proven, as the orrery's arcs prove them, out of all of them, else the record tasks done.
function countOf(item: EpicItem, detail: EpicsGetResponse): string {
  const requirements = detail.requirements;
  if (requirements.length > 0) {
    const wedges = wedgesOf(detail);
    const proven = requirements.filter((requirement) => provenOf(requirement.id, wedges, requirements)).length;
    return `${proven} of ${requirements.length} proven`;
  }
  return `${item.done} of ${item.total} tasks done`;
}

// The state: the title row's state phrase, then the units integrated when the epic has any units.
function stateWords(detail: EpicsGetResponse, now: number): string {
  const wedges = wedgesOf(detail);
  const integrated = wedges.filter((wedge) => wedge.place === "integrated").length;
  const units = wedges.length > 0 ? `${integrated} of ${wedges.length} units integrated` : "";
  return [stateLine(detail, now), units].filter((part) => part !== "").join(" · ");
}

// The record tasks whose status is done.
export function doneOf(tasks: EpicTask[]): number {
  return tasks.filter((task) => taskStateOf(task.status) === "done").length;
}

// One tone per record task, in record order: the strip tone its placement takes over the epic's steps of every kind,
// the same placement the orrery draws its wedge at.
export function stripOf(tasks: EpicTask[], steps: EpicStep[]): StripTone[] {
  return tasks.map((record) => {
    const task = recordTaskOf(record);
    return stripToneOf[placementOf(task, steps.filter((step) => step.task === task.task)).tone];
  });
}

// A full block's tail: its gap wedges, the record tasks not started, and its ended steps, each left out at zero. A gap
// is a wedge the orrery draws bad, so the count matches its glyph, whether or not the epic has record tasks.
function tailOf(ended: number, strip: StripTone[], detail: EpicsGetResponse | undefined): string {
  const gaps = detail === undefined ? 0 : wedgesOf(detail).filter((wedge) => wedge.tone === "bad").length;
  const notStarted = strip.filter((tone) => tone === "none").length;
  return [
    gaps > 0 ? `${gaps} gap` : "",
    notStarted > 0 ? `${notStarted} not started` : "",
    ended > 0 ? `${ended} steps ended` : "",
  ]
    .filter((part) => part !== "")
    .join(" · ");
}

function stepOf(epic: Epic, step: EpicStep, slices: EpicSlice[]): StepItem {
  return {
    kind: "step",
    key: `step:${step.id}`,
    id: step.id,
    epic: epic.id,
    epicSlug: epic.slug,
    label: step.task || step.agent,
    doing: step.doing,
    held: step.held,
    agent: step.agent,
    started: Number(step.started),
    round: roundOf(step),
    pips: pipsOf(step.task, slices),
    reason: step.reason,
    step,
  };
}

// A step's five cells: the cells of the slice that names its task, or five track when no slice does.
export function pipsOf(task: string, slices: EpicSlice[]): CellKind[] {
  const slice = slices.find((candidate) => candidate.task === task);
  return RINGS.map((_, index) => (slice ? cellKind(slice, index + 1) : "track"));
}

// A step's round, per DL17: the highest round its checks reached, plus one while the step runs.
export function roundOf(step: EpicStep): number {
  const highest = step.checks.reduce((max, check) => Math.max(max, check.round), 0);
  return step.end === "" ? highest + 1 : highest;
}

export function repositoryName(repository: string): string {
  return repository.replace(hexSuffix, "");
}
