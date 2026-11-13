import type { JSX } from "preact";

import type { EpicSlice, EpicStep, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { cellKind, cellTitle, type EpicReadout, epicReadout, RINGS, type TaskReadout, taskReadout } from "./orbitcells";
import { bezel, cellPath, CENTRE, handPath, ringLetters, VIEW, wedgePath } from "./orbits";
import { taskStateOf } from "./taskstatus";

// The empty state's five orbits, as radii about the middle of a 132 unit square, from the outside in.
const emptyOrbits = [62, 51, 40, 29, 18];
// The cursor's wash is drawn from this many slices up: a narrower slice needs the wash to be found.
const washFrom = 18;

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
      {slices.length === 0 ? (
        <NoSteps />
      ) : (
        <SliceChart slices={slices} cursorTask={cursorTask} recordTasks={tasks.length} />
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

// SliceChart draws the Orbits chart of chart-spec.md in section 10's order: the cursor's wash, the cells slice by slice,
// the glows, the ring letters and bezel, the cursor's outline and hand, and the centre readout. The yellow cell of a
// running task glows over the cells. The slice whose task is the cursor's is marked cur. The two key lines sit under it.
function SliceChart(props: { slices: EpicSlice[]; cursorTask: string | undefined; recordTasks: number }): JSX.Element {
  const { slices, cursorTask, recordTasks } = props;
  const count = slices.length;
  const cursorIndex = cursorTask === undefined ? -1 : slices.findIndex((slice) => slice.task === cursorTask);
  const glows = slices.flatMap((slice, index) =>
    !slice.held && slice.at >= 1 && slice.at <= RINGS.length ? [cellPath(count, index, slice.at)] : [],
  );
  return (
    <div class="chart-block">
      <svg class="chart" viewBox={`0 0 ${VIEW} ${VIEW}`}>
        <defs>
          <filter id="orbit-glow" filterUnits="userSpaceOnUse" x="0" y="0" width={VIEW} height={VIEW}>
            <feGaussianBlur stdDeviation="3.4" />
          </filter>
        </defs>
        {cursorIndex >= 0 && count >= washFrom && <path class="wash" d={wedgePath(count, cursorIndex)} />}
        {slices.map((slice, index) => (
          <g key={index} class={index === cursorIndex ? "slice cur" : "slice"}>
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
        {cursorIndex >= 0 && <path class="outline" d={wedgePath(count, cursorIndex)} />}
        {cursorIndex >= 0 && <path class="hand" d={handPath(count, cursorIndex)} />}
        {cursorIndex >= 0 ? (
          <TaskCentre readout={taskReadout(slices, cursorIndex)} />
        ) : (
          <EpicCentre readout={epicReadout(slices, recordTasks)} />
        )}
      </svg>
      <ChartKey />
    </div>
  );
}

// EpicCentre is the epic's centre readout: the done count over its caption, then its tallies, one line or two.
function EpicCentre(props: { readout: EpicReadout }): JSX.Element {
  const { done, total, caption, lines } = props.readout;
  // Two tally lines sit higher than one, so the count and caption move up with them, as chart-spec section 11 has it.
  const two = lines.length === 2;
  return (
    <g class="readout">
      <text class="count" x={CENTRE} y={CENTRE + (two ? -6 : -2)} text-anchor="middle">
        {done}
        <tspan class="total dim">/{total}</tspan>
      </text>
      <text class="caption dim" x={CENTRE} y={CENTRE + (two ? 12 : 17)} text-anchor="middle">
        {caption}
      </text>
      {lines.map((line) => (
        <text key={line.offset} class="tallies" x={CENTRE} y={CENTRE + line.offset} text-anchor="middle">
          {line.tallies.map((tally, index) => (
            <>
              {index > 0 && <tspan class="dim"> · </tspan>}
              <tspan class={tally.tone}>{tally.text}</tspan>
            </>
          ))}
        </text>
      ))}
    </g>
  );
}

// TaskCentre is the cursor's task's centre readout: its ordinal, its label, its status line, and a letter per ring in
// the colour of that ring's cell.
function TaskCentre(props: { readout: TaskReadout }): JSX.Element {
  const { ordinal, label, size, status, tone, letters } = props.readout;
  return (
    <g class="readout">
      <text class="ordinal" x={CENTRE} y={CENTRE - 30} text-anchor="middle">
        {ordinal}
      </text>
      <text class="label" x={CENTRE} y={CENTRE - 5} text-anchor="middle" font-size={size.toFixed(1)}>
        {label}
      </text>
      <text class={`status ${tone}`} x={CENTRE} y={CENTRE + 14} text-anchor="middle">
        {status}
      </text>
      {letters.map((letter, index) => (
        <text
          key={index}
          class={`letter ${letter.kind}`}
          x={CENTRE + (index - 2) * 15}
          y={CENTRE + 37}
          text-anchor="middle"
        >
          {letter.letter}
        </text>
      ))}
    </g>
  );
}

// ChartKey is the two lines under the chart: the rings by their letters, then the colours, each after its swatch.
function ChartKey(): JSX.Element {
  return (
    <>
      <p class="chart-key">
        {RINGS.map((ring, index) => (
          <>
            {index > 0 && " "}
            <span>
              <span class="letter">{ring.letter}</span> {ring.name.replace(" accepted", "")}
            </span>
          </>
        ))}
      </p>
      <p class="chart-key">
        {(["passed", "now", "held", "errored"] as const).map((kind, index) => (
          <>
            {index > 0 && " "}
            <span>
              <span class={`swatch ${kind}`} />
              {kind}
            </span>
          </>
        ))}
      </p>
    </>
  );
}

// endKind is how a step ended, else whether it is held or still running.
function endKind(step: EpicStep): string {
  if (step.end !== "") {
    return step.end;
  }
  return step.held ? "held" : "running";
}
