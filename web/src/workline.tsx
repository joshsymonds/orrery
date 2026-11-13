import type { JSX } from "preact";

import type { Pane } from "./gen/orrery/v1/orrery_pb";

// PaneTool is the pane's current tool: its name, and the unix seconds it started.
interface PaneTool {
  name: string;
  start: number;
}

// shortName reads a tool name by the part after its last "__", so an MCP name such as mcp__orrery__step_wait reads as
// its short name.
function shortName(name: string): string {
  const at = name.lastIndexOf("__");
  return at < 0 ? name : name.slice(at + 2);
}

// currentTool reads the pane's tool JSON. An empty or unparsable value, or one that is not a JSON object, means no tool.
function currentTool(pane: Pane): PaneTool | null {
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
    start: typeof fields.start === "number" ? fields.start : 0,
  };
}

// note says where a message sent now lands, and when the last turn ended once the pane has one.
function note(pane: Pane, now: number): string {
  const lands = "a message sent now lands at Claude's next step";
  if (pane.lastTurnEnd <= 0n) {
    return lands;
  }
  return `${lands} · last turn ended ${age(now - Number(pane.lastTurnEnd))} ago`;
}

// Tone is a pill's colour: the state it shows, with ended for a run that failed or was stopped.
type Tone = "working" | "waiting" | "held" | "done" | "interrupted" | "ended" | "idle";

// PillModel is what a pill shows: its tone, its word, its tool's short name when it has one, and the unix seconds its
// clock runs from when it has a clock.
export interface PillModel {
  tone: Tone;
  word: string;
  tool: string;
  since?: number;
}

// rowPill is the pill a row shows. A pane given speaks for the row: a working pane reads its tool and the unix seconds
// the tool started, or else its last change. Any other pane, and no pane, reads its state.
export function rowPill(state: string, pane?: Pane): PillModel {
  if (pane !== undefined && pane.state === "working") {
    const tool = currentTool(pane);
    return {
      tone: "working",
      word: "working",
      tool: shortName(tool?.name ?? ""),
      since: tool?.start ?? Number(pane.changed),
    };
  }
  return stateModel(pane?.state ?? state);
}

// stepPill is the pill a step shows: held with no clock, or running from when it started.
export function stepPill(held: boolean, started: number): PillModel {
  if (held) {
    return { tone: "held", word: "held", tool: "" };
  }
  return { tone: "working", word: "running", tool: "", since: started };
}

// stateModel is the pill a session state shows, with no tool and no clock.
function stateModel(state: string): PillModel {
  switch (state) {
    case "working":
      return { tone: "working", word: "working", tool: "" };
    case "waiting":
    case "done":
    case "interrupted":
      return { tone: state, word: state, tool: "" };
    case "failed":
    case "stopped":
      return { tone: "ended", word: state, tool: "" };
    default:
      return { tone: "idle", word: state === "" ? "idle" : state, tool: "" };
  }
}

// Pill draws a pill: its word, its tool in a bold run when it has one, and its clock when it has one. A working pill
// carries a spinner glyph beside its word. Every text is a text child.
export function Pill({ model, now }: { model: PillModel; now: number }): JSX.Element {
  return (
    <span class={`pill tone-${model.tone}`}>
      {model.tone === "working" ? <span class="g g-working" aria-hidden="true" /> : null}
      <span class="word">{model.word}</span>
      {model.tool !== "" ? <b class="tool">{model.tool}</b> : null}
      {model.since !== undefined ? <span class="clock">{clock(now - model.since)}</span> : null}
    </span>
  );
}

// WorkNote draws a working pane's note, where a message sent now lands. It draws nothing for any other pane.
export function WorkNote({ pane, now }: { pane: Pane | undefined; now: number }): JSX.Element | null {
  if (pane === undefined || pane.state !== "working") {
    return null;
  }
  return <p class="how work-note">{note(pane, now)}</p>;
}

// clock is elapsed time as m:ss, or h:mm:ss from an hour. It never reads negative.
function clock(seconds: number): string {
  const total = Math.max(seconds, 0);
  const hours = Math.floor(total / 3_600);
  const minutes = Math.floor((total % 3_600) / 60);
  const secs = total % 60;
  const pad = (value: number): string => String(value).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(secs)}` : `${minutes}:${pad(secs)}`;
}

// age is the largest whole unit: seconds, minutes, hours, then days.
function age(seconds: number): string {
  const total = Math.max(seconds, 0);
  if (total < 60) {
    return `${total}s`;
  }
  if (total < 3_600) {
    return `${Math.floor(total / 60)}m`;
  }
  if (total < 86_400) {
    return `${Math.floor(total / 3_600)}h`;
  }
  return `${Math.floor(total / 86_400)}d`;
}
