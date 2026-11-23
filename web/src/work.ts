import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Design } from "./gen/orrery/v1/designs_pb";
import type { Epic } from "./gen/orrery/v1/epics_pb";
import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";
import { contractSlug } from "./contracts";
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

export interface ContractThing {
  kind: "contract";
  key: string;
  id: string;
  slug: string;
  short: string;
  asks: boolean;
  tone: ThingTone;
  title: string;
}

export interface ListThing {
  kind: "agents" | "running";
  key: "agents" | "running";
  count: number;
  tone: ThingTone;
  tasks: BackgroundTask[];
}

export type Thing = EpicThing | ListThing | ContractThing;

export interface RailGroup {
  kind: "contract" | "epic" | "agents" | "running";
  label: string;
  things: Thing[];
  many: boolean;
}

export interface WorkConversation {
  session: string;
  pane: boolean;
  background: BackgroundTask[];
}

// WorkLinkItem is one thing a conversation's work links to: a design, an epic or a contract, with its tree key, its id and
// its name.
export interface WorkLinkItem {
  kind: "design" | "epic" | "contract";
  key: string;  // design:<id>, epic:<id> or contract:<id>
  id: string;
  name: string;  // a design's title, an epic's or a contract's slug
}

// workLinksOf is the work links of a session: first each design whose conversation is the session and which is not of
// one, by when it was created, then each epic the session steers and each proposed contract of the session that no such
// design holds, in the order the epics come in the rail and then the contracts in the order they come. An empty session
// has none.
export function workLinksOf(
  session: string,
  designs: readonly Design[],
  epics: EpicsView,
  contracts: readonly Contract[],
): WorkLinkItem[] {
  if (session === "") {
    return [];
  }
  const own = designs
    .filter((design) => design.conversation === session && !design.ofOne)
    .sort((left, right) => Number(left.created - right.created) || left.id.localeCompare(right.id));
  // held is whether a design of the session holds the thing with this id, as one of its contracts' id or epic.
  const held = (id: string): boolean =>
    own.some((design) => design.contracts.some((contract) => contract.id === id || contract.epic === id));
  const designLinks = own.map(
    (design): WorkLinkItem => ({ kind: "design", key: `design:${design.id}`, id: design.id, name: design.title }),
  );
  const steered = [...epics.blocks.map((block) => block.epic), ...epics.waiting, ...epics.justEnded, ...epics.endedEarlier];
  const epicLinks = steered
    .filter((item) => epics.listed.get(item.id)?.steering === session && !held(item.id))
    .map((item): WorkLinkItem => ({ kind: "epic", key: item.key, id: item.id, name: item.slug }));
  const contractLinks = contracts
    .filter((contract) => contract.state === "proposed" && contract.conversation === session && !held(contract.id))
    .map(
      (contract): WorkLinkItem => ({
        kind: "contract",
        key: `contract:${contract.id}`,
        id: contract.id,
        name: contractSlug(contract.id),
      }),
    );
  return [...designLinks, ...epicLinks, ...contractLinks];
}

// ActivityLine is one line of a conversation's activity: a background task, an agent or a shell, with who runs it, what it
// is for, what it is doing, when it started and the list it sits in. key is the BackgroundTask id, started is unix seconds.
export interface ActivityLine {
  key: string;      // the BackgroundTask id
  type: "agent" | "shell";
  who: string;
  what: string;
  doing: string;
  started: number; // unix seconds
  list: "agents" | "running";
}

// activityOf is the activity lines of a conversation: one per background task that is running, the agents first and
// then the shells, each kind in the order its tasks started. A pane and a null conversation have none.
export function activityOf(conversation: WorkConversation | null): ActivityLine[] {
  if (conversation === null || conversation.pane) {
    return [];
  }
  const agents = startedOrder(running(conversation.background, "agent")).map((task) => activityLineOf(task, "agent"));
  const shells = startedOrder(running(conversation.background, "shell")).map((task) => activityLineOf(task, "shell"));
  return [...agents, ...shells];
}

// activityLineOf is the activity line of one running task. An agent is who its sub-agent type names, or agent when it
// names none; a shell is who shell. A task's list is the list of its kind.
function activityLineOf(task: BackgroundTask, type: "agent" | "shell"): ActivityLine {
  return {
    key: task.id,
    type,
    who: type === "agent" ? task.subagentType || "agent" : "shell",
    what: task.description,
    doing: task.activity,
    started: Number(task.started),
    list: type === "agent" ? "agents" : "running",
  };
}

// startedOrder is the tasks in the order they started, the earliest first. Tasks that started together keep their
// order.
function startedOrder(tasks: BackgroundTask[]): BackgroundTask[] {
  return [...tasks].sort((a, b) => Number(a.started) - Number(b.started));
}

type ListKind = "agents" | "running";

// A group with more than this many things is marked many.
const manyThings = 3;

// workOf gives a conversation's right-rail groups, in order: the contracts it proposed, the epics it steers, its
// running sub-agents and its running shells. An epic is steered when its record names the conversation's session, and
// a session that is empty steers none. A pane has no lists. A group with no thing is left out.
export function workOf(
  conversation: WorkConversation | null,
  epics: EpicsView,
  now: number,
  contracts: readonly Contract[] = [],
): RailGroup[] {
  if (conversation === null) {
    return [];
  }
  const groups: RailGroup[] = [];
  const contractThings = contractThingsOf(conversation.session, contracts);
  if (contractThings.length > 0) {
    groups.push(groupOf("contract", "contract", contractThings));
  }
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

// contractThingsOf gives the contracts the session proposed, in List's order: the proposed ones whose conversation is the
// session. A session that is empty has none. A contract asks while a decision is open.
export function contractThingsOf(session: string, contracts: readonly Contract[]): ContractThing[] {
  if (session === "") {
    return [];
  }
  return contracts
    .filter((contract) => contract.state === "proposed" && contract.conversation === session)
    .map((contract) => {
      const slug = contractSlug(contract.id);
      return {
        kind: "contract",
        key: `contract:${contract.id}`,
        id: contract.id,
        slug,
        short: shortName(slug, contract.repository),
        asks: contract.openDecisions > 0n,
        tone: "attention",
        title: `${slug}: a contract waits for you`,
      };
    });
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
