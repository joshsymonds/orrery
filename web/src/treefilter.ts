import { waitsOnYou } from "./rows";
import { type Convo, type Hung, type Project, waits } from "./tree";

// Narrow is the chip the tree is narrowed by: none, what waits on you, or what is new (unseen).
export type Narrow = "" | "waiting" | "new";
export interface Counts { waiting: number; fresh: number }

// counted is one thing that waits on you, with its item key and when it last changed.
interface Counted { key: string; changed: number }

// countsOf is the two chips: what waits on you, counted once per thing (DL27), and the unseen rows, each once.
export function countsOf(projects: readonly Project[]): Counts {
  let waiting = 0;
  let fresh = 0;
  for (const project of projects) {
    for (const convo of project.convos) {
      waiting += countedOf(convo).length;
    }
    fresh += freshConvos(project).length;
  }
  return { waiting, fresh };
}

// projectWaiting is a folded project's waiting number: what countsOf counts over that project alone.
export function projectWaiting(project: Project): number {
  return countsOf([project]).waiting;
}

// narrow keeps what the chip names: the projects with a counted thing, each keeping its waiting conversations and only
// the hung things that wait; or the projects with an unseen row, each keeping its unseen conversations with nothing hung.
// Either way no ended line is kept. "" keeps every project as it is.
export function narrow(projects: readonly Project[], by: Narrow): Project[] {
  if (by === "") {
    return [...projects];
  }
  const kept: Project[] = [];
  for (const project of projects) {
    const convos = by === "waiting" ? waitingConvos(project) : freshConvos(project).map((convo) => ({ ...convo, again: false, hung: [] }));
    if (convos.length > 0) {
      kept.push({ ...project, convos, ended: [] });
    }
  }
  return kept;
}

// waitOrder is the item key of each counted thing, oldest change first, ties by key.
export function waitOrder(projects: readonly Project[]): string[] {
  return projects
    .flatMap((project) => project.convos.flatMap((convo) => countedOf(convo)))
    .sort(byOldest)
    .map((counted) => counted.key);
}

// search keeps the projects with a conversation or a hung thing that holds every word, ignoring case. A conversation
// that matches itself keeps all its hung things; otherwise it keeps only the ones that match. Ended conversations are
// searched the same way.
export function search(projects: readonly Project[], words: readonly string[]): Project[] {
  const needles = words.map((word) => word.toLowerCase());
  const kept: Project[] = [];
  for (const project of projects) {
    const place = [project.name, project.path];
    const convos = searchConvos(project.convos, place, needles);
    const ended = searchConvos(project.ended, place, needles);
    if (convos.length > 0 || ended.length > 0) {
      kept.push({ ...project, convos, ended });
    }
  }
  return kept;
}

// countedOf is the things of a conversation that wait on you, each once (DL27): the conversation itself when its row
// waits on you, each hung epic that raises a need of its own, and each hung proposed contract unless its conversation's
// own row waits on you. An archived conversation counts nothing.
function countedOf(convo: Convo): Counted[] {
  if (convo.row?.archived) {
    return [];
  }
  const counted: Counted[] = [];
  if (convo.row !== null && waitsOnYou(convo.row)) {
    counted.push({ key: convo.row.key, changed: convo.row.changed });
  }
  for (const hung of convo.hung) {
    if (countsHung(convo, hung)) {
      counted.push({ key: hung.key, changed: changedOf(hung) });
    }
  }
  return counted;
}

// countsHung says whether a hung thing is a count of its own. An epic with its own need is one; a proposed contract, on
// its own or carried by its epic, counts unless the conversation that wrote it waits.
function countsHung(convo: Convo, hung: Hung): boolean {
  if (hung.kind === "epic" && waits({ ...hung, contract: null })) {
    return true;
  }
  const proposed = hung.kind === "contract" ? hung.contract.state === "proposed" : hung.contract?.state === "proposed";
  return proposed && (convo.row === null || !waitsOnYou(convo.row));
}

function changedOf(hung: Hung): number {
  return Number(hung.kind === "epic" ? hung.listed.changed : hung.contract.changed);
}

// freshConvos is each conversation of a project whose row is unseen, once: in the tree, then behind the ended line.
function freshConvos(project: Project): Convo[] {
  const seen = new Set<string>();
  return [...project.convos, ...project.ended].filter((convo) => {
    if (convo.row === null || !convo.row.unseen || convo.row.archived || seen.has(convo.key)) {
      return false;
    }
    seen.add(convo.key);
    return true;
  });
}

// waitingConvos is each conversation of a project that has a counted thing, keeping only the hung things that wait.
function waitingConvos(project: Project): Convo[] {
  return project.convos
    .filter((convo) => countedOf(convo).length > 0)
    .map((convo) => ({ ...convo, hung: convo.hung.filter((hung) => waits(hung)) }));
}

function byOldest(left: Counted, right: Counted): number {
  if (left.changed !== right.changed) {
    return left.changed - right.changed;
  }
  if (left.key === right.key) {
    return 0;
  }
  return left.key < right.key ? -1 : 1;
}

// searchConvos keeps each conversation that matches itself or has a hung thing that matches, with the things it keeps.
function searchConvos(convos: readonly Convo[], place: string[], needles: string[]): Convo[] {
  return convos.flatMap((convo) => {
    const self = convo.row !== null && matches([convo.row.label, convo.row.state, convo.row.host, ...place], needles);
    const hung = self ? convo.hung : convo.hung.filter((item) => matches(textOf(item, place), needles));
    return self || hung.length > 0 ? [{ ...convo, hung }] : [];
  });
}

// textOf is what a hung thing's search reads: its epic's slug and outcome, with the contract it carries, or its
// contract's id and title, and its project's name and path.
function textOf(hung: Hung, place: string[]): string[] {
  if (hung.kind === "contract") {
    return [hung.contract.id, hung.contract.title, ...place];
  }
  const contract = hung.contract === null ? [] : [hung.contract.id, hung.contract.title];
  return [hung.listed.slug, hung.listed.outcome, ...contract, ...place];
}

// matches says every needle appears in one of the fields, ignoring case: a word does not span two fields.
function matches(fields: string[], needles: string[]): boolean {
  return needles.every((needle) => fields.some((field) => field.toLowerCase().includes(needle)));
}
