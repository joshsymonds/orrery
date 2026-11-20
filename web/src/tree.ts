import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { EpicBlock } from "./epics";
import type { Row } from "./rows";

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

// Rank of a conversation, smallest first: waiting, unseen, working, then the rest. Waiting reads the row alone here;
// the hung things that wait join it with the epics of the next step.
const rankWaiting = 0;
const rankUnseen = 1;
const rankWorking = 2;
const rankRest = 3;

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

// buildTree groups the rows the view draws into projects and ranks them: conversations by rank then newest change,
// projects by their best conversation's rank then their newest change, and the No directory card last.
export function buildTree(input: TreeInput): Project[] {
  const groups = new Map<string, { place: ReturnType<typeof projectOf>; rows: Row[] }>();
  const noDirectory: Row[] = [];
  for (const row of input.rows) {
    const shown = input.view === "inbox" ? !row.archived : row.archived;
    if (!shown) {
      continue;
    }
    if (row.project === "") {
      noDirectory.push(row);
      continue;
    }
    const place = projectOf(row.project, row.host, input.host);
    const group = groups.get(place.key);
    if (group) {
      group.rows.push(row);
    } else {
      groups.set(place.key, { place, rows: [row] });
    }
  }
  const projects: Project[] = [...groups.values()]
    .map(({ place, rows }) => ({
      project: { ...place, convos: convosOf(rows), ended: [] },
      best: Math.min(...rows.map(rankOf)),
      newest: Math.max(...rows.map((row) => row.changed)),
    }))
    .sort((left, right) => left.best - right.best || right.newest - left.newest)
    .map((item) => item.project);
  if (noDirectory.length > 0) {
    projects.push({ key: noDirectoryKey, dir: "", name: noDirectoryName, path: "", host: "", convos: convosOf(noDirectory), ended: [] });
  }
  return projects;
}

function convosOf(rows: Row[]): Convo[] {
  return [...rows]
    .sort((left, right) => rankOf(left) - rankOf(right) || right.changed - left.changed)
    .map((row) => ({ key: row.key, row, standIn: "", again: false, hung: [] }));
}

function rankOf(row: Row): number {
  if (row.state === "waiting") {
    return rankWaiting;
  }
  if (row.unseen) {
    return rankUnseen;
  }
  if (row.state === "working") {
    return rankWorking;
  }
  return rankRest;
}
