package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Most running step rows one epic shows; the rest fold into one "more" line. */
private const val MAX_STEP_ROWS = 4

/** The view boxes the round-6 glyph, gauge and contract mark are drawn in, and their strokes, in those units. */
private const val GLYPH_VIEW = 16f
private const val GAUGE_VIEW = 24f
private const val GLYPH_STROKE = 1.5f
private const val GAUGE_STROKE = 3f
private const val GAUGE_INNER_STROKE = 1.5f
private const val GAUGE_RADIUS = 9f

private const val STATE_WAITING = "waiting"
private const val STATE_BROKEN = "broken"
private const val UNSEEN_WORDS = "new since you looked"
private const val CONTRACT_WORDS = "contract"
private const val QUESTION_LEAD = "? "

/**
 * One conversation and what hangs under it, drawn as round 6 draws the inbox tree: its row, then each epic's and
 * contract's row in order. A tap on a row or a step calls [onOpen] with its key; a long press on a conversation's row or
 * an epic's row calls [onLongPress] with the row's [SheetRow]. A stand-in draws its words in place of a row.
 */
@Composable
internal fun ConvoRowItem(
    convo: Convo,
    now: Long,
    onOpen: (String) -> Unit,
    onLongPress: (SheetRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    val pane = convo.pane
    Column(modifier = modifier.fillMaxWidth()) {
        if (pane == null) {
            Text(
                convoLineOf(convo, now),
                modifier = Modifier.fillMaxWidth().testTag("standin:${convo.key}").padding(vertical = OrreryTokens.Space.eight),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
            )
        } else {
            ConvoLine(convo, pane, now, onOpen, onLongPress)
        }
        if (convo.hung.isNotEmpty()) {
            Column(modifier = Modifier.fillMaxWidth()) {
                for (hung in convo.hung) {
                    HungRow(hung, now, onOpen, onLongPress)
                }
            }
        }
    }
}

@Composable
private fun ConvoLine(convo: Convo, pane: Pane, now: Long, onOpen: (String) -> Unit, onLongPress: (SheetRow) -> Unit) {
    val unseen = pane.unread()
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("convo:${convo.key}")
            .combinedClickable(onClick = { onOpen(convo.key) }, onLongClick = { onLongPress(SheetRow.Conversation(pane)) })
            .padding(vertical = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.Top,
    ) {
        NewDot(unseen)
        StateGlyph(glyphOfRow(pane), Modifier.padding(top = OrreryTokens.Space.four))
        Column(modifier = Modifier.weight(1f)) {
            Text(
                pane.label,
                style = OrreryTokens.Type.body.copy(
                    fontWeight = if (unseen) OrreryTokens.Font.Weight.bold else OrreryTokens.Font.Weight.regular,
                ),
                color = if (unseen) OrreryTokens.Ink.one else OrreryTokens.Ink.two,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                convoLineOf(convo, now),
                style = OrreryTokens.Type.small,
                color = if (pane.state == STATE_WAITING) OrreryTokens.Role.attention else OrreryTokens.Ink.three,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (pane.changed != 0L) {
            Text(
                ageOf((now - pane.changed).coerceAtLeast(0L)),
                style = OrreryTokens.Type.small,
                color = if (unseen) OrreryTokens.Role.new else OrreryTokens.Ink.three,
            )
        }
    }
}

// A conversation's glyph: broken for a session that failed or a pane with no Claude session bound to it, else its state's.
private fun glyphOfRow(pane: Pane): String = if (pane.state == "failed" || pane.unbound) STATE_BROKEN else pane.state

// The new dot: drawn only for a row unseen since its owner last looked, else a gap of the same width.
@Composable
private fun NewDot(unseen: Boolean) {
    if (unseen) {
        Box(
            modifier = Modifier
                .padding(top = OrreryTokens.Space.four + OrreryTokens.Space.two)
                .size(OrreryTokens.Size.dot)
                .background(OrreryTokens.Role.new, CircleShape)
                .semantics { contentDescription = UNSEEN_WORDS },
        )
    } else {
        Spacer(modifier = Modifier.width(OrreryTokens.Size.dot))
    }
}

// A hung epic or contract: an epic's gauge or a contract's mark, its name and words, its blocker in attention, and for
// an epic its running steps. The count of a contract sits at the row's end, its version.
@Composable
private fun HungRow(hung: Hung, now: Long, onOpen: (String) -> Unit, onLongPress: (SheetRow) -> Unit) {
    val words = epicRowWords(hung, now)
    val gauge = words.gauge
    val contract = gauge == null
    val ended = hung is EpicNode && !hung.epic.live
    val line = if (contract) words.words else listOf(words.words, words.count).filter { it.isNotEmpty() }.joinToString(" · ")
    // Only an epic has a sheet; a contract has no archive to offer.
    val onLongClick: (() -> Unit)? = (hung as? EpicNode)?.let { node -> { onLongPress(SheetRow.Epic(node.epic)) } }
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .testTag("hung:${hung.key}")
                .combinedClickable(onClick = { onOpen(hung.key) }, onLongClick = onLongClick)
                .padding(vertical = OrreryTokens.Space.four),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalAlignment = Alignment.Top,
        ) {
            if (hung is EpicNode && gauge != null) {
                GaugeMark(hung.epic.id, gauge)
            } else {
                ContractMark(hung.key)
            }
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    words.name,
                    style = OrreryTokens.Type.body.copy(
                        fontWeight = if (ended) OrreryTokens.Font.Weight.regular else OrreryTokens.Font.Weight.bold,
                    ),
                    color = if (ended) OrreryTokens.Ink.two else OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(line, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (words.blocker.isNotEmpty()) {
                    Text(
                        QUESTION_LEAD + words.blocker,
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Role.attention,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            if (contract && words.count.isNotEmpty()) {
                Text(words.count, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            }
        }
        if (hung is EpicNode) {
            EpicSteps(hung.epic, onOpen)
        }
    }
}

// An epic's running steps, at most MAX_STEP_ROWS, then one line for the rest.
@Composable
private fun EpicSteps(epic: EpicRow, onOpen: (String) -> Unit) {
    val shown = epic.runningSteps.take(MAX_STEP_ROWS)
    val more = epic.runningSteps.size - shown.size
    for (step in shown) {
        StepLine(step, onOpen)
    }
    if (more > 0) {
        Text(
            "$more more",
            modifier = Modifier.padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
        )
    }
}

@Composable
private fun StepLine(step: StepRow, onOpen: (String) -> Unit) {
    val key = "step:${step.id}"
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(key)
            .clickable { onOpen(key) }
            .padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.Top,
    ) {
        StateGlyph(if (step.held) "held" else "working", Modifier.padding(top = OrreryTokens.Space.two))
        Column(modifier = Modifier.weight(1f)) {
            Text(
                step.task,
                style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                color = OrreryTokens.Ink.one,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                step.doing,
                style = OrreryTokens.Type.small,
                color = if (step.held) OrreryTokens.Role.attention else OrreryTokens.Ink.three,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

// A state glyph in the state's colour, named by the state's words; an unknown state draws idle.
@Composable
internal fun StateGlyph(state: String, modifier: Modifier = Modifier) {
    val color = glyphColorOf(state)
    val word = glyphWordOf(state)
    Canvas(modifier = modifier.size(OrreryTokens.Size.glyph).semantics { contentDescription = word }) {
        drawGlyph(state, color)
    }
}

private fun glyphColorOf(state: String): Color = when (state) {
    "working" -> OrreryTokens.State.working
    "waiting" -> OrreryTokens.State.waiting
    "held" -> OrreryTokens.State.held
    "limited" -> OrreryTokens.State.limited
    "done" -> OrreryTokens.State.done
    "interrupted" -> OrreryTokens.State.interrupted
    "interrupting" -> OrreryTokens.State.interrupting
    "failed", STATE_BROKEN -> OrreryTokens.State.failed
    "stopped" -> OrreryTokens.State.stopped
    else -> OrreryTokens.State.idle
}

internal fun glyphWordOf(state: String): String = when (state) {
    "working" -> "working"
    "waiting" -> "waiting on you"
    "held" -> "held"
    "limited" -> "waiting on a limit"
    "done" -> "done"
    "interrupted" -> "cut off"
    "interrupting" -> "interrupting"
    "failed" -> "failed"
    STATE_BROKEN -> STATE_BROKEN
    "stopped" -> "stopped"
    else -> "idle"
}

// A glyph's drawing in its 16-unit box: a ring with a mark inside it, except working (a track with an arc), waiting (a
// solid disc with a question mark) and stopped (a ring with a square).
private fun DrawScope.drawGlyph(state: String, color: Color) {
    val stroke = Stroke(width = glyphPx(GLYPH_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
    val centre = Offset(glyphPx(8f), glyphPx(8f))
    val radius = glyphPx(6.25f)
    fun ring(ringColor: Color) = drawCircle(ringColor, radius, centre, style = stroke)
    when (state) {
        "working" -> {
            ring(OrreryTokens.Line.firm)
            drawArc(
                color = color,
                startAngle = -90f,
                sweepAngle = 90f,
                useCenter = false,
                topLeft = Offset(centre.x - radius, centre.y - radius),
                size = Size(radius * 2, radius * 2),
                style = stroke,
            )
        }
        "waiting" -> {
            drawCircle(color, glyphPx(7f), centre)
            val hook = Path().apply {
                moveTo(glyphPx(6.1f), glyphPx(6.3f))
                cubicTo(glyphPx(6.4f), glyphPx(4.6f), glyphPx(9.6f), glyphPx(4.6f), glyphPx(9.6f), glyphPx(6.4f))
                cubicTo(glyphPx(9.6f), glyphPx(7.6f), glyphPx(8f), glyphPx(7.4f), glyphPx(8f), glyphPx(8.9f))
            }
            drawPath(hook, OrreryTokens.Ink.onAccent, style = stroke)
            drawCircle(OrreryTokens.Ink.onAccent, glyphPx(0.9f), Offset(glyphPx(8f), glyphPx(11.6f)))
        }
        "held", "limited" -> {
            ring(color)
            glyphLine(color, 6.5f, 5.5f, 6.5f, 10.5f, stroke.width)
            glyphLine(color, 9.5f, 5.5f, 9.5f, 10.5f, stroke.width)
        }
        "done" -> {
            ring(color)
            drawPath(
                Path().apply {
                    moveTo(glyphPx(5f), glyphPx(8.2f))
                    lineTo(glyphPx(7.1f), glyphPx(10.3f))
                    lineTo(glyphPx(11.1f), glyphPx(5.9f))
                },
                color,
                style = stroke,
            )
        }
        "interrupted", "interrupting" -> {
            ring(color)
            glyphLine(color, 8f, 4.6f, 8f, 8.8f, stroke.width)
            glyphLine(color, 8f, 11.1f, 8f, 11.3f, stroke.width)
        }
        "failed" -> {
            ring(color)
            glyphLine(color, 5.8f, 5.8f, 10.2f, 10.2f, stroke.width)
            glyphLine(color, 10.2f, 5.8f, 5.8f, 10.2f, stroke.width)
        }
        STATE_BROKEN -> {
            // The ring is open at its top, and a jagged line runs across the inside where a crack would.
            drawArc(
                color = color,
                startAngle = -80f,
                sweepAngle = 320f,
                useCenter = false,
                topLeft = Offset(centre.x - radius, centre.y - radius),
                size = Size(radius * 2, radius * 2),
                style = stroke,
            )
            drawPath(
                Path().apply {
                    moveTo(glyphPx(5f), glyphPx(8f))
                    lineTo(glyphPx(6.5f), glyphPx(6.2f))
                    lineTo(glyphPx(7.9f), glyphPx(9.8f))
                    lineTo(glyphPx(9.4f), glyphPx(6.2f))
                    lineTo(glyphPx(11f), glyphPx(8f))
                },
                color,
                style = stroke,
            )
        }
        "stopped" -> {
            ring(color)
            drawRect(color, topLeft = Offset(glyphPx(5.75f), glyphPx(5.75f)), size = Size(glyphPx(4.5f), glyphPx(4.5f)))
        }
        else -> ring(color)
    }
}

private fun DrawScope.glyphPx(view: Float): Float = view * size.width / GLYPH_VIEW

private fun DrawScope.glyphLine(color: Color, x1: Float, y1: Float, x2: Float, y2: Float, width: Float) {
    drawLine(
        color,
        Offset(glyphPx(x1), glyphPx(y1)),
        Offset(glyphPx(x2), glyphPx(y2)),
        strokeWidth = width,
        cap = StrokeCap.Round,
    )
}

// An epic's gauge: a planned ring in the line colour (dashed while it has no tasks), its done share as an arc in the
// tone, and the inner mark in the tone.
@Composable
internal fun GaugeMark(epicId: String, share: GaugeShare) {
    val tone = toneColorOf(share.tone)
    val label = if (share.total > 0) "${share.done} of ${share.total} tasks done" else "no tasks yet"
    Canvas(
        modifier = Modifier
            .size(OrreryTokens.Size.gauge)
            .testTag("gauge:$epicId")
            .semantics { contentDescription = label },
    ) {
        drawGauge(share, tone)
    }
}

private fun DrawScope.drawGauge(share: GaugeShare, tone: Color) {
    val centre = Offset(size.width / 2f, size.height / 2f)
    val radius = gaugePx(GAUGE_RADIUS)
    val planned = if (share.total > 0) {
        Stroke(width = gaugePx(GAUGE_STROKE))
    } else {
        Stroke(
            width = gaugePx(GAUGE_STROKE),
            pathEffect = PathEffect.dashPathEffect(floatArrayOf(gaugePx(2f), gaugePx(3f))),
        )
    }
    drawCircle(OrreryTokens.Line.firm, radius, centre, style = planned)
    if (share.total > 0 && share.done > 0) {
        drawArc(
            color = tone,
            startAngle = -90f,
            sweepAngle = 360f * share.done / share.total,
            useCenter = false,
            topLeft = Offset(centre.x - radius, centre.y - radius),
            size = Size(radius * 2, radius * 2),
            style = Stroke(width = gaugePx(GAUGE_STROKE)),
        )
    }
    val inner = Stroke(width = gaugePx(GAUGE_INNER_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
    when (share.inner) {
        GaugeInner.CHECK -> drawPath(
            Path().apply {
                moveTo(gaugePx(8.6f), gaugePx(12.3f))
                lineTo(gaugePx(10.9f), gaugePx(14.6f))
                lineTo(gaugePx(15.3f), gaugePx(9.7f))
            },
            tone,
            style = inner,
        )
        GaugeInner.DASH -> drawLine(
            tone,
            Offset(gaugePx(9f), centre.y),
            Offset(gaugePx(15f), centre.y),
            strokeWidth = inner.width,
            cap = StrokeCap.Round,
        )
        GaugeInner.DOT -> drawCircle(tone, gaugePx(3f), centre)
        null -> Unit
    }
}

private fun DrawScope.gaugePx(view: Float): Float = view * size.width / GAUGE_VIEW

private fun toneColorOf(tone: GaugeTone): Color = when (tone) {
    GaugeTone.NONE -> OrreryTokens.Ink.three
    GaugeTone.LIVE -> OrreryTokens.Role.live
    GaugeTone.ENGINE -> OrreryTokens.Role.engine
    GaugeTone.ATTENTION -> OrreryTokens.Role.attention
    GaugeTone.OK -> OrreryTokens.Role.ok
}

// A contract's mark: a sheet of paper in attention, with three lines of text.
@Composable
private fun ContractMark(key: String) {
    Canvas(
        modifier = Modifier
            .size(OrreryTokens.Size.gauge)
            .testTag("mark:$key")
            .semantics { contentDescription = CONTRACT_WORDS },
    ) {
        val stroke = Stroke(width = gaugePx(GAUGE_INNER_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawPath(
            Path().apply {
                moveTo(gaugePx(7f), gaugePx(4f))
                lineTo(gaugePx(14f), gaugePx(4f))
                lineTo(gaugePx(17f), gaugePx(7f))
                lineTo(gaugePx(17f), gaugePx(20f))
                lineTo(gaugePx(7f), gaugePx(20f))
                close()
            },
            OrreryTokens.Role.attention,
            style = stroke,
        )
        for (y in listOf(10f, 13f, 16f)) {
            drawLine(
                OrreryTokens.Role.attention,
                Offset(gaugePx(10f), gaugePx(y)),
                Offset(gaugePx(14f), gaugePx(y)),
                strokeWidth = stroke.width,
                cap = StrokeCap.Round,
            )
        }
    }
}

// The age of a stamp in seconds: "30s", "12m", "3h", "2d".
private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}
