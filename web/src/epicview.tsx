import type { JSX } from "preact";

import type { EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { taskStateOf } from "./taskstatus";

// The rings are concentric, the first task innermost. ringGap is the distance between neighbouring ring centres,
// innermost the radius of the first task's ring, and ringMargin the room beyond the outermost ring for the wider
// stroke of the cursor's ring.
const ringGap = 8;
const innermost = 6;
const ringMargin = 3;

// EpicView draws the right column for one epic: its slug, repository, outcome and any problem; a ring per task; the
// task list with each status as recorded; and every step, oldest first, with a pip per stored check and its end kind.
// The cursor's step and its task's ring are marked. Every text is a child of its element, so no markup in a slug,
// status, label or check name becomes an element.
export function EpicView(props: { detail: EpicsGetResponse; cursorStep: string }): JSX.Element {
  const { detail, cursorStep } = props;
  const { epic, tasks, steps } = detail;
  const cursor = cursorStep === "" ? undefined : steps.find((step) => step.id === cursorStep);
  const cursorTask = cursor === undefined || cursor.task === "" ? undefined : cursor.task;
  const half = innermost + Math.max(tasks.length - 1, 0) * ringGap + ringMargin;
  return (
    <div class="epic-view">
      {epic && (
        <header class="epic-head">
          <h2 class="slug">{epic.slug}</h2>
          <p class="repo">{epic.repository}</p>
          <p class="outcome">{epic.outcome}</p>
          {epic.problem !== "" && <p class="problem">{epic.problem}</p>}
        </header>
      )}
      <svg class="rings" viewBox={`0 0 ${2 * half} ${2 * half}`}>
        {tasks.map((task, index) => (
          <circle
            key={index}
            class={`ring ts-${taskStateOf(task.status)}${task.slug === cursorTask ? " cur" : ""}`}
            cx={half}
            cy={half}
            r={innermost + index * ringGap}
          >
            <title>{`${task.slug}: ${task.status}`}</title>
          </circle>
        ))}
      </svg>
      <ul class="tasks">
        {tasks.map((task, index) => (
          <li key={index} class="task">
            <span class={`mark ts-${taskStateOf(task.status)}`} />
            <span class="slug">{task.slug}</span>
            <span class="status">{task.status}</span>
          </li>
        ))}
      </ul>
      <ol class="steps">
        {steps.map((step) => (
          <li
            key={step.id}
            class={step === cursor ? "step cur" : "step"}
            aria-current={step === cursor ? "true" : undefined}
          >
            <span class="label">{step.task || step.agent}</span>
            <span class="pips">
              {step.checks.map((check, index) => (
                <span
                  key={index}
                  class={check.ok ? "pip ok" : "pip bad"}
                  title={`round ${check.round} ${check.name}: ${check.ok ? "passed" : "failed"}`}
                />
              ))}
            </span>
            <span class="end">{endKind(step)}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}

// endKind is how a step ended, else whether it is held or still running.
function endKind(step: EpicStep): string {
  if (step.end !== "") {
    return step.end;
  }
  return step.held ? "held" : "running";
}
