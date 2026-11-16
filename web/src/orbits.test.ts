import assert from "node:assert/strict";
import { test } from "node:test";

import { cellPath, CENTRE, GAP, point, RING_RADII, sector, sliceGap, sliceSpan, sliceStart, VIEW } from "./orbits";

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
