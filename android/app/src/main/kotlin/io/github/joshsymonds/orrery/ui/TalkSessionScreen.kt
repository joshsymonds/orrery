package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The conversation a Talk row opens: the epic's line pinned on one line at the top, where a tap returns
 * to the epic through [onBack]. Below it is the target's own screen: [SessionScreen] for a session,
 * [PaneScreen] for a pane.
 */
@Composable
fun TalkSessionScreen(epicLine: String, target: TalkTarget, client: OrreryClient, onBack: () -> Unit) {
    Column(modifier = Modifier.fillMaxSize()) {
        Text(
            text = epicLine,
            modifier = Modifier
                .testTag("talk-epic-line")
                .fillMaxWidth()
                .clickable(onClick = onBack)
                .padding(
                    horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                    vertical = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                ),
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Box(modifier = Modifier.weight(1f).fillMaxWidth()) {
            when (target.kind) {
                TalkKind.SESSION -> SessionScreen(target.id, client, onBack)
                TalkKind.PANE -> PaneScreen(target.id, client, onBack = onBack)
            }
        }
    }
}
