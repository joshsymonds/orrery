import type { ComponentChildren, JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { Sheet } from "./sheet";
import type { Gauge, ListThing, RailGroup, Thing } from "./work";

export interface WorkRailProps {
  groups: RailGroup[];
  lit: string;
  layout: "rail" | "strip";
  onOpen: (thing: Thing) => void;
  end?: ComponentChildren;
}

// WorkRail draws what a conversation steers, in its named groups: on the desk's rail, or as the phone's strip, which
// ends with end. A group of more than three things is one tile of dots that opens a picker. lit is the key of the thing
// whose panel is open, "" for none. Choosing a thing calls onOpen with it.
export function WorkRail({ groups, lit, layout, onOpen, end }: WorkRailProps): JSX.Element {
  const drawn = groups.map((group) => (
    <GroupView key={group.kind} group={group} lit={lit} layout={layout} onOpen={onOpen} />
  ));
  if (layout === "strip") {
    return (
      <nav class="tilestrip" aria-label="what this conversation steers">
        {drawn}
        {end}
      </nav>
    );
  }
  return (
    <aside class="rail end" aria-label="what this conversation steers">
      {drawn}
    </aside>
  );
}

// Tile draws one thing as a tile: an epic's gauge, its short name, and ? when it asks, or a list's icon and count. lit
// marks the tile whose panel is open.
export function Tile({
  thing,
  lit,
  onOpen,
}: {
  thing: Thing;
  lit: boolean;
  onOpen: (thing: Thing) => void;
}): JSX.Element {
  const title = titleOf(thing);
  return (
    <button
      type="button"
      class={classes("tile-w", lit && "on", lit && thing.tone)}
      title={title}
      aria-label={title}
      onClick={() => onOpen(thing)}
    >
      <span class={classes("tile", thing.tone, lit && "cur")}>
        {thing.kind === "epic" ? <GaugeRing gauge={thing.gauge} /> : <ListIcon kind={thing.kind} />}
        {thing.kind === "epic" ? (
          thing.asks && <span class="n attention">?</span>
        ) : (
          <span class={`n ${thing.tone}`}>{thing.count}</span>
        )}
      </span>
      {thing.kind === "epic" && <span class="cap">{thing.short}</span>}
    </button>
  );
}

// dotsShown is how many things a dots tile draws a dot for.
const dotsShown = 9;

interface GroupViewProps {
  group: RailGroup;
  lit: string;
  layout: "rail" | "strip";
  onOpen: (thing: Thing) => void;
}

// GroupView draws one group: its name, then its tiles. A group of many things is a dots tile instead, and the dots tile
// opens the group's picker: a popover on the rail, a sheet in the strip.
function GroupView({ group, lit, layout, onOpen }: GroupViewProps): JSX.Element {
  const [picking, setPicking] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const picker = useRef<HTMLDivElement>(null);

  // The rail's picker takes focus on its first row when it opens.
  useEffect(() => {
    if (picking && layout === "rail") {
      picker.current?.querySelector<HTMLElement>(".rpick-row")?.focus();
    }
  }, [picking, layout]);

  // While the rail's picker is open, Escape shuts it from anywhere on the page. The window's capture listener runs before
  // every other key handler, and it stops the key there, so the page's own handler never acts on it.
  useEffect(() => {
    if (!picking || layout !== "rail") {
      return undefined;
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        setPicking(false);
      }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [picking, layout]);

  // The rail's picker shuts on a press outside the group, its tile and picker included.
  useEffect(() => {
    if (!picking || layout !== "rail") {
      return undefined;
    }
    const onPointerDown = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Node) || !wrap.current?.contains(target)) {
        setPicking(false);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [picking, layout]);

  const head = `${group.things.length} ${group.label}`;
  const asks = group.things.some((thing) => thing.tone === "attention");
  const litThing = group.things.find((thing) => thing.key === lit);
  const choose = (thing: Thing) => {
    setPicking(false);
    onOpen(thing);
  };
  const rows = group.things.map((thing) => (
    <button type="button" key={thing.key} class="rpick-row" role="menuitem" onClick={() => choose(thing)}>
      {thing.kind === "epic" && <GaugeRing gauge={thing.gauge} />}
      <span class="t">{nameOf(thing)}</span>
      <span class="s">{wordsOf(thing)}</span>
    </button>
  ));

  return (
    <div ref={wrap} class={`rgroup ${group.kind}`}>
      <span class="rlabel">{group.label}</span>
      {group.many ? (
        <button
          type="button"
          class={classes("tile-w", "group", litThing && "on", litThing && litThing.tone)}
          title={`${head}: ${group.things.map(nameOf).join(", ")}`}
          aria-label={head}
          onClick={() => setPicking(true)}
        >
          <span class={classes("tile", asks && "attention", litThing && "cur")}>
            <span class="dotgrid">
              {group.things.slice(0, dotsShown).map((thing) => (
                <i key={thing.key} class={thing.tone} />
              ))}
            </span>
            <span class={`n ${asks ? "attention" : "quiet"}`}>{asks ? "?" : group.things.length}</span>
          </span>
          <span class="cap">{group.things.length}</span>
        </button>
      ) : (
        group.things.map((thing) => (
          <Tile key={thing.key} thing={thing} lit={thing.key === lit} onOpen={onOpen} />
        ))
      )}
      {picking && layout === "rail" && (
        <div ref={picker} class="rpick" role="menu">
          <div class="menu-head">{head}</div>
          {rows}
        </div>
      )}
      {picking && layout === "strip" && (
        <Sheet title={head} onClose={() => setPicking(false)}>
          <div class="rpick" role="menu">
            {rows}
          </div>
        </Sheet>
      )}
    </div>
  );
}

// GaugeRing draws an epic's gauge: a planned ring, with its started and done arcs as shares of its total.
function GaugeRing({ gauge }: { gauge: Gauge }): JSX.Element {
  const total = Math.max(1, gauge.total);
  const started = (100 * (gauge.done + gauge.started)) / total;
  const done = (100 * gauge.done) / total;
  const label = gauge.total > 0 ? `${gauge.done} of ${gauge.total} tasks done` : "no tasks yet";
  return (
    <svg class="gauge" viewBox="0 0 24 24" role="img" aria-label={label}>
      <title>{label}</title>
      <circle class="planned" cx="12" cy="12" r="9" />
      {started > 0 && <Arc klass="started" share={started} />}
      {done > 0 && <Arc klass="done" share={done} />}
    </svg>
  );
}

// listIcons is each list's drawing, round 6's: the agents' star, and the running shell's prompt.
const listIcons: Record<ListThing["kind"], string> = {
  agents: "M8 2l1.5 4.5L14 8l-4.5 1.5L8 14l-1.5-4.5L2 8l4.5-1.5z",
  running: "M3 4l4 4-4 4M8.5 12H13",
};

// ListIcon draws a list's icon, stroked in the current colour, where an epic draws its gauge.
function ListIcon({ kind }: { kind: ListThing["kind"] }): JSX.Element {
  return (
    <svg class="icon" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor">
      <path d={listIcons[kind]} />
    </svg>
  );
}

// Arc draws one arc of a gauge's ring, share percent of the way round from the top.
function Arc({ klass, share }: { klass: string; share: number }): JSX.Element {
  return (
    <circle
      class={klass}
      cx="12"
      cy="12"
      r="9"
      pathLength={100}
      stroke-dasharray={`${share.toFixed(1)} 100`}
      transform="rotate(-90 12 12)"
    />
  );
}

// nameOf is what a thing is called: an epic's slug, or a list's key.
function nameOf(thing: Thing): string {
  return thing.kind === "epic" ? thing.slug : thing.key;
}

// wordsOf is what a thing is doing: an epic's state, or a list's count.
function wordsOf(thing: Thing): string {
  if (thing.kind === "epic") {
    return thing.state;
  }
  return thing.kind === "agents" ? `${thing.count} running` : `${thing.count} left running`;
}

// titleOf is a tile's name in full: its thing's name, then its words.
function titleOf(thing: Thing): string {
  return `${nameOf(thing)}: ${wordsOf(thing)}`;
}

// classes joins the class names that are not false.
function classes(...names: (string | false | undefined)[]): string {
  return names.filter((name) => name !== false && name !== undefined).join(" ");
}
