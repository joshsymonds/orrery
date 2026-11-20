package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The epic's track: its [stages] in order, as five equal columns, each done, now or ahead. Not clickable. */
@Composable
fun Track(stages: List<Stage>) {
    Row(modifier = Modifier.fillMaxWidth().testTag("track")) {
        stages.forEach { stage -> TrackStage(stage) }
    }
}

@Composable
private fun RowScope.TrackStage(stage: Stage) {
    val name = if (stage.state == StageState.DONE) "✓ ${stage.name}" else stage.name
    Column(
        modifier = Modifier
            .weight(1f)
            .testTag("track-stage:${stage.name}")
            .semantics(mergeDescendants = true) { stateDescription = stage.state.name.lowercase() }
            .padding(end = OrreryTokens.Space.eight),
    ) {
        Text(AnnotatedString(name, SpanStyle(color = nameColourOf(stage))), style = OrreryTokens.Type.label)
        Text(AnnotatedString(stage.words, SpanStyle(color = wordsColourOf(stage))), style = OrreryTokens.Type.small)
    }
}

// nameColourOf is a stage's name colour: its tone when it is now, Ink.two once done, Ink.four ahead.
private fun nameColourOf(stage: Stage): Color = when (stage.state) {
    StageState.NOW -> toneColourOf(stage.tone)
    StageState.DONE -> OrreryTokens.Ink.two
    StageState.AHEAD -> OrreryTokens.Ink.four
}

// wordsColourOf is a stage's words colour: its tone when it is now, Ink.three once done, Ink.four ahead.
private fun wordsColourOf(stage: Stage): Color = when (stage.state) {
    StageState.NOW -> toneColourOf(stage.tone)
    StageState.DONE -> OrreryTokens.Ink.three
    StageState.AHEAD -> OrreryTokens.Ink.four
}

private fun toneColourOf(tone: WordTone): Color = when (tone) {
    WordTone.LIVE -> OrreryTokens.Role.live
    WordTone.ATTENTION -> OrreryTokens.Role.attention
    WordTone.QUIET -> OrreryTokens.Ink.three
    WordTone.ENGINE -> OrreryTokens.Role.engine
    WordTone.OK -> OrreryTokens.Role.ok
    WordTone.BAD -> OrreryTokens.Role.bad
}
