import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicSliceSchema, type EpicSlice } from "./gen/orrery/v1/epics_pb";
import { cellKind, type CellKind } from "./orbitcells";

type SliceInit = MessageInitShape<typeof EpicSliceSchema>;

// A slice as serve sends it. What the init leaves out is false or zero.
function slice(task: string, init: SliceInit): EpicSlice {
  return create(EpicSliceSchema, { task, ...init });
}

// Section 2's table. Each row is a task's latest writing step and the cell kinds the spec gives for R W T C G:
// Y now, G passed, P held, R errored, · track.
const glyphs: Record<string, CellKind> = { Y: "now", G: "passed", P: "held", R: "errored", "·": "track" };
const rows: [name: string, init: SliceInit, cells: string][] = [
  ["running, just started", { hue: "dispatched", working: true, at: 1 }, "Y G · · ·"],
  ["running, red evidence given", { hue: "dispatched", redTest: true, working: true, at: 3 }, "G G Y · ·"],
  [
    "running, tests pass",
    { hue: "dispatched", redTest: true, working: true, testsPass: true, at: 4 },
    "G G G Y ·",
  ],
  [
    "running, checks passed",
    { hue: "dispatched", redTest: true, working: true, testsPass: true, checks: true, at: 5 },
    "G G G G Y",
  ],
  ["running, green evidence but no red", { hue: "dispatched", working: true, testsPass: true, at: 1 }, "Y G G · ·"],
  [
    "held after tests pass",
    { hue: "dispatched", redTest: true, working: true, testsPass: true, at: 4, held: true },
    "G G G P ·",
  ],
  ["held, just started", { hue: "dispatched", working: true, at: 1, held: true }, "P G · · ·"],
  ["running, nothing reached", { hue: "dispatched", at: 1 }, "Y · · · ·"],
  [
    "ended done",
    { hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true },
    "G G G G G",
  ],
  [
    "ended done, checks not evidenced",
    { hue: "done", redTest: true, working: true, testsPass: true, gateAccepted: true },
    "G G G · G",
  ],
  [
    "ended checks_failed after tests pass",
    { hue: "gap", redTest: true, working: true, testsPass: true, errorAt: 4 },
    "G G G R ·",
  ],
  ["ended checks_failed, no green evidence", { hue: "gap", redTest: true, working: true, errorAt: 4 }, "G G · R ·"],
  [
    "ended goal_unmet",
    { hue: "gap", redTest: true, working: true, testsPass: true, checks: true, errorAt: 5 },
    "G G G G R",
  ],
  ["ended returned", { hue: "gap", redTest: true, working: true, errorAt: 3 }, "G G R · ·"],
  ["ended failed, no red test", { hue: "gap", working: true, errorAt: 1 }, "R G · · ·"],
  ["ended, nothing reached", { hue: "gap", errorAt: 1 }, "R · · · ·"],
];

for (const [name, init, cells] of rows) {
  test(`section 2: ${name} colours R W T C G as ${cells}`, () => {
    const kinds = [1, 2, 3, 4, 5].map((ring) => cellKind(slice("t01", init), ring));
    assert.deepEqual(
      kinds,
      cells.split(" ").map((glyph) => glyphs[glyph]),
    );
  });
}

test("at wins over errorAt on the same ring", () => {
  assert.equal(cellKind(slice("t01", { hue: "dispatched", at: 3, errorAt: 3 }), 3), "now");
});

test("a held at wins over errorAt on the same ring", () => {
  assert.equal(cellKind(slice("t01", { hue: "dispatched", at: 3, errorAt: 3, held: true }), 3), "held");
});

test("at wins over a true boolean on the same ring", () => {
  assert.equal(cellKind(slice("t01", { hue: "dispatched", at: 3, testsPass: true }), 3), "now");
});

test("a held at wins over a true boolean on the same ring", () => {
  assert.equal(cellKind(slice("t01", { hue: "dispatched", at: 3, testsPass: true, held: true }), 3), "held");
});

test("errorAt wins over a true boolean on the same ring", () => {
  assert.equal(cellKind(slice("t01", { hue: "gap", checks: true, errorAt: 4 }), 4), "errored");
});
