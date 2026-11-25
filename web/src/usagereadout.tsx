// UsageReadout is the title row's readout of a conversation's usage: the word usage, then the session's list total in
// dollars. It is lit while the Usage panel is open, and a press on it calls onOpen, which opens the panel or shuts it.

import type { JSX } from "preact";

import type { UsageSessionTotal } from "./gen/orrery/v1/usage_pb";
import { dollars, sessionSum } from "./usagefigures";

export function UsageReadout({
  totals,
  open,
  onOpen,
}: {
  totals: readonly UsageSessionTotal[];
  open: boolean;
  onOpen: () => void;
}): JSX.Element {
  return (
    <button
      type="button"
      class="usage-readout"
      aria-pressed={open ? "true" : "false"}
      title="Usage of this conversation"
      onClick={() => onOpen()}
    >
      <span class="usage-readout-name">usage</span> <span class="usage-readout-figure">{dollars(sessionSum(totals).list)}</span>
    </button>
  );
}
