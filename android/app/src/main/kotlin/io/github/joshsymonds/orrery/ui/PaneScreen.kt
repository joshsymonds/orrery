package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.animation.core.animateIntAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.gestures.ScrollableState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLifecycleOwner
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
import org.json.JSONException

private const val TAG = "PaneScreen"
private const val POLL_INTERVAL_MILLIS = 1_000L
private const val HEADER_SLIDE_MILLIS = 220
private val BODY_GAP_BELOW_HEADER = 6.dp

/**
 * One tick's pane reads. `conversation` is null for a terminal pane. `text` is null when the
 * screen was not needed this tick.
 */
private class Snapshot(val pane: Pane?, val conversation: Conversation?, val text: String?)

/** A `/v1/host` read. `stats` is null when the host answers 404, which clears the meters. */
private class HostRead(val stats: List<HostStat>?)

/**
 * One tick's pane reads: the pane list, then the conversation when the pane's state is non-empty,
 * then the screen when it shows as text (a terminal pane, or the live screen). Null when a call
 * fails, so the tick keeps what was shown.
 */
private suspend fun fetchSnapshot(client: OrreryClient, paneId: String, liveScreen: Boolean): Snapshot? =
    withContext(Dispatchers.IO) {
        try {
            val pane = client.panes().firstOrNull { it.id == paneId }
            val conversation = if (pane == null || pane.state.isEmpty()) null else client.conversation(paneId)
            val text = if (liveScreen || conversation == null) client.screen(paneId) else null
            Snapshot(pane, conversation, text)
        } catch (e: IOException) {
            null
        } catch (e: JSONException) {
            null
        }
    }

/** One `/v1/host` read. Null when the call fails, so the meters keep their last stats. */
private suspend fun fetchHost(client: OrreryClient): HostRead? =
    withContext(Dispatchers.IO) {
        try {
            HostRead(client.host()?.stats)
        } catch (e: IOException) {
            null
        } catch (e: JSONException) {
            null
        }
    }

/**
 * A pane, refreshed every second while the screen is at least started. A Claude pane shows its
 * conversation, with the question card after the last item; any other pane shows its terminal
 * text, with the card below it. Tapping the status line opens the live screen, which shows the
 * terminal text until the status line is tapped again or the live-screen bar is tapped. Opening
 * the screen marks the pane seen once.
 *
 * The header overlays the body and slides away while the reader scrolls towards newer content;
 * it returns on a short scroll back, and always shows at the top and the newest end.
 */
@Composable
fun PaneScreen(
    paneId: String,
    client: OrreryClient,
    modifier: Modifier = Modifier,
    onBack: () -> Unit = {},
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var text by remember(paneId) { mutableStateOf("") }
    var pane by remember(paneId) { mutableStateOf<Pane?>(null) }
    var conversation by remember(paneId) { mutableStateOf<Conversation?>(null) }
    var stats by remember(paneId) { mutableStateOf<List<HostStat>?>(null) }
    var nowSeconds by remember(paneId) { mutableLongStateOf(0L) }
    var liveScreen by remember(paneId) { mutableStateOf(false) }
    var refreshFailed by remember(paneId) { mutableStateOf(false) }
    val refreshRequests = remember(paneId) { Channel<Unit>(Channel.CONFLATED) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()

    LaunchedEffect(paneId) {
        withContext(Dispatchers.IO) {
            try {
                client.markSeen(paneId)
            } catch (e: IOException) {
                Log.w(TAG, "could not mark $paneId seen", e)
            }
        }
    }

    DisposableEffect(paneId, lifecycle) {
        var poller: Job? = null
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    poller = scope.launch {
                        while (true) {
                            val snapshot = fetchSnapshot(client, paneId, liveScreen)
                            val host = fetchHost(client)
                            if (snapshot == null) {
                                refreshFailed = true
                            } else {
                                pane = snapshot.pane
                                conversation = snapshot.conversation
                                snapshot.text?.let { text = it }
                                refreshFailed = false
                            }
                            host?.let { stats = it.stats }
                            nowSeconds = clock()
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
    val showingConversation = conversation != null && !liveScreen
    // Keyed by pane, and the terminal's also by view, so a pane switch or a return to the live
    // screen starts at the newest line instead of where another view was left.
    val listState = remember(paneId) { LazyListState() }
    val terminalScroll = remember(paneId, showingConversation) { ScrollState(0) }
    var headerHeightPx by remember { mutableIntStateOf(0) }
    val bodyScroll: ScrollableState = if (showingConversation) listState else terminalScroll
    val headerShift by animateIntAsState(
        targetValue = if (headerRule.visible) 0 else -headerHeightPx,
        animationSpec = tween(HEADER_SLIDE_MILLIS),
        label = "header-shift",
    )
    val bodyTop = with(density) { headerHeightPx.toDp() } + BODY_GAP_BELOW_HEADER
    val bodyReady = current == null || headerHeightPx > 0
    val headerScroll = remember(headerRule, bodyScroll) {
        object : NestedScrollConnection {
            override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
                headerRule.onScroll(
                    deltaPx = -consumed.y,
                    atTop = !bodyScroll.canScrollBackward,
                    atBottom = !bodyScroll.canScrollForward,
                )
                return Offset.Zero
            }
        }
    }
    LaunchedEffect(headerRule, bodyScroll) {
        snapshotFlow { bodyScroll.canScrollBackward to bodyScroll.canScrollForward }.collect { (canBack, canForward) ->
            if (!canBack || !canForward) headerRule.onScroll(0f, atTop = !canBack, atBottom = !canForward)
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        if (refreshFailed) {
            Text("could not refresh")
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().nestedScroll(headerScroll)) {
            // The body waits for the header's height, so its top padding is final before its first
            // layout. The conversation follows the newest item only on new entries, so a later
            // change of the padding would push the newest item out of view.
            if (bodyReady && showingConversation) {
                val footer: (@Composable () -> Unit)? = question?.let { card ->
                    val content: @Composable () -> Unit = { QuestionCard(card, client, onRefresh = refresh) }
                    content
                }
                ConversationView(
                    entries,
                    listState = listState,
                    contentPadding = PaddingValues(top = bodyTop),
                    footer = footer,
                )
            } else if (bodyReady) {
                Column(modifier = Modifier.fillMaxSize()) {
                    TerminalView(
                        text,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        verticalState = terminalScroll,
                        topPadding = bodyTop,
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
                    nowSeconds = nowSeconds,
                    showStatus = conversation != null,
                    liveScreen = liveScreen,
                    onStatusClick = {
                        liveScreen = !liveScreen
                        refresh()
                    },
                    onConversation = { liveScreen = false },
                    onBack = onBack,
                    modifier = Modifier
                        .align(Alignment.TopCenter)
                        .onSizeChanged { headerHeightPx = it.height }
                        .offset { IntOffset(0, headerShift) },
                )
            }
        }
        ComposeBox(paneId, client)
        QuickKeys(paneId, client)
    }
}
