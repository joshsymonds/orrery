package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val TAG = "SessionScreen"
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val CLOCK_TICK_MILLIS = 1_000L
private const val REFRESH_FAILED = "could not refresh"
private const val TEXT_KIND = "text"

/** The unfinished main-thread text blocks that have text so far, each as a reply after the items. */
private fun unfinishedReplies(held: StreamedConversation): List<ConvoEntry> =
    held.unfinished
        .filter { it.parentToolUseId.isEmpty() && it.kind == TEXT_KIND && it.text.isNotEmpty() }
        .map { ConvoEntry.Reply(it.text) }

/**
 * A labelled step's session on the step screen: its owner's calls are Hold and Stop, and a message to it
 * is a nudge. [held] is the step's held state, so a held step shows no Hold. [label] names the step in the dock,
 * and [doing] says what the step is doing there.
 */
data class StepMode(val held: Boolean, val label: String, val doing: String = "")

/**
 * A headless session's screen: its conversation, streamed while the screen is started. Each delta is
 * folded into what the screen holds, and a failed stream reconnects after the seq it holds, so the
 * conversation resumes where it stopped. A stream that ends without error reconnects the same way.
 *
 * The screen shows the session's header, its conversation with each unfinished main-thread text block
 * as a reply after the items, and the open request as a card after the last item. The dock under the
 * conversation says the session's state from the stream, with its controls and its queue; Send posts the
 * draft as a prompt through [SessionSender]. Reading the conversation marks the session seen.
 *
 * With [step] set the screen is a step's: the dock's Hold and Stop post to the step, the open request card
 * is not drawn, and Send posts the draft as a nudge through [StepCalls]. [reason] is the listed row's summary,
 * which the dock shows when the session failed.
 */
@Composable
fun SessionScreen(
    sessionId: String,
    client: OrreryClient,
    onBack: () -> Unit = {},
    step: StepMode? = null,
    reason: String = "",
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var streamed by remember(sessionId) { mutableStateOf<StreamedConversation?>(null) }
    var refreshFailed by remember(sessionId) { mutableStateOf(false) }
    var seenAtOpen by remember(sessionId) { mutableStateOf<Long?>(null) }
    var nowSeconds by remember { mutableLongStateOf(clock()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    val sender = remember(sessionId) { SessionSender(client, sessionId) }
    val stepCalls = remember(sessionId) { StepCalls(client, sessionId) }

    LaunchedEffect(Unit) {
        while (true) {
            nowSeconds = clock()
            delay(CLOCK_TICK_MILLIS)
        }
    }

    DisposableEffect(sessionId, lifecycle) {
        var stream: Job? = null
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    stream = scope.launch {
                        while (true) {
                            try {
                                client.watchSession(sessionId, after = streamed?.seq ?: 0L) { delta ->
                                    val next = streamed.applyDelta(delta)
                                    streamed = next
                                    seenAtOpen = seenAtOpen ?: next.pane?.seen
                                    refreshFailed = false
                                }
                            } catch (e: IOException) {
                                refreshFailed = true
                            }
                            delay(RECONNECT_DELAY_MILLIS)
                        }
                    }
                }
                Lifecycle.Event.ON_STOP -> {
                    stream?.cancel()
                    stream = null
                }
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            stream?.cancel()
        }
    }

    val held = streamed
    val conversation = held?.conversation
    val pane = held?.pane
    val entries = remember(held) {
        held?.let { foldConversation(it.shownItems()) + unfinishedReplies(it) }.orEmpty()
    }
    val footer: (@Composable () -> Unit)? = held?.request?.takeIf { step == null }?.let { open ->
        val content: @Composable () -> Unit = {
            SessionRequestCard(sessionId, open.id, open.question, open.options, client, onAnswered = {})
        }
        content
    }
    val draft = rememberDockDraft(sessionId)

    Column(modifier = Modifier.fillMaxSize()) {
        if (refreshFailed) {
            Text(REFRESH_FAILED, modifier = Modifier.padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two))
        }
        pane?.let { shown ->
            PaneHeader(
                pane = shown,
                origin = conversation?.origin,
                stats = null,
                context = conversation?.context,
                onBack = onBack,
            )
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            if (held != null) {
                ConversationView(
                    entries,
                    footer = footer,
                    seen = seenAtOpen,
                    now = nowSeconds,
                    onRead = { _ ->
                        scope.launch(Dispatchers.IO) {
                            try {
                                client.sessionSeen(sessionId)
                            } catch (e: IOException) {
                                Log.w(TAG, "could not mark $sessionId seen", e)
                            }
                        }
                    },
                )
            }
        }
        Dock(
            facts = dockFactsOf(
                kind = if (step != null) DockKind.STEP else DockKind.SESSION,
                name = pane?.cwd.orEmpty().trimEnd('/').substringAfterLast('/'),
                pane = pane,
                streamed = held,
                reason = reason,
                step = step?.let { DockStep(it.label, it.held, it.doing) },
            ),
            nowSeconds = nowSeconds,
            client = client,
            draft = draft,
            send = { text -> if (step != null) stepCalls.nudge(text) else sender.send(text) },
            sessionId = sessionId,
            queued = held?.queued.orEmpty(),
            pending = held?.pending.orEmpty(),
            stepCalls = if (step != null) stepCalls else null,
        )
    }
}
