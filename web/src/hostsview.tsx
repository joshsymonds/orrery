// HostsView: the hosts view, every host Host.Status lists, one row each, in the order the server lists them. A host that
// answers draws its load and memory as meters and says reachable; one that does not draws the unreachable glyph, its name
// marked down, and no numbers, since its load and memory are not known now.

import type { JSX } from "preact";

import type { HostEntry, HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { StateGlyph } from "./glyphs";
import { HostLabel } from "./hostlabel";
import { Meter, type MeterStat } from "./meter";

// HostsView draws the status's hosts, one row each. A null status lists no host.
export function HostsView({ status }: { status: HostStatusResponse | null }): JSX.Element {
  return (
    <section class="hosts" aria-label="hosts">
      {(status?.hosts ?? []).map((host, index) => (
        <HostRow key={index} host={host} />
      ))}
    </section>
  );
}

// HostRow draws one host: its label, and its load and memory meters with the word reachable when it answers, or its
// unreachable glyph, its label marked down, and the reason it draws no numbers when it does not.
function HostRow({ host }: { host: HostEntry }): JSX.Element {
  if (!host.reachable) {
    return (
      <div class="host-row" data-state="unreachable">
        <StateGlyph state="unreachable" />
        <HostLabel name={host.name} down />
        <span class="none">no numbers while it does not answer</span>
      </div>
    );
  }
  return (
    <div class="host-row" data-state="reachable">
      <span class="slot" />
      <HostLabel name={host.name} />
      <span class="stats">
        <Meter stat={meterOf("load", host.loadPercent)} series={1} />
        <Meter stat={meterOf("memory", host.memoryPercent)} series={2} />
      </span>
      <span class="reach">reachable</span>
    </div>
  );
}

// meterOf is the meter a host's percent draws: its value is the percent rounded to a whole number, and its bar is filled to
// that number up to 100, since a load above 100 is real and its bar stops there. A host has no warn or crit level, so its
// meters take the ok level.
function meterOf(label: string, percent: number): MeterStat {
  const whole = Math.round(percent);
  return { label, value: `${whole}%`, percent: Math.min(whole, 100), level: "ok" };
}
