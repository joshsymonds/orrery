package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.Dp
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.min

/** Semantics key: the mark's model, so a test reads what the drawing was given. */
internal val EpicMarkKey = SemanticsPropertyKey<EpicMarkModel>("EpicMark")

/** The tone a rim segment is drawn in, by its requirement's state: ok, run, mine, bad, or firm for not reached. */
internal enum class RimTone { OK, RUN, MINE, BAD, FIRM }

/** One rim segment of a mark: its requirement's [id], its [state], and the [tone] that state is drawn in. */
internal data class MarkSegment(val id: String, val state: String, val tone: RimTone)

/** The tone a centre mark is drawn in. */
internal enum class CentreTone { LIVE, ATTENTION, ENGINE, OK, QUIET }

/** The glyph at a centre: the proven count, a question mark, a pause bar, an arrow, a check, or a dash. */
internal enum class CentreGlyph { COUNT, QUESTION, PAUSE, ARROW, CHECK, DASH }

/** An epic's centre: the [word] its label reads, its [tone], its [glyph], and the [proven] count a COUNT shows. */
internal data class CentreMark(val word: String, val tone: CentreTone, val glyph: CentreGlyph, val proven: Int = 0)

/**
 * What an epic's mark draws: one rim segment per requirement in contract order, none for the dotted planned ring, its
 * [centre] (null when the centre state names none), and the [label] the drawing reads.
 */
internal data class EpicMarkModel(val rim: List<MarkSegment>, val centre: CentreMark?, val label: String)

private const val PROVEN = "proven"
private const val NO_REQUIREMENTS = "no requirements yet"

/**
 * The mark of an epic from its Requirements' states and its centre state, as Epics.Get gives them. Its label names the
 * proven count of the requirements, then the centre's word.
 */
internal fun epicMarkOf(requirements: List<EpicRequirementRow>, centre: String): EpicMarkModel {
    val proven = requirements.count { it.state == PROVEN }
    val count = if (requirements.isEmpty()) NO_REQUIREMENTS else "$proven of ${requirements.size} requirements proven"
    val mark = centreOf(centre, proven)
    return EpicMarkModel(
        rim = requirements.map { MarkSegment(it.id, it.state, markToneOf(it.state)) },
        centre = mark,
        label = if (mark == null) count else "$count, ${mark.word}",
    )
}

private fun markToneOf(state: String): RimTone = when (state) {
    PROVEN -> RimTone.OK
    "in_flight" -> RimTone.RUN
    "held" -> RimTone.MINE
    "failed" -> RimTone.BAD
    else -> RimTone.FIRM
}

// centreOf is the centre mark of a centre state: the proven count while it runs, and a glyph for each other state. A
// centre state it does not name has no centre mark.
private fun centreOf(centre: String, proven: Int): CentreMark? = when (centre) {
    "running" -> CentreMark("running", CentreTone.LIVE, CentreGlyph.COUNT, proven)
    "asks" -> CentreMark("asks you a question", CentreTone.ATTENTION, CentreGlyph.QUESTION)
    "paused" -> CentreMark("paused", CentreTone.QUIET, CentreGlyph.PAUSE)
    "releasing" -> CentreMark("releasing", CentreTone.ENGINE, CentreGlyph.ARROW)
    "shipped" -> CentreMark("shipped", CentreTone.OK, CentreGlyph.CHECK)
    "ended_short" -> CentreMark("ended short", CentreTone.QUIET, CentreGlyph.DASH)
    else -> null
}

/** The view box the mark is drawn in, and its radius and strokes, in those units. */
private const val MARK_VIEW = 24f
private const val RIM_RADIUS = 9f
private const val RIM_STROKE = 3f
private const val CENTRE_STROKE = 2f
private const val PLANNED_DASH = 2f
private const val PLANNED_GAP = 3f

// A rim segment's gap is at most RIM_GAP_MAX percent of the ring; a percent of the ring is DEGREES_PER_PERCENT degrees.
private const val RIM_GAP_MAX = 3f
private const val DEGREES_PER_PERCENT = 3.6f

/**
 * An epic's mark at [size]: a rim of one segment per requirement, each toned by its state and starting at the top, and
 * the centre mark. With no requirements it is the dotted planned ring. Tagged by [epicId].
 */
@Composable
internal fun EpicMark(epicId: String, mark: EpicMarkModel, size: Dp) {
    val measurer = rememberTextMeasurer()
    Canvas(
        modifier = Modifier
            .size(size)
            .testTag("epicmark:$epicId")
            .semantics {
                contentDescription = mark.label
                set(EpicMarkKey, mark)
            },
    ) {
        drawEpicMark(mark, measurer)
    }
}

private fun DrawScope.drawEpicMark(mark: EpicMarkModel, measurer: TextMeasurer) {
    val centre = Offset(size.width / 2f, size.height / 2f)
    val radius = markPx(RIM_RADIUS)
    if (mark.rim.isEmpty()) {
        val planned = Stroke(
            width = markPx(RIM_STROKE),
            pathEffect = PathEffect.dashPathEffect(floatArrayOf(markPx(PLANNED_DASH), markPx(PLANNED_GAP))),
        )
        drawCircle(OrreryTokens.Line.firm, radius, centre, style = planned)
    } else {
        // Each segment takes an equal share of the ring less a gap, from the top, clockwise; a ring of one is drawn whole.
        val share = 100f / mark.rim.size
        val gap = if (mark.rim.size > 1) min(RIM_GAP_MAX, share / 3f) else 0f
        mark.rim.forEachIndexed { i, segment ->
            drawArc(
                color = rimColorOf(segment.tone),
                startAngle = -90f + (i * share + gap / 2f) * DEGREES_PER_PERCENT,
                sweepAngle = (share - gap) * DEGREES_PER_PERCENT,
                useCenter = false,
                topLeft = Offset(centre.x - radius, centre.y - radius),
                size = Size(radius * 2, radius * 2),
                style = Stroke(width = markPx(RIM_STROKE)),
            )
        }
    }
    mark.centre?.let { drawCentre(it, centre, measurer) }
}

private fun DrawScope.drawCentre(mark: CentreMark, centre: Offset, measurer: TextMeasurer) {
    val tone = centreColorOf(mark.tone)
    val stroke = Stroke(width = markPx(CENTRE_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
    when (mark.glyph) {
        CentreGlyph.COUNT -> drawCentreText(mark.proven.toString(), tone, centre, measurer)
        CentreGlyph.QUESTION -> drawCentreText("?", tone, centre, measurer)
        CentreGlyph.PAUSE -> {
            centreLine(tone, 10f, 8.5f, 10f, 15.5f, stroke.width)
            centreLine(tone, 14f, 8.5f, 14f, 15.5f, stroke.width)
        }
        CentreGlyph.ARROW -> {
            centreLine(tone, 8.5f, 12f, 15.5f, 12f, stroke.width)
            drawPath(
                Path().apply {
                    moveTo(markPx(12.5f), markPx(8.5f))
                    lineTo(markPx(16f), markPx(12f))
                    lineTo(markPx(12.5f), markPx(15.5f))
                },
                tone,
                style = stroke,
            )
        }
        CentreGlyph.CHECK -> drawPath(
            Path().apply {
                moveTo(markPx(8.6f), markPx(12.3f))
                lineTo(markPx(10.9f), markPx(14.6f))
                lineTo(markPx(15.3f), markPx(9.7f))
            },
            tone,
            style = stroke,
        )
        CentreGlyph.DASH -> centreLine(tone, 9f, 12f, 15f, 12f, stroke.width)
    }
}

// The centre's text, the count or the question mark, set at the middle of the ring in the centre's tone.
private fun DrawScope.drawCentreText(text: String, tone: Color, centre: Offset, measurer: TextMeasurer) {
    val layout = measurer.measure(text, style = OrreryTokens.Type.label.copy(color = tone))
    drawText(
        layout,
        topLeft = Offset(centre.x - layout.size.width / 2f, centre.y - layout.size.height / 2f),
    )
}

private fun DrawScope.centreLine(color: Color, x1: Float, y1: Float, x2: Float, y2: Float, width: Float) {
    drawLine(
        color,
        Offset(markPx(x1), markPx(y1)),
        Offset(markPx(x2), markPx(y2)),
        strokeWidth = width,
        cap = StrokeCap.Round,
    )
}

private fun DrawScope.markPx(view: Float): Float = view * size.width / MARK_VIEW

private fun rimColorOf(tone: RimTone): Color = when (tone) {
    RimTone.OK -> OrreryTokens.Role.ok
    RimTone.RUN -> OrreryTokens.Role.live
    RimTone.MINE -> OrreryTokens.Role.attention
    RimTone.BAD -> OrreryTokens.Role.bad
    RimTone.FIRM -> OrreryTokens.Line.firm
}

private fun centreColorOf(tone: CentreTone): Color = when (tone) {
    CentreTone.LIVE -> OrreryTokens.Role.live
    CentreTone.ATTENTION -> OrreryTokens.Role.attention
    CentreTone.ENGINE -> OrreryTokens.Role.engine
    CentreTone.OK -> OrreryTokens.Role.ok
    CentreTone.QUIET -> OrreryTokens.Ink.three
}
