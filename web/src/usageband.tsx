import type { JSX } from "preact";

import { Icon } from "./glyphs";
import { windowClaims } from "./usagedata";
import { type BandRow, dollarsText, type SpendTotals, type WindowDraw } from "./usagemodel";

// The hue class of each window column, in the order of windowClaims: 5h, 7d, and Fable for 7d_oi.
const windowHues = ["d5", "d7", "fb"] as const;

// sevenDayColumn is the window column the 7d reset is read from.
const sevenDayColumn = windowClaims.indexOf("7d");

const secondsPerMinute = 60;
const secondsPerHour = 3600;
const secondsPerDay = 86400;

// untilText is the time from now to at in its largest whole unit: days, then hours, then minutes. It never reads under
// a minute.
function untilText(at: number, now: number): string {
  const seconds = Math.max(0, at - now);
  const days = Math.floor(seconds / secondsPerDay);
  if (days > 0) {
    return `${days}d`;
  }
  const hours = Math.floor(seconds / secondsPerHour);
  if (hours > 0) {
    return `${hours}h`;
  }
  return `${Math.max(1, Math.floor(seconds / secondsPerMinute))}m`;
}

// windowCell draws one window column of a row: its value right-aligned over a bar that fills the column, in the level's
// class. A window with no number draws an empty cell, so the columns stay aligned.
function windowCell(draw: WindowDraw | null, hue: (typeof windowHues)[number]): JSX.Element {
  if (draw === null || draw.percent === null) {
    return <span key={hue} />;
  }
  const level = draw.level === "warn" || draw.level === "crit" ? ` ${draw.level}` : "";
  const width = Math.min(Math.max(draw.percent, 0), 100);
  return (
    <span key={hue} class={`uv ${hue}${level}`}>
      <span class="v">{`${draw.percent}%`}</span>
      <span class="bar">
        <i style={{ width: `${width}%` }} />
      </span>
    </span>
  );
}

// UsageBand draws the usage band under the inbox head: a table with a row for each account, in the order of rows, and
// the columns 5h, 7d and Fable, then the time to the 7d reset. An account a context names has its dot filled. Under the
// table, a line totals the seven days' spend: what the seats billed, and what they list at. The line is not drawn while
// spend is null, which it is until a Spend read lands. A press on the band is onOpen; the gear opens the accounts and
// routes page through onAccounts.
export function UsageBand({
  rows,
  now,
  spend,
  onOpen,
  onAccounts,
}: {
  rows: readonly BandRow[];
  now: number;
  spend: SpendTotals | null;
  onOpen: () => void;
  onAccounts: () => void;
}): JSX.Element {
  return (
    <section class="uband" aria-label="usage" onClick={() => onOpen()}>
      <div class="ugrid">
        <span class="hd nm">
          Claude
          <button
            type="button"
            class="ctlq"
            aria-label="Accounts and routes"
            title="Accounts and routes"
            onClick={(event) => {
              // The gear is a control of the band, so its press does not also open the band.
              event.stopPropagation();
              onAccounts();
            }}
          >
            <Icon name="gear" />
          </button>
        </span>
        <span class="hd c5">5h</span>
        <span class="hd c7">7d</span>
        <span class="hd cf">Fable</span>
        <span class="hd">resets</span>
        {rows.map((row) => {
          const reset = row.windows[sevenDayColumn]?.reset ?? null;
          return (
            <div
              key={row.account.id}
              class={row.inUse ? "urow inuse" : "urow"}
              title={row.account.plan === "" ? row.account.name : `${row.account.name} · ${row.account.plan}`}
            >
              <span class="uname">
                <span class="udot" />
                <span class="t">{row.account.name}</span>
              </span>
              {row.windows.map((draw, index) => windowCell(draw, windowHues[index]))}
              <span class="urs">{reset === null ? "" : untilText(reset, now)}</span>
            </div>
          );
        })}
      </div>
      {spend !== null && (
        <div class="uspend">
          <span class="lbl">Billed 7d</span> <span class="amt">{dollarsText(spend.billed)}</span>{" "}
          <span>list {dollarsText(spend.list)}</span>
        </div>
      )}
    </section>
  );
}
