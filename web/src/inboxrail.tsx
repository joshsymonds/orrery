// The inbox rail at rest: the mark tile, a tile per project, then New, Search and Keys. A tile is a button with its
// letters, its count when there is one, its dots and its current mark, drawn as round 6's Tile.

import type { JSX } from "preact";
import { Mark } from "./mark";
import { Icon, type IconName } from "./glyphs";
import type { RailTileModel } from "./railtiles";

// RailTile draws one tile on the rail: a mark, an icon or two letters, its count when it is above zero, its dots, and
// the current mark. label is its accessible name; tip, when given, is its keyboard hint and stands in for the title.
export function RailTile({
  label,
  tip,
  mark,
  icon,
  text,
  tone,
  cur,
  bare,
  n,
  live,
  fresh,
  onClick,
}: {
  label: string;
  tip?: string;
  mark?: boolean;
  icon?: IconName;
  text?: string;
  tone?: "place" | "primary";
  cur?: boolean;
  bare?: boolean;
  n?: number;
  live?: boolean;
  fresh?: boolean;
  onClick: () => void;
}): JSX.Element {
  const look = ["tile", tone, cur && "cur", bare && "bare", mark && "brand"].filter(Boolean).join(" ");
  return (
    <button
      type="button"
      class={look}
      title={tip === undefined ? label : undefined}
      aria-label={label}
      data-tip={tip}
      onClick={onClick}
    >
      {mark && <Mark />}
      {icon !== undefined && <Icon name={icon} />}
      {text}
      {n !== undefined && n > 0 && <span class="n">{n}</span>}
      {(live || fresh) && (
        <span class="dots">
          {live && <i class="live" />}
          {fresh && <i class="new" />}
        </span>
      )}
    </button>
  );
}

// InboxRail draws the rail at rest: the mark tile opens the inbox, the tiles are the projects in the model's order,
// and New, Search and Keys sit under the spring. Pointer enter and leave report to the page, leave with the element
// the pointer went to.
export function InboxRail({
  tiles,
  onMark,
  onTile,
  onNew,
  onSearch,
  onKeys,
  onEnter,
  onLeave,
}: {
  tiles: readonly RailTileModel[];
  onMark: () => void;
  onTile: (key: string) => void;
  onNew: () => void;
  onSearch: () => void;
  onKeys: () => void;
  onEnter: () => void;
  onLeave: (to: EventTarget | null) => void;
}): JSX.Element {
  return (
    <nav
      class="rail start"
      aria-label="inbox, at rest"
      onPointerEnter={() => onEnter()}
      onPointerLeave={(event) => onLeave(event.relatedTarget)}
    >
      <RailTile mark bare label="Open the inbox" tip="Inbox  b" onClick={onMark} />
      <span class="rail-sep" />
      {tiles.map((tile) => (
        <RailTile
          key={tile.key}
          tone="place"
          text={tile.text}
          n={tile.n}
          live={tile.live}
          fresh={tile.fresh}
          cur={tile.cur}
          label={tile.title}
          onClick={() => onTile(tile.key)}
        />
      ))}
      <span class="spring" />
      <RailTile icon="plus" tone="primary" label="New session" onClick={onNew} />
      <RailTile icon="search" bare label="Search" onClick={onSearch} />
      <RailTile icon="keys" bare label="Keys" onClick={onKeys} />
    </nav>
  );
}
