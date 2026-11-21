package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Semantics key: a Rule's tone, the colour its label is drawn in, read by tests from the rule's node. */
internal val RuleToneKey = SemanticsPropertyKey<Color>("RuleTone")

private val DAY_OF_MONTH: DateTimeFormatter = DateTimeFormatter.ofPattern("EEEE d MMMM", Locale.ENGLISH)

/**
 * What happened between: a hairline across the column, its [label] in [tone], its [detail] when given, then the
 * hairline again. The row is the rule's `rule:<kind>` node, merged so its words read together; with [onTap] it is
 * pressed and draws a chevron that points down while [open]. [opened] draws under the row only while [open]. A rule
 * draws no rail.
 */
@Composable
private fun Rule(
    kind: String,
    label: String,
    tone: Color,
    line: Color,
    detail: String? = null,
    onTap: (() -> Unit)? = null,
    open: Boolean = false,
    opened: @Composable () -> Unit = {},
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .testTag("rule:$kind")
                .semantics(mergeDescendants = true) { this[RuleToneKey] = tone }
                .fillMaxWidth()
                .let { if (onTap != null) it.clickable(onClick = onTap) else it }
                .padding(top = OrreryTokens.Space.eight + OrreryTokens.Space.two, bottom = OrreryTokens.Space.two),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            RuleHairline(line)
            Text(text = label.inLabelCase(), color = tone, style = OrreryTokens.Type.label, maxLines = 1)
            if (detail != null) {
                Text(text = detail, color = OrreryTokens.Ink.four, style = OrreryTokens.Type.small, maxLines = 1)
            }
            if (onTap != null) RuleCaret(open)
            RuleHairline(line)
        }
        if (open) opened()
    }
}

@Composable
private fun RowScope.RuleHairline(colour: Color) {
    Box(
        modifier = Modifier
            .weight(1f)
            .height(OrreryTokens.Size.hairline)
            .background(colour),
    )
}

/** The caret of a rule that opens: a chevron turned down while it is open. */
@Composable
private fun RuleCaret(open: Boolean) {
    Text(
        text = "›",
        modifier = Modifier
            .width(OrreryTokens.Space.eight)
            .graphicsLayer { rotationZ = if (open) 90f else 0f },
        color = OrreryTokens.Ink.four,
        style = OrreryTokens.Type.small,
        maxLines = 1,
    )
}

/**
 * A compaction, labelled Compacted with its trigger and token count as its detail, for example "auto · 611k tokens".
 * A tap calls [onToggle], and [summary] draws under the rule while [expanded].
 */
@Composable
fun CompactionRule(
    trigger: String,
    tokens: Long,
    expanded: Boolean,
    onToggle: () -> Unit,
    summary: @Composable () -> Unit,
) {
    val detail = listOfNotNull(
        trigger.ifEmpty { null },
        tokens.takeIf { it > 0 }?.let { "${it / 1_000}k tokens" },
    ).joinToString(" · ").ifEmpty { null }
    Rule(
        kind = "compaction",
        label = "Compacted",
        tone = OrreryTokens.Ink.four,
        line = OrreryTokens.Line.soft,
        detail = detail,
        onTap = onToggle,
        open = expanded,
        opened = summary,
    )
}

/** The interruption by the owner, labelled with the time it happened in [zone]. */
@Composable
fun InterruptedRule(seconds: Long, zone: ZoneId = ZoneId.systemDefault()) {
    Rule(
        kind = "interrupted",
        label = "Interrupted by you",
        tone = OrreryTokens.Role.halt,
        line = OrreryTokens.Role.haltLine,
        detail = Instant.ofEpochSecond(seconds).atZone(zone).format(CLOCK_TIME),
    )
}

/** The mark where the entries newer than the owner's last look begin. */
@Composable
fun NewRule() {
    Rule(
        kind = "new",
        label = "New since you looked",
        tone = OrreryTokens.Role.new,
        line = OrreryTokens.Role.newLine,
    )
}

/** The day a change of local day falls on, for example "Monday 16 November", in [zone]. */
@Composable
fun DayRule(seconds: Long, zone: ZoneId = ZoneId.systemDefault()) {
    Rule(
        kind = "day",
        label = Instant.ofEpochSecond(seconds).atZone(zone).format(DAY_OF_MONTH),
        tone = OrreryTokens.Ink.four,
        line = OrreryTokens.Line.soft,
    )
}
