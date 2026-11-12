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
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

internal enum class PaneFilter(val label: String, val tag: String) {
    ALL("All", "filter-all"),
    NEW("New", "filter-new"),
    WAITING("Waiting", "filter-waiting"),
}

/**
 * The view and the collapsed folders of the pane list. The caller keeps one above the screens it
 * switches between, so both survive opening a pane and coming back.
 */
class PaneListState {
    internal var filter by mutableStateOf(PaneFilter.ALL)
    internal var collapsed by mutableStateOf(emptySet<String>())

    internal fun toggleFolder(key: String) {
        collapsed = if (key in collapsed) collapsed - key else collapsed + key
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

/**
 * Every pane as a folder tree, or the New and Waiting panes as flat lists, with a filter. The New
 * button at the top opens the picker above the filters; a run it answers opens that pane through
 * [onOpenPaneId]. Tapping a folder header collapses or expands it. Tapping a row opens that pane.
 */
@Composable
fun PaneListScreen(
    client: OrreryClient,
    onOpenPane: (Pane) -> Unit,
    onOpenPaneId: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
    state: PaneListState = rememberPaneListState(),
) {
    var load by remember { mutableStateOf<PaneLoad>(PaneLoad.Loading) }
    var generation by remember { mutableIntStateOf(0) }
    var picking by remember { mutableStateOf(false) }

    LaunchedEffect(client, generation) {
        load = PaneLoad.Loading
        load = try {
            PaneLoad.Loaded(withContext(Dispatchers.IO) { client.panes() })
        } catch (error: IOException) {
            PaneLoad.Failed
        }
    }

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
                filter = state.filter,
                collapsed = state.collapsed,
                now = now(),
                onOpenPane = onOpenPane,
                onToggleFolder = state::toggleFolder,
            )
        }
    }
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
    filter: PaneFilter,
    collapsed: Set<String>,
    now: Long,
    onOpenPane: (Pane) -> Unit,
    onToggleFolder: (String) -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        when (filter) {
            PaneFilter.ALL -> items(
                treeRows(panes, collapsed),
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
            PaneFilter.NEW -> flatRows(newPanes(panes), now, onOpenPane)
            PaneFilter.WAITING -> flatRows(waitingPanes(panes), now, onOpenPane)
        }
    }
}

/** The panes as one flat list, each row carrying its project. */
private fun LazyListScope.flatRows(panes: List<Pane>, now: Long, onOpenPane: (Pane) -> Unit) {
    items(panes, key = { "pane:${it.id}" }) { pane ->
        PaneRow(pane = pane, depth = 0, project = projectOf(pane), now = now, onOpenPane = onOpenPane)
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
