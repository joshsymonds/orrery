import type { HostStat, HostStatusResponse } from "./gen/orrery/v1/orrery_pb";

// MetersButton is the list footer's meters button: the host, its first stat's label and value, and the m key cap. A
// click calls onToggle, as the m key does. A host with no stats reads only its name.
export function MetersButton({ status, onToggle }: { status: HostStatusResponse; onToggle: () => void }) {
  const first = status.stats[0];
  return (
    <button class="meters" onClick={onToggle}>
      <span class="meters-host">{status.host}</span>
      {first !== undefined && (
        <>
          <span class="meter-label">{first.label}</span>
          <span class="meter-value">{first.value}</span>
        </>
      )}
      <kbd>m</kbd>
    </button>
  );
}

// MetersPanel draws every stat of the host: its label and value, and for a stat with a percent a bar at that width,
// with its level as a class.
export function MetersPanel({ stats }: { stats: HostStat[] }) {
  return (
    <section class="meters-panel" aria-label="meters">
      {stats.map((stat, index) => (
        <div key={index} class="meter">
          <span class="meter-label">{stat.label}</span>
          <span class="meter-value">{stat.value}</span>
          {stat.percent !== undefined && (
            <div class={`bar level-${stat.level}`} style={{ width: `${stat.percent}%` }} />
          )}
        </div>
      ))}
    </section>
  );
}
