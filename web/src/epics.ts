import type { Epic, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { Row, Section } from "./rows";
import { taskStateOf } from "./taskstatus";

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
}

export interface EpicBlock {
  epic: EpicItem;
  steps: StepItem[];
  more: number;
  ended: number;
}

export interface EpicsView {
  blocks: EpicBlock[];
  folded: number;
  details: Map<string, EpicsGetResponse>;
}

export interface EpicSection {
  title: string;
  epics: EpicBlock[];
  rows: Row[];
  folded: number;
}

// An epic block shows this many of its running steps; the rest are counted.
const shownSteps = 4;

const needsYouTitle = "Needs you";
const epicsTitle = "Epics";

// A repository's record name ends in a dash and seven lowercase hex digits; the folder it is named for does not.
const hexSuffix = /-[0-9a-f]{7}$/;

export const noEpics: EpicsView = { blocks: [], folded: 0, details: new Map() };

// Blocks the live epics in input order and counts the folded ones. An epic with no detail reads as unknown,
// with a gauge of zero.
export function epicsView(epics: Epic[], details: Map<string, EpicsGetResponse>): EpicsView {
  const blocks: EpicBlock[] = [];
  let folded = 0;
  for (const epic of epics) {
    if (epic.live) {
      blocks.push(blockOf(epic, details.get(epic.id)));
    } else {
      folded++;
    }
  }
  return { blocks, folded, details };
}

function blockOf(epic: Epic, detail: EpicsGetResponse | undefined): EpicBlock {
  const tasks = detail?.tasks ?? [];
  return {
    epic: {
      kind: "epic",
      key: `epic:${epic.id}`,
      id: epic.id,
      slug: epic.slug,
      repository: epic.repository,
      outcome: epic.outcome,
      known: detail !== undefined,
      done: tasks.filter((task) => taskStateOf(task.status) === "done").length,
      total: tasks.length,
    },
    steps: epic.runningSteps.slice(0, shownSteps).map((step) => stepOf(epic, step)),
    more: Math.max(0, epic.runningSteps.length - shownSteps),
    ended: epic.endedSteps,
  };
}

function stepOf(epic: Epic, step: EpicStep): StepItem {
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
  };
}

// A step's round, per DL17: the highest round its checks reached, plus one while the step runs.
function roundOf(step: EpicStep): number {
  const highest = step.checks.reduce((max, check) => Math.max(max, check.round), 0);
  return step.end === "" ? highest + 1 : highest;
}

export function repositoryName(repository: string): string {
  return repository.replace(hexSuffix, "");
}

// Wraps each section, then places the Epics group after the Needs you section, or first when there is none.
// The group is left out when no epic is live and none is folded.
export function withEpicsGroup(sections: Section[], view: EpicsView): EpicSection[] {
  const result = sections.map(
    (section): EpicSection => ({ title: section.title, epics: [], rows: section.rows, folded: 0 }),
  );
  if (view.blocks.length === 0 && view.folded === 0) {
    return result;
  }
  const after = sections.findIndex((section) => section.title === needsYouTitle) + 1;
  result.splice(after, 0, { title: epicsTitle, epics: view.blocks, rows: [], folded: view.folded });
  return result;
}

// Puts each block atop the first section named for its repository. A block with no such section gets one
// section per repository, titled by it, after the others in first-seen order.
export function placeEpics(sections: Section[], blocks: EpicBlock[]): EpicSection[] {
  const result = sections.map(
    (section): EpicSection => ({ title: section.title, epics: [], rows: section.rows, folded: 0 }),
  );
  const unmatched = new Map<string, EpicBlock[]>();
  for (const block of blocks) {
    const name = repositoryName(block.epic.repository);
    const section = result.find((candidate) => lastPathElement(candidate.title) === name);
    if (section) {
      section.epics.push(block);
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
    result.push({ title: repository, epics, rows: [], folded: 0 });
  }
  return result;
}

function lastPathElement(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}
