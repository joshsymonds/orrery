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
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private const val TALK_LABEL = "Talk to the session that steers it"
private const val NO_STEERER = "No conversation steers this epic"
private const val STEER_ITEM = "Steer it from another conversation"

/** Which sheet the foot has open: the commands, the confirm for Stop, or the steering menu. */
private enum class FootSheet { Commands, Stop, Steer }

/**
 * The foot of an epic's screen. Its top is the ended bar when [detail] is a live epic orreryd runs whose steering has
 * ended: with the unsteered lead when no conversation steers it, and with the ended lead once the conversations are
 * read and its steerer has stopped, failed or is no longer listed. Under it, or alone otherwise, is the bar: `Talk to
 * the session that steers it` (when a steerer is known and has not ended; [onOpenSteerer] opens it), or a quiet line
 * when no conversation steers an epic that cannot be steered, and a More button when the epic offers any command.
 *
 * More opens a sheet titled with the slug. It lists `Steer it from another conversation` for a live orreryd epic,
 * which opens the steering menu, then the commands allowed: Pause and Resume call their callback once and close it;
 * Stop… moves to a confirm sheet, and only its confirm calls [onStop], once. [onSteered] hears each steering that the
 * ended bar or the steering menu settles on.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EpicFoot(
    client: OrreryClient,
    detail: EpicDetail,
    onOpenSteerer: () -> Unit,
    onSteered: (String) -> Unit,
    canPause: Boolean,
    canResume: Boolean,
    canStop: Boolean,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onStop: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val epic = detail.epic
    val steerable = steersLive(epic)
    val ended = steeringEnded(client, detail)
    val endedLead = when {
        !steerable -> null
        epic.steering.isEmpty() -> unsteeredLead(epic.slug)
        ended == true -> endedLead(epic.slug)
        else -> null
    }
    var sheet by remember { mutableStateOf<FootSheet?>(null) }
    val scope = rememberCoroutineScope()
    val commandsState = rememberModalBottomSheetState()
    val stopState = rememberModalBottomSheetState()
    val send = rememberSteerSend(client, epic.id) { steering ->
        sheet = null
        onSteered(steering)
    }
    // A sheet hides itself, then leaves the tree; [next] opens once it has hidden.
    fun close(state: SheetState, next: FootSheet? = null) {
        scope.launch { state.hide() }.invokeOnCompletion { sheet = next }
    }
    val hasMore = steerable || canPause || canResume || canStop
    Column(modifier = modifier.fillMaxWidth()) {
        endedLead?.let { lead ->
            EndedBar(
                client = client,
                epic = epic,
                repository = detail.repository,
                lead = lead,
                onSteered = onSteered,
            )
        }
        val talk = endedLead == null && epic.steering.isNotEmpty()
        val none = endedLead == null && epic.steering.isEmpty()
        if (talk || none || hasMore) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                when {
                    talk -> FootButton(TALK_LABEL, "epic-talk-bar", Modifier.weight(1f), onOpenSteerer)
                    none -> Text(
                        NO_STEERER,
                        style = OrreryTokens.Type.small,
                        color = OrreryTokens.Ink.three,
                        modifier = Modifier.weight(1f).testTag("epic-talk-none"),
                    )
                    else -> Spacer(Modifier.weight(1f))
                }
                if (hasMore) {
                    FootButton("More", "epic-more", Modifier, onClick = { sheet = FootSheet.Commands })
                }
            }
        }
    }
    when (sheet) {
        FootSheet.Commands -> ModalBottomSheet(sheetState = commandsState, onDismissRequest = { sheet = null }) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(epic.slug, style = OrreryTokens.Type.small, modifier = Modifier.testTag("epic-more-title"))
                if (steerable) {
                    TextButton(
                        onClick = { close(commandsState, next = FootSheet.Steer) },
                        modifier = Modifier.fillMaxWidth().testTag("epic-steer"),
                    ) {
                        Text(STEER_ITEM)
                    }
                }
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
                    "Stop ${epic.slug}?",
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
                    Text("Stop ${epic.slug}")
                }
                TextButton(
                    onClick = { close(stopState) },
                    modifier = Modifier.fillMaxWidth().testTag("epic-stop-keep"),
                ) {
                    Text("Keep it running")
                }
            }
        }
        FootSheet.Steer -> SteerMenuSheet(
            client = client,
            epicId = epic.id,
            slug = epic.slug,
            repository = detail.repository,
            steering = epic.steering,
            send = send,
            onDismiss = { sheet = null },
        )
        null -> Unit
    }
}

/**
 * Whether the conversation steering [detail]'s epic has ended, read off the main thread: true once it is read as
 * stopped, failed or unlisted, false once it is read as running, and null until that read answers, when it fails, or
 * when the epic is not a live orreryd epic with a steerer, which reads nothing.
 */
@Composable
private fun steeringEnded(client: OrreryClient, detail: EpicDetail): Boolean? {
    val epic = detail.epic
    val reads = steersLive(epic) && epic.steering.isNotEmpty()
    var ended by remember(client, epic.steering, reads) { mutableStateOf<Boolean?>(null) }
    LaunchedEffect(client, epic.steering, reads) {
        if (!reads) return@LaunchedEffect
        ended = try {
            withContext(Dispatchers.IO) { conversationEnded(client.panes() + client.headless(), epic.steering) }
        } catch (error: IOException) {
            null
        }
    }
    return ended
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
