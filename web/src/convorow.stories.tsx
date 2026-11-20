// The stories of ConvoRow: a conversation's row with what it steers under it, in each of round 6's five states. The rows
// are the fixture conversations the tree draws on the serving host, drawn with the real InboxRow, and the steered things
// are the fixture epics and contracts with no-op actions.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import { ConvoRow as ConvoRowView } from "./convorow";
import { InboxRow } from "./inboxrow";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import type { MenuEntry } from "./menu";
import { type Row, rowsFrom } from "./rows";
import { buildTree, type Convo } from "./tree";

// now is the instant the fixtures are set at, in unix seconds, as the page takes it.
const now = Date.parse(liveEpoch) / 1000;

// tree is the inbox tree of the fixtures, as the page places them on the serving host.
const tree = buildTree({
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
  view: "inbox",
});

// convoOf is the conversation the tree draws for the row key, which the fixtures must hold.
function convoOf(rowKey: string): Convo {
  const convo = tree.flatMap((project) => project.convos).find((candidate) => candidate.row?.key === rowKey);
  if (convo === undefined) {
    throw new Error(`the fixture tree draws no conversation for ${rowKey}`);
  }
  return convo;
}

const noop = () => {};

// row draws the conversation's row as one line of the inbox list, with its More holding the items the App gives a row.
function row(line: Row): VNode {
  const items: MenuEntry[] = [
    { label: "Open in a new window", icon: "open", onSelect: noop },
    ...(line.unseen ? [{ label: "Mark seen", icon: "check", kbd: "x", onSelect: noop } satisfies MenuEntry] : []),
    ...(line.archiveKind === "none" ? [] : ["sep" as const, { label: "Archive", icon: "archive", kbd: "A", onSelect: noop } satisfies MenuEntry]),
  ];
  return (
    <InboxRow
      row={line}
      now={now}
      cursor={false}
      more={{ open: false, items, toggle: noop, close: noop }}
      onOpen={noop}
      onSeen={noop}
    />
  );
}

// convo draws the conversation with key, and what it steers under it, with the cursor on no thing.
function convo(key: string): VNode {
  return <ConvoRowView convo={convoOf(key)} now={now} cursorKey="" renderRow={row} onSelect={noop} />;
}

export const ConvoRow: Stories = {
  // from-shell is the headless conversation that steers buoy-feeds, started from the shell.
  "from-shell": () => convo("session:6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55"),
  // plain is the lamp's conversation, which steers nothing, so it draws its row alone.
  plain: () => convo("pane:halyard:%11"),
  // steers-epic is the rota planner, which steers the rota-export epic.
  "steers-epic": () => convo("pane:halyard:%14"),
  // steers-several is the follow-ups conversation, which steers four epics.
  "steers-several": () => convo("pane:halyard:%22"),
  // wrote-contract is the tide conversation, which wrote the tide-alerts contract.
  "wrote-contract": () => convo("pane:halyard:%16"),
};
