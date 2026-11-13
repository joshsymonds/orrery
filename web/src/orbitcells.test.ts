import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { EpicSliceSchema, type EpicSlice } from "./gen/orrery/v1/epics_pb";
import { cellKind, cellTitle, epicReadout, taskReadout, type CellKind } from "./orbitcells";

type SliceInit = MessageInitShape<typeof EpicSliceSchema>;

// A slice as serve sends it. What the init leaves out is false or zero.
function slice(task: string, init: SliceInit): EpicSlice {
  return create(EpicSliceSchema, { task, ...init });
}

// A task named by the rings it has reached: the letter of each reached ring, "_" for each one it has not. at is the
// first ring not reached while the task runs or is held, and error_at is that same ring for a gap, which is where
// every end in these fixtures errors.
function task(name: string, rings: string, hue: string, held = false): EpicSlice {
  const first = rings.indexOf("_") + 1;
  return slice(name, {
    hue,
    redTest: rings[0] !== "_",
    working: rings[1] !== "_",
    testsPass: rings[2] !== "_",
    checks: rings[3] !== "_",
    gateAccepted: rings[4] !== "_",
    at: hue === "dispatched" ? first : 0,
    held,
    errorAt: hue === "gap" ? first : 0,
  });
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

// Section 15's words, one for each kind of cell, on the ring that has it.
const titles: [ring: number, init: SliceInit, title: string][] = [
  [1, { redTest: true }, "t07 red test: passed"],
  [2, { hue: "dispatched", at: 2 }, "t07 working: working now"],
  [3, { hue: "dispatched", at: 3, held: true }, "t07 tests pass: held here"],
  [4, { hue: "gap", errorAt: 4 }, "t07 checks: errored here"],
  [5, {}, "t07 gate accepted: not reached"],
];

for (const [ring, init, title] of titles) {
  test(`cellTitle of ring ${ring} reads "${title}"`, () => {
    assert.equal(cellTitle(slice("t07", init), ring), title);
  });
}

// Section 17's synthetic 35: 11 done, 20 gap, 3 running and 1 held. Each entry is a count, the rings the tasks reached,
// their hue and whether they are held.
function synthetic35(): EpicSlice[] {
  const mix: [count: number, rings: string, hue: string, held?: boolean][] = [
    [5, "RW___", "gap"],
    [4, "RWT__", "gap"],
    [2, "_W___", "gap"],
    [9, "RWTCG", "done"],
    [2, "RWT_G", "done"],
    [4, "RWT__", "gap"],
    [3, "RWTC_", "gap"],
    [2, "_W___", "gap"],
    [1, "_W___", "dispatched"],
    [2, "RWT__", "dispatched"],
    [1, "RWT__", "dispatched", true],
  ];
  const tasks: EpicSlice[] = [];
  for (const [count, rings, hue, held = false] of mix) {
    for (let i = 0; i < count; i++) {
      tasks.push(task(`t${String(tasks.length + 1).padStart(2, "0")}`, rings, hue, held));
    }
  }
  return tasks;
}

test("the synthetic 35 has 35 slices", () => {
  assert.equal(synthetic35().length, 35);
});

test("the synthetic 35 reads 11 of 35 done", () => {
  const readout = epicReadout(synthetic35(), 35);
  assert.equal(readout.done, 11);
  assert.equal(readout.total, 35);
});

test("the synthetic 35 is captioned tasks done when the record lists 35 tasks", () => {
  assert.equal(epicReadout(synthetic35(), 35).caption, "tasks done");
});

test("the synthetic 35 has 3 running and 1 held at +31, and 20 gap at +46", () => {
  assert.deepEqual(epicReadout(synthetic35(), 35).lines, [
    {
      offset: 31,
      tallies: [
        { text: "3 running", tone: "now" },
        { text: "1 held", tone: "held" },
      ],
    },
    { offset: 46, tallies: [{ text: "20 gap", tone: "errored" }] },
  ]);
});

test("the caption is tracked tasks done when the record lists more tasks than there are slices", () => {
  assert.equal(epicReadout(synthetic35(), 36).caption, "tracked tasks done");
});

test("one tally is one line at +38", () => {
  const slices = [task("t01", "RWTCG", "done"), task("t02", "_W___", "dispatched")];
  assert.deepEqual(epicReadout(slices, 2).lines, [{ offset: 38, tallies: [{ text: "1 running", tone: "now" }] }]);
});

test("two tallies share one line at +38", () => {
  const slices = [task("t01", "_W___", "dispatched"), task("t02", "RW___", "gap")];
  assert.deepEqual(epicReadout(slices, 2).lines, [
    {
      offset: 38,
      tallies: [
        { text: "1 running", tone: "now" },
        { text: "1 gap", tone: "errored" },
      ],
    },
  ]);
});

test("no tallies, so no lines", () => {
  assert.deepEqual(epicReadout([task("t01", "RWTCG", "done")], 1).lines, []);
});

test("a running task is counted running and not held", () => {
  const slices = [task("t01", "RWT__", "dispatched", true), task("t02", "_W___", "dispatched")];
  assert.deepEqual(epicReadout(slices, 2).lines, [
    {
      offset: 38,
      tallies: [
        { text: "1 running", tone: "now" },
        { text: "1 held", tone: "held" },
      ],
    },
  ]);
});

test("the task readout counts from one", () => {
  const slices = synthetic35();
  assert.equal(taskReadout(slices, 0).ordinal, "1 of 35");
  assert.equal(taskReadout(slices, 34).ordinal, "35 of 35");
});

test("a label of 26 characters is drawn whole", () => {
  const name = "abcdefghijklmnopqrstuvwxyz";
  assert.equal(taskReadout([task(name, "RWTCG", "done")], 0).label, name);
});

test("a label of 27 characters is cut to 25 and an ellipsis", () => {
  const name = "abcdefghijklmnopqrstuvwxyz0";
  assert.equal(taskReadout([task(name, "RWTCG", "done")], 0).label, "abcdefghijklmnopqrstuvwxy…");
});

// The size is 146 / (0.6 × the length of the label as drawn), clamped to 9 to 22.
const sizes: [length: number, fixed: string][] = [
  [1, "22.00"],
  [12, "20.28"],
  [26, "9.36"],
];

for (const [length, fixed] of sizes) {
  test(`a label of ${length} characters is drawn at size ${fixed}`, () => {
    const label = "a".repeat(length);
    assert.equal(taskReadout([task(label, "RWTCG", "done")], 0).size.toFixed(2), fixed);
  });
}

test("a label over 26 characters is sized by its cut length", () => {
  const label = "a".repeat(27);
  assert.equal(taskReadout([task(label, "RWTCG", "done")], 0).size.toFixed(2), "9.36");
});

test("a done task reads done and passed", () => {
  const readout = taskReadout([task("t01", "RWTCG", "done")], 0);
  assert.equal(readout.status, "done");
  assert.equal(readout.tone, "passed");
});

test("a running task reads running with the ring it works toward", () => {
  const readout = taskReadout([task("t01", "RW___", "dispatched")], 0);
  assert.equal(readout.status, "running · tests pass");
  assert.equal(readout.tone, "now");
});

test("a held task reads held with the ring it waits at", () => {
  const readout = taskReadout([task("t01", "RWT__", "dispatched", true)], 0);
  assert.equal(readout.status, "held · checks");
  assert.equal(readout.tone, "held");
});

test("a gap reads gap with the ring it errored at", () => {
  const readout = taskReadout([task("t01", "RWTC_", "gap")], 0);
  assert.equal(readout.status, "gap · gate accepted");
  assert.equal(readout.tone, "errored");
});

test("the letters carry each ring's cell kind", () => {
  const readout = taskReadout([task("t01", "RW___", "dispatched")], 0);
  assert.deepEqual(readout.letters, [
    { letter: "R", kind: "passed" },
    { letter: "W", kind: "passed" },
    { letter: "T", kind: "now" },
    { letter: "C", kind: "track" },
    { letter: "G", kind: "track" },
  ]);
});
