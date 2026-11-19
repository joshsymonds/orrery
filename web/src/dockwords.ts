import { clock } from "./reader";

// The words the web dock says, as pure functions of its facts. Times are unix seconds. Android says the same words in
// ui/DockWords.kt and ui/DockTrayWords.kt.

export type DockKind = "session" | "pane" | "shell" | "step";

export interface DockTool {
  name: string;
  arg: string;
  start: number; // unix seconds, 0 when unknown
}

export interface DockFacts {
  kind: DockKind;
  name: string;
  state: string;
  interrupting: boolean;
  interruptingSince: number; // unix seconds, 0 when unknown
  turnSince: number; // unix seconds, 0 when unset; a step's started time for a step
  tool: DockTool | null;
  changed: number; // unix seconds of the state's last change, 0 when unknown
  reason: string;
  interruptedAt: number; // unix seconds of the newest item when it is the interrupted rule, else 0
  putOff: boolean;
  step: { label: string; held: boolean } | null;
}

export type DockControl = "interrupt" | "interrupting" | "resume" | "none";

export interface StateLine {
  tone: string;
  word: string;
  tool: string;
  what: string;
  clock: string;
  control: DockControl;
}

export interface TrayRowFacts {
  waits: string;
  cancelled: boolean;
}

const secondsPerMinute = 60;
const secondsPerHour = 3_600;
const minutesPerHour = 60;
const hoursPerDay = 24;

// elapsed is m:ss, or h:mm:ss from an hour. It never reads negative.
export function elapsed(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(total / secondsPerHour);
  const minutes = Math.floor((total % secondsPerHour) / secondsPerMinute);
  const secs = total % secondsPerMinute;
  const pad = (value: number): string => String(value).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(secs)}` : `${minutes}:${pad(secs)}`;
}

// ago is "just now" under a minute, else the largest whole unit with " ago".
export function ago(changed: number, now: number): string {
  const seconds = Math.max(0, now - changed);
  if (seconds < secondsPerMinute) {
    return "just now";
  }
  const minutes = Math.floor(seconds / secondsPerMinute);
  if (minutes < minutesPerHour) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / minutesPerHour);
  if (hours < hoursPerDay) {
    return `${hours}h ago`;
  }
  return `${Math.floor(hours / hoursPerDay)}d ago`;
}

// what joins the non-empty parts with " · ", led by "· ". No parts gives "".
function what(parts: readonly string[]): string {
  const kept = parts.filter((part) => part !== "");
  return kept.length === 0 ? "" : `· ${kept.join(" · ")}`;
}

// since is the elapsed time from a start, or "" when the start is unknown.
function since(from: number, now: number): string {
  return from > 0 ? elapsed(now - from) : "";
}

// agoPart is the age of a change, or "" when the change time is unknown.
function agoPart(changed: number, now: number): string {
  return changed > 0 ? ago(changed, now) : "";
}

// firstSet is the first time that is known, or 0 when none is.
function firstSet(...times: number[]): number {
  return times.find((time) => time > 0) ?? 0;
}

// shortTool is the part of a tool's name after its last "__".
function shortTool(tool: DockTool | null): string {
  if (tool === null) {
    return "";
  }
  const at = tool.name.lastIndexOf("__");
  return at < 0 ? tool.name : tool.name.slice(at + 2);
}

// plain is a state line with no tool, clock or control beyond the given ones.
function plain(tone: string, word: string, parts: readonly string[] = [], control: DockControl = "none"): StateLine {
  return { tone, word, tool: "", what: what(parts), clock: "", control };
}

// running is a working line that names the tool and its arg, or "thinking" when there is none.
function running(word: string, facts: DockFacts, clockText: string): StateLine {
  return {
    tone: "working",
    word,
    tool: shortTool(facts.tool),
    what: facts.tool?.arg || "thinking",
    clock: clockText,
    control: "interrupt",
  };
}

export function stateLine(facts: DockFacts, now: number): StateLine {
  if (facts.kind === "step" && facts.step !== null) {
    if (facts.step.held) {
      return plain("held", "held");
    }
    return running("running", facts, since(facts.turnSince, now));
  }
  if (facts.interrupting && facts.state === "working") {
    return {
      ...plain("interrupting", "interrupting…", [], "interrupting"),
      what: "waiting for the turn to end",
      clock: since(facts.interruptingSince, now),
    };
  }
  if (facts.state === "working") {
    const started = firstSet(facts.turnSince, facts.tool?.start ?? 0, facts.changed);
    return running("working", facts, since(started, now));
  }
  if (facts.state === "waiting") {
    return facts.putOff ? plain("idle", "waiting on a question you put off") : plain("waiting", "waiting on you");
  }
  if (facts.state === "done" && facts.interruptedAt > 0) {
    return plain("interrupted", "interrupted by you", [clock(BigInt(facts.interruptedAt))]);
  }
  if (facts.state === "done") {
    return plain("done", "done", [agoPart(facts.changed, now)]);
  }
  if (facts.state === "interrupted") {
    return plain("interrupted", "cut off", [facts.reason, agoPart(facts.changed, now)], "resume");
  }
  if (facts.state === "failed") {
    return plain("failed", "failed", [facts.reason, agoPart(facts.changed, now)]);
  }
  if (facts.state === "stopped") {
    return plain("stopped", "stopped", [agoPart(facts.changed, now)]);
  }
  return plain("idle", "idle");
}

// placeholder is the words of the box's empty field. A narrow dock says the short queue prompt.
export function placeholder(facts: DockFacts, narrow: boolean): string {
  const n = facts.name;
  if (facts.kind === "shell") {
    return "Type a command…";
  }
  if (facts.kind === "step" && facts.step !== null) {
    const { label, held } = facts.step;
    return held
      ? `Nudge ${label}. It resumes with your words`
      : `Nudge ${label}. It interrupts the turn, then gives it your words`;
  }
  if (facts.interrupting && facts.state === "working") {
    return `Stopping ${n}…`;
  }
  if (facts.state === "working") {
    if (narrow) {
      return `Queue for ${n}`;
    }
    return facts.kind === "pane"
      ? `Queue a message for ${n}. Claude Code takes it when it can`
      : `Queue a message for ${n}. Claude takes it at its next tool result`;
  }
  if (facts.state === "waiting") {
    return `Answer ${n}`;
  }
  if (facts.state === "done" && facts.interruptedAt > 0) {
    return `${n} stopped where it was. Say what to do next`;
  }
  if (facts.state === "interrupted") {
    return `${n} was cut off. Resume it, or say what to do next`;
  }
  if (facts.state === "failed") {
    return `${n} failed. A message starts it again`;
  }
  if (facts.state === "stopped") {
    return `${n} is stopped. A message starts it again`;
  }
  return `Message ${n}`;
}

export function trayLabel(count: number): string {
  return `Queued ${count}`;
}

// trayWhy says what the tray's rows will do. A session counts only the rows not cancelled.
export function trayWhy(kind: "session" | "pane", rows: readonly TrayRowFacts[]): string {
  const live = rows.filter((row) => !row.cancelled);
  if (kind === "pane") {
    return live.length > 0 ? "Claude Code takes them when it chooses" : "dropped when the turn was cancelled";
  }
  if (live.length === 0) {
    return "dropped when the session ended";
  }
  const starts = live.filter((row) => row.waits === "start").length;
  if (starts === 1) {
    return "Claude takes it as its next turn";
  }
  if (starts > 1) {
    return "Claude takes them as its next turns";
  }
  return "Claude takes them at its next tool result";
}

// waitsLine counts the pending events that wait on you. A request counts only when a question was put off.
export function waitsLine(pending: readonly { waits: string }[], putOff: boolean): string {
  const speak = pending.filter((event) => event.waits === "speak").length;
  if (speak > 0) {
    return speak === 1 ? "1 event waits until you speak" : `${speak} events wait until you speak`;
  }
  const request = putOff ? pending.filter((event) => event.waits === "request").length : 0;
  if (request > 0) {
    return request === 1
      ? "1 event waits on the question you put off"
      : `${request} events wait on the question you put off`;
  }
  return "";
}

export const dockLines = {
  sendRefused: "Not sent. Your draft is kept; send again.",
  sendUnknown: "No answer from the server. It may have been sent: check the conversation before sending again.",
  alreadyTook: "Claude already took it in.",
  takeBackFailed: "Could not take it back.",
  takeAllBackFailed: "Could not take them back.",
  backInBox: (count: number): string =>
    count === 1 ? "Your queued message is back in the box." : `Your ${count} queued messages are back in the box.`,
  sendNowHeld: (count: number): string =>
    count === 1
      ? "1 message was not sent again. It is back in the box."
      : `${count} messages were not sent again. They are back in the box.`,
  resumeRefused: (message: string): string => `Not resumed: ${message}`,
  resumeUnknown: "No answer from the server. It may have resumed: check before trying again.",
  notDelivered: "not delivered",
};
