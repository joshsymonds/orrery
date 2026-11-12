import type { Pane, Session } from "./gen/orrery/v1/orrery_pb";

// One line of the list: a pane, or a headless session that names no pane.
export interface Row {
  key: string;
  // The pane's id (such as %3) for a pane row, the session's id for a session row.
  id: string;
  kind: "pane" | "session";
  label: string;
  project: string;
  host: string;
  state: string;
  summary: string;
  question: string;
  // The pane's question_hash, which answers a waiting question by hash. Empty for a session row.
  questionHash: string;
  changed: number;
  seen: number;
  unseen: boolean;
}

// A titled run of rows, in display order.
export interface Section {
  title: string;
  rows: Row[];
}

// Panes in a tmux session with this prefix are internal to orrery and are not listed.
const shellSessionPrefix = "_orrery-shell-";

// A session row with no label is named by this many characters of its id.
const sessionLabelLength = 8;

const sessionsTitle = "Sessions";

const stateWaiting = "waiting";
const stateWorking = "working";

// Row ranks, smallest first. Waiting always sorts first.
const rankWaiting = 0;
const rankUnseen = 1;
const rankWorking = 2;
const rankRest = 3;

export function rowsFrom(panes: Pane[], sessions: Session[]): Row[] {
  const rows: Row[] = [];
  for (const pane of panes) {
    if (pane.session.startsWith(shellSessionPrefix)) {
      continue;
    }
    const changed = Number(pane.changed);
    const seen = Number(pane.seen);
    rows.push({
      key: `pane:${pane.host}:${pane.id}`,
      id: pane.id,
      kind: "pane",
      label: pane.label || pane.session,
      project: pane.cwd,
      host: pane.host,
      state: pane.state,
      summary: pane.summary,
      question: pane.question,
      questionHash: pane.questionHash,
      changed,
      seen,
      unseen: isUnseen(pane.state, changed, seen),
    });
  }
  for (const session of sessions) {
    if (session.pane !== "") {
      continue;
    }
    const changed = parseSeconds(session.changed);
    const seen = parseSeconds(session.seen);
    rows.push({
      key: `session:${session.id}`,
      id: session.id,
      kind: "session",
      label: session.label || session.id.slice(0, sessionLabelLength),
      project: "",
      host: "",
      state: session.state,
      summary: session.summary,
      question: session.question,
      questionHash: "",
      changed,
      seen,
      unseen: isUnseen(session.state, changed, seen),
    });
  }
  return rows;
}

// Arranges rows into sections by attention, newest first within each section.
export function byAttention(rows: Row[]): Section[] {
  const sections: [number, string][] = [
    [rankWaiting, "Needs you"],
    [rankUnseen, "New since you looked"],
    [rankWorking, "Working"],
    [rankRest, "Seen"],
  ];
  const result: Section[] = [];
  for (const [sectionRank, title] of sections) {
    const sectionRows = rows
      .filter((row) => rank(row) === sectionRank)
      .sort((left, right) => right.changed - left.changed);
    if (sectionRows.length > 0) {
      result.push({ title, rows: sectionRows });
    }
  }
  return result;
}

// Arranges rows into one section per project directory, with session rows under Sessions.
export function byProject(rows: Row[]): Section[] {
  const projects = new Map<string, Row[]>();
  const sessionRows: Row[] = [];
  for (const row of rows) {
    if (row.kind === "session") {
      sessionRows.push(row);
      continue;
    }
    const group = projects.get(row.project);
    if (group) {
      group.push(row);
    } else {
      projects.set(row.project, [row]);
    }
  }

  const sections: Section[] = [...projects].map(([title, sectionRows]) => ({
    title,
    rows: sectionRows.sort(compareProjectRows),
  }));
  if (sessionRows.length > 0) {
    sections.push({ title: sessionsTitle, rows: sessionRows.sort(compareProjectRows) });
  }
  return sections.sort(
    (left, right) =>
      rank(left.rows[0]) - rank(right.rows[0]) || compareStrings(left.title, right.title),
  );
}

function isUnseen(state: string, changed: number, seen: number): boolean {
  return state !== "" && changed > seen;
}

function rank(row: Row): number {
  if (row.state === stateWaiting) {
    return rankWaiting;
  }
  if (row.unseen) {
    return rankUnseen;
  }
  if (row.state === stateWorking) {
    return rankWorking;
  }
  return rankRest;
}

function compareProjectRows(left: Row, right: Row): number {
  return (
    rank(left) - rank(right) ||
    compareStrings(left.host, right.host) ||
    compareStrings(left.label, right.label) ||
    compareStrings(left.key, right.key)
  );
}

// parseSeconds reads a decimal session timestamp; anything non-numeric reads as zero.
function parseSeconds(value: string): number {
  return /^\d+$/.test(value) ? Number(value) : 0;
}

function compareStrings(left: string, right: string): number {
  if (left < right) {
    return -1;
  }
  return left > right ? 1 : 0;
}
