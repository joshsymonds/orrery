import type { Conversation } from "./conversation";
import type { DockFacts, DockKind, DockTool } from "./dockwords";
import type { StepItem } from "./epics";
import type { ConversationItem, Pane } from "./gen/orrery/v1/orrery_pb";
import type { Row } from "./rows";
import { lastPathElement } from "./titleparts";

// The facts the dock reads, built from a row, its conversation stream and the page's interrupt mark. The stream says
// what is happening; the mark only adds an interrupt the stream has not yet shown.

// paneTool reads the pane's tool JSON, {name, input, start}. An empty or unparsable value, or one that is not a JSON
// object, means no tool. A pane's input is its arg when it is a string, and empty otherwise.
function paneTool(pane: Pane): DockTool | null {
  if (pane.tool === "") {
    return null;
  }
  let json: unknown;
  try {
    json = JSON.parse(pane.tool);
  } catch {
    return null;
  }
  if (typeof json !== "object" || json === null || Array.isArray(json)) {
    return null;
  }
  const fields = json as Record<string, unknown>;
  return {
    name: typeof fields.name === "string" ? fields.name : "",
    arg: typeof fields.input === "string" ? fields.input : "",
    start: typeof fields.start === "number" ? fields.start : 0,
  };
}

// runningCall is a session's running tool call: walking back over the trailing tool and result items, the newest tool
// item that no later result answers. Any other kind of item ends the walk.
function runningCall(items: readonly ConversationItem[]): DockTool | null {
  const answered = new Set<string>();
  for (let at = items.length - 1; at >= 0; at--) {
    const current = items[at];
    if (current.kind === "result") {
      answered.add(current.id);
    } else if (current.kind !== "tool") {
      return null;
    } else if (!answered.has(current.id)) {
      return { name: current.tool, arg: current.input, start: Number(current.time) };
    }
  }
  return null;
}

// interruptedAt is the newest item's time when that item is an interrupt, else 0.
function interruptedAt(items: readonly ConversationItem[]): number {
  const newest = items.at(-1);
  return newest?.kind === "interrupted" ? Number(newest.time) : 0;
}

// firstSet is the first time that is set, or 0 when none is.
function firstSet(...times: (bigint | undefined)[]): number {
  const set = times.find((time) => time !== undefined && time > 0n);
  return set === undefined ? 0 : Number(set);
}

export function rowFacts(row: Row, conversation: Conversation | null, interrupting: boolean): DockFacts {
  const streamState = conversation?.state;
  const streamPane = streamState?.pane;
  const streamSession = streamState?.session;
  const items = conversation?.items ?? [];
  const isPane = row.kind === "pane";
  const kind: DockKind = isPane ? (streamPane?.claudeSession === "" ? "shell" : "pane") : "session";
  const tool: DockTool | null = isPane ? (streamPane === undefined ? null : paneTool(streamPane)) : runningCall(items);
  return {
    kind,
    name: lastPathElement(row.project) || row.label,
    state: streamPane?.state || streamSession?.state || row.state,
    interrupting: interrupting || (streamState?.interrupting ?? false),
    interruptingSince: Number(streamState?.interruptingSince ?? 0n),
    turnSince: Number(streamState?.turnSince ?? 0n),
    tool,
    changed: firstSet(streamPane?.changed, streamSession?.changed) || row.changed,
    reason: isPane ? "" : row.summary,
    interruptedAt: interruptedAt(items),
    putOff: (streamState?.request?.putOff ?? false) || (streamPane?.putOff ?? false),
    archived: row.archived,
    step: null,
  };
}

export function stepFacts(
  step: Pick<StepItem, "label" | "held" | "started" | "doing">,
  conversation: Conversation | null,
): DockFacts {
  return {
    kind: "step",
    name: step.label,
    state: "working",
    interrupting: false,
    interruptingSince: 0,
    turnSince: step.started,
    tool: runningCall(conversation?.items ?? []),
    changed: 0,
    reason: "",
    interruptedAt: 0,
    putOff: false,
    archived: false,
    step: { label: step.label, held: step.held, doing: step.doing },
  };
}
