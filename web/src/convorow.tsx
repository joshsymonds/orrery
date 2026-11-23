import { type JSX, type VNode, cloneElement } from "preact";

import { EpicRow } from "./epicrow";
import { Icon } from "./glyphs";
import type { HeldThing } from "./held";
import type { Row } from "./rows";
import type { Convo, DesignNode, Hung } from "./tree";

// ConvoRowProps are a conversation's row with what it steers under it: the conversation (its row is null for a thing
// drawn as its own row), the clock its times read against, the key of the cursor's thing, whether the conversation's fold
// of ended epics is open and the press that toggles it, the caller's draw of the row, and the press that selects a thing
// by its key.
export interface ConvoRowProps {
  convo: Convo;
  now: number;
  cursorKey: string;
  endedOpen: boolean;
  onEnded: () => void;
  renderRow: (row: Row) => VNode;
  onSelect: (key: string) => void;
  // held is the thing held open, which the tree marks, or null when none is held.
  held?: HeldThing | null;
}

// designMark is a design's mark: round 8's design icon, drawn on the 16 grid as glyphs draws its icons. The held card's head
// draws the same mark.
export const designMark =
  "M3.5 2.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM3.5 10.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM12.5 6.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM5 4h1.5c2 0 2 4 4.5 4M5 12h1.5c2 0 2-4 4.5-4";

// DesignBlock draws a design as a block of its own: its tab, which holds the mark and the bold name, then, directly under the
// tab with no gap, the well, which is the design's page. The page's first line is the design's state, and then come the
// things it owns, as the caller drew them. The count of its contracts is beside the tab, on the block's ground. A press on
// the tab calls onSelect with the design's key. held is whether the design is the thing held open, which the tab marks.
function DesignBlock({
  node,
  things,
  held,
  onSelect,
}: {
  node: DesignNode;
  things: VNode[];
  held: boolean;
  onSelect: (key: string) => void;
}): JSX.Element {
  const count = node.design.contracts.length;
  return (
    <div class="design-block">
      <div
        class="design-tab"
        role="link"
        data-key={node.key}
        data-held={held ? "open" : undefined}
        onClick={() => onSelect(node.key)}
      >
        <svg class="icon design-mark" viewBox="0 0 16 16" aria-hidden="true">
          <path d={designMark} />
        </svg>
        <span class="design-name">{node.design.title}</span>
      </div>
      <div class="design-well">
        <p class="design-state">{node.design.state}</p>
        {things}
      </div>
      <span class="design-count">{`${count} contract${count === 1 ? "" : "s"}`}</span>
    </div>
  );
}

// ConvoRow draws a conversation as its row, which the caller draws. A conversation with anything of its own is a block headed
// by that row: under it, in .steers, are the things in no design, each an EpicRow, then a DesignBlock for each design it
// works, then, when the conversation has ended epics, a fold line that reads how many they are, and under it, while the fold
// is open, an EpicRow for each folded epic. A conversation with nothing of its own draws its row alone. A conversation with
// no row is its one thing, drawn as that thing's EpicRow at the top level, and a design with no row is its DesignBlock at the
// top level. The cursor's key marks its EpicRow current, and its step current when it is a step's key. A press on a thing
// calls onSelect with its key. When held names this conversation, it holds the thing held open: the block is the tab, the
// conversation's row is marked convo, and the open thing's row, or its design's tab, is marked open. A conversation with no
// row holds its open thing with no tab.
export function ConvoRow({
  convo,
  now,
  cursorKey,
  endedOpen,
  onEnded,
  renderRow,
  onSelect,
  held,
}: ConvoRowProps): JSX.Element {
  // open is the key of the thing this conversation holds open, or null when it holds none.
  const open = held && held.convo === convo.key ? held.key : null;
  const thing = (hung: Hung): VNode => (
    <EpicRow
      key={hung.key}
      hung={hung}
      now={now}
      current={hung.key === cursorKey}
      currentStep={cursorKey}
      held={hung.key === open}
      onSelect={onSelect}
    />
  );
  const designs = convo.designs ?? [];
  const designed = new Set(designs.flatMap((node) => node.hung.map((hung) => hung.key)));
  const own = convo.hung.filter((hung) => !designed.has(hung.key)).map(thing);
  const blocks = designs.map((node) => (
    <DesignBlock
      key={node.key}
      node={node}
      things={node.hung.map(thing)}
      held={node.key === open}
      onSelect={onSelect}
    />
  ));
  if (convo.row === null) {
    return (
      <>
        {own}
        {blocks}
      </>
    );
  }
  const folded = convo.folded;
  const drawn = renderRow(convo.row);
  const row = open === null ? drawn : cloneElement(drawn, { "data-held": "convo" });
  if (own.length === 0 && folded.length === 0 && designs.length === 0) {
    return <>{row}</>;
  }
  return (
    <div class="convo-block" data-held={open === null ? undefined : "tab"}>
      {row}
      <div class="steers" aria-label="what this conversation steers">
        {own}
        {blocks}
        {folded.length > 0 && (
          <button type="button" class="steers-fold" aria-expanded={endedOpen} onClick={onEnded}>
            <Icon name={endedOpen ? "chevronDown" : "chevronRight"} />
            <span>{`${folded.length} ended`}</span>
          </button>
        )}
        {endedOpen && folded.map(thing)}
      </div>
    </div>
  );
}
