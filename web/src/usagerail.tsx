// The collapsed inbox's usage tile: the account in use, drawn as two rings, 7d outside and 5h inside, each stroked in its
// level. Its tip names the account and both values, and a click calls onOpen, which opens the usage popover beside the rail.

import { Fragment, type JSX } from "preact";

import { windowClaims } from "./usagedata";
import type { BandRow, WindowDraw } from "./usagemodel";

// ringCentre is the centre of the tile's ring drawing, in its 28-unit viewBox.
const ringCentre = 14;

// ringPlaces are the rings the tile draws, outermost first: 7d outside at radius 12, 5h inside at radius 7.
const ringPlaces = [
  { claim: "7d", radius: 12 },
  { claim: "5h", radius: 7 },
] as const;

// drawOf is the window a row draws for a claim, or null when no current reading carries it.
function drawOf(row: BandRow, claim: (typeof windowClaims)[number]): WindowDraw | null {
  return row.windows[windowClaims.indexOf(claim)] ?? null;
}

// valueText is a window's percent for the tip, or "no reading" when there is none.
function valueText(draw: WindowDraw | null): string {
  return draw === null || draw.percent === null ? "no reading" : `${draw.percent}%`;
}

// usageTip is the tile's tip: the account's name, then its 5h and 7d values.
function usageTip(row: BandRow): string {
  return `${row.account.name} · 5h ${valueText(drawOf(row, "5h"))} · 7d ${valueText(drawOf(row, "7d"))}`;
}

// UsageRailTile draws the usage tile of the account in use: its two rings, and its tip, which is also its accessible name.
// A ring with no current reading for its window is drawn as its track alone.
export function UsageRailTile({ row, onOpen }: { row: BandRow; onOpen: () => void }): JSX.Element {
  const tip = usageTip(row);
  return (
    <button type="button" class="utile" title={tip} aria-label={tip} data-account={row.account.id} onClick={onOpen}>
      <svg class="ring" viewBox="0 0 28 28" aria-hidden="true">
        {ringPlaces.map(({ claim, radius }) => {
          const draw = drawOf(row, claim);
          const circumference = 2 * Math.PI * radius;
          const percent = draw?.percent ?? null;
          const share = percent === null ? null : Math.min(100, Math.max(0, percent)) / 100;
          const level = draw?.level ?? null;
          return (
            <Fragment key={claim}>
              <circle class="track" cx={ringCentre} cy={ringCentre} r={radius} />
              {share !== null && level !== null && (
                <circle
                  class={`arc level-${level}`}
                  data-claim={claim}
                  cx={ringCentre}
                  cy={ringCentre}
                  r={radius}
                  stroke-dasharray={`${(share * circumference).toFixed(1)} ${circumference.toFixed(1)}`}
                  transform={`rotate(-90 ${ringCentre} ${ringCentre})`}
                />
              )}
            </Fragment>
          );
        })}
      </svg>
    </button>
  );
}
