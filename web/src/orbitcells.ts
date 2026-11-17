import type { EpicSlice } from "./gen/orrery/v1/epics_pb";

// The five rings, outermost first.
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
