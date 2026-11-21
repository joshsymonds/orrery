import type { JSX } from "preact";

import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { Phase } from "./phases";
import { Pips } from "./pips";
import { phaseOfPlace, recordTasksOf } from "./record";
import { blockerPillOf, statePillOf } from "./recordwords";

// EpicPin is the epic's one line, drawn first under a task's title row: the epic's slug, its state word with its time, and
// its tasks done. While the epic asks, its blocker follows in the attention tone. Pressing the line opens the epic.
export function EpicPin({
  detail,
  now,
  onOpen,
}: {
  detail: EpicsGetResponse;
  now: number;
  onOpen: () => void;
}): JSX.Element {
  const tasks = recordTasksOf(detail);
  const done = tasks.filter((task) => task.place === "integrated").length;
  const state = statePillOf(detail, now);
  const blocker = blockerPillOf(detail, now);
  return (
    <button type="button" class="epic-pin" onClick={onOpen}>
      <span class="slug">{detail.epic?.slug ?? ""}</span>
      <span class="words">{`${state.word} · ${done} of ${tasks.length} tasks done`}</span>
      {blocker !== null && <span class={`blocker t-${blocker.tone}`}>{blocker.word}</span>}
      <span class="end">
        <span class="open">open the epic</span>
        <span class="chev" aria-hidden="true">
          ›
        </span>
      </span>
    </button>
  );
}

// ContextBand is what a task's record and its worker's session draw first: the EpicPin, then the epic's tasks as a strip
// of chips, one per task in task order, each with its title over its five pips. The current task's chip is marked and
// opens nothing. Every other chip opens its task at the phase its place is in, or at implementing for a planned task.
export function ContextBand({
  detail,
  now,
  current,
  onOpenEpic,
  onOpenTask,
}: {
  detail: EpicsGetResponse;
  now: number;
  current: string;
  onOpenEpic: () => void;
  onOpenTask: (task: string, phase: Phase) => void;
}): JSX.Element {
  return (
    <div class="band">
      <EpicPin detail={detail} now={now} onOpen={onOpenEpic} />
      <nav class="tstrip" aria-label="this epic's tasks">
        {recordTasksOf(detail).map((task) => {
          const here = task.task === current;
          return (
            <button
              type="button"
              key={task.task}
              class={here ? "tchip cur" : "tchip"}
              aria-current={here ? "page" : undefined}
              onClick={() => {
                if (!here) {
                  onOpenTask(task.task, phaseOfPlace(task.place) ?? "implementing");
                }
              }}
            >
              <span class="tt">{task.title}</span>
              <Pips cells={task.cells} />
            </button>
          );
        })}
      </nav>
    </div>
  );
}
