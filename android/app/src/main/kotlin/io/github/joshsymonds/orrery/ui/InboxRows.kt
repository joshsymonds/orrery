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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryMono
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** Most running step rows one epic shows; the rest fold into one "more" line. */
private const val MAX_STEP_ROWS = 4

/** The view boxes the round-6 glyph and the contract mark are drawn in, and their strokes, in those units. */
private const val GLYPH_VIEW = 16f
private const val CONTRACT_VIEW = 24f
private const val GLYPH_STROKE = 1.5f
private const val CONTRACT_STROKE = 1.5f

private const val STATE_WAITING = "waiting"
private const val STATE_BROKEN = "broken"
private const val UNSEEN_WORDS = "new since you looked"
private const val CONTRACT_WORDS = "contract"

/**
 * One conversation and what hangs under it, drawn as round 8 draws the inbox tree (D13): its row, then each epic's and
 * contract's row in order. A conversation with anything of its own is a block on the side's ground, headed by its row,
 * with its design blocks in served order, then its loose hung rows, then its fold line with the ended epics it folds,
 * which draw under the line while [foldOpen] holds its key. A tap on the fold line calls [onFold]. A row with no pane is
 * an own row: it has no conversation line, and its things draw in the same block. A tap on a row or a step calls
 * [onOpen] with its key; a long press on a conversation's row or an epic's row calls [onLongPress] with the row's
 * [SheetRow]. [moving] marks the conversation's row as moving to a new place, which is what its line carries.
 */
@Composable
internal fun ConvoRowItem(
    convo: Convo,
    now: Long,
    foldOpen: Boolean,
    onFold: () -> Unit,
    onOpen: (String) -> Unit,
    onLongPress: (SheetRow) -> Unit,
    modifier: Modifier = Modifier,
    moving: Boolean = false,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        if (convo.hung.isEmpty() && convo.designs.isEmpty() && convo.folded.isEmpty()) {
            ConvoHead(convo, now, onOpen, onLongPress, moving)
        } else {
            Column(
                modifier = Modifier
                    .testTag("convo-block:${convo.key}")
                    .fillMaxWidth()
                    .padding(vertical = OrreryTokens.Space.four)
                    .background(OrreryTokens.Surface.side, RoundedCornerShape(OrreryTokens.Radius.three))
                    .padding(horizontal = OrreryTokens.Space.eight, vertical = OrreryTokens.Space.four),
            ) {
                ConvoHead(convo, now, onOpen, onLongPress, moving)
                for (design in convo.designs) {
                    DesignBlockItem(design, now, onOpen, onLongPress)
                }
                for (hung in convo.hung) {
                    HungRow(hung, now, onOpen, onLongPress)
                }
                if (convo.folded.isNotEmpty()) {
                    FoldLine(convo, foldOpen, onFold)
                    if (foldOpen) {
                        Column(modifier = Modifier.fillMaxWidth()) {
                            for (epic in convo.folded) {
                                HungRow(epic, now, onOpen, onLongPress)
                            }
                        }
                    }
                }
            }
        }
    }
}

// A conversation's own row: its pane's line. An own row has no pane, so it draws no line and its things draw under it.
@Composable
private fun ConvoHead(convo: Convo, now: Long, onOpen: (String) -> Unit, onLongPress: (SheetRow) -> Unit, moving: Boolean) {
    val pane = convo.pane ?: return
    ConvoLine(convo, pane, now, onOpen, onLongPress, moving)
}

private const val FOLD_OPEN = "open"
private const val FOLD_SHUT = "shut"

/**
 * The fold line of a conversation's folded ended epics: their count in words, as a button whose state is open or shut.
 * A tap calls [onFold]. The epics it folds draw under the row, so the line itself is not indented.
 */
@Composable
internal fun FoldLine(convo: Convo, open: Boolean, onFold: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("fold:${convo.key}")
            .semantics { stateDescription = if (open) FOLD_OPEN else FOLD_SHUT }
            .clickable(role = Role.Button, onClick = onFold)
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        Text("${convo.folded.size} ended", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
    }
}

@Composable
private fun ConvoLine(
    convo: Convo,
    pane: Pane,
    now: Long,
    onOpen: (String) -> Unit,
    onLongPress: (SheetRow) -> Unit,
    moving: Boolean,
) {
    val unseen = pane.unread()
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("convo:${convo.key}")
            .then(if (moving) Modifier.semantics { this.moving = true } else Modifier)
            .arrival(moving)
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
                convoLineText(convo, now),
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

// A conversation's second line; an unbound pane's command is set in the mono family.
private fun convoLineText(convo: Convo, now: Long): AnnotatedString {
    val line = convoLineOf(convo, now)
    val command = if (convo.pane?.unbound == true) line.indexOf(UNBOUND_COMMAND) else -1
    return buildAnnotatedString {
        append(line)
        if (command >= 0) addStyle(SpanStyle(fontFamily = OrreryMono), command, command + UNBOUND_COMMAND.length)
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

// A hung epic or contract: an epic's mark or a contract's mark, then three lines: its name with its count at the end (a
// contract's count sits at the row's end, its version), its words, and its blocker in attention, else its newest
// sentence. An epic also draws its running steps. Under a design ([inDesign]) the name and the steps are in regular
// weight: the design's name is the only bold one in its block.
@Composable
internal fun HungRow(
    hung: Hung,
    now: Long,
    onOpen: (String) -> Unit,
    onLongPress: (SheetRow) -> Unit,
    modifier: Modifier = Modifier,
    inDesign: Boolean = false,
) {
    val words = epicRowWords(hung, now)
    val mark = words.mark
    val contract = mark == null
    val ended = hung is EpicNode && !hung.epic.live
    val third = thirdLineOf(words)
    // Only an epic has a sheet; a contract has no archive to offer.
    val onLongClick: (() -> Unit)? = (hung as? EpicNode)?.let { node -> { onLongPress(SheetRow.Epic(node.epic)) } }
    Column(modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .testTag("hung:${hung.key}")
                .combinedClickable(onClick = { onOpen(hung.key) }, onLongClick = onLongClick)
                .padding(vertical = OrreryTokens.Space.four),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalAlignment = Alignment.Top,
        ) {
            if (hung is EpicNode && mark != null) {
                EpicMark(hung.epic.id, mark, OrreryTokens.Size.mark)
            } else {
                ContractMark(hung.key)
            }
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    titleLineOf(words),
                    style = OrreryTokens.Type.body.copy(
                        fontWeight = if (ended) OrreryTokens.Font.Weight.regular else boldUnlessIn(inDesign),
                    ),
                    color = if (ended) OrreryTokens.Ink.two else OrreryTokens.Ink.one,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(words.words, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (third.isNotEmpty()) {
                    Text(
                        third,
                        style = OrreryTokens.Type.small,
                        color = if (words.blocker.isNotEmpty()) OrreryTokens.Role.attention else OrreryTokens.Ink.three,
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
            EpicSteps(hung.epic, hung.detail, onOpen, inDesign)
        }
    }
}

// An epic's running steps, at most MAX_STEP_ROWS, then one line for the rest. A step's pips are its task's cells in the
// epic's record, when the record is known.
@Composable
private fun EpicSteps(epic: EpicRow, detail: EpicDetail?, onOpen: (String) -> Unit, inDesign: Boolean) {
    val shown = epic.runningSteps.take(MAX_STEP_ROWS)
    val more = epic.runningSteps.size - shown.size
    val tasks = detail?.let { recordTasksOf(it) }.orEmpty()
    for (step in shown) {
        StepLine(step, stepCellsOf(step.task, tasks), onOpen, inDesign)
    }
    if (more > 0) {
        Text(
            "$more more",
            modifier = Modifier.padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
            style = OrreryTokens.Type.small.copy(fontWeight = boldUnlessIn(inDesign)),
        )
    }
}

// A step reads as the Now rows do: its purpose, else its task, else its role, then what it is doing, then the pips of its
// task when it has one.
@Composable
private fun StepLine(step: StepRow, cells: List<CellTone>, onOpen: (String) -> Unit, inDesign: Boolean) {
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
                stepHeadlineOf(step),
                style = OrreryTokens.Type.small.copy(fontWeight = boldUnlessIn(inDesign)),
                color = OrreryTokens.Ink.one,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                doingOf(step),
                style = OrreryTokens.Type.small,
                color = if (step.held) OrreryTokens.Role.attention else OrreryTokens.Ink.three,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (step.task.isNotEmpty()) {
            Pips(cells = cells)
        }
    }
}

// A name's or a step's weight: bold in the tree, regular under a design, whose own name is its block's one bold name.
private fun boldUnlessIn(inDesign: Boolean): FontWeight =
    if (inDesign) OrreryTokens.Font.Weight.regular else OrreryTokens.Font.Weight.bold

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

// A contract's mark: a sheet of paper in attention, with three lines of text.
@Composable
internal fun ContractMark(key: String) {
    Canvas(
        modifier = Modifier
            .size(OrreryTokens.Size.gauge)
            .testTag("mark:$key")
            .semantics { contentDescription = CONTRACT_WORDS },
    ) {
        val stroke = Stroke(width = contractPx(CONTRACT_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawPath(
            Path().apply {
                moveTo(contractPx(7f), contractPx(4f))
                lineTo(contractPx(14f), contractPx(4f))
                lineTo(contractPx(17f), contractPx(7f))
                lineTo(contractPx(17f), contractPx(20f))
                lineTo(contractPx(7f), contractPx(20f))
                close()
            },
            OrreryTokens.Role.attention,
            style = stroke,
        )
        for (y in listOf(10f, 13f, 16f)) {
            drawLine(
                OrreryTokens.Role.attention,
                Offset(contractPx(10f), contractPx(y)),
                Offset(contractPx(14f), contractPx(y)),
                strokeWidth = stroke.width,
                cap = StrokeCap.Round,
            )
        }
    }
}

private fun DrawScope.contractPx(view: Float): Float = view * size.width / CONTRACT_VIEW

// The age of a stamp in seconds: "30s", "12m", "3h", "2d".
private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}
