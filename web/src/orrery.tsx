import { type JSX, Fragment } from "preact";
import { useState } from "preact/hooks";

import { CENTRE, RING_RADII, VIEW, cellPath, point, sector, sliceSpan, sliceStart } from "./orbits";
import { PHASES, type Phase, type PhaseSlice, type RequirementArc, type Wedge, durationText } from "./phases";

// The requirement arcs lie between radius ARC_INNER and ARC_OUTER, and their ids sit at ID_RADIUS.
export const ARC_INNER = 184;
export const ARC_OUTER = 192;
const ID_RADIUS = 200;

// The rings, outermost first: the planned rim, then one ring per phase.
const RING_NAMES = ["planned", ...PHASES];

// held is the phase cell under the pointer, by its task and phase.
interface Held {
  task: string;
  phase: Phase;
}

// timeOf is a slice's time as text: how long a now slice has run, or how long a passed slice took. Nothing otherwise.
function timeOf(slice: PhaseSlice, now: number): string | undefined {
  if (slice.state === "now" && slice.since > 0) {
    return durationText(now - slice.since);
  }
  if (slice.state === "passed" && slice.seconds > 0) {
    return durationText(slice.seconds);
  }
  return undefined;
}

// SliceCard shows a held slice: its task, phase, summary when there is one, and its time when there is one.
function SliceCard(props: { task: string; slice: PhaseSlice; now: number }): JSX.Element {
  const { task, slice, now } = props;
  const time = timeOf(slice, now);
  return (
    <div class="slice-card">
      <p class="card-task">{task}</p>
      <p class="card-phase">{slice.phase}</p>
      {slice.summary !== "" && <p class="card-summary">{slice.summary}</p>}
      {time !== undefined && <p class="card-time">{time}</p>}
    </div>
  );
}

// Orrery draws the wedges as rings of cells, one wedge per task and one ring per phase with the planned rim outside
// them. A running wedge's now slice glows, the requirement arcs ring the wedges, and the centre counts the integrated
// wedges. Hovering a phase cell shows its slice card. With onOpenPhase, a click on a phase cell opens its task and
// phase; the rim cells never open.
export function Orrery(props: {
  wedges: Wedge[];
  arcs: RequirementArc[];
  now: number;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element {
  const { wedges, arcs, now, onOpenPhase } = props;
  const [held, setHeld] = useState<Held | undefined>(undefined);
  const count = wedges.length;
  const integrated = wedges.filter((wedge) => wedge.place === "integrated").length;
  const readout = `${integrated} of ${count}`;
  const heldWedge = held === undefined ? undefined : wedges.find((wedge) => wedge.task === held.task);
  const heldSlice = heldWedge?.slices.find((slice) => slice.phase === held?.phase);
  const glows = wedges.flatMap((wedge, index) =>
    wedge.tone === "run"
      ? wedge.slices.flatMap((slice, ring) => (slice.state === "now" ? [cellPath(count, index, ring + 2)] : []))
      : [],
  );
  const arcStart = sliceStart(count, 0);
  const arcSpan = sliceSpan(count);
  const openClass = onOpenPhase === undefined ? "" : " open";
  return (
    <Fragment>
      <svg class="orrery" viewBox={`0 0 ${VIEW} ${VIEW}`} role="img" aria-label={`${readout} integrated`}>
        {wedges.map((wedge, index) => (
          <path
            key={`rim-${wedge.task}`}
            class={`cell rim ${wedge.place === "planned" ? "planned" : "passed"}`}
            d={cellPath(count, index, 1)}
            data-task={wedge.task}
            data-phase="planned"
          />
        ))}
        {wedges.flatMap((wedge, index) =>
          wedge.slices.map((slice, ring) => (
            <path
              key={`${wedge.task}-${slice.phase}`}
              class={`cell phase-${slice.phase} ${slice.state}${slice.state === "now" ? ` t-${wedge.tone}` : ""}${openClass}`}
              d={cellPath(count, index, ring + 2)}
              data-task={wedge.task}
              data-phase={slice.phase}
              role={onOpenPhase === undefined ? undefined : "button"}
              onClick={onOpenPhase === undefined ? undefined : () => onOpenPhase(wedge.task, slice.phase)}
              onPointerEnter={() => setHeld({ task: wedge.task, phase: slice.phase })}
              onPointerLeave={() => setHeld(undefined)}
            />
          )),
        )}
        <g class="glows">
          {glows.map((d, index) => (
            <path key={index} class="glow" d={d} />
          ))}
        </g>
        {arcs.map((arc) => {
          const from = arcStart + arc.start * arcSpan;
          const to = arcStart + arc.end * arcSpan;
          const id = point(ID_RADIUS, (from + to) / 2);
          return (
            <Fragment key={`${arc.id}-${arc.start}`}>
              <path
                class={`req ${arc.proven ? "proven" : "open"}`}
                d={sector(ARC_INNER, ARC_OUTER, from, to)}
                data-req={arc.id}
              />
              <text class="req-id" x={id.x} y={id.y} text-anchor="middle" dominant-baseline="central">
                {arc.id}
              </text>
            </Fragment>
          );
        })}
        {RING_NAMES.map((name, ring) => {
          const label = point(RING_RADII[ring], 0);
          return (
            <text
              key={name}
              class="phase-name"
              x={label.x}
              y={label.y}
              text-anchor="middle"
              dominant-baseline="central"
            >
              {name}
            </text>
          );
        })}
        <text class="count" x={CENTRE} y={CENTRE} text-anchor="middle" dominant-baseline="central">
          {readout}
        </text>
        <text class="count-of" x={CENTRE} y={CENTRE + 16} text-anchor="middle" dominant-baseline="central">
          integrated
        </text>
      </svg>
      {heldWedge !== undefined && heldSlice !== undefined && (
        <SliceCard task={heldWedge.task} slice={heldSlice} now={now} />
      )}
    </Fragment>
  );
}
