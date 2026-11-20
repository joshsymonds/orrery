package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** A state's tone: Neutral is plain, the others take their role's colours. */
enum class ChipTone { Neutral, Ok, Bad, Attention, Live, Engine, New }

/** Semantics key: the tone of a [Chip], on its text node. */
val ChipToneKey = SemanticsPropertyKey<ChipTone>("ChipTone")

/** A state as a small rounded label: its text in the tone's colour, on the tone's fill with its line. */
@Composable
fun Chip(text: String, tone: ChipTone, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.round)
    Text(
        text,
        modifier = modifier
            .clip(shape)
            .border(OrreryTokens.Size.hairline, chipLine(tone), shape)
            .background(chipFill(tone))
            .padding(horizontal = OrreryTokens.Space.eight)
            .semantics { set(ChipToneKey, tone) },
        color = chipInk(tone),
        style = OrreryTokens.Type.small,
    )
}

private fun chipInk(tone: ChipTone): Color = when (tone) {
    ChipTone.Neutral -> OrreryTokens.Ink.two
    ChipTone.Ok -> OrreryTokens.Role.ok
    ChipTone.Bad -> OrreryTokens.Role.bad
    ChipTone.Attention -> OrreryTokens.Role.attention
    ChipTone.Live -> OrreryTokens.Role.live
    ChipTone.Engine -> OrreryTokens.Role.engine
    ChipTone.New -> OrreryTokens.Role.new
}

private fun chipFill(tone: ChipTone): Color = when (tone) {
    ChipTone.Neutral -> OrreryTokens.Surface.raised
    ChipTone.Ok -> OrreryTokens.Role.okFill
    ChipTone.Bad -> OrreryTokens.Role.badFill
    ChipTone.Attention -> OrreryTokens.Role.attentionFill
    ChipTone.Live -> OrreryTokens.Role.liveFill
    ChipTone.Engine -> OrreryTokens.Role.engineFill
    ChipTone.New -> OrreryTokens.Role.newFill
}

private fun chipLine(tone: ChipTone): Color = when (tone) {
    ChipTone.Neutral -> OrreryTokens.Line.firm
    ChipTone.Ok -> OrreryTokens.Role.okLine
    ChipTone.Bad -> OrreryTokens.Role.badLine
    ChipTone.Attention -> OrreryTokens.Role.attentionLine
    ChipTone.Live -> OrreryTokens.Role.liveLine
    ChipTone.Engine -> OrreryTokens.Role.engineLine
    ChipTone.New -> OrreryTokens.Role.newLine
}
