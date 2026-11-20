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

/** A contract that waits on the reader: its id, its slug, and the text of what is left to settle. */
data class ContractBandLine(val id: String, val slug: String, val text: String)

/** One line of the band before it is drawn: a contract that waits on the reader, or an epic's ask. */
private sealed interface BandAsk {
    val name: String

    data class Contract(val line: ContractBandLine) : BandAsk {
        override val name: String get() = "${line.slug} contract"
    }

    data class Epic(val line: AskLine) : BandAsk {
        override val name: String get() = line.slug
    }
}

private const val ASK_LINES_SHOWN = 3

/**
 * What the conversation is for and what waits on the reader: the goal on one line with the control that hides the
 * header when [onHide] is given (the strip of tiles under the band carries it when it has tiles), then up to three ask
 * lines, the waiting [contracts] first and then the epic [lines], then "and N more ask" with the rest's names. A contract
 * line opens it through [onOpenContract], except the contract open on the right ([hereContract]), which says so instead.
 * Nothing is drawn with neither a goal nor a line.
 */
@Composable
fun Band(
    goal: String,
    lines: List<AskLine>,
    onOpen: (String) -> Unit,
    onHide: (() -> Unit)?,
    contracts: List<ContractBandLine> = emptyList(),
    hereContract: String = "",
    onOpenContract: (String) -> Unit = {},
) {
    if (goal.isEmpty() && lines.isEmpty() && contracts.isEmpty()) return
    val asks = contracts.map { BandAsk.Contract(it) } + lines.map { BandAsk.Epic(it) }
    Column(modifier = Modifier.testTag("band").fillMaxWidth()) {
        if (goal.isNotEmpty()) BandGoal(goal, onHide)
        asks.take(ASK_LINES_SHOWN).forEach { ask ->
            when (ask) {
                is BandAsk.Contract -> BandContractLine(
                    line = ask.line,
                    here = ask.line.id == hereContract,
                    onOpen = onOpenContract,
                )
                is BandAsk.Epic -> BandLine(ask.line, onOpen)
            }
        }
        if (asks.size > ASK_LINES_SHOWN) BandMore(asks.drop(ASK_LINES_SHOWN))
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
private fun BandContractLine(line: ContractBandLine, here: Boolean, onOpen: (String) -> Unit) {
    Row(
        modifier = Modifier
            .testTag("band-contract:${line.id}")
            .then(if (here) Modifier else Modifier.clickable { onOpen(line.id) })
            .bandRow(),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = "${line.slug} contract",
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
            text = if (here) "open on the right ›" else "Read and decide ›",
            color = OrreryTokens.Role.attention,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}

@Composable
private fun BandMore(rest: List<BandAsk>) {
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
            text = rest.joinToString(", ") { it.name },
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
        )
    }
}
