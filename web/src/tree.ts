import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { blockOf, type EpicBlock, repositoryName } from "./epics";
import { epicNeeds } from "./needs";
import { type Row, waitsOnYou } from "./rows";

// EpicNode is a listed epic hung under its conversation with its running steps (block.steps, at most four, block.more
// for the rest, block.tail) and, when Contracts.List holds a contract of the same id, that contract.
export interface EpicNode { kind: "epic"; key: string /* `epic:${id}` */; block: EpicBlock; listed: Epic; contract: Contract | null }
// ContractItem is a contract hung under its conversation: proposed, or accepted while no listed epic has its id.
export interface ContractItem { kind: "contract"; key: string /* `contract:${id}` */; contract: Contract }
export type Hung = EpicNode | ContractItem;

// StandIn: "" for a conversation's own row; "unlisted" for the one row per project that holds what steers with no
// listed conversation; "archived" for the one row per project that holds what a now-archived conversation steers.
export type StandIn = "" | "unlisted" | "archived";

export interface Convo {
  key: string;        // row.key; a stand-in's is `standin:${standIn}:${projectKey}`
  row: Row | null;    // null only for a stand-in
  standIn: StandIn;
  again: boolean;     // true when this is a conversation drawn a second time, behind the ended line, as the parent of
                      // its ended epics while it is still in the tree above
  hung: Hung[];       // in the tree: its live epics (Epics.List order), then its contracts (Contracts.List order);
                      // behind the ended line: its ended epics
}

export interface Project {
  key: string;        // `${host}|${dir}`; `repo:${repository}` for a card named by a repository name; "nodir"
  dir: string;        // the absolute project directory ("" for repo: and nodir cards)
  name: string;       // last path element ("~" for the home itself); repositoryName(repository); "No directory"
  path: string;       // the shown path: the whole directory with home as ~ (e.g. "~/code/lighthouse"); "" for repo:/nodir
  host: string;       // the rows' host when it is not the serving host, else ""
  convos: Convo[];    // in the tree, ranked
  ended: Convo[];     // behind the project's ended line, ranked newest first
}

export type View = "inbox" | "archived";
export interface TreeInput {
  rows: readonly Row[];
  epics: readonly Epic[];                         // Epics.List, archived ones included
  details: ReadonlyMap<string, EpicsGetResponse>; // Epics.Get answers by id
  contracts: readonly Contract[];                 // Contracts.List
  host: string;                                   // the serving host, "" when unknown
  view: View;
}

const homeParents = ["home", "Users"];
const worktreeSuffixes = [".worktrees", "-worktrees"];
const noDirectoryKey = "nodir";
const noDirectoryName = "No directory";

// Rank of a conversation, smallest first: waiting (its row waits on you, or a hung thing that waits), unseen, working, then
// the rest.
const rankWaiting = 0;
const rankUnseen = 1;
const rankWorking = 2;
const rankRest = 3;

type Place = ReturnType<typeof projectOf>;

const noDirectoryPlace: Place = { key: noDirectoryKey, dir: "", name: noDirectoryName, path: "", host: "" };

// projectOf places a directory: its project key, absolute directory, name and shown path, and the host it runs on
// when that is not the serving host. A `<repo>.worktrees/<x>` or `<repo>-worktrees/<x>` pair folds to `<repo>`, and
// home is shown as ~ under /home/<user> or /Users/<user>.
export function projectOf(dir: string, host: string, serving: string): { key: string; dir: string; name: string; path: string; host: string } {
  const parts = dir.split("/").filter((part) => part !== "");
  const homed = parts.length >= 2 && homeParents.includes(parts[0]);
  const head = homed ? parts.slice(0, 2) : [];
  const folders = foldWorktrees(homed ? parts.slice(2) : parts);
  const absolute = `/${[...head, ...folders].join("/")}`;
  const shownHost = host === serving ? "" : host;
  const path = homed ? (folders.length === 0 ? "~" : `~/${folders.join("/")}`) : absolute;
  const name = folders.length === 0 ? (homed ? "~" : "/") : folders[folders.length - 1];
  return { key: `${shownHost}|${absolute}`, dir: absolute, name, path, host: shownHost };
}

// foldWorktrees replaces each `<repo>.worktrees/<x>` pair with `<repo>`. A worktree segment with no name after it stays.
function foldWorktrees(parts: string[]): string[] {
  const folders: string[] = [];
  for (let index = 0; index < parts.length; index++) {
    const repo = index + 1 < parts.length ? repoOf(parts[index]) : null;
    if (repo !== null) {
      folders.push(repo);
      index++;
    } else {
      folders.push(parts[index]);
    }
  }
  return folders;
}

function repoOf(name: string): string | null {
  for (const suffix of worktreeSuffixes) {
    if (name.endsWith(suffix) && name.length > suffix.length) {
      return name.slice(0, -suffix.length);
    }
  }
  return null;
}

// Where a thing hangs: under a conversation's row, or under the stand-in of its project.
type Anchor = { kind: "row"; row: Row } | { kind: "standin"; standIn: "unlisted" | "archived"; place: Place };

// A thing in the view before it is grouped: where it hangs, what it draws as, and whether its epic has ended.
interface Item { anchor: Anchor; ended: boolean; hung: Hung }

// placeOf is where a thing with no listed conversation hangs: the project of its absolute path at the serving host, or
// else a card named by its repository.
function placeOf(path: string, repository: string, serving: string): Place {
  if (path.startsWith("/")) {
    return projectOf(path, serving, serving);
  }
  return { key: `repo:${repository}`, dir: "", name: repositoryName(repository), path: "", host: "" };
}

// buildTree places what the view draws: each conversation's row with the epics and contracts that hang under it, the
// stand-ins of what has no listed conversation, and the ended lines; then it groups them into projects and ranks them.
export function buildTree(input: TreeInput): Project[] {
  const inbox = input.view === "inbox";
  const serving = input.host;
  const rowByConvo = new Map<string, Row>();
  for (const row of input.rows) {
    if (row.archiveId !== "") {
      rowByConvo.set(row.archiveId, row);
    }
  }
  const listedIds = new Set(input.epics.map((epic) => epic.id));
  const shown = input.contracts.filter(
    (contract) => contract.state === "proposed" || (contract.state === "accepted" && !listedIds.has(contract.id)),
  );

  const inboxAnchor = (conversation: Row | undefined, place: Place): Anchor => {
    if (conversation === undefined) {
      return { kind: "standin", standIn: "unlisted", place };
    }
    if (conversation.archived) {
      return { kind: "standin", standIn: "archived", place };
    }
    return { kind: "row", row: conversation };
  };
  // archivedAnchor places a thing in the archived view: under an archived conversation, or else, when it is an archived
  // epic, under its live conversation drawn as the parent, or under the unlisted stand-in.
  const archivedAnchor = (conversation: Row | undefined, archivedThing: boolean, place: Place): Anchor | null => {
    if (conversation !== undefined && conversation.archived) {
      return { kind: "row", row: conversation };
    }
    if (!archivedThing) {
      return null;
    }
    return conversation === undefined ? { kind: "standin", standIn: "unlisted", place } : { kind: "row", row: conversation };
  };
  const anchorOfEpic = (epic: Epic): Anchor | null => {
    const conversation = rowByConvo.get(epic.steering);
    const place = placeOf(epic.path, epic.repository, serving);
    if (inbox) {
      return epic.archived ? null : inboxAnchor(conversation, place);
    }
    return archivedAnchor(conversation, epic.archived, place);
  };
  const anchorOfContract = (contract: Contract): Anchor | null => {
    const conversation = rowByConvo.get(contract.conversation);
    const place = placeOf(contract.repository, contract.repository, serving);
    return inbox ? inboxAnchor(conversation, place) : archivedAnchor(conversation, false, place);
  };

  // Each epic in Epics.List order, with the proposed contract of its id when there is one, drawn as the epic; then each
  // contract that is not drawn that way, in Contracts.List order.
  const items: Item[] = [];
  const attached = new Set<Contract>();
  for (const epic of input.epics) {
    const anchor = anchorOfEpic(epic);
    if (anchor === null) {
      continue;
    }
    const contract = shown.find((candidate) => candidate.state === "proposed" && candidate.id === epic.id) ?? null;
    if (contract !== null) {
      attached.add(contract);
    }
    items.push({
      anchor,
      // An ended epic carrying a proposed contract stays in the tree: the contract's ask is live work.
      ended: inbox && !epic.live && contract === null,
      hung: { kind: "epic", key: `epic:${epic.id}`, block: blockOf(epic, input.details.get(epic.id)), listed: epic, contract },
    });
  }
  for (const contract of shown) {
    const anchor = attached.has(contract) ? null : anchorOfContract(contract);
    if (anchor !== null) {
      items.push({ anchor, ended: false, hung: { kind: "contract", key: `contract:${contract.id}`, contract } });
    }
  }

  const anchoredTo = (row: Row): Item[] => items.filter((item) => item.anchor.kind === "row" && item.anchor.row === row);
  // A conversation stays in the tree while it steers a live epic or a shown contract, so an ended one is drawn behind the
  // line only when nothing live or waiting hangs under it.
  const keeps = (row: Row): boolean =>
    anchoredTo(row).some((item) => !item.ended) || shown.some((contract) => rowByConvo.get(contract.conversation) === row);
  const behindLine = (row: Row): boolean =>
    !keeps(row) &&
    ((row.archiveKind === "headless" && (row.state === "stopped" || (row.state === "failed" && !row.unseen))) ||
      (row.state === "done" && anchoredTo(row).some((item) => item.ended)));

  const projects = new Map<string, Project>();
  const projectAt = (place: Place): Project => {
    const existing = projects.get(place.key);
    if (existing !== undefined) {
      return existing;
    }
    const project: Project = { ...place, convos: [], ended: [] };
    projects.set(place.key, project);
    return project;
  };
  const placeOfRow = (row: Row): Place => (row.project === "" ? noDirectoryPlace : projectOf(row.project, row.host, serving));

  const drawn = new Set<Row>(input.rows.filter((row) => (inbox ? !row.archived : row.archived)));
  for (const item of items) {
    if (item.anchor.kind === "row") {
      drawn.add(item.anchor.row);
    }
  }
  for (const row of drawn) {
    const own = anchoredTo(row);
    const project = projectAt(placeOfRow(row));
    const convo = (hung: Hung[], again: boolean): Convo => ({ key: row.key, row, standIn: "", again, hung });
    const live = own.filter((item) => !item.ended).map((item) => item.hung);
    const finished = own.filter((item) => item.ended).map((item) => item.hung);
    if (!inbox) {
      project.convos.push(convo(own.map((item) => item.hung), false));
    } else if (behindLine(row)) {
      project.ended.push(convo(finished, false));
    } else {
      project.convos.push(convo(live, false));
      if (finished.length > 0) {
        project.ended.push(convo(finished, true));
      }
    }
  }

  // A stand-in holds the things of its kind in its project: one row in the tree, and one behind the ended line.
  const standIns = new Map<string, Convo>();
  for (const item of items) {
    if (item.anchor.kind !== "standin") {
      continue;
    }
    const { standIn, place } = item.anchor;
    const slot = `${item.ended ? "ended" : "tree"}|${standIn}|${place.key}`;
    let convo = standIns.get(slot);
    if (convo === undefined) {
      convo = { key: `standin:${standIn}:${place.key}`, row: null, standIn, again: false, hung: [] };
      standIns.set(slot, convo);
      const project = projectAt(place);
      if (item.ended) {
        project.ended.push(convo);
      } else {
        project.convos.push(convo);
      }
    }
    convo.hung.push(item.hung);
  }

  for (const project of projects.values()) {
    project.convos.sort((left, right) => rankOf(left) - rankOf(right) || changedOf(right) - changedOf(left));
    project.ended.sort((left, right) => changedOf(right) - changedOf(left));
  }
  return [...projects.values()].sort(
    (left, right) =>
      Number(left.key === noDirectoryKey) - Number(right.key === noDirectoryKey) ||
      bestOf(left) - bestOf(right) ||
      newestOf(right) - newestOf(left),
  );
}

// waits says whether a hung thing waits on you (R4, DL27): a proposed contract, or a live, unarchived epic that raises a
// need. An epic that is not listed live never waits on its needs.
export function waits(hung: Hung): boolean {
  if (hung.kind === "contract") {
    return hung.contract.state === "proposed";
  }
  if (hung.contract?.state === "proposed") {
    return true;
  }
  const detail = hung.block.epic.detail;
  return (
    hung.listed.live &&
    !hung.listed.archived &&
    detail !== undefined &&
    epicNeeds([hung.listed], new Map([[hung.listed.id, detail]])).length > 0
  );
}

// convoWaits says whether a conversation waits on you: its row waits on you, or a thing hung under it waits.
export function convoWaits(convo: Convo): boolean {
  return (convo.row !== null && waitsOnYou(convo.row)) || convo.hung.some((hung) => waits(hung));
}

function rankOf(convo: Convo): number {
  if (convoWaits(convo)) {
    return rankWaiting;
  }
  if (convo.row?.unseen) {
    return rankUnseen;
  }
  if (convo.row?.state === "working") {
    return rankWorking;
  }
  return rankRest;
}

// changedOf is when a conversation last changed: its row's, or for a stand-in its newest hung thing's.
export function changedOf(convo: Convo): number {
  if (convo.row !== null) {
    return convo.row.changed;
  }
  return Math.max(...convo.hung.map((hung) => Number(hung.kind === "epic" ? hung.listed.changed : hung.contract.changed)));
}

// bestOf is a project's best conversation's rank, after every rank when it has none in the tree.
function bestOf(project: Project): number {
  return Math.min(rankRest + 1, ...project.convos.map((convo) => rankOf(convo)));
}

function newestOf(project: Project): number {
  return Math.max(...[...project.convos, ...project.ended].map((convo) => changedOf(convo)));
}
