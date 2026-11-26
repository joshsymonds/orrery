// Meter: one host stat drawn as a meter, its name, its bar and its value. A host's stats and a host's load and memory
// draw it alike, so it lives apart from the strip that holds the stats.

import type { JSX } from "preact";

// MeterStat is what a meter draws: its name, its value as text, its percent when it has one, and its level.
export interface MeterStat {
  label: string;
  value: string;
  percent?: number;
  level: string;
}

// Meter draws one stat: its name, its bar, and its value, in the series it is given and at its level. The bar is filled to
// its percent; a stat with no percent draws an empty bar line.
export function Meter({ stat, series }: { stat: MeterStat; series: number }): JSX.Element {
  return (
    <span class={`meter series-${series} level-${stat.level}`}>
      <span class="name">{stat.label}</span>
      <span class="bar">{stat.percent !== undefined && <i style={{ width: `${stat.percent}%` }} />}</span>
      <span class="value">{stat.value}</span>
    </span>
  );
}
