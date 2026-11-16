package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow

/** Most running step rows one epic shows; the rest fold into one "more" line. */
private const val MAX_STEP_ROWS = 4

/**
 * Home's Epics group. Each live epic shows its slug, its repository, up to [MAX_STEP_ROWS] of its
 * running and held steps (a held one marked), and a "more" line for the rest; its ended steps show
 * as one count. Live epics waiting to start fold into one line, "n accepted, waiting to start", that opens
 * to list them. Folded epics fold into one line that opens to list them. Tapping an epic, a step or
 * a "more" line opens the epic. Nothing here answers a step: the group only opens epics. No epics
 * draws nothing.
 */
@Composable
fun EpicsGroup(epics: List<EpicRow>, onOpenEpic: (String) -> Unit, modifier: Modifier = Modifier) {
    var waitingOpen by remember { mutableStateOf(false) }
    var foldedOpen by remember { mutableStateOf(false) }
    if (epics.isEmpty()) return
    val live = epics.filter { it.live && !it.waiting }
    val waiting = epics.filter { it.live && it.waiting }
    val folded = epics.filterNot { it.live }
    Column(modifier = modifier.fillMaxWidth().testTag("epics-group").padding(vertical = 4.dp)) {
        Text(
            "Epics",
            style = MaterialTheme.typography.titleSmall,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )
        for (epic in live) {
            LiveEpic(epic, onOpenEpic)
        }
        if (waiting.isNotEmpty()) {
            FoldLine("${waiting.size} accepted, waiting to start", "epics-waiting") { waitingOpen = !waitingOpen }
            if (waitingOpen) {
                for (epic in waiting) {
                    EpicLine(epic, onOpenEpic, start = 24.dp)
                }
            }
        }
        if (folded.isNotEmpty()) {
            FoldLine("${folded.size} folded", "epics-folded") { foldedOpen = !foldedOpen }
            if (foldedOpen) {
                for (epic in folded) {
                    EpicLine(epic, onOpenEpic, start = 24.dp)
                }
            }
        }
    }
}

@Composable
private fun FoldLine(text: String, tag: String, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .clickable(onClick = onToggle)
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Text(text, style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun LiveEpic(epic: EpicRow, onOpenEpic: (String) -> Unit) {
    val shown = epic.runningSteps.take(MAX_STEP_ROWS)
    val more = epic.runningSteps.size - shown.size
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
        EpicLine(epic, onOpenEpic, start = 0.dp)
        for (step in shown) {
            StepLine(epic.id, step, onOpenEpic)
        }
        if (more > 0) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("epic-more:${epic.id}")
                    .clickable { onOpenEpic(epic.id) }
                    .padding(start = 24.dp, top = 4.dp, bottom = 4.dp),
            ) {
                Text("$more more", style = MaterialTheme.typography.labelSmall)
            }
        }
        if (epic.endedSteps > 0) {
            Text(
                "${epic.endedSteps} ended",
                style = MaterialTheme.typography.labelSmall,
                modifier = Modifier.padding(start = 24.dp, top = 4.dp),
            )
        }
    }
}

@Composable
private fun EpicLine(epic: EpicRow, onOpenEpic: (String) -> Unit, start: Dp) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic:${epic.id}")
            .clickable { onOpenEpic(epic.id) }
            .padding(start = start, top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(epic.slug, style = MaterialTheme.typography.bodyLarge)
        Text(epic.repository, style = MaterialTheme.typography.labelSmall)
    }
}

@Composable
private fun StepLine(epicId: String, step: StepRow, onOpenEpic: (String) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("step:${step.id}")
            .clickable { onOpenEpic(epicId) }
            .padding(start = 24.dp, top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(step.agent, style = MaterialTheme.typography.bodySmall)
            Text(step.task, style = MaterialTheme.typography.labelSmall)
            Text(step.doing, style = MaterialTheme.typography.bodySmall)
        }
        if (step.held) {
            Text(
                "held",
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.testTag("held:${step.id}"),
            )
        }
    }
}
