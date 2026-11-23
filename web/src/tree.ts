import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Design, DesignContract } from "./gen/orrery/v1/designs_pb";
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
// DesignNode is a design hung under its conversation, keyed `design:${id}`. hung is the design's children in its
// contracts' position order, and they are the same Hung objects the conversation's hung holds.
export interface DesignNode { kind: "design"; key: string /* `design:${id}` */; design: Design; hung: Hung[] }

export interface Convo {
  key: string;        // row.key; a thing with no drawn conversation is its own row, keyed by the thing's key
  row: Row | null;    // null for a thing drawn as its own row, with no conversation drawn above it
  hung: Hung[];       // in the tree: its live epics (Epics.List order), then its contracts (Contracts.List order);
                      // behind the ended line: its ended epics
  folded: Hung[];     // in the tree: its ended epics, newest first, drawn under its row only while its fold is open;
                      // behind the ended line: none
  // designs: the designs hung under this conversation. Absent reads as none. convo.hung still holds every thing: things in
  // no design first, then each design's children in design order.
  designs?: DesignNode[];
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
  designs?: readonly Design[];                    // the designs; absent reads as none
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

// A design placed in a view: the drawn conversation it hangs under, or null when it is a conversation of its own, and the
// items it takes, in position order.
interface DesignPlace { design: Design; row: Row | null; children: Item[] }

// idOf is the id of a hung thing: its epic's, or its contract's.
function idOf(hung: Hung): string {
  return hung.kind === "epic" ? hung.listed.id : hung.contract.id;
}

// byCreated orders designs by when they were created, then by id.
function byCreated(left: Design, right: Design): number {
  return Number(left.created - right.created) || left.id.localeCompare(right.id);
}

// byPosition orders a design's contracts by their place in its start order.
function byPosition(left: DesignContract, right: DesignContract): number {
  return Number(left.position - right.position);
}

// designChildren are the unclaimed items a design takes: each whose id is a contract's id or its non-empty epic, in the
// order of the contracts' positions.
function designChildren(design: Design, items: readonly Item[], claimed: ReadonlySet<Item>): Item[] {
  const children: Item[] = [];
  for (const contract of [...design.contracts].sort(byPosition)) {
    for (const id of [contract.id, contract.epic]) {
      for (const item of items) {
        if (id !== "" && idOf(item.hung) === id && !claimed.has(item) && !children.includes(item)) {
          children.push(item);
        }
      }
    }
  }
  return children;
}

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
      // An epic hung under a drawn conversation that carries a proposed contract stays in the tree: the contract's ask is
      // live work. An epic with no drawn conversation is its own row, ended once it is not live.
      ended: inbox && !epic.live && (conversation === undefined || contract === null),
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

  // A design that is not of one is placed where its conversation is drawn: under that row, or in the inbox as a conversation
  // of its own when no drawn conversation works it but a thing it holds is drawn. Its children are the items its contracts
  // name, by id or by epic, in position order. A thing one design takes is no other design's child. The Archived view leaves
  // out a design with no drawn conversation, so its things stay as they are, ungrouped.
  const claimed = new Set<Item>();
  const placed: DesignPlace[] = [];
  for (const design of (input.designs ?? []).filter((candidate) => !candidate.ofOne).sort(byCreated)) {
    const conversation = drawnRow(rowByConvo.get(design.conversation));
    const children = designChildren(design, items, claimed);
    if (conversation === undefined && (!inbox || children.length === 0)) {
      continue;
    }
    for (const child of children) {
      claimed.add(child);
    }
    placed.push({ design, row: conversation ?? null, children });
  }
  const nodeOf = (place: DesignPlace): DesignNode => ({
    kind: "design",
    key: `design:${place.design.id}`,
    design: place.design,
    hung: place.children.map((child) => child.hung),
  });

  const anchoredTo = (row: Row): Item[] => items.filter((item) => item.row === row && !claimed.has(item));

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
  // draws one that serve reports ended behind its project's ended line, with everything hung under it, and any other in
  // the tree, with its ended epics folded under it, newest first, and nothing of it behind the line.
  for (const row of drawn) {
    const own = anchoredTo(row);
    const project = projectAt(placeOfRow(row));
    const nodes = placed.filter((place) => place.row === row).map(nodeOf);
    const designHung = nodes.flatMap((node) => node.hung);
    // A design's children follow the row's own things, and are never folded: an ended one stays under its design.
    const convo = (hung: Hung[], folded: Hung[]): Convo => ({
      key: row.key,
      row,
      hung: [...hung, ...designHung],
      folded,
      designs: nodes,
    });
    if (!inbox) {
      project.convos.push(convo(own.map((item) => item.hung), []));
    } else if (row.ended === true) {
      project.ended.push(convo(own.map((item) => item.hung), []));
    } else {
      const live = own.filter((item) => !item.ended).map((item) => item.hung);
      const folded = own
        .filter((item) => item.ended)
        .map((item) => item.hung)
        .sort((left, right) => changedThing(right) - changedThing(left));
      project.convos.push(convo(live, folded));
    }
  }

  // A thing with no drawn conversation is its own row, holding just that thing, in its project: in the tree, or behind
  // the ended line once it has ended.
  for (const item of items) {
    if (item.row !== null || claimed.has(item)) {
      continue;
    }
    const convo: Convo = { key: item.hung.key, row: null, hung: [item.hung], folded: [] };
    const project = projectAt(item.place);
    if (item.ended) {
      project.ended.push(convo);
    } else {
      project.convos.push(convo);
    }
  }

  // A design with no drawn conversation is a conversation of its own, holding its children, in the project of the first of
  // them. It is behind the ended line only when every child has ended.
  for (const place of placed) {
    if (place.row !== null) {
      continue;
    }
    const node = nodeOf(place);
    const convo: Convo = { key: node.key, row: null, hung: node.hung, folded: [], designs: [node] };
    const [first] = place.children;
    const project = projectAt(first.place);
    if (place.children.every((child) => child.ended)) {
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
  return Math.max(...convo.hung.map((hung) => changedThing(hung)));
}

// changedThing is when a hung thing last changed: its epic's listed change, or its contract's.
function changedThing(hung: Hung): number {
  return Number(hung.kind === "epic" ? hung.listed.changed : hung.contract.changed);
}

// bestOf is a project's best conversation's rank, after every rank when it has none in the tree.
function bestOf(project: Project): number {
  return Math.min(rankRest + 1, ...project.convos.map((convo) => rankOf(convo)));
}

function newestOf(project: Project): number {
  return Math.max(...[...project.convos, ...project.ended].map((convo) => changedOf(convo)));
}
