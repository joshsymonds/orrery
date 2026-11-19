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

// ListPanel draws a conversation's running sub-agents, or its shells left running, as one row per task it is given: its
// description, its activity, and its clock from when it started. An agent row leads its activity with its sub-agent type.
export function ListPanel({ kind, tasks, now }: { kind: "agents" | "running"; tasks: BackgroundTask[]; now: number }) {
  const list = lists[kind];
  return (
    <div class="plist">
      <h2>{list.head}</h2>
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
