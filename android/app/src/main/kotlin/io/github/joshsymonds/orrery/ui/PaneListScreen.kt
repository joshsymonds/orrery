package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.IOException

/** The fixed delay the inbox waits before it reads again after a read that did not answer. */
internal const val RETRY_DELAY_MILLIS = 5_000L

/** The fixed delay between the inbox's re-reads of its three row lists while it is started. */
internal const val TICK_MILLIS = 5_000L

/**
 * The inbox's state: the chip on, the search words (null while the search is closed), the folded projects, the projects
 * whose ended line is open, the conversations whose ended epics are open, the view (the inbox or the archived tree), and
 * the tree of the last read that answered. The caller keeps one above the screens it switches between, so all of it
 * survives opening a conversation and coming back.
 */
class PaneListState {
    internal var narrow by mutableStateOf(Narrow.NONE)
    internal var search by mutableStateOf<String?>(null)
    internal var folded by mutableStateOf(emptySet<String>())
    internal var endedOpen by mutableStateOf(emptySet<String>())
    internal var foldOpen by mutableStateOf(emptySet<String>())
    internal var view by mutableStateOf(TreeView.INBOX)
    internal var held: TreeData? = null

    /** Where the last answered read drew each conversation, null until the first answer. */
    internal var spots: Map<String, Placed>? = null

    /** The moves the latest answer made, which the move notice reads. */
    internal var moves: List<Moved> = emptyList()

    /** The key of the entry the owner last opened from the inbox, which any later open replaces. */
    internal var opened: String? = null

    /** The conversation the open entry hold keeps drawn in its tree place, or null when no entry is held. */
    internal var heldKey: String? = null

    /** The conversations the owner moved by hand from the inbox since the last answer, which are not moves. */
    internal var handMoved: Set<String> = emptySet()

    /**
     * Takes [answer]'s spots as the last answer: its moves are the rows that moved since the last answer, less those the
     * owner moved by hand, and the first answer moves nothing. The open entry is held when this answer moves it out of
     * the tree, and stays held until the owner opens another entry. Returns the moves.
     */
    internal fun answered(answer: Map<String, Placed>): List<Moved> {
        val before = spots
        val moved = if (before == null) emptyList() else movesBetween(before, answer).filter { it.key !in handMoved }
        val open = opened
        val line = open?.let { answer[it]?.spot?.line }
        val away = line == RowLine.ENDED || line == RowLine.ARCHIVED
        val wasInTree = open != null && before?.get(open)?.spot?.line == RowLine.TREE
        heldKey = if (open != null && away && (wasInTree || heldKey == open)) open else null
        spots = answer
        moves = moved
        handMoved = emptySet()
        return moved
    }

    internal fun toggleFolded(key: String) {
        folded = if (key in folded) folded - key else folded + key
    }

    internal fun toggleEnded(key: String) {
        endedOpen = if (key in endedOpen) endedOpen - key else endedOpen + key
    }

    internal fun toggleFold(key: String) {
        foldOpen = if (key in foldOpen) foldOpen - key else foldOpen + key
    }
}

/** A [PaneListState] that the composition remembers, for a caller that keeps none of its own. */
@Composable
internal fun rememberPaneListState(): PaneListState = remember { PaneListState() }

/** What one read of the lists, the epics and their details, the contracts and the host answered with. */
internal class TreeData(
    val panes: List<Pane>,
    val archived: Set<String>,
    val epics: List<EpicRow>,
    val details: Map<String, EpicDetail>,
    val contracts: List<ContractRow>,
    val host: String,
)

/**
 * The inbox: the [InboxHead], round 6's phone head of the Orrery mark, Search, the Notices bell, Pools, Settings and
 * New; then, while search is open, its field; then the two chips and each project as a card, its conversations hanging
 * under it with what steers them, each conversation's ended epics folded under its row, and the project's ended line.
 * The chips narrow the tree, search narrows what the chips left, and a folded card says what waits in it. The foot
 * opens the archived tree and returns from it. A tap on a conversation calls [onOpenPane] with its pane, a tap on an epic or one of its steps calls [onOpenEpic], a tap on a contract's row calls [onOpenContract]
 * with its id and the conversation it hangs under, whose pane is null for a contract on its own row, and a tap on a project's
 * name calls [onOpenProject] with its key. A long press on a conversation or an epic opens its sheet, where the owner
 * marks it seen or archives it, and a landed change reads the tree again; a landed archive also shows the [ArchiveToast]
 * at the foot, whose Undo reads the tree again. An answer that moved rows shows its move notice in that same place, which
 * names the moved rows and their places, with Show and Undo. New opens the picker as a bottom sheet; a
 * run it answers opens that pane through [onOpenPaneId], and a start it answers opens that session through
 * [onStartedSession]. The Pools button opens the pools view through [onOpenPools]. The bell counts the unread notices,
 * read on start and every minute while started, and opens the notices through [onOpenNotices].
 *
 * Until the first answer the inbox says it is loading. A read that fails, the panes, a headless or archived list, or an
 * epic or its detail, shows that the server did not answer, and reads again after [RETRY_DELAY_MILLIS] or at once on
 * Try now. A failed contracts read keeps the contracts last read, and a failed host read keeps the host last read.
 *
 * While the inbox is at least started, it also re-reads its three row lists every [TICK_MILLIS] once an answer has
 * landed. A tick keeps the epics, details, contracts and host of the held answer, and its answer goes through the same
 * moves and notice as a full read. A tick that does not answer keeps what is drawn and waits for the next tick, and it
 * does not show the server did not answer. The ticks stop when the lifecycle drops below started, and start again when
 * it is started again.
 */
@Composable
fun PaneListScreen(
    client: OrreryClient,
    onOpenPane: (Pane) -> Unit,
    onOpenPaneId: (String) -> Unit,
    onStartedSession: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
    onOpenPools: () -> Unit = {},
    onOpenNotices: () -> Unit = {},
    onOpenEpic: (String) -> Unit = {},
    onOpenProject: (String) -> Unit = {},
    onOpenContract: (id: String, conversation: Pane?) -> Unit = { _, _ -> },
    now: () -> Long = { System.currentTimeMillis() / 1000 },
    state: PaneListState = rememberPaneListState(),
) {
    val notices = rememberNoticeRows(client)
    var data by remember(client) { mutableStateOf<TreeData?>(null) }
    var failed by remember(client) { mutableStateOf(false) }
    var generation by remember(client) { mutableIntStateOf(0) }
    var picking by remember { mutableStateOf(false) }
    var sheet by remember { mutableStateOf<SheetRow?>(null) }
    // The moves marked for Move.LAYOUT's length, by row key. A mark clears only while it is still the move that set it.
    var marking by remember { mutableStateOf(emptyMap<String, Moved>()) }
    val scope = rememberCoroutineScope()
    val animated = animationsOn()
    val toast = rememberArchiveToast()
    // The reads take turns, so an answer that began earlier never lands after one that began later.
    val reads = remember(client) { Mutex() }
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    // Takes [read] as the answer the inbox draws: an answer that moved rows shows its move notice and marks the moved rows.
    val land: (TreeData) -> Unit = { read ->
        val moved = state.answered(spotsOfRead(read))
        // An answer that moved rows shows its one notice in the foot's slot, in place of any toast or earlier notice.
        if (state.moves.isNotEmpty()) toast.showMoves(state.moves)
        // Reduced motion marks nothing; the rows still land where the answer put them.
        if (animated) {
            for (move in moved.filter { it.key != state.heldKey }) {
                marking = marking + (move.key to move)
                scope.launch {
                    delay(Move.LAYOUT.millis.toLong())
                    if (marking[move.key] === move) marking = marking - move.key
                }
            }
        }
        data = read
        state.held = read
        failed = false
    }

    LaunchedEffect(client, generation) {
        val read = reads.withLock { readTree(client, state.held) }
        if (read == null) {
            failed = true
            delay(RETRY_DELAY_MILLIS)
            generation++
        } else {
            land(read)
        }
    }

    LaunchedEffect(client, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                delay(TICK_MILLIS)
                reads.withLock {
                    // A tick keeps the held answer's epics, details, contracts and host, so it waits for the first answer.
                    val held = state.held ?: return@withLock
                    readRows(client, held)?.let(land)
                }
            }
        }
    }

    BackHandler(enabled = state.search != null) { state.search = null }

    Box(modifier = modifier.fillMaxSize()) {
        Column(modifier = Modifier.fillMaxSize()) {
            InboxHead(
                unread = notices.unread,
                onSearch = { state.search = state.search ?: "" },
                onNotices = onOpenNotices,
                onPools = onOpenPools,
                onSettings = onOpenSettings,
                onNew = { picking = true },
            )
            state.search?.let { words ->
                SearchField(
                    words = words,
                    onWords = { state.search = it },
                    onClear = { state.search = null },
                    modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                )
            }
            if (picking) {
                PickerPanel(
                    client = client,
                    onOpened = { paneId ->
                        picking = false
                        onOpenPaneId(paneId)
                    },
                    onStarted = { sessionId ->
                        picking = false
                        onStartedSession(sessionId)
                    },
                    onClose = { picking = false },
                )
            }
            sheet?.let { row ->
                val rowKey = (row as? SheetRow.Conversation)?.let { convoKeyOf(it.pane) }.orEmpty()
                InboxSheet(
                    row = row,
                    client = client,
                    onArchived = { title, kind, id -> toast.showArchive(title, kind, id, rowKey) },
                    onLanded = {
                        // A row the owner archived or marked here is not a move: the next answer leaves it out.
                        (row as? SheetRow.Conversation)?.let { state.handMoved = state.handMoved + convoKeyOf(it.pane) }
                        sheet = null
                        generation++
                    },
                    onClose = { sheet = null },
                )
            }
            val loaded = data
            when {
                failed -> DidNotAnswer(onTryNow = { generation++ })
                loaded == null -> Text(
                    "loading…",
                    color = OrreryTokens.Ink.three,
                    modifier = Modifier.padding(OrreryTokens.Space.sixteen),
                )
                else -> InboxBody(
                    data = loaded,
                    state = state,
                    marking = marking,
                    now = now(),
                    onOpenPane = onOpenPane,
                    onOpenEpic = onOpenEpic,
                    onOpenProject = onOpenProject,
                    onOpenContract = onOpenContract,
                    onNew = { picking = true },
                    onLongPress = { sheet = it },
                )
            }
        }
        ArchiveToast(
            state = toast,
            client = client,
            onUndone = { generation++ },
            modifier = Modifier.align(Alignment.BottomCenter),
            // The rows an Undo brings back are the owner's doing, not moves: the read after the Undo does not notice them.
            onRowsBack = { keys -> state.handMoved = state.handMoved + keys },
            onShow = { spot -> state.showPlace(spot) },
        )
    }
}

/**
 * Reads the lists, every live epic's detail, the contracts and the host. Null when a list or an epic did not answer. A
 * contracts read that fails keeps the contracts of [held], and a host read that fails keeps its host.
 */
internal suspend fun readTree(client: OrreryClient, held: TreeData?): TreeData? = withContext(Dispatchers.IO) {
    try {
        val rows = readRowLists(client)
        val epics = client.epics()
        val details = detailsOf(client, epics)
        val contracts = try {
            client.contracts()
        } catch (error: IOException) {
            held?.contracts ?: emptyList()
        }
        val host = try {
            client.host()?.host ?: ""
        } catch (error: IOException) {
            held?.host ?: ""
        }
        TreeData(rows.panes, rows.archived, epics, details, contracts, host)
    } catch (error: IOException) {
        null
    }
}

/**
 * Reads only the three row lists, and keeps the epics, details, contracts and host of [held]. Null when a list did not
 * answer.
 */
private suspend fun readRows(client: OrreryClient, held: TreeData): TreeData? = withContext(Dispatchers.IO) {
    try {
        val rows = readRowLists(client)
        TreeData(rows.panes, rows.archived, held.epics, held.details, held.contracts, held.host)
    } catch (error: IOException) {
        null
    }
}

/** The rows the three lists answered with: the panes with their sessions' ended marks, and the archived sessions. */
private class RowLists(val panes: List<Pane>, val archived: Set<String>)

/** Reads the panes, the headless sessions and the session marks. Throws [IOException] when one of them did not answer. */
private fun readRowLists(client: OrreryClient): RowLists {
    val tmux = client.panes()
    val headless = client.headless()
    val marks = client.sessionMarks()
    val byId = marks.associateBy { it.id }
    val panes = tmux.map { pane ->
        byId[pane.orreryId]?.let { pane.copy(ended = it.ended, endedAt = it.endedAt) } ?: pane
    } + headless
    val archived = marks.filter { it.archived }.map { it.id }.toSet()
    return RowLists(panes, archived)
}

/** Where a read draws each conversation, in the inbox and in the archived view, without the open entry's hold. */
private fun spotsOfRead(data: TreeData): Map<String, Placed> = spotsOf(
    inboxTree(data.panes, data.archived, data.epics, data.details, data.contracts, data.host, TreeView.INBOX),
    inboxTree(data.panes, data.archived, data.epics, data.details, data.contracts, data.host, TreeView.ARCHIVED),
)

/**
 * The read with [held]'s conversation drawn in its tree place: unarchived and not ended, as it was before the answer
 * moved it out of the tree. The read itself is not changed.
 */
private fun TreeData.holding(held: String?): TreeData {
    val pane = held?.let { key -> panes.firstOrNull { convoKeyOf(it) == key } } ?: return this
    return TreeData(
        panes = panes.map { if (it === pane) it.copy(ended = false, archived = false) else it },
        archived = archived - pane.orreryId,
        epics = epics,
        details = details,
        contracts = contracts,
        host = host,
    )
}

/**
 * Whether a row drawn at [spot] is drawn on the screen now: a tree row in the inbox, a row behind an open ended line in
 * the inbox, or an archived row in the archived view.
 */
private fun drawnAt(spot: Spot, state: PaneListState): Boolean = when (spot.line) {
    RowLine.TREE -> state.view == TreeView.INBOX
    RowLine.ENDED -> state.view == TreeView.INBOX && spot.project in state.endedOpen
    RowLine.ARCHIVED -> state.view == TreeView.ARCHIVED
}

/** The detail of every live epic, each read at once. A detail whose read fails fails the whole read. */
private suspend fun detailsOf(client: OrreryClient, epics: List<EpicRow>): Map<String, EpicDetail> = coroutineScope {
    val reads = epics.filter { it.live }.map { epic ->
        async(Dispatchers.IO) { client.epic(epic.id)?.let { epic.id to it } }
    }
    reads.awaitAll().filterNotNull().toMap()
}

/**
 * The tree as the inbox draws it, or as the archived view draws it: the chips, the cards, the nothing-running line when the
 * inbox has no project, and the foot that switches between the two views.
 */
@Composable
private fun InboxBody(
    data: TreeData,
    state: PaneListState,
    marking: Map<String, Moved>,
    now: Long,
    onOpenPane: (Pane) -> Unit,
    onOpenEpic: (String) -> Unit,
    onOpenProject: (String) -> Unit,
    onOpenContract: (id: String, conversation: Pane?) -> Unit,
    onNew: () -> Unit,
    onLongPress: (SheetRow) -> Unit,
) {
    val projects = remember(data, state.view, state.heldKey) {
        val drawn = if (state.view == TreeView.INBOX) data.holding(state.heldKey) else data
        inboxTree(drawn.panes, drawn.archived, drawn.epics, drawn.details, drawn.contracts, drawn.host, state.view)
    }
    // The rows and ended lines drawn with a mark: a move lands in a place drawn here, or behind a shut ended line.
    val markedRows = marking.values.filter { drawnAt(it.to, state) }.mapTo(HashSet()) { it.key }
    val markedLines = marking.values
        .filter { it.to.line == RowLine.ENDED && state.view == TreeView.INBOX && it.to.project !in state.endedOpen }
        .mapTo(HashSet()) { it.to.project }
    val query = state.search.orEmpty()
    val counts = remember(projects) { countsOf(projects) }
    val waitingOf = remember(projects) { projects.associate { it.key to projectWaiting(it) } }
    // A chip that has nothing left to narrow to is not drawn, so it is not on either: the tree shows whole.
    val chip = when (state.narrow) {
        Narrow.WAITING -> if (counts.waiting > 0) Narrow.WAITING else Narrow.NONE
        Narrow.NEW -> if (counts.fresh > 0) Narrow.NEW else Narrow.NONE
        Narrow.NONE -> Narrow.NONE
    }
    val shown = remember(projects, chip, query) {
        narrow(projects, chip).let { if (query.isBlank()) it else search(it, query) }
    }
    val paneOf = remember(projects) {
        projects.flatMap { it.convos + it.ended }.mapNotNull { convo -> convo.pane?.let { convo.key to it } }.toMap()
    }
    val epicOfStep = remember(data) {
        data.epics.flatMap { epic -> epic.runningSteps.map { step -> step.id to epic.id } }.toMap()
    }
    // Each contract row's key, with the conversation it hangs under: null for a contract on its own row, which no conversation holds.
    val conversationOfContract = remember(projects) {
        projects.flatMap { it.convos + it.ended }.flatMap { convo ->
            convo.hung.filterIsInstance<ContractNode>().map { it.key to convo.pane }
        }.toMap()
    }
    val onOpen: (String) -> Unit = { key ->
        state.opened = key
        when {
            key.startsWith(EPIC_KEY_PREFIX) -> onOpenEpic(key.removePrefix(EPIC_KEY_PREFIX))
            key.startsWith(STEP_KEY_PREFIX) -> epicOfStep[key.removePrefix(STEP_KEY_PREFIX)]?.let(onOpenEpic)
            key.startsWith(CONTRACT_KEY_PREFIX) ->
                onOpenContract(key.removePrefix(CONTRACT_KEY_PREFIX), conversationOfContract[key])
            else -> paneOf[key]?.let(onOpenPane)
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        if (projects.isEmpty() && query.isBlank()) {
            // The archived view with nothing archived draws no words, as the web does; only the inbox says nothing runs.
            if (state.view == TreeView.INBOX) NothingRunning(onNew = onNew)
        } else {
            LazyColumn(modifier = Modifier.weight(1f)) {
                item(key = "chips") {
                    InboxChips(
                        counts = counts,
                        narrow = chip,
                        onNarrow = { state.narrow = it },
                        modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                    )
                }
                if (query.isNotBlank() && shown.isEmpty()) {
                    item(key = "no-match") { NothingMatches(words = query) }
                }
                items(shown, key = { it.key }) { project ->
                    ProjectCard(
                        project = project,
                        folded = project.key in state.folded,
                        waiting = waitingOf[project.key] ?: 0,
                        endedOpen = project.key in state.endedOpen,
                        onFold = { state.toggleFolded(project.key) },
                        onEnded = { state.toggleEnded(project.key) },
                        foldOpen = state.foldOpen,
                        onConvoFold = { state.toggleFold(it) },
                        convo = { convo, open, onFold ->
                            ConvoRowItem(
                                convo = convo,
                                now = now,
                                foldOpen = open,
                                onFold = onFold,
                                onOpen = onOpen,
                                onLongPress = onLongPress,
                                moving = convo.key in markedRows,
                            )
                        },
                        modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                        onOpenProject = onOpenProject,
                        endedMoving = project.key in markedLines,
                    )
                }
            }
        }
        ViewFoot(view = state.view, onView = { state.view = it })
    }
}

/** The foot of the inbox: Archived opens the archived tree, and Back to the inbox returns from it. */
@Composable
private fun ViewFoot(view: TreeView, onView: (TreeView) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
    ) {
        when (view) {
            TreeView.INBOX -> TextButton(
                onClick = { onView(TreeView.ARCHIVED) },
                modifier = Modifier.testTag("open-archived"),
            ) {
                Text("Archived")
            }
            TreeView.ARCHIVED -> TextButton(
                onClick = { onView(TreeView.INBOX) },
                modifier = Modifier.testTag("back-to-inbox"),
            ) {
                Text("Back to the inbox")
            }
        }
    }
}

private const val EPIC_KEY_PREFIX = "epic:"
private const val STEP_KEY_PREFIX = "step:"
private const val CONTRACT_KEY_PREFIX = "contract:"

/** The empty inbox: nothing is running, and New starts a session. */
@Composable
private fun NothingRunning(onNew: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(OrreryTokens.Space.sixteen),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text("Nothing is running.", style = OrreryTokens.Type.body, color = OrreryTokens.Ink.three)
        Text(
            "Start a session in a directory and it shows here, grouped by that directory.",
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
        )
        TextButton(onClick = onNew, modifier = Modifier.testTag("empty-new")) {
            Text("New")
        }
    }
}

/**
 * The search field under the head: the words typed, a placeholder that names what search finds, and Clear, which empties
 * the field and closes it. The field takes focus when it opens.
 */
@Composable
private fun SearchField(words: String, onWords: (String) -> Unit, onClear: () -> Unit, modifier: Modifier) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    val shape = RoundedCornerShape(OrreryTokens.Radius.two)
    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        BasicTextField(
            value = words,
            onValueChange = onWords,
            modifier = Modifier
                .weight(1f)
                .focusRequester(focus)
                .testTag("search-field")
                .semantics { contentDescription = SEARCH_LABEL }
                .background(OrreryTokens.Surface.well, shape)
                .border(OrreryTokens.Size.hairline, OrreryTokens.Line.firm, shape),
            textStyle = OrreryTokens.Type.body.copy(color = OrreryTokens.Ink.one),
            cursorBrush = SolidColor(OrreryTokens.Role.focus),
            singleLine = true,
            decorationBox = { inner ->
                Box(
                    modifier = Modifier.padding(
                        horizontal = OrreryTokens.Space.twelve,
                        vertical = OrreryTokens.Space.eight,
                    ),
                ) {
                    if (words.isEmpty()) {
                        Text(SEARCH_LABEL, color = OrreryTokens.Ink.three, style = OrreryTokens.Type.body)
                    }
                    inner()
                }
            },
        )
        TextButton(onClick = onClear, modifier = Modifier.testTag("clear-search")) {
            Text("Clear")
        }
    }
}

/** The search found no project: the words searched, and what search looks at. */
@Composable
private fun NothingMatches(words: String) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(OrreryTokens.Space.sixteen),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text("Nothing matches “$words”.", style = OrreryTokens.Type.body, color = OrreryTokens.Ink.three)
        Text(
            "Search looks at titles, directories, hosts, states, and the names of epics and contracts.",
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.three,
        )
    }
}

/** The inbox's error state: the server did not answer, the inbox is trying again, and Try now reads at once. */
@Composable
private fun DidNotAnswer(onTryNow: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(OrreryTokens.Space.sixteen),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
    ) {
        Text("The server did not answer.", style = OrreryTokens.Type.body, color = OrreryTokens.Role.bad)
        Text("Trying again…", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        TextButton(onClick = onTryNow, modifier = Modifier.testTag("try-now")) {
            Text("Try now")
        }
    }
}
