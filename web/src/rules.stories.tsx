// The stories of the rules: a Rule draws a mark across the column, and the reader draws every one with it. Round 6's
// states: a compaction closed and open, the day it opens on, the owner's interruption, and the since-you-looked mark.

import type { Stories } from "./catalog/story";
import { Rule as RuleLine } from "./rules";

const noop = () => {};

export const Rule: Stories = {
  compaction: () => (
    <RuleLine kind="compaction" label="Compacted" detail="auto · 611k tokens" onToggle={noop} open={false} />
  ),
  "compaction-open": () => (
    <RuleLine kind="compaction" label="Compacted" detail="auto · 611k tokens" onToggle={noop} open />
  ),
  day: () => <RuleLine kind="day" label="Monday 16 November" />,
  interrupted: () => <RuleLine kind="interrupted" label="Interrupted by you" detail="14:02" />,
  new: () => <RuleLine kind="new" label="New since you looked" />,
};
