package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** A note's tone: Quiet is plain, Ok a success, Error a failure. */
enum class NoteTone { Quiet, Ok, Error }

/** Semantics key: the tone of an [InlineNote], on its text node. */
val NoteToneKey = SemanticsPropertyKey<NoteTone>("NoteTone")

/** A line of small text in the tone's colour, for a note under a control or a section. */
@Composable
fun InlineNote(text: String, tone: NoteTone, modifier: Modifier = Modifier) {
    Text(
        text,
        modifier = modifier.semantics { set(NoteToneKey, tone) },
        color = noteColor(tone),
        style = OrreryTokens.Type.small,
    )
}

private fun noteColor(tone: NoteTone): Color = when (tone) {
    NoteTone.Quiet -> OrreryTokens.Ink.three
    NoteTone.Ok -> OrreryTokens.Role.ok
    NoteTone.Error -> OrreryTokens.Role.bad
}
