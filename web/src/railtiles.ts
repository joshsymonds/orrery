import { countsOf, projectWaiting } from "./treefilter";
import type { Convo, Project } from "./tree";

// RailTileModel is one project's tile on the inbox rail: its key, its two letters, its waiting count, its dots, whether
// it holds the open page, and its title.
export interface RailTileModel {
  key: string;     // the project key
  text: string;    // the name's first two letters; its first and last when another project's name shares the first two
  n: number;       // projectWaiting(project)
  live: boolean;   // a conversation in the tree is working, or an epic hung in the tree has running steps
  fresh: boolean;  // countsOf([project]).fresh > 0
  cur: boolean;    // the project holds the open page
  title: string;   // "<name>" or "<name>: <n> wait on you" ("waits" when n is 1)
}

// railTiles is a tile per project, in the tree's order. open is the open page's item key or project key, "" for none.
export function railTiles(projects: readonly Project[], open: string): RailTileModel[] {
  const names = projects.map((project) => project.name);
  return projects.map((project) => {
    const n = projectWaiting(project);
    return {
      key: project.key,
      text: shortOf(project.name, names),
      n,
      live: project.convos.some(isLive),
      fresh: countsOf([project]).fresh > 0,
      cur: holds(project, open),
      title: n === 0 ? project.name : `${project.name}: ${n} ${n === 1 ? "waits" : "wait"} on you`,
    };
  });
}

// shortOf is a name's two letters, or its first and last when another name shares its first two.
function shortOf(name: string, names: readonly string[]): string {
  const prefix = name.slice(0, 2);
  const shared = names.filter((other) => other.slice(0, 2) === prefix).length > 1;
  return shared ? name[0] + name[name.length - 1] : prefix;
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
