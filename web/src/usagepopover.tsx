// The usage popover: every account's windows at reading size, in the band's columns and order. Each window cell adds its
// countdown to the reset, with the reset's clock in its tip. Under each name go who uses the account, else its plan, or
// when it is limited, when its limit lifts; then when it was read. An "Accounts and routes" link calls onAccounts. Escape,
// and a press outside the popover, call onClose. The band draws it beside the band, and the collapsed rail beside the rail.

import type { JSX } from "preact";
import { useEffect, useRef } from "preact/hooks";

import { ageText } from "./inboxrow";
import { windowClaims } from "./usagedata";
import type { BandRow, WindowDraw } from "./usagemodel";

// windowHues are the hue classes of the window cells, in the order of windowClaims: 5h, 7d, and Fable for 7d_oi.
const windowHues = ["d5", "d7", "fb"] as const;

const secondsPerMinute = 60;
const secondsPerHour = 3600;
const secondsPerDay = 86400;

// clockFormat is the 24-hour clock of the viewer's zone, two digits each side of the colon.
const clockFormat = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

// dayFormat is the short weekday of a day, for a reset that falls on another day.
const dayFormat = new Intl.DateTimeFormat("en-GB", { weekday: "short" });

// untilText is how long from now until the time at, in the words the popover uses: "3d", "2h 20m", "2h", "45m". A time that
// has passed reads as none left.
function untilText(at: number, now: number): string {
  const seconds = Math.max(0, at - now);
  const days = Math.floor(seconds / secondsPerDay);
  const hours = Math.floor((seconds % secondsPerDay) / secondsPerHour);
  const minutes = Math.floor((seconds % secondsPerHour) / secondsPerMinute);
  if (days > 0) {
    return `${days}d`;
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  }
  return `${minutes}m`;
}

// resetTip is a window's reset as its tip reads: its clock, with the weekday when the reset is not on today's date.
function resetTip(at: number, now: number): string {
  const reset = new Date(at * 1000);
  const clock = clockFormat.format(reset);
  return reset.toDateString() === new Date(now * 1000).toDateString()
    ? `resets ${clock}`
    : `resets ${dayFormat.format(reset)} ${clock}`;
}

// whoText is what sits under a row's name: who uses the account, else when its limit lifts while it is limited, else its plan.
function whoText(row: BandRow): string {
  if (row.inUse) {
    return `used by ${row.usedBy.join(", ")}`;
  }
  if (row.limited) {
    return row.liftsAt === null ? "limited" : `limited until ${clockFormat.format(new Date(row.liftsAt * 1000))}`;
  }
  return row.account.plan;
}

// WindowCell draws one window of a row: its value over a bar, and its countdown to the reset. A window no current reading
// carries reads as no reading.
function WindowCell({ draw, hue, now }: { draw: WindowDraw | null; hue: (typeof windowHues)[number]; now: number }): JSX.Element {
  if (draw === null || draw.percent === null) {
    return (
      <span class="up-win">
        <span class="up-v">no reading</span>
      </span>
    );
  }
  const level = draw.level === null ? "" : ` level-${draw.level}`;
  const width = Math.min(Math.max(draw.percent, 0), 100);
  return (
    <span class={`up-win ${hue}${level}`} title={draw.reset === null ? undefined : resetTip(draw.reset, now)}>
      <span class="up-v">{`${draw.percent}%`}</span>
      <span class="up-bar">
        <i style={{ width: `${width}%` }} />
      </span>
      <span class="up-r">{draw.reset === null ? "" : `in ${untilText(draw.reset, now)}`}</span>
    </span>
  );
}

// AccountRow draws one account: its name, what sits under it, when it was read, and its three windows.
function AccountRow({ row, now }: { row: BandRow; now: number }): JSX.Element {
  const who = whoText(row);
  return (
    <div class={row.inUse ? "up-row inuse" : "up-row"} data-account={row.account.id}>
      <div class="up-acct">
        <span class="up-nm">{row.account.name}</span>
        {who !== "" && <span class="up-who">{who}</span>}
        {row.readAt !== null && (
          <span class={row.stale === null ? "up-read" : "up-read stale"}>{`read ${ageText(row.readAt, now)} ago`}</span>
        )}
      </div>
      {row.windows.map((draw, index) => (
        <WindowCell key={windowClaims[index]} draw={draw} hue={windowHues[index]} now={now} />
      ))}
    </div>
  );
}

// UsagePopover draws every row: the band's account names and windows at reading size, with a header over the columns and
// the link to the accounts and routes page. Escape, and a press outside it, call onClose.
export function UsagePopover({
  rows,
  now,
  onAccounts,
  onClose,
}: {
  rows: readonly BandRow[];
  now: number;
  onAccounts: () => void;
  onClose: () => void;
}): JSX.Element {
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    const onPress = (event: Event) => {
      const target = event.target;
      const inside = target instanceof Node && root.current !== null && root.current.contains(target);
      if (!inside) {
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPress);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPress);
    };
  }, [onClose]);
  // A press in the popover is not a press on the band, which would open it again, so the popover stops it here.
  return (
    <div class="upop" role="dialog" aria-label="Claude accounts" ref={root} onClick={(event) => event.stopPropagation()}>
      <div class="up-head">
        <h3 class="up-title">Claude accounts</h3>
        <a
          class="up-link"
          href="#/accounts"
          onClick={(event) => {
            event.preventDefault();
            onAccounts();
          }}
        >
          Accounts and routes
        </a>
      </div>
      <div class="up-table">
        <span class="up-hd nm">account</span>
        <span class="up-hd c5">5h</span>
        <span class="up-hd c7">7d</span>
        <span class="up-hd cf">Fable</span>
        {rows.map((row) => (
          <AccountRow key={row.account.id} row={row} now={now} />
        ))}
      </div>
    </div>
  );
}
