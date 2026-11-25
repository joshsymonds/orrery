// The stories of Dir: a project's card in the four states round 6 draws it, on the fixture projects, in the inbox column.
// The conversations under each head are drawn as the inbox draws them, each row with what it steers under it, and the
// projects are what buildTree makes of the fixture rows.

import type { VNode } from "preact";

import type { Stories } from "./catalog/story";
import { ConvoRow as ConvoRowView } from "./convorow";
import { Dir as DirView } from "./dir";
import { liveEpoch } from "./fixtures/clock";
import { shippedSlices } from "./fixtures/slices";
import { inboxColumn } from "./inboxframe";
import { InboxRow } from "./inboxrow";
import type { MenuEntry } from "./menu";
import { type Row, rowsFrom } from "./rows";
import { buildTree, type Convo, type Project } from "./tree";

// now is the instant the fixtures are set at, in unix seconds, as the page takes it.
const now = Date.parse(liveEpoch) / 1000;

// projects are the fixture projects, as the inbox tree places the fixture rows on the serving host.
const projects = buildTree({
  rows: rowsFrom(shippedSlices.panes.panes, shippedSlices.sessions.sessions, shippedSlices.headless.sessions),
  epics: shippedSlices.epics.epics,
  details: shippedSlices.gets,
  contracts: shippedSlices.contracts.contracts,
  host: shippedSlices.host.host,
  view: "inbox",
});

// projectNamed is the fixture project with name, which the fixtures must hold.
function projectNamed(name: string): Project {
  const project = projects.find((candidate) => candidate.name === name);
  if (project === undefined) {
    throw new Error(`the fixtures hold no project ${name}`);
  }
  return project;
}

const noop = () => {};

// line draws row as one line of the inbox list, with its More holding the items the App gives a row.
function line(row: Row): VNode {
  const items: MenuEntry[] = [
    { label: "Open in a new window", icon: "open", onSelect: noop },
    ...(row.unseen ? [{ label: "Mark seen", icon: "check", kbd: "x", onSelect: noop } satisfies MenuEntry] : []),
    ...(row.archiveKind === "none" ? [] : ["sep" as const, { label: "Archive", icon: "archive", onSelect: noop } satisfies MenuEntry]),
  ];
  return (
    <InboxRow
      row={row}
      now={now}
      cursor={false}
      more={{ open: false, items, toggle: noop, close: noop }}
      onOpen={noop}
      onSeen={noop}
    />
  );
}

// convoRows draws each conversation of convos as the inbox draws it: its row, with what it steers hung under it, and its
// ended epics folded under it, shut.
function convoRows(convos: readonly Convo[]): VNode[] {
  return convos.map((convo) => (
    <ConvoRowView
      key={convo.key}
      convo={convo}
      now={now}
      cursorKey=""
      endedOpen={false}
      onEnded={noop}
      renderRow={line}
      onSelect={noop}
    />
  ));
}

export const Dir: Stories = {
  folded: () => {
    const project = projectNamed("lighthouse");
    return inboxColumn(
      <DirView
        project={project}
        folded={true}
        waiting={0}
        endedOpen={false}
        onFold={noop}
        onEnded={noop}
        ended={convoRows(project.ended)}
      >
        {convoRows(project.convos)}
      </DirView>,
    );
  },
  "folded-waiting": () => {
    const project = projectNamed("tidepool");
    return inboxColumn(
      <DirView
        project={project}
        folded={true}
        waiting={3}
        endedOpen={false}
        onFold={noop}
        onEnded={noop}
        ended={convoRows(project.ended)}
      >
        {convoRows(project.convos)}
      </DirView>,
    );
  },
  open: () => {
    const project = projectNamed("lighthouse");
    return inboxColumn(
      <DirView
        project={project}
        folded={false}
        waiting={0}
        endedOpen={false}
        onFold={noop}
        onEnded={noop}
        ended={convoRows(project.ended)}
      >
        {convoRows(project.convos)}
      </DirView>,
    );
  },
  "other-host": () => {
    const project = projectNamed("kelp");
    return inboxColumn(
      <DirView
        project={project}
        folded={true}
        waiting={0}
        endedOpen={false}
        onFold={noop}
        onEnded={noop}
        ended={convoRows(project.ended)}
      >
        {convoRows(project.convos)}
      </DirView>,
    );
  },
};
