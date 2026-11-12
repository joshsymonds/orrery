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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.OrreryClient
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val TAG = "SessionScreen"
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val CLOCK_TICK_MILLIS = 1_000L
private const val REFRESH_FAILED = "could not refresh"
private const val WORKING_STATE = "working"
private const val RESULT_TAG = "session-result"
private const val RESULT_TEXT = "Interrupted"
private const val TEXT_KIND = "text"
private const val COMPOSE_HINT = "Message Claude…"

/** The unfinished main-thread text blocks that have text so far, each as a reply after the items. */
private fun unfinishedReplies(held: StreamedConversation): List<ConvoEntry> =
    held.unfinished
        .filter { it.parentToolUseId.isEmpty() && it.kind == TEXT_KIND && it.text.isNotEmpty() }
        .map { ConvoEntry.Reply(it.text) }

private fun currentSeconds(): Long = System.currentTimeMillis() / 1000

/**
 * A headless session's screen: its conversation, streamed while the screen is started. Each delta is
 * folded into what the screen holds, and a failed stream reconnects after the seq it holds, so the
 * conversation resumes where it stopped. A stream that ends without error reconnects the same way.
 *
 * The screen shows the session's header, its conversation with each unfinished main-thread text block
 * as a reply after the items, and the open request as a card after the last item. Interrupt and Stop
 * sit above the compose box while the session is working or waiting. Once an Interrupt is taken, the
 * screen shows Interrupted above the compose box after the session leaves working and waiting, until it
 * works again. Send posts the draft as a prompt through [SessionSender]. Reading the conversation marks
 * the session seen.
 */
@Composable
fun SessionScreen(sessionId: String, client: OrreryClient, onBack: () -> Unit = {}) {
    var streamed by remember(sessionId) { mutableStateOf<StreamedConversation?>(null) }
    var refreshFailed by remember(sessionId) { mutableStateOf(false) }
    var seenAtOpen by remember(sessionId) { mutableStateOf<Long?>(null) }
    var interruptTaken by remember(sessionId) { mutableStateOf(false) }
    var nowSeconds by remember { mutableLongStateOf(currentSeconds()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    val sender = remember(sessionId) { SessionSender(client, sessionId) }

    LaunchedEffect(Unit) {
        while (true) {
            nowSeconds = currentSeconds()
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
    val state = pane?.state.orEmpty()
    LaunchedEffect(sessionId, state) {
        if (state == WORKING_STATE) interruptTaken = false
    }
    val entries = remember(held) {
        held?.let { foldConversation(it.conversation.items) + unfinishedReplies(it) }.orEmpty()
    }
    val footer: (@Composable () -> Unit)? = held?.request?.let { open ->
        val content: @Composable () -> Unit = {
            SessionRequestCard(sessionId, open.id, open.question, open.options, client, onAnswered = {})
        }
        content
    }

    Column(modifier = Modifier.fillMaxSize()) {
        if (refreshFailed) {
            Text(REFRESH_FAILED, modifier = Modifier.padding(horizontal = 14.dp))
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
                    working = pane?.state == WORKING_STATE,
                    waiting = held.conversation.waiting,
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
        Box(modifier = Modifier.fillMaxWidth().padding(horizontal = 14.dp)) {
            SessionActions(
                sessionId,
                state,
                client,
                onChanged = {},
                onTaken = { call -> if (call == LiveCall.INTERRUPT) interruptTaken = true },
            )
        }
        if (interruptTaken && pane != null && state !in LIVE_STATES) {
            Text(RESULT_TEXT, modifier = Modifier.testTag(RESULT_TAG).padding(horizontal = 14.dp))
        }
        // The compose key is the session's id; its sends go through the session sender.
        ComposeBox(
            sessionId,
            client,
            hint = COMPOSE_HINT,
            send = { text -> sender.send(text) },
        )
    }
}
