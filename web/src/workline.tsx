import type { JSX } from "preact";

import type { Pane } from "./gen/orrery/v1/orrery_pb";

// ToolWords is a tool in plain words, and whether it is a wait on background work.
interface ToolWords {
  text: string;
  background: boolean;
}

// PaneTool is the pane's current tool: its name, and the unix seconds it started.
interface PaneTool {
  name: string;
  start: number;
}

const waiting: ToolWords = { text: "waiting on background work", background: true };

const wordsByShortName = new Map<string, ToolWords>([
  ["Bash", { text: "running a command", background: false }],
  ["Read", { text: "reading a file", background: false }],
  ["Write", { text: "writing a file", background: false }],
  ["Edit", { text: "editing a file", background: false }],
  ["MultiEdit", { text: "editing a file", background: false }],
  ["NotebookEdit", { text: "editing a file", background: false }],
  ["Grep", { text: "searching code", background: false }],
  ["Glob", { text: "finding files", background: false }],
  ["WebFetch", { text: "reading a web page", background: false }],
  ["WebSearch", { text: "searching the web", background: false }],
  ["Agent", { text: "running an agent", background: false }],
  ["Task", { text: "running an agent", background: false }],
  ["TodoWrite", { text: "updating its plan", background: false }],
  ["Skill", { text: "loading a skill", background: false }],
  ["ToolSearch", { text: "loading tools", background: false }],
  ["AskUserQuestion", { text: "asking you", background: false }],
  ["step_wait", waiting],
  ["Monitor", waiting],
]);

// toolWords reads a tool name by the part after its last "__", so an MCP name such as mcp__orrery__step_wait reads as
// its short name. A name not in the table reads as that short name, not as a background wait.
function toolWords(name: string): ToolWords {
  const at = name.lastIndexOf("__");
  const shortName = at < 0 ? name : name.slice(at + 2);
  return wordsByShortName.get(shortName) ?? { text: shortName, background: false };
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

// WorkLine draws a working pane's line: the tool in plain words with its elapsed time, a background wait apart, and
// where a message sent now lands. It draws nothing for any other state. Every text is a text child, so no markup
// becomes an element.
export function WorkLine({ pane, now }: { pane: Pane | undefined; now: number }): JSX.Element | null {
  if (pane === undefined || pane.state !== "working") {
    return null;
  }
  const tool = currentTool(pane);
  const words = toolWords(tool?.name ?? "");
  const since = tool?.start ?? Number(pane.changed);
  return (
    <div class={words.background ? "work-line background" : "work-line"}>
      <span class="work-label">{words.background ? "background" : "working"}</span>
      <span class="work-tool">{words.text}</span>
      <span class="work-time">{clock(now - since)}</span>
      <span class="work-note">{note(pane, now)}</span>
    </div>
  );
}

// note says where a message sent now lands, and when the last turn ended once the pane has one.
function note(pane: Pane, now: number): string {
  const lands = "a message sent now lands at Claude's next step";
  if (pane.lastTurnEnd <= 0n) {
    return lands;
  }
  return `${lands} · last turn ended ${age(now - Number(pane.lastTurnEnd))} ago`;
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
