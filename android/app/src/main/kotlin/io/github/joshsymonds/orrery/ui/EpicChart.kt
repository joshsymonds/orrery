package io.github.joshsymonds.orrery.ui

import android.graphics.BlurMaskFilter
import android.graphics.Paint
import android.provider.Settings
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.text.InlineTextContent
import androidx.compose.foundation.text.appendInlineContent
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.asAndroidPath
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.Placeholder
import androidx.compose.ui.text.PlaceholderVerticalAlign
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.EpicSliceRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import io.github.joshsymonds.orrery.ui.theme.OrreryMono
import kotlin.math.PI
import kotlin.math.min

// The chart's box is OrbitGeometry's square, which point(0, 0) is the centre of: the canvas centres it and scales it to fit.
private val CENTRE = point(0f, 0f)
private val BOX = 2 * CENTRE.x

// The wash under the cursor's slice shows only on a chart of WASH_MIN_SLICES or more: a narrow slice needs it to be found.
private const val WASH_MIN_SLICES = 18
private const val WASH_MIX = 0.16f
private const val TRACK_MIX = 0.6f

// The glow breathes between GLOW_LOW and GLOW_HIGH, GLOW_MS each way, and holds at GLOW_STILL when animations are off.
private const val GLOW_HIGH = 0.8f
private const val GLOW_LOW = 0.2f
private const val GLOW_STILL = 0.5f
private const val GLOW_MS = 900
private const val GLOW_STROKE = 4f
private const val GLOW_BLUR = 5.5f

private const val OUTLINE_WIDTH = 1.5f
private const val BEZEL_TICK_WIDTH = 1f
private const val LETTER_SPACING = 15f

// Text sizes, in units.
private const val RING_LETTER_SIZE = 8.5f
private const val BEZEL_NUMBER_SIZE = 8.5f
private const val POSITION_SIZE = 9f
private const val COUNT_SIZE = 34f
private const val CAPTION_SIZE = 11f
private const val TALLY_SIZE = 10.5f
private const val LETTER_SIZE = 11f

// The ring key: each ring's letter and name, red test first. OrbitModel's ring letters are private, so the chart keeps its own.
private val RING_KEY = listOf("R" to "red test", "W" to "working", "T" to "tests pass", "C" to "checks", "G" to "gate")
private val COLOUR_KEY = listOf("passed" to Mocha.Green, "now" to Mocha.Yellow, "held" to Mocha.Peach, "errored" to Mocha.Red)
private const val KEY_GAP = "  "
private val KEY_STYLE = TextStyle(fontFamily = OrreryMono, fontSize = 11.sp, color = Mocha.Overlay0, textAlign = TextAlign.Center)

private val RING_KEY_TEXT: AnnotatedString = buildAnnotatedString {
    RING_KEY.forEachIndexed { index, (letter, name) ->
        if (index > 0) append(KEY_GAP)
        withStyle(SpanStyle(color = Mocha.Overlay1, fontWeight = FontWeight.Bold)) { append(letter) }
        append(" $name")
    }
}

private val COLOUR_KEY_TEXT: AnnotatedString = buildAnnotatedString {
    COLOUR_KEY.forEachIndexed { index, (name, _) ->
        if (index > 0) append(KEY_GAP)
        appendInlineContent(id = name, alternateText = name)
        append(" $name")
    }
}

// The empty state's five orbits, in dp about the centre of a box EMPTY_SIZE square.
private val EMPTY_SIZE = 132.dp
private val EMPTY_ORBITS = listOf(62, 51, 40, 29, 18)

/**
 * The epic's Orbits chart: a slice per task, clockwise from 12 o'clock, and five rings of cells with red test outermost.
 * The centre reads the epic, or the selected task. [selected] is the slice the screen holds. A tap or drag on the rings
 * reports the slice under the finger through [onSelect]; a tap on the selected slice or on the centre reports null. With
 * no slices the chart is its empty state. [taskCount] is how many tasks the epic's record lists.
 */
@Composable
fun EpicChart(
    slices: List<EpicSliceRow>,
    taskCount: Int,
    selected: Int?,
    onSelect: (Int?) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (slices.isEmpty()) {
        EmptyChart(modifier)
        return
    }
    val count = slices.size
    val chosen = selected?.takeIf { it in slices.indices }
    val currentSelected by rememberUpdatedState(chosen)
    val currentOnSelect by rememberUpdatedState(onSelect)
    val measurer = rememberTextMeasurer()
    val glow = glowAlpha()
    var canvasSize by remember { mutableStateOf(IntSize.Zero) }
    val unit = unitOf(canvasSize)
    val cells = remember(slices, unit) { cellPaths(slices, unit) }
    val task = remember(slices, chosen) { chosen?.let { taskReadout(slices, it) } }
    val epic = remember(slices, taskCount) { epicReadout(slices, taskCount) }
    val description = contentDescription(slices)
    val selectedTask = chosen?.let { slices[it].task }

    Column(modifier = modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        Canvas(
            modifier = Modifier
                .weight(1f)
                .fillMaxWidth()
                .onSizeChanged { canvasSize = it }
                .pointerInput(count, canvasSize) {
                    detectTapGestures(
                        onTap = { position ->
                            when (val hit = hitAt(position, canvasSize, count)) {
                                is OrbitHit.Slice -> currentOnSelect(if (hit.index == currentSelected) null else hit.index)
                                OrbitHit.Centre -> currentOnSelect(null)
                                null -> Unit
                            }
                        },
                    )
                }
                .pointerInput(count, canvasSize) {
                    // A drag that starts on the rings scrubs: each slice it enters is selected, until the finger lifts.
                    var scrubbing = false
                    var last: Int? = null
                    detectDragGestures(
                        onDragStart = { start ->
                            scrubbing = hitAt(start, canvasSize, count) is OrbitHit.Slice
                            last = currentSelected
                        },
                        onDrag = { change, _ ->
                            val hit = hitAt(change.position, canvasSize, count)
                            if (scrubbing && hit is OrbitHit.Slice && hit.index != last) {
                                last = hit.index
                                currentOnSelect(hit.index)
                            }
                        },
                    )
                }
                .testTag("epic-chart")
                .semantics {
                    contentDescription = description
                    selectedTask?.let { stateDescription = it }
                },
        ) {
            if (unit == 0f) return@Canvas
            drawRect(Mocha.Base)
            translate(left = size.width / 2 - CENTRE.x * unit, top = size.height / 2 - CENTRE.y * unit) {
                if (chosen != null && count >= WASH_MIN_SLICES) {
                    drawPath(cursorWedge(count, chosen, unit), lerp(Mocha.Base, Mocha.Mauve, WASH_MIX))
                }
                cells.forEach { (tone, path) -> drawPath(path, cellFill(tone)) }
                drawGlow(cells.getValue(Tone.NOW), unit, glow.value)
                drawRingLetters(measurer, unit)
                drawBezel(measurer, count, unit)
                if (chosen != null) drawCursor(count, chosen, unit)
                if (task != null) drawTaskReadout(measurer, task, unit) else drawEpicReadout(measurer, epic, unit)
            }
        }
        Text(RING_KEY_TEXT, style = KEY_STYLE, modifier = Modifier.fillMaxWidth().padding(top = 2.dp))
        ColourKey()
    }
}

@Composable
private fun EmptyChart(modifier: Modifier) {
    Column(
        modifier = modifier.testTag("epic-chart-none"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Canvas(modifier = Modifier.size(EMPTY_SIZE)) {
            val stroke = Stroke(
                width = 1.5.dp.toPx(),
                pathEffect = PathEffect.dashPathEffect(floatArrayOf(2.dp.toPx(), 5.dp.toPx())),
            )
            EMPTY_ORBITS.forEach { radius -> drawCircle(Mocha.Surface1, radius.dp.toPx(), center, style = stroke) }
        }
        Spacer(Modifier.height(4.dp))
        Text("No orrery-tracked steps", style = MaterialTheme.typography.bodySmall, color = Mocha.Overlay1)
    }
}

@Composable
private fun ColourKey() {
    val swatches = COLOUR_KEY.associate { (name, colour) ->
        name to InlineTextContent(
            Placeholder(width = 9.sp, height = 5.sp, placeholderVerticalAlign = PlaceholderVerticalAlign.Center),
        ) {
            Box(Modifier.fillMaxSize().background(colour))
        }
    }
    Text(COLOUR_KEY_TEXT, style = KEY_STYLE, inlineContent = swatches, modifier = Modifier.fillMaxWidth())
}

// The glow's opacity. It breathes between GLOW_HIGH and GLOW_LOW unless the system's animator duration scale is 0, when it
// holds at GLOW_STILL.
@Composable
private fun glowAlpha(): State<Float> {
    val context = LocalContext.current
    val animated = remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    }
    if (!animated) return remember { mutableStateOf(GLOW_STILL) }
    return rememberInfiniteTransition(label = "glow").animateFloat(
        initialValue = GLOW_HIGH,
        targetValue = GLOW_LOW,
        animationSpec = infiniteRepeatable(tween(GLOW_MS, easing = FastOutSlowInEasing), RepeatMode.Reverse),
        label = "glow",
    )
}

// The chart's units to pixels: the box fills the smaller side of the canvas.
private fun unitOf(size: IntSize): Float = min(size.width, size.height) / BOX

// What a pointer at [position] on a canvas of [size] lands on. The chart is centred on the canvas.
private fun hitAt(position: Offset, size: IntSize, count: Int): OrbitHit? {
    val centre = Offset(size.width / 2f, size.height / 2f)
    return orbitHit((position - centre) / unitOf(size), count)
}

// Every cell of the chart as one Path per tone, in pixels at unit per unit, so each colour is drawn once.
private fun cellPaths(slices: List<EpicSliceRow>, unit: Float): Map<Tone, Path> {
    val paths = mapOf(
        Tone.PASSED to Path(),
        Tone.NOW to Path(),
        Tone.HELD to Path(),
        Tone.ERRORED to Path(),
        Tone.TRACK to Path(),
    )
    slices.forEachIndexed { index, slice ->
        cellTones(slice).forEachIndexed { ring, tone ->
            paths.getValue(tone).addPath(cellPath(slices.size, index, ring, unit))
        }
    }
    return paths
}

// A cell's fill: its tone's colour, with the track at TRACK_MIX over the ground.
private fun cellFill(tone: Tone): Color =
    if (tone == Tone.TRACK) lerp(Mocha.Base, Mocha.Surface1, TRACK_MIX) else toneColour(tone)

// A tone's colour as text and as a cell. The track is Surface1 here; cellFill mixes it into the ground.
private fun toneColour(tone: Tone): Color = when (tone) {
    Tone.PASSED -> Mocha.Green
    Tone.NOW -> Mocha.Yellow
    Tone.HELD -> Mocha.Peach
    Tone.ERRORED -> Mocha.Red
    Tone.TRACK -> Mocha.Surface1
    Tone.PLAIN -> Mocha.Text
    Tone.DIM -> Mocha.Overlay1
}

// The glow: the now cells once more, filled and stroked in yellow with round joins, blurred, at [alpha].
private fun glowPaint(unit: Float, alpha: Float): Paint = Paint().apply {
    style = Paint.Style.FILL_AND_STROKE
    color = Mocha.Yellow.copy(alpha = alpha).toArgb()
    strokeWidth = GLOW_STROKE * unit
    strokeJoin = Paint.Join.ROUND
    maskFilter = BlurMaskFilter(GLOW_BLUR * unit, BlurMaskFilter.Blur.NORMAL)
    isAntiAlias = true
}

private fun DrawScope.drawGlow(now: Path, unit: Float, alpha: Float) {
    drawContext.canvas.nativeCanvas.drawPath(now.asAndroidPath(), glowPaint(unit, alpha))
}

private fun DrawScope.drawRingLetters(measurer: TextMeasurer, unit: Float) {
    RING_KEY.forEachIndexed { ring, (letter, _) ->
        val at = point(ringRadius(ring), 0f)
        paintCentred(measurer, inkOf(letter, Mocha.Overlay0, bold = true), RING_LETTER_SIZE, at.x, at.y, unit)
    }
}

private fun DrawScope.drawBezel(measurer: TextMeasurer, count: Int, unit: Float) {
    for (tick in bezelTicks(count)) {
        val radians = tick.angle * PI.toFloat() / 180f
        drawLine(
            color = if (tick.numberCentre != null) Mocha.Overlay0 else Mocha.Surface1,
            start = point(tick.inner, radians) * unit,
            end = point(tick.outer, radians) * unit,
            strokeWidth = BEZEL_TICK_WIDTH * unit,
        )
        tick.numberCentre?.let { at ->
            paintCentred(measurer, inkOf(tick.slice.toString(), Mocha.Overlay0), BEZEL_NUMBER_SIZE, at.x, at.y, unit)
        }
    }
}

private fun DrawScope.drawCursor(count: Int, slice: Int, unit: Float) {
    drawPath(cursorWedge(count, slice, unit), Mocha.Mauve, style = Stroke(width = OUTLINE_WIDTH * unit, join = StrokeJoin.Round))
    drawPath(cursorHand(count, slice, unit), Mocha.Mauve)
}

private fun DrawScope.drawEpicReadout(measurer: TextMeasurer, readout: EpicReadout, unit: Float) {
    val allThree = readout.tallies.size == 2
    val countAt = if (allThree) -6f else -2f
    val captionAt = if (allThree) 12f else 17f
    val tallyAt = if (allThree) listOf(31f, 46f) else listOf(38f)
    paintOnBaseline(measurer, lineOf(readout.count, boldFirst = true), COUNT_SIZE, CENTRE.x, CENTRE.y + countAt, unit)
    paintOnBaseline(measurer, lineOf(listOf(readout.caption)), CAPTION_SIZE, CENTRE.x, CENTRE.y + captionAt, unit)
    readout.tallies.forEachIndexed { line, spans ->
        paintOnBaseline(measurer, lineOf(spans), TALLY_SIZE, CENTRE.x, CENTRE.y + tallyAt[line], unit)
    }
}

private fun DrawScope.drawTaskReadout(measurer: TextMeasurer, readout: TaskReadout, unit: Float) {
    paintOnBaseline(
        measurer,
        lineOf(listOf(readout.position), colour = Mocha.Overlay0),
        POSITION_SIZE,
        CENTRE.x,
        CENTRE.y - 30f,
        unit,
    )
    paintOnBaseline(
        measurer,
        lineOf(listOf(readout.label), boldFirst = true),
        readout.labelSize.toFloat(),
        CENTRE.x,
        CENTRE.y - 5f,
        unit,
    )
    paintOnBaseline(measurer, lineOf(listOf(readout.status)), TALLY_SIZE, CENTRE.x, CENTRE.y + 14f, unit)
    readout.letters.forEachIndexed { ring, letter ->
        paintOnBaseline(
            measurer,
            lineOf(listOf(letter), boldFirst = true),
            LETTER_SIZE,
            CENTRE.x + (ring - 2) * LETTER_SPACING,
            CENTRE.y + 37f,
            unit,
        )
    }
}

// One line of spans, each in its tone's colour unless [colour] takes them all; [boldFirst] sets the first span bold.
private fun lineOf(spans: List<Span>, boldFirst: Boolean = false, colour: Color? = null): AnnotatedString =
    buildAnnotatedString {
        spans.forEachIndexed { index, span ->
            val weight = if (boldFirst && index == 0) FontWeight.Bold else FontWeight.Normal
            withStyle(SpanStyle(color = colour ?: toneColour(span.tone), fontWeight = weight)) { append(span.text) }
        }
    }

// Text in one colour, for the chart's own labels.
private fun inkOf(text: String, colour: Color, bold: Boolean = false): AnnotatedString =
    lineOf(listOf(Span(text, Tone.PLAIN)), boldFirst = bold, colour = colour)

private fun DrawScope.layoutOf(measurer: TextMeasurer, text: AnnotatedString, sizeUnits: Float, unit: Float): TextLayoutResult =
    measurer.measure(text, TextStyle(fontFamily = OrreryMono, fontSize = (sizeUnits * unit).toSp()))

// Paints [text] centred on (x, y) units, both ways.
private fun DrawScope.paintCentred(
    measurer: TextMeasurer,
    text: AnnotatedString,
    sizeUnits: Float,
    x: Float,
    y: Float,
    unit: Float,
) {
    val layout = layoutOf(measurer, text, sizeUnits, unit)
    drawText(layout, topLeft = Offset(x * unit - layout.size.width / 2f, y * unit - layout.size.height / 2f))
}

// Paints [text] centred across x, with its baseline on y, both in units.
private fun DrawScope.paintOnBaseline(
    measurer: TextMeasurer,
    text: AnnotatedString,
    sizeUnits: Float,
    x: Float,
    y: Float,
    unit: Float,
) {
    val layout = layoutOf(measurer, text, sizeUnits, unit)
    drawText(layout, topLeft = Offset(x * unit - layout.size.width / 2f, y * unit - layout.firstBaseline))
}
