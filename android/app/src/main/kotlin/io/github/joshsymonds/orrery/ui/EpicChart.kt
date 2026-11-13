package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicSliceRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.min
import kotlin.math.sin

// The chart is a square of 2 * HALF units, scaled to the canvas's smaller side and centred. A hole of HOLE units sits in
// the middle, then one ring per field in RING_FIELDS, outermost first, each RING_WIDTH units thick.
private const val HALF = 61f
private const val HOLE = 24f
private const val RING_WIDTH = 7f
private const val DOT_RADIUS = 2.5f

// The field that marks a cell reached, one per ring, outermost ring first.
private val RING_FIELDS: List<(EpicSliceRow) -> Boolean> = listOf(
    EpicSliceRow::redTest,
    EpicSliceRow::working,
    EpicSliceRow::testsPass,
    EpicSliceRow::checks,
    EpicSliceRow::gateAccepted,
)

/**
 * The epic's slices as one chart: each slice an equal angle clockwise from the top, with a cell per ring. A reached cell
 * takes its slice's hue and any other a faint track, each edged in a Base hairline. A slice whose [EpicSliceRow.at] names a ring has a dot on that ring
 * at its middle. The slice at [outlined], when it is an index of [slices], has its cells edged in mauve.
 */
@Composable
fun EpicChart(slices: List<EpicSliceRow>, outlined: Int?, modifier: Modifier = Modifier) {
    val outlinedIndex = outlined?.takeIf { it in slices.indices }
    val outlinedTask = outlinedIndex?.let { slices[it].task }
    Canvas(
        modifier = modifier
            .testTag("epic-chart")
            .semantics { outlinedTask?.let { stateDescription = it } },
    ) {
        if (slices.isEmpty()) return@Canvas
        val unit = unitPx()
        val span = 360f / slices.size
        val cellEdge = 0.5.dp.toPx()
        val outlineEdge = 1.5.dp.toPx()
        val dotEdge = 1.dp.toPx()

        val cells = slices.flatMapIndexed { index, slice ->
            RING_FIELDS.mapIndexed { ring, field ->
                val inner = ringInner(ring)
                Cell(
                    path = sector(inner, inner + RING_WIDTH, index * span, span),
                    fill = if (field(slice)) hueColour(slice.hue) else Mocha.Surface0,
                    outlined = index == outlinedIndex,
                )
            }
        }
        // Every fill and its hairline Base edge are drawn before any outline, so a neighbour never covers an outlined edge.
        cells.forEach { drawPath(it.path, it.fill) }
        cells.forEach { drawPath(it.path, Mocha.Base, style = Stroke(cellEdge)) }
        cells.filter { it.outlined }.forEach { drawPath(it.path, Mocha.Mauve, style = Stroke(outlineEdge)) }

        slices.forEachIndexed { index, slice ->
            if (slice.at in 1..RING_FIELDS.size) {
                val centre = spot(ringInner(slice.at - 1) + RING_WIDTH / 2, (index + 0.5f) * span)
                val radius = DOT_RADIUS * unit
                drawCircle(Mocha.Yellow, radius, centre)
                drawCircle(Mocha.Base, radius, centre, style = Stroke(dotEdge))
            }
        }
    }
}

private class Cell(val path: Path, val fill: Color, val outlined: Boolean)

// hueColour is a reached cell's fill: its slice's hue as serve gives it, or the muted text colour for any other.
private fun hueColour(hue: String): Color = when (hue) {
    "dispatched" -> Mocha.Yellow
    "done" -> Mocha.Green
    "gap" -> Mocha.Red
    else -> Mocha.Subtext0
}

// ringInner is the inner radius, in units, of the ring at index. The first ring is the outermost.
private fun ringInner(index: Int): Float = HOLE + (RING_FIELDS.size - 1 - index) * RING_WIDTH

private fun DrawScope.unitPx(): Float = min(size.width, size.height) / (2 * HALF)

// spot is the point at radius units and degrees clockwise from the top of the chart.
private fun DrawScope.spot(radius: Float, degrees: Float): Offset {
    val unit = unitPx()
    val angle = degrees * PI.toFloat() / 180f
    return Offset(center.x + radius * unit * sin(angle), center.y - radius * unit * cos(angle))
}

// sector is the annulus sector from radius inner to outer, spanning sweep degrees clockwise from start degrees from the
// top. Each arc is drawn in two halves, so a lone slice's whole circle still has its ends and middle to draw between.
private fun DrawScope.sector(inner: Float, outer: Float, start: Float, sweep: Float): Path {
    val unit = unitPx()
    val outerRect = Rect(center, outer * unit)
    val innerRect = Rect(center, inner * unit)
    val half = sweep / 2
    // Path angles run from the positive x-axis, so the top is at -90.
    val canvasStart = start - 90f
    return Path().apply {
        arcTo(outerRect, canvasStart, half, true)
        arcTo(outerRect, canvasStart + half, half, false)
        arcTo(innerRect, canvasStart + sweep, -half, false)
        arcTo(innerRect, canvasStart + half, -half, false)
        close()
    }
}
