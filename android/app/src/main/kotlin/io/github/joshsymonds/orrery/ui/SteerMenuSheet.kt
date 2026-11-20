package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.disabled
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SteerResult
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

private const val NO_ANSWER = "The server did not answer, so the move may have gone through. Reading the epic again…"
private const val NEW_ROW_LEAD = "starts with the contract and the epic’s log"

/** Where a steer stands: free to choose, sending, refused with orreryd's reason, or unanswered until the epic is read again. */
sealed interface SteerState {
    data object Ready : SteerState
    data object Sending : SteerState
    data class Refused(val message: String) : SteerState
    data object Unanswered : SteerState
}

/**
 * One epic's steer: [state] and the steering the latest re-read named, null before any re-read. [choose] sends
 * Epics.Move to a session, or Epics.Continue for null, and does nothing while a send is out or unanswered.
 */
class SteerSend internal constructor(
    private val client: OrreryClient,
    private val epicId: String,
    private val scope: CoroutineScope,
    private val onSteered: (String) -> Unit,
) {
    var state: SteerState by mutableStateOf(SteerState.Ready)
        private set

    var steering: String? by mutableStateOf(null)
        private set

    fun choose(session: String?) {
        if (state == SteerState.Sending || state == SteerState.Unanswered) return
        state = SteerState.Sending
        scope.launch {
            val answer = try {
                withContext(Dispatchers.IO) {
                    if (session == null) client.continueEpic(epicId) else client.moveEpic(epicId, session)
                }
            } catch (error: IOException) {
                state = SteerState.Unanswered
                val named = readSteering()
                if (named != null) {
                    steering = named
                    state = SteerState.Ready
                }
                return@launch
            }
            when (answer) {
                is SteerResult.Refused -> state = SteerState.Refused(answer.message)
                is SteerResult.Steered -> {
                    val named = readSteering()
                    state = SteerState.Ready
                    if (named != null) steering = named
                    val target = named ?: answer.session
                    if (target.isNotEmpty()) onSteered(target)
                }
            }
        }
    }

    /** The steering the epic's re-read names, or null when the re-read gets no answer. */
    private suspend fun readSteering(): String? = try {
        withContext(Dispatchers.IO) { client.epic(epicId)?.epic?.steering.orEmpty() }
    } catch (error: IOException) {
        null
    }
}

/** Remembers the [SteerSend] of [epicId] for the composition, and reports each steering it settles on to [onSteered]. */
@Composable
fun rememberSteerSend(client: OrreryClient, epicId: String, onSteered: (String) -> Unit): SteerSend {
    val scope = rememberCoroutineScope()
    val latestOnSteered by rememberUpdatedState(onSteered)
    return remember(client, epicId) {
        SteerSend(client, epicId, scope) { latestOnSteered(it) }
    }
}

/** The conversations a steer can take an epic to, read off the main thread. */
private class SteerConversations(val panes: List<Pane>, val archived: Set<String>)

/** Every pane and headless session, with the sessions the owner has archived. */
private fun readConversations(client: OrreryClient): SteerConversations {
    val headless = client.headless()
    return SteerConversations(
        panes = client.panes() + headless,
        archived = client.archivedSessions() + headless.filter { it.archived }.map { it.id },
    )
}

/**
 * The steering menu, a bottom sheet headed `Steer <slug> from`. It lists the conversation that steers the epic, marked,
 * then the live conversations of the epic's project and a new conversation. A row sends [send] its choice, and the
 * sheet's rows wait while a send is out. When the conversations cannot be read, only the new conversation is offered.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SteerMenuSheet(
    client: OrreryClient,
    epicId: String,
    slug: String,
    repository: String,
    steering: String,
    send: SteerSend,
    onDismiss: () -> Unit,
) {
    var conversations by remember { mutableStateOf<SteerConversations?>(null) }
    LaunchedEffect(client, epicId) {
        conversations = try {
            withContext(Dispatchers.IO) { readConversations(client) }
        } catch (error: IOException) {
            null
        }
    }
    val busy = send.state == SteerState.Sending || send.state == SteerState.Unanswered
    val choices = conversations?.let { steerChoices(it.panes, it.archived, send.steering ?: steering, repository) }

    fun offer(session: String?): (() -> Unit)? = if (busy) null else ({ send.choose(session) })

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .testTag("steer-menu")
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            Text("Steer $slug from", style = OrreryTokens.Type.title)
            choices?.current?.let { steerer ->
                SteerRow("steer-current", steerer.label.ifEmpty { steerer.command }, "steers it now", onClick = null)
            }
            choices?.others?.forEach { pane ->
                SteerRow(
                    tag = "steer-row-${pane.orreryId}",
                    title = pane.label.ifEmpty { pane.command },
                    detail = "${lastPathElement(pane.cwd)} · ${pane.state}",
                    onClick = offer(pane.orreryId),
                )
            }
            SteerRow(
                tag = "steer-new",
                title = "A new conversation in ${lastPathElement(repository)}",
                detail = NEW_ROW_LEAD,
                onClick = offer(null),
            )
            when (val state = send.state) {
                is SteerState.Refused -> SteerNote(state.message)
                SteerState.Unanswered -> SteerNote(NO_ANSWER)
                SteerState.Ready, SteerState.Sending -> Unit
            }
        }
    }
}

/** One row of the menu: its [title] and [detail] lines. A row with no [onClick] is marked disabled. */
@Composable
private fun SteerRow(tag: String, title: String, detail: String, onClick: (() -> Unit)?) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.eight)
            .let { if (onClick != null) it.clickable(onClick = onClick) else it }
            .testTag(tag)
            .semantics(mergeDescendants = true) { if (onClick == null) disabled() },
    ) {
        Text(text = title, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.one)
        Text(text = detail, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
    }
}

@Composable
private fun SteerNote(message: String) {
    Text(
        text = message,
        style = OrreryTokens.Type.small,
        color = OrreryTokens.Role.bad,
        modifier = Modifier.testTag("steer-note"),
    )
}
