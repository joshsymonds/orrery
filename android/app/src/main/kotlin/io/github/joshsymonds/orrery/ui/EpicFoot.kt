package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.SheetState
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.launch

/** Which sheet the foot has open: the commands, or the confirm for Stop. */
private enum class FootSheet { Commands, Stop }

/**
 * The foot bar of an epic page: a conversation button when [steering] names a session (empty for none), and a
 * More button when any command is allowed. More opens a sheet titled with [slug] that lists the allowed commands:
 * Pause and Resume call their callback once and close it; Stop… moves to a confirm sheet, and only its confirm
 * calls [onStop], once.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EpicFoot(
    slug: String,
    steering: String,
    onTalk: () -> Unit,
    canPause: Boolean,
    canResume: Boolean,
    canStop: Boolean,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onStop: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var sheet by remember { mutableStateOf<FootSheet?>(null) }
    val scope = rememberCoroutineScope()
    val commandsState = rememberModalBottomSheetState()
    val stopState = rememberModalBottomSheetState()
    // A sheet hides itself, then leaves the tree; [next] opens once it has hidden.
    fun close(state: SheetState, next: FootSheet? = null) {
        scope.launch { state.hide() }.invokeOnCompletion { sheet = next }
    }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (steering.isNotEmpty()) {
            FootButton("Talk to the session that steers it", "epic-talk-bar", Modifier.weight(1f), onTalk)
        } else {
            Spacer(Modifier.weight(1f))
        }
        if (canPause || canResume || canStop) {
            FootButton("More", "epic-more", Modifier, onClick = { sheet = FootSheet.Commands })
        }
    }
    when (sheet) {
        FootSheet.Commands -> ModalBottomSheet(sheetState = commandsState, onDismissRequest = { sheet = null }) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(slug, style = OrreryTokens.Type.small, modifier = Modifier.testTag("epic-more-title"))
                if (canPause) {
                    TextButton(
                        onClick = {
                            close(commandsState)
                            onPause()
                        },
                        modifier = Modifier.fillMaxWidth().testTag("epic-sheet-pause"),
                    ) {
                        Text("Pause")
                    }
                }
                if (canResume) {
                    TextButton(
                        onClick = {
                            close(commandsState)
                            onResume()
                        },
                        modifier = Modifier.fillMaxWidth().testTag("epic-sheet-resume"),
                    ) {
                        Text("Resume")
                    }
                }
                if (canStop) {
                    TextButton(
                        onClick = { close(commandsState, next = FootSheet.Stop) },
                        modifier = Modifier.fillMaxWidth().testTag("epic-sheet-stop"),
                    ) {
                        Text("Stop…")
                    }
                }
            }
        }
        FootSheet.Stop -> ModalBottomSheet(sheetState = stopState, onDismissRequest = { sheet = null }) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(
                    "Stop $slug?",
                    style = OrreryTokens.Type.small,
                    modifier = Modifier.testTag("epic-stop-title"),
                )
                Text(
                    "Its running steps are stopped. Nothing is released.",
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                )
                TextButton(
                    onClick = {
                        close(stopState)
                        onStop()
                    },
                    modifier = Modifier.fillMaxWidth().testTag("epic-stop-confirm"),
                ) {
                    Text("Stop $slug")
                }
                TextButton(
                    onClick = { close(stopState) },
                    modifier = Modifier.fillMaxWidth().testTag("epic-stop-keep"),
                ) {
                    Text("Keep it running")
                }
            }
        }
        null -> Unit
    }
}

@Composable
private fun RowScope.FootButton(text: String, tag: String, modifier: Modifier, onClick: () -> Unit) {
    Button(
        onClick = onClick,
        modifier = modifier.testTag(tag),
        shape = RoundedCornerShape(OrreryTokens.Radius.two),
        colors = ButtonDefaults.buttonColors(
            containerColor = OrreryTokens.Surface.raised,
            contentColor = OrreryTokens.Ink.one,
        ),
        border = BorderStroke(OrreryTokens.Size.hairline, OrreryTokens.Line.firm),
        elevation = null,
    ) {
        Text(text, style = OrreryTokens.Type.small)
    }
}
