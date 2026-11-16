import type { EpicSlice } from "./gen/orrery/v1/epics_pb";

// The five rings, outermost first: the letter on the chart and the name in titles.
export const RINGS: readonly { letter: string; name: string }[] = [
  { letter: "R", name: "red test" },
  { letter: "W", name: "working" },
  { letter: "T", name: "tests pass" },
  { letter: "C", name: "checks" },
  { letter: "G", name: "gate accepted" },
];

// now is the ring a running task is working toward, held the same ring on a held task, errored the ring that failed,
// passed a ring with evidence, and track a ring not reached.
export type CellKind = "now" | "held" | "errored" | "passed" | "track";

// The slice field that holds each ring's evidence, in ring order.
const evidence = ["redTest", "working", "testsPass", "checks", "gateAccepted"] as const;

// The kind of ring's cell, in chart-spec section 4's order: at, then error_at, then the ring's boolean.
export function cellKind(slice: EpicSlice, ring: number): CellKind {
  if (slice.at === ring) {
    return slice.held ? "held" : "now";
  }
  if (slice.errorAt === ring) {
    return "errored";
  }
  return slice[evidence[ring - 1]] ? "passed" : "track";
}

export type StatusTone = "passed" | "now" | "held" | "errored";

export interface TaskReadout {
  ordinal: string;
  label: string;
  size: number;
  status: string;
  tone: StatusTone;
  letters: { letter: string; kind: CellKind }[];
}

// A label over 26 characters is cut to 25 and an ellipsis.
const labelLimit = 26;
const labelKept = 25;

// The readout for the task at index. Its size is for the label as drawn.
export function taskReadout(slices: EpicSlice[], index: number): TaskReadout {
  const slice = slices[index];
  const label = slice.task.length > labelLimit ? `${slice.task.slice(0, labelKept)}…` : slice.task;
  const [status, tone] = statusOf(slice);
  return {
    ordinal: `${index + 1} of ${slices.length}`,
    label,
    size: clamp(146 / (0.6 * label.length), 9, 22),
    status,
    tone,
    letters: RINGS.map((ring, i) => ({ letter: ring.letter, kind: cellKind(slice, i + 1) })),
  };
}

// The status line and its tone: the ring named is the one a running task works toward, where a held one waits, or
// where an ended one errored.
function statusOf(slice: EpicSlice): [string, StatusTone] {
  if (slice.hue === "done") {
    return ["done", "passed"];
  }
  if (slice.held) {
    return [`held · ${RINGS[slice.at - 1].name}`, "held"];
  }
  if (slice.hue === "dispatched") {
    return [`running · ${RINGS[slice.at - 1].name}`, "now"];
  }
  return [`gap · ${RINGS[slice.errorAt - 1].name}`, "errored"];
}

function clamp(value: number, low: number, high: number): number {
  return Math.min(high, Math.max(low, value));
}
