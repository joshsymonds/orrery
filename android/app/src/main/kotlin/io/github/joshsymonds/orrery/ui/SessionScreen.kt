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
import kotlinx.coroutines.withContext

private const val TAG = "SessionScreen"
private const val RECONNECT_DELAY_MILLIS = 1_000L
private const val HEAD_POLL_MILLIS = 1_000L
private const val CLOCK_TICK_MILLIS = 1_000L
private const val TEXT_KIND = "text"
private val ENDED_STATES = setOf(STEER_STOPPED, STEER_FAILED)

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
 * The screen shows the session's head, under it while it is open the rail strip of the epics the session steers and
 * its running tasks (a tile of an epic calls [onOpenEpic] with its id), its conversation with each unfinished main-thread text block
 * as a reply after the items, and the open request's Ask after the last item. The head's epics and
 * host are read while the screen is started, and the head folds as the pane screen's does. The dock under the
 * conversation says the session's state from the stream, with its controls and its queue; Send posts the
 * draft as a prompt through [SessionSender]. Reading the conversation marks the session seen.
 *
 * With [step] set the screen is a step's: the dock's Hold and Stop post to the step, the open request's Ask
 * is not drawn, and Send posts the draft as a nudge through [StepCalls]. [reason] is the listed row's summary,
 * which the dock shows when the session failed.
 *
 * A headless session that has stopped or failed, or a session no pane or headless session carries any more, while it
 * steers a live orreryd epic has no way to answer that epic's questions, so the [EndedBar] of the epic takes the dock's
 * place. [onSteered] hears each steering
 * the bar settles on.
 */
@Composable
fun SessionScreen(
    sessionId: String,
    client: OrreryClient,
    onBack: () -> Unit = {},
    onOpenEpic: (String) -> Unit = {},
    step: StepMode? = null,
    reason: String = "",
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
    onSteered: (String) -> Unit = {},
) {
    var streamed by remember(sessionId) { mutableStateOf<StreamedConversation?>(null) }
    var refreshFailed by remember(sessionId) { mutableStateOf(false) }
    // Whether no pane or headless session carries this session any more, read once its watch has failed. An unlisted
    // session has ended (P4), so one that steers a live epic draws the ended bar.
    var unlisted by remember(sessionId) { mutableStateOf(false) }
    var seenAtOpen by remember(sessionId) { mutableStateOf<Long?>(null) }
    // Whether the strip under the band has tiles: then it, not the band, carries the control that hides the header.
    var stripTiles by remember(sessionId) { mutableStateOf(false) }
    var headEpics by remember(sessionId) { mutableStateOf(HeadEpics(emptyList(), emptyMap())) }
    var servingHost by remember(sessionId) { mutableStateOf("") }
    var stats by remember(sessionId) { mutableStateOf<List<HostStat>?>(null) }
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

    LaunchedEffect(sessionId, refreshFailed) {
        unlisted = refreshFailed && try {
            withContext(Dispatchers.IO) { steererOf(client.panes() + client.headless(), sessionId) == null }
        } catch (e: IOException) {
            false
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
    val entries = remember(held) {
        held?.let { foldConversation(it.shownItems()) + unfinishedReplies(it) }.orEmpty()
    }
    val ask = if (step == null) held?.request?.let { open -> pane?.let { sessionAsk(it, open) } } else null
    // The stream redraws the session once its answer lands, so the Ask needs no refresh of its own.
    val footer: @Composable () -> Unit = {
        AskHost(model = ask, client = client, now = nowSeconds, onRefresh = {})
    }
    val draft = rememberDockDraft(sessionId)

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
                bandHides = !stripTiles,
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
            if (fold == HeadFold.OPEN) {
                ConversationStrip(
                    conversation = sessionId,
                    pane = false,
                    background = conversation?.background.orEmpty(),
                    now = nowSeconds,
                    client = client,
                    onOpenEpic = onOpenEpic,
                    onTiles = { stripTiles = it },
                    end = { HideHeader { headHidden.value = true } },
                )
            }
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            if (held != null) {
                ConversationView(
                    entries,
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
        val ended = pane?.state.orEmpty() in ENDED_STATES || (pane == null && unlisted)
        val endedEpic = if (step == null && ended) {
            headEpics.steered.firstOrNull(::steersLive)
        } else {
            null
        }
        if (endedEpic != null) {
            EndedBar(
                client = client,
                epic = endedEpic,
                repository = headEpics.details[endedEpic.id]?.repository.orEmpty(),
                lead = endedLead(endedEpic.slug),
                onSteered = onSteered,
                // The head drops the epic once it moves, and the bar with it, so its calls run in the screen's scope.
                scope = scope,
            )
        } else {
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
}
