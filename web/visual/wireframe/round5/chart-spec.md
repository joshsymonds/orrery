# Epic chart: Orbits

Implementation spec for the chart the owner chose in `chart-options.html` (direction 1, Orbits, with the owner's three
changes). The reference drawing is that page's `STYLES.orbits`, `cellOf()`, `sector()` and `chart()`; where this text and
that code disagree, the code is what the owner saw.

Everything here is decided by the owner; nothing is a proposal. This is the spec W3a's next effort implements.

Targets: `web/src/epicview.tsx` and `web/src/style.css` (the `SliceChart` component), `proto/orrery/v1/epics.proto` and
serve (`at` changes meaning and two fields are new on `EpicSlice`, section 2), and the phone's `EpicScreen.kt`. At orrery 5df809d the phone's
`EpicDetail` carries no slices; `effort/orrery-epic-chart-phone-1` (W3b) adds them, and the phone's chart builds on that
branch.

Orbits is view B's chart (the wireframe's `#b`) turned to the rules: view B draws a ring per task and five arcs per
ring; here a slice is a task and a ring is a phase. It keeps view B's weight (a stroke two thirds of the ring pitch),
its full shape (an unreached cell is drawn at full weight), its colour by phase, its pulsing running phase and its
count in the centre.

## What the owner decided

- One slice per task, in slice order, clockwise. Five rings, outermost first: red test, working, tests pass, checks,
  gate accepted.
- **No dot and no halo.**
- **Colour by phase, per cell** (section 4). If a phase succeeded it is green, on a running task as on a finished one.
  Yellow is the phase a running task is working toward: the first ring it has not yet reached, counting from red test
  inward (DL-O9). Peach is that same ring on a held task. Red is the phase that errored. The track tone is a phase not reached.
- **The yellow cell glows**, on the cell and not on the whole slice (section 6).
- The cursor's task is outlined, with a wash under it and a hand pointing at it (section 9).
- The gap at 12 o'clock with the ring letters in it (sections 3, 7); the numbers and ticks round the rim (section 8);
  the ring key and the colour key under the chart (section 7); 404 px wide in the web column (section 15).
- An epic with no slices shows "No orrery-tracked steps" (section 13).

## 1. Box and units

Square, `viewBox="0 0 412 412"`, centre `c = (206, 206)`. Every length below is in viewBox units (`u`) and every radius is
measured from `c`. Drawn 404 px wide, 1 u is about 1 px; on the phone `u = canvasWidthPx / 412`. Nothing uses
`vector-effect: non-scaling-stroke`; strokes scale with the chart.

Angles are clockwise from 12 o'clock. `point(r, a) = (c.x + r·sin a, c.y − r·cos a)`.

The reference page uses `viewBox="-6 -6 412 412"` with the centre at (200, 200). It is the same drawing shifted by 6.

## 2. What serve changes on EpicSlice

Today a slice carries `task`, `hue`, the five booleans and `at`. One field changes meaning and two are new. All three
are facts about the task's latest writing step.

```proto
// The ring the task is working toward, 1 for red_test through 5 for gate_accepted, while the latest writing step runs
// or is held: the first ring not yet reached, counting from red_test inward, which is the first of red_test, working,
// tests_pass, checks, gate_accepted whose field is false. 0 once the step ended.
int32 at = 8;

// Whether the latest writing step is held by the owner. False once it has ended.
bool held = 9;

// The ring that errored, 1 for red_test through 5 for gate_accepted, when the latest writing step ended any way but
// done; 0 while it runs or is held, and when it ended done. It is 4 (checks) when the step ended checks_failed, 5
// (gate_accepted) when it ended goal_unmet, and for every other end the first ring not reached, counted as at counts
// it.
int32 error_at = 10;
```

- **`at` changes meaning (DL-O9).** At 5df809d it names the innermost reached ring (W3a's DL-O2). It now names the
  first ring not yet reached, counting from red test inward, so the cell it points at is never a reached cell and
  every reached cell is green. A running step always has such a ring: `gate_accepted` is true only once the step ended
  done. Serve's test for the running slice's `at` changes with it.
- **`held`** separates peach from yellow. `EpicStep.held` already has it per step; the slice needs it for its own step.
- **`error_at`** says which cell is red. Serve applies the rule once, so the web and the phone cannot disagree.
- `hue` stays. It no longer colours any cell. It is the task's state for the centre readout and its counts:
  `dispatched`, `done`, `gap`.

**How the three sit together.** `at` is non-zero only while the step runs or is held; `error_at` is non-zero only once
it ended short. Serve never sends both. Each names a ring whose boolean is false: `at` and the "every other end" case
by construction, checks_failed and goal_unmet because those ends mean that ring's evidence is missing. A client does
not rely on that. It colours a cell by the first rule that applies, in section 4's order: `at`, then `error_at`, then
the boolean.

Because the count starts at red test, the yellow or red cell can sit **before** a green one: a step with no red
evidence is yellow on red test while working, behind it, is already green.

**Examples.** `G` green, `Y` yellow and glowing, `P` peach, `R` red, `·` track.

| Latest writing step | True booleans | `at` | `held` | `error_at` | R W T C G |
|---|---|---|---|---|---|
| running, just started | working | 1 | | 0 | `Y G · · ·` |
| running, red evidence given | red test, working | 3 | | 0 | `G G Y · ·` |
| running, tests pass | red test, working, tests pass | 4 | | 0 | `G G G Y ·` |
| running, checks passed | red test … checks | 5 | | 0 | `G G G G Y` |
| running, green evidence but no red | working, tests pass | 1 | | 0 | `Y G G · ·` |
| held after tests pass | red test, working, tests pass | 4 | yes | 0 | `G G G P ·` |
| held, just started | working | 1 | yes | 0 | `P G · · ·` |
| running, nothing reached | none | 1 | | 0 | `Y · · · ·` |
| ended done | all five | 0 | | 0 | `G G G G G` |
| ended done, checks not evidenced | all but checks | 0 | | 0 | `G G G · G` |
| ended checks_failed after tests pass | red test, working, tests pass | 0 | | 4 | `G G G R ·` |
| ended checks_failed, no green evidence | red test, working | 0 | | 4 | `G G · R ·` |
| ended goal_unmet | red test … checks | 0 | | 5 | `G G G G R` |
| ended returned (or failed, stopped, …) | red test, working | 0 | | 3 | `G G R · ·` |
| ended failed, no red test | working | 0 | | 1 | `R G · · ·` |
| ended, nothing reached | none | 0 | | 1 | `R · · · ·` |

The first row is what a task looks like the moment it starts: `working` is true as soon as the step has run, so it is
green at once, and the task glows yellow on red test until red evidence arrives.

## 3. Slices

- Start gap: `GAP = 16°`, centred on 12 o'clock. Slices fill the other 344°.
- `span = 344° / N`. Slice `i` (0-based) runs from `a0(i) = 8° + i·span` to `a0(i + 1)`. Its middle is
  `mid(i) = 8° + (i + ½)·span`.
- Slice gap, a channel of constant width between neighbours: `g = clamp(0.16 · span_rad · 100, 0.7, 3)` u. Each side of a
  cell is pulled in by `g / 2`, measured along the arc at that radius, so the two walls of a channel are parallel.

## 4. Rings and cells

Ring centre radii, red test first: `R = [168, 151, 134, 117, 100]`. Pitch 17.

**Every cell is the same shape:** a filled annulus sector from `R[k] − 5.5` to `R[k] + 5.5` (11 u thick) across the
slice, inset by the slice gap, with its four corners rounded by `cr = min(2, 0.3 · (span_rad · R[k] − g))`.

**A cell's colour**, for ring `k` (1 to 5) of a slice. The first line that applies:

| | When | Colour | Class |
|---|---|---|---|
| now | `at == k` and not `held`: the ring a running task is working toward | yellow, with the glow of section 6 | `now` |
| held | `at == k` and `held`: that ring on a held task | peach, still | `held` |
| errored | `error_at == k` | red | `errored` |
| passed | the ring's boolean is true | green | `passed` |
| not reached | otherwise | the track tone | `track` |

So a task that is done is green on every ring it has evidence for. A running task is green on every ring it has
reached and yellow on the first ring it has not, counting from red test; the rest are track. A task that ended short is
green where it got, red on one cell, track on the rest. The yellow or red cell can sit before a green one (red test
yellow, working green), and a red cell can sit past an unreached one (red test, working, then checks red, with tests
pass still track). Section 2 has a row for each case.

The sector, as the reference page builds it. `a0`, `a1` in radians, `g` the gap, `cr` the corner radius; `pt(r, a)` prints
`point(r, a)`:

```ts
function sector(ri: number, ro: number, a0: number, a1: number, g = 0, cr = 0): string {
  let o0 = a0 + g / 2 / ro, o1 = a1 - g / 2 / ro, i0 = a0 + g / 2 / ri, i1 = a1 - g / 2 / ri;
  if (i1 <= i0) { const m = (a0 + a1) / 2; i0 = m - 0.0005; i1 = m + 0.0005; }
  cr = Math.min(cr, (ro - ri) / 2, ((i1 - i0) * ri) / 2);
  const big = (sweep: number) => (sweep > Math.PI ? 1 : 0);
  if (cr < 0.3) {
    return `M${pt(ro, o0)}A${ro} ${ro} 0 ${big(o1 - o0)} 1 ${pt(ro, o1)}L${pt(ri, i1)}A${ri} ${ri} 0 ${big(i1 - i0)} 0 ${pt(ri, i0)}Z`;
  }
  const co = cr / ro, ci = cr / ri, f = cr / (ro - ri);
  const s1o = o1 + (i1 - o1) * f, s1i = i1 + (o1 - i1) * f, s0o = o0 + (i0 - o0) * f, s0i = i0 + (o0 - i0) * f;
  return `M${pt(ro, o0 + co)}A${ro} ${ro} 0 ${big(o1 - o0 - 2 * co)} 1 ${pt(ro, o1 - co)}Q${pt(ro, o1)} ${pt(ro - cr, s1o)}` +
    `L${pt(ri + cr, s1i)}Q${pt(ri, i1)} ${pt(ri, i1 - ci)}A${ri} ${ri} 0 ${big(i1 - i0 - 2 * ci)} 0 ${pt(ri, i0 + ci)}` +
    `Q${pt(ri, i0)} ${pt(ri + cr, s0i)}L${pt(ro - cr, s0o)}Q${pt(ro, o0)} ${pt(ro, o0 + co)}Z`;
}
```

A sweep never reaches 360° (the widest slice is 344°), so one arc command with the large-arc flag is enough; the
two-halves trick in today's `sectorPath` is not needed.

## 5. Colour, by token

| Part | Web (`style.css`) | Phone (`Mocha`) |
|---|---|---|
| Ground (the page behind the chart) | `--crust` | `Base` |
| Passed cell | `--green` | `Green` |
| Now cell, and its glow | `--yellow` | `Yellow` |
| Held cell | `--peach` | `Peach` |
| Errored cell | `--red` | `Red` |
| Unreached cell (track) | `color-mix(in srgb, var(--surface0) 60%, var(--crust))` | `Surface1` at 60% over `Base` |
| Cursor outline, hand | `--mauve` | `Mauve` |
| Cursor wash | `color-mix(in srgb, var(--mauve) 16%, var(--crust))` | `Mauve` at 16% over `Base` |
| Hover outline (web only) | `--text` at 55%, fill `--text` at 6% | none |
| Ring letters, bezel numbers, labelled ticks, "i of N" | `--overlay0` | `Overlay0` |
| Unlabelled ticks | `--surface1` | `Surface1` |
| Readout: label, done count | `--text` | `Text` |
| Readout: "/N", "tasks done", separators | `--overlay1` | `Overlay1` |
| Readout: a phase letter | its cell's colour; `--surface1` when not reached | the same; `Surface1` |
| Empty state's orbits | `--surface0` | `Surface1` |

Every colour is at full strength. Yellow appears only on now cells, their glow and the readout's words for them. The
`.ts-*` classes and `--ts` no longer colour the chart; they still colour the task list's marks.

## 6. The yellow cell and its glow

The now cell, the cell of ring `at` on a running task, is drawn like any cell, in yellow at full strength, and does
not change. A cell that dims stops looking yellow, so the breathing is on the glow alone.

**The glow.** For each slice with `1 ≤ at ≤ 5` that is not held: the now cell's own sector path again, filled yellow
and stroked yellow 4 u wide with round joins, blurred by a Gaussian of standard deviation 3.4 u. It is drawn over all
the cells (section 10), so it spills a little on its neighbours, and it takes no pointer events.

**Breathing.** The glow's opacity goes 0.8 → 0.2 → 0.8 over 1.8 s, ease-in-out, forever. Every glow in a chart breathes
together.

**Reduced motion.** No animation. The glow holds at opacity 0.5.

**Held.** A held task's cell is peach with no glow and no motion.

**Why the cell and not the whole slice.** A glow over the whole slice lays yellow on the green cells the task has
passed and lights the cells it has not reached; green must mean passed and track must mean not reached. At 6 tasks it is
a slab a sixth of the chart wide. The cell's glow has the same size at every count, so at 150 it is wider than the cell
and is what the eye finds. `chart-options.html` shows both.

## 7. Ring letters and the keys

In the start gap, one letter per ring at `(c.x, c.y − R[k])`, centred both ways: `R W T C G`. Bold, 8.5 u, overlay0.

Under the chart, two centred lines that wrap, 11 px, overlay0, 12 px between entries:

- the rings: `R red test  W working  T tests pass  C checks  G gate`, the letters bold in overlay1;
- the colours, each after a 9 × 5 px swatch: `passed`, `now`, `held`, `errored`.

## 8. Bezel

Only when `N ≥ 10`.

- A tick at `mid(i)` for every fifth slice (the 5th, 10th, 15th, ...), from radius 179 to 182, 1 u wide, surface1.
- `step` = the first of `[5, 10, 25, 50]` with `N / step ≤ 8`. A tick whose slice number is a multiple of `step` runs to
  184 in overlay0 and has its number (the 1-based slice position) centred at radius 192.5, 8.5 u, overlay0, upright.

## 9. Cursor outline

For the slice whose task is the cursor's:

- Wedge: `sector(91, 177, a0(i), a0(i + 1), −1, 3)`. The negative gap pushes each side 0.5 u out, so the outline sits in
  the channels beside the slice.
- Outline: the wedge, no fill, stroke mauve 1.5 u, round joins, drawn over the cells.
- Wash: the wedge filled with the wash colour, drawn **under** the cells so their colour is untouched. Only
  when `N ≥ 18`: a narrow slice needs the wash to be found; on a wide one it is a slab of mauve.
- Hand: a filled mauve triangle pointing outward at the slice from the centre. Tip at `point(89, mid(i))`;
  base corners at `point(82.5, mid(i)) ± 3.4 · (cos mid, sin mid)`.

No slice is outlined when the cursor has no task or its task has no slice, as today.

## 10. Draw order

1. cursor wash
2. the cells, slice by slice
3. the glows
4. ring letters, bezel
5. cursor outline and hand
6. hover outline (web)
7. centre readout

## 11. Centre readout

All text is centred on `c.x`. Baselines are given as offsets from `c.y`. Nothing in the readout goes outside radius 78.

**No cursor task and no hover: the epic.** Counts are of slices: done is `hue == done`; held is `held`; running is
`hue == dispatched` and not held; gap is `hue == gap`. A zero count is left out.

- The done count, bold, 34 u, text; then `/N`, regular, overlay1, same size.
- `tasks done`, 11 u, overlay1. Where the epic's record lists more tasks than it has slices, `tracked tasks done`, so
  the count is not read against the list row's `done of total`.
- `{n} running` in yellow, `{n} held` in peach, `{n} gap` in red, 10.5 u, joined by ` · ` in overlay1.

| | count | caption | tallies |
|---|---|---|---|
| one or two tallies | `−2` | `+17` | one line at `+38` |
| all three | `−6` | `+12` | running and held at `+31`, gap at `+46` |

**A cursor task or a hovered slice: that task.**

- `−30`: `{i + 1} of {N}`, 9 u, overlay0.
- `−5`: the task label, bold, text. Size `clamp(146 / (0.6 · length), 9, 22)` u; a label over 26 characters is cut to 25
  and an ellipsis.
- `+14`: one line, 10.5 u: `done` in green; `running · {name of ring at}` in yellow; `held · {name of ring at}` in
  peach; `gap · {name of ring error_at}` in red. The ring named is the yellow, peach or red one: what a running task is
  working toward, where a held one waits, where an ended one errored.
- `+37`: the five letters `R W T C G`, bold, 11 u, at `c.x + (k − 3) · 15` for ring `k`. Each takes its cell's colour;
  a letter whose cell is not reached is surface1.

Precedence: hovered slice, then the cursor's task, then the epic.

## 12. Hover and tap

A slice is found by angle alone, so a 4 px slice is as easy to hit as a wide one. From a pointer at `(x, y)` relative to
`c`: `r = hypot(x, y)`, `a = atan2(x, −y)` wrapped to `[0, 2π)`.

- `r < 80.5`: the centre.
- `r > 200`, or `a` inside the start gap: nothing.
- otherwise slice `min(N − 1, floor((a − 8°) / span))`.

**Web.** `pointermove` over the chart sets the hovered slice; `pointerleave` clears it. The hovered slice gets the
wedge of section 9 with stroke text at 55%, 1 u, and fill text at 6%, no hand, and the readout shows it. The cursor's
outline stays where it is. The per-cell `<title>` stays. Optional: a click on a slice whose task has a step among the
list's entries moves the list cursor to that step; a click on any other slice does nothing.

**Phone.** There is no cursor, so the chart keeps its own selection. A tap on a slice selects it: wash, outline, hand,
readout. A tap on the selected slice or on the centre clears it. A drag that starts on the rings scrubs: the selection
follows the finger's angle until it lifts. No hover outline.

## 13. Empty state

When there are no slices, no chart. In its place, centred: five dotted orbits and the sentence.

- A 132 px square with circles of radius 62, 51, 40, 29, 18 about its centre: no fill, stroke 1.5 px in the orbit
  colour, dash `2 5`.
- 4 px below, `No orrery-tracked steps` in overlay1, the column's own text size.

On the web keep the sentence in `p.side-note`, so the existing test still finds it, and do not give the small SVG the
class `chart`.

## 14. From 1 to 150

The formulas cover every count. Only the bezel (`N ≥ 10`) and the wash (`N ≥ 18`) switch.

| N | span | gap `g` | cell width, inner to outer ring | corner `cr` | bezel numbers every |
|---|---|---|---|---|---|
| 1 | 344° | 3.0 | 597 to 1006 | 2 | none |
| 2 | 172° | 3.0 | 297 to 501 | 2 | none |
| 5 | 68.8° | 3.0 | 117 to 199 | 2 | none |
| 12 | 28.7° | 3.0 | 47 to 81 | 2 | 5 |
| 35 | 9.83° | 2.74 | 14.4 to 26.1 | 2 | 5 |
| 80 | 4.30° | 1.20 | 6.3 to 11.4 | 1.9 to 2 | 10 |
| 150 | 2.29° | 0.70 | 3.3 to 6.0 | 1.0 to 1.8 | 25 |

One slice is a ring broken only at 12 o'clock. Two are two halves. At 150 the cells are teeth: the reader takes in the
green, the red teeth and the glows, then points at a tooth for its name. The glow is the same size at every count. The
drawing holds to about 220 slices, where the inner cell is 2 u wide; past that it needs a different answer and this
spec does not give one.

## 15. Web: markup, CSS and tests

Keep the structure the tests read: `svg.chart` > `g.slice` (with `cur` on the cursor's) > five `path.cell`, each with
its `<title>`. The dot is gone, so nothing has to leave its slice's group; the glows are their own group after the
slices.

- A cell's class is `cell` and one of `passed`, `now`, `held`, `errored`, `track` (section 4). The `ts-{hue}` class on
  cells goes.
- A cell's title is `{task} {ring}: ` and one of `passed`, `working now`, `held here`, `errored here`, `not reached`.
- `g.glows` holds one `path.glow` per running slice that is not held, with that slice's now cell's `d`.

```css
.epic-view .chart { display: block; width: 100%; max-width: 404px; height: auto; margin-inline: auto; }
.epic-view .chart text { font-family: var(--mono); }
.epic-view .cell.passed { fill: var(--green); }
.epic-view .cell.now { fill: var(--yellow); }
.epic-view .cell.held { fill: var(--peach); }
.epic-view .cell.errored { fill: var(--red); }
.epic-view .cell.track { fill: color-mix(in srgb, var(--surface0) 60%, var(--crust)); }
.epic-view .glow {
  fill: var(--yellow); stroke: var(--yellow); stroke-width: 4; stroke-linejoin: round; pointer-events: none;
  filter: url(#orbit-glow); animation: orbit-glow 1.8s ease-in-out infinite;
}
@keyframes orbit-glow { 0%, 100% { opacity: .8; } 50% { opacity: .2; } }
@media (prefers-reduced-motion: reduce) { .epic-view .glow { animation: none; opacity: .5; } }
.epic-view .wash { fill: color-mix(in srgb, var(--mauve) 16%, var(--crust)); }
.epic-view .outline { fill: none; stroke: var(--mauve); stroke-width: 1.5; stroke-linejoin: round; }
.epic-view .outline.hover { fill: color-mix(in srgb, var(--text) 6%, transparent); stroke: color-mix(in srgb, var(--text) 55%, transparent); stroke-width: 1; }
.epic-view .hand { fill: var(--mauve); }
.epic-view .ring-letter { fill: var(--overlay0); font-size: 8.5px; font-weight: 700; }
.epic-view .tick { stroke: var(--surface1); stroke-width: 1; }
.epic-view .tick.major { stroke: var(--overlay0); }
.epic-view .tick-number { fill: var(--overlay0); font-size: 8.5px; }
.epic-view .chart-key { margin: 2px 0 0; display: flex; flex-wrap: wrap; justify-content: center; gap: 0 12px; font-size: 11px; line-height: 18px; color: var(--overlay0); }
.epic-view .chart-key b { color: var(--overlay1); margin-right: 5px; }
```

The filter is in the chart's own `<defs>`, over the whole box so a small cell's blur is not cut off:

```html
<filter id="orbit-glow" filterUnits="userSpaceOnUse" x="0" y="0" width="412" height="412"><feGaussianBlur stdDeviation="3.4"/></filter>
```

The old rules for `.cell` (its `--ts` fill and base stroke), `.cell.track`'s `--surface0` fill, `.slice.cur .cell`,
`.dot` and the 200 px `max-width` go. The readout's colours are in section 5; give its `<text>` elements classes to
match.

Tests in `web/src/epicview.test.tsx`:

| Test today | Becomes |
|---|---|
| "N slices draw five cells each, and only the running slice has a dot" | Five cells each; no `.dot` anywhere; a running slice has exactly one `.cell.now`, and `.glow` count equals the running slices that are not held |
| "a reached cell carries its slice's hue class and an unreached cell the track class" | Each cell carries the class of section 4's table for its slice's fields |
| "each ring reads its own field, outermost first" | The same, with `cell passed` where it expected `cell ts-{hue}` |
| "each cell's title names its task, its ring and whether it is reached" | The five words above |
| "the running slice's dot sits on the ring its at names, and on no other" | The slice's one `.cell.now` is the cell of ring `at`; every cell with a true boolean is `passed`, the `now` cell among none of them; the glow's `d` is the `now` cell's `d` |
| "N slices start at the top, run clockwise, and each takes an equal angle" | Slices start at 8° and each takes `344° / N`; a cell's corners are inset by the slice gap and have no point at the slice's middle; the dot assertions go |
| the cursor tests | Unchanged: `.chart .cur` matches only the cursor's `g.slice`; the wash, outline and hand use other class names |

New tests: a held slice has one `.cell.held` on ring `at`, no `.cell.now` and no glow; a slice with `error_at` has one
`.cell.errored` on that ring, including when a ring before it is `track` and when a ring after it is `passed`; a done
slice has neither; a running slice with only `working` true and `at` 1 has `now` on red test, `passed` on working and
three `track` cells; a running slice with no true boolean and `at` 1 has `now` on red test and four `track` cells; `at`
wins over `error_at`, and each wins over a true boolean on the same ring.

The fixtures change too: a running slice's `at` is now the first ring whose boolean is false, counting from red test,
so `syntheticSlices` and every hand-written slice with an `at` take that value. Serve's own test for `at` (W3a, DL-O2,
"a running slice's dot is on its innermost reached ring") becomes the rule in section 2 (DL-O9), with a case where the
first false ring comes before a true one.

`centreOf` reads half the viewBox width, which is still the centre with `viewBox="0 0 412 412"`. The synthetic slices in
the tests and in the W3a scratch seed need `held`, `error_at` and the new `at`.

## 16. Phone: Compose

A new item between the header (or the problem line) and the Tasks heading:

- `Box(Modifier.fillMaxWidth().padding(horizontal = 14.dp).widthIn(max = 420.dp).aspectRatio(1f))`, a `Canvas` filling
  it, and the readout as a centred `Column` of `Text` over the canvas. Then the two key lines as centred `Text`, 11 sp.
- `val u = size.width / 412f`. Compose measures angles clockwise from 3 o'clock, so a spec angle `a` in degrees is
  `a − 90` for `arcTo`.
- Cells: build the sector of section 4 into a `Path` with `arcTo` for the two arcs, `quadraticBezierTo` for the four
  corners and `lineTo` for the sides. One `Path` per colour: track in `lerp(Mocha.Base, Mocha.Surface1, 0.6f)`, then
  `Green`, `Red`, `Peach`, `Yellow`.
- Glow: the now cells' path once more, through `drawIntoCanvas` with a framework `Paint` in `Yellow`, style
  `FILL_AND_STROKE`, stroke width `4f * u`, round joins, and `maskFilter = BlurMaskFilter(5.5f * u, NORMAL)`. Its alpha
  follows one `rememberInfiniteTransition` value from 0.8 to 0.2, 900 ms each way, `FastOutSlowInEasing`, reversing.
  When the system's animator duration scale is 0, draw it still at alpha 0.5.
- Ring letters and bezel numbers: `rememberTextMeasurer()` and `drawText`, Orrery Mono, sized `(8.5f * u).toSp()`.
  Readout sizes convert the same way from section 11.
- Selection: `var selected by remember(detail) { mutableIntStateOf(-1) }`. `pointerInput` with `detectTapGestures`
  and `detectDragGestures`, both through the angle rule of section 12.
- Semantics: the canvas gets `contentDescription = "{N} tasks: {d} done, {r} running, {h} held, {g} gap"` and
  `testTag("epic-chart")`; the empty state gets `testTag("epic-chart-none")`.

## 17. What to check against the reference page

- There is no dot and no halo ring anywhere.
- No cell with a true boolean is anything but green.
- At 35 slices t33 and t34 are green on red test, working and tests pass and yellow on checks, with a soft glow; t35,
  held, is peach on checks and does not move; t32, which has given no red evidence, is yellow on red test and green on
  working; t10 and t11, which failed with none, are red on red test and green on working.
- A task that ended short is green up to where it got and has exactly one red cell.
- Yellow is on now cells and nowhere else; at the glow's low point no neighbour looks yellow.
- With reduced motion on, nothing in the chart moves and the glow is still visible.
- The wash under the cursor's slice does not change the colour of that slice's cells.
- With no cursor and nothing hovered the centre reads `11/35`, `tasks done`, `3 running · 1 held`, `20 gap` for the
  synthetic-35 epic.
