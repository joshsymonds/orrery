package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentWidth
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.SubcomposeLayout
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.TextUnit
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepFacts
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlin.math.cos
import kotlin.math.sin

/** Semantics key: a Block's rail's tone, named as the web names it (you, engine, ok, bad, attention, quiet). */
val ShapeToneKey = SemanticsPropertyKey<String>("ShapeTone")

/** A check a send-back round ran, and whether it passed. */
data class CheckResult(val name: String, val ok: Boolean)

/** The checks of a step that [round] ran, in the step's order, as a CheckBlock draws them. */
fun roundChecks(checks: List<StepCheckRow>, round: Int): List<CheckResult> =
    checks.filter { it.round == round }.map { CheckResult(it.name, it.ok) }

/** A send-back's note: the text's first line. */
internal fun checkNote(text: String): String = text.lines().first()

/** A send-back's rest: the lines after its note, which open under it. */
internal fun checkRest(text: String): String = text.lines().drop(1).joinToString("\n")

/** How a step's fact is drawn: plain ink, added lines green, removed lines red. */
enum class FactKind { Plain, Added, Removed }

/** One fact of a step's end, drawn as [text] in the colour its [kind] gives. */
data class StepFact(val text: String, val kind: FactKind)

/** The tone a Block draws in: its rail's and its label's. */
private enum class BlockTone { You, Engine, Ok, Bad, Attention, Quiet }

/** The icon a Block's label line leads with, and the host icon the machine band's label leads with. */
internal enum class BlockIcon { Person, Phone, Gear, Clock, Check, Cross, Stop, Question, Pause, Open, Gauge, Host }

/** The kinds of epic event an EpicEventBlock draws: a question, a park, a release, an end, and the epic a create made. */
enum class EpicEventKind { Question, Park, Release, End, Created }

/** A step end's chip words, in round 6's words. An end not named here is drawn as it is written. */
private val END_WORDS: Map<String, String> = mapOf(
    "done" to "done",
    "returned" to "returned",
    "failed" to "failed",
    "goal_unmet" to "goal not met",
    "checks_failed" to "checks failed",
    "gate_failed" to "gate failed",
    "stopped" to "stopped",
    "engine_restarted" to "engine restarted",
    "overtime" to "ran over time",
    "tool_stuck" to "a tool stuck",
    "silent" to "went silent",
    "looping" to "looping",
    "wedged" to "wedged",
)

// endToneOf is a step end's tone: done and returned pass, stopped is quiet, and every other end failed.
private fun endToneOf(end: String): BlockTone = when (end) {
    "done", "returned" -> BlockTone.Ok
    "stopped" -> BlockTone.Quiet
    else -> BlockTone.Bad
}

/** The facts of a step's end as they are drawn, in order; empty when the item carries no facts. */
fun stepFactsOf(facts: StepFacts?): List<StepFact> {
    if (facts == null) return emptyList()
    return buildList {
        if (facts.rounds > 0) add(StepFact(countOf(facts.rounds.toLong(), "round"), FactKind.Plain))
        add(StepFact(durationOf(facts.seconds), FactKind.Plain))
        if (facts.counted) {
            add(StepFact(countOf(facts.files, "file"), FactKind.Plain))
            add(StepFact("+${facts.added}", FactKind.Added))
            add(StepFact("−${facts.removed}", FactKind.Removed))
        }
    }
}

private fun countOf(count: Long, word: String): String = "$count ${if (count == 1L) word else "${word}s"}"

// Seconds under a minute, minutes under an hour, then hours and minutes.
private fun durationOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3600 -> "${seconds / 60}m"
    else -> "${seconds / 3600}h ${(seconds % 3600) / 60}m"
}

/**
 * A user's message; while it is being sent it is drawn as SENDING, dashed, with no time. A message sent from the phone
 * app, when [phone] is set, is drawn with the phone's icon and the words from your phone. Its head, YOU beside the time,
 * leads the first line of its words when they open on a paragraph, so the message takes no line of its own for its
 * head; words that open on anything else start under the head.
 */
@Composable
fun YouBlock(text: String, time: String? = null, sending: Boolean = false, marks: EntryMarks? = null, phone: Boolean = false) {
    val leads = remember(text) { parseMarkdown(text).firstOrNull().let { it is MdBlock.Paragraph && it.lead == 0 } }
    Block(
        kind = "you",
        tone = BlockTone.You,
        icon = if (phone) BlockIcon.Phone else BlockIcon.Person,
        label = if (sending) "SENDING" else "YOU",
        words = if (phone) "· from your phone" else null,
        pending = sending,
        time = if (sending) null else time,
        body = { Prose(text, OrreryTokens.Ink.one, marks) },
        leadBody = if (leads) { indent -> Prose(text, OrreryTokens.Ink.one, marks, firstIndent = indent) } else null,
    )
}

/** What the engine sent; a cron prompt is drawn as CRON with the clock. */
@Composable
fun EventBlock(text: String, time: String? = null, cron: Boolean = false, marks: EntryMarks? = null) {
    Block(
        kind = "event",
        tone = BlockTone.Engine,
        icon = if (cron) BlockIcon.Clock else BlockIcon.Gear,
        label = if (cron) "CRON" else "EVENT",
        words = if (cron) null else "from the engine",
        time = time,
        body = { Prose(text, OrreryTokens.Ink.one, marks) },
    )
}

/**
 * A step that ended: its rail is its verdict, its end chip says how, and its facts line sits under its summary. It is
 * named by [who], the task the step ran or else its step id, with its [agent] beside it as words when it names one.
 */
@Composable
fun StepEndBlock(
    who: String? = null,
    agent: String,
    end: String,
    summary: String,
    facts: StepFacts?,
    time: String? = null,
    marks: EntryMarks? = null,
) {
    val tone = endToneOf(end)
    Block(
        kind = "stepend",
        tone = tone,
        icon = when (tone) {
            BlockTone.Ok -> BlockIcon.Check
            BlockTone.Bad -> BlockIcon.Cross
            else -> BlockIcon.Stop
        },
        label = "STEP ENDED",
        who = who,
        words = agent.takeIf { it.isNotEmpty() },
        time = time,
        end = { Chip(END_WORDS[end] ?: end, chipToneOf(tone)) },
        body = {
            Prose(summary, OrreryTokens.Ink.one, marks)
            FactsLine(stepFactsOf(facts))
        },
    )
}

/**
 * An epic's event as its block names it: its kind's tone and icon, the EPIC label, its [sentence], and its [slug] with a
 * chevron. The slug and the chevron are one control that [onOpen] runs when pressed, when it is given.
 */
@Composable
fun EpicEventBlock(
    kind: EpicEventKind,
    slug: String,
    sentence: String,
    time: String? = null,
    onOpen: (() -> Unit)? = null,
    under: @Composable () -> Unit = {},
    marks: EntryMarks? = null,
) {
    val tone = when (kind) {
        EpicEventKind.Question, EpicEventKind.Park -> BlockTone.Attention
        EpicEventKind.Release -> BlockTone.Ok
        EpicEventKind.End -> BlockTone.Quiet
        EpicEventKind.Created -> BlockTone.Engine
    }
    val icon = when (kind) {
        EpicEventKind.Question -> BlockIcon.Question
        EpicEventKind.Park -> BlockIcon.Pause
        EpicEventKind.Release -> BlockIcon.Open
        EpicEventKind.End -> BlockIcon.Stop
        EpicEventKind.Created -> BlockIcon.Gauge
    }
    Block(
        kind = "epicevent",
        tone = tone,
        icon = icon,
        label = "EPIC",
        time = time,
        end = {
            val open = onOpen
            Row(
                modifier = if (open == null) {
                    Modifier
                } else {
                    Modifier
                        .semantics { contentDescription = "Open the epic $slug" }
                        .clickable(onClick = open)
                },
            ) {
                Text(text = slug, color = OrreryTokens.Role.ref, style = OrreryTokens.Type.small, maxLines = 1)
                Text(text = "›", color = OrreryTokens.Ink.four, style = OrreryTokens.Type.small, maxLines = 1)
            }
        },
        body = {
            Prose(sentence, OrreryTokens.Ink.one, marks)
            under()
        },
    )
}

/**
 * A send-back round: its chips are the round's checks, its rail is ok when they all passed and bad when one failed,
 * and with no checks it is the engine's. The text's first line is the note; the rest opens under it, and opens for
 * the find's current match when that match is in the rest.
 */
@Composable
fun CheckBlock(
    round: Int,
    text: String,
    checks: List<CheckResult> = emptyList(),
    time: String? = null,
    marks: EntryMarks? = null,
) {
    val tone = when {
        checks.isEmpty() -> BlockTone.Engine
        checks.all { it.ok } -> BlockTone.Ok
        else -> BlockTone.Bad
    }
    val note = checkNote(text)
    val rest = checkRest(text)
    // The rest's searched text starts after the note's, so a current match from there on is in the rest.
    val restBase = remember(note) { searchedSpan(note) }
    val current = marks?.let { found -> found.current?.takeIf { it in found.matches } }
    var open by remember { mutableStateOf(false) }
    LaunchedEffect(current, restBase) {
        if (current != null && current.range.first >= restBase) open = true
    }
    Block(
        kind = "checks",
        tone = tone,
        icon = when (tone) {
            BlockTone.Ok -> BlockIcon.Check
            BlockTone.Bad -> BlockIcon.Cross
            else -> BlockIcon.Gear
        },
        label = "CHECKS",
        words = "round $round from the engine",
        time = time,
        onTap = if (rest.isEmpty()) null else { { open = !open } },
        body = {
            if (checks.isNotEmpty()) ChecksRow(checks)
            Prose(note, OrreryTokens.Ink.one, marks)
            if (open) Prose(rest, OrreryTokens.Ink.one, marks, base = restBase)
        },
    )
}

@Composable
private fun Block(
    kind: String,
    tone: BlockTone,
    icon: BlockIcon,
    label: String,
    pending: Boolean = false,
    who: String? = null,
    words: String? = null,
    time: String? = null,
    end: (@Composable () -> Unit)? = null,
    onTap: (() -> Unit)? = null,
    leadBody: (@Composable (TextUnit) -> Unit)? = null,
    body: @Composable () -> Unit,
) {
    val ink = inkOf(tone)
    val tint = tintOf(tone)
    // The tap sits before the clip: a hit test against the clip's non-uniform outline misses in Robolectric.
    val tap = onTap
    val tapping = if (tap == null) Modifier else Modifier
        .semantics { onClick(action = { tap(); true }) }
        .pointerInput(tap) { detectTapGestures(onTap = { tap() }) }
    Box(
        modifier = Modifier
            .testTag("block:$kind")
            .fillMaxWidth()
            .padding(top = OrreryTokens.Space.eight + OrreryTokens.Space.two, bottom = OrreryTokens.Space.twelve)
            .then(tapping)
            .clip(RoundedCornerShape(topEnd = OrreryTokens.Radius.two, bottomEnd = OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.well)
            .then(if (tint != null) Modifier.background(tint) else Modifier),
    ) {
        Canvas(
            modifier = Modifier
                .matchParentSize()
                .wrapContentWidth(Alignment.Start)
                .width(OrreryTokens.Size.rail)
                .testTag("rail")
                .semantics { set(ShapeToneKey, tone.name.lowercase()) },
        ) {
            drawRail(ink, pending)
        }
        Column(
            modifier = Modifier.padding(
                start = OrreryTokens.Size.rail + OrreryTokens.Space.twelve,
                end = OrreryTokens.Space.twelve,
                top = OrreryTokens.Space.eight,
                bottom = OrreryTokens.Space.eight,
            ),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        ) {
            val head = @Composable {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                ) {
                    BlockGlyph(icon, ink)
                    Text(text = label.inLabelCase(), color = ink, style = OrreryTokens.Type.label)
                    if (who != null) Text(text = who, color = OrreryTokens.Ink.one, style = OrreryTokens.Type.small)
                    if (words != null) Text(text = words, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
                    if (time != null) {
                        Text(
                            text = time,
                            modifier = Modifier.testTag("at"),
                            color = OrreryTokens.Ink.four,
                            style = OrreryTokens.Type.small,
                        )
                    }
                    end?.invoke()
                }
            }
            if (leadBody == null) {
                head()
                body()
            } else {
                HeadLeading(head, leadBody)
            }
        }
    }
}

/**
 * Draws [head] at the start of the first line of [body], which is given the indent that clears the head and the gap after
 * it. The head sits centred on that line; a body shorter than the head takes the head's height.
 */
@Composable
private fun HeadLeading(head: @Composable () -> Unit, body: @Composable (TextUnit) -> Unit) {
    val density = LocalDensity.current
    val line = with(density) { OrreryTokens.Type.body.lineHeight.roundToPx() }
    val gap = with(density) { OrreryTokens.Space.eight.roundToPx() }
    SubcomposeLayout { constraints ->
        val loose = constraints.copy(minWidth = 0, minHeight = 0)
        val heads = subcompose("head", head).map { it.measure(loose) }
        val headWidth = heads.maxOfOrNull { it.width } ?: 0
        val headHeight = heads.maxOfOrNull { it.height } ?: 0
        val indent = with(density) { (headWidth + gap).toSp() }
        val bodies = subcompose("body") { body(indent) }.map { it.measure(loose) }
        val width = maxOf(headWidth, bodies.maxOfOrNull { it.width } ?: 0).coerceIn(constraints.minWidth, constraints.maxWidth)
        val headTop = maxOf(0, (line - headHeight) / 2)
        val height = maxOf(headTop + headHeight, bodies.maxOfOrNull { it.height } ?: 0)
        layout(width, height) {
            bodies.forEach { it.place(0, 0) }
            heads.forEach { it.place(0, headTop) }
        }
    }
}

// A Block's rail: a solid bar in its tone, or a dashed one while the message is still being sent.
private fun DrawScope.drawRail(ink: Color, pending: Boolean) {
    if (!pending) {
        drawRect(ink)
        return
    }
    val dash = OrreryTokens.Space.four.toPx()
    drawLine(
        color = ink,
        start = Offset(size.width / 2f, 0f),
        end = Offset(size.width / 2f, size.height),
        strokeWidth = size.width,
        pathEffect = PathEffect.dashPathEffect(floatArrayOf(dash, dash)),
    )
}

// Each check is its mark (the check when it passed, the cross when it failed) beside its chip; the chips wrap onto more lines.
@Composable
private fun ChecksRow(checks: List<CheckResult>) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        checks.forEach { check ->
            val tone = if (check.ok) BlockTone.Ok else BlockTone.Bad
            Row(
                modifier = Modifier.testTag("check:${check.name}"),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                BlockGlyph(if (check.ok) BlockIcon.Check else BlockIcon.Cross, inkOf(tone))
                Chip(check.name, chipToneOf(tone))
            }
        }
    }
}

@Composable
private fun FactsLine(facts: List<StepFact>) {
    if (facts.isEmpty()) return
    Row(
        modifier = Modifier.testTag("facts"),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        facts.forEach { fact ->
            Text(text = fact.text, color = factInkOf(fact.kind), style = OrreryTokens.Type.small)
        }
    }
}

/** A text as Markdown, with the find's marks drawn from [base] in its entry's searched text. */
@Composable
private fun Prose(text: String, color: Color, marks: EntryMarks? = null, base: Int = 0, firstIndent: TextUnit = TextUnit.Unspecified) {
    MarkdownBody(
        blocks = remember(text, marks, base) { markedBlocks(parseMarkdown(text), marks, base) },
        color = color,
        firstIndent = firstIndent,
    )
}

@Composable
internal fun BlockGlyph(icon: BlockIcon, ink: Color) {
    Canvas(modifier = Modifier.size(OrreryTokens.Size.icon).testTag("icon:${icon.name.lowercase()}")) {
        drawBlockIcon(icon, ink)
    }
}

// An icon's drawing in its 16-unit box.
private fun DrawScope.drawBlockIcon(icon: BlockIcon, ink: Color) {
    val unit = size.minDimension / 16f
    val stroke = Stroke(width = (OrreryTokens.Size.hairline * 2).toPx(), cap = StrokeCap.Round)
    fun point(x: Float, y: Float) = Offset(x * unit, y * unit)
    fun line(x1: Float, y1: Float, x2: Float, y2: Float) =
        drawLine(ink, point(x1, y1), point(x2, y2), stroke.width, StrokeCap.Round)
    when (icon) {
        BlockIcon.Person -> {
            drawCircle(ink, 2.75f * unit, point(8f, 5.25f), style = stroke)
            drawArc(
                color = ink,
                startAngle = 0f,
                sweepAngle = 180f,
                useCenter = false,
                topLeft = point(4.5f, 9f),
                size = Size(7f * unit, 7f * unit),
                style = stroke,
            )
        }
        BlockIcon.Phone -> {
            drawRoundRect(
                color = ink,
                topLeft = point(5f, 2f),
                size = Size(6f * unit, 12f * unit),
                cornerRadius = CornerRadius(1.5f * unit),
                style = stroke,
            )
            line(7.5f, 11.5f, 8.5f, 11.5f)
        }
        BlockIcon.Gear -> {
            drawCircle(ink, 3.25f * unit, point(8f, 8f), style = stroke)
            for (spoke in 0 until 8) {
                val angle = spoke * Math.PI / 4
                val (cosine, sine) = cos(angle).toFloat() to sin(angle).toFloat()
                line(8f + 5f * cosine, 8f + 5f * sine, 8f + 6.75f * cosine, 8f + 6.75f * sine)
            }
        }
        BlockIcon.Clock -> {
            drawCircle(ink, 6.25f * unit, point(8f, 8f), style = stroke)
            line(8f, 8f, 8f, 4.5f)
            line(8f, 8f, 10.5f, 8f)
        }
        BlockIcon.Check -> {
            line(4.5f, 8.5f, 7f, 11f)
            line(7f, 11f, 11.5f, 5.5f)
        }
        BlockIcon.Cross -> {
            line(5f, 5f, 11f, 11f)
            line(11f, 5f, 5f, 11f)
        }
        BlockIcon.Stop -> drawRect(ink, point(5f, 5f), Size(6f * unit, 6f * unit))
        BlockIcon.Question -> {
            drawArc(
                color = ink,
                startAngle = 180f,
                sweepAngle = 270f,
                useCenter = false,
                topLeft = point(5f, 4f),
                size = Size(6f * unit, 6f * unit),
                style = stroke,
            )
            line(8f, 10f, 8f, 11f)
            drawCircle(ink, OrreryTokens.Size.hairline.toPx(), point(8f, 13.5f))
        }
        BlockIcon.Pause -> {
            line(6f, 5f, 6f, 11f)
            line(10f, 5f, 10f, 11f)
        }
        BlockIcon.Open -> {
            line(5f, 11f, 11f, 5f)
            line(6.5f, 5f, 11f, 5f)
            line(11f, 5f, 11f, 9.5f)
        }
        BlockIcon.Gauge -> {
            drawArc(
                color = ink,
                startAngle = 180f,
                sweepAngle = 180f,
                useCenter = false,
                topLeft = point(3f, 4f),
                size = Size(10f * unit, 10f * unit),
                style = stroke,
            )
            line(8f, 9f, 11f, 6.5f)
        }
        BlockIcon.Host -> {
            // Two stacked units, each with its light: the web's host glyph.
            drawRect(ink, point(3f, 3f), Size(10f * unit, 4f * unit), style = stroke)
            drawRect(ink, point(3f, 9f), Size(10f * unit, 4f * unit), style = stroke)
            line(5.5f, 5f, 6f, 5f)
            line(5.5f, 11f, 6f, 11f)
        }
    }
}

private fun inkOf(tone: BlockTone): Color = when (tone) {
    BlockTone.You -> OrreryTokens.Role.you
    BlockTone.Engine -> OrreryTokens.Role.engine
    BlockTone.Ok -> OrreryTokens.Role.ok
    BlockTone.Bad -> OrreryTokens.Role.bad
    BlockTone.Attention -> OrreryTokens.Role.attention
    BlockTone.Quiet -> OrreryTokens.Ink.three
}

// A Block's ground is its rail's tint over the well; a quiet block has no tint.
private fun tintOf(tone: BlockTone): Color? = when (tone) {
    BlockTone.You -> OrreryTokens.Role.youFill
    BlockTone.Engine -> OrreryTokens.Role.engineFill
    BlockTone.Ok -> OrreryTokens.Role.okFill
    BlockTone.Bad -> OrreryTokens.Role.badFill
    BlockTone.Attention -> OrreryTokens.Role.attentionFill
    BlockTone.Quiet -> null
}

private fun chipToneOf(tone: BlockTone): ChipTone = when (tone) {
    BlockTone.Ok -> ChipTone.Ok
    BlockTone.Bad -> ChipTone.Bad
    else -> ChipTone.Neutral
}

private fun factInkOf(kind: FactKind): Color = when (kind) {
    FactKind.Plain -> OrreryTokens.Ink.three
    FactKind.Added -> OrreryTokens.Role.ok
    FactKind.Removed -> OrreryTokens.Role.bad
}
