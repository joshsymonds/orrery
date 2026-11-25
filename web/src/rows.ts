import type { HeadlessSession, Pane, Session } from "./gen/orrery/v1/orrery_pb";

// The kind Epics.Archive takes to archive a row: session for a Sessions.List row, and for a pane with an orrery_id,
// which archives as the session that id names; headless for a headless session; and none for a pane with no
// orrery_id, which has no archive control.
export type ArchiveKind = "session" | "headless" | "none";

// One line of the list: a pane, or a headless session that names no pane.
export interface Row {
  key: string;
  // The pane's id (such as %3) for a pane row, the session's id for a session row.
  id: string;
  // The id Epics.Archive and Epics.Unarchive take with archiveKind: the pane's orrery_id for a pane row, the
  // session's id for a session or headless row, and empty for a pane with no orrery_id.
  archiveId: string;
  kind: "pane" | "session";
  // The row's line, which its title draws after its directory: a pane's summary, a headless session's served headline,
  // or a Sessions.List row's label, which reads 'session' when it is empty.
  label: string;
  // The directory the row runs in, which its title draws as the last element of its path. Empty for a Sessions.List row.
  project: string;
  host: string;
  state: string;
  summary: string;
  question: string;
  // The pane's question_hash, which answers a waiting question by hash. Empty for a session row.
  questionHash: string;
  // The pane's running tool: its @orrery_tool value as Panes.List gives it, a JSON object with name, input and start, or
  // empty. Empty for a session or headless row.
  tool: string;
  changed: number;
  seen: number;
  unseen: boolean;
  // Whether the owner archived the row. A pane is archived when the Session with its orrery_id is archived, and a pane
  // with no orrery_id, or whose Session is not in the answer, is not.
  archived: boolean;
  // Whether the owner put the row's question off: a pane's Pane.putOff, a headless session's HeadlessSession.putOff,
  // and always false for a Sessions.List row. A new question reads false again.
  putOff: boolean;
  archiveKind: ArchiveKind;
  // Whether serve reports the row's session as ended: a pane's Session with its orrery_id, a Sessions.List row's
  // Session, or a headless session's HeadlessSession. Absent reads as false.
  ended?: boolean;
  // The unix seconds serve's ended_at gives when the session ended, or 0 while it runs or when unknown.
  // Absent reads as 0.
  endedAt?: number;
  // Whether the pane is unbound (Pane.unbound). Absent reads as false, and always false for a session or headless row.
  unbound?: boolean;
}

// Panes in a tmux session with this prefix are internal to orrery and are not listed.
const shellSessionPrefix = "_orrery-shell-";

// A Sessions.List row with no label reads this word as its line.
const sessionLabel = "session";

// rowsFrom makes a row of each pane outside orrery's own shells that has a conversation or is unbound, of each session
// that names no pane, and of each headless session. A pane with no claudeSession and not unbound is a plain shell and
// makes no row. A row's project is its directory and its label is its line: a pane's summary, a headless session's served
// headline, or a Sessions.List row's label. A session row is driven by its id, through the session row path,
// Sessions.WatchConversation and Control.
export function rowsFrom(panes: Pane[], sessions: Session[], headless: HeadlessSession[] = []): Row[] {
  const archivedSessions = new Map(sessions.map((session) => [session.id, session.archived]));
  const sessionsById = new Map(sessions.map((session) => [session.id, session]));
  const rows: Row[] = [];
  for (const pane of panes) {
    if (pane.session.startsWith(shellSessionPrefix)) {
      continue;
    }
    if (pane.claudeSession === "" && !pane.unbound) {
      continue;
    }
    const changed = Number(pane.changed);
    const seen = Number(pane.seen);
    const archivable = pane.orreryId !== "";
    const owner = archivable ? sessionsById.get(pane.orreryId) : undefined;
    rows.push({
      key: `pane:${pane.host}:${pane.id}`,
      id: pane.id,
      archiveId: pane.orreryId,
      kind: "pane",
      label: pane.summary,
      project: pane.cwd,
      host: pane.host,
      state: pane.state,
      summary: pane.summary,
      question: pane.question,
      questionHash: pane.questionHash,
      tool: pane.tool,
      changed,
      seen,
      unseen: isUnseen(pane.state, changed, seen),
      archived: archivable && archivedSessions.get(pane.orreryId) === true,
      putOff: pane.putOff,
      archiveKind: archivable ? "session" : "none",
      ended: owner?.ended ?? false,
      endedAt: owner ? Number(owner.endedAt) : 0,
      unbound: pane.unbound,
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
      archiveId: session.id,
      kind: "session",
      label: session.label || sessionLabel,
      project: "",
      host: "",
      state: session.state,
      summary: session.summary,
      question: session.question,
      questionHash: "",
      tool: "",
      changed,
      seen,
      unseen: isUnseen(session.state, changed, seen),
      archived: session.archived,
      putOff: false,
      archiveKind: "session",
      ended: session.ended,
      endedAt: Number(session.endedAt),
      unbound: false,
    });
  }
  for (const session of headless) {
    const changed = Number(session.changed);
    const seen = Number(session.seen);
    rows.push({
      key: `session:${session.id}`,
      id: session.id,
      archiveId: session.id,
      kind: "session",
      label: session.headline,
      project: session.dir,
      host: "",
      state: session.state,
      summary: session.summary,
      question: session.question,
      questionHash: "",
      tool: "",
      changed,
      seen,
      unseen: isUnseen(session.state, changed, seen),
      archived: session.archived,
      putOff: session.putOff,
      archiveKind: "headless",
      ended: session.ended,
      endedAt: Number(session.endedAt),
      unbound: false,
    });
  }
  return rows;
}

// waitsOnYou reports whether a row's question waits on the owner: the row is waiting, not archived, and not put off.
export function waitsOnYou(row: Row): boolean {
  return row.state === "waiting" && !row.archived && !row.putOff;
}

function isUnseen(state: string, changed: number, seen: number): boolean {
  return state !== "" && changed > seen;
}

// parseSeconds reads a decimal session timestamp; anything non-numeric reads as zero.
function parseSeconds(value: string): number {
  return /^\d+$/.test(value) ? Number(value) : 0;
}
