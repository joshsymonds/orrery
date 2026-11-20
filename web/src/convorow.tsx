import type { JSX, VNode } from "preact";

import { EpicRow } from "./epicrow";
import type { Row } from "./rows";
import type { Convo, StandIn } from "./tree";

// ConvoRowProps are a conversation's row with what it steers under it: the conversation (its row is null for a
// stand-in), the clock its times read against, the key of the cursor's thing, the caller's draw of the row, and the
// press that selects a thing by its key.
export interface ConvoRowProps {
  convo: Convo;
  now: number;
  cursorKey: string;
  renderRow: (row: Row) => VNode;
  onSelect: (key: string) => void;
}

// standInWords says what a stand-in line says: its conversation is not listed, or it is archived.
function standInWords(standIn: StandIn): string {
  if (standIn === "archived") {
    return "Its conversation is archived";
  }
  if (standIn === "unlisted") {
    return "Its conversation is not listed";
  }
  throw new Error("a conversation with no row is a stand-in, which names why its conversation is not drawn");
}

// ConvoRow draws a conversation as its row, which the caller draws, or for a stand-in as one line saying its
// conversation is not listed or is archived. Under it, in .steers, hangs an EpicRow for each thing the tree hung under
// the conversation, in order, and no .steers when nothing hangs there. The cursor's key marks its EpicRow current, and
// its step current when it is a step's key. A press on a thing calls onSelect with its key.
export function ConvoRow({ convo, now, cursorKey, renderRow, onSelect }: ConvoRowProps): JSX.Element {
  return (
    <>
      {convo.row !== null ? (
        renderRow(convo.row)
      ) : (
        <div class="row standin" data-key={convo.key}>
          {standInWords(convo.standIn)}
        </div>
      )}
      {convo.hung.length > 0 && (
        <div class="steers" aria-label="what this conversation steers">
          {convo.hung.map((hung) => (
            <EpicRow
              key={hung.key}
              hung={hung}
              now={now}
              current={hung.key === cursorKey}
              currentStep={cursorKey}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </>
  );
}
