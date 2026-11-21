import type { JSX } from "preact";

import type { BackgroundTask, ConversationItem } from "./gen/orrery/v1/orrery_pb";
import { Icon } from "./glyphs";
import { partCurrent } from "./lines";
import { MarkedText } from "./text";

// TaskLineProps is what a TaskLine draws: its icon's type, agent or shell; its name, the sub-agent type or shell; its
// description; its latest activity; its end word once it has ended; whether it runs; onOpen, when a click opens
// something; and the find's query with current, the offset in the searched text where the current match starts, the
// name and the description being the searched text one to a line.
interface TaskLineProps {
  type: "agent" | "shell";
  name: string;
  description: string;
  activity?: string;
  end?: string;
  running?: boolean;
  onOpen?: () => void;
  query?: string;
  current?: number;
}

// TaskLine draws a task as a Line: its icon, its name, its description, its latest activity, its end word, and the
// running mark while it runs. A line that opens something is a button. The name and the description are marked with the
// find's query.
export function TaskLine({
  type,
  name,
  description,
  activity = "",
  end = "",
  running = false,
  onOpen,
  query = "",
  current = -1,
}: TaskLineProps): JSX.Element {
  const className = running ? "line task running" : "line task";
  const line = (
    <>
      <Icon name={type} />
      <span class="name">
        <MarkedText text={name} query={query} current={partCurrent(current, 0, name.length)} />
      </span>
      <span class="arg strong">
        <MarkedText text={description} query={query} current={partCurrent(current, name.length + 1, description.length)} />
      </span>
      {activity !== "" && <span class="more">{activity}</span>}
      {running && <span class="running-mark">running</span>}
      {end !== "" && <span class="end">{end}</span>}
    </>
  );
  if (onOpen === undefined) {
    return (
      <div class={className} data-shape="line">
        {line}
      </div>
    );
  }
  return (
    <button type="button" class={className} data-shape="line" onClick={onOpen}>
      {line}
    </button>
  );
}

// BackgroundRow draws one background task a headless session reported, as a TaskLine: the agent icon and the sub-agent
// type an agent task runs (agent when it names none), or the shell icon and shell for a shell task; then its description,
// its latest activity, its status once it has ended, and the running mark while it runs. An agent task with a tool use id
// is a link: a click calls onOpen with that id, and the reader brings the Agent call with it into view and opens it.
export function BackgroundRow({
  task,
  onOpen,
}: {
  task: BackgroundTask;
  onOpen: (toolUseId: string) => void;
}): JSX.Element {
  const running = task.status === "running";
  return (
    <TaskLine
      type={task.type === "agent" ? "agent" : "shell"}
      name={backgroundLabel(task)}
      description={task.description}
      activity={task.activity}
      end={running ? "" : taskEndWord(task.status)}
      running={running}
      onOpen={isLink(task) ? () => onOpen(task.toolUseId) : undefined}
    />
  );
}

// agentFinishedTask is the TaskLine an agent_finished item draws: the agent icon, the item's sub-agent type, its
// description, and its end word: finished when the agent is done, else the item's notice, failed or stopped.
export function agentFinishedTask(item: ConversationItem): TaskLineProps {
  return {
    type: "agent",
    name: item.tool,
    description: item.text,
    end: taskEndWord(item.notice),
  };
}

// taskEndWord is the word a task's line ends with once it has ended, round 6's: a done task reads finished, and any
// other end reads as its status or notice does.
function taskEndWord(status: string): string {
  return status === "done" ? "finished" : status;
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
