package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONException
import java.io.IOException

private enum class PaneFilter(val label: String, val tag: String) {
    ALL("All", "filter-all"),
    NEW("New", "filter-new"),
    WAITING("Waiting", "filter-waiting");

    fun keeps(pane: Pane): Boolean = when (this) {
        ALL -> true
        NEW -> pane.changed > pane.seen
        WAITING -> pane.state == "waiting"
    }
}

private sealed interface PaneLoad {
    data object Loading : PaneLoad
    data object Failed : PaneLoad
    data class Loaded(val panes: List<Pane>) : PaneLoad
}

/** The project is the last path segment of the pane's cwd, as orreryd names it. */
private fun projectOf(cwd: String): String = cwd.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }

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

/**
 * Every pane, grouped by project, with a filter. The New button at the top opens the picker
 * above the filters; a run it answers opens that pane through [onOpenPaneId]. Each project
 * header carries a New button for its first visible pane. Tapping a row opens that pane.
 */
@Composable
fun PaneListScreen(
    client: OrreryClient,
    onOpenPane: (Pane) -> Unit,
    onOpenPaneId: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
) {
    var load by remember { mutableStateOf<PaneLoad>(PaneLoad.Loading) }
    var filter by remember { mutableStateOf(PaneFilter.ALL) }
    var generation by remember { mutableIntStateOf(0) }
    var picking by remember { mutableStateOf(false) }

    LaunchedEffect(client, generation) {
        load = PaneLoad.Loading
        load = try {
            PaneLoad.Loaded(withContext(Dispatchers.IO) { client.panes() })
        } catch (error: IOException) {
            PaneLoad.Failed
        } catch (error: JSONException) {
            PaneLoad.Failed
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Panes", style = MaterialTheme.typography.titleLarge)
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
                    onClick = { filter = option },
                    modifier = Modifier.testTag(option.tag),
                ) {
                    Text(
                        option.label,
                        fontWeight = if (option == filter) FontWeight.Bold else FontWeight.Normal,
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
            is PaneLoad.Loaded -> PaneGroups(
                panes = current.panes,
                filter = filter,
                now = now(),
                client = client,
                onOpenPane = onOpenPane,
                onOpened = { generation++ },
            )
        }
    }
}

@Composable
private fun PaneGroups(
    panes: List<Pane>,
    filter: PaneFilter,
    now: Long,
    client: OrreryClient,
    onOpenPane: (Pane) -> Unit,
    onOpened: () -> Unit,
) {
    val groups = panes.filter(filter::keeps).groupBy { projectOf(it.cwd) }
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(groups.entries.toList(), key = { it.key }) { (project, rows) ->
            Column(modifier = Modifier.fillMaxWidth().testTag("group:$project")) {
                Row(
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(project, style = MaterialTheme.typography.titleSmall)
                    NewWindowButton(
                        paneId = rows.first().id,
                        client = client,
                        onOpened = onOpened,
                        modifier = Modifier.testTag("new-window:$project"),
                    )
                }
                rows.forEach { pane ->
                    PaneRow(pane = pane, now = now, onOpenPane = onOpenPane)
                }
            }
        }
    }
}

@Composable
private fun PaneRow(pane: Pane, now: Long, onOpenPane: (Pane) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("pane-row:${pane.id}")
            .clickable { onOpenPane(pane) }
            .padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        if (pane.changed > pane.seen) {
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
