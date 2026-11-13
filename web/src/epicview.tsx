import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { denseStrip, doneOf, pipsOf, roundOf, type StripTone, stripOf } from "./epics";
import type { EpicRequirement, EpicSlice, EpicStep, EpicsGetResponse, EpicTask } from "./gen/orrery/v1/epics_pb";
import { cellKind, cellTitle, type EpicReadout, epicReadout, RINGS, type TaskReadout, taskReadout } from "./orbitcells";
import { bezel, cellPath, CENTRE, handPath, hitSlice, ringLetters, VIEW, wedgePath } from "./orbits";
import { clock } from "./reader";
import { taskStateOf } from "./taskstatus";

// The empty state's five orbits, as radii about the middle of a 132 unit square, from the outside in.
const emptyOrbits = [62, 51, 40, 29, 18];
// The cursor's wash is drawn from this many slices up: a narrower slice needs the wash to be found.
const washFrom = 18;
// A task row's mark for each tone that is not now, which draws a spinner instead.
const markOf: Record<Exclude<StripTone, "now">, string> = { passed: "✓", held: "‖", errored: "✗", none: "○" };
// A step's start and now are unix seconds, and a Date takes milliseconds.
const secondsToMillis = 1000;

// EpicView draws the right column for one epic: its head, with the slug, repository, outcome and done count, any
// problem and the task strip; the Requirements of an orreryd epic; a chart of its slices, or the orbits and the line
// that it has none; the task rows, each with its mark, agent, pips and line; a card for the cursor's step while it
// runs; and the steps, folded behind a line that counts them and their ends. The fold opens to the steps started on
// now's day, oldest first, each with a pip per stored check and its end kind. The cursor's step and its task are
// marked. Every text is a child of its element, so no markup in a slug, status, label or check name becomes an element.
export function EpicView(props: { detail: EpicsGetResponse; cursorStep: string; now: number }): JSX.Element {
  const { detail, cursorStep, now } = props;
  const { epic, tasks, slices, steps, requirements } = detail;
  const [open, setOpen] = useState(false);
  const cursor = cursorStep === "" ? undefined : steps.find((step) => step.id === cursorStep);
  const cursorTask = cursor === undefined || cursor.task === "" ? undefined : cursor.task;
  const strip = stripOf(tasks, slices);
  return (
    <div class="epic-view">
      {epic && (
        <header class="epic-head">
          <h3 class="eyebrow">EPIC</h3>
          <div class="head-line">
            <h2 class="slug">{epic.slug}</h2>
            <span class="repo">{epic.repository}</span>
            <span class="rt">{joinParts(epic.outcome, `${doneOf(tasks)} of ${tasks.length}`)}</span>
          </div>
          {epic.problem !== "" && <p class="problem">{epic.problem}</p>}
          {tasks.length > 0 && (
            <div class={strip.length > denseStrip ? "strip dense" : "strip"}>
              {strip.map((tone, index) => (
                <i key={index} class={`k-${tone}`} />
              ))}
            </div>
          )}
        </header>
      )}
      {requirements.length > 0 && <Requirements requirements={requirements} />}
      {slices.length === 0 ? (
        <NoSteps />
      ) : (
        <SliceChart slices={slices} cursorTask={cursorTask} recordTasks={tasks.length} />
      )}
      <h3 class="eyebrow">
        TASKS<span class="sub">{`${tasks.length} · ${strip.filter((tone) => tone !== "none").length} tracked`}</span>
      </h3>
      <ul class="tasks">
        {tasks.map((task, index) => {
          const tone = rowToneOf(task, strip[index]);
          const step = latestStepOf(steps, task.slug);
          return (
            <li key={index} class={taskClassOf(task.slug === cursorTask, tone)}>
              {tone === "now" ? (
                <span class="um g-working" aria-hidden="true" />
              ) : (
                <span class={`um k-${tone}`} aria-hidden="true">
                  {markOf[tone]}
                </span>
              )}
              <span class="slug">{task.slug}</span>
              <span class="agent">{step?.agent ?? ""}</span>
              <span class="pips">
                {pipsOf(task.slug, slices).map((kind, pip) => (
                  <i key={pip} class={`k-${kind}`} />
                ))}
              </span>
              <span class="line">{lineOf(task, step)}</span>
            </li>
          );
        })}
      </ul>
      {cursor !== undefined && cursor.end === "" && !cursor.held && (
        <section class="now-card">
          <p class="cap">{`${cursor.task || cursor.agent} · now`}</p>
          {cursor.doing !== "" && <p class="doing">{cursor.doing}</p>}
          <p class="m">{`started ${clock(cursor.started)} · round ${roundOf(cursor)}`}</p>
        </section>
      )}
      <button type="button" class="steps-fold" aria-expanded={open} onClick={() => setOpen((isOpen) => !isOpen)}>
        {`${steps.length} steps · ${steps.filter((step) => step.end !== "").length} ended`}
      </button>
      {open && (
        <ol class="steps">
          {steps
            .filter((step) => startedToday(step.started, now))
            .map((step) => (
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
      )}
    </div>
  );
}

// Requirements is an orreryd epic's requirements under their count: each id, text and kind, in contract order.
function Requirements(props: { requirements: EpicRequirement[] }): JSX.Element {
  const { requirements } = props;
  return (
    <section class="requirements">
      <h3 class="eyebrow">
        REQUIREMENTS<span class="sub">{requirements.length}</span>
      </h3>
      <ol class="requirements">
        {requirements.map((requirement, index) => (
          <li key={index} class="requirement">
            <span class="rid">{requirement.id}</span>
            <span class="text">{requirement.text}</span>
            <span class="kind">{requirement.kind}</span>
          </li>
        ))}
      </ol>
    </section>
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
// the glows, the ring letters and bezel, the cursor's outline and hand, the hover outline, and the centre readout. The
// yellow cell of a running task glows over the cells. The slice whose task is the cursor's is marked cur, and the slice
// under the pointer is hovered. The two key lines sit under it.
function SliceChart(props: { slices: EpicSlice[]; cursorTask: string | undefined; recordTasks: number }): JSX.Element {
  const { slices, cursorTask, recordTasks } = props;
  // The hovered slice is held by its task, as the cursor is: when the slices change, a hover on a lost task finds none.
  const [hoveredTask, setHoveredTask] = useState<string | undefined>(undefined);
  const count = slices.length;
  const cursorIndex = cursorTask === undefined ? -1 : slices.findIndex((slice) => slice.task === cursorTask);
  const hoverIndex = slices.findIndex((slice) => slice.task === hoveredTask);
  // The readout is the hovered slice's task, else the cursor's, else the epic.
  const readIndex = hoverIndex >= 0 ? hoverIndex : cursorIndex;
  const glows = slices.flatMap((slice, index) =>
    !slice.held && slice.at >= 1 && slice.at <= RINGS.length ? [cellPath(count, index, slice.at)] : [],
  );
  return (
    <div class="chart-block">
      <svg
        class="chart"
        viewBox={`0 0 ${VIEW} ${VIEW}`}
        onPointerMove={(event) => setHoveredTask(taskUnder(event, slices))}
        onPointerLeave={() => setHoveredTask(undefined)}
      >
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
        {hoverIndex >= 0 && <path class="outline hover" d={wedgePath(count, hoverIndex)} />}
        {readIndex >= 0 ? (
          <TaskCentre readout={taskReadout(slices, readIndex)} />
        ) : (
          <EpicCentre readout={epicReadout(slices, recordTasks)} />
        )}
      </svg>
      <ChartKey />
    </div>
  );
}

// taskUnder is the task of the slice under a pointer on the chart, found by its angle alone from the chart's centre, or
// undefined over the centre, the start gap or past the rings.
function taskUnder(event: JSX.TargetedPointerEvent<SVGSVGElement>, slices: EpicSlice[]): string | undefined {
  const box = event.currentTarget.getBoundingClientRect();
  const x = ((event.clientX - box.left) / box.width) * VIEW - CENTRE;
  const y = ((event.clientY - box.top) / box.height) * VIEW - CENTRE;
  const hit = hitSlice(x, y, slices.length);
  return typeof hit === "number" ? slices[hit].task : undefined;
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

// startedToday is whether a step started on the local calendar day of now.
function startedToday(started: bigint, now: number): boolean {
  const day = new Date(Number(started) * secondsToMillis).toDateString();
  return day === new Date(now * secondsToMillis).toDateString();
}

// joinParts joins the parts that are not empty with a middle dot.
function joinParts(...parts: string[]): string {
  return parts.filter((part) => part !== "").join(" · ");
}

// rowToneOf is a task row's tone: its strip tone, or for a task no slice names, passed when its status is done, errored
// when it is a gap, and none otherwise.
function rowToneOf(task: EpicTask, stripTone: StripTone): StripTone {
  if (stripTone !== "none") {
    return stripTone;
  }
  const state = taskStateOf(task.status);
  if (state === "done") {
    return "passed";
  }
  return state === "gap" ? "errored" : "none";
}

// taskClassOf is a task row's classes: cur when the cursor's step is on its task, held and out by its tone.
function taskClassOf(current: boolean, tone: StripTone): string {
  return ["task", current ? "cur" : "", tone === "held" ? "held" : "", tone === "none" ? "out" : ""]
    .filter((name) => name !== "")
    .join(" ");
}

// latestStepOf is the task's latest step: the last, in the order given, whose task is the slug.
function latestStepOf(steps: EpicStep[], task: string): EpicStep | undefined {
  return steps.filter((step) => step.task === task).at(-1);
}

// lineOf is a task row's line from its latest step: the held reason or the doing of a running step, the end and reason
// of a step that ended other than done, else the recorded status as written.
function lineOf(task: EpicTask, step: EpicStep | undefined): string {
  if (step === undefined) {
    return task.status;
  }
  if (step.end === "") {
    if (step.held) {
      return joinParts("held", step.reason);
    }
    return step.doing === "" ? task.status : step.doing;
  }
  return step.end === "done" ? task.status : joinParts(step.end, step.reason);
}
