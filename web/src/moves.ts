import type { Convo, Project } from "./tree";
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
  const spots = new Map<string, Placed>();
  const place = (project: Project, convos: readonly Convo[], line: Line) => {
    for (const convo of convos) {
      if (convo.row !== null) {
        spots.set(convo.row.key, { row: convo.row, spot: { project: project.key, name: project.name, line } });
      }
    }
  };
  for (const project of inbox) {
    place(project, project.convos, "tree");
    place(project, project.ended, "ended");
  }
  for (const project of archived) {
    place(project, project.convos, "archived");
  }
  return spots;
}

// movesBetween returns the rows present in both before and after whose Spot differs, in after's order. A row present
// in only one of the two maps is not a move.
export function movesBetween(before: ReadonlyMap<string, Placed>, after: ReadonlyMap<string, Placed>): Move[] {
  const moves: Move[] = [];
  for (const [key, placed] of after) {
    const previous = before.get(key);
    if (previous !== undefined && !sameSpot(previous.spot, placed.spot)) {
      moves.push({ row: placed.row, from: previous.spot, to: placed.spot });
    }
  }
  return moves;
}

// moveNotice is the words of the notice a list answer's moves leave, for at least one move. One moved row names the row
// and the place it moved to. More name their count and every row's name, in the order they moved.
export function moveNotice(moves: readonly Move[]): string {
  const [first] = moves;
  if (moves.length === 1) {
    return `Moved “${first.row.label}” to ${placeOf(first.to)}.`;
  }
  const names = moves.map((move) => `“${move.row.label}”`).join(", ");
  return `Moved ${moves.length} conversations: ${names}`;
}

// placeOf is the place a Spot names: its project's name for the tree, its project's ended line, or Archived.
function placeOf(spot: Spot): string {
  if (spot.line === "archived") {
    return "Archived";
  }
  if (spot.line === "ended") {
    return `${spot.name}’s ended line`;
  }
  return spot.name;
}

// sameSpot is whether two Spots are the same place: the same project, name and line.
function sameSpot(left: Spot, right: Spot): boolean {
  return left.project === right.project && left.name === right.name && left.line === right.line;
}
