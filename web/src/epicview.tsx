import type { JSX } from "preact";

import type { EpicSlice, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { taskStateOf } from "./taskstatus";

// The chart's rings, outermost first, so a task's progress moves inward. Each slice has one cell per ring: the field it
// reads and the name its title gives.
const rings = [
  { field: "redTest", name: "red test" },
  { field: "working", name: "working" },
  { field: "testsPass", name: "tests pass" },
  { field: "checks", name: "checks" },
  { field: "gateAccepted", name: "gate accepted" },
] as const;

// The chart has a fixed size whatever the task count: a hole, then the rings, each ringWidth thick. The slices divide the
// circle, so adding tasks narrows the slices and leaves the rings as they are. The margin keeps the cursor's outline
// inside the viewBox.
const hole = 24;
const ringWidth = 7;
const half = hole + rings.length * ringWidth + 2;
const dotRadius = 2.5;

// EpicView draws the right column for one epic: its slug, repository, outcome and any problem; a chart of its slices, or
// the line that it has none; the task list with each status as recorded; and every step, oldest first, with a pip per
// stored check and its end kind. The cursor's step and its task's slice are marked. Every text is a child of its
// element, so no markup in a slug, status, label or check name becomes an element.
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
      {slices.length === 0 ? (
        <p class="side-note">No orrery-tracked steps</p>
      ) : (
        <SliceChart slices={slices} cursorTask={cursorTask} />
      )}
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

// SliceChart draws the slices clockwise from the top, each an equal angle, and outlines the slice whose task is the
// cursor's. A slice whose at names a ring has a dot on that ring, in its middle.
function SliceChart(props: { slices: EpicSlice[]; cursorTask: string | undefined }): JSX.Element {
  const { slices, cursorTask } = props;
  const span = (2 * Math.PI) / slices.length;
  return (
    <svg class="chart" viewBox={`0 0 ${2 * half} ${2 * half}`}>
      {slices.map((slice, index) => {
        const start = index * span;
        const end = start + span;
        const dotRing = slice.at >= 1 && slice.at <= rings.length ? slice.at - 1 : undefined;
        const dot =
          dotRing === undefined
            ? undefined
            : pointAt(ringInner(dotRing) + ringWidth / 2, start + span / 2);
        return (
          <g key={index} class={slice.task === cursorTask ? "slice cur" : "slice"}>
            {rings.map((ring, ringIndex) => {
              const inner = ringInner(ringIndex);
              const reached = slice[ring.field];
              return (
                <path
                  key={ringIndex}
                  class={reached ? `cell ts-${slice.hue}` : "cell track"}
                  d={sectorPath(inner, inner + ringWidth, start, end)}
                >
                  <title>{`${slice.task} ${ring.name}: ${reached ? "reached" : "not reached"}`}</title>
                </path>
              );
            })}
            {dot !== undefined && <circle class="dot" cx={dot.x} cy={dot.y} r={dotRadius} />}
          </g>
        );
      })}
    </svg>
  );
}

// ringInner is the inner radius of the ring at index, the first ring outermost.
function ringInner(index: number): number {
  return hole + (rings.length - 1 - index) * ringWidth;
}

// pointAt is the point at radius r and angle a, in radians clockwise from the top of the chart.
function pointAt(r: number, a: number): { x: number; y: number } {
  return { x: half + r * Math.sin(a), y: half - r * Math.cos(a) };
}

// sectorPath is the annulus sector between the inner and outer radius from angle a0 to a1. Each arc is drawn in two
// halves, so a sector that is the whole circle, a lone slice's, still has its ends and middle to draw between.
function sectorPath(inner: number, outer: number, a0: number, a1: number): string {
  const mid = (a0 + a1) / 2;
  const outerStart = pointAt(outer, a0);
  const outerMid = pointAt(outer, mid);
  const outerEnd = pointAt(outer, a1);
  const innerEnd = pointAt(inner, a1);
  const innerMid = pointAt(inner, mid);
  const innerStart = pointAt(inner, a0);
  return [
    `M${outerStart.x} ${outerStart.y}`,
    `A${outer} ${outer} 0 0 1 ${outerMid.x} ${outerMid.y}`,
    `A${outer} ${outer} 0 0 1 ${outerEnd.x} ${outerEnd.y}`,
    `L${innerEnd.x} ${innerEnd.y}`,
    `A${inner} ${inner} 0 0 0 ${innerMid.x} ${innerMid.y}`,
    `A${inner} ${inner} 0 0 0 ${innerStart.x} ${innerStart.y}`,
    "Z",
  ].join(" ");
}

// endKind is how a step ended, else whether it is held or still running.
function endKind(step: EpicStep): string {
  if (step.end !== "") {
    return step.end;
  }
  return step.held ? "held" : "running";
}
