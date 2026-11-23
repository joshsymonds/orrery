import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Design } from "./gen/orrery/v1/designs_pb";
import type { Epic } from "./gen/orrery/v1/epics_pb";
import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";
import { contractSlug } from "./contracts";
import { type EpicItem, type EpicsView } from "./epics";
import { epicNeeds } from "./needs";

export type ThingTone = "attention" | "live" | "quiet";

// EpicThing is an epic a conversation steers, as the epic panel takes it: its key in the epics list, and the tone its card
// takes.
export interface EpicThing {
  kind: "epic";
  key: string;
  tone: ThingTone;
}

// ListThing is a list of a conversation's running tasks, its sub-agents or its shells left running, with the tone its
// panel's card takes and the tasks it holds.
export interface ListThing {
  kind: "agents" | "running";
  key: "agents" | "running";
  tone: ThingTone;
  tasks: BackgroundTask[];
}

export type Thing = EpicThing | ListThing;

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
// design holds, in the order the epics list gives them and then the contracts in the order they come. An empty session
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

// workOf gives the things a conversation's side panels take their tones and tasks from, in order: the epics it steers, in
// the order the epics list gives them (the blocks, the waiting, then the just ended), and its running sub-agents and its
// running shells. An epic is steered when its record names the conversation's session, and a session that is empty steers
// none. A pane has no lists.
export function workOf(conversation: WorkConversation | null, epics: EpicsView): Thing[] {
  if (conversation === null) {
    return [];
  }
  const things: Thing[] = [];
  const items = [...epics.blocks.map((block) => block.epic), ...epics.waiting, ...epics.justEnded];
  for (const item of items) {
    const listed = epics.listed.get(item.id);
    if (listed !== undefined && conversation.session !== "" && listed.steering === conversation.session) {
      things.push(epicThingOf(item, listed, epics));
    }
  }
  if (!conversation.pane) {
    const agents = running(conversation.background, "agent");
    if (agents.length > 0) {
      things.push(listThingOf("agents", agents, "live"));
    }
    const shells = running(conversation.background, "shell");
    if (shells.length > 0) {
      things.push(listThingOf("running", shells, "quiet"));
    }
  }
  return things;
}

function epicThingOf(item: EpicItem, listed: Epic, epics: EpicsView): EpicThing {
  const asks = epicNeeds([listed], epics.details).length > 0;
  return { kind: "epic", key: item.key, tone: toneOf(asks, listed) };
}

// An epic asks when a need is open, and is live while it has a running step.
function toneOf(asks: boolean, listed: Epic): ThingTone {
  if (asks) {
    return "attention";
  }
  return listed.runningSteps.length > 0 ? "live" : "quiet";
}

function listThingOf(kind: ListThing["kind"], tasks: BackgroundTask[], tone: ThingTone): ListThing {
  return { kind, key: kind, tone, tasks };
}

// The tasks of a kind that are running.
function running(background: BackgroundTask[], type: string): BackgroundTask[] {
  return background.filter((task) => task.type === type && task.status === "running");
}
