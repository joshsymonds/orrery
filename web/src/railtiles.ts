import { epicRowWords, hungName } from "./epicrow";
import type { Row } from "./rows";
import { changedOf, countsOf, freshConvos, projectWaiting, waitOrder } from "./treefilter";
import type { Convo, Project } from "./tree";

// RailTileModel is one project's tile on the collapsed inbox's rail: its key, its whole name, its waiting count, its
// dots, whether it is quiet, whether it holds the open page, and its title.
export interface RailTileModel {
  key: string;     // the project key
  name: string;    // the project's whole name, set sideways on the tile
  n: number;       // projectWaiting(project)
  live: boolean;   // a conversation in the tree is working, or an epic hung in the tree has running steps
  fresh: boolean;  // countsOf([project]).fresh > 0
  quiet: boolean;  // nothing waits on you and nothing is new: n is 0 and fresh is false
  cur: boolean;    // the project holds the open page
  title: string;   // "<name>" or "<name>: <n> wait on you" ("waits" when n is 1)
}

// railTiles is a tile per project, in the tree's order. open is the open page's item key or project key, "" for none.
export function railTiles(projects: readonly Project[], open: string): RailTileModel[] {
  return projects.map((project) => {
    const n = projectWaiting(project);
    const fresh = countsOf([project]).fresh > 0;
    return {
      key: project.key,
      name: project.name,
      n,
      live: project.convos.some(isLive),
      fresh,
      quiet: n === 0 && !fresh,
      cur: holds(project, open),
      title: n === 0 ? project.name : `${project.name}: ${n} ${n === 1 ? "waits" : "wait"} on you`,
    };
  });
}

// FlyoutRow is one row of a tile's flyout: a conversation's row, which waits on you or is new, or a hung epic or contract
// that waits on you, with its name, what it asks, and when it last changed.
export type FlyoutRow =
  | { kind: "row"; key: string; row: Row }
  | { kind: "hung"; key: string; name: string; ask: string; changed: number };

// FlyoutModel is what a tile's flyout holds: the project's key, name and path, its counts in words ("" for a zero
// count), the rows that wait on you, oldest first, then the new rows, and the line that counts the rest ("" when there
// is no rest).
export interface FlyoutModel {
  key: string;
  name: string;
  path: string;
  waits: string;
  fresh: string;
  rows: FlyoutRow[];
  rest: string;
}

// flyoutOf is the flyout of a project at now, in unix seconds. Its rows are what waits on you in waitOrder's order, which
// w scoped to the project walks, then each unseen conversation not already listed, in the tree and behind the ended
// line. The rest counts the tree's other conversation rows and hung things, none of which waits, and the conversations
// behind the ended line.
export function flyoutOf(project: Project, now: number): FlyoutModel {
  const convos = [...project.convos, ...project.ended];
  const waiting = waitOrder([project]).map((key) => flyoutRow(convos, key, now));
  const listed = new Set(waiting.map((row) => row.key));
  const fresh = freshConvos(project);
  const unseen = fresh.flatMap((convo): FlyoutRow[] =>
    convo.row === null || listed.has(convo.key) ? [] : [{ kind: "row", key: convo.key, row: convo.row }],
  );
  const rows = [...waiting, ...unseen];
  const shown = new Set(rows.map((row) => row.key));
  const entries = project.convos.flatMap((convo) => [...(convo.row === null ? [] : [convo.key]), ...convo.hung.map((hung) => hung.key)]);
  const more = entries.filter((key) => !shown.has(key)).length;
  const rest = [more > 0 ? `and ${more} more, none waiting` : "", project.ended.length > 0 ? `${project.ended.length} ended` : ""];
  return {
    key: project.key,
    name: project.name,
    path: project.path,
    waits: waiting.length === 0 ? "" : `${waiting.length} ${waiting.length === 1 ? "waits" : "wait"}`,
    fresh: fresh.length === 0 ? "" : `${fresh.length} new`,
    rows,
    rest: rest.filter((part) => part !== "").join(" · "),
  };
}

// flyoutRow is the flyout's row for a counted thing's item key: the conversation whose row it is, or the hung epic or
// contract it names, with that thing's ask.
function flyoutRow(convos: readonly Convo[], key: string, now: number): FlyoutRow {
  for (const convo of convos) {
    if (convo.row !== null && convo.key === key) {
      return { kind: "row", key, row: convo.row };
    }
    const hung = convo.hung.find((candidate) => candidate.key === key);
    if (hung !== undefined) {
      return { kind: "hung", key, name: hungName(hung), ask: epicRowWords(hung, now).blocker, changed: changedOf(hung) };
    }
  }
  throw new Error(`waitOrder named ${key}, which its project does not hold`);
}

// isLive says a conversation in the tree is working, or an epic hung under it has running steps.
function isLive(convo: Convo): boolean {
  return convo.row?.state === "working" || convo.hung.some((hung) => hung.kind === "epic" && hung.block.full);
}

// holds says the project is the open key, or one of its conversations, in the tree or behind the ended line, is, or has
// a hung thing that is.
function holds(project: Project, open: string): boolean {
  if (project.key === open) {
    return true;
  }
  return [...project.convos, ...project.ended].some(
    (convo) => convo.key === open || convo.hung.some((hung) => hung.key === open),
  );
}
