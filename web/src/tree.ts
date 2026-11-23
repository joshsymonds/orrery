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

export interface Convo {
  key: string;        // row.key; a thing with no drawn conversation is its own row, keyed by the thing's key
  row: Row | null;    // null for a thing drawn as its own row, with no conversation drawn above it
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

// A thing in the view before it is grouped: the conversation row it hangs under, or null when it is its own row; the
// project it sits in; whether its epic has ended; and the thing itself.
interface Item { row: Row | null; place: Place; ended: boolean; hung: Hung }

// placeOf is the project a thing sits in: the project of its absolute path at the serving host, or else a card named by
// its repository.
function placeOf(path: string, repository: string, serving: string): Place {
  if (path.startsWith("/")) {
    return projectOf(path, serving, serving);
  }
  return { key: `repo:${repository}`, dir: "", name: repositoryName(repository), path: "", host: "" };
}

// buildTree places what the view draws: each drawn conversation's row with the epics and contracts that hang under it,
// each thing with no drawn conversation as a row of its own, and the ended lines; then it groups them into projects and
// ranks them.
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
  // drawnRow is the conversation a thing hangs under in this view: listed and not archived in the inbox, listed and
  // archived in the Archived view. A conversation the view does not draw holds nothing.
  const drawnRow = (conversation: Row | undefined): Row | undefined =>
    conversation !== undefined && (inbox ? !conversation.archived : conversation.archived) ? conversation : undefined;

  // Each epic in Epics.List order, with the proposed contract of its id when there is one, drawn as the epic; then each
  // contract that is not drawn that way, in Contracts.List order. The inbox draws an epic unless it is archived; the
  // Archived view draws one that is archived, or whose conversation is. A thing with no drawn conversation is its own row.
  const items: Item[] = [];
  const attached = new Set<Contract>();
  for (const epic of input.epics) {
    const conversation = drawnRow(rowByConvo.get(epic.steering));
    const visible = inbox ? !epic.archived : epic.archived || conversation !== undefined;
    if (!visible) {
      continue;
    }
    const contract = shown.find((candidate) => candidate.state === "proposed" && candidate.id === epic.id) ?? null;
    if (contract !== null) {
      attached.add(contract);
    }
    items.push({
      row: conversation ?? null,
      place: placeOf(epic.path, epic.repository, serving),
      // An ended epic carrying a proposed contract stays in the tree: the contract's ask is live work.
      ended: inbox && !epic.live && contract === null,
      hung: { kind: "epic", key: `epic:${epic.id}`, block: blockOf(epic, input.details.get(epic.id)), listed: epic, contract },
    });
  }
  for (const contract of shown) {
    if (attached.has(contract)) {
      continue;
    }
    const conversation = drawnRow(rowByConvo.get(contract.conversation));
    // The Archived view draws a contract only under an archived conversation.
    if (conversation === undefined && !inbox) {
      continue;
    }
    items.push({
      row: conversation ?? null,
      place: placeOf(contract.repository, contract.repository, serving),
      ended: false,
      hung: { kind: "contract", key: `contract:${contract.id}`, contract },
    });
  }

  const anchoredTo = (row: Row): Item[] => items.filter((item) => item.row === row);

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
    if (item.row !== null) {
      drawn.add(item.row);
    }
  }
  // A drawn conversation is placed by serve's fields: the Archived view draws each one it holds in the tree; the inbox
  // draws one that serve reports ended behind its project's ended line, with everything hung under it, and any other in the
  // tree, with its ended epics again behind the line.
  for (const row of drawn) {
    const own = anchoredTo(row);
    const project = projectAt(placeOfRow(row));
    const convo = (hung: Hung[], again: boolean): Convo => ({ key: row.key, row, again, hung });
    if (!inbox) {
      project.convos.push(convo(own.map((item) => item.hung), false));
    } else if (row.ended === true) {
      project.ended.push(convo(own.map((item) => item.hung), false));
    } else {
      const live = own.filter((item) => !item.ended).map((item) => item.hung);
      const finished = own.filter((item) => item.ended).map((item) => item.hung);
      project.convos.push(convo(live, false));
      if (finished.length > 0) {
        project.ended.push(convo(finished, true));
      }
    }
  }

  // A thing with no drawn conversation is its own row, holding just that thing, in its project: in the tree, or behind
  // the ended line once it has ended.
  for (const item of items) {
    if (item.row !== null) {
      continue;
    }
    const convo: Convo = { key: item.hung.key, row: null, again: false, hung: [item.hung] };
    const project = projectAt(item.place);
    if (item.ended) {
      project.ended.push(convo);
    } else {
      project.convos.push(convo);
    }
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

// changedOf is when a conversation last changed: its row's, or for a row-less one its hung thing's.
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
