import { blockerLine, stateLine } from "./epicpage";
import type { Epic, EpicSlice, EpicStep, EpicsGetResponse, EpicTask } from "./gen/orrery/v1/epics_pb";
import { type CellKind, cellKind, RINGS, type StatusTone, taskReadout } from "./orbitcells";
import { durationText, provenOf, wedgesOf } from "./phases";
import type { Row, Section } from "./rows";
import { blockerOf } from "./status";
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
}

export interface EpicSection {
  title: string;
  epics: EpicBlock[];
  waiting: EpicItem[];
  rows: Row[];
  justEnded: EpicItem[];
  endedEarlier: EpicItem[];
  count: number;
}

// An epic block shows this many of its running steps; the rest are counted.
const shownSteps = 4;

// A task strip past this many segments draws dense, its segments touching.
export const denseStrip = 60;

const needsYouTitle = "Needs you";
const epicsTitle = "Epics";
const justEndedTitle = "Just ended";

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
  };
}

// Orders ended epics newest ending first. Equal endings keep their order, so an epic moved in goes after those it ties.
function newestFirst(items: EpicItem[]): EpicItem[] {
  return items.sort((a, b) => b.phaseAt - a.phaseAt);
}

// Keeps the ended epic under the cursor in Just ended: it moves from endedEarlier into justEnded, newest ending first.
// An epic that is not in endedEarlier leaves the view as it is.
export function keepUnderCursor(view: EpicsView, epicId: string): EpicsView {
  const item = view.endedEarlier.find((candidate) => candidate.id === epicId);
  if (item === undefined) {
    return view;
  }
  return {
    ...view,
    justEnded: newestFirst([...view.justEnded, item]),
    endedEarlier: view.endedEarlier.filter((candidate) => candidate.id !== epicId),
  };
}

// An ended epic's words: its outcome, then the time since it ended, never below zero when the browser's clock is behind
// the server's stamp. At phaseAt 0 there is no time to read, so the outcome stands alone.
export function endedWords(item: EpicItem, now: number): string {
  return item.phaseAt > 0 ? `${item.outcome} · ${durationText(Math.max(0, now - item.phaseAt))} ago` : item.outcome;
}

function blockOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicBlock {
  const slices = detail?.slices ?? [];
  const item = itemOf(epic, detail);
  const full = epic.runningSteps.length > 0;
  return {
    epic: item,
    full,
    steps: epic.runningSteps.slice(0, shownSteps).map((step) => stepOf(epic, step, slices)),
    more: Math.max(0, epic.runningSteps.length - shownSteps),
    tail: full ? tailOf(epic.endedSteps, item.strip, slices) : "",
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
    strip: stripOf(tasks, detail?.slices ?? []),
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
  return {
    title: section.title,
    epics: [],
    waiting: [],
    rows: section.rows,
    justEnded: [],
    endedEarlier: [],
    count: section.rows.length,
  };
}

// Wraps each section, then places the Epics group after the Needs you section, or first when there is none, and the
// Just ended section right after it. The Epics group counts every live epic and is left out when no epic is drawn or
// waiting. The Just ended section carries both ended lists and counts the unseen ones; it is left out when neither list
// holds an epic.
export function withEpicsGroup(sections: Section[], view: EpicsView): EpicSection[] {
  const result = sections.map(rowSection);
  const groups: EpicSection[] = [];
  if (view.blocks.length > 0 || view.waiting.length > 0) {
    groups.push({
      title: epicsTitle,
      epics: view.blocks,
      waiting: view.waiting,
      rows: [],
      justEnded: [],
      endedEarlier: [],
      count: view.live,
    });
  }
  if (view.justEnded.length > 0 || view.endedEarlier.length > 0) {
    groups.push({
      title: justEndedTitle,
      epics: [],
      waiting: [],
      rows: [],
      justEnded: view.justEnded,
      endedEarlier: view.endedEarlier,
      count: view.justEnded.length,
    });
  }
  const after = sections.findIndex((section) => section.title === needsYouTitle) + 1;
  result.splice(after, 0, ...groups);
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
    result.push({
      title: repository,
      epics,
      waiting: [],
      rows: [],
      justEnded: [],
      endedEarlier: [],
      count: epics.length,
    });
  }
  return result;
}

function lastPathElement(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}
