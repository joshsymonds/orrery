import type { Conversation } from "./conversation";
import type { Epic, EpicQuestion } from "./gen/orrery/v1/epics_pb";
import { makerOf } from "./happenings";
import { parseQuestion, readQuestion } from "./question";
import type { Row } from "./rows";
import { lastPathElement } from "./titleparts";

// The kind of an Ask: a session's question, a session's or pane's permission, a pane's question, an epic's question, or
// an epic's park.
export type AskKind = "session" | "permission" | "pane" | "epic" | "park";

// One option of an Ask, numbered from 1. answerable is false past the ninth, which no key picks.
export interface AskOption {
  n: number;
  name: string;
  does: string;
  recommended: boolean;
  why: string;
  answerable: boolean;
}

// The call an answer, not-now, bring-back or decline makes: a pane's keys with the question hash they expect, or a
// headless session's open request with the options it serves.
export type AskTarget =
  | { kind: "pane"; id: string; questionHash: string; changed: number }
  | { kind: "headless"; id: string; requestId: string; options: string[] };

// An Ask as the client draws it and sends it: its words, its options, and the calls it makes. asked is the unix time the
// question began waiting. command is the tool's command a permission asks about, when it has one.
export interface AskModel {
  kind: AskKind;
  who: string;
  asked: number;
  question: string;
  command: { tool: string; text: string } | null;
  options: AskOption[];
  putOff: boolean;
  archive: { kind: "session" | "headless"; id: string } | null;
  target: AskTarget | null;
  epic: { id: string; questionId: string } | null;
  // escalation is the line that names who escalated an epic's question and why. It is absent while the question is not
  // escalated.
  escalation?: string;
}

// Only the first nine options can be picked by a key.
const lastKeyedOption = 9;

const secondsPerDay = 86400;

// The state a pane or session is in while a question waits on the owner.
const waitingState = "waiting";

// The three answers a pane permission offers, in the order keys 1, 2 and 3 send them.
const panePermissionOptions: { name: string; does: string }[] = [
  { name: "Yes", does: "" },
  { name: "Yes, and don’t ask again", does: "for commands like this one in this session" },
  { name: "No", does: "and say what to do instead, below" },
];

// conversationAsk is the Ask of a conversation row: a waiting pane's stored question, or a headless session's open request
// as its stream holds it. A row with no question has no Ask. putOff is the stream's: its open request's, or its pane's.
export function conversationAsk(row: Row, conversation: Conversation | null): AskModel | null {
  const state = conversation?.state;
  const putOff = (state?.request?.putOff ?? false) || (state?.pane?.putOff ?? false);
  const who = joinWords([lastPathElement(row.project), row.label]);
  const archive = archiveOf(row);

  if (row.kind === "pane") {
    if (row.state !== waitingState || row.question === "") {
      return null;
    }
    const shape = readQuestion(row.question);
    const common = {
      asked: row.changed,
      command: null,
      putOff,
      archive,
      target: { kind: "pane" as const, id: row.id, questionHash: row.questionHash, changed: row.changed },
      epic: null,
    };
    if (shape.kind === "askUser") {
      const parsed = parseQuestion(row.question);
      return {
        ...common,
        kind: "pane",
        who: shape.count > 1 ? `${who} · question 1 of ${shape.count}` : who,
        question: parsed.title,
        options: parsed.options.map((option, index) => optionOf(index + 1, option.label, option.description)),
      };
    }
    if (shape.kind === "permission") {
      return {
        ...common,
        kind: "permission",
        who,
        question: permissionQuestion(shape.tool, shape.detail),
        command: shape.detail === "" ? null : { tool: shape.tool, text: shape.detail },
        options: panePermissionOptions.map((option, index) => optionOf(index + 1, option.name, option.does)),
      };
    }
    // A plain-text question is a permission prompt Claude Code shows as text: the three permission answers answer it.
    return {
      ...common,
      kind: "permission",
      who,
      question: row.question,
      options: panePermissionOptions.map((option, index) => optionOf(index + 1, option.name, option.does)),
    };
  }

  const request = state?.request;
  if (request === undefined) {
    return null;
  }
  const permission = request.kind === "permission";
  return {
    kind: permission ? "permission" : "session",
    who,
    asked: row.changed,
    question: permission ? permissionQuestion(request.question, "") : request.question,
    command: null,
    options: request.options.map((label, index) => optionOf(index + 1, label)),
    putOff,
    archive,
    target: { kind: "headless", id: row.id, requestId: request.id, options: request.options },
    epic: null,
  };
}

// epicAsk is an epic's question: its choices in the order the planner declared them, with the recommended one's why.
// The owner's own answer, Something else, is not an option; the drawing adds it.
export function epicAsk(epic: Epic, question: EpicQuestion): AskModel {
  const recommendation = question.recommendation;
  const model: AskModel = {
    kind: "epic",
    who: joinWords([epicProject(epic), epic.slug]),
    asked: Number(question.asked),
    question: question.text,
    command: null,
    options: question.choices.map((choice, index) => {
      const recommended = recommendation !== undefined && recommendation.choice === choice.id;
      return optionOf(index + 1, choice.id, choice.does, recommended, recommended ? recommendation.why : "");
    }),
    putOff: false,
    archive: null,
    target: null,
    epic: { id: epic.id, questionId: question.id },
  };
  if (question.escalated > 0n) {
    const maker =
      question.escalationBy === "" ? "unknown" : makerOf(question.escalationBy, question.escalationSession, undefined);
    model.escalation = `escalated by ${maker}: ${question.escalationReason}`;
  }
  return model;
}

// parkAsk is an epic's park: its parked reason, with the one answer that runs the parked work again.
export function parkAsk(epic: Epic, parked: string): AskModel {
  return {
    kind: "park",
    who: joinWords([epicProject(epic), epic.slug]),
    asked: Number(epic.phaseAt),
    question: parked,
    command: null,
    options: [optionOf(1, "Retry", "Run the parked work again")],
    putOff: false,
    archive: null,
    target: null,
    epic: { id: epic.id, questionId: "" },
  };
}

// staleWords is how long a question has waited, in whole days, once it has waited a day or more, and null before that.
// A question with no asked time has no words.
export function staleWords(asked: number, now: number): string | null {
  if (asked <= 0) {
    return null;
  }
  const days = Math.floor((now - asked) / secondsPerDay);
  if (days < 1) {
    return null;
  }
  return days === 1 ? "1 day" : `${days} days`;
}

// permissionQuestion asks a tool's permission. A Bash command reads as a command to run, once there is one to show.
function permissionQuestion(tool: string, detail: string): string {
  return tool === "Bash" && detail !== "" ? "Run this command?" : `Use ${tool}?`;
}

// epicProject is the project an epic's Ask names: its repository's directory, or its repository id when it has no path.
export function epicProject(epic: Epic): string {
  const directory = lastPathElement(epic.path);
  return directory === "" ? epic.repository : directory;
}

// archiveOf is the Epics.Archive target of a row: none for a pane with no orrery id.
function archiveOf(row: Row): AskModel["archive"] {
  return row.archiveKind === "none" ? null : { kind: row.archiveKind, id: row.archiveId };
}

function optionOf(n: number, name: string, does = "", recommended = false, why = ""): AskOption {
  return { n, name, does, recommended, why, answerable: n <= lastKeyedOption };
}

// joinWords joins the parts that are not empty with the middle dot the Ask's who uses.
function joinWords(parts: string[]): string {
  return parts.filter((part) => part !== "").join(" · ");
}
