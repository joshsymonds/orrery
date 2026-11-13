import type { JSX } from "preact";

import type { EpicSlice, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { cellKind, cellTitle, RINGS } from "./orbitcells";
import { bezel, cellPath, ringLetters, VIEW } from "./orbits";
import { taskStateOf } from "./taskstatus";

// The empty state's five orbits, as radii about the middle of a 132 unit square, from the outside in.
const emptyOrbits = [62, 51, 40, 29, 18];

// EpicView draws the right column for one epic: its slug, repository, outcome and any problem; a chart of its slices,
// or the orbits and the line that it has none; the task list with each status as recorded; and every step, oldest
// first, with a pip per stored check and its end kind. The cursor's step and its task's slice are marked. Every text is
// a child of its element, so no markup in a slug, status, label or check name becomes an element.
export function EpicView(props: { detail: EpicsGetResponse; cursorStep: string }): JSX.Element {
  const { detail, cursorStep } = props;
  const { epic, tasks, slices, steps } = detail;
  const cursor = cursorStep === "" ? undefined : steps.find((step) => step.id === cursorStep);
  const cursorTask = cursor === undefined || cursor.task === "" ? undefined : cursor.task;
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
      {slices.length === 0 ? <NoSteps /> : <SliceChart slices={slices} cursorTask={cursorTask} />}
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

// NoSteps is the empty state: five dotted orbits over the line that the epic has no orrery-tracked steps.
function NoSteps(): JSX.Element {
  return (
    <div class="no-steps">
      <svg class="empty-orbits" viewBox="0 0 132 132">
        {emptyOrbits.map((radius) => (
          <circle key={radius} class="orbit" cx={66} cy={66} r={radius} />
        ))}
      </svg>
      <p class="side-note">No orrery-tracked steps</p>
    </div>
  );
}

// SliceChart draws the Orbits chart of chart-spec.md: a slice per task, clockwise from the top, each cut into a cell
// per ring. The yellow cell of a running task glows, and the glows are drawn over the cells. The slice whose task is
// the cursor's is marked cur.
function SliceChart(props: { slices: EpicSlice[]; cursorTask: string | undefined }): JSX.Element {
  const { slices, cursorTask } = props;
  const count = slices.length;
  const glows = slices.flatMap((slice, index) =>
    !slice.held && slice.at >= 1 && slice.at <= RINGS.length ? [cellPath(count, index, slice.at)] : [],
  );
  return (
    <svg class="chart" viewBox={`0 0 ${VIEW} ${VIEW}`}>
      <defs>
        <filter id="orbit-glow" filterUnits="userSpaceOnUse" x="0" y="0" width={VIEW} height={VIEW}>
          <feGaussianBlur stdDeviation="3.4" />
        </filter>
      </defs>
      {slices.map((slice, index) => (
        <g key={index} class={slice.task === cursorTask ? "slice cur" : "slice"}>
          {RINGS.map((_, ringIndex) => (
            <path
              key={ringIndex}
              class={`cell ${cellKind(slice, ringIndex + 1)}`}
              d={cellPath(count, index, ringIndex + 1)}
            >
              <title>{cellTitle(slice, ringIndex + 1)}</title>
            </path>
          ))}
        </g>
      ))}
      <g class="glows">
        {glows.map((d, index) => (
          <path key={index} class="glow" d={d} />
        ))}
      </g>
      {ringLetters().map((letter) => (
        <text
          key={letter.letter}
          class="ring-letter"
          x={letter.x}
          y={letter.y}
          text-anchor="middle"
          dominant-baseline="central"
        >
          {letter.letter}
        </text>
      ))}
      {bezel(count).map((tick, index) => (
        <g key={index}>
          <line class={tick.major ? "tick major" : "tick"} x1={tick.x1} y1={tick.y1} x2={tick.x2} y2={tick.y2} />
          {tick.label && (
            <text
              class="tick-number"
              x={tick.label.x}
              y={tick.label.y}
              text-anchor="middle"
              dominant-baseline="central"
            >
              {tick.label.text}
            </text>
          )}
        </g>
      ))}
    </svg>
  );
}

// endKind is how a step ended, else whether it is held or still running.
function endKind(step: EpicStep): string {
  if (step.end !== "") {
    return step.end;
  }
  return step.held ? "held" : "running";
}
