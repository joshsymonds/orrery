package io.github.joshsymonds.orrery.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.io.IOException

/** The fixed delay the inbox waits before it reads again after a read that did not answer. */
internal const val RETRY_DELAY_MILLIS = 5_000L

/** The view box the bell is drawn in, and its stroke, in those units. */
private const val BELL_VIEW = 16f
private const val BELL_STROKE = 1.5f

/** The bell's accessible label: the head has no room for the word, so the bell is labelled by it, not shown with it. */
private const val NOTICES_WORDS = "Notices"

/**
 * The inbox's state: the chip on, the search words (null while the search is closed), the folded projects, the projects
 * whose ended line is open, the view (the inbox or the archived tree), and the tree of the last read that answered. The caller keeps one above the screens it
 * switches between, so all of it survives opening a conversation and coming back.
 */
class PaneListState {
    internal var narrow by mutableStateOf(Narrow.NONE)
    internal var search by mutableStateOf<String?>(null)
    internal var folded by mutableStateOf(emptySet<String>())
    internal var endedOpen by mutableStateOf(emptySet<String>())
    internal var view by mutableStateOf(TreeView.INBOX)
    internal var held: TreeData? = null

    internal fun toggleFolded(key: String) {
        folded = if (key in folded) folded - key else folded + key
    }

    internal fun toggleEnded(key: String) {
        endedOpen = if (key in endedOpen) endedOpen - key else endedOpen + key
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
 * The inbox: the head with the Orrery mark, Search, New, Pools, the Notices bell and Settings; then, while search is
 * open, its field; then the two chips and each project as a card, its conversations hanging under it with what steers
 * them, and its ended line. The chips narrow the tree, search narrows what the chips left, and a folded card says what
 * waits in it. The foot opens the archived tree and returns from it. A tap on a conversation calls [onOpenPane] with
 * its pane, a tap on an epic or one of its steps calls [onOpenEpic], a tap on a contract's row calls [onOpenContract]
 * with its id and the conversation it hangs under, whose pane is null for a stand-in row, and a tap on a project's
 * name calls [onOpenProject] with its key. A long press on a conversation or an epic opens its sheet, where the owner
 * marks it seen or archives it, and a landed change reads the tree again; a landed archive also shows the [ArchiveToast]
 * at the foot, whose Undo reads the tree again. New opens the picker as a bottom sheet; a
 * run it answers opens that pane through [onOpenPaneId], and a start it answers opens that session through
 * [onStartedSession]. The Pools button opens the pools view through [onOpenPools]. The bell counts the unread notices,
 * read on start and every minute while started, and opens the notices through [onOpenNotices].
 *
 * Until the first answer the inbox says it is loading. A read that fails, the panes, a headless or archived list, or an
 * epic or its detail, shows that the server did not answer, and reads again after [RETRY_DELAY_MILLIS] or at once on
 * Try now. A failed contracts read keeps the contracts last read, and a failed host read keeps the host last read.
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
    val toast = rememberArchiveToast()

    LaunchedEffect(client, generation) {
        val read = readTree(client, state.held)
        if (read == null) {
            failed = true
            delay(RETRY_DELAY_MILLIS)
            generation++
        } else {
            data = read
            state.held = read
            failed = false
        }
    }

    BackHandler(enabled = state.search != null) { state.search = null }

    Box(modifier = modifier.fillMaxSize()) {
        Column(modifier = Modifier.fillMaxSize()) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Row(
                    modifier = Modifier.height(IntrinsicSize.Min),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    TitleMark(modifier = Modifier.fillMaxHeight())
                    Spacer(modifier = Modifier.width(OrreryTokens.Space.eight))
                    Text("orrery", style = OrreryTokens.Type.title)
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    TextButton(
                        onClick = { state.search = state.search ?: "" },
                        modifier = Modifier.testTag("open-search"),
                    ) {
                        Text("Search")
                    }
                    TextButton(
                        onClick = { picking = true },
                        modifier = Modifier.testTag("picker-new"),
                    ) {
                        Text("New")
                    }
                    TextButton(
                        onClick = onOpenPools,
                        modifier = Modifier.testTag("open-pools"),
                    ) {
                        Text("Pools")
                    }
                    NoticesBell(unread = notices.unread, onClick = onOpenNotices)
                    TextButton(onClick = onOpenSettings) {
                        Text("Settings")
                    }
                }
            }
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
                InboxSheet(
                    row = row,
                    client = client,
                    onArchived = { title, kind, id -> toast.show(title, kind, id) },
                    onLanded = {
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
        )
    }
}

/**
 * Reads the lists, every live epic's detail, the contracts and the host. Null when a list or an epic did not answer. A
 * contracts read that fails keeps the contracts of [held], and a host read that fails keeps its host.
 */
internal suspend fun readTree(client: OrreryClient, held: TreeData?): TreeData? = withContext(Dispatchers.IO) {
    try {
        val panes = client.panes() + client.headless()
        val archived = client.archivedSessions()
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
        TreeData(panes, archived, epics, details, contracts, host)
    } catch (error: IOException) {
        null
    }
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
    now: Long,
    onOpenPane: (Pane) -> Unit,
    onOpenEpic: (String) -> Unit,
    onOpenProject: (String) -> Unit,
    onOpenContract: (id: String, conversation: Pane?) -> Unit,
    onNew: () -> Unit,
    onLongPress: (SheetRow) -> Unit,
) {
    val projects = remember(data, state.view) {
        inboxTree(data.panes, data.archived, data.epics, data.details, data.contracts, data.host, state.view)
    }
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
    // Each contract row's key, with the conversation it hangs under: null for a stand-in row, which no conversation holds.
    val conversationOfContract = remember(projects) {
        projects.flatMap { it.convos + it.ended }.flatMap { convo ->
            convo.hung.filterIsInstance<ContractNode>().map { it.key to convo.pane }
        }.toMap()
    }
    val onOpen: (String) -> Unit = { key ->
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
                        convo = { convo -> ConvoRowItem(convo = convo, now = now, onOpen = onOpen, onLongPress = onLongPress) },
                        modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
                        onOpenProject = onOpenProject,
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

private const val SEARCH_LABEL = "Search sessions and epics"

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

/**
 * The bell that opens the notices: its unread count in the new role when there is one, and nothing beside it at zero.
 */
@Composable
private fun NoticesBell(unread: Int, onClick: () -> Unit) {
    TextButton(onClick = onClick, modifier = Modifier.testTag("open-notices")) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            BellGlyph()
            if (unread > 0) {
                Text(
                    unread.toString(),
                    modifier = Modifier.padding(start = OrreryTokens.Space.four).testTag("notices-unread"),
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Role.new,
                )
            }
        }
    }
}

// A bell: a dome on a rim, with its clapper, drawn as the Notices screen draws its own.
@Composable
private fun BellGlyph() {
    Canvas(modifier = Modifier.size(OrreryTokens.Size.glyph).semantics { contentDescription = NOTICES_WORDS }) {
        val stroke = Stroke(width = bellPx(BELL_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawPath(
            Path().apply {
                moveTo(bellPx(3.5f), bellPx(11.5f))
                lineTo(bellPx(3.5f), bellPx(7.5f))
                cubicTo(bellPx(3.5f), bellPx(4.5f), bellPx(12.5f), bellPx(4.5f), bellPx(12.5f), bellPx(7.5f))
                lineTo(bellPx(12.5f), bellPx(11.5f))
                lineTo(bellPx(13.5f), bellPx(12.5f))
                lineTo(bellPx(2.5f), bellPx(12.5f))
                close()
            },
            OrreryTokens.Ink.two,
            style = stroke,
        )
        drawCircle(OrreryTokens.Ink.two, bellPx(1f), Offset(bellPx(8f), bellPx(14f)))
    }
}

private fun DrawScope.bellPx(view: Float): Float = view * size.width / BELL_VIEW

/**
 * The app's colour icon as a disc: the launcher's background and foreground layers, clipped to a
 * circle. The 72-unit centre of the 108 canvas fills the circle, so each layer is scaled by 1.5
 * about the disc's centre. Decorative, so it has no content description.
 */
@Composable
private fun TitleMark(modifier: Modifier) {
    Box(modifier = modifier.aspectRatio(1f).clip(CircleShape).testTag("title-mark")) {
        Image(
            painter = painterResource(R.drawable.ic_launcher_background),
            contentDescription = null,
            modifier = Modifier.matchParentSize().scale(1.5f),
        )
        Image(
            painter = painterResource(R.drawable.ic_launcher_foreground),
            contentDescription = null,
            modifier = Modifier.matchParentSize().scale(1.5f),
        )
    }
}
