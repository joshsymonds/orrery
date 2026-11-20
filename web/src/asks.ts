import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { Conversation } from "./conversation";
import { epicNeeds, type Need } from "./needs";
import { waitsOnYou, type Row } from "./rows";

// The way an ask line says its epic waits on the owner: a park is decided, a question is answered, any other need is opened.
export type AskWay = "Answer" | "Decide" | "Open";

// One steered epic that needs the owner: the epic's id and slug, the need's text without the slug, and its way.
export interface AskLine {
  epic: string;
  slug: string;
  text: string;
  way: AskWay;
}

// The epics a conversation steers, in listing order: live, unarchived epics whose steering is the conversation's id. An
// empty id steers nothing, since an epic with no steering has an empty steering.
export function steeredEpics(conversationId: string, listed: Iterable<Epic>): Epic[] {
  const steered: Epic[] = [];
  for (const epic of listed) {
    if (epic.live && !epic.archived && epic.steering !== "" && epic.steering === conversationId) {
      steered.push(epic);
    }
  }
  return steered;
}

// askLines gives each epic that has a need one ask line, in listing order. The needs are the inbox's needs for that epic
// alone. A park makes a Decide line, else a question an Answer line, else the epic's first need makes an Open line.
export function askLines(epics: readonly Epic[], details: ReadonlyMap<string, EpicsGetResponse>): AskLine[] {
  const lines: AskLine[] = [];
  for (const epic of epics) {
    const detail = details.get(epic.id);
    if (detail === undefined) {
      continue;
    }
    const line = lineOf(epic, epicNeeds([epic], new Map([[epic.id, detail]])));
    if (line !== null) {
      lines.push(line);
    }
  }
  return lines;
}

// lineOf is the ask line of an epic's needs, or null when it has none.
function lineOf(epic: Epic, needs: Need[]): AskLine | null {
  const chosen = needs.find((need) => need.kind === "park") ?? needs.find((need) => need.kind === "question") ?? needs[0];
  if (chosen === undefined) {
    return null;
  }
  const way: AskWay = chosen.kind === "park" ? "Decide" : chosen.kind === "question" ? "Answer" : "Open";
  return { epic: epic.id, slug: epic.slug, text: withoutSlug(chosen.text, epic.slug), way };
}

// withoutSlug removes the leading "<slug> " or "<slug>: " that epicNeeds gives every need text.
function withoutSlug(text: string, slug: string): string {
  const colon = `${slug}: `;
  if (text.startsWith(colon)) {
    return text.slice(colon.length);
  }
  return text.slice(slug.length + 1);
}

// ownAsks is whether the conversation's own question asks the owner: its row waits on the owner (waiting, not archived,
// not put off), and neither its pane nor its session has its question put off.
export function ownAsks(row: Row, conversation: Conversation | null): boolean {
  if (!waitsOnYou(row)) {
    return false;
  }
  const state = conversation?.state;
  return !state?.pane?.putOff && !state?.session?.putOff;
}

// askingWords is what the folded line says is asking: the count of asks, then the name of the one that asks, or the
// count with "ask you" when there are several. ownName is the conversation's name, or null when its own question does not
// ask.
export function askingWords(ownName: string | null, lines: readonly AskLine[]): string {
  const count = (ownName === null ? 0 : 1) + lines.length;
  if (count === 0) {
    return "";
  }
  if (count > 1) {
    return `${count} ask you`;
  }
  if (ownName !== null) {
    return `${ownName} asks`;
  }
  return `${lines[0].slug} asks`;
}

// A badge of the folded line. A count of 0 draws no number.
export interface Badge {
  key: string;
  label: string;
  count: number;
  asks: boolean;
}

// headBadges gives one badge per steered epic, labelled with the first part of its slug and marked when it has an ask
// line, then a badge for the running agents and one for the running shells, each only when it counts.
export function headBadges(epics: readonly Epic[], lines: readonly AskLine[], background: readonly BackgroundTask[]): Badge[] {
  const badges: Badge[] = epics.map((epic) => ({
    key: epic.id,
    label: epic.slug.split("-")[0],
    count: 0,
    asks: lines.some((line) => line.epic === epic.id),
  }));
  const agents = runningOf(background, "agent");
  if (agents > 0) {
    badges.push({ key: "agents", label: "agents", count: agents, asks: false });
  }
  const shells = runningOf(background, "shell");
  if (shells > 0) {
    badges.push({ key: "running", label: "running", count: shells, asks: false });
  }
  return badges;
}

// runningOf counts the background tasks of one type that are running.
function runningOf(background: readonly BackgroundTask[], type: string): number {
  return background.filter((task) => task.type === type && task.status === "running").length;
}
