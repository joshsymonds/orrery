package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The foot bar of an epic page: a full-width button that opens the conversation steering the epic, or, when no
 * conversation steers it, a line saying so. [steering] is the steerer's session id, empty for none.
 */
@Composable
fun EpicTalkBar(steering: String, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val placed = modifier
        .fillMaxWidth()
        .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight)
    if (steering.isEmpty()) {
        Text(
            "No conversation steers this epic",
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
            modifier = placed.testTag("epic-talk-none"),
        )
    } else {
        Button(
            onClick = onOpen,
            modifier = placed.testTag("epic-talk-bar"),
            shape = RoundedCornerShape(OrreryTokens.Radius.two),
            colors = ButtonDefaults.buttonColors(
                containerColor = OrreryTokens.Surface.raised,
                contentColor = OrreryTokens.Ink.one,
            ),
            border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
            elevation = null,
        ) {
            Text("Talk to the session that steers it", style = OrreryTokens.Type.small)
        }
    }
}
