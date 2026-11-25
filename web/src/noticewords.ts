import { putOffWords } from "./dockwords";
import { type MarkDetail, markDetailOf } from "./epicrow";
import type { NoticeBlock, NoticeKey } from "./gen/orrery/v1/notice_pb";
import { stateWord } from "./glyphs";
import { quietRow, stateWaiting } from "./inboxrow";
import type { Row } from "./rows";
import { lastPathElement } from "./titleparts";
import type { EpicNode, Project } from "./tree";

// noticeKey names a notice's key as one string over all five fields in order, so a key that sets a different field is a
// different notice. It is the same string web/src/notices.tsx names a key by.
export function noticeKey(key: NoticeKey | undefined): string {
  return JSON.stringify([key?.pane ?? "", key?.session ?? "", key?.epic ?? "", key?.limit ?? "", key?.alert ?? ""]);
}

// NoticeFields is what a notice says: what it is about, its state, its server title and the block of its epic it opens on.
export interface NoticeFields {
  key?: NoticeKey;
  state: string;
  title: string;
  block?: NoticeBlock;
}

// NoticeLead is what a notice's first line is led by: an epic's mark, a pane's or session's state glyph, or an icon.
export type NoticeLead = { kind: "mark"; detail: MarkDetail } | { kind: "glyph"; state: string } | { kind: "icon"; icon: "bell" | "clock" };

// NoticeHead is a notice's first line: its lead, its directory or slug, and what in words.
export interface NoticeHead {
  lead: NoticeLead;
  dir: string;
  what: string;
}

// The words an epic's notice says of its block kind. A block of any other kind, or none, waits on you.
const blockWords: Record<string, string> = {
  question: "asks you a question",
  release_step: "a release step failed",
  report: "finished",
  contract: "has a contract for you",
};
const waitsOnYou = "waits on you";

// The notice's subject: the epic it is about, a pane or session row, a usage limit, or otherwise an alert.
type NoticeSubject = "epic" | "row" | "limit" | "alert";

// subjectOf is the subject a notice's key names. A key that names none of the others is an alert.
function subjectOf(key: NoticeKey | undefined): NoticeSubject {
  if (key?.epic) {
    return "epic";
  }
  if (key?.pane || key?.session) {
    return "row";
  }
  return key?.limit ? "limit" : "alert";
}

// epicNodeOf finds the listed epic whose id is id, among the conversations in the tree, with the ended epics folded under
// them, and behind the ended lines.
function epicNodeOf(projects: readonly Project[], id: string): EpicNode | undefined {
  return projects
    .flatMap((project) => [...project.convos, ...project.ended])
    .flatMap((convo) => [...convo.hung, ...convo.folded])
    .find((hung): hung is EpicNode => hung.kind === "epic" && hung.block.epic.id === id);
}

// listedRowOf finds the listed row a notice's pane or session names. A session notice names the row whose id is its session.
function listedRowOf(rows: readonly Row[], key: NoticeKey | undefined): Row | undefined {
  const pane = key?.pane ?? "";
  const session = key?.session ?? "";
  return rows.find((row) => (row.kind === "pane" ? pane !== "" && row.id === pane : session !== "" && row.id === session));
}

// quietRowOf is the listed row a notice reads quiet from: a notice that says waiting whose listed row is quiet (quietRow).
// A notice of another state keeps its own state, whatever its row is now. The head and noticeAct both read this one rule,
// so a notice and the inbox never disagree about whether something waits on the owner.
function quietRowOf(notice: NoticeFields, rows: readonly Row[]): Row | undefined {
  const listed = listedRowOf(rows, notice.key);
  return notice.state === stateWaiting && listed !== undefined && quietRow(listed) ? listed : undefined;
}

// quietWordsOf is what a listed row that does not wait on the owner says: its put-off words when its owner put it off, else
// the idle state's word. An archived row that is also put off reads put off.
function quietWordsOf(row: Row): string {
  return row.putOff ? putOffWords : stateWord("idle");
}

// epicLead is an epic's lead: its mark, drawn from the detail its inbox row reads.
function epicLead(node: EpicNode): NoticeLead {
  return { kind: "mark", detail: markDetailOf(node.block.epic.detail) };
}

// noticeHead writes a notice's first line. An epic's line is led by its mark and named by its slug, or by a bell and the
// text after the last slash of its id when no listed project holds it; its words are its block's. A pane's or session's
// is led by its state glyph and its state's word, and names the directory its listed row runs in, or the title before
// its separator when no listed row names one. A limit is led by a clock and an alert by a bell, and each reads its
// title. A session's id never appears.
export function noticeHead(notice: NoticeFields, projects: readonly Project[], rows: readonly Row[]): NoticeHead {
  const key = notice.key;
  switch (subjectOf(key)) {
    case "epic": {
      const id = key?.epic ?? "";
      const node = epicNodeOf(projects, id);
      const words = blockWords[notice.block?.kind ?? ""] ?? waitsOnYou;
      if (node === undefined) {
        return { lead: { kind: "icon", icon: "bell" }, dir: "", what: `${id.slice(id.lastIndexOf("/") + 1)} ${words}` };
      }
      return { lead: epicLead(node), dir: "", what: `${node.listed.slug} ${words}` };
    }
    case "row": {
      const listed = listedRowOf(rows, key);
      const quiet = quietRowOf(notice, rows);
      const separator = notice.title.indexOf(" · ");
      const titled = separator === -1 ? notice.title : notice.title.slice(0, separator);
      const dir = listed === undefined ? "" : lastPathElement(listed.project);
      return {
        lead: { kind: "glyph", state: quiet !== undefined ? "idle" : notice.state },
        dir: dir !== "" ? dir : titled,
        what: quiet !== undefined ? quietWordsOf(quiet) : stateWord(notice.state),
      };
    }
    case "limit":
      return { lead: { kind: "icon", icon: "clock" }, dir: "", what: notice.title };
    case "alert":
      return { lead: { kind: "icon", icon: "bell" }, dir: "", what: notice.title };
  }
}

// NoticeAct is a notice's primary control: Answer, Open, or none.
export type NoticeAct = "Answer" | "Open" | null;

// noticeAct is the control a notice draws. An epic whose block is a question, and a pane or session waiting on you, are
// answered; a pane or session whose listed row is waiting but does not wait on you (quietRow) is opened, as the inbox
// draws that row quiet. A limit and an alert have none. Anything else is opened.
export function noticeAct(notice: NoticeFields, rows: readonly Row[]): NoticeAct {
  switch (subjectOf(notice.key)) {
    case "epic":
      return notice.block?.kind === "question" ? "Answer" : "Open";
    case "row":
      return notice.state === stateWaiting && quietRowOf(notice, rows) === undefined ? "Answer" : "Open";
    case "limit":
    case "alert":
      return null;
  }
}

// bringBackRowOf is the listed row a notice's Bring it back takes: a pane that is put off and waiting. A headless session
// names no pane, and its Control.BringBack needs the open request id its row does not hold, so a session has none.
export function bringBackRowOf(notice: NoticeFields, rows: readonly Row[]): Row | undefined {
  const listed = listedRowOf(rows, notice.key);
  return listed !== undefined && listed.kind === "pane" && listed.putOff && listed.state === stateWaiting ? listed : undefined;
}

// Seconds in a minute, an hour and a day.
const minute = 60;
const hour = 3600;
const day = 86400;

// noticeAge is how long ago a notice was timed, at now in unix seconds: "now" under a minute or in the future, then whole
// minutes, hours and days.
export function noticeAge(time: bigint, now: number): string {
  const elapsed = now - Number(time);
  if (elapsed < minute) {
    return "now";
  }
  if (elapsed < hour) {
    return `${Math.floor(elapsed / minute)}m`;
  }
  if (elapsed < day) {
    return `${Math.floor(elapsed / hour)}h`;
  }
  return `${Math.floor(elapsed / day)}d`;
}

// noticeClock is a notice's time of day: 24-hour, two digits each side of the colon, in timeZone, or the viewer's zone
// when none is given. It is formatted in English, so its digits and colon are the same for every viewer.
export function noticeClock(time: bigint, timeZone?: string): string {
  return new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone }).format(
    new Date(Number(time) * 1000),
  );
}

// NoticeDay is one day's heading and the rows timed on it, in the order given.
export interface NoticeDay<T> {
  head: string;
  rows: T[];
}

// dayOrdinal is the calendar day, in timeZone, of the instant seconds in unix seconds, as a count of days since the epoch.
// Counting days in UTC keeps the difference of two days exact across a change of clock.
function dayOrdinal(seconds: number, timeZone?: string): number {
  const iso = new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit", timeZone }).format(
    new Date(seconds * 1000),
  );
  const [year, month, dayOfMonth] = iso.split("-").map(Number);
  return Date.UTC(year, month - 1, dayOfMonth) / (day * 1000);
}

// dayHead is the heading of a day: Today, Yesterday, or else its weekday, day and month, such as Monday 16 November.
function dayHead(seconds: number, today: number, ordinal: number, timeZone?: string): string {
  const ago = today - ordinal;
  if (ago === 0) {
    return "Today";
  }
  if (ago === 1) {
    return "Yesterday";
  }
  return new Intl.DateTimeFormat("en-GB", { weekday: "long", day: "numeric", month: "long", timeZone }).format(
    new Date(seconds * 1000),
  );
}

// noticeDays groups rows, which are newest first, by their calendar day in timeZone, or the viewer's zone when none is
// given. now, in unix seconds, is the day Today names.
export function noticeDays<T extends { time: bigint }>(rows: readonly T[], now: number, timeZone?: string): NoticeDay<T>[] {
  const today = dayOrdinal(now, timeZone);
  const days: NoticeDay<T>[] = [];
  let group: { ordinal: number; day: NoticeDay<T> } | undefined;
  for (const row of rows) {
    const seconds = Number(row.time);
    const ordinal = dayOrdinal(seconds, timeZone);
    if (group === undefined || group.ordinal !== ordinal) {
      group = { ordinal, day: { head: dayHead(seconds, today, ordinal, timeZone), rows: [] } };
      days.push(group.day);
    }
    group.day.rows.push(row);
  }
  return days;
}
