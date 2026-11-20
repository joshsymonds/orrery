import type { JSX } from "preact";

// CompactBadge is one badge of the folded line: a steered epic, the agents, or the running shells. A badge that asks
// draws a question mark; any other draws its count when the count is above zero.
export interface CompactBadge {
  key: string;
  label: string;
  count: number;
  asks: boolean;
}

// CompactHead is the phone header folded to one line: the asking words first, peach and named whenever something asks,
// then the badges in the order given, then the chevron. A tap on the line opens the header.
export function CompactHead({
  asking,
  badges,
  onOpen,
}: {
  asking: string;
  badges: readonly CompactBadge[];
  onOpen: () => void;
}): JSX.Element {
  return (
    <button
      type="button"
      class={asking !== "" ? "compact attention" : "compact"}
      aria-label="Show the header"
      onClick={() => onOpen()}
    >
      {asking !== "" && <span class="cask">{asking}</span>}
      {badges.map((badge) => (
        <span key={badge.key} class={badge.asks ? "cb attention" : "cb"} data-badge={badge.key}>
          {badge.label}
          {badge.asks ? "?" : badge.count > 0 ? ` ${badge.count}` : ""}
        </span>
      ))}
      <span class="end">⌄</span>
    </button>
  );
}
