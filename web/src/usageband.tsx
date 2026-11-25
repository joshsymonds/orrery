import type { JSX } from "preact";

import { Icon, StateGlyph } from "./glyphs";
import type { BandRow, LastCell, WindowDraw } from "./usagemodel";

// The hue class of each window column, in the order of windowClaims: 5h, 7d, and Fable for 7d_oi.
const windowHues = ["d5", "d7", "fb"] as const;

const secondsPerMinute = 60;
const secondsPerHour = 3600;
const secondsPerDay = 86400;

// durationText is a span of seconds in its largest whole unit: days, then hours, then minutes. It never reads under a
// minute.
function durationText(seconds: number): string {
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

// untilText is the time from now to at. A time that has passed reads as none left.
function untilText(at: number, now: number): string {
  return durationText(Math.max(0, at - now));
}

// lastText is what a row's last column reads: the time until its limit lifts when it is limited, its age when it is
// stale, the time to its 7d reset, or nothing. A lift with no reset reads nothing.
function lastText(last: LastCell, now: number): string {
  switch (last.kind) {
    case "lifts":
      return last.at === null ? "" : untilText(last.at, now);
    case "age":
      return `${durationText(Math.max(0, now - last.at))} ago`;
    case "reset":
      return untilText(last.at, now);
    case "none":
      return "";
  }
}

// marker draws a row's mark in its name: the limited glyph in a limited account's place, and otherwise the dot, filled
// when a context names the account.
function marker(row: BandRow): JSX.Element {
  if (row.limited) {
    return (
      <span class="limg">
        <StateGlyph state="limited" />
      </span>
    );
  }
  return <span class="udot" />;
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
// the columns 5h, 7d and Fable, then the last column. An account a context names has its dot filled. A limited account
// has the limited glyph in its dot's place, and its last column is the time until its limit lifts. A stale account
// that is not limited has its age in the last column, and an account with no drawn reading reads no reading yet. A
// press on the band is onOpen; the gear opens the accounts and routes page through onAccounts.
export function UsageBand({
  rows,
  now,
  onOpen,
  onAccounts,
}: {
  rows: readonly BandRow[];
  now: number;
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
          const classes = ["urow", row.inUse ? "inuse" : "", row.none ? "span" : ""].filter((name) => name !== "");
          return (
            <div
              key={row.account.id}
              class={classes.join(" ")}
              title={row.account.plan === "" ? row.account.name : `${row.account.name} · ${row.account.plan}`}
            >
              <span class="uname">
                {marker(row)}
                <span class="t">{row.account.name}</span>
              </span>
              {row.windows.map((draw, index) => windowCell(draw, windowHues[index]))}
              <span class={row.last.kind === "age" ? "urs stale" : "urs"}>{lastText(row.last, now)}</span>
              {row.none && <span class="note">no reading yet</span>}
            </div>
          );
        })}
      </div>
    </section>
  );
}
