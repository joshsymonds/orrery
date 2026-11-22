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
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.spec
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
private val ENDED_STATES = setOf(STEER_STOPPED, STEER_FAILED)

/** The states a conversation has ended in: a contract it proposed opens read-only from it. */
internal val ENDED_CONVERSATION_STATES = setOf("stopped", "failed")

/** The band lines of [conversation]'s waiting contracts, the proposed ones it proposed, in list order. */
internal fun contractBandLines(contracts: List<ContractRow>, conversation: String): List<ContractBandLine> =
    if (conversation.isEmpty()) {
        emptyList()
    } else {
        contracts
            .filter { it.state == "proposed" && it.conversation == conversation }
            .map { contract ->
                val left = leftOf(contract.openDecisions.toInt(), contract.care, shown = false)
                ContractBandLine(contract.id, contractSlug(contract.id), bandText(left))
            }
    }

/** Whether a contract the conversation shows opens live: the conversation is shown and has not stopped or failed. */
internal fun conversationLive(pane: Pane?): Boolean = pane != null && pane.state !in ENDED_CONVERSATION_STATES

/**
 * A labelled step's session on the step screen: its owner's calls are Hold and Stop, and a message to it
 * is a nudge. [held] is the step's held state, so a held step shows no Hold. [label] names the step in the dock,
 * and [doing] says what the step is doing there. [epic] is the id of the epic the step belongs to and [task] the id of
 * its task: the screen reads the epic, and the step's title, state and end come from its record once it is read.
 * Until then [label], [held] and [doing] stand.
 */
data class StepMode(
    val held: Boolean,
    val label: String,
    val doing: String = "",
    val epic: String = "",
    val task: String = "",
)

/**
 * A headless session's screen: its conversation, streamed while the screen is started. Each delta is
 * folded into what the screen holds, and a failed stream reconnects after the seq it holds, so the
 * conversation resumes where it stopped. A stream that ends without error reconnects the same way.
 *
 * The screen shows the session's head, under it while it is open the rail strip of the epics the session steers and
 * its running tasks (a tile of an epic calls [onOpenEpic] with its id), its conversation with each unfinished main-thread
 * block after the items (a thinking block as a line, a text block as a live reply), its background tasks, and each message
 * it sent that the conversation does not hold yet as a sending block, then the open request's Ask after the last item. The head's epics and
 * host are read while the screen is started, and the head folds as the pane screen's does. The dock under the
 * conversation says the session's state from the stream, with its controls and its queue; Send posts the
 * draft as a prompt through [SessionSender]. Reading the conversation marks the session seen.
 *
 * With [step] set the screen is a step's: under its title row, the crumbs of its epic, its task and its own session, the
 * band of its epic draws over the conversation, and the dock's Hold and Stop post to the step, the open request's Ask
 * is not drawn, and Send posts the draft as a nudge through [StepCalls]. A step that has ended has no dock, and its
 * end is the conversation's last block. [onOpenTask] is a task crumb's or chip's tap, with the phase the task is in.
 * [reason] is the listed row's summary, which the dock shows when the session failed.
 *
 * A contract the conversation shows (its tile, its band line, its transcript line) calls [onOpenContract] with its id
 * and whether it opens live. [about] is the row's About the dock shows; a send posts it as [aboutMessage] and then
 * calls [onClearAbout], and the dock's Not about this calls [onClearAbout] too.
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
    onOpenTask: (task: String, phase: Phase) -> Unit = { _, _ -> },
    step: StepMode? = null,
    reason: String = "",
    about: RowAbout? = null,
    onClearAbout: () -> Unit = {},
    onOpenContract: (id: String, live: Boolean) -> Unit = { _, _ -> },
    clock: () -> Long = { System.currentTimeMillis() / 1000 },
    onSteered: (String) -> Unit = {},
) {
    var streamed by remember(sessionId) { mutableStateOf<StreamedConversation?>(null) }
    var refreshFailed by remember(sessionId) { mutableStateOf(false) }
    // Whether no pane or headless session carries this session any more, read once its watch has failed. An unlisted
    // session has ended (P4), so one that steers a live epic draws the ended bar.
    var unlisted by remember(sessionId) { mutableStateOf(false) }
    var seenAtOpen by remember(sessionId) { mutableStateOf<Long?>(null) }
    // The messages the screen sent, until the conversation holds each one.
    var sentTexts by remember(sessionId) { mutableStateOf<List<SentText>>(emptyList()) }
    // Whether the strip under the band has tiles: then it, not the band, carries the control that hides the header.
    var stripTiles by remember(sessionId) { mutableStateOf(false) }
    // The find bar while it is open: its query, and the current match of the matches the transcript holds.
    var finding by remember(sessionId) { mutableStateOf<FindSession?>(null) }
    val searchCache = remember { MarkdownCache() }
    var headEpics by remember(sessionId) { mutableStateOf(HeadEpics(emptyList(), emptyMap())) }
    // A step's epic, read while the screen is started and again when the step's session changes state. [stepRead] is
    // whether the first read has finished, failed or not: the step's dock waits for it, so its label and state are the
    // epic's.
    var stepDetail by remember(sessionId) { mutableStateOf<EpicDetail?>(null) }
    var stepRead by remember(sessionId) { mutableStateOf(false) }
    var contracts by remember(sessionId) { mutableStateOf<List<ContractRow>>(emptyList()) }
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
                                    // Forgotten here, in the write that holds the delta, so a prompt the stream trims later cannot bring it back.
                                    sentTexts = unsentTexts(sentTexts, next.conversation.items, next.queued)
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
                            // The epics and contracts reads are before the host read, as the pane screen's poll reads them.
                            val epics = fetchHeadEpics(client, sessionId)
                            val listed = fetchHeadContracts(client)
                            val host = fetchHost(client)
                            epics?.let { headEpics = it }
                            listed?.let { contracts = it }
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
    val stepEpic = step?.epic.orEmpty()
    LaunchedEffect(stepEpic, pane?.state) {
        if (stepEpic.isNotEmpty()) {
            try {
                withContext(Dispatchers.IO) { client.epic(stepEpic) }?.let { stepDetail = it }
            } catch (e: IOException) {
                Log.w(TAG, "could not read epic $stepEpic", e)
            }
        }
        stepRead = true
    }
    val stepRecord = step?.let { s -> stepDetail?.let { detail -> recordTasksOf(detail).taskOfStep(s.task) } }
    val stepRow = if (step != null) stepDetail?.steps?.find { it.id == sessionId } else null
    val stepEnded = stepRow?.end?.isNotEmpty() == true
    val stepTitle = stepRecord?.title ?: step?.label.orEmpty()
    val stepPhase = stepRecord?.let { phaseOfPlace(it.place, it.integrated) } ?: Phase.IMPLEMENTING
    val stepDock = step?.let { DockStep(stepTitle, stepRow?.held ?: it.held, stepRow?.doing ?: it.doing) }
    val unsent = held?.let { unsentTexts(sentTexts, it.conversation.items, it.queued) }.orEmpty()
    val entries = remember(held, unsent) {
        held?.let {
            foldConversation(it.shownItems(), now = nowSeconds) +
                unfinishedEntries(it.unfinished) +
                backgroundEntries(it.conversation.background) +
                unsent.map { message -> ConvoEntry.Sending(message.text) }
        }.orEmpty()
    }
    val epicDetails = questionEpicDetails(entries, headEpics.details, client)
    val found = remember(entries, finding?.query) { transcriptMatches(entries, finding?.query.orEmpty(), searchCache) }
    val foundCurrent = finding?.let { if (found.isEmpty()) -1 else it.current.coerceIn(0, found.lastIndex) } ?: -1
    val ask = if (step == null) held?.request?.let { open -> pane?.let { sessionAsk(it, open) } } else null
    // The stream redraws the session once its answer lands, so the Ask needs no refresh of its own.
    // The Ask's answer lives per session, outside the footer item that draws the Ask, so scrolling that item away keeps it.
    val askLife = rememberAskLife(sessionId)
    // The archive toast a stale Ask's Archive this session shows; it leaves with this page's composition.
    val toast = remember(sessionId) { ArchiveToastState() }
    val onArchived: (kind: String, id: String) -> Unit = { kind, id ->
        pane?.let { toast.show(it.label.ifEmpty { it.command }, kind, id) }
    }
    // A step's footer is its end once it has ended; a session's is its Ask.
    val footer: (@Composable () -> Unit)? = if (step != null) {
        stepRow?.takeIf { it.end.isNotEmpty() }?.let { ended ->
            val content: @Composable () -> Unit = {
                StepEndBlock(agent = ended.agent, end = ended.end, summary = ended.summary, facts = null)
            }
            content
        }
    } else {
        val content: @Composable () -> Unit = {
            AskHost(
                model = ask,
                client = client,
                now = nowSeconds,
                onRefresh = {},
                onArchived = onArchived,
                life = askLife,
            )
        }
        content
    }
    val draft = rememberDockDraft(sessionId)
    JoinKeptReplies(draft = draft, host = pane?.host.orEmpty().ifEmpty { servingHost }, kind = "session", id = sessionId)

    val headHidden = rememberHeadHidden()
    val density = LocalDensity.current
    val ime = WindowInsets.ime
    val keyboardUp by remember(ime, density) { derivedStateOf { ime.getBottom(density) > 0 } }
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
    val bandLines = contractBandLines(contracts, sessionId)
    // The tile, the band line and the transcript line of a contract the conversation proposed open live while it runs.
    // A transcript line also needs the contract listed as this conversation's, so a line of another's stays read-only.
    val live = conversationLive(pane)
    val openContract: (String) -> Unit = { id -> onOpenContract(id, live) }
    val openTranscriptContract: (String) -> Unit = { id ->
        onOpenContract(id, live && contracts.any { it.id == id && it.conversation == sessionId })
    }

    Column(modifier = Modifier.fillMaxSize()) {
        if (refreshFailed && pane == null) {
            Text(REFRESH_FAILED, modifier = Modifier.padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two))
        }
        if (step != null) {
            val detail = stepDetail
            TitleRow(
                crumbs = buildList {
                    if (detail != null) {
                        add(Crumb(label = detail.epic.slug, onGo = { onOpenEpic(step.epic) }))
                        add(Crumb(label = stepTitle, onGo = { onOpenTask(step.task, stepPhase) }))
                    }
                    add(Crumb(label = "Worker session"))
                },
                onBack = onBack,
                onFind = { finding = FindSession(query = "") },
            )
            detail?.let { shown ->
                ContextBand(
                    detail = shown,
                    currentTask = step.task,
                    now = nowSeconds,
                    onOpenEpic = { onOpenEpic(step.epic) },
                    onOpenTask = onOpenTask,
                    modifier = Modifier.fillMaxWidth().background(OrreryTokens.Surface.page),
                )
            }
        }
        pane?.takeIf { step == null }?.let { shown ->
            AnimatedVisibility(
                visible = !keyboardUp,
                enter = expandVertically(Move.LAYOUT.spec(animated = true)) + fadeIn(Move.LAYOUT.spec(animated = true)),
                exit = shrinkVertically(Move.LAYOUT.spec(animated = true)) + fadeOut(Move.LAYOUT.spec(animated = true)),
                label = "head",
            ) {
                Column(modifier = Modifier.fillMaxWidth()) {
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
                        onFind = { finding = FindSession(query = "") },
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
                            onOpenContract = openContract,
                            onTiles = { stripTiles = it },
                            end = { HideHeader { headHidden.value = true } },
                        )
                    }
                }
            }
        }
        Box(modifier = Modifier.weight(1f).fillMaxWidth().clipToBounds()) {
            if (held != null) {
                ConversationView(
                    entries,
                    listState = listState,
                    onFollowing = { atNewest = it },
                    footer = footer,
                    checks = stepRow?.checks.orEmpty(),
                    seen = seenAtOpen,
                    contracts = contracts,
                    onOpenContract = openTranscriptContract,
                    onOpenEpic = onOpenEpic,
                    epicQuestion = { epic, question ->
                        EpicQuestion(epicDetails[epic], question, nowSeconds, client)
                    },
                    find = finding?.let { TranscriptFind(found, foundCurrent, it.step) },
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
            // At the foot of the body, above the dock, so it never covers the dock's input.
            ArchiveToast(
                state = toast,
                client = client,
                onUndone = {},
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
        finding?.let { session ->
            FindBar(
                query = session.query,
                count = found.size,
                current = foundCurrent,
                onQuery = { finding = FindSession(query = it, step = session.step + 1) },
                onNext = { finding = session.copy(current = stepFind(foundCurrent, found.size, 1), step = session.step + 1) },
                onPrevious = { finding = session.copy(current = stepFind(foundCurrent, found.size, -1), step = session.step + 1) },
                onClose = { finding = null },
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.four),
            )
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
        } else if (!stepEnded && (step == null || stepRead)) {
            Dock(
                facts = dockFactsOf(
                    kind = if (step != null) DockKind.STEP else DockKind.SESSION,
                    name = pane?.cwd.orEmpty().trimEnd('/').substringAfterLast('/'),
                    pane = pane,
                    streamed = held,
                    reason = reason,
                    step = stepDock,
                ),
                nowSeconds = nowSeconds,
                client = client,
                draft = draft,
                send = { text ->
                    val sent = about?.let { aboutMessage(it.id, it.text, text) } ?: text
                    val after = held?.conversation?.items?.lastOrNull()
                    val promptId = if (step != null) {
                        stepCalls.nudge(sent)
                        ""
                    } else {
                        sender.send(sent)
                    }
                    scope.launch { sentTexts = sentTexts + SentText(sent, after, promptId) }
                    if (about != null) onClearAbout()
                },
                sessionId = sessionId,
                queued = held?.queued.orEmpty(),
                pending = held?.pending.orEmpty(),
                stepCalls = if (step != null) stepCalls else null,
                about = about?.let { "${it.id} ${it.text}" },
                onClearAbout = onClearAbout,
            )
        }
    }
}
