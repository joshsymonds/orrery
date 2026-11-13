// The Orbits chart's geometry, as pure functions. Lengths are in viewBox units of a VIEW square centred on CENTRE.
// Angles are radians clockwise from 12 o'clock, except GAP, which is degrees. Spec: chart-spec.md sections 1, 3, 4, 8,
// 9 and 12.

export const VIEW = 412;
export const CENTRE = 206;
// The start gap at 12 o'clock, in degrees, which the slices do not fill.
export const GAP = 16;
// Ring centre radii, red test first.
export const RING_RADII = [168, 151, 134, 117, 100];

type Pt = { x: number; y: number };
type Tick = {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  major: boolean;
  label: { x: number; y: number; text: string } | null;
};

const DEG = Math.PI / 180;
const SWEEP = 360 - GAP;
const RING_LETTERS = ["R", "W", "T", "C", "G"];
const BEZEL_STEPS = [5, 10, 25, 50];
// hitSlice: a pointer closer to the centre than HOLE is the centre, and one farther than REACH is outside the chart.
const HOLE = 80.5;
const REACH = 200;

// point is the point at radius r and angle a.
export function point(r: number, a: number): Pt {
  return { x: CENTRE + r * Math.sin(a), y: CENTRE - r * Math.cos(a) };
}

// printed is a point as an SVG path writes it, to 2 decimals.
function printed(p: Pt): string {
  return `${p.x.toFixed(2)} ${p.y.toFixed(2)}`;
}

// sliceSpan is the angle each of n slices takes: 344 degrees shared out.
export function sliceSpan(n: number): number {
  return (SWEEP * DEG) / n;
}

// sliceStart is a0(i), where slice i starts: the start gap's half, then i slices on.
export function sliceStart(n: number, i: number): number {
  return (GAP / 2) * DEG + i * sliceSpan(n);
}

// sliceMid is the middle of slice i.
export function sliceMid(n: number, i: number): number {
  return sliceStart(n, i) + sliceSpan(n) / 2;
}

// sliceGap is g, the channel of constant width between neighbouring slices: 0.16 of the slice's arc at 100 u, between
// 0.7 and 3.
export function sliceGap(n: number): number {
  return Math.min(3, Math.max(0.7, 0.16 * sliceSpan(n) * 100));
}

// sector is the annulus sector from radius ri to ro, from angle a0 to a1, with each side pulled in by g / 2 along its
// arc and its four corners rounded by cr. A negative g pushes the sides out.
export function sector(ri: number, ro: number, a0: number, a1: number, g = 0, cr = 0): string {
  const o0 = a0 + g / 2 / ro;
  const o1 = a1 - g / 2 / ro;
  let i0 = a0 + g / 2 / ri;
  let i1 = a1 - g / 2 / ri;
  if (i1 <= i0) {
    const m = (a0 + a1) / 2;
    i0 = m - 0.0005;
    i1 = m + 0.0005;
  }
  const corner = Math.min(cr, (ro - ri) / 2, ((i1 - i0) * ri) / 2);
  const pt = (r: number, a: number) => printed(point(r, a));
  const big = (sweep: number) => (sweep > Math.PI ? 1 : 0);
  if (corner < 0.3) {
    return (
      `M${pt(ro, o0)}A${ro} ${ro} 0 ${big(o1 - o0)} 1 ${pt(ro, o1)}` +
      `L${pt(ri, i1)}A${ri} ${ri} 0 ${big(i1 - i0)} 0 ${pt(ri, i0)}Z`
    );
  }
  const co = corner / ro;
  const ci = corner / ri;
  const f = corner / (ro - ri);
  const s1o = o1 + (i1 - o1) * f;
  const s1i = i1 + (o1 - i1) * f;
  const s0o = o0 + (i0 - o0) * f;
  const s0i = i0 + (o0 - i0) * f;
  const outer = `M${pt(ro, o0 + co)}A${ro} ${ro} 0 ${big(o1 - o0 - 2 * co)} 1 ${pt(ro, o1 - co)}`;
  const rightSide = `Q${pt(ro, o1)} ${pt(ro - corner, s1o)}L${pt(ri + corner, s1i)}`;
  const inner = `Q${pt(ri, i1)} ${pt(ri, i1 - ci)}A${ri} ${ri} 0 ${big(i1 - i0 - 2 * ci)} 0 ${pt(ri, i0 + ci)}`;
  const leftSide = `Q${pt(ri, i0)} ${pt(ri + corner, s0i)}L${pt(ro - corner, s0o)}Q${pt(ro, o0)} ${pt(ro, o0 + co)}Z`;
  return outer + rightSide + inner + leftSide;
}

// cellPath is the cell of section 4: ring `ring` (1 red test to 5 gate accepted) across slice i of n. It is an 11 u
// band inset by the slice gap, with its corners rounded by at most 2.
export function cellPath(n: number, i: number, ring: number): string {
  const radius = RING_RADII[ring - 1];
  const span = sliceSpan(n);
  const g = sliceGap(n);
  const a0 = sliceStart(n, i);
  return sector(radius - 5.5, radius + 5.5, a0, a0 + span, g, Math.min(2, 0.3 * (span * radius - g)));
}

// wedgePath is the cursor's outline wedge of section 9: slice i, pushed 0.5 u out on each side into the channels.
export function wedgePath(n: number, i: number): string {
  return sector(91, 177, sliceStart(n, i), sliceStart(n, i + 1), -1, 3);
}

// handPath is the cursor's hand of section 9: a triangle pointing outward at slice i's middle, its tip at 89 and its
// base at 82.5, 3.4 u either side of the middle.
export function handPath(n: number, i: number): string {
  const a = sliceMid(n, i);
  const tip = point(89, a);
  const base = point(82.5, a);
  const dx = 3.4 * Math.cos(a);
  const dy = 3.4 * Math.sin(a);
  const left = printed({ x: base.x + dx, y: base.y + dy });
  const right = printed({ x: base.x - dx, y: base.y - dy });
  return `M${printed(tip)}L${left}L${right}Z`;
}

// bezel is section 8's rim: a tick at every fifth slice's middle, from 10 slices up. The step between labels is the
// first of 5, 10, 25 and 50 that leaves at most 8 labels. Above 400 slices no step qualifies, and the largest is used.
export function bezel(n: number): Tick[] {
  if (n < 10) return [];
  const step = BEZEL_STEPS.find((s) => n / s <= 8) ?? BEZEL_STEPS[BEZEL_STEPS.length - 1];
  const ticks: Tick[] = [];
  for (let k = 5; k <= n; k += 5) {
    const a = sliceMid(n, k - 1);
    const major = k % step === 0;
    const from = point(179, a);
    const to = point(major ? 184 : 182, a);
    const number = point(192.5, a);
    ticks.push({
      x1: from.x,
      y1: from.y,
      x2: to.x,
      y2: to.y,
      major,
      label: major ? { x: number.x, y: number.y, text: String(k) } : null,
    });
  }
  return ticks;
}

// ringLetters are section 7's letters in the start gap: one on each ring's radius at 12 o'clock, red test first.
export function ringLetters(): { letter: string; x: number; y: number }[] {
  return RING_RADII.map((radius, k) => ({ letter: RING_LETTERS[k], x: CENTRE, y: CENTRE - radius }));
}

// hitSlice is the slice under a pointer at (x, y), measured from the centre, found by angle alone (section 12). It is
// 'centre' inside the hole, null past the reach or in the start gap, and otherwise the slice's index.
export function hitSlice(x: number, y: number, n: number): number | "centre" | null {
  const r = Math.hypot(x, y);
  if (r < HOLE) return "centre";
  if (r > REACH) return null;
  let a = Math.atan2(x, -y);
  if (a < 0) a += 2 * Math.PI;
  const start = (GAP / 2) * DEG;
  if (a < start || a >= 2 * Math.PI - start) return null;
  return Math.min(n - 1, Math.floor((a - start) / sliceSpan(n)));
}
