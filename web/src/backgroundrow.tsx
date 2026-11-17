import type { JSX } from "preact";

import type { BackgroundTask } from "./gen/orrery/v1/orrery_pb";

// BackgroundRow draws one background task a headless session reported, as a row of the conversation. Its label is its
// type: the sub-agent type an agent task runs, or agent when it names none, and shell for a shell task. Its description
// follows, then its latest activity, and then its status once it has ended. A task that runs draws a live row. An agent
// task with a tool use id is a link: a click calls onOpen with that id, and the reader brings the Agent call with it
// into view and opens it.
export function BackgroundRow({
  task,
  onOpen,
}: {
  task: BackgroundTask;
  onOpen: (toolUseId: string) => void;
}): JSX.Element {
  const running = task.status === "running";
  const line = (
    <>
      <b>{backgroundLabel(task)}</b>
      <span class="bg-description">{task.description}</span>
      {task.activity !== "" && <span class="bg-activity">{task.activity}</span>}
      {!running && <span class={`bg-status ${task.status}`}>{task.status}</span>}
    </>
  );
  return (
    <div class={running ? "t live-row" : "t"}>
      <span class={running ? "gut live" : "gut"}>{running && "live"}</span>
      <div class="body">
        {isLink(task) ? (
          <button type="button" class="background-row" onClick={() => onOpen(task.toolUseId)}>
            {line}
          </button>
        ) : (
          <div class="background-row">{line}</div>
        )}
      </div>
    </div>
  );
}

// backgroundLabel is a task's label: its sub-agent type, or agent when it names none, for an agent task, and shell for
// a shell task.
function backgroundLabel(task: BackgroundTask): string {
  if (task.type !== "agent") {
    return "shell";
  }
  return task.subagentType !== "" ? task.subagentType : "agent";
}

// isLink is whether a task's row is a link: an agent task whose tool use id names the Agent call that started it.
function isLink(task: BackgroundTask): boolean {
  return task.type === "agent" && task.toolUseId !== "";
}
