import type { Epic, EpicSlice, EpicStep, EpicsGetResponse, EpicTask } from "./gen/orrery/v1/epics_pb";
import { type CellKind, cellKind, RINGS, type StatusTone, taskReadout } from "./orbitcells";
import type { Row, Section } from "./rows";
import { taskStateOf } from "./taskstatus";

// A strip segment's tone: its record task's status tone, or none when no slice names the task.
export type StripTone = StatusTone | "none";

export interface EpicItem {
  kind: "epic";
  key: string;
  id: string;
  slug: string;
  repository: string;
  outcome: string;
  known: boolean;
  done: number;
  total: number;
  strip: StripTone[];
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
  live: number;
  folded: number;
  details: Map<string, EpicsGetResponse>;
}

export interface EpicSection {
  title: string;
  epics: EpicBlock[];
  rows: Row[];
  folded: number;
  count: number;
}

// An epic block shows this many of its running steps; the rest are counted.
const shownSteps = 4;

// Quiet epics draw one line each until this many blocks are drawn. Epics with running or held work always draw.
const shownBlocks = 6;

// A task strip past this many segments draws dense, its segments touching.
export const denseStrip = 60;

const needsYouTitle = "Needs you";
const epicsTitle = "Epics";

// A repository's record name ends in a dash and seven lowercase hex digits; the folder it is named for does not.
const hexSuffix = /-[0-9a-f]{7}$/;

export const noEpics: EpicsView = { blocks: [], live: 0, folded: 0, details: new Map() };

// Blocks the live epics that draw, and counts every other epic as folded. Epics with a running or held step draw first,
// in input order; quiet live epics follow in input order while fewer than shownBlocks are drawn. An epic with no detail
// reads as unknown, with a gauge of zero.
export function epicsView(epics: Epic[], details: Map<string, EpicsGetResponse>): EpicsView {
  const busy: EpicBlock[] = [];
  const quiet: EpicBlock[] = [];
  let live = 0;
  for (const epic of epics) {
    if (!epic.live) {
      continue;
    }
    live++;
    const block = blockOf(epic, details.get(epic.id));
    if (block.full) {
      busy.push(block);
    } else {
      quiet.push(block);
    }
  }
  const blocks = [...busy, ...quiet.slice(0, Math.max(0, shownBlocks - busy.length))];
  return { blocks, live, folded: epics.length - blocks.length, details };
}

function blockOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicBlock {
  const tasks = detail?.tasks ?? [];
  const slices = detail?.slices ?? [];
  const strip = stripOf(tasks, slices);
  const full = epic.runningSteps.length > 0;
  return {
    epic: {
      kind: "epic",
      key: `epic:${epic.id}`,
      id: epic.id,
      slug: epic.slug,
      repository: epic.repository,
      outcome: epic.outcome,
      known: detail !== undefined,
      done: doneOf(tasks),
      total: tasks.length,
      strip,
    },
    full,
    steps: epic.runningSteps.slice(0, shownSteps).map((step) => stepOf(epic, step, slices)),
    more: Math.max(0, epic.runningSteps.length - shownSteps),
    tail: full ? tailOf(epic.endedSteps, strip, slices) : "",
  };
}

// The record tasks whose status is done.
export function doneOf(tasks: EpicTask[]): number {
  return tasks.filter((task) => taskStateOf(task.status) === "done").length;
}

// One tone per record task, in record order: the tone of the slice that names the task, or none when none does.
export function stripOf(tasks: EpicTask[], slices: EpicSlice[]): StripTone[] {
  return tasks.map((task) => {
    const index = slices.findIndex((slice) => slice.task === task.slug);
    return index === -1 ? "none" : taskReadout(slices, index).tone;
  });
}

// A full block's tail: its gap slices, the record tasks no slice names, and its ended steps, each left out at zero.
function tailOf(ended: number, strip: StripTone[], slices: EpicSlice[]): string {
  const gaps = slices.filter((slice) => slice.hue === "gap").length;
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

// A row section, counting its rows.
function rowSection(section: Section): EpicSection {
  return { title: section.title, epics: [], rows: section.rows, folded: 0, count: section.rows.length };
}

// Wraps each section, then places the Epics group after the Needs you section, or first when there is none. The group
// counts every live epic and is left out when no epic is live and none is folded.
export function withEpicsGroup(sections: Section[], view: EpicsView): EpicSection[] {
  const result = sections.map(rowSection);
  if (view.blocks.length === 0 && view.folded === 0) {
    return result;
  }
  const after = sections.findIndex((section) => section.title === needsYouTitle) + 1;
  result.splice(after, 0, { title: epicsTitle, epics: view.blocks, rows: [], folded: view.folded, count: view.live });
  return result;
}

// Puts each block atop the first section named for its repository. A block with no such section gets one
// section per repository, titled by it, after the others in first-seen order.
export function placeEpics(sections: Section[], blocks: EpicBlock[]): EpicSection[] {
  const result = sections.map(rowSection);
  const unmatched = new Map<string, EpicBlock[]>();
  for (const block of blocks) {
    const name = repositoryName(block.epic.repository);
    const section = result.find((candidate) => lastPathElement(candidate.title) === name);
    if (section) {
      section.epics.push(block);
      section.count++;
      continue;
    }
    const group = unmatched.get(block.epic.repository);
    if (group) {
      group.push(block);
    } else {
      unmatched.set(block.epic.repository, [block]);
    }
  }
  for (const [repository, epics] of unmatched) {
    result.push({ title: repository, epics, rows: [], folded: 0, count: epics.length });
  }
  return result;
}

function lastPathElement(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}
