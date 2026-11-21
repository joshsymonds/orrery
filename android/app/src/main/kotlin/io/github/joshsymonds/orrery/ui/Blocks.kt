package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentWidth
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.StepFacts
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlin.math.cos
import kotlin.math.sin

/** Semantics key: a Block's rail's tone, named as the web names it (you, engine, ok, bad, attention, quiet). */
val ShapeToneKey = SemanticsPropertyKey<String>("ShapeTone")

/** A check a send-back round ran, and whether it passed. */
data class CheckResult(val name: String, val ok: Boolean)

/** How a step's fact is drawn: plain ink, added lines green, removed lines red. */
enum class FactKind { Plain, Added, Removed }

/** One fact of a step's end, drawn as [text] in the colour its [kind] gives. */
data class StepFact(val text: String, val kind: FactKind)

/** The tone a Block draws in: its rail's and its label's. */
private enum class BlockTone { You, Engine, Ok, Bad, Attention, Quiet }

/** The icon a Block's label line leads with. */
private enum class BlockIcon { Person, Gear, Clock, Check, Cross, Stop, Question, Pause, Open, Gauge }

/** The kinds of epic event an EpicEventBlock draws: a question, a park, a release, an end, and the epic a create made. */
enum class EpicEventKind { Question, Park, Release, End, Created }

/** A step end's words and the tone its rail takes. */
private class EndWord(val word: String, val tone: BlockTone)

private val END_WORDS: Map<String, EndWord> = mapOf(
    "done" to EndWord("done", BlockTone.Ok),
    "returned" to EndWord("returned", BlockTone.Ok),
    "failed" to EndWord("failed", BlockTone.Bad),
    "goal_unmet" to EndWord("goal not met", BlockTone.Bad),
    "checks_failed" to EndWord("checks failed", BlockTone.Bad),
    "gate_failed" to EndWord("gate failed", BlockTone.Bad),
    "stopped" to EndWord("stopped", BlockTone.Quiet),
    "engine_restarted" to EndWord("engine restarted", BlockTone.Quiet),
    "overtime" to EndWord("ran over time", BlockTone.Bad),
    "tool_stuck" to EndWord("a tool stuck", BlockTone.Bad),
    "silent" to EndWord("went silent", BlockTone.Bad),
    "looping" to EndWord("looping", BlockTone.Bad),
    "wedged" to EndWord("wedged", BlockTone.Bad),
)

/** The facts of a step's end as they are drawn, in order; empty when the item carries no facts. */
fun stepFactsOf(facts: StepFacts?): List<StepFact> {
    if (facts == null) return emptyList()
    return buildList {
        if (facts.counted) {
            add(StepFact(countOf(facts.files, "file"), FactKind.Plain))
            add(StepFact("+${facts.added}", FactKind.Added))
            add(StepFact("−${facts.removed}", FactKind.Removed))
        }
        if (facts.rounds > 0) add(StepFact(countOf(facts.rounds.toLong(), "round"), FactKind.Plain))
        add(StepFact(durationOf(facts.seconds), FactKind.Plain))
    }
}

private fun countOf(count: Long, word: String): String = "$count ${if (count == 1L) word else "${word}s"}"

// Seconds under a minute, minutes under an hour, then hours and minutes.
private fun durationOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3600 -> "${seconds / 60}m"
    else -> "${seconds / 3600}h ${(seconds % 3600) / 60}m"
}

/** A user's message; while it is being sent it is drawn as SENDING, dashed, with no time. */
@Composable
fun YouBlock(text: String, time: String? = null, sending: Boolean = false) {
    Block(
        kind = "you",
        tone = BlockTone.You,
        icon = BlockIcon.Person,
        label = if (sending) "SENDING" else "YOU",
        pending = sending,
        time = if (sending) null else time,
        body = { Prose(text, OrreryTokens.Ink.one) },
    )
}

/** What the engine sent; a cron prompt is drawn as CRON with the clock. */
@Composable
fun EventBlock(text: String, time: String? = null, cron: Boolean = false) {
    Block(
        kind = "event",
        tone = BlockTone.Engine,
        icon = if (cron) BlockIcon.Clock else BlockIcon.Gear,
        label = if (cron) "CRON" else "EVENT",
        words = if (cron) null else "from the engine",
        time = time,
        body = { Prose(text, OrreryTokens.Ink.one) },
    )
}

/** A step that ended: its rail is its verdict, its end chip says how, and its facts line sits under its summary. */
@Composable
fun StepEndBlock(agent: String, end: String, summary: String, facts: StepFacts?, time: String? = null) {
    val verdict = END_WORDS[end] ?: EndWord(end, BlockTone.Quiet)
    Block(
        kind = "stepend",
        tone = verdict.tone,
        icon = when (verdict.tone) {
            BlockTone.Ok -> BlockIcon.Check
            BlockTone.Bad -> BlockIcon.Cross
            else -> BlockIcon.Stop
        },
        label = "STEP ENDED",
        who = agent,
        time = time,
        end = { Chip(verdict.word, chipToneOf(verdict.tone)) },
        body = {
            Prose(summary, OrreryTokens.Ink.one)
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
            Prose(sentence, OrreryTokens.Ink.one)
            under()
        },
    )
}

/**
 * A send-back round: its chips are the round's checks, its rail is ok when they all passed and bad when one failed,
 * and with no checks it is the engine's. The text's first line is the note; the rest opens under it.
 */
@Composable
fun CheckBlock(round: Int, text: String, checks: List<CheckResult> = emptyList(), time: String? = null) {
    val tone = when {
        checks.isEmpty() -> BlockTone.Engine
        checks.all { it.ok } -> BlockTone.Ok
        else -> BlockTone.Bad
    }
    val lines = text.lines()
    val rest = lines.drop(1).joinToString("\n")
    var open by remember { mutableStateOf(false) }
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
            Prose(lines.first(), OrreryTokens.Ink.one)
            if (open) Prose(rest, OrreryTokens.Ink.one)
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
            body()
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

@Composable
private fun ChecksRow(checks: List<CheckResult>) {
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        checks.forEach { check -> Chip(check.name, if (check.ok) ChipTone.Ok else ChipTone.Bad) }
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

@Composable
private fun Prose(text: String, color: Color) {
    MarkdownBody(blocks = remember(text) { parseMarkdown(text) }, color = color)
}

@Composable
private fun BlockGlyph(icon: BlockIcon, ink: Color) {
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
