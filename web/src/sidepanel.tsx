import type { ComponentChildren } from "preact";

import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";
import { clock } from "./workline";

// SidePanel draws one opened thing as a card beside the conversation. Its top edge takes the tone of the lit tile it opened
// from, and its controls are Open in a new window and Close. Every thing opens in this one frame, so it has no width or
// kind of its own.
export function SidePanel({
  label,
  tone,
  href,
  onClose,
  children,
}: {
  label: string;
  tone: string;
  href: string;
  onClose: () => void;
  children?: ComponentChildren;
}) {
  return (
    <aside class="page side rpanel" aria-label={label}>
      <div class={`rcard ${tone}`}>
        <div class="panel-ctl">
          <a href={href} target="_blank" rel="noopener" aria-label="Open in a new window" title="Open in a new window">
            ↗
          </a>
          <button type="button" aria-label="Close the panel" title="Close  ]" onClick={onClose}>
            ×
          </button>
        </div>
        {children}
      </div>
    </aside>
  );
}

// lists gives each list its head, its lead line and the tone of its rows: agents are live, and shells left running are grey.
const lists = {
  agents: { head: "Sub-agents", lead: "These run for this conversation.", tone: "live" },
  running: {
    head: "Left running",
    lead: "Nothing waits on these, and the session can be done while they run.",
    tone: "quiet",
  },
} as const;

// listHead is the name a list's panel goes by: "Sub-agents" or "Left running".
export function listHead(kind: "agents" | "running"): string {
  return lists[kind].head;
}

// ListPanel draws a conversation's running sub-agents, or its shells left running, as one row per task it is given: its
// description, its activity, and its clock from when it started. An agent row leads its activity with its sub-agent type.
// headless leaves out the head, for a list in a sheet whose title already names it.
export function ListPanel({
  kind,
  tasks,
  now,
  headless = false,
}: {
  kind: "agents" | "running";
  tasks: BackgroundTask[];
  now: number;
  headless?: boolean;
}) {
  const list = lists[kind];
  return (
    <div class="plist">
      {headless ? null : <h2>{list.head}</h2>}
      <p class="inote">{list.lead}</p>
      <ul>
        {tasks.map((task) => (
          <li class={`row ${list.tone}`}>
            <span class="title">{task.description}</span>
            <span class="age">{clock(now - Number(task.started))}</span>
            <span class="l2">
              {kind === "agents" ? (
                <>
                  <span class="name">{task.subagentType || "agent"}</span> {task.activity}
                </>
              ) : (
                task.activity
              )}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
