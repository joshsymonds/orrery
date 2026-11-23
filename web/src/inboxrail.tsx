// The collapsed inbox's rail: the mark tile that expands the inbox, a tile per project, then New, Search, Notices and
// Keys. A project tile is a button with its count, its whole name set sideways and its dots, drawn as round 7's B2c
// tile; a project with nothing waiting and nothing new is drawn quiet. One project's flyout opens beside its tile.

import { Fragment, type JSX, type RefObject } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import { Icon, type IconName, StateGlyph } from "./glyphs";
import type { HeldMove } from "./held";
import { ageText, quietRow, SecondLine } from "./inboxrow";
import { Mark } from "./mark";
import type { FlyoutModel, FlyoutRow, LineageConvo, LineageMark, RailTileModel } from "./railtiles";
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

// RailLineage draws a project's lineage on the rail, the tree's nesting in small: a pill for each conversation that works a
// design or holds an open epic or contract of its own, holding the conversation's mark; the conversation's own marks, then
// a pill for each design, its mark on a head with a rounded top, touching a smaller pill under it that holds a mark for each
// contract or epic in order. A design's mark and a child's mark each call onPress with its key. The held conversation's
// mark is a neck, and the open mark a lozenge, each drawn out to the rail's edge (D12, D14). move is the motion of the change
// that holds the open thing, which each held conversation mark and open mark carries as data-move. projectKey is the project
// whose tile the block lies under, which the block carries for the pointer.
export function RailLineage({
  projectKey,
  convos,
  move = "",
  onPress,
}: {
  projectKey: string;
  convos: readonly LineageConvo[];
  move?: HeldMove;
  onPress: (key: string) => void;
}): JSX.Element {
  const motion = move === "" ? undefined : move;
  return (
    <div class="rail-lineage" data-project={projectKey}>
      {convos.map((convo) => (
        <div class="lconvo" key={convo.key}>
          <span
            class="lmark lconv"
            role="img"
            aria-label="conversation"
            title="conversation"
            data-held={convo.held ? "convo" : undefined}
            data-move={convo.held ? motion : undefined}
          >
            <Icon name="chat" />
          </span>
          {convo.own.map((mark) => (
            <LineageButton key={mark.key} mark={mark} move={motion} onPress={onPress} />
          ))}
          {convo.designs.map((design) => (
            <div class="ldesign" key={design.key}>
              <button
                type="button"
                class={design.open ? "lmark open" : "lmark"}
                data-held={design.open ? "open" : undefined}
                data-move={design.open ? motion : undefined}
                aria-label={`design: ${design.name}`}
                title={`design: ${design.name}`}
                onClick={() => onPress(design.key)}
              >
                <DesignIcon />
              </button>
              <div class="lwell">
                {design.marks.map((mark) => (
                  <LineageButton key={mark.key} mark={mark} move={motion} onPress={onPress} />
                ))}
              </div>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// LineageButton draws an epic's or contract's mark on the lineage, which calls onPress with its key. The open one is held,
// and carries move as data-move.
function LineageButton({
  mark,
  move,
  onPress,
}: {
  mark: LineageMark;
  move: HeldMove | undefined;
  onPress: (key: string) => void;
}): JSX.Element {
  return (
    <button
      type="button"
      class={mark.open ? "lmark open" : "lmark"}
      data-held={mark.open ? "open" : undefined}
      data-move={mark.open ? move : undefined}
      aria-label={`${mark.kind}: ${mark.name}`}
      title={`${mark.kind}: ${mark.name}`}
      onClick={() => onPress(mark.key)}
    >
      <Icon name={mark.kind === "contract" ? "doc" : "pools"} />
    </button>
  );
}

// DesignIcon draws round 8's design mark: two marks on a line, a third beside them, and the links that join them.
function DesignIcon(): JSX.Element {
  return (
    <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3.5 2.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM3.5 10.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM12.5 6.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 4h1.5c2 0 2 4 4.5 4M5 12h1.5c2 0 2-4 4.5-4" />
    </svg>
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
// in a scroll area of their own, each with its lineage under it, and New, Search, Notices and Keys sit under the spring.
// Notices counts the unread notices and opens the centre. The pointer over a project tile or its lineage calls onHover with
// the project's key, over the flyout keeps it, and over any other part of the rail, or leaving it, calls onHoverOff. A press
// on a lineage mark calls onLineage with its key. The flyout, when given, lies beside the rail, level with its tile's top
// and held inside the rail's height.
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
  onLineage,
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
  onLineage?: (key: string) => void;
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
    const lineage = target?.closest(".rail-tiles > .rail-lineage");
    const key = tile?.getAttribute("data-key") ?? lineage?.getAttribute("data-project");
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
        {tiles.map((tile) => {
          const lineage = tile.lineage ?? [];
          return (
            <Fragment key={tile.key}>
              <ProjectTile tile={tile} open={flyout?.key === tile.key} onClick={() => onTile(tile.key)} />
              {lineage.length > 0 && (
                <RailLineage projectKey={tile.key} convos={lineage} onPress={(key) => onLineage?.(key)} />
              )}
            </Fragment>
          );
        })}
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
