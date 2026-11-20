package io.github.joshsymonds.orrery.ui

import android.graphics.Paint
import android.graphics.Typeface
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asAndroidPath
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.min

/** One ring's name path: the ring, its centre radius, and the arc it runs along in degrees clockwise from 12 o'clock. */
data class RingNameArc(val place: RingPlace, val radius: Float, val startAngle: Float, val endAngle: Float)

// The chart's square, in units, centred on point(0, 0), as Orrery draws it.
private val CENTRE = point(0f, 0f)
private val BOX = 2 * CENTRE.x

// The requirement rim: a thin arc outside the red ring, centred on RIM_RADIUS.
private const val RIM_RADIUS = 177f
private const val RIM_WIDTH = 3f

// The outline of a tapped wedge, in units.
private const val OUTLINE_WIDTH = 1.5f

// The ring names' size in units, the most a name is drawn at. A name too long for its arc is shrunk to fit it.
private const val RING_NAME_SIZE = 8.5f

// A name's baseline sits this many ems inside its ring's centre line, so its letters centre on the ring.
private const val NAME_BASELINE_DROP = 0.35f

/**
 * The name arc of each of the five rings, outside in. Each runs from the gap's far edge, a turn short of the last cell's
 * end, to where the first cell begins, so it passes 12 o'clock inside the gap and ends on its own ring.
 */
fun ringNameArcs(count: Int): List<RingNameArc> {
    val end = sliceStart(0, count)
    val start = sliceStart(count, count) - 360f
    return RingPlace.entries.map { place -> RingNameArc(place, ringRadius(place.ordinal), start, end) }
}

/**
 * The epic's chart: a wedge per task with its five cells outside in, a requirement rim outside them, each ring's name on the
 * ring through the gap at 12 o'clock, and the count of integrated tasks at the centre. A tap on a wedge outlines it and
 * captions it with its task and words; a tap on that wedge again clears it. With no tasks it draws nothing.
 */
@Composable
fun EpicOrrery(tasks: List<RecordTask>, proven: Set<String>, modifier: Modifier = Modifier) {
    var selected by rememberSaveable { mutableStateOf<String?>(null) }
    if (tasks.isEmpty()) return
    val done = tasks.count { it.integrated }
    val chosen = tasks.indexOfFirst { it.task == selected }
    Column(modifier = modifier) {
        Box(modifier = Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
            Canvas(
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("epic-orrery")
                    .semantics { contentDescription = "$done of ${tasks.size} tasks done" }
                    .pointerInput(tasks) {
                        detectTapGestures(
                            onTap = { position ->
                                val unit = min(size.width, size.height) / BOX
                                val relative = (position - Offset(size.width / 2f, size.height / 2f)) / unit
                                (orbitHit(relative, tasks.size) as? OrbitHit.Slice)?.let { hit ->
                                    val task = tasks[hit.index].task
                                    selected = if (selected == task) null else task
                                }
                            },
                        )
                    },
            ) {
                val unit = min(size.width, size.height) / BOX
                translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
                    tasks.forEachIndexed { index, task -> drawWedge(tasks.size, index, task, proven, unit) }
                    if (chosen >= 0) drawOutline(tasks.size, chosen, unit)
                    ringNameArcs(tasks.size).forEach { drawRingName(it, unit) }
                }
            }
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text(text = "$done", style = OrreryTokens.Type.display, color = OrreryTokens.Ink.one)
                Text(text = "of ${tasks.size} done", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.four)
            }
        }
        tasks.firstOrNull { it.task == selected }?.let { task ->
            Text(
                text = "${task.title} · ${task.words}",
                modifier = Modifier.testTag("orrery-caption"),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.one,
            )
        }
    }
}

// A wedge's five cells, outside in, then its requirement rim.
private fun DrawScope.drawWedge(count: Int, index: Int, task: RecordTask, proven: Set<String>, unit: Float) {
    task.cells.forEachIndexed { ring, cell -> drawPath(cellPath(count, index, ring, unit), cellColourOf(cell)) }
    val rim = if (task.requirements.isNotEmpty() && task.requirements.all { it in proven }) {
        OrreryTokens.Role.ok
    } else {
        OrreryTokens.Line.firm
    }
    drawRim(count, index, rim, unit)
}

// The rim arc of wedge [index], inset from its neighbours by half the channel between cells.
private fun DrawScope.drawRim(count: Int, index: Int, colour: Color, unit: Float) {
    val radius = RIM_RADIUS * unit
    val inset = (sliceGap(count) / 2 / RIM_RADIUS).radiansOf().degreesOf()
    drawArc(
        color = colour,
        startAngle = sliceStart(index, count) + inset - 90f,
        sweepAngle = sliceSpan(count) - 2 * inset,
        useCenter = false,
        topLeft = Offset(CENTRE.x * unit - radius, CENTRE.y * unit - radius),
        size = Size(2 * radius, 2 * radius),
        style = Stroke(width = RIM_WIDTH * unit),
    )
}

// The selected wedge's cells, each outlined.
private fun DrawScope.drawOutline(count: Int, index: Int, unit: Float) {
    val stroke = Stroke(width = OUTLINE_WIDTH * unit)
    RingPlace.entries.forEach { ring ->
        drawPath(cellPath(count, index, ring.ordinal, unit), OrreryTokens.Ink.one, style = stroke)
    }
}

// A ring's name, drawn along its arc right-aligned to the arc's end, in the native canvas. It is shrunk when it is longer
// than its arc, so it never runs into the neighbouring wedge.
private fun DrawScope.drawRingName(arc: RingNameArc, unit: Float) {
    val word = arc.place.word
    val sweep = (arc.endAngle - arc.startAngle).radiansOf()
    val nominal = RING_NAME_SIZE * unit
    val radius = arc.radius * unit - NAME_BASELINE_DROP * nominal
    val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = OrreryTokens.Ink.four.toArgb()
        typeface = Typeface.create(Typeface.MONOSPACE, Typeface.BOLD)
        textSize = nominal
    }
    val fitted = min(1f, sweep * radius / paint.measureText(word))
    paint.textSize = nominal * fitted
    val path = Path().apply {
        arcTo(
            Rect(center = Offset(CENTRE.x * unit, CENTRE.y * unit), radius = radius),
            arc.startAngle - 90f,
            arc.endAngle - arc.startAngle,
            false,
        )
    }
    val hOffset = sweep * radius - paint.measureText(word)
    drawContext.canvas.nativeCanvas.drawTextOnPath(word, path.asAndroidPath(), hOffset, 0f, paint)
}

private fun Float.radiansOf(): Float = Math.toRadians(toDouble()).toFloat()

private fun Float.degreesOf(): Float = Math.toDegrees(toDouble()).toFloat()
