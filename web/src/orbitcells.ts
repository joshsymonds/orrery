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

// The words a cell's title ends with, by kind.
const titleWords: Record<CellKind, string> = {
  now: "working now",
  held: "held here",
  errored: "errored here",
  passed: "passed",
  track: "not reached",
};

export function cellTitle(slice: EpicSlice, ring: number): string {
  return `${slice.task} ${RINGS[ring - 1].name}: ${titleWords[cellKind(slice, ring)]}`;
}

export interface Tally {
  text: string;
  tone: "now" | "held" | "errored";
}

// A line of the centre readout and its baseline, as an offset from the centre.
export interface TallyLine {
  offset: number;
  tallies: Tally[];
}

export interface EpicReadout {
  done: number;
  total: number;
  caption: string;
  lines: TallyLine[];
}

// The epic's centre readout, from the slices alone: recordTasks only chooses the caption.
export function epicReadout(slices: EpicSlice[], recordTasks: number): EpicReadout {
  const done = slices.filter((s) => s.hue === "done").length;
  const held = slices.filter((s) => s.held).length;
  const running = slices.filter((s) => s.hue === "dispatched" && !s.held).length;
  const gap = slices.filter((s) => s.hue === "gap").length;
  const tallies: Tally[] = [];
  if (running) {
    tallies.push({ text: `${running} running`, tone: "now" });
  }
  if (held) {
    tallies.push({ text: `${held} held`, tone: "held" });
  }
  if (gap) {
    tallies.push({ text: `${gap} gap`, tone: "errored" });
  }
  return {
    done,
    total: slices.length,
    caption: recordTasks > slices.length ? "tracked tasks done" : "tasks done",
    lines: tallyLines(tallies),
  };
}

// Three tallies are two lines, at +31 and +46; one or two are one line at +38.
function tallyLines(tallies: Tally[]): TallyLine[] {
  if (tallies.length === 0) {
    return [];
  }
  if (tallies.length === 3) {
    return [
      { offset: 31, tallies: tallies.slice(0, 2) },
      { offset: 46, tallies: tallies.slice(2) },
    ];
  }
  return [{ offset: 38, tallies }];
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
