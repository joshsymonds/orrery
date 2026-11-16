import type { JSX } from "preact";

import { type EpicItem, denseStrip, epicLine } from "./epics";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { VIEW, cellPath, sector, sliceSpan, sliceStart } from "./orbits";
import { ARC_INNER, ARC_OUTER } from "./orrery";
import { requirementArcs, wedgesOf } from "./phases";

// EpicLine draws an epic's line: its small orrery, then its slug, repository and count, then its state and blocker. A
// full epic also draws its strip of one segment per record task. An epic with no detail draws no orrery.
export function EpicLine({
  epic,
  full,
  current,
  now,
  onSelect,
}: {
  epic: EpicItem;
  full: boolean;
  current: boolean;
  now: number;
  onSelect: (item: EpicItem) => void;
}): JSX.Element {
  const line = epicLine(epic, now);
  return (
    <div
      class={current ? "b-row epic cur" : "b-row epic"}
      data-row=""
      data-key={epic.key}
      aria-current={current ? "true" : undefined}
      onClick={() => onSelect(epic)}
    >
      {epic.detail !== undefined && <MiniOrrery detail={epic.detail} />}
      <div class="main">
        <div class="l1">
          <span class="label">{epic.slug}</span>
          <span class="proj">{epic.repository}</span>
          <span class="rt">{line.count}</span>
        </div>
        <div class="words">
          {line.state !== "" && <span class="state">{line.state}</span>}
          {line.blocker !== "" && <span class="blocker">{line.blocker}</span>}
        </div>
        {full && epic.strip.length > 0 && (
          <div class={epic.strip.length > denseStrip ? "strip dense" : "strip"}>
            {epic.strip.map((tone) => (
              <i class={`k-${tone}`} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// MiniOrrery draws an epic's wedges as the orrery does: a rim cell and four phase cells for each, and the requirement
// arcs. It draws no text and takes no pointer, since the line's words say what the chart counts.
function MiniOrrery({ detail }: { detail: EpicsGetResponse }): JSX.Element {
  const wedges = wedgesOf(detail);
  const arcs = requirementArcs(wedges, detail.requirements);
  const count = wedges.length;
  const arcStart = sliceStart(count, 0);
  const arcSpan = sliceSpan(count);
  return (
    <svg class="orrery" viewBox={`0 0 ${VIEW} ${VIEW}`} aria-hidden="true">
      {wedges.map((wedge, index) => (
        <path
          key={`rim-${wedge.task}`}
          class={`cell rim ${wedge.place === "planned" ? "planned" : "passed"}`}
          d={cellPath(count, index, 1)}
        />
      ))}
      {wedges.flatMap((wedge, index) =>
        wedge.slices.map((slice, ring) => (
          <path
            key={`${wedge.task}-${slice.phase}`}
            class={`cell phase-${slice.phase} ${slice.state}${slice.state === "now" ? ` t-${wedge.tone}` : ""}`}
            d={cellPath(count, index, ring + 2)}
          />
        )),
      )}
      {arcs.map((arc) => (
        <path
          key={`${arc.id}-${arc.start}`}
          class={`req ${arc.proven ? "proven" : "open"}`}
          d={sector(ARC_INNER, ARC_OUTER, arcStart + arc.start * arcSpan, arcStart + arc.end * arcSpan)}
          data-req={arc.id}
        />
      ))}
    </svg>
  );
}
