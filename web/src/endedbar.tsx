import type { JSX } from "preact";
import { useState } from "preact/hooks";

import type { Row } from "./rows";
import { Sheet } from "./sheet";
import type { SteerCalls } from "./steer";
import { SteerMenu, useSteer } from "./steermenu";

// The note an unanswered move shows while the epic is read again. steermenu.tsx holds the same text for the menu.
const unansweredText = "The server did not answer, so the move may have gone through. Reading the epic again…";

// endedLead is the bar's lead when the conversation that steered an epic has ended.
export function endedLead(slug: string): string {
  return `This conversation has ended, and ${slug} still runs. Continue it in a new conversation, which starts with the contract and the epic’s log, or move it to one that is running.`;
}

// unsteeredLead is the bar's lead when no conversation steers an epic.
export function unsteeredLead(slug: string): string {
  return `No conversation steers ${slug}. Continue it in a new conversation, which starts with the contract and the epic’s log, or move it to one that is running.`;
}

// EndedBar stands where a conversation's dock was when the conversation steering an epic has ended. Its lead says so, and
// its first button continues the epic in a new conversation. Its second opens the steering menu: in the anchor beside the
// button at the desk, and in a sheet when narrow. One steering serves both buttons and the menu, so a call either makes
// holds every control until it is answered. A refusal or a call that gave no answer shows its note in the bar while the
// menu is shut.
export function EndedBar(props: {
  epic: string;
  slug: string;
  repository: string;
  steering: string;
  rows: Row[];
  lead: string;
  calls: SteerCalls;
  onSteered: (session: string) => void;
  narrow: boolean;
}): JSX.Element {
  const { epic, slug, repository, steering, rows, lead, calls, onSteered, narrow } = props;
  const steer = useSteer(epic, calls, onSteered);
  const [open, setOpen] = useState(false);
  const busy = steer.state.kind === "sending" || steer.state.kind === "unanswered";
  const menu = (
    <SteerMenu
      slug={slug}
      steering={steering}
      repository={repository}
      rows={rows}
      steer={steer}
      onClose={() => setOpen(false)}
    />
  );
  let note: string | null = null;
  if (steer.state.kind === "refused") {
    note = steer.state.reason;
  } else if (steer.state.kind === "unanswered") {
    note = unansweredText;
  }
  return (
    <div class="dock ended-bar">
      <div class="dock-box ended" data-state="stopped">
        <p class="ended-lead">{lead}</p>
        <div class="acts">
          <button type="button" class="act primary" disabled={busy} onClick={() => steer.choose(null)}>
            Continue in a new conversation
          </button>
          <span class="steer-anchor">
            <button type="button" class="act quiet" disabled={busy} onClick={() => setOpen((isOpen) => !isOpen)}>
              Steer from another…
            </button>
            {open && !narrow && menu}
          </span>
        </div>
        {!open && note !== null && <p class="steer-note error">{note}</p>}
      </div>
      {open && narrow && <Sheet title={`Steer ${slug} from`} onClose={() => setOpen(false)}>{menu}</Sheet>}
    </div>
  );
}
