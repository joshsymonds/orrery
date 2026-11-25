import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { EpicRequirement } from "./gen/orrery/v1/epics_pb";
import { CENTRE, RING_RADII, VIEW, cellPath, point, sector, sliceGap, sliceStart } from "./orbits";
import { type Phase, PLACES } from "./phases";
import { type RecordTask, phaseOfPlace } from "./record";

// The requirement rim lies between radius ARC_INNER and ARC_OUTER, outside the rings.
export const ARC_INNER = 184;
export const ARC_OUTER = 192;

// LABEL_RADIUS is the radius a requirement's id is drawn at, just outside the rim.
const LABEL_RADIUS = 196;

// RECORD_GAP is the gap at 12 o'clock the record's chart opens, in degrees: wide enough that each ring's name reads whole
// along its ring.
const RECORD_GAP = 62;

// The charts drawn so far: each chart's ring-name paths take ids from its own number, so no two charts share one.
let charts = 0;

// RimGroup is one segment of the rim: a Requirement with the tasks that sit in it, or the unlabelled group of the tasks
// that name no Requirement. The group takes a slot for each task, and one slot when it has none, so the slots run from
// first to first plus slots.
interface RimGroup {
  requirement?: EpicRequirement;
  tasks: RecordTask[];
  first: number;
  slots: number;
}

// Rim is the chart's rim: its groups in order, the Requirements in the epic's order and the unlabelled group last, and
// the slots the groups take in all.
interface Rim {
  groups: RimGroup[];
  slots: number;
}

// rimOf groups the tasks under the requirements. A task sits in the first Requirement it names that the epic lists, in
// task order; a task naming none sits in the unlabelled last group, which is kept only when a task is in it.
function rimOf(tasks: RecordTask[], requirements: EpicRequirement[]): Rim {
  const byId = new Map<string, { requirement: EpicRequirement; tasks: RecordTask[] }>();
  for (const requirement of requirements) {
    byId.set(requirement.id, { requirement, tasks: [] });
  }
  const unlabelled: RecordTask[] = [];
  for (const task of tasks) {
    const named = task.requirements.find((id) => byId.has(id));
    const group = named === undefined ? undefined : byId.get(named);
    if (group === undefined) {
      unlabelled.push(task);
    } else {
      group.tasks.push(task);
    }
  }
  const groups: { requirement?: EpicRequirement; tasks: RecordTask[] }[] = [...byId.values()];
  if (unlabelled.length > 0) {
    groups.push({ tasks: unlabelled });
  }
  let first = 0;
  const laid = groups.map((group) => {
    const slots = Math.max(1, group.tasks.length);
    const placed = { ...group, first, slots };
    first += slots;
    return placed;
  });
  return { groups: laid, slots: first };
}

// segmentClass is a rim segment's class: its Requirement's server state, or the unlabelled group's.
function segmentClass(group: RimGroup): string {
  return group.requirement === undefined ? "req unlabelled" : `req ${group.requirement.state}`.trimEnd();
}

// Selection is what a click picked: a Requirement's segment by its id, or a task by its slug.
type Selection = { kind: "requirement"; id: string } | { kind: "task"; task: string };

// Selected is a selection as the chart draws it: its caption, and the angles its outline runs between.
interface Selected {
  caption: string;
  from: number;
  to: number;
}

// selectedOf is the selection's caption and outline, or undefined when the chart draws nothing it names.
function selectedOf(selection: Selection, rim: Rim): Selected | undefined {
  for (const group of rim.groups) {
    if (selection.kind === "requirement") {
      const requirement = group.requirement;
      if (requirement !== undefined && requirement.id === selection.id) {
        const words = [requirement.id, requirement.state.replace(/_/g, " "), requirement.text];
        return {
          caption: words.filter((word) => word !== "").join(" · "),
          from: sliceStart(rim.slots, group.first, RECORD_GAP),
          to: sliceStart(rim.slots, group.first + group.slots, RECORD_GAP),
        };
      }
    } else {
      for (const [offset, task] of group.tasks.entries()) {
        if (task.task === selection.task) {
          const slot = group.first + offset;
          return {
            caption: `${task.title} · ${task.words}`,
            from: sliceStart(rim.slots, slot, RECORD_GAP),
            to: sliceStart(rim.slots, slot + 1, RECORD_GAP),
          };
        }
      }
    }
  }
  return undefined;
}

// ringWay is the path a ring's name runs along: its centre radius, from the gap's left edge through the gap at 12 o'clock
// to where the ring's first cell begins.
function ringWay(radius: number, count: number): string {
  const left = point(radius, (-RECORD_GAP / 2) * (Math.PI / 180));
  const first = point(radius, sliceStart(count, 0, RECORD_GAP));
  return `M${left.x.toFixed(2)} ${left.y.toFixed(2)}A${radius} ${radius} 0 0 1 ${first.x.toFixed(2)} ${first.y.toFixed(2)}`;
}

// Orrery draws the epic's tasks as a rim outside the rings, one segment per requirement, and a wedge of five cells per
// task, one cell per ring with the rings outside in in PLACES order. A segment is labelled with its requirement's id and
// toned by its server state; a task's cells sit inside the first requirement it names, and the tasks naming none sit in
// an unlabelled last group, while a requirement no task names keeps an empty segment. Each ring's name runs along its ring
// through the gap at 12 o'clock. The centre reads how many of the requirements are proven. A click selects a segment or a
// task and captions it, with its outline drawn; nothing is captioned on hover. With onOpenPhase, a click on a task's cell
// also opens its task and that ring's phase; a planned task has no phase to open. No tasks draw nothing.
export function Orrery(props: {
  tasks: RecordTask[];
  requirements: EpicRequirement[];
  proven: ReadonlySet<string>;
  onOpenPhase?: (task: string, phase: Phase) => void;
}): JSX.Element | null {
  const { tasks, requirements, proven, onOpenPhase } = props;
  const [selection, setSelection] = useState<Selection | undefined>(undefined);
  const [chart] = useState(() => {
    charts += 1;
    return charts;
  });
  if (tasks.length === 0) {
    return null;
  }
  const rim = rimOf(tasks, requirements);
  const slots = rim.slots;
  const gap = sliceGap(slots, RECORD_GAP);
  const provenCount = requirements.filter((requirement) => proven.has(requirement.id)).length;
  const selected = selection === undefined ? undefined : selectedOf(selection, rim);
  return (
    <figure class="orrery-fig">
      <svg
        class="orrery"
        viewBox={`0 0 ${VIEW} ${VIEW}`}
        role="img"
        aria-label={`${provenCount} of ${requirements.length} proven`}
      >
        {rim.groups.map((group) => {
          const id = group.requirement?.id;
          return (
            <path
              key={`seg-${id ?? "unlabelled"}`}
              class={segmentClass(group)}
              d={sector(
                ARC_INNER,
                ARC_OUTER,
                sliceStart(slots, group.first, RECORD_GAP),
                sliceStart(slots, group.first + group.slots, RECORD_GAP),
                gap,
              )}
              data-requirement={id}
              role={id === undefined ? undefined : "button"}
              onClick={id === undefined ? undefined : () => setSelection({ kind: "requirement", id })}
            />
          );
        })}
        {rim.groups.flatMap((group) =>
          group.tasks.flatMap((task, offset) =>
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
                  d={cellPath(slots, group.first + offset, ring + 1, RECORD_GAP)}
                  data-task={task.task}
                  data-place={place}
                  data-phase={phase}
                  data-requirement={group.requirement?.id}
                  role="button"
                  onClick={() => {
                    setSelection({ kind: "task", task: task.task });
                    opener?.();
                  }}
                />
              );
            }),
          ),
        )}
        {selected !== undefined && (
          <path class="hl" d={sector(RING_RADII[RING_RADII.length - 1] - 5.5, ARC_OUTER, selected.from, selected.to)} />
        )}
        {rim.groups.map((group) => {
          const requirement = group.requirement;
          if (requirement === undefined) {
            return null;
          }
          const middle =
            (sliceStart(slots, group.first, RECORD_GAP) + sliceStart(slots, group.first + group.slots, RECORD_GAP)) / 2;
          const at = point(LABEL_RADIUS, middle);
          return (
            <text key={`id-${requirement.id}`} class="req-id" x={at.x} y={at.y} dy="0.35em" text-anchor="middle">
              {requirement.id}
            </text>
          );
        })}
        {PLACES.map((place, ring) => (
          <path key={`way-${place}`} id={`orrery-${chart}-${place}`} class="ring-way" d={ringWay(RING_RADII[ring], slots)} />
        ))}
        {PLACES.map((place) => (
          <text key={`name-${place}`} class="ring-name" text-anchor="end">
            <textPath href={`#orrery-${chart}-${place}`} startOffset="100%">
              {place}
            </textPath>
          </text>
        ))}
        <text class="count" x={CENTRE} y={CENTRE + 10} text-anchor="middle">
          {provenCount}
        </text>
        <text class="of" x={CENTRE} y={CENTRE + 30} text-anchor="middle">
          {`of ${requirements.length} proven`}
        </text>
      </svg>
      {selected !== undefined && <figcaption class="orrery-cap">{selected.caption}</figcaption>}
    </figure>
  );
}
