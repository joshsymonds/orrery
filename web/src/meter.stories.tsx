// The stories of Meter: a host stat in each series, and at warn and crit.

import type { Stories } from "./catalog/story";
import * as meter from "./meter";

export const Meter: Stories = {
  series1: () => <meter.Meter stat={{ label: "load", value: "12%", percent: 12, level: "ok" }} series={1} />,
  series2: () => <meter.Meter stat={{ label: "memory", value: "44%", percent: 44, level: "ok" }} series={2} />,
  warn: () => <meter.Meter stat={{ label: "load", value: "71%", percent: 71, level: "warn" }} series={1} />,
  crit: () => <meter.Meter stat={{ label: "memory", value: "94%", percent: 94, level: "crit" }} series={2} />,
};
