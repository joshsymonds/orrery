import type { JSX, VNode } from "preact";

import { EpicRow } from "./epicrow";
import { Icon } from "./glyphs";
import type { Row } from "./rows";
import type { Convo, Hung } from "./tree";

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
}

// ConvoRow draws a conversation as its row, which the caller draws. Under it, in .steers, hangs an EpicRow for each thing
// the tree hung under the conversation, in order, then, when the conversation has ended epics, a fold line that reads how
// many they are, and under it, while the fold is open, an EpicRow for each folded epic. No .steers is drawn when nothing
// hangs there. A conversation with no row is its one thing, drawn as that thing's EpicRow at the top level. The cursor's
// key marks its EpicRow current, and its step current when it is a step's key. A press on a thing calls onSelect with its
// key.
export function ConvoRow({ convo, now, cursorKey, endedOpen, onEnded, renderRow, onSelect }: ConvoRowProps): JSX.Element {
  const thing = (hung: Hung): VNode => (
    <EpicRow
      key={hung.key}
      hung={hung}
      now={now}
      current={hung.key === cursorKey}
      currentStep={cursorKey}
      onSelect={onSelect}
    />
  );
  const things = convo.hung.map(thing);
  if (convo.row === null) {
    return <>{things}</>;
  }
  const folded = convo.folded;
  return (
    <>
      {renderRow(convo.row)}
      {(things.length > 0 || folded.length > 0) && (
        <div class="steers" aria-label="what this conversation steers">
          {things}
          {folded.length > 0 && (
            <button type="button" class="steers-fold" aria-expanded={endedOpen} onClick={onEnded}>
              <Icon name={endedOpen ? "chevronDown" : "chevronRight"} />
              <span>{`${folded.length} ended`}</span>
            </button>
          )}
          {endedOpen && folded.map(thing)}
        </div>
      )}
    </>
  );
}
