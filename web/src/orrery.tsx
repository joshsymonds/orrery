import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { CENTRE, GAP, RING_RADII, VIEW, cellPath, point, sector, sliceGap, sliceSpan, sliceStart } from "./orbits";
import type { Phase } from "./phases";
import { PLACES, type RecordTask, phaseOfPlace } from "./record";

// The requirement rim lies between radius ARC_INNER and ARC_OUTER, outside the rings. epicline.tsx draws its mini chart's
// rim between the same radii.
export const ARC_INNER = 184;
export const ARC_OUTER = 192;

// The charts drawn so far: each chart's ring-name paths take ids from its own number, so no two charts share one.
let charts = 0;

// ringWay is the path a ring's name runs along: its centre radius, from the gap's left edge through the gap at 12 o'clock
// to where the ring's first cell begins.
function ringWay(radius: number, count: number): string {
  const left = point(radius, (-GAP / 2) * (Math.PI / 180));
  const first = point(radius, sliceStart(count, 0));
  return `M${left.x.toFixed(2)} ${left.y.toFixed(2)}A${radius} ${radius} 0 0 1 ${first.x.toFixed(2)} ${first.y.toFixed(2)}`;
}

// Orrery draws each task as a wedge of five cells, one per ring, with the rings outside in in PLACES order, and a rim
// outside the rings that reads proven when every requirement the task proves is proven. Each ring's name runs along its
// ring through the gap at 12 o'clock. Hovering a task's cell outlines its wedge and captions it. The centre counts the
// integrated tasks. With onOpenPhase, a click on a task's cell opens its task and that ring's phase; a planned task has no
// phase to open. No tasks draw nothing.
export function Orrery(props: {
  tasks: RecordTask[];
  proven: ReadonlySet<string>;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element | null {
  const { tasks, proven, onOpenPhase } = props;
  const [hovered, setHovered] = useState<string | undefined>(undefined);
  const [chart] = useState(() => {
    charts += 1;
    return charts;
  });
  if (tasks.length === 0) {
    return null;
  }
  const count = tasks.length;
  const span = sliceSpan(count);
  const gap = sliceGap(count);
  const done = tasks.filter((task) => task.place === "integrated").length;
  const hoveredIndex = tasks.findIndex((task) => task.task === hovered);
  const hoveredTask = tasks[hoveredIndex];
  const provenTask = (task: RecordTask): boolean =>
    task.requirements.length > 0 && task.requirements.every((id) => proven.has(id));
  return (
    <figure class="orrery-fig">
      <svg class="orrery" viewBox={`0 0 ${VIEW} ${VIEW}`} role="img" aria-label={`${done} of ${count} tasks done`}>
        {tasks.map((task, index) => (
          <path
            key={`req-${task.task}`}
            class={provenTask(task) ? "req proven" : "req"}
            d={sector(ARC_INNER, ARC_OUTER, sliceStart(count, index), sliceStart(count, index) + span, gap)}
          />
        ))}
        {tasks.flatMap((task, index) =>
          PLACES.map((place, ring) => {
            const phase = phaseOfPlace(place);
            const opener =
              phase === undefined || onOpenPhase === undefined || task.place === "planned"
                ? undefined
                : () => onOpenPhase(task.task, phase);
            return (
              <path
                key={`${task.task}-${place}`}
                class={`cell c-${task.cells[ring]}`}
                d={cellPath(count, index, ring + 1)}
                data-task={task.task}
                data-place={place}
                data-phase={phase}
                role={opener === undefined ? undefined : "button"}
                onClick={opener}
                onPointerEnter={() => setHovered(task.task)}
                onPointerLeave={() => setHovered(undefined)}
              />
            );
          }),
        )}
        {hoveredTask !== undefined && (
          <path
            class="hl"
            d={sector(
              RING_RADII[RING_RADII.length - 1] - 5.5,
              ARC_OUTER,
              sliceStart(count, hoveredIndex),
              sliceStart(count, hoveredIndex) + span,
            )}
          />
        )}
        {PLACES.map((place, ring) => (
          <path key={`way-${place}`} id={`orrery-${chart}-${place}`} class="ring-way" d={ringWay(RING_RADII[ring], count)} />
        ))}
        {PLACES.map((place) => (
          <text key={`name-${place}`} class="ring-name" text-anchor="end">
            <textPath href={`#orrery-${chart}-${place}`} startOffset="100%">
              {place}
            </textPath>
          </text>
        ))}
        <text class="count" x={CENTRE} y={CENTRE} text-anchor="middle" dominant-baseline="central">
          {done}
        </text>
        <text class="of" x={CENTRE} y={CENTRE + 16} text-anchor="middle" dominant-baseline="central">
          {`of ${count} done`}
        </text>
      </svg>
      {hoveredTask !== undefined && <figcaption class="orrery-cap">{`${hoveredTask.title} · ${hoveredTask.words}`}</figcaption>}
    </figure>
  );
}
