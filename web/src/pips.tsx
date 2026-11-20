import type { JSX } from "preact";

// PipTone is what a cell of a task's progress is: planned, running, held, waiting on a usage limit, failed, accepted,
// passed, or not reached.
export type PipTone = "none" | "plan" | "run" | "mine" | "limited" | "bad" | "acc" | "ok";

// rings are a task's five places, outside in, one per cell.
const rings = ["red", "implement", "green", "review", "integrate"];

// words is what a cell of each tone says in the pips' label.
const words: Record<PipTone, string> = {
  none: "not reached",
  plan: "planned",
  run: "running",
  mine: "held",
  limited: "waiting on a limit",
  bad: "failed",
  acc: "accepted",
  ok: "passed",
};

// Pips draws a task's cells as one mark each, in the order of the cells, and each mark carries its cell's tone. The
// label names each ring with its cell's words, so a screen reader reads the same progress the marks show.
export function Pips({ cells }: { cells: readonly PipTone[] }): JSX.Element {
  const label = cells.map((tone, index) => `${rings[index]} ${words[tone]}`).join(", ");
  return (
    <span class="pips" role="img" aria-label={label}>
      {cells.map((tone) => (
        <i data-tone={tone} />
      ))}
    </span>
  );
}
