// The stories of InboxRow: one line of the inbox list in each state round 6 draws. The rows are the fixture slices read
// the way load.ts reads them, through rowsFrom, and the age of each line is drawn at the fixtures' instant.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { inboxColumn } from "./inboxframe";
import { InboxRow as InboxRowView, type RowMore } from "./inboxrow";
import type { MenuEntry } from "./menu";
import { type Row, rowsFrom } from "./rows";

// now is the instant the fixtures are set at, in unix seconds, as the page takes it.
const now = Date.parse(liveEpoch) / 1000;

// rows are the fixture rows, read as load.ts reads them.
const rows = rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions);

// rowOf is the fixture row with key, which the fixtures must hold.
function rowOf(key: string): Row {
  const row = rows.find((candidate) => candidate.key === key);
  if (row === undefined) {
    throw new Error(`the fixtures hold no row ${key}`);
  }
  return row;
}

const noop = () => {};

// more is the More of row in the story, its menu shut, holding the items the App gives the row.
function more(row: Row): RowMore {
  const items: MenuEntry[] = [
    { label: "Open in a new window", icon: "open", onSelect: noop },
    ...(row.unseen ? [{ label: "Mark seen", icon: "check", kbd: "x", onSelect: noop } satisfies MenuEntry] : []),
    ...(row.archiveKind === "none" ? [] : ["sep" as const, { label: "Archive", icon: "archive", onSelect: noop } satisfies MenuEntry]),
  ];
  return { open: false, items, toggle: noop, close: noop };
}

// line draws row as one line of the inbox list, in the inbox column. cursor, open and hover set the look the row's state
// takes.
function line(row: Row, look: { cursor?: boolean; open?: boolean; hover?: boolean } = {}): VNode {
  return inboxColumn(
    <InboxRowView
      row={row}
      now={now}
      cursor={look.cursor ?? false}
      open={look.open ?? false}
      hover={look.hover ?? false}
      more={more(row)}
      onOpen={noop}
      onSeen={noop}
    />,
  );
}

// Each state draws the fixture row round 6's catalogue draws for it: Unseen is the phase row, which is done and new,
// Cursor is the waiting row, Hover is the done row, and Open and OpenAndCursor are the working row.
export const InboxRow: Stories = {
  "waiting-question": () => line(rowOf("pane:halyard:%12")),
  working: () => line(rowOf("pane:halyard:%11")),
  unseen: () => line(rowOf("pane:halyard:%15")),
  done: () => line(rowOf("pane:halyard:%14")),
  failed: () => line(rowOf("pane:brine:%24")),
  idle: () => line(rowOf("pane:halyard:%23")),
  stopped: () => line(rowOf("session:9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90")),
  "cut-off": () => line(rowOf("session:4be0c6d2-8f3a-4c71-9e25-ephemeris001")),
  cursor: () => line(rowOf("pane:halyard:%12"), { cursor: true }),
  hover: () => line(rowOf("pane:halyard:%14"), { hover: true }),
  open: () => line(rowOf("pane:halyard:%11"), { open: true }),
  "open-and-cursor": () => line(rowOf("pane:halyard:%11"), { cursor: true, open: true }),
};
