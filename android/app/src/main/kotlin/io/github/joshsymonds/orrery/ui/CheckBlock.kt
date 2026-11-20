package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
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
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/**
 * One check round of a step, as the conversation draws it: a cap naming the round and that the engine ran it, one chip
 * per check of [round] in the step's order (a passed check neutral, a failed one bad), and the text's first line as the
 * note. The lines after the first fold under the note and show when it is tapped. The edge is bad when one of the
 * round's checks failed, ok otherwise, and reads as the block's state.
 */
@Composable
fun CheckBlock(text: String, round: Int, checks: List<StepCheckRow>, modifier: Modifier = Modifier) {
    val roundChecks = checks.filter { it.round == round }
    val failed = roundChecks.any { !it.ok }
    val lines = text.split("\n")
    val first = lines.first()
    val rest = lines.drop(1)
    var restShown by remember { mutableStateOf(false) }
    val edge = if (failed) OrreryTokens.Role.bad else OrreryTokens.Role.ok
    Column(
        modifier = modifier
            .fillMaxWidth()
            .testTag("check-block:$round")
            .semantics { stateDescription = if (failed) "bad" else "ok" }
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.raised)
            .drawBehind { drawRect(edge, size = size.copy(width = OrreryTokens.Size.rail.toPx())) }
            .padding(
                start = OrreryTokens.Space.twelve + OrreryTokens.Size.rail,
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
            Text(
                text = "Checks · round $round".inLabelCase(),
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.label,
            )
            Text(
                text = "from the engine",
                color = OrreryTokens.Ink.four,
                style = OrreryTokens.Type.small,
            )
        }
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
        ) {
            roundChecks.forEach { check ->
                Chip(
                    text = "${check.name} ${if (check.ok) "✓" else "✗"}",
                    tone = if (check.ok) ChipTone.Neutral else ChipTone.Bad,
                    modifier = Modifier.testTag("check-chip:${check.name}"),
                )
            }
        }
        val noteModifier = if (rest.isEmpty()) {
            Modifier.testTag("check-note")
        } else {
            Modifier.testTag("check-note").clickable { restShown = !restShown }
        }
        Text(
            text = first,
            modifier = noteModifier,
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.body,
        )
        if (restShown) {
            Text(
                text = rest.joinToString("\n"),
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.small,
            )
        }
    }
}
