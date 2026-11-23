import type { Project } from "./tree";
import type { Row } from "./rows";

// Line is the line a conversation is drawn on: its project's tree, its project's ended line, or the archived view.
export type Line = "tree" | "ended" | "archived";

// Spot is where a conversation is drawn: project is Project.key, name is Project.name, and line is the Line.
export interface Spot { project: string /* Project.key */; name: string /* Project.name */; line: Line }

// Placed is a conversation row with the Spot it is drawn at.
export interface Placed { row: Row; spot: Spot }

// Move is a conversation row drawn at one Spot before and at another Spot after.
export interface Move { row: Row; from: Spot; to: Spot }

// spotsOf maps each conversation row by its key to the Spot where it is drawn: in the inbox, a project's convos on the
// tree line and its ended on the ended line; in the archived view, a project's convos on the archived line. A Convo
// with a null row is a stand-in and is not drawn as a conversation, so it has no entry.
export function spotsOf(inbox: readonly Project[], archived: readonly Project[]): Map<string, Placed> {
  return new Map();
}

// movesBetween returns the rows present in both before and after whose Spot differs, in after's order. A row present
// in only one of the two maps is not a move.
export function movesBetween(before: ReadonlyMap<string, Placed>, after: ReadonlyMap<string, Placed>): Move[] {
  return [];
}
