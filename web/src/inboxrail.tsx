// The collapsed inbox's rail: the mark tile that expands the inbox, a tile per project, then New, Search, Notices and
// Keys. A project tile is a button with its count, its whole name set sideways and its dots, drawn as round 7's B2c
// tile; a project with nothing waiting and nothing new is drawn quiet. One project's flyout opens beside its tile.

import type { JSX, RefObject } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { Icon, type IconName, StateGlyph } from "./glyphs";
import { ageText, quietRow, SecondLine } from "./inboxrow";
import { Mark } from "./mark";
import type { FlyoutModel, FlyoutRow, RailTileModel } from "./railtiles";
import { ReplyText } from "./text";

// noticesTip is the tip of the Notices control: its unread count when there is one, else the name alone.
export function noticesTip(unread: number): string {
  return unread > 0 ? `Notices · ${unread} unread` : "Notices";
}

// RailTile draws one square tile on the rail: the mark or an icon, and its count when it is above zero. label is its
// accessible name; tip, when given, is its title with its keyboard hint, and the title is label otherwise. newCount draws
// its count in the new role, as the Notices tile's unread count is drawn.
export function RailTile({
  label,
  tip,
  mark,
  icon,
  tone,
  bare,
  n,
  newCount,
  onClick,
}: {
  label: string;
  tip?: string;
  mark?: boolean;
  icon?: IconName;
  tone?: "primary";
  bare?: boolean;
  n?: number;
  newCount?: boolean;
  onClick: () => void;
}): JSX.Element {
  const look = ["tile", tone, bare && "bare", mark && "brand"].filter(Boolean).join(" ");
  return (
    <button type="button" class={look} title={tip ?? label} aria-label={label} data-tip={tip} onClick={onClick}>
      {mark && <Mark />}
      {icon !== undefined && <Icon name={icon} />}
      {n !== undefined && n > 0 && <span class={newCount ? "n new" : "n"}>{n}</span>}
    </button>
  );
}

// ProjectTile draws a project's tile on the rail: its waiting count above when it is above zero, its whole name set
// sideways, and its running and new dots below. It is cur while its project holds the open page, quiet while nothing in it
// waits on you and nothing is new, and open while its flyout is. Its title, the project's name with its count, is its
// accessible name.
export function ProjectTile({
  tile,
  open = false,
  onClick,
}: {
  tile: RailTileModel;
  open?: boolean;
  onClick: () => void;
}): JSX.Element {
  const look = ["vtile", tile.cur && "cur", tile.quiet && "quiet", open && "open"].filter(Boolean).join(" ");
  return (
    <button type="button" class={look} title={tile.title} aria-label={tile.title} data-key={tile.key} onClick={onClick}>
      {tile.n > 0 && <span class="n">{tile.n}</span>}
      <span class="vn">{tile.name}</span>
      {(tile.live || tile.fresh) && (
        <span class="vdots">
          {tile.live && <i class="live" />}
          {tile.fresh && <i class="new" />}
        </span>
      )}
    </button>
  );
}

// ProjectFlyout draws one project's flyout: its name with its counts and its path, its rows that wait on you and then its
// new rows, the line that counts the rest, and its foot, which opens the project's page beside the flyout's keys. The
// first row is the cursor, the row a click on the tile or ⏎ opens; with no rows, the page is. A press on a row calls
// onRow with it, and the foot calls onPage with the project's key.
export function ProjectFlyout({
  model,
  now,
  flyRef,
  onRow,
  onPage,
}: {
  model: FlyoutModel;
  now: number;
  flyRef?: RefObject<HTMLDivElement>;
  onRow: (row: FlyoutRow) => void;
  onPage: (key: string) => void;
}): JSX.Element {
  return (
    <div class="pfly" role="dialog" aria-label={model.name} ref={flyRef}>
      <div class="pf-head">
        <span class="pf-nm">{model.name}</span>
        <span class="pf-n">
          {model.waits !== "" && <span class="is-attention">{model.waits}</span>}
          {model.fresh !== "" && <span class="is-new">{model.fresh}</span>}
        </span>
        <span class="pf-path">{model.path}</span>
      </div>
      {model.rows.length > 0 && (
        <div class="pf-rows">
          {model.rows.map((row, index) => (
            <FlyoutLine key={row.key} row={row} now={now} cur={index === 0} onOpen={() => onRow(row)} />
          ))}
        </div>
      )}
      {model.rest !== "" && <div class="pf-more">{model.rest}</div>}
      <div class="pf-foot">
        <button
          type="button"
          class={model.rows.length === 0 ? "pf-open cur" : "pf-open"}
          onClick={() => onPage(model.key)}
        >
          Open {model.name}’s page
        </button>
        <span class="pf-keys">
          <kbd>⏎</kbd> open <kbd>↑</kbd>
          <kbd>↓</kbd> projects <kbd>esc</kbd>
        </span>
      </div>
    </div>
  );
}

// FlyoutLine draws one row of a flyout: a conversation's row with its glyph, title, age and second line, as the inbox
// draws it; or a hung epic or contract that waits, with its name, its age and what it asks.
function FlyoutLine({ row, now, cur, onOpen }: { row: FlyoutRow; now: number; cur: boolean; onOpen: () => void }) {
  const unseen = row.kind === "row" && row.row.unseen;
  return (
    <div class={cur ? "pf-row cur" : "pf-row"} data-key={row.key} data-unseen={unseen ? "" : undefined} onClick={onOpen}>
      {row.kind === "row" ? (
        <>
          <StateGlyph state={quietRow(row.row) ? "idle" : row.row.state} />
          <span class="t">
            <ReplyText text={row.row.label} />
          </span>
          <span class="age">{ageText(row.row.changed, now)}</span>
          <SecondLine row={row.row} now={now} />
        </>
      ) : (
        <>
          <StateGlyph state="waiting" />
          <span class="t">{row.name}</span>
          <span class="age">{ageText(row.changed, now)}</span>
          {row.ask !== "" && <span class="s asks">{row.ask}</span>}
        </>
      )}
    </div>
  );
}

// InboxRail draws the collapsed inbox's rail: the mark tile expands the inbox, the project tiles follow the model's order
// in a scroll area of their own, and New, Search, Notices and Keys sit under the spring. Notices counts the unread notices
// and opens the centre. The pointer over a project tile calls onHover with its key, over the flyout keeps it, and over
// any other part of the rail, or leaving it, calls onHoverOff. The flyout, when given, lies beside the rail, level with
// its tile's top and held inside the rail's height.
export function InboxRail({
  tiles,
  flyout,
  now,
  onMark,
  onTile,
  onHover,
  onHoverOff,
  onFlyoutRow,
  onFlyoutPage,
  onNew,
  onSearch,
  unread,
  onNotices,
  onKeys,
}: {
  tiles: readonly RailTileModel[];
  flyout: FlyoutModel | null;
  now: number;
  onMark: () => void;
  onTile: (key: string) => void;
  onHover: (key: string) => void;
  onHoverOff: () => void;
  onFlyoutRow: (row: FlyoutRow) => void;
  onFlyoutPage: (key: string) => void;
  onNew: () => void;
  onSearch: () => void;
  unread: number;
  onNotices: () => void;
  onKeys: () => void;
}): JSX.Element {
  const navRef = useRef<HTMLElement>(null);
  const tilesRef = useRef<HTMLDivElement>(null);
  const flyRef = useRef<HTMLDivElement>(null);
  // The flyout's top is its tile's, measured from the rail's top after each render, so a scroll of the tiles or a change
  // of the rows moves it with them. A flyout that would run past the rail's foot is held at it.
  useLayoutEffect(() => {
    const nav = navRef.current;
    const fly = flyRef.current;
    if (nav === null || fly === null || flyout === null) {
      return;
    }
    const tile = Array.from(tilesRef.current?.children ?? []).find((child) => child.getAttribute("data-key") === flyout.key);
    if (tile === undefined) {
      return;
    }
    const rail = nav.getBoundingClientRect();
    const top = tile.getBoundingClientRect().top - rail.top;
    const lowest = rail.height - fly.getBoundingClientRect().height;
    fly.style.top = `${Math.max(0, Math.min(top, lowest))}px`;
  });
  const pointerOver = (event: PointerEvent) => {
    const target = event.target instanceof Element ? event.target : null;
    if (target?.closest(".pfly") != null) {
      return;
    }
    const tile = target?.closest(".rail-tiles > .vtile");
    const key = tile?.getAttribute("data-key");
    if (key != null) {
      onHover(key);
    } else {
      onHoverOff();
    }
  };
  return (
    <nav
      class="rail start"
      aria-label="inbox, collapsed"
      ref={navRef}
      onPointerOver={pointerOver}
      onPointerLeave={() => onHoverOff()}
    >
      <RailTile mark bare label="Expand the inbox" tip="Expand the inbox  b" onClick={onMark} />
      <span class="rail-sep" />
      <div class="rail-tiles" ref={tilesRef}>
        {tiles.map((tile) => (
          <ProjectTile key={tile.key} tile={tile} open={flyout?.key === tile.key} onClick={() => onTile(tile.key)} />
        ))}
      </div>
      {flyout !== null && (
        <ProjectFlyout model={flyout} now={now} flyRef={flyRef} onRow={onFlyoutRow} onPage={onFlyoutPage} />
      )}
      <span class="spring" />
      <RailTile icon="plus" tone="primary" label="New session" onClick={onNew} />
      <RailTile icon="search" bare label="Search" onClick={onSearch} />
      <RailTile icon="bell" bare label="Notices" tip={noticesTip(unread)} n={unread} newCount onClick={onNotices} />
      <RailTile icon="keys" bare label="Keys" onClick={onKeys} />
    </nav>
  );
}
