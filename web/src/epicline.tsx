import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { type EpicItem, denseStrip, epicLine } from "./epics";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { VIEW, cellPath, sector, sliceSpan, sliceStart } from "./orbits";
import { ARC_INNER, ARC_OUTER } from "./orrery";
import { requirementArcs, wedgesOf } from "./phases";

// ArchiveControl is the ⋯ menu a list line draws and the archive its one item holds. archived is whether the line sits in
// the Archived view, whose item is Unarchive; run archives the line, or unarchives it there. open is whether this line's
// menu is the open one; toggle opens or closes it on a press of ⋯, and close shuts it.
export interface ArchiveControl {
  archived: boolean;
  open: boolean;
  run: () => void;
  toggle: () => void;
  close: () => void;
}

// RowMenu draws a line's ⋯ button and the menu it opens. The menu holds one item, Archive, or Unarchive in the Archived
// view: choosing it runs the control and closes the menu. The ⋯ and the menu stop click, double-click and pointerdown
// propagation, so a press on either moves no cursor, opens no row and marks nothing seen. An open menu scrolls into view
// within the list.
export function RowMenu({ control }: { control: ArchiveControl }): JSX.Element {
  const menuRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (control.open) {
      menuRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [control.open]);
  const stop = (event: Event) => event.stopPropagation();
  return (
    <div class="row-menu" onClick={stop} onDblClick={stop} onPointerDown={stop}>
      <button type="button" class="more" aria-label="more" aria-expanded={control.open} onClick={control.toggle}>
        ⋯
      </button>
      {control.open && (
        <div class="row-menu-list" ref={menuRef}>
          <button
            type="button"
            onClick={() => {
              control.run();
              control.close();
            }}
          >
            {control.archived ? "Unarchive" : "Archive"}
          </button>
        </div>
      )}
    </div>
  );
}

// EpicLine draws an epic's line: its small orrery, then its slug, repository and count, then its state and blocker, and
// its ⋯ menu when it has one. An ended epic's line draws its ended words as its state, in place of the count, state and
// blocker. A full epic also draws its strip of one segment per record task. An epic with no detail draws no orrery. Each
// state and blocker span is keyed by its text, so a changed phrase mounts anew and its move plays.
export function EpicLine({
  epic,
  full,
  current,
  now,
  ended,
  archive,
  onSelect,
}: {
  epic: EpicItem;
  full: boolean;
  current: boolean;
  now: number;
  ended?: string;
  archive?: ArchiveControl;
  onSelect: (item: EpicItem) => void;
}): JSX.Element {
  const line = ended === undefined ? epicLine(epic, now) : { count: "", state: ended, blocker: "" };
  return (
    <div
      class={`b-row epic${current ? " cur" : ""}${ended === undefined ? "" : " ended"}`}
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
          {line.state !== "" && <span class="state" key={line.state}>{line.state}</span>}
          {line.blocker !== "" && <span class="blocker" key={line.blocker}>{line.blocker}</span>}
        </div>
        {full && epic.strip.length > 0 && (
          <div class={epic.strip.length > denseStrip ? "strip dense" : "strip"}>
            {epic.strip.map((tone) => (
              <i class={`k-${tone}`} />
            ))}
          </div>
        )}
      </div>
      {archive !== undefined && <RowMenu control={archive} />}
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
