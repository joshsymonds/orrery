package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.R
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException

internal enum class PaneFilter(val label: String, val tag: String) {
    ALL("All", "filter-all"),
    NEW("New", "filter-new"),
    WAITING("Waiting", "filter-waiting"),
}

/**
 * The view, the collapsed folders, and whether the ended line is open. The caller keeps one above the screens it
 * switches between, so all three survive opening a pane and coming back.
 */
class PaneListState {
    internal var filter by mutableStateOf(PaneFilter.ALL)
    internal var collapsed by mutableStateOf(emptySet<String>())
    internal var endedOpen by mutableStateOf(false)

    internal fun toggleFolder(key: String) {
        collapsed = if (key in collapsed) collapsed - key else collapsed + key
    }

    internal fun toggleEnded() {
        endedOpen = !endedOpen
    }
}

/** A [PaneListState] that the composition remembers, for a caller that keeps none of its own. */
@Composable
internal fun rememberPaneListState(): PaneListState = remember { PaneListState() }

private sealed interface PaneLoad {
    data object Loading : PaneLoad
    data object Failed : PaneLoad
    data class Loaded(val panes: List<Pane>) : PaneLoad
}

/** The Mocha colour of a pane's state word; any other state keeps the default text colour. */
private fun stateColorOf(state: String): Color = when (state) {
    "working" -> Mocha.Yellow
    "waiting" -> Mocha.Peach
    "done" -> Mocha.Green
    else -> Color.Unspecified
}

private fun ageOf(seconds: Long): String = when {
    seconds < 60 -> "${seconds}s"
    seconds < 3_600 -> "${seconds / 60}m"
    seconds < 86_400 -> "${seconds / 3_600}h"
    else -> "${seconds / 86_400}d"
}

/** The tallest the picker panel grows before it scrolls, so the pane list keeps room below it. */
private val PICKER_MAX_HEIGHT = 320.dp

/** The indent of one folder level, in dp. */
private const val INDENT_DP = 16

/** Panes in a tmux session with this prefix are internal to orrery and are not listed. */
private const val SHELL_SESSION_PREFIX = "_orrery-shell-"

/**
 * Every pane as a folder tree, or the New and Waiting panes as flat lists, with a filter. The New
 * button at the top opens the picker above the filters; a run it answers opens that pane through
 * [onOpenPaneId], and a start it answers opens that session through [onStartedSession]. The Pools
 * button opens the pools view through [onOpenPools]. Tapping a folder header collapses or expands
 * it. Tapping a row opens that pane. In the All view, the ended headless sessions sit under one
 * "n ended" line below the tree, which opens to a row per ended session.
 * Needs you is the first item, from the waiting panes and the live epics' details; its answer, retry and stop calls run
 * off the main thread, and a call that did not land shows as a notice under it. The Epics group sits above the rows and
 * opens an epic through [onOpenEpic]. Each live epic's line reads its detail, and one that has no detail reads "epic
 * unavailable". An epics load that fails shows no group and leaves the panes as they are.
 */
@Composable
fun PaneListScreen(
    client: OrreryClient,
    onOpenPane: (Pane) -> Unit,
    onOpenPaneId: (String) -> Unit,
    onStartedSession: (String) -> Unit,
    onOpenSettings: () -> Unit,
    onOpenPools: () -> Unit = {},
    modifier: Modifier = Modifier,
    onOpenEpic: (String) -> Unit = {},
    now: () -> Long = { System.currentTimeMillis() / 1000 },
    state: PaneListState = rememberPaneListState(),
) {
    var load by remember { mutableStateOf<PaneLoad>(PaneLoad.Loading) }
    var epics by remember { mutableStateOf<List<EpicRow>>(emptyList()) }
    var details by remember { mutableStateOf<Map<String, EpicDetail>>(emptyMap()) }
    var generation by remember { mutableIntStateOf(0) }
    var epicsGeneration by remember { mutableIntStateOf(0) }
    var notice by remember { mutableStateOf("") }
    var picking by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(client, generation) {
        load = PaneLoad.Loading
        load = try {
            PaneLoad.Loaded(
                withContext(Dispatchers.IO) {
                    client.panes().filterNot { it.session.startsWith(SHELL_SESSION_PREFIX) } + client.headless()
                },
            )
        } catch (error: IOException) {
            PaneLoad.Failed
        }
    }

    // A new client drops the epics it read, as it drops the panes. A reload keeps them on screen until the new read
    // lands.
    LaunchedEffect(client) {
        epics = emptyList()
        details = emptyMap()
    }

    LaunchedEffect(client, generation, epicsGeneration) {
        val listed = try {
            withContext(Dispatchers.IO) { client.epics() }
        } catch (error: IOException) {
            emptyList()
        }
        details = epicDetailsOf(client, listed)
        epics = listed
    }

    // send runs one epic command off the main thread. What did not land shows as the notice, with the typed answer
    // beside it. Then the epics are read again.
    fun send(answer: String = "", call: () -> StepCallResult) {
        scope.launch {
            notice = withContext(Dispatchers.IO) { commandNoticeOf(call, answer) }
            epicsGeneration++
        }
    }

    val onAnswer: (String, String, String) -> String = { epic, questionId, text ->
        send(answer = text) { client.answerEpic(epic, questionId, text) }
        ""
    }
    val onRetry: (String) -> Unit = { epic -> send { client.retryEpic(epic) } }
    val onStop: (String) -> Unit = { epic -> send { client.stopEpic(epic) } }

    Column(modifier = modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Row(
                modifier = Modifier.height(IntrinsicSize.Min),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TitleMark(modifier = Modifier.fillMaxHeight())
                Spacer(modifier = Modifier.width(8.dp))
                Text("Panes", style = MaterialTheme.typography.titleLarge)
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
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
                TextButton(onClick = onOpenSettings) {
                    Text("Settings")
                }
            }
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
                modifier = Modifier.heightIn(max = PICKER_MAX_HEIGHT),
            )
        }
        Row(modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp)) {
            PaneFilter.entries.forEach { option ->
                TextButton(
                    onClick = { state.filter = option },
                    modifier = Modifier.testTag(option.tag),
                ) {
                    Text(
                        option.label,
                        fontWeight = if (option == state.filter) FontWeight.Bold else FontWeight.Normal,
                    )
                }
            }
        }
        when (val current = load) {
            PaneLoad.Loading -> Text("Loading...", modifier = Modifier.padding(16.dp))
            PaneLoad.Failed -> Column(modifier = Modifier.padding(16.dp)) {
                Text("could not load panes", color = MaterialTheme.colorScheme.error)
                TextButton(onClick = { generation++ }) {
                    Text("Retry")
                }
            }
            is PaneLoad.Loaded -> PaneRows(
                panes = current.panes,
                epics = epics,
                details = details,
                notice = notice,
                filter = state.filter,
                collapsed = state.collapsed,
                endedOpen = state.endedOpen,
                now = now(),
                onOpenPane = onOpenPane,
                onOpenEpic = onOpenEpic,
                onAnswer = onAnswer,
                onRetry = onRetry,
                onStop = onStop,
                onToggleFolder = state::toggleFolder,
                onToggleEnded = state::toggleEnded,
            )
        }
    }
}

/**
 * The detail of every live epic, each read at once. An epic whose read fails, or that has no detail, has no entry, so
 * its line reads "epic unavailable". A folded epic is never read.
 */
private suspend fun epicDetailsOf(
    client: OrreryClient,
    epics: List<EpicRow>,
): Map<String, EpicDetail> = coroutineScope {
    val reads = epics.filter { it.live }.map { epic ->
        async(Dispatchers.IO) {
            try {
                client.epic(epic.id)?.let { epic.id to it }
            } catch (error: IOException) {
                null
            }
        }
    }
    reads.awaitAll().filterNotNull().toMap()
}

// The notice an epic command leaves that did not land by the server's reason, or that the epic could not be reached.
private const val EPIC_UNREACHABLE = "could not reach the epic"

// commandNoticeOf runs one epic command and says what did not land: "" when it landed, else the server's reason or that
// the epic could not be reached. An answer that did not land keeps its text in the notice, so it is not lost with the
// form.
private fun commandNoticeOf(call: () -> StepCallResult, answer: String): String {
    val reason = try {
        when (val result = call()) {
            StepCallResult.Landed -> return ""
            is StepCallResult.Refused -> result.message
        }
    } catch (error: IOException) {
        EPIC_UNREACHABLE
    }
    return if (answer.isEmpty()) "not sent: $reason" else "not sent: $reason. Your answer: $answer"
}

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

@Composable
private fun PaneRows(
    panes: List<Pane>,
    epics: List<EpicRow>,
    details: Map<String, EpicDetail>,
    notice: String,
    filter: PaneFilter,
    collapsed: Set<String>,
    endedOpen: Boolean,
    now: Long,
    onOpenPane: (Pane) -> Unit,
    onOpenEpic: (String) -> Unit,
    onAnswer: (epic: String, questionId: String, text: String) -> String,
    onRetry: (epic: String) -> Unit,
    onStop: (epic: String) -> Unit,
    onToggleFolder: (String) -> Unit,
    onToggleEnded: () -> Unit,
) {
    val waiting = remember(panes) { waitingPanes(panes) }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item(key = "needs") {
            Column(modifier = Modifier.fillMaxWidth()) {
                NeedsYou(
                    waiting = waiting,
                    epics = epics,
                    details = details,
                    onOpenPane = onOpenPane,
                    onOpenEpic = onOpenEpic,
                    onAnswer = onAnswer,
                    onRetry = onRetry,
                    onStop = onStop,
                )
                if (notice.isNotEmpty()) {
                    Text(
                        notice,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(horizontal = 16.dp).testTag("needs-notice"),
                    )
                }
            }
        }
        item(key = "epics") {
            EpicsGroup(epics = epics, details = details, now = now, onOpenEpic = onOpenEpic)
        }
        when (filter) {
            PaneFilter.ALL -> {
                // Ended sessions are left out of the tree, so a folder holding only them is not built.
                items(
                    treeRows(panes.filterNot(::isEnded), collapsed),
                    key = { row ->
                        when (row) {
                            is Folder -> "folder:${row.key}"
                            is PaneAt -> "pane:${row.pane.id}"
                        }
                    },
                ) { row ->
                    when (row) {
                        is Folder -> FolderRow(
                            folder = row,
                            onToggle = { onToggleFolder(row.key) },
                        )
                        is PaneAt -> PaneRow(
                            pane = row.pane,
                            depth = row.depth,
                            project = null,
                            now = now,
                            onOpenPane = onOpenPane,
                        )
                    }
                }
                endedRows(panes.filter(::isEnded), endedOpen, now, onOpenPane, onToggleEnded)
            }
            PaneFilter.NEW -> flatRows(newPanes(panes), now, onOpenPane)
            PaneFilter.WAITING -> flatRows(waitingPanes(panes), now, onOpenPane)
        }
    }
}

/** A headless session that has ended: stopped, or failed and seen since it failed. */
private fun isEnded(pane: Pane): Boolean =
    pane.headless && (pane.state == "stopped" || (pane.state == "failed" && pane.changed <= pane.seen))

/** The ended sessions as one line below the tree, which opens to a row per session; no line when none ended. */
private fun LazyListScope.endedRows(
    ended: List<Pane>,
    open: Boolean,
    now: Long,
    onOpenPane: (Pane) -> Unit,
    onToggle: () -> Unit,
) {
    if (ended.isEmpty()) return
    item(key = "ended") {
        EndedLine(count = ended.size, open = open, onToggle = onToggle)
    }
    if (open) flatRows(ended, now, onOpenPane)
}

/** The panes as one flat list, each row carrying its project. */
private fun LazyListScope.flatRows(panes: List<Pane>, now: Long, onOpenPane: (Pane) -> Unit) {
    items(panes, key = { "pane:${it.id}" }) { pane ->
        PaneRow(pane = pane, depth = 0, project = projectOf(pane), now = now, onOpenPane = onOpenPane)
    }
}

@Composable
private fun EndedLine(count: Int, open: Boolean, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("ended-toggle")
            .clickable(onClick = onToggle)
            .padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(if (open) "\u25BE" else "\u25B8", style = MaterialTheme.typography.titleSmall)
        Text("$count ended", style = MaterialTheme.typography.titleSmall)
    }
}

@Composable
private fun FolderRow(folder: Folder, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("folder:${folder.key}")
            .clickable(onClick = onToggle)
            .padding(start = 16.dp + (INDENT_DP * folder.depth).dp, end = 16.dp, top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(if (folder.collapsed) "\u25B8" else "\u25BE", style = MaterialTheme.typography.titleSmall)
        Text(folder.name, style = MaterialTheme.typography.titleSmall)
    }
}

@Composable
private fun PaneRow(pane: Pane, depth: Int, project: String?, now: Long, onOpenPane: (Pane) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("pane-row:${pane.id}")
            .clickable { onOpenPane(pane) }
            .padding(start = 16.dp + (INDENT_DP * depth).dp, end = 16.dp, top = 8.dp, bottom = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        if (pane.unread()) {
            Box(
                modifier = Modifier
                    .padding(top = 6.dp)
                    .size(8.dp)
                    .background(MaterialTheme.colorScheme.primary, CircleShape)
                    .semantics { contentDescription = "unseen" },
            )
        } else {
            Spacer(modifier = Modifier.width(8.dp))
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(pane.label, style = MaterialTheme.typography.bodyLarge)
            Text(pane.summary, style = MaterialTheme.typography.bodySmall)
            if (project != null) {
                Text(project, style = MaterialTheme.typography.labelSmall)
            }
        }
        Column(horizontalAlignment = Alignment.End) {
            Text(
                pane.state,
                style = MaterialTheme.typography.labelMedium,
                color = stateColorOf(pane.state),
            )
            if (pane.changed != 0L) {
                Text(ageOf(now - pane.changed), style = MaterialTheme.typography.labelSmall)
            }
        }
    }
}
