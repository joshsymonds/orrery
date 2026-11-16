package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Path
import kotlin.math.PI
import kotlin.math.atan2
import kotlin.math.cos
import kotlin.math.floor
import kotlin.math.min
import kotlin.math.sin

// The Orbits chart's geometry in viewBox units: a square of 412 units centred on (CENTRE, CENTRE). Angles are degrees
// clockwise from 12 o'clock. A caller draws at u pixels per unit, so a canvas of width w passes u = w / 412.
private const val CENTRE = 206f
private const val GAP = 16f
private const val START = GAP / 2
private const val SWEEP = 360f - GAP

// The ring centre radii, red test first, and the half thickness of a cell's band.
private val RING_RADII = floatArrayOf(168f, 151f, 134f, 117f, 100f)
private const val RING_HALF = 5.5f

/** The centre radius, in units, of ring [ring]: 0 is red test, 4 is gate accepted. */
fun ringRadius(ring: Int): Float = RING_RADII[ring]

// The hit rule: inside HOLE_RADIUS is the centre, beyond OUTER_RADIUS is nothing.
private const val HOLE_RADIUS = 80.5f
private const val OUTER_RADIUS = 200f

/** Where a pointer lands on the chart: the centre, or the slice at [index]. */
sealed interface OrbitHit {
    data object Centre : OrbitHit
    data class Slice(val index: Int) : OrbitHit
}

/** The angle, in degrees, that each of [count] slices takes: 344 degrees shared out. */
fun sliceSpan(count: Int): Float = SWEEP / count

/** The angle, in degrees, at which slice [index] starts: a0(index). */
fun sliceStart(index: Int, count: Int): Float = START + index * sliceSpan(count)

/** The channel between neighbouring slices, in units: 0.16 of the span in radians times 100, kept between 0.7 and 3. */
fun sliceGap(count: Int): Float = (0.16f * sliceSpan(count).radians * 100f).coerceIn(0.7f, 3f)

/** The corner radius, in units, of the cell on [ring] (0 is red test): 0.3 of the arc left after the gap, at most 2. */
fun cellCorner(count: Int, ring: Int): Float =
    min(2f, 0.3f * (sliceSpan(count).radians * RING_RADII[ring] - sliceGap(count)))

/** The sector of the cell on [ring] of [slice], in pixels at [u] per unit. */
fun cellPath(count: Int, slice: Int, ring: Int, u: Float): Path {
    val radius = RING_RADII[ring]
    return sectorPath(
        ri = radius - RING_HALF,
        ro = radius + RING_HALF,
        startDeg = sliceStart(slice, count),
        endDeg = sliceStart(slice + 1, count),
        gap = sliceGap(count),
        corner = cellCorner(count, ring),
        u = u,
    )
}

/**
 * What a pointer at [relative], an offset from the centre in units, hits on a chart of [count] slices. Inside the hole is
 * the centre; beyond the chart or in the start gap is nothing (null); anywhere else is the slice its angle falls in.
 */
fun orbitHit(relative: Offset, count: Int): OrbitHit? {
    if (relative.getDistance() < HOLE_RADIUS) return OrbitHit.Centre
    if (relative.getDistance() > OUTER_RADIUS) return null
    // atan2 gives angles in (-180, 180]; wrap them into [0, 360).
    var angle = atan2(relative.x, -relative.y).degrees
    if (angle < 0f) angle += 360f
    if (angle < START || angle >= START + SWEEP) return null
    return OrbitHit.Slice(min(count - 1, floor((angle - START) / sliceSpan(count)).toInt()))
}

/**
 * The point [radius] units from the chart's centre, at [angle] radians clockwise from 12 o'clock, in units. The centre
 * itself is point(0, 0).
 */
fun point(radius: Float, angle: Float): Offset =
    Offset(CENTRE + radius * sin(angle), CENTRE - radius * cos(angle))

// The annulus sector from radius ri to ro, from startDeg to endDeg, inset by gap: half of it from each side, measured along
// the arc at that radius, so the two walls of a channel are parallel. Its four corners are rounded by corner. This is the
// reference `sector` of chart-spec.md section 4, in pixels at u per unit.
private fun sectorPath(ri: Float, ro: Float, startDeg: Float, endDeg: Float, gap: Float, corner: Float, u: Float): Path {
    val a0 = startDeg.radians
    val a1 = endDeg.radians
    val o0 = a0 + gap / 2 / ro
    val o1 = a1 - gap / 2 / ro
    var i0 = a0 + gap / 2 / ri
    var i1 = a1 - gap / 2 / ri
    if (i1 <= i0) {
        val m = (a0 + a1) / 2
        i0 = m - 0.0005f
        i1 = m + 0.0005f
    }
    val cr = min(corner, min((ro - ri) / 2, (i1 - i0) * ri / 2))
    val outer = circleBounds(ro, u)
    val inner = circleBounds(ri, u)
    // arcTo measures from 3 o'clock, so an angle clockwise from 12 o'clock is 90 degrees less.
    if (cr < 0.3f) {
        return Path().apply {
            moveTo(point(ro, o0) * u)
            arcTo(outer, o0.degrees - 90f, (o1 - o0).degrees, false)
            lineTo(point(ri, i1) * u)
            arcTo(inner, i1.degrees - 90f, -(i1 - i0).degrees, false)
            close()
        }
    }
    val co = cr / ro
    val ci = cr / ri
    val f = cr / (ro - ri)
    val s1o = o1 + (i1 - o1) * f
    val s1i = i1 + (o1 - i1) * f
    val s0o = o0 + (i0 - o0) * f
    val s0i = i0 + (o0 - i0) * f
    return Path().apply {
        moveTo(point(ro, o0 + co) * u)
        arcTo(outer, (o0 + co).degrees - 90f, (o1 - o0 - 2 * co).degrees, false)
        curveTo(point(ro, o1) * u, point(ro - cr, s1o) * u)
        lineTo(point(ri + cr, s1i) * u)
        curveTo(point(ri, i1) * u, point(ri, i1 - ci) * u)
        arcTo(inner, (i1 - ci).degrees - 90f, -(i1 - i0 - 2 * ci).degrees, false)
        curveTo(point(ri, i0) * u, point(ri + cr, s0i) * u)
        lineTo(point(ro - cr, s0o) * u)
        curveTo(point(ro, o0) * u, point(ro, o0 + co) * u)
        close()
    }
}

// The square that bounds the circle of radius around the centre, in pixels.
private fun circleBounds(radius: Float, u: Float): Rect =
    Rect(center = Offset(CENTRE * u, CENTRE * u), radius = radius * u)

private fun Path.moveTo(p: Offset) = moveTo(p.x, p.y)

private fun Path.lineTo(p: Offset) = lineTo(p.x, p.y)

private fun Path.curveTo(control: Offset, end: Offset) = quadraticTo(control.x, control.y, end.x, end.y)

private val Float.radians: Float
    get() = this * PI.toFloat() / 180f

private val Float.degrees: Float
    get() = this * 180f / PI.toFloat()
