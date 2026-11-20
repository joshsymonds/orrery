package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.rememberLazyListState
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
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val TAG = "SessionScreen"
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val HEAD_POLL_MILLIS = 1_000L
private const val CLOCK_TICK_MILLIS = 1_000L
private const val RESULT_TAG = "session-result"
private const val RESULT_TEXT = "Interrupted"
private const val TEXT_KIND = "text"
private const val QUEUE_HINT = "Queue for Claude…"
private const val ANSWER_HINT = "Answer Claude…"
private const val MESSAGE_HINT = "Message Claude…"
private const val NUDGE_RUNNING_HINT = "Nudge the step: it interrupts the turn, then prompts"
private const val NUDGE_HELD_HINT = "Nudge the step: it resumes with your text"
private const val WORKING_STATE = "working"
private const val WAITING_STATE = "waiting"

/** The unfinished main-thread text blocks that have text so far, each as a reply after the items. */
private fun unfinishedReplies(held: StreamedConversation): List<ConvoEntry> =
    held.unfinished
        .filter { it.parentToolUseId.isEmpty() && it.kind == TEXT_KIND && it.text.isNotEmpty() }
        .map { ConvoEntry.Reply(it.text) }

/**
 * The composer's hint says what sending does. A step's nudge interrupts a running turn, or resumes a held
 * step; a session's send queues while it works, answers while it waits, and messages otherwise.
 */
private fun composeHint(step: StepMode?, state: String): String = when {
    step == null -> when (state) {
        WORKING_STATE -> QUEUE_HINT
        WAITING_STATE -> ANSWER_HINT
        else -> MESSAGE_HINT
    }
    step.held -> NUDGE_HELD_HINT
    else -> NUDGE_RUNNING_HINT
}

private fun currentSeconds(): Long = System.currentTimeMillis() / 1000

/**
 * A labelled step's session on the step screen: its owner's calls are Hold and Stop, and a message to it
 * is a nudge. [held] is the step's held state, so a held step shows no Hold button.
 */
data class StepMode(val held: Boolean)

/**
 * A headless session's screen: its conversation, streamed while the screen is started. Each delta is
 * folded into what the screen holds, and a failed stream reconnects after the seq it holds, so the
 * conversation resumes where it stopped. A stream that ends without error reconnects the same way.
 *
 * The screen shows the session's head, its conversation with each unfinished main-thread text block
 * as a reply after the items, and the open request as a card after the last item. The head's epics and
 * host are read while the screen is started, and the head folds as the pane screen's does. Interrupt and Stop
 * sit above the compose box while the session is working or waiting. Once an Interrupt is taken, the
 * screen shows Interrupted above the compose box after the session leaves working and waiting, until it
 * works again. Send posts the draft as a prompt through [SessionSender]. Reading the conversation marks
 * the session seen.
 *
 * With [step] set the screen is a step's: Hold and Stop replace Interrupt and Stop and post to the
 * step, the open request card is not drawn, and Send posts the draft as a nudge through [StepCalls].
 */
@Composable
fun SessionScreen(
    sessionId: String,
    client: OrreryClient,
    onBack: () -> Unit = {},
    onOpenEpic: (String) -> Unit = {},
    step: StepMode? = null,
) {
    var streamed by remember(sessionId) { mutableStateOf<StreamedConversation?>(null) }
    var refreshFailed by remember(sessionId) { mutableStateOf(false) }
    var seenAtOpen by remember(sessionId) { mutableStateOf<Long?>(null) }
    var interruptTaken by remember(sessionId) { mutableStateOf(false) }
    var headEpics by remember(sessionId) { mutableStateOf(HeadEpics(emptyList(), emptyMap())) }
    var servingHost by remember(sessionId) { mutableStateOf("") }
    var stats by remember(sessionId) { mutableStateOf<List<HostStat>?>(null) }
    var nowSeconds by remember { mutableLongStateOf(currentSeconds()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    val sender = remember(sessionId) { SessionSender(client, sessionId) }
    val stepCalls = remember(sessionId) { StepCalls(client, sessionId) }

    LaunchedEffect(Unit) {
        while (true) {
            nowSeconds = currentSeconds()
            delay(CLOCK_TICK_MILLIS)
        }
    }

    DisposableEffect(sessionId, lifecycle) {
        var stream: Job? = null
        var headPoll: Job? = null
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
                    headPoll = scope.launch {
                        while (true) {
                            // The epics read is before the host read, as the pane screen's poll reads them.
                            val epics = fetchHeadEpics(client, sessionId)
                            val host = fetchHost(client)
                            epics?.let { headEpics = it }
                            host?.let {
                                servingHost = it.host
                                stats = it.stats
                            }
                            delay(HEAD_POLL_MILLIS)
                        }
                    }
                }
                Lifecycle.Event.ON_STOP -> {
                    stream?.cancel()
                    stream = null
                    headPoll?.cancel()
                    headPoll = null
                }
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            stream?.cancel()
            headPoll?.cancel()
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
    val footer: (@Composable () -> Unit)? = held?.request?.takeIf { step == null }?.let { open ->
        val content: @Composable () -> Unit = {
            SessionRequestCard(sessionId, open.id, open.question, open.options, client, onAnswered = {})
        }
        content
    }

    val headHidden = rememberHeadHidden()
    val listState = rememberLazyListState()
    var tapped by remember(sessionId) { mutableStateOf(false) }
    // Whether the reader is at the newest line, as the conversation view follows it.
    var atNewest by remember(sessionId) { mutableStateOf(true) }
    // A tap on the folded line holds only until the reader next moves between the newest line and the rest.
    LaunchedEffect(atNewest) { tapped = false }
    val fold = headFold(hidden = headHidden.value, atNewest = atNewest, tapped = tapped)
    val steered = headEpics.steered
    val lines = askLines(steered, headEpics.details)
    val asking = askingWords(ownName = pane?.takeIf { ownAsks(it) }?.let { lastPathElement(it.cwd) }, lines = lines)
    val otherHost = pane?.let { otherHostOf(it.host, servingHost) }

    Column(modifier = Modifier.fillMaxSize()) {
        if (refreshFailed && pane == null) {
            Text(REFRESH_FAILED, modifier = Modifier.padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two))
        }
        pane?.let { shown ->
            Head(
                dir = lastPathElement(shown.cwd),
                host = shown.host.ifEmpty { servingHost },
                title = shown.label.ifEmpty { shown.command },
                context = conversation?.context,
                note = if (refreshFailed) REFRESH_FAILED else "",
                stats = if (otherHost == null) stats else null,
                otherHost = otherHost,
                goal = conversation?.goal.orEmpty(),
                lines = lines,
                asking = asking,
                badges = headBadges(steered, lines, conversation?.background.orEmpty()),
                fold = fold,
                onBack = onBack,
                onOpenEpic = onOpenEpic,
                onHide = { headHidden.value = true },
                onShow = {
                    headHidden.value = false
                    tapped = true
                },
                modifier = Modifier.fillMaxWidth().background(OrreryTokens.Surface.page),
            )
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            if (held != null) {
                ConversationView(
                    entries,
                    waiting = held.conversation.waiting,
                    listState = listState,
                    onFollowing = { atNewest = it },
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
        Box(modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two)) {
            if (step != null) {
                StepActions(stepCalls, step.held)
            } else {
                SessionActions(
                    sessionId,
                    state,
                    client,
                    onChanged = {},
                    onTaken = { call -> if (call == LiveCall.INTERRUPT) interruptTaken = true },
                )
            }
        }
        if (step == null && interruptTaken && pane != null && state !in LIVE_STATES) {
            Text(RESULT_TEXT, modifier = Modifier.testTag(RESULT_TAG).padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two), color = OrreryTokens.Role.halt)
        }
        // The compose key is the session's id; its sends go through the session sender, or a step's nudge.
        ComposeBox(
            sessionId,
            client,
            edge = if (step?.held == true) ComposeEdge.HELD else composeEdgeOf(state),
            hint = composeHint(step, state),
            send = { text -> if (step != null) stepCalls.nudge(text) else sender.send(text) },
        )
    }
}
