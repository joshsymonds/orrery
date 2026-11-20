package io.github.joshsymonds.orrery.ui

import android.util.Log
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.spec
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

private const val TAG = "PaneScreen"
private const val POLL_INTERVAL_MILLIS = 1_000L
private const val STREAM_RECONNECT_MILLIS = 1_000L
/**
 * Consecutive streams that fail without a delta before the screen polls until its next start. A stream that ends
 * UNAVAILABLE does not count.
 */
private const val STREAM_FAILURES_BEFORE_POLL = 3
private val BODY_GAP_BELOW_HEADER = OrreryTokens.Space.four + OrreryTokens.Space.two

/** The note a conversation's screen draws when a refresh fails: under the head, or alone when there is no pane. */
internal const val REFRESH_FAILED = "could not refresh"

/** The draft field never takes more than this share of the screen above the keyboard. */
private const val COMPOSE_MAX_SHARE = 1f / 3f

/** Nor less than this, so a very short screen still shows a line or two of the draft. */
private val COMPOSE_MIN_CAP = OrreryTokens.Size.controlTouch

/** One tick's pane reads. `conversation` is null when the pane has none. */
private class Snapshot(val pane: Pane?, val conversation: Conversation?)

/** A `Host.Status` read: the serving host's name, and its stats, which are null when the host answers not_found. */
internal class HostRead(val host: String, val stats: List<HostStat>?)

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

/** The contracts the server lists, or null when the call fails, so the head keeps what it showed. */
internal suspend fun fetchHeadContracts(client: OrreryClient): List<ContractRow>? =
    withContext(Dispatchers.IO) {
        try {
            client.contracts()
        } catch (e: IOException) {
            null
        }
    }

/** One `Host.Status` read. Null when the call fails, so the meters keep their last stats. */
internal suspend fun fetchHost(client: OrreryClient): HostRead? =
    withContext(Dispatchers.IO) {
        try {
            val status = client.host()
            HostRead(status?.host.orEmpty(), status?.stats)
        } catch (e: IOException) {
            null
        }
    }

/** The last element of a pane's cwd, which the head names as its directory; "/" for the root. */
internal fun lastPathElement(path: String): String = path.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }

/**
 * The host a conversation is on when that is not the serving host, else null. A serving host not yet read counts as
 * no other host, so a conversation is never called remote before its host is known.
 */
internal fun otherHostOf(paneHost: String, servingHost: String): String? =
    paneHost.takeIf { it.isNotEmpty() && servingHost.isNotEmpty() && it != servingHost }

/**
 * A pane, followed while the screen is at least started: its conversation stream delivers each change,
 * and once that stream gives up for this start, the pane is read every second instead. Each poll also reads the
 * epics the pane steers and the host. A pane with a conversation shows it, with the question card after the last
 * item; a pane without one shows a line saying so, with the card below it. The pane's dock sits at the foot of the
 * screen, always in view, and says the pane's state. Each newer read time the conversation reports is sent to the
 * server as the pane's read-up-to time.
 *
 * The head overlays the body, clipped to the body's bounds. It opens at the newest line, folds to its one line
 * while the reader is away from the newest line, and stays folded when the owner hides it until the owner taps
 * the folded line. Open, it draws the rail strip of the epics the pane steers under the band (a tile of an epic calls
 * [onOpenEpic] with its id). While the software keyboard is up the head folds away entirely, and the draft field is capped
 * at a third of the screen above the keyboard, so the conversation keeps the room between them. The quick keys
 * hide while the draft has text, so a reply in progress is one row above the keyboard.
 */
@Composable
fun PaneScreen(
    paneId: String,
    client: OrreryClient,
    modifier: Modifier = Modifier,
    onBack: () -> Unit = {},
    onOpenEpic: (String) -> Unit = {},
    about: RowAbout? = null,
    onClearAbout: () -> Unit = {},
    onOpenContract: (id: String, live: Boolean) -> Unit = { _, _ -> },
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var pane by remember(paneId) { mutableStateOf<Pane?>(null) }
    var conversation by remember(paneId) { mutableStateOf<Conversation?>(null) }
    var servingHost by remember(paneId) { mutableStateOf("") }
    var stats by remember(paneId) { mutableStateOf<List<HostStat>?>(null) }
    var headEpics by remember(paneId) { mutableStateOf(HeadEpics(emptyList(), emptyMap())) }
    var contracts by remember(paneId) { mutableStateOf<List<ContractRow>>(emptyList()) }
    var nowSeconds by remember(paneId) { mutableLongStateOf(0L) }
    // Whether the strip under the band has tiles: then it, not the band, carries the control that hides the header.
    var stripTiles by remember(paneId) { mutableStateOf(false) }
    var refreshFailed by remember(paneId) { mutableStateOf(false) }
    var seenAtOpen by remember(paneId) { mutableStateOf<Long?>(null) }
    // What the stream holds, kept across starts so the next start resumes from its transcript and seq.
    var held by remember(paneId) { mutableStateOf<StreamedConversation?>(null) }
    val refreshRequests = remember(paneId) { Channel<Unit>(Channel.CONFLATED) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()

    DisposableEffect(paneId, lifecycle) {
        var poller: Job? = null
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> {
                    poller = scope.launch {
                        // Set once the stream gives up; the rest of this start polls.
                        var polling = false
                        coroutineScope {
                            launch {
                                var failures = 0
                                while (!polling) {
                                    // The stream's end comes back as a value, as fetchSnapshot and fetchHost return
                                    // theirs, so this coroutine resumes from the IO hop the same way they do.
                                    val ended: IOException? = withContext(Dispatchers.IO) {
                                        try {
                                            client.watchConversation(paneId, held?.transcript.orEmpty(), held?.seq ?: 0L) { delta ->
                                                withContext(Dispatchers.Main) {
                                                    failures = 0
                                                    refreshFailed = false
                                                    // Read before the delta's conversation is shown, as the poll reads its clock.
                                                    nowSeconds = clock()
                                                    val next = held.applyDelta(delta)
                                                    held = next
                                                    pane = next.pane
                                                    seenAtOpen = seenAtOpen ?: next.pane?.seen
                                                    conversation = next.conversation.takeIf { next.pane?.state?.isNotEmpty() == true }
                                                }
                                            }
                                            null
                                        } catch (e: IOException) {
                                            e
                                        }
                                    }
                                    val gaveUp = ended is OrreryCallException && ended.code == Code.NOT_FOUND
                                    // An UNAVAILABLE end is the server restarting, so the reconnect does not count.
                                    val restarting = ended is OrreryCallException && ended.code == Code.UNAVAILABLE
                                    if (!gaveUp && !restarting) failures++
                                    if (gaveUp || failures >= STREAM_FAILURES_BEFORE_POLL) {
                                        // The poll shows the pane from now on, so what the stream held is stale.
                                        held = null
                                        polling = true
                                        refreshRequests.trySend(Unit)
                                    } else {
                                        delay(STREAM_RECONNECT_MILLIS)
                                    }
                                }
                            }
                            launch {
                                while (true) {
                                    val polled = polling
                                    val snapshot = if (polled) fetchSnapshot(client, paneId) else null
                                    // Read before the snapshot's conversation is shown, so its time rows
                                    // never see the zero clock the state starts with.
                                    nowSeconds = clock()
                                    // The epics and contracts reads are before the host read, which stays the read's last call.
                                    val epics = fetchHeadEpics(client, pane?.orreryId.orEmpty())
                                    val listed = fetchHeadContracts(client)
                                    val host = fetchHost(client)
                                    if (polled) {
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
                                    }
                                    epics?.let { headEpics = it }
                                    listed?.let { contracts = it }
                                    host?.let {
                                        servingHost = it.host
                                        stats = it.stats
                                    }
                                    withTimeoutOrNull(POLL_INTERVAL_MILLIS) { refreshRequests.receive() }
                                }
                            }
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
    val showingConversation = conversation != null
    // Keyed by pane, so a pane switch starts at the newest line instead of where another pane was left.
    val listState = remember(paneId) { LazyListState() }
    var endRequests by remember(paneId) { mutableIntStateOf(0) }
    var headHeightPx by remember { mutableIntStateOf(0) }
    var headMeasured by remember { mutableStateOf(false) }
    val bodyTop = with(density) { headHeightPx.toDp() } + BODY_GAP_BELOW_HEADER
    val ime = WindowInsets.ime
    val keyboardUp by remember(ime, density) { derivedStateOf { ime.getBottom(density) > 0 } }
    var screenHeightPx by remember { mutableIntStateOf(0) }
    var draftPresent by remember(paneId) { mutableStateOf(false) }
    val composeCap = if (screenHeightPx > 0) {
        maxOf(with(density) { screenHeightPx.toDp() } * COMPOSE_MAX_SHARE, COMPOSE_MIN_CAP)
    } else {
        Dp.Unspecified
    }
    val bodyReady = current == null || headMeasured

    val headHidden = rememberHeadHidden()
    var tapped by remember(paneId) { mutableStateOf(false) }
    // Whether the reader is at the newest line, as the conversation view follows it.
    var atNewest by remember(paneId) { mutableStateOf(true) }
    // A tap on the folded line holds only until the reader next moves between the newest line and the rest.
    LaunchedEffect(atNewest) { tapped = false }
    val fold = headFold(hidden = headHidden.value, atNewest = atNewest, tapped = tapped)
    val steered = headEpics.steered
    val lines = askLines(steered, headEpics.details)
    val asking = askingWords(ownName = current?.takeIf { ownAsks(it) }?.let { lastPathElement(it.cwd) }, lines = lines)
    val otherHost = current?.let { otherHostOf(it.host, servingHost) }
    val bandLines = contractBandLines(contracts, current?.orreryId.orEmpty())
    // The tile, the band line and the transcript line of a contract the conversation proposed open live while it runs.
    // A transcript line also needs the contract listed as this conversation's, so a line of another's stays read-only.
    val conversationId = current?.orreryId.orEmpty()
    val live = conversationLive(current)
    val openContract: (String) -> Unit = { id -> onOpenContract(id, live) }
    val openTranscriptContract: (String) -> Unit = { id ->
        onOpenContract(id, live && contracts.any { it.id == id && it.conversation == conversationId })
    }

    Column(modifier = modifier.fillMaxSize().onSizeChanged { screenHeightPx = it.height }) {
        if (refreshFailed && current == null) {
            Text(REFRESH_FAILED)
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            // The body waits for the head's height, so its first layout already clears the head.
            // Later changes of the padding (the head folding for the keyboard) are followed by
            // the conversation while the reader is at the newest end.
            if (bodyReady && showingConversation) {
                val footer: (@Composable () -> Unit)? = question?.let { card ->
                    val content: @Composable () -> Unit = { QuestionCard(card, client, onRefresh = refresh) }
                    content
                }
                ConversationView(
                    entries,
                    listState = listState,
                    onFollowing = { atNewest = it },
                    contentPadding = PaddingValues(top = bodyTop),
                    footer = footer,
                    endRequests = endRequests,
                    seen = seenAtOpen,
                    now = nowSeconds,
                    contracts = contracts,
                    onOpenContract = openTranscriptContract,
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
                            .padding(
                                start = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                                end = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                                top = bodyTop,
                            ),
                    )
                    if (question != null) {
                        QuestionCard(question, client, onRefresh = refresh)
                    }
                }
            }
            current?.let { shown ->
                Column(
                    modifier = Modifier
                        .align(Alignment.TopCenter)
                        .fillMaxWidth()
                        .onSizeChanged {
                            headHeightPx = it.height
                            headMeasured = true
                        },
                ) {
                    AnimatedVisibility(
                        visible = !keyboardUp,
                        enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
                        exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
                        label = "head",
                    ) {
                        Column {
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
                            contracts = bandLines,
                            onOpenContract = openContract,
                            onHide = { headHidden.value = true },
                            onShow = {
                                headHidden.value = false
                                tapped = true
                            },
                            modifier = Modifier.fillMaxWidth().background(OrreryTokens.Surface.page),
                        )
                        if (fold == HeadFold.OPEN) {
                            ConversationStrip(
                                conversation = shown.orreryId,
                                pane = true,
                                background = emptyList(),
                                now = nowSeconds,
                                client = client,
                                onOpenEpic = onOpenEpic,
                                onOpenContract = openContract,
                                modifier = Modifier.background(OrreryTokens.Surface.page),
                                onTiles = { stripTiles = it },
                                end = { HideHeader { headHidden.value = true } },
                            )
                        }
                        }
                    }
                }
            }
        }
        val draft = rememberDockDraft(paneId)
        Dock(
            facts = dockFactsOf(
                kind = if (current?.state.orEmpty().isEmpty()) DockKind.SHELL else DockKind.PANE,
                name = current?.cwd.orEmpty().trimEnd('/').substringAfterLast('/'),
                pane = current,
                streamed = held,
            ),
            nowSeconds = nowSeconds,
            client = client,
            draft = draft,
            send = { text ->
                val sent = about?.let { aboutMessage(it.id, it.text, text) } ?: text
                client.sendComposed(paneId, sent)
                if (about != null) onClearAbout()
            },
            paneId = paneId,
            about = about?.let { "${it.id} ${it.text}" },
            onClearAbout = onClearAbout,
            waiting = conversation?.waiting.orEmpty(),
            maxFieldHeight = composeCap,
            onDraftPresent = { draftPresent = it },
            onSend = { endRequests++ },
        )
        AnimatedVisibility(
            visible = !draftPresent,
            enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
            exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
            label = "quick-keys",
        ) {
            QuickKeys(paneId, client, state = current?.state.orEmpty(), changed = current?.changed ?: 0L)
        }
    }
}
