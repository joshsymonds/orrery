// The usage popover: one account's windows, each with its value, its level and its countdown to the reset, drawn beside the
// collapsed inbox's rail. The rail's usage tile opens it, and it shows the account the tile names.

import type { JSX } from "preact";

import { windowClaims } from "./usagedata";
import type { BandRow, WindowDraw } from "./usagemodel";

// windowLabels are the column names of the windows: 7d_oi is headed Fable, as the band heads it.
const windowLabels: Readonly<Record<(typeof windowClaims)[number], string>> = { "5h": "5h", "7d": "7d", "7d_oi": "Fable" };

// untilText is how long from now until the time at, in the words the popover uses: "3d", "2h 20m", "45m". A time that has
// passed reads as none left.
function untilText(at: number, now: number): string {
  const seconds = Math.max(0, at - now);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) {
    return `${days}d`;
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  }
  return `${minutes}m`;
}

// UsagePopover draws one account's row: its name, the contexts that use it when any does, and a line for each window, its
// label, its percent in its level, and when its limit resets, counted from now. A window no current reading carries reads
// as no reading.
export function UsagePopover({ row, now }: { row: BandRow; now: number }): JSX.Element {
  return (
    <div class="upop" role="dialog" aria-label={`${row.account.name} usage`} data-account={row.account.id}>
      <div class="up-head">
        <span class="up-nm">{row.account.name}</span>
        {row.inUse && <span class="up-use">used by {row.usedBy.join(", ")}</span>}
      </div>
      {windowClaims.map((claim, index) => (
        <WindowLine key={claim} label={windowLabels[claim]} draw={row.windows[index] ?? null} now={now} />
      ))}
    </div>
  );
}

// WindowLine draws one window of the popover: its label, its percent, and its countdown to the reset.
function WindowLine({ label, draw, now }: { label: string; draw: WindowDraw | null; now: number }): JSX.Element {
  if (draw === null || draw.percent === null) {
    return (
      <div class="up-win">
        <span class="up-lbl">{label}</span>
        <span class="up-v">no reading</span>
      </div>
    );
  }
  const level = draw.level === null ? "" : ` level-${draw.level}`;
  return (
    <div class={`up-win${level}`}>
      <span class="up-lbl">{label}</span>
      <span class="up-v">{draw.percent}%</span>
      <span class="up-r">{draw.reset === null ? "" : `in ${untilText(draw.reset, now)}`}</span>
    </div>
  );
}
