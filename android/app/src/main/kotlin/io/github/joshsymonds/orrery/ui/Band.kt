package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

enum class AskWay(val word: String) { ANSWER("Answer"), DECIDE("Decide"), OPEN("Open") }

/** One epic that waits on the reader: its id, its slug, what it asks, and the way in. */
data class AskLine(val epicId: String, val slug: String, val text: String, val way: AskWay)

private const val ASK_LINES_SHOWN = 3

/**
 * What the conversation is for and what waits on the reader: the goal on one line with the control that hides the
 * header when [onHide] is given (the strip of tiles under the band carries it when it has tiles), then up to three ask lines, each opening its epic, then "and N more ask" with the rest's slugs. Nothing is
 * drawn with neither a goal nor a line.
 */
@Composable
fun Band(goal: String, lines: List<AskLine>, onOpen: (String) -> Unit, onHide: (() -> Unit)?) {
    if (goal.isEmpty() && lines.isEmpty()) return
    Column(modifier = Modifier.testTag("band").fillMaxWidth()) {
        if (goal.isNotEmpty()) BandGoal(goal, onHide)
        lines.take(ASK_LINES_SHOWN).forEach { BandLine(it, onOpen) }
        if (lines.size > ASK_LINES_SHOWN) BandMore(lines.drop(ASK_LINES_SHOWN))
    }
}

private fun Modifier.bandRow(): Modifier = fillMaxWidth().padding(
    horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two,
    vertical = OrreryTokens.Space.four,
)

@Composable
private fun BandGoal(goal: String, onHide: (() -> Unit)?) {
    Row(
        modifier = Modifier.testTag("band-goal").bandRow(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = "Goal".inLabelCase(),
            color = OrreryTokens.Role.you,
            style = OrreryTokens.Type.label.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        Text(
            text = goal,
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small,
        )
        if (onHide != null) HideHeader(onHide)
    }
}

/** The control that hides the header: on the band's goal line, or at the end of the strip of tiles once it has tiles. */
@Composable
fun HideHeader(onHide: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag("band-hide")
            .size(OrreryTokens.Size.controlTouch)
            .clickable(onClick = onHide)
            .semantics { contentDescription = "Hide the header" },
        contentAlignment = Alignment.Center,
    ) {
        Text(text = "⌃", color = OrreryTokens.Ink.four, style = OrreryTokens.Type.small, maxLines = 1)
    }
}

@Composable
private fun BandLine(line: AskLine, onOpen: (String) -> Unit) {
    Row(
        modifier = Modifier
            .testTag("band-line:${line.epicId}")
            .clickable { onOpen(line.epicId) }
            .bandRow(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = line.slug,
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        Text(
            text = line.text,
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small,
        )
        Text(
            text = line.way.word + " ›",
            color = OrreryTokens.Role.attention,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}

@Composable
private fun BandMore(rest: List<AskLine>) {
    Row(
        modifier = Modifier.testTag("band-more").bandRow(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = "and ${rest.size} more ask",
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
        Text(
            text = rest.joinToString(", ") { it.slug },
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
        )
    }
}
