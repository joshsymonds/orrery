import assert from "node:assert/strict";
import { test } from "node:test";

import {
  bezel,
  cellPath,
  CENTRE,
  GAP,
  handPath,
  hitSlice,
  point,
  ringLetters,
  RING_RADII,
  sector,
  sliceGap,
  sliceMid,
  sliceSpan,
  sliceStart,
  VIEW,
  wedgePath,
} from "./orbits";

const DEG = Math.PI / 180;
const COUNTS = [1, 35, 150];
const RADII = [168, 151, 134, 117, 100];

interface Pt {
  x: number;
  y: number;
}

function close(actual: number, expected: number, tol = 1e-9): void {
  assert.ok(Math.abs(actual - expected) <= tol, `${actual} is not ${expected} within ${tol}`);
}

// startDeg and midDeg state the spec's angles in degrees for N slices: slice 0 starts at 8 and each takes 344 / N.
function startDeg(n: number, i: number): number {
  return 8 + (i * 344) / n;
}

function midDeg(n: number, i: number): number {
  return startDeg(n, i) + 344 / n / 2;
}

// rel is the point at radius r and angle deg clockwise from 12 o'clock, relative to the centre.
function rel(deg: number, r: number): Pt {
  return { x: r * Math.sin(deg * DEG), y: -r * Math.cos(deg * DEG) };
}

// radiusOf and angleDeg read a point on the chart: its distance from the centre, and its clockwise angle from 12
// o'clock.
function radiusOf(p: Pt): number {
  return Math.hypot(p.x - CENTRE, p.y - CENTRE);
}

function angleDeg(p: Pt): number {
  return ((Math.atan2(p.x - CENTRE, -(p.y - CENTRE)) / DEG) + 360) % 360;
}

// commands splits a path into its letters and the numbers after each.
function commands(d: string): { op: string; nums: number[] }[] {
  return (d.match(/[MALQZ][^MALQZ]*/g) ?? []).map((c) => ({
    op: c[0],
    nums: (c.slice(1).match(/-?[\d.]+/g) ?? []).map(Number),
  }));
}

// endsOf is the point a drawing command ends at.
function endsOf(c: { op: string; nums: number[] }): Pt {
  const n = c.nums;
  if (c.op === "A") return { x: n[5], y: n[6] };
  if (c.op === "Q") return { x: n[2], y: n[3] };
  return { x: n[0], y: n[1] };
}

// endPoints is every point a command of the path ends at.
function endPoints(d: string): Pt[] {
  return commands(d)
    .filter((c) => c.op !== "Z")
    .map(endsOf);
}

// arcPoints is every point an arc of the path starts or ends at.
function arcPoints(d: string): Pt[] {
  const pts: Pt[] = [];
  let here: Pt = { x: NaN, y: NaN };
  let start: Pt = here;
  for (const c of commands(d)) {
    if (c.op === "Z") {
      here = start;
      continue;
    }
    const end = endsOf(c);
    if (c.op === "M") start = end;
    if (c.op === "A") pts.push(here, end);
    here = end;
  }
  return pts;
}

test("the frozen constants are a 412 view centred at 206, a 16 degree start gap and five ring radii", () => {
  assert.equal(VIEW, 412);
  assert.equal(CENTRE, VIEW / 2);
  assert.equal(GAP, 16);
  assert.deepEqual(RING_RADII, RADII);
});

test("point puts radius r at angle a clockwise from 12 o'clock", () => {
  const top = point(168, 0);
  close(top.x, 206);
  close(top.y, 38);
  const right = point(100, Math.PI / 2);
  close(right.x, 306);
  close(right.y, 206);
  const bottom = point(100, Math.PI);
  close(bottom.x, 206);
  close(bottom.y, 306);
});

test("slice 0 starts at 8 degrees and each slice takes 344 over N degrees, for N 1, 35 and 150", () => {
  for (const n of COUNTS) {
    close(sliceStart(n, 0) / DEG, 8);
    close(sliceSpan(n) / DEG, 344 / n);
    close(sliceStart(n, 1) / DEG, 8 + 344 / n);
    close(sliceMid(n, 0) / DEG, 8 + 172 / n);
    close(sliceStart(n, n) / DEG, 352);
  }
});

test("the slice gap is 3.00 up to 12 slices, then narrows: 2.74 at 35, 1.20 at 80, 0.70 at 150", () => {
  assert.deepEqual([1, 2, 5, 12].map((n) => sliceGap(n).toFixed(2)), ["3.00", "3.00", "3.00", "3.00"]);
  assert.equal(sliceGap(35).toFixed(2), "2.74");
  assert.equal(sliceGap(80).toFixed(2), "1.20");
  assert.equal(sliceGap(150).toFixed(2), "0.70");
});

test("sector draws a plain annulus sector as move, arc, line, arc, close", () => {
  const plain = commands(sector(100, 110, 0, 1));
  assert.equal(plain.map((c) => c.op).join(""), "MALAZ");
});

test("sector rounds its four corners when the corner radius is at least 0.3", () => {
  const rounded = commands(sector(100, 110, 0, 1, 0, 2));
  assert.equal(rounded.map((c) => c.op).join(""), "MAQLQAQLQZ");
});

test("a cell's arc ends lie on its ring's inner and outer radius, inside its slice, and none at its middle", () => {
  for (const n of COUNTS) {
    for (const i of [0, Math.floor(n / 2), n - 1]) {
      for (let ring = 1; ring <= 5; ring++) {
        const R = RADII[ring - 1];
        const pts = arcPoints(cellPath(n, i, ring));
        assert.equal(pts.length, 4, `N ${n} slice ${i} ring ${ring} has two arcs`);
        for (const p of pts) {
          const r = radiusOf(p);
          const onRadius = Math.min(Math.abs(r - (R - 5.5)), Math.abs(r - (R + 5.5)));
          assert.ok(onRadius < 0.01, `N ${n} ring ${ring} radius ${r}`);
          const a = angleDeg(p);
          assert.ok(a > startDeg(n, i) && a < startDeg(n, i) + 344 / n, `N ${n} slice ${i} angle ${a}`);
          assert.ok(Math.abs(a - midDeg(n, i)) > 0.01, `N ${n} slice ${i} ring ${ring} angle ${a} is the middle`);
        }
      }
    }
  }
});

test("a cell's corners are rounded by 2 at N 1, and by 0.99 at N 150 on the outer ring", () => {
  // N 1, ring 1 (R 168): corner = min(2, ...) = 2, so the sides turn at 171.5 and 164.5.
  const one = endPoints(cellPath(1, 0, 1)).map(radiusOf);
  assert.ok(one.some((r) => Math.abs(r - 171.5) < 0.01));
  assert.ok(one.some((r) => Math.abs(r - 164.5) < 0.01));
  // N 150, ring 5 (R 100): corner = 0.3 * (span * 100 - 0.7) = 0.9908, so the outer side turns at 105.5 - 0.9908.
  const many = endPoints(cellPath(150, 0, 5)).map(radiusOf);
  assert.ok(many.some((r) => Math.abs(r - 104.51) < 0.02));
});

test("the cursor's wedge runs out to 177 and in to 91 over its own slice, overhanging it by at most 0.5 u a side", () => {
  const n = 35;
  const i = 2;
  const d = wedgePath(n, i);
  for (const p of arcPoints(d)) {
    const r = radiusOf(p);
    assert.ok(Math.abs(r - 177) < 0.01 || Math.abs(r - 91) < 0.01, `wedge arc at radius ${r}`);
  }
  // The wedge overhangs its slice by 0.5 u on each side, which is at most 0.5 / 91 rad at its inner radius. The slack
  // allows for the 2-decimal printing of each point.
  const overhang = 0.5 / 91 / DEG + 0.05;
  const angles = endPoints(d).map(angleDeg);
  for (const a of angles) {
    assert.ok(a >= startDeg(n, i) - overhang && a <= startDeg(n, i) + 344 / n + overhang, `wedge point at ${a}`);
  }
  assert.ok(Math.min(...angles) < startDeg(n, i));
  assert.ok(Math.max(...angles) > startDeg(n, i) + 344 / n);
});

test("the cursor's hand has its tip at 89 and its base at 82.5, straight down for slice 0 of one", () => {
  // N 1, slice 0: its middle is 180 degrees, so the tip is at (206, 295) and the base at y 288.5, 3.4 either side.
  const pts = endPoints(handPath(1, 0));
  assert.equal(pts.length, 3);
  close(pts[0].x, 206, 0.01);
  close(pts[0].y, 295, 0.01);
  assert.ok(pts.some((p) => Math.abs(p.x - 202.6) < 0.01 && Math.abs(p.y - 288.5) < 0.01));
  assert.ok(pts.some((p) => Math.abs(p.x - 209.4) < 0.01 && Math.abs(p.y - 288.5) < 0.01));
});

test("the cursor's hand points outward at its slice's middle for N 35 slice 17", () => {
  const pts = endPoints(handPath(35, 17));
  const [tip, b1, b2] = pts;
  close(radiusOf(tip), 89, 0.01);
  close(radiusOf({ x: (b1.x + b2.x) / 2, y: (b1.y + b2.y) / 2 }), 82.5, 0.01);
  close(angleDeg(tip), midDeg(35, 17), 0.01);
});

test("the ring letters are R W T C G down the start gap, each on its ring's radius, and sit in no slice", () => {
  const letters = ringLetters();
  assert.equal(letters.map((l) => l.letter).join(""), "RWTCG");
  assert.deepEqual(
    letters.map((l) => l.y),
    [38, 55, 72, 89, 106],
  );
  for (const l of letters) {
    assert.equal(l.x, 206);
    assert.equal(hitSlice(l.x - CENTRE, l.y - CENTRE, 35), null);
  }
});

test("the bezel is empty below 10 slices", () => {
  assert.deepEqual(bezel(9), []);
});

// labelsOf is the text of the bezel's labels, in order.
function labelsOf(n: number): string[] {
  return bezel(n).flatMap((t) => (t.label === null ? [] : [t.label.text]));
}

test("the bezel labels every 5th slice at 12 and 35, every 10th at 80 and every 25th at 150", () => {
  assert.equal(labelsOf(12).join(","), "5,10");
  assert.equal(labelsOf(35).join(","), "5,10,15,20,25,30,35");
  assert.equal(labelsOf(80).join(","), "10,20,30,40,50,60,70,80");
  assert.equal(labelsOf(150).join(","), "25,50,75,100,125,150");
});

test("the bezel's plain tick runs 179 to 182; its labelled tick runs to 184, with its number at 192.5", () => {
  const ticks = bezel(80);
  assert.equal(ticks.length, 16);

  const five = ticks[0];
  assert.equal(five.major, false);
  assert.equal(five.label, null);
  close(radiusOf({ x: five.x1, y: five.y1 }), 179);
  close(radiusOf({ x: five.x2, y: five.y2 }), 182);
  close(angleDeg({ x: five.x1, y: five.y1 }), midDeg(80, 4), 0.01);

  const ten = ticks[1];
  assert.equal(ten.major, true);
  close(radiusOf({ x: ten.x1, y: ten.y1 }), 179);
  close(radiusOf({ x: ten.x2, y: ten.y2 }), 184);
  const label = ten.label;
  assert.ok(label !== null);
  assert.equal(label.text, "10");
  close(radiusOf(label), 192.5);
  close(angleDeg(label), midDeg(80, 9), 0.01);
});

test("hitSlice reads the centre inside 80.5, nothing past 200 or in the start gap, else the slice's index", () => {
  // The centre, up to 80.5; from 80.5 out, the slice under the pointer.
  assert.equal(hitSlice(0, 0, 35), "centre");
  assert.equal(hitSlice(rel(90, 80.4).x, rel(90, 80.4).y, 35), "centre");
  assert.equal(hitSlice(rel(90, 80.6).x, rel(90, 80.6).y, 35), 8);
  // Outside: past radius 200.
  assert.equal(hitSlice(rel(90, 200.5).x, rel(90, 200.5).y, 1), null);
  // The start gap: within 8 degrees of 12 o'clock on either side.
  for (const deg of [0, 7.9, 352.1, 355]) {
    assert.equal(hitSlice(rel(deg, 100).x, rel(deg, 100).y, 35), null, `gap at ${deg}`);
  }
  // N 1: the whole ring is slice 0.
  for (const deg of [8.1, 90, 180, 351.9]) {
    assert.equal(hitSlice(rel(deg, 100).x, rel(deg, 100).y, 1), 0, `N 1 at ${deg}`);
  }
  // N 35: the first, second and last slices, and the middle of slice 17.
  assert.equal(hitSlice(rel(12, 100).x, rel(12, 100).y, 35), 0);
  assert.equal(hitSlice(rel(22.74, 100).x, rel(22.74, 100).y, 35), 1);
  assert.equal(hitSlice(rel(347.1, 100).x, rel(347.1, 100).y, 35), 34);
  assert.equal(hitSlice(rel(351.9, 100).x, rel(351.9, 100).y, 35), 34);
  assert.equal(hitSlice(rel(180, 200).x, rel(180, 200).y, 35), 17);
  // N 150: the first slice starts at 8 and the last ends at 352, and a point at 350.9 is slice 149.
  assert.equal(hitSlice(rel(8.5, 100).x, rel(8.5, 100).y, 150), 0);
  assert.equal(hitSlice(rel(350.9, 100).x, rel(350.9, 100).y, 150), 149);
});
