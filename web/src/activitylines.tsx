import type { JSX } from "preact";

import { elapsed } from "./dockwords";
import { Icon, StateGlyph } from "./glyphs";
import type { ListPanelKind } from "./router";
import type { ActivityLine } from "./work";

// shownLines is how many running things a conversation's activity draws as lines. More than this fold into one line
// that counts them, so the composer never leaves the screen.
const shownLines = 3;

export interface ActivityLinesProps {
  // lines are the conversation's running things, agents first, as activityOf gives them.
  lines: readonly ActivityLine[];
  // now is the clock the lines' times are read from, in unix seconds.
  now: number;
  // open is the list panel that is open, if any: each line of its kind is lit while it is open.
  open: ListPanelKind | null;
  // onOpen is a press on a line: it opens the list panel of the kind given.
  onOpen: (kind: ListPanelKind) => void;
}

// ActivityLines draws what runs under a conversation as one line above its composer: a state glyph, who it is, what it
// is for, what it is doing when it says, and how long it has run. Nothing running draws no container, so it takes no
// room. The first three lines are drawn; more fold into one line that counts the rest and opens the agents when one of
// them is an agent, else the running shells. A press on a line opens its list panel.
export function ActivityLines({ lines, now, open, onOpen }: ActivityLinesProps): JSX.Element | null {
  if (lines.length === 0) {
    return null;
  }
  const shown = lines.slice(0, shownLines);
  const folded = lines.slice(shownLines);
  const foldedKind: ListPanelKind = folded.some((line) => line.type === "agent") ? "agents" : "running";
  return (
    <div class="activity-lines" role="group" aria-label="running now">
      {shown.map((line) => (
        <button
          key={line.key}
          type="button"
          class={line.list === open ? "activity-line on" : "activity-line"}
          onClick={() => onOpen(line.list)}
        >
          <StateGlyph state={line.type === "agent" ? "working" : "idle"} />
          <span class="who">{line.who}</span>
          <span class="what">{line.what}</span>
          {line.doing !== "" && <span class="doing">{line.doing}</span>}
          <span class="clock">{elapsed(now - line.started)}</span>
          <Icon name="chevronRight" />
        </button>
      ))}
      {folded.length > 0 && (
        <button
          type="button"
          class={foldedKind === open ? "activity-line more on" : "activity-line more"}
          onClick={() => onOpen(foldedKind)}
        >
          <span class="what">and {folded.length} more running</span>
          <Icon name="chevronRight" />
        </button>
      )}
    </div>
  );
}
