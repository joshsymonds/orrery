package io.github.joshsymonds.orrery.ui

import android.graphics.BlurMaskFilter
import android.graphics.Paint
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import io.github.joshsymonds.orrery.ui.theme.Mocha
import io.github.joshsymonds.orrery.ui.theme.OrreryMono
import io.github.joshsymonds.orrery.ui.theme.animationsOn
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

// The glow breathes between GLOW_LOW and GLOW_HIGH, GLOW_MS each way, and holds at GLOW_STILL when animations are off.
private const val GLOW_HIGH = 0.8f
private const val GLOW_LOW = 0.2f
private const val GLOW_STILL = 0.5f
private const val GLOW_MS = 900
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

// Unreached cells are Surface1 at 60% over the ground.
private val TRACK: Color = lerp(Mocha.Base, Mocha.Surface1, 0.6f)

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
        drawRect(Mocha.Base)
        translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
            drawCells(wedges, unit)
            drawGlow(glowPath(wedges, unit), unit, glow.value)
            drawArcs(measurer, arcs, wedges.size, unit)
            drawRingNames(measurer, unit)
            paintCentred(measurer, readout, COUNT_SIZE, CENTRE.x, CENTRE.y, unit, Mocha.Text, bold = true)
            paintCentred(measurer, "integrated", COUNT_OF_SIZE, CENTRE.x, CENTRE.y + COUNT_OF_DROP, unit, Mocha.Overlay0)
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
    WedgeTone.BAD -> "ended short"
    WedgeTone.ACC -> "accepted"
    WedgeTone.OK -> "integrated"
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
        val rim = if (wedge.place == WedgePlace.PLANNED) TRACK else Mocha.Green
        drawPath(cellPath(wedges.size, index, RIM, unit), rim)
        wedge.slices.forEach { slice ->
            drawPath(cellPath(wedges.size, index, ringOf(slice.phase), unit), sliceColour(slice.state))
        }
    }
}

private fun sliceColour(state: SliceState): Color = when (state) {
    SliceState.PASSED -> Mocha.Green
    SliceState.NOW -> Mocha.Yellow
    SliceState.AHEAD -> TRACK
}

// The glowing cells as one path.
private fun glowPath(wedges: List<Wedge>, unit: Float): Path = Path().apply {
    glowsOf(wedges).forEach { (index, phase) -> addPath(cellPath(wedges.size, index, ringOf(phase), unit)) }
}

// Each requirement arc, from its start to its end in wedge units, with its id beside it. Compose's arcs run from 3 o'clock,
// so an angle clockwise from 12 o'clock is 90 degrees less.
private fun DrawScope.drawArcs(measurer: TextMeasurer, arcs: List<RequirementArc>, count: Int, unit: Float) {
    val origin = sliceStart(0, count)
    val span = sliceSpan(count)
    val radius = ARC_MID * unit
    arcs.forEach { arc ->
        val from = origin + arc.start.toFloat() * span
        val to = origin + arc.end.toFloat() * span
        drawArc(
            color = if (arc.proven) Mocha.Green else Mocha.Overlay0,
            startAngle = from - 90f,
            sweepAngle = to - from,
            useCenter = false,
            topLeft = Offset(CENTRE.x * unit - radius, CENTRE.y * unit - radius),
            size = Size(2 * radius, 2 * radius),
            style = Stroke(width = (ARC_OUTER - ARC_INNER) * unit),
        )
        val at = point(ID_RADIUS, radiansOf((from + to) / 2))
        paintCentred(measurer, arc.id, ARC_ID_SIZE, at.x, at.y, unit, Mocha.Overlay1)
    }
}

// The phase names in the start gap, each on its ring's radius at 12 o'clock.
private fun DrawScope.drawRingNames(measurer: TextMeasurer, unit: Float) {
    RING_NAMES.forEachIndexed { ring, name ->
        val at = point(ringRadius(ring), 0f)
        paintCentred(measurer, name, RING_NAME_SIZE, at.x, at.y, unit, Mocha.Overlay0, bold = true)
    }
}

// The glow's opacity. It breathes between GLOW_HIGH and GLOW_LOW while animations run, and holds at GLOW_STILL when they don't.
@Composable
private fun glowAlpha(): State<Float> {
    if (!animationsOn()) return remember { mutableStateOf(GLOW_STILL) }
    return rememberInfiniteTransition(label = "glow").animateFloat(
        initialValue = GLOW_HIGH,
        targetValue = GLOW_LOW,
        animationSpec = infiniteRepeatable(tween(GLOW_MS, easing = FastOutSlowInEasing), RepeatMode.Reverse),
        label = "glow",
    )
}

// The glow: the glowing cells filled and stroked in yellow with round joins, blurred, at [alpha].
private fun glowPaint(unit: Float, alpha: Float): Paint = Paint().apply {
    style = Paint.Style.FILL_AND_STROKE
    color = Mocha.Yellow.copy(alpha = alpha).toArgb()
    strokeWidth = GLOW_STROKE * unit
    strokeJoin = Paint.Join.ROUND
    maskFilter = BlurMaskFilter(GLOW_BLUR * unit, BlurMaskFilter.Blur.NORMAL)
    isAntiAlias = true
}

private fun DrawScope.drawGlow(cells: Path, unit: Float, alpha: Float) {
    drawContext.canvas.nativeCanvas.drawPath(cells.asAndroidPath(), glowPaint(unit, alpha))
}

// Paints [text] centred on (x, y) units, both ways.
private fun DrawScope.paintCentred(
    measurer: TextMeasurer,
    text: String,
    sizeUnits: Float,
    x: Float,
    y: Float,
    unit: Float,
    colour: Color,
    bold: Boolean = false,
) {
    val layout = measurer.measure(
        text,
        TextStyle(
            fontFamily = OrreryMono,
            fontSize = (sizeUnits * unit).toSp(),
            fontWeight = if (bold) FontWeight.Bold else FontWeight.Normal,
            color = colour,
        ),
    )
    drawText(layout, topLeft = Offset(x * unit - layout.size.width / 2f, y * unit - layout.size.height / 2f))
}

// Degrees as radians, for point().
private fun radiansOf(degrees: Float): Float = degrees * PI.toFloat() / 180f
