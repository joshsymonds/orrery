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

const DEG = Math.PI / 180;

// point is the point at radius r and angle a.
export function point(r: number, a: number): Pt {
  return { x: CENTRE + r * Math.sin(a), y: CENTRE - r * Math.cos(a) };
}

// printed is a point as an SVG path writes it, to 2 decimals.
function printed(p: Pt): string {
  return `${p.x.toFixed(2)} ${p.y.toFixed(2)}`;
}

// sliceSpan is the angle each of n slices takes: the sweep, 360 degrees less the start gap, shared out.
export function sliceSpan(n: number, gap = GAP): number {
  return ((360 - gap) * DEG) / n;
}

// sliceStart is a0(i), where slice i starts: the start gap's half, then i slices on.
export function sliceStart(n: number, i: number, gap = GAP): number {
  return (gap / 2) * DEG + i * sliceSpan(n, gap);
}

// sliceGap is g, the channel of constant width between neighbouring slices: 0.16 of the slice's arc at 100 u, between
// 0.7 and 3.
export function sliceGap(n: number, gap = GAP): number {
  return Math.min(3, Math.max(0.7, 0.16 * sliceSpan(n, gap) * 100));
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

// cellPath is the cell of section 4: ring `ring` (1 red test to 5 gate accepted) across slice i of n, with the start gap
// `gap`. It is an 11 u band inset by the slice gap, with its corners rounded by at most 2.
export function cellPath(n: number, i: number, ring: number, gap = GAP): string {
  const radius = RING_RADII[ring - 1];
  const span = sliceSpan(n, gap);
  const g = sliceGap(n, gap);
  const a0 = sliceStart(n, i, gap);
  return sector(radius - 5.5, radius + 5.5, a0, a0 + span, g, Math.min(2, 0.3 * (span * radius - g)));
}

