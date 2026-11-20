package io.github.joshsymonds.orrery.ui

import android.graphics.BlurMaskFilter
import android.graphics.Paint
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asAndroidPath
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.TextUnit
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import io.github.joshsymonds.orrery.ui.theme.pulseSpec
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.min

// The chart's box is OrbitGeometry's square, whose centre is point(0, 0): the canvas centres it and scales it to fit.
private val CENTRE = point(0f, 0f)
private val BOX = 2 * CENTRE.x

// The rim is ring 0, and each phase's ring is one more than its index.
private const val RIM = 0

// The distance between neighbouring rings. A pointer nearer than half of it to a ring's radius is on that ring.
private val RING_STEP = ringRadius(RIM) - ringRadius(RIM + 1)

// The requirement arcs are a stroke from ARC_INNER to ARC_OUTER, centred on ARC_MID; their ids sit at ID_RADIUS.
private const val ARC_INNER = 184f
private const val ARC_OUTER = 192f
private const val ARC_MID = (ARC_INNER + ARC_OUTER) / 2
private const val ID_RADIUS = 200f

// The glow breathes between the pulse's full and its quarter, each way for half of motion.loop.pulse. It holds at full
// when animations are off.
private const val GLOW_STROKE = 4f
private const val GLOW_BLUR = 5.5f

// Text sizes, in units, and how far the word under the count sits below its centre.
private const val RING_NAME_SIZE = 8.5f
private const val ARC_ID_SIZE = 8.5f
private const val COUNT_SIZE = 34f
private const val COUNT_OF_SIZE = 11f
private const val COUNT_OF_DROP = 16f

// The rim's name, then each phase's, from the rim inward.
private val RING_NAMES = listOf("planned") + Phase.entries.map { it.word }

// Unreached cells are the firm line at 60% over the page.
private val TRACK: Color = lerp(OrreryTokens.Surface.page, OrreryTokens.Line.firm, 0.6f)

/**
 * The epic's orrery: a wedge per task, a ring of cells per phase inward from the planned rim, the requirement arcs around
 * them, the now slice of each running wedge glowing, and the count of integrated wedges at the centre. A tap on a phase cell
 * calls [onOpenPhase] with that cell's task and phase; a tap on the rim calls nothing, and with null the canvas takes no taps.
 */
@Composable
fun Orrery(
    wedges: List<Wedge>,
    arcs: List<RequirementArc>,
    modifier: Modifier = Modifier,
    onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null,
) {
    val glow = glowAlpha()
    val measurer = rememberTextMeasurer()
    val readout = "${wedges.count { it.place == WedgePlace.INTEGRATED }} of ${wedges.size}"
    val description = descriptionOf(readout, wedges, arcs)
    val glowText = glowTextOf(wedges)
    val tappable = if (onOpenPhase == null) {
        modifier
    } else {
        modifier.pointerInput(wedges, onOpenPhase) {
            detectTapGestures(
                onTap = { position ->
                    val centre = Offset(size.width / 2f, size.height / 2f)
                    cellAt((position - centre) / (min(size.width, size.height) / BOX), wedges)?.let { (task, phase) ->
                        onOpenPhase(task, phase)
                    }
                },
            )
        }
    }
    Canvas(
        modifier = tappable
            .testTag("orrery")
            .semantics {
                this[SemanticsProperties.ContentDescription] = description
                stateDescription = glowText
            },
    ) {
        val unit = min(size.width, size.height) / BOX
        drawRect(OrreryTokens.Surface.page)
        translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
            drawCells(wedges, unit)
            drawGlow(glowPath(wedges, unit), unit, glow.value)
            drawArcStrokes(arcs, wedges.size, unit)
            drawArcIds(measurer, arcs, wedges.size, unit)
            drawRingNames(measurer, unit)
            paintCentred(
                measurer,
                readout,
                OrreryTokens.Type.display,
                COUNT_SIZE,
                CENTRE.x,
                CENTRE.y,
                unit,
                OrreryTokens.Ink.one,
            )
            paintCentred(
                measurer,
                "integrated",
                OrreryTokens.Type.small,
                COUNT_OF_SIZE,
                CENTRE.x,
                CENTRE.y + COUNT_OF_DROP,
                unit,
                OrreryTokens.Ink.four,
            )
        }
    }
}

// The canvas's description, a line each: the integrated count, each wedge's place and tone, then each arc's proof.
private fun descriptionOf(readout: String, wedges: List<Wedge>, arcs: List<RequirementArc>): List<String> = buildList {
    add("$readout integrated")
    wedges.forEach { add("${it.task}: ${it.place.name.lowercase()}, ${toneWord(it.tone)}") }
    arcs.forEach { add("${it.id} ${if (it.proven) "proven" else "open"}") }
}

private fun toneWord(tone: WedgeTone): String = when (tone) {
    WedgeTone.PLAN -> "planned"
    WedgeTone.RUN -> "running"
    WedgeTone.MINE -> "waits on you"
    WedgeTone.LIMITED -> "waits on a limit"
    WedgeTone.BAD -> "ended short"
    WedgeTone.ACC -> "accepted"
    WedgeTone.OK -> "integrated"
}

/**
 * An epic's line-sized orrery: the cells and requirement arcs of [Orrery], with no text and no taps. It draws on no
 * background, so the line's ground shows through the track.
 */
@Composable
fun MiniOrrery(wedges: List<Wedge>, arcs: List<RequirementArc>, modifier: Modifier = Modifier) {
    Canvas(modifier = modifier) {
        val unit = min(size.width, size.height) / BOX
        translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
            drawCells(wedges, unit)
            drawArcStrokes(arcs, wedges.size, unit)
        }
    }
}

// The glowing slices, each as its wedge's index and the phase of its now slice. Only a running wedge glows.
private fun glowsOf(wedges: List<Wedge>): List<Pair<Int, Phase>> = wedges.flatMapIndexed { index, wedge ->
    if (wedge.tone == WedgeTone.RUN) {
        wedge.slices.filter { it.state == SliceState.NOW }.map { index to it.phase }
    } else {
        emptyList()
    }
}

// The glow's state, naming each glowing slice by its task and phase.
private fun glowTextOf(wedges: List<Wedge>): String {
    val glows = glowsOf(wedges)
    if (glows.isEmpty()) return "glows: none"
    return "glows: " + glows.joinToString(", ") { (index, phase) -> "${wedges[index].task} ${phase.word}" }
}

// The task and phase of the phase cell at [relative], an offset from the centre in units. Null on the rim, in the centre,
// in the start gap, and beyond the outermost or innermost ring.
private fun cellAt(relative: Offset, wedges: List<Wedge>): Pair<String, Phase>? {
    if (wedges.isEmpty()) return null
    val hit = orbitHit(relative, wedges.size) as? OrbitHit.Slice ?: return null
    val distance = relative.getDistance()
    val ring = (0..Phase.entries.size).minBy { abs(distance - ringRadius(it)) }
    if (abs(distance - ringRadius(ring)) > RING_STEP / 2) return null
    if (ring == RIM) return null
    return wedges[hit.index].task to Phase.entries[ring - 1]
}

// The ring a phase's cells lie on: one inward from the rim.
private fun ringOf(phase: Phase): Int = phase.ordinal + 1

// Each wedge's rim cell, then its four phase cells. The rim is track while its wedge is planned and passed once placed.
private fun DrawScope.drawCells(wedges: List<Wedge>, unit: Float) {
    wedges.forEachIndexed { index, wedge ->
        val rim = if (wedge.place == WedgePlace.PLANNED) TRACK else OrreryTokens.Role.ok
        drawPath(cellPath(wedges.size, index, RIM, unit), rim)
        wedge.slices.forEach { slice ->
            drawPath(cellPath(wedges.size, index, ringOf(slice.phase), unit), sliceColour(slice.state, wedge.tone))
        }
    }
}

private fun sliceColour(state: SliceState, tone: WedgeTone): Color = when (state) {
    SliceState.PASSED -> OrreryTokens.Role.ok
    SliceState.NOW -> nowColour(tone)
    SliceState.AHEAD -> TRACK
}

// The colour of a now slice, by its wedge's tone: running yellow, waits on you peach, waits on a limit grey, ended short
// red, accepted teal and integrated green. A planned wedge has no now slice, so its colour is the track's.
private fun nowColour(tone: WedgeTone): Color = when (tone) {
    WedgeTone.PLAN -> TRACK
    WedgeTone.RUN -> OrreryTokens.Tone.run
    WedgeTone.MINE -> OrreryTokens.Tone.mine
    WedgeTone.LIMITED -> TRACK
    WedgeTone.BAD -> OrreryTokens.Tone.bad
    WedgeTone.ACC -> OrreryTokens.Tone.acc
    WedgeTone.OK -> OrreryTokens.Tone.ok
}

// The glowing cells as one path.
private fun glowPath(wedges: List<Wedge>, unit: Float): Path = Path().apply {
    glowsOf(wedges).forEach { (index, phase) -> addPath(cellPath(wedges.size, index, ringOf(phase), unit)) }
}

// Each requirement arc's stroke, from its start to its end in wedge units. Compose's arcs run from 3 o'clock, so an
// angle clockwise from 12 o'clock is 90 degrees less.
private fun DrawScope.drawArcStrokes(arcs: List<RequirementArc>, count: Int, unit: Float) {
    val radius = ARC_MID * unit
    arcs.forEach { arc ->
        val (from, to) = arcAnglesOf(arc, count)
        drawArc(
            color = if (arc.proven) OrreryTokens.Role.ok else OrreryTokens.Line.glyph,
            startAngle = from - 90f,
            sweepAngle = to - from,
            useCenter = false,
            topLeft = Offset(CENTRE.x * unit - radius, CENTRE.y * unit - radius),
            size = Size(2 * radius, 2 * radius),
            style = Stroke(width = (ARC_OUTER - ARC_INNER) * unit),
        )
    }
}

// Each requirement arc's id, beside the middle of its stroke.
private fun DrawScope.drawArcIds(measurer: TextMeasurer, arcs: List<RequirementArc>, count: Int, unit: Float) {
    arcs.forEach { arc ->
        val (from, to) = arcAnglesOf(arc, count)
        val at = point(ID_RADIUS, radiansOf((from + to) / 2))
        paintCentred(measurer, arc.id, OrreryTokens.Type.small, ARC_ID_SIZE, at.x, at.y, unit, OrreryTokens.Ink.three)
    }
}

// The angles, in the orrery's degrees, at which an arc starts and ends.
private fun arcAnglesOf(arc: RequirementArc, count: Int): Pair<Float, Float> {
    val origin = sliceStart(0, count)
    val span = sliceSpan(count)
    return Pair(origin + arc.start.toFloat() * span, origin + arc.end.toFloat() * span)
}

// The phase names in the start gap, each on its ring's radius at 12 o'clock.
private fun DrawScope.drawRingNames(measurer: TextMeasurer, unit: Float) {
    RING_NAMES.forEachIndexed { ring, name ->
        val at = point(ringRadius(ring), 0f)
        paintCentred(
            measurer,
            name.inLabelCase(),
            OrreryTokens.Type.label,
            RING_NAME_SIZE,
            at.x,
            at.y,
            unit,
            OrreryTokens.Ink.four,
        )
    }
}

// The glow's opacity. It breathes from the pulse's full to its quarter while animations run, and holds at full when
// they don't.
@Composable
private fun glowAlpha(): State<Float> {
    if (!animationsOn()) return remember { mutableStateOf(OrreryTokens.Motion.Loop.pulseFrom) }
    return rememberInfiniteTransition(label = "glow").animateFloat(
        initialValue = OrreryTokens.Motion.Loop.pulseFrom,
        targetValue = OrreryTokens.Motion.Loop.pulseTo,
        animationSpec = pulseSpec(),
        label = "glow",
    )
}

// The glow: the glowing cells filled and stroked in yellow with round joins, blurred, at [alpha].
private fun glowPaint(unit: Float, alpha: Float): Paint = Paint().apply {
    style = Paint.Style.FILL_AND_STROKE
    color = OrreryTokens.Role.live.copy(alpha = alpha).toArgb()
    strokeWidth = GLOW_STROKE * unit
    strokeJoin = Paint.Join.ROUND
    maskFilter = BlurMaskFilter(GLOW_BLUR * unit, BlurMaskFilter.Blur.NORMAL)
    isAntiAlias = true
}

private fun DrawScope.drawGlow(cells: Path, unit: Float, alpha: Float) {
    drawContext.canvas.nativeCanvas.drawPath(cells.asAndroidPath(), glowPaint(unit, alpha))
}

// Paints [text] in [face] centred on (x, y) units, both ways. The face sets the family and weight; its size is
// sizeUnits of the chart, so the text scales with it, and its line height is the text's own.
private fun DrawScope.paintCentred(
    measurer: TextMeasurer,
    text: String,
    face: TextStyle,
    sizeUnits: Float,
    x: Float,
    y: Float,
    unit: Float,
    colour: Color,
) {
    val layout = measurer.measure(
        text,
        face.copy(fontSize = (sizeUnits * unit).toSp(), lineHeight = TextUnit.Unspecified, color = colour),
    )
    drawText(layout, topLeft = Offset(x * unit - layout.size.width / 2f, y * unit - layout.size.height / 2f))
}

// Degrees as radians, for point().
private fun radiansOf(degrees: Float): Float = degrees * PI.toFloat() / 180f
