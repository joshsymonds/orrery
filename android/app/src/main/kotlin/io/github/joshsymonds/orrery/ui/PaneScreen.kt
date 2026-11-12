package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateIntAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

private const val TAG = "PaneScreen"
private const val POLL_INTERVAL_MILLIS = 1_000L
private const val HEADER_SLIDE_MILLIS = 220
private const val QUICK_KEYS_FOLD_MILLIS = 180
private val BODY_GAP_BELOW_HEADER = 6.dp
private val GOAL_STATUS_GAP = 2.dp

/** The draft field never takes more than this share of the screen above the keyboard. */
private const val COMPOSE_MAX_SHARE = 1f / 3f

/** Nor less than this, so a very short screen still shows a line or two of the draft. */
private val COMPOSE_MIN_CAP = 48.dp

/** One tick's pane reads. `conversation` is null when the pane has none. */
private class Snapshot(val pane: Pane?, val conversation: Conversation?)

/** A `Host.Status` read. `stats` is null when the host answers not_found, which clears the meters. */
private class HostRead(val stats: List<HostStat>?)

/**
 * One tick's pane reads: the pane list, then the conversation when the pane's state is non-empty.
 * Null when a call fails, so the tick keeps what was shown.
 */
private suspend fun fetchSnapshot(client: OrreryClient, paneId: String): Snapshot? =
    withContext(Dispatchers.IO) {
        try {
            val pane = client.panes().firstOrNull { it.id == paneId }
            val conversation = if (pane == null || pane.state.isEmpty()) null else client.conversation(paneId)
            Snapshot(pane, conversation)
        } catch (e: IOException) {
            null
        }
    }

/** One `Host.Status` read. Null when the call fails, so the meters keep their last stats. */
private suspend fun fetchHost(client: OrreryClient): HostRead? =
    withContext(Dispatchers.IO) {
        try {
            HostRead(client.host()?.stats)
        } catch (e: IOException) {
            null
        }
    }

/**
 * A pane, refreshed every second while the screen is at least started. A pane with a conversation
 * shows it, with the question card after the last item; a pane without one shows a line saying so,
 * with the card below it. A pane with a conversation shows its status line at the top of the
 * composer band, always in view, with the pane's active goal, when it has one, on a line just
 * above it. Each newer read time the conversation reports is sent to the server as the pane's
 * read-up-to time.
 *
 * The header overlays the body, clipped to the body's bounds, and slides away while the reader
 * scrolls towards newer content; it returns on a short scroll back, and always shows at the top
 * and the newest end. While the software keyboard is up the header folds away entirely, and the
 * draft field is capped at a third of the screen above the keyboard, so the conversation keeps the
 * room between them. The quick keys hide while the draft has text, so a reply in progress is one
 * row above the keyboard.
 */
@Composable
fun PaneScreen(
    paneId: String,
    client: OrreryClient,
    modifier: Modifier = Modifier,
    onBack: () -> Unit = {},
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var pane by remember(paneId) { mutableStateOf<Pane?>(null) }
    var conversation by remember(paneId) { mutableStateOf<Conversation?>(null) }
    var stats by remember(paneId) { mutableStateOf<List<HostStat>?>(null) }
    var nowSeconds by remember(paneId) { mutableLongStateOf(0L) }
    var refreshFailed by remember(paneId) { mutableStateOf(false) }
    var seenAtOpen by remember(paneId) { mutableStateOf<Long?>(null) }
    val refreshRequests = remember(paneId) { Channel<Unit>(Channel.CONFLATED) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()

    DisposableEffect(paneId, lifecycle) {
        var poller: Job? = null
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    poller = scope.launch {
                        while (true) {
                            val snapshot = fetchSnapshot(client, paneId)
                            // Read before the snapshot's conversation is shown, so its time rows
                            // never see the zero clock the state starts with.
                            nowSeconds = clock()
                            val host = fetchHost(client)
                            if (snapshot == null) {
                                refreshFailed = true
                            } else {
                                pane = snapshot.pane
                                // Kept from the first read that finds the pane, so a later read's newer
                                // seen time does not move the divider.
                                seenAtOpen = seenAtOpen ?: snapshot.pane?.seen
                                conversation = snapshot.conversation
                                refreshFailed = false
                            }
                            host?.let { stats = it.stats }
                            withTimeoutOrNull(POLL_INTERVAL_MILLIS) { refreshRequests.receive() }
                        }
                    }
                }
                Lifecycle.Event.ON_STOP -> {
                    poller?.cancel()
                    poller = null
                }
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            poller?.cancel()
        }
    }

    val current = pane
    val question = current?.takeIf { it.question.isNotEmpty() }
    val refresh: () -> Unit = { refreshRequests.trySend(Unit) }
    val entries = remember(conversation) { foldConversation(conversation?.items.orEmpty()) }

    val density = LocalDensity.current
    val headerRule = remember(paneId) {
        with(density) { HeaderScrollRule(HEADER_HIDE_AFTER_DP.dp.toPx(), HEADER_SHOW_AFTER_DP.dp.toPx()) }
    }
    val showingConversation = conversation != null
    // Keyed by pane, so a pane switch starts at the newest line instead of where another pane was left.
    val listState = remember(paneId) { LazyListState() }
    var endRequests by remember(paneId) { mutableIntStateOf(0) }
    var headerHeightPx by remember { mutableIntStateOf(0) }
    var headerMeasured by remember { mutableStateOf(false) }
    val headerShift by animateIntAsState(
        targetValue = if (headerRule.visible) 0 else -headerHeightPx,
        animationSpec = tween(HEADER_SLIDE_MILLIS),
        label = "header-shift",
    )
    val bodyTop = with(density) { headerHeightPx.toDp() } + BODY_GAP_BELOW_HEADER
    val ime = WindowInsets.ime
    val keyboardUp by remember(ime, density) { derivedStateOf { ime.getBottom(density) > 0 } }
    var screenHeightPx by remember { mutableIntStateOf(0) }
    var draftPresent by remember(paneId) { mutableStateOf(false) }
    val composeCap = if (screenHeightPx > 0) {
        maxOf(with(density) { screenHeightPx.toDp() } * COMPOSE_MAX_SHARE, COMPOSE_MIN_CAP)
    } else {
        Dp.Unspecified
    }
    val bodyReady = current == null || headerMeasured
    val headerScroll = remember(headerRule, listState) {
        object : NestedScrollConnection {
            override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
                headerRule.onScroll(
                    deltaPx = -consumed.y,
                    atTop = !listState.canScrollBackward,
                    atBottom = !listState.canScrollForward,
                )
                return Offset.Zero
            }
        }
    }
    LaunchedEffect(headerRule, listState) {
        snapshotFlow { listState.canScrollBackward to listState.canScrollForward }.collect { (canBack, canForward) ->
            if (!canBack || !canForward) headerRule.onScroll(0f, atTop = !canBack, atBottom = !canForward)
        }
    }

    Column(modifier = modifier.fillMaxSize().onSizeChanged { screenHeightPx = it.height }) {
        if (refreshFailed) {
            Text("could not refresh")
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds().nestedScroll(headerScroll)) {
            // The body waits for the header's height, so its first layout already clears the header.
            // Later changes of the padding (the header folding for the keyboard) are followed by
            // the conversation while the reader is at the newest end.
            if (bodyReady && showingConversation) {
                val footer: (@Composable () -> Unit)? = question?.let { card ->
                    val content: @Composable () -> Unit = { QuestionCard(card, client, onRefresh = refresh) }
                    content
                }
                ConversationView(
                    entries,
                    working = current?.state == "working",
                    waiting = conversation?.waiting.orEmpty(),
                    listState = listState,
                    contentPadding = PaddingValues(top = bodyTop),
                    footer = footer,
                    endRequests = endRequests,
                    seen = seenAtOpen,
                    now = nowSeconds,
                    onRead = { readUpTo ->
                        scope.launch(Dispatchers.IO) {
                            try {
                                client.markSeen(paneId, readUpTo)
                            } catch (e: IOException) {
                                Log.w(TAG, "could not mark $paneId seen", e)
                            }
                        }
                    },
                )
            } else if (bodyReady && current != null) {
                Column(modifier = Modifier.fillMaxSize()) {
                    Text(
                        text = "no conversation",
                        modifier = Modifier
                            .testTag("no-conversation")
                            .weight(1f)
                            .fillMaxWidth()
                            .padding(start = 14.dp, end = 14.dp, top = bodyTop),
                    )
                    if (question != null) {
                        QuestionCard(question, client, onRefresh = refresh)
                    }
                }
            }
            current?.let { shown ->
                PaneHeader(
                    pane = shown,
                    origin = conversation?.origin,
                    stats = stats,
                    context = conversation?.context,
                    onBack = onBack,
                    compact = keyboardUp,
                    modifier = Modifier
                        .align(Alignment.TopCenter)
                        .onSizeChanged {
                            headerHeightPx = it.height
                            headerMeasured = true
                        }
                        .offset { IntOffset(0, headerShift) },
                )
            }
        }
        ComposeBox(
            paneId,
            client,
            maxFieldHeight = composeCap,
            hint = when {
                current == null -> ""
                current.state.isEmpty() -> "Type a command…"
                else -> "Message Claude…"
            },
            onDraftPresent = { draftPresent = it },
            onSend = { endRequests++ },
            status = current?.takeIf { conversation != null }?.let { shown ->
                {
                    Column(verticalArrangement = Arrangement.spacedBy(GOAL_STATUS_GAP)) {
                        GoalLine(conversation?.goal.orEmpty())
                        StatusLine(shown, nowSeconds)
                    }
                }
            },
        )
        AnimatedVisibility(
            visible = !draftPresent,
            enter = expandVertically(tween(QUICK_KEYS_FOLD_MILLIS)) + fadeIn(tween(QUICK_KEYS_FOLD_MILLIS)),
            exit = shrinkVertically(tween(QUICK_KEYS_FOLD_MILLIS)) + fadeOut(tween(QUICK_KEYS_FOLD_MILLIS)),
            label = "quick-keys",
        ) {
            QuickKeys(paneId, client)
        }
    }
}
