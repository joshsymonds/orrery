import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";

import type { Conversation } from "./conversation";
import { Icon, StateGlyph } from "./glyphs";
import { Menu, type MenuEntry } from "./menu";
import { type ParsedQuestion, parseQuestion } from "./question";
import type { Row } from "./rows";
import { ReplyText } from "./text";

export const stateWaiting = "waiting";

const secondsPerMinute = 60;
const minutesPerHour = 60;
const hoursPerDay = 24;

// RowMore is the ⋯ menu a row's More button opens: open is whether it is the row's open menu, toggle opens or shuts it,
// close shuts it, and items is what it holds, in order. The App passes the items, and each acts on the row alone. sheet,
// when set, is the line the menu's sheet draws under the row's title; absent, the menu is a popover.
export interface RowMore {
  open: boolean;
  items: readonly MenuEntry[];
  sheet?: string;
  toggle: () => void;
  close: () => void;
}

// InboxRow draws one row of the inbox list as round 6 draws it: its dot and state glyph, its title and age, its second
// line, and its More. A click calls onOpen and a double-click calls onSeen, each unless the press landed on a link. cursor
// is whether the row is the cursor's; open is whether its page is open, and hover whether it is drawn hovered.
export function InboxRow({
  row,
  now,
  cursor,
  open = false,
  hover = false,
  more,
  onOpen,
  onSeen,
}: {
  row: Row;
  now: number;
  cursor: boolean;
  open?: boolean;
  hover?: boolean;
  more: RowMore;
  onOpen: () => void;
  onSeen: () => void;
}) {
  return (
    <div
      class={rowClass(cursor, open, hover, more.open)}
      tabIndex={-1}
      data-row=""
      data-key={row.key}
      data-unseen={row.unseen ? "" : undefined}
      aria-current={open ? "page" : cursor ? "true" : undefined}
      onClick={(event) => {
        if (!isLinkClick(event)) {
          onOpen();
        }
      }}
      onDblClick={(event) => {
        if (!isLinkClick(event)) {
          onSeen();
        }
      }}
    >
      <span class="dot" title={row.unseen ? "new since you looked" : undefined} />
      <StateGlyph state={row.state} />
      <div class="main">
        <div class="l1">
          <span class="title">
            <ReplyText text={row.label} />
          </span>
          <span class="age">{ageText(row.changed, now)}</span>
          <More more={more} title={row.label} />
        </div>
        <SecondLine row={row} now={now} />
      </div>
    </div>
  );
}

// readerQuestion is the question the cursor's reader draws: a waiting pane row's stored question, or a session's open
// request as its stream holds it. A row with no question draws none.
export function readerQuestion(row: Row, conversation: Conversation | null): ParsedQuestion | null {
  if (row.kind === "pane") {
    return row.state === stateWaiting && row.question !== "" ? parseQuestion(row.question) : null;
  }
  const request = conversation?.state?.request;
  if (request === undefined) {
    return null;
  }
  return { title: request.question, options: request.options.map((label) => ({ label, description: "" })) };
}

// rowClass is a row's class: the row, cur for the cursor, open while its page is open, menu-open while its More menu is
// open, and hover while it is drawn hovered.
function rowClass(cursor: boolean, open: boolean, hover: boolean, menuOpen: boolean): string {
  return ["b-row", "row", cursor && "cur", open && "open", menuOpen && "menu-open", hover && "hover"].filter(Boolean).join(" ");
}

// SecondLine draws a row's second line. A waiting row says what it asks, led by a question mark when it holds a question.
// A working pane's line is its tool, its argument, and its clock from the tool's start. A terminal with no state says it
// has no conversation. A session's line is its summary. Any other pane has no second line.
function SecondLine({ row, now }: { row: Row; now: number }): JSX.Element | null {
  const line = row.unseen ? "l2" : "l2 one";
  if (row.state === stateWaiting) {
    return (
      <div class={row.question !== "" ? `${line} asks` : line}>
        {row.question !== "" && <Icon name="question" />}
        <ReplyText text={waitingText(row)} />
      </div>
    );
  }
  const tool = row.kind === "pane" && row.state === "working" ? parseTool(row.tool) : null;
  if (tool !== null) {
    return (
      <div class={line}>
        <span class="name">{shortName(tool.name)}</span>
        {tool.input !== "" && ` ${tool.input}`}
        {tool.start > 0 && ` · ${clockText(now - tool.start)}`}
      </div>
    );
  }
  if (row.kind === "pane" && row.state === "") {
    return <div class={line}>a terminal with no conversation</div>;
  }
  if (row.kind === "session" && row.summary !== "") {
    return (
      <div class={line}>
        <ReplyText text={row.summary} />
      </div>
    );
  }
  return null;
}

// waitingText is what a waiting row asks: its question's title, else a session's last words, else that it waits on you.
// A pane's summary is its title, so a pane never repeats it here.
export function waitingText(row: Row): string {
  if (row.question !== "") {
    return parseQuestion(row.question).title;
  }
  return row.kind === "session" && row.summary !== "" ? row.summary : "waits on you";
}

// shortName reads a tool name by the part after its last "__", so mcp__orrery__step_start reads as step_start.
export function shortName(name: string): string {
  const at = name.lastIndexOf("__");
  return at < 0 ? name : name.slice(at + 2);
}

// More draws a row's More button and the menu it opens. The button's tip names what the menu holds. A press on More or
// on the menu stops there, so it opens no row, moves no cursor and marks nothing seen. An item calls its own action and
// then shuts the menu, and an open menu scrolls into view within the list. With more.sheet set, the menu opens as a sheet
// over a scrim instead: headed by title and the sheet's line, its items without key hints. A press on the scrim outside
// the dialog shuts the sheet and calls nothing.
function More({ more, title }: { more: RowMore; title: string }): JSX.Element {
  const menuRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (more.open) {
      menuRef.current?.scrollIntoView({ block: "nearest" });
    }
  }, [more.open]);
  const stop = (event: Event) => event.stopPropagation();
  const tip = more.items.flatMap((item) => (item === "sep" ? [] : [item.label])).join(", ");
  const items = more.items.map((item) =>
    item === "sep"
      ? item
      : {
          ...item,
          kbd: more.sheet === undefined ? item.kbd : undefined,
          onSelect: () => {
            item.onSelect();
            more.close();
          },
        },
  );
  return (
    <div class="row-menu" onClick={stop} onDblClick={stop} onPointerDown={stop}>
      <button type="button" class="more" aria-label="more" title={tip} aria-expanded={more.open} onClick={more.toggle}>
        <Icon name="more" />
      </button>
      {more.open &&
        (more.sheet === undefined ? (
          <div class="row-menu-list" ref={menuRef}>
            <Menu items={items} />
          </div>
        ) : (
          <div
            class="sheet-scrim"
            onClick={(event) => {
              if (event.target === event.currentTarget) {
                more.close();
              }
            }}
          >
            <section class="sheet" role="dialog">
              <div class="sheet-head">
                <b>{title}</b>
                {more.sheet}
              </div>
              <Menu items={items} />
            </section>
          </div>
        ))}
    </div>
  );
}

// RowTool is a pane's running tool as its row reads it: its name, its argument, and the unix seconds it started.
interface RowTool {
  name: string;
  input: string;
  start: number;
}

// parseTool reads a row's tool JSON. An empty or unparsable value, or one that is not a JSON object, has no tool.
export function parseTool(value: string): RowTool | null {
  if (value === "") {
    return null;
  }
  let json: unknown;
  try {
    json = JSON.parse(value);
  } catch {
    return null;
  }
  if (typeof json !== "object" || json === null || Array.isArray(json)) {
    return null;
  }
  const fields = json as Record<string, unknown>;
  return {
    name: typeof fields.name === "string" ? fields.name : "",
    input: typeof fields.input === "string" ? fields.input : "",
    start: typeof fields.start === "number" ? fields.start : 0,
  };
}

// isLinkClick reports whether a click or double click landed on a link, which opens it and moves nothing else.
export function isLinkClick(event: Event): boolean {
  return event.target instanceof Element && event.target.closest("a") !== null;
}

// clockText is an elapsed time as m:ss, its minutes unbounded. It never reads negative.
export function clockText(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  return `${Math.floor(total / secondsPerMinute)}:${String(total % secondsPerMinute).padStart(2, "0")}`;
}

export function ageText(changed: number, now: number): string {
  if (changed <= 0) {
    return "";
  }
  const seconds = Math.max(0, now - changed);
  if (seconds < secondsPerMinute) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / secondsPerMinute);
  if (minutes < minutesPerHour) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / minutesPerHour);
  if (hours < hoursPerDay) {
    return `${hours}h`;
  }
  return `${Math.floor(hours / hoursPerDay)}d`;
}
