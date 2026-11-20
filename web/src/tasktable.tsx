import { Fragment, type JSX } from "preact";
import { useState } from "preact/hooks";

import { type Phase } from "./phases";
import { phaseOfPlace, type RecordTask } from "./record";
import { Pips } from "./pips";
import { elapsed } from "./dockwords";

// shownRows is how many task rows the table draws before its fold line.
const shownRows = 12;

// TaskTable draws an epic's tasks beside the chart: each row its name, the requirements it proves, its five pips, its
// words and its clock. The first twelve rows show, then a fold line names the rest and shows them when pressed. A row
// whose task has a phase opens that phase when onOpenPhase is given.
export function TaskTable({
  tasks,
  now,
  onOpenPhase,
}: {
  tasks: RecordTask[];
  now: number;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const shown = expanded ? tasks : tasks.slice(0, shownRows);
  const hidden = tasks.length - shown.length;
  return (
    <div class="tbl tasks">
      <div class="tr head">
        <span>task</span>
        <span>proves</span>
        <span>phases</span>
        <span>now</span>
      </div>
      {shown.map((task) => (
        <TaskRow key={task.task} task={task} now={now} onOpenPhase={onOpenPhase} />
      ))}
      {hidden > 0 ? (
        <button type="button" class="fold-line" onClick={() => setExpanded(true)}>
          {`${hidden} more tasks`}
        </button>
      ) : null}
    </div>
  );
}

// TaskRow draws one task: a button that opens its phase when the task has one and the table can open it, else a plain
// row.
function TaskRow({
  task,
  now,
  onOpenPhase,
}: {
  task: RecordTask;
  now: number;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element {
  const phase = phaseOfPlace(task.place);
  const cells = (
    <>
      <span class="strong">{task.title}</span>
      <span>
        {task.requirements.map((id, index) => (
          <Fragment key={id}>
            {index > 0 ? " " : null}
            <span class="id">{id}</span>
          </Fragment>
        ))}
      </span>
      <Pips cells={task.cells} />
      <span class={`cell t-${task.tone}`}>{task.words}</span>
      <span class="end">{task.since > 0 ? elapsed(now - task.since) : ""}</span>
    </>
  );
  if (phase !== undefined && onOpenPhase !== undefined) {
    return (
      <button type="button" class="tr" onClick={() => onOpenPhase(task.task, phase)}>
        {cells}
      </button>
    );
  }
  return <div class="tr">{cells}</div>;
}
