// The stories of InboxRow: one line of the inbox list in each state the fixtures hold. The rows are the fixture slices read
// the way load.ts reads them, through rowsFrom, and the age of each line is drawn at the fixtures' instant.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import type { ArchiveControl } from "./epicline";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { type Answer, InboxRow as InboxRowView } from "./inboxrow";
import { parseQuestion } from "./question";
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

// idle is the ⋯ menu of a line that is not open.
const idle: ArchiveControl = { archived: false, open: false, run: () => {}, toggle: () => {}, close: () => {} };

const noop = () => {};

// line draws row as one line of the inbox list, with the answer a landed answer left, if any.
function line(row: Row, answer?: Answer): VNode {
  return (
    <InboxRowView
      row={row}
      now={now}
      cursor={false}
      archive={idle}
      conversation={null}
      answer={answer}
      onOpen={noop}
      onSeen={noop}
    />
  );
}

// waitingRow is pane %12, which asks which index to add, and its options.
const waitingRow = rowOf("pane:halyard:%12");

// The answer a landed choice of the second option leaves on the waiting line.
const answered: Answer = {
  question: waitingRow.question,
  choice: 2,
  label: parseQuestion(waitingRow.question).options[1].label,
};

export const InboxRow: Stories = {
  "waiting-question": () => line(waitingRow),
  answered: () => line(waitingRow, answered),
  working: () => line(rowOf("pane:halyard:%11")),
  unseen: () => line(rowOf("pane:halyard:%13")),
  "cut-off": () => line(rowOf("session:4be0c6d2-8f3a-4c71-9e25-ephemeris001")),
};
