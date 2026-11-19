// The stories of RowMenu and EpicLine: the ⋯ menu shut and open, and an epic's line in each way the list draws it. The
// lines are the fixture epics' items as the app's list reads them, with no-op actions. No fixture epic holds record
// tasks, so no line here draws its strip.

import type { Stories } from "./catalog/story";
import { type ArchiveControl, EpicLine as Line, RowMenu as Menu } from "./epicline";
import { type EpicItem, type EpicsView, endedWords, epicsView } from "./epics";
import { liveEpoch } from "./fixtures/clock";
import { epics, gets } from "./fixtures/epics";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// view is the list the fixture epics make, read as the app reads it.
const view = epicsView(epics.epics, gets);

// bare is the same list with no detail for any epic, so its lines draw no orrery.
const bare = epicsView(epics.epics, new Map());

// lineOf is the line item of the epic with the id, from the list that holds it.
function lineOf(list: EpicsView, id: string): EpicItem {
  const item = [
    ...list.blocks.map((block) => block.epic),
    ...list.waiting,
    ...list.justEnded,
    ...list.endedEarlier,
    ...list.archived,
  ].find((candidate) => candidate.id === id);
  if (item === undefined) {
    throw new Error(`the fixture epics hold no line for ${id}`);
  }
  return item;
}

// control is a line's ⋯ menu, shut or open, over no-op actions. An archived line's one item is Unarchive.
function control(open: boolean, archived = false): ArchiveControl {
  return { archived, open, run: noop, toggle: noop, close: noop };
}

export const RowMenu: Stories = {
  shut: () => <Menu control={control(false)} />,
  "archive-open": () => <Menu control={control(true)} />,
  "unarchive-open": () => <Menu control={control(true, true)} />,
};

export const EpicLine: Stories = {
  // running is a building epic's line, with its orrery and its count and state. Its block is full, as the app passes
  // block.full.
  running: () => (
    <Line epic={lineOf(view, "orrery/orrery-followups")} full current={false} now={now} onSelect={noop} />
  ),
  // current is the line the cursor is on.
  current: () => <Line epic={lineOf(view, "tidepool/tide-alerts")} full={false} current now={now} onSelect={noop} />,
  // ended is an ended epic's line, whose ended words stand in for its count, state and blocker.
  ended: () => {
    const item = lineOf(view, "orrery/holder-scopes");
    return <Line epic={item} full={false} current={false} now={now} ended={endedWords(item, now)} onSelect={noop} />;
  },
  // with-menu is a line with its ⋯ menu shut.
  "with-menu": () => (
    <Line
      epic={lineOf(view, "tidepool/offline-tables")}
      full={false}
      current={false}
      now={now}
      archive={control(false)}
      onSelect={noop}
    />
  ),
  // no-detail is a line of an epic with no Get detail, which draws no orrery.
  "no-detail": () => (
    <Line epic={lineOf(bare, "orrery/orrery-followups")} full={false} current={false} now={now} onSelect={noop} />
  ),
};
