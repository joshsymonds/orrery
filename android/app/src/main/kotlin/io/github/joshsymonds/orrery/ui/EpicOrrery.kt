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
import androidx.compose.runtime.remember
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
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.abs
import kotlin.math.min

/** One ring's name path: the ring, its centre radius, and the arc it runs along in degrees clockwise from 12 o'clock. */
data class RingNameArc(val place: RingPlace, val radius: Float, val startAngle: Float, val endAngle: Float)

// The chart's square, in units, centred on point(0, 0), as Orrery draws it.
private val CENTRE = point(0f, 0f)
private val BOX = 2 * CENTRE.x

// The requirement rim: a thin arc outside the red ring, centred on RIM_RADIUS. A tap within RIM_HIT of it is on the rim.
private const val RIM_RADIUS = 177f
private const val RIM_WIDTH = 3f
private const val RIM_HIT = 4f

// A requirement's id sits outside the rim, centred on ID_RADIUS, at ID_SIZE units.
private const val ID_RADIUS = 190f
private const val ID_SIZE = 8.5f

// The outline of a selected segment or task, in units.
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
 * One segment of the requirement rim: the requirement [id], or null for the unlabelled group of tasks that name none; the
 * [first] slot of the chart it takes and its [slots]; and its [tasks], in slot order from [first].
 */
internal data class RimSegment(val id: String?, val first: Int, val slots: Int, val tasks: List<RecordTask>)

/**
 * The rim's segments in order: one per requirement the epic lists, in its order, then the unlabelled group of the tasks that
 * name none it lists, when there is one. A task sits in the first requirement it names that the epic lists; a name the epic
 * does not list is passed over. A task takes one slot of the chart; a segment with no task keeps one empty slot, so its
 * requirement still shows.
 */
internal fun rimSegmentsOf(tasks: List<RecordTask>, requirements: List<String>): List<RimSegment> {
    val groups = mutableListOf<Pair<String?, List<RecordTask>>>()
    for (id in requirements.distinct()) {
        groups += id to tasks.filter { placedIn(it, requirements) == id }
    }
    val unlabelled = tasks.filter { placedIn(it, requirements) == null }
    if (unlabelled.isNotEmpty()) groups += null to unlabelled
    val segments = mutableListOf<RimSegment>()
    var first = 0
    for ((id, members) in groups) {
        val slots = maxOf(1, members.size)
        segments += RimSegment(id, first, slots, members)
        first += slots
    }
    return segments
}

// The requirement a task sits in: the first it names that [requirements] lists, null when it names none of them.
private fun placedIn(task: RecordTask, requirements: List<String>): String? =
    task.requirements.firstOrNull { it in requirements }

/** The slots a chart of [segments] takes in all: one per task, and one for each segment with none. */
internal fun rimSlotsOf(segments: List<RimSegment>): Int = segments.sumOf { it.slots }

/**
 * The selection a tap at [relative], an offset from the centre in units, makes: the labelled segment whose rim it is on, else
 * the task whose cells it is on, keyed as [segmentKeyOf] and [taskKeyOf] key them. Null for the centre, the start gap,
 * beyond the rim, the rim of the unlabelled group and an empty slot's cells.
 */
internal fun pickOf(relative: Offset, segments: List<RimSegment>): String? {
    val slot = (orbitHit(relative, rimSlotsOf(segments)) as? OrbitHit.Slice)?.index ?: return null
    val segment = segments.first { slot >= it.first && slot < it.first + it.slots }
    val distance = relative.getDistance()
    return when {
        abs(distance - RIM_RADIUS) <= RIM_HIT -> segmentKeyOf(segment)
        distance > RIM_RADIUS + RIM_HIT -> null
        else -> segment.tasks.getOrNull(slot - segment.first)?.let(::taskKeyOf)
    }
}

// The key a segment is selected by: its requirement's id, and none for the unlabelled group.
private fun segmentKeyOf(segment: RimSegment): String? = segment.id?.let { "segment:$it" }

// The key a task is selected by.
private fun taskKeyOf(task: RecordTask): String = "task:${task.task}"

/**
 * A segment's rim colour by its requirement's server [state]: proven is ok, failed bad, held attention, in_flight live, and
 * any other state, or none, the firm line.
 */
internal fun rimToneOf(state: String): Color = when (state) {
    "proven" -> OrreryTokens.Role.ok
    "failed" -> OrreryTokens.Role.bad
    "held" -> OrreryTokens.Tone.mine
    "in_flight" -> OrreryTokens.Tone.run
    else -> OrreryTokens.Line.firm
}

/** A rim segment as the chart's semantics name it: its requirement's [id] (null unlabelled), [state], [colour] and [tasks]. */
data class RimMark(val id: String?, val state: String, val colour: Color, val tasks: List<String>)

/** Semantics key: the rim's segments, in order, on the chart's node. */
val EpicRimKey = SemanticsPropertyKey<List<RimMark>>("EpicRim")

/**
 * The epic's chart, the record's orrery: a rim of one segment per requirement, labelled with its id and toned by its server
 * state, outside five rings of cells, outside in red, implement, green, review and integrate. A task's five cells sit inside
 * the first requirement it names that the epic lists; tasks that name none of them sit in an unlabelled last segment; a
 * requirement no task names keeps an empty segment. The centre reads the proven count of the requirements. A tap on a segment's rim selects it and a tap on a
 * task's cells selects the task; the selection is captioned and outlined, and a second tap clears it. Nothing captions on
 * hover. With no tasks it draws nothing.
 */
@Composable
fun EpicOrrery(
    tasks: List<RecordTask>,
    proven: Set<String>,
    modifier: Modifier = Modifier,
    requirements: List<EpicRequirementRow> = emptyList(),
) {
    var selected by rememberSaveable { mutableStateOf<String?>(null) }
    val segments = remember(tasks, requirements) { rimSegmentsOf(tasks, requirements.map { it.id }) }
    val marks = remember(segments, requirements) { marksOf(segments, requirements) }
    if (tasks.isEmpty()) return
    val slots = rimSlotsOf(segments)
    val done = tasks.count { it.integrated }
    val labelled = segments.mapNotNull { it.id }
    val provenCount = labelled.count { it in proven }
    val readout = buildAnnotatedString {
        withStyle(SpanStyle(fontSize = OrreryTokens.Type.title.fontSize)) { append("$provenCount") }
        append(" of ${labelled.size} proven")
    }
    val caption = captionOf(selected, segments, requirements)
    Column(modifier = modifier) {
        Box(modifier = Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
            Canvas(
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("epic-orrery")
                    .semantics {
                        contentDescription = "$done of ${tasks.size} tasks done"
                        set(EpicRimKey, marks)
                    }
                    .pointerInput(segments) {
                        detectTapGestures(
                            onTap = { position ->
                                val unit = min(size.width, size.height) / BOX
                                val relative = (position - Offset(size.width / 2f, size.height / 2f)) / unit
                                pickOf(relative, segments)?.let { key -> selected = if (selected == key) null else key }
                            },
                        )
                    },
            ) {
                val unit = min(size.width, size.height) / BOX
                translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
                    segments.forEach { segment ->
                        segment.tasks.forEachIndexed { position, task -> drawTask(slots, segment.first + position, task, unit) }
                    }
                    segments.forEachIndexed { index, segment ->
                        drawRimArc(segment, slots, RIM_RADIUS, RIM_WIDTH, marks[index].colour, unit)
                        drawSegmentId(segment, slots, unit)
                    }
                    segments.forEach { segment ->
                        if (isSelected(segmentKeyOf(segment), selected)) drawSegmentOutline(segment, slots, unit)
                        segment.tasks.forEachIndexed { position, task ->
                            if (isSelected(taskKeyOf(task), selected)) drawOutline(slots, segment.first + position, unit)
                        }
                    }
                    ringNameArcs(slots).forEach { drawRingName(it, unit) }
                }
            }
            Text(text = readout, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one, modifier = Modifier.testTag("orrery-centre"))
        }
        caption?.let { text ->
            Text(
                text = text,
                modifier = Modifier.testTag("orrery-caption"),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.one,
            )
        }
    }
}

// The semantics of each segment, in the order the rim draws them. The unlabelled group takes the soft line.
private fun marksOf(segments: List<RimSegment>, requirements: List<EpicRequirementRow>): List<RimMark> =
    segments.map { segment ->
        val state = requirements.find { it.id == segment.id }?.state.orEmpty()
        val colour = if (segment.id == null) OrreryTokens.Line.soft else rimToneOf(state)
        RimMark(id = segment.id, state = state, colour = colour, tasks = segment.tasks.map { it.task })
    }

private fun isSelected(key: String?, selected: String?): Boolean = key != null && key == selected

// The caption of a selection: a segment's id, its state in words and its requirement's text, or a task's title and words.
// Null for no selection, and for a key the chart does not draw.
private fun captionOf(key: String?, segments: List<RimSegment>, requirements: List<EpicRequirementRow>): String? {
    if (key == null) return null
    val id = segments.find { isSelected(segmentKeyOf(it), key) }?.id
    if (id != null) {
        val row = requirements.find { it.id == id }
        return listOf(id, row?.state.orEmpty().replace('_', ' '), row?.text.orEmpty())
            .filter { it.isNotEmpty() }
            .joinToString(" · ")
    }
    return segments.flatMap { it.tasks }.find { taskKeyOf(it) == key }?.let { "${it.title} · ${it.words}" }
}

// A task's five cells, outside in, at its [slot] of a chart of [count] slots.
private fun DrawScope.drawTask(count: Int, slot: Int, task: RecordTask, unit: Float) {
    task.cells.forEachIndexed { ring, cell -> drawPath(cellPath(count, slot, ring, unit), cellColourOf(cell)) }
}

// A segment's arc at [radius] units, [width] units thick: its slots, inset from its neighbours by half the channel between
// cells.
private fun DrawScope.drawRimArc(
    segment: RimSegment,
    count: Int,
    radius: Float,
    width: Float,
    colour: Color,
    unit: Float,
) {
    val inset = (sliceGap(count) / 2 / radius).degreesOf()
    drawArc(
        color = colour,
        startAngle = sliceStart(segment.first, count) + inset - 90f,
        sweepAngle = sliceSpan(count) * segment.slots - 2 * inset,
        useCenter = false,
        topLeft = Offset(CENTRE.x * unit - radius * unit, CENTRE.y * unit - radius * unit),
        size = Size(2 * radius * unit, 2 * radius * unit),
        style = Stroke(width = width * unit),
    )
}

// A selected segment's rim, outlined along both its edges.
private fun DrawScope.drawSegmentOutline(segment: RimSegment, count: Int, unit: Float) {
    val edge = RIM_WIDTH / 2
    drawRimArc(segment, count, RIM_RADIUS - edge, OUTLINE_WIDTH, OrreryTokens.Ink.one, unit)
    drawRimArc(segment, count, RIM_RADIUS + edge, OUTLINE_WIDTH, OrreryTokens.Ink.one, unit)
}

// A labelled segment's id, outside the rim at the middle of its arc. An unlabelled segment has none.
private fun DrawScope.drawSegmentId(segment: RimSegment, count: Int, unit: Float) {
    val id = segment.id ?: return
    val middle = sliceStart(segment.first, count) + sliceSpan(count) * segment.slots / 2
    val at = point(ID_RADIUS, middle.radiansOf())
    val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = OrreryTokens.Ink.three.toArgb()
        typeface = Typeface.create(Typeface.MONOSPACE, Typeface.BOLD)
        textSize = ID_SIZE * unit
        textAlign = Paint.Align.CENTER
    }
    drawContext.canvas.nativeCanvas.drawText(id, at.x * unit, at.y * unit + paint.textSize * NAME_BASELINE_DROP, paint)
}

// The selected task's cells, each outlined.
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
