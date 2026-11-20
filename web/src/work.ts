import type { Epic } from "./gen/orrery/v1/epics_pb";
import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";
import { type EpicItem, type EpicsView, endedWords, epicLine, repositoryName } from "./epics";
import { type Need, epicNeeds } from "./needs";

export type ThingTone = "attention" | "live" | "quiet";

export interface Gauge {
  done: number;
  started: number;
  total: number;
}

export interface EpicThing {
  kind: "epic";
  key: string;
  id: string;
  slug: string;
  short: string;
  asks: boolean;
  tone: ThingTone;
  state: string;
  gauge: Gauge;
}

export interface ListThing {
  kind: "agents" | "running";
  key: "agents" | "running";
  count: number;
  tone: ThingTone;
  tasks: BackgroundTask[];
}

export type Thing = EpicThing | ListThing;

export interface RailGroup {
  kind: "epic" | "agents" | "running";
  label: string;
  things: Thing[];
  many: boolean;
}

export interface WorkConversation {
  session: string;
  pane: boolean;
  background: BackgroundTask[];
}

type ListKind = "agents" | "running";

// A group with more than this many things is marked many.
const manyThings = 3;

// workOf gives a conversation's right-rail groups, in order: the epics it steers, its running sub-agents and its
// running shells. An epic is steered when its record names the conversation's session, and a session that is empty
// steers none. A pane has no lists. A group with no thing is left out.
export function workOf(conversation: WorkConversation | null, epics: EpicsView, now: number): RailGroup[] {
  if (conversation === null) {
    return [];
  }
  const groups: RailGroup[] = [];
  const epicThings = epicThingsOf(conversation.session, epics, now);
  if (epicThings.length > 0) {
    groups.push(groupOf("epic", epicThings.length === 1 ? "epic" : "epics", epicThings));
  }
  if (!conversation.pane) {
    const agents = running(conversation.background, "agent");
    if (agents.length > 0) {
      groups.push(listGroup("agents", agents, "live"));
    }
    const shells = running(conversation.background, "shell");
    if (shells.length > 0) {
      groups.push(listGroup("running", shells, "quiet"));
    }
  }
  return groups;
}

// shortName is the slug past its repository's name and the dash after it, up to the first dash after that.
export function shortName(slug: string, repository: string): string {
  const prefix = `${repositoryName(repository)}-`;
  const rest = slug.startsWith(prefix) ? slug.slice(prefix.length) : slug;
  return rest.split("-")[0];
}

// The epics the session steers, in the rail's order: the blocks, then the waiting epics, then the just ended ones.
function epicThingsOf(session: string, epics: EpicsView, now: number): EpicThing[] {
  const things: EpicThing[] = [];
  const items: [EpicItem, boolean][] = [
    ...epics.blocks.map((block): [EpicItem, boolean] => [block.epic, false]),
    ...epics.waiting.map((item): [EpicItem, boolean] => [item, false]),
    ...epics.justEnded.map((item): [EpicItem, boolean] => [item, true]),
  ];
  for (const [item, ended] of items) {
    const listed = epics.listed.get(item.id);
    if (listed !== undefined && session !== "" && listed.steering === session) {
      things.push(epicThingOf(item, listed, epics, ended, now));
    }
  }
  return things;
}

function epicThingOf(item: EpicItem, listed: Epic, epics: EpicsView, ended: boolean, now: number): EpicThing {
  const needs = epicNeeds([listed], epics.details);
  const asks = needs.length > 0;
  return {
    kind: "epic",
    key: item.key,
    id: item.id,
    slug: item.slug,
    short: shortName(item.slug, item.repository),
    asks,
    tone: toneOf(asks, listed),
    state: stateOf(item, needs, ended, now),
    gauge: {
      done: item.done,
      started: item.strip.filter((tone) => tone === "now").length,
      total: item.total,
    },
  };
}

// An epic asks when a need is open, and is live while it has a running step.
function toneOf(asks: boolean, listed: Epic): ThingTone {
  if (asks) {
    return "attention";
  }
  return listed.runningSteps.length > 0 ? "live" : "quiet";
}

// An epic asking reads its first need, a just ended epic reads its ending, and any other reads its line's state.
function stateOf(item: EpicItem, needs: Need[], ended: boolean, now: number): string {
  const [first] = needs;
  if (first !== undefined) {
    return first.text;
  }
  return ended ? endedWords(item, now) : epicLine(item, now).state;
}

// The tasks of a kind that are running.
function running(background: BackgroundTask[], type: string): BackgroundTask[] {
  return background.filter((task) => task.type === type && task.status === "running");
}

function listGroup(kind: ListKind, tasks: BackgroundTask[], tone: ThingTone): RailGroup {
  const thing: ListThing = { kind, key: kind, count: tasks.length, tone, tasks };
  return groupOf(kind, kind, [thing]);
}

function groupOf(kind: RailGroup["kind"], label: string, things: Thing[]): RailGroup {
  return { kind, label, things, many: things.length > manyThings };
}
