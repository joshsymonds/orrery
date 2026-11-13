package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.selection.selectable
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
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

private sealed interface EpicLoad {
    data object Loading : EpicLoad
    data object Failed : EpicLoad
    data object NotFound : EpicLoad
    data class Loaded(val detail: EpicDetail) : EpicLoad
}

/**
 * One epic: its tasks, each with the status as recorded, and its steps one line each.
 * Ended steps fold into one line that opens. Tapping a step calls [onOpenStep] with it. The back
 * button calls [onBack].
 */
@Composable
fun EpicScreen(
    client: OrreryClient,
    epicId: String,
    onBack: () -> Unit,
    onOpenStep: (StepRow) -> Unit,
    modifier: Modifier = Modifier,
) {
    var load by remember { mutableStateOf<EpicLoad>(EpicLoad.Loading) }
    var generation by remember { mutableIntStateOf(0) }

    LaunchedEffect(client, epicId, generation) {
        load = EpicLoad.Loading
        load = try {
            val detail = withContext(Dispatchers.IO) { client.epic(epicId) }
            if (detail == null) EpicLoad.NotFound else EpicLoad.Loaded(detail)
        } catch (error: IOException) {
            EpicLoad.Failed
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        Row(modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp)) {
            TextButton(onClick = onBack, modifier = Modifier.testTag("epic-back")) {
                Text("‹ Back")
            }
        }
        when (val current = load) {
            EpicLoad.Loading -> Text("Loading...", modifier = Modifier.padding(16.dp))
            EpicLoad.Failed -> Column(modifier = Modifier.padding(16.dp)) {
                Text("could not load epic", color = MaterialTheme.colorScheme.error)
                TextButton(onClick = { generation++ }) {
                    Text("Retry")
                }
            }
            EpicLoad.NotFound -> Text("epic not found", modifier = Modifier.padding(16.dp))
            is EpicLoad.Loaded -> EpicBody(detail = current.detail, onOpenStep = onOpenStep)
        }
    }
}

// In portrait the chart is a square drawing at most CHART_MAX_WIDTH wide, CHART_SIDE_PADDING in from each side, with its
// two key lines under it, which take CHART_KEYS_HEIGHT. The chart gives up height before the list drops below
// LIST_MIN_HEIGHT, so task rows stay on screen.
private val CHART_SIDE_PADDING = 14.dp
private val CHART_MAX_WIDTH = 420.dp
private val CHART_KEYS_HEIGHT = 72.dp
private val LIST_MIN_HEIGHT = 240.dp

// The chart stays outside the list, so it stays on screen while the list scrolls: above the list in portrait, beside it
// in landscape.
@Composable
private fun EpicBody(detail: EpicDetail, onOpenStep: (StepRow) -> Unit) {
    var selectedRow by remember { mutableStateOf<Int?>(null) }
    var selectedSlice by remember { mutableStateOf<Int?>(null) }
    // Tapping a row with a slice marks it and selects its slice; tapping the marked row again clears both; a row with no
    // slice changes nothing.
    val onTapTask: (Int) -> Unit = { row ->
        val slice = sliceOf(detail, row)
        when {
            row == selectedRow -> {
                selectedRow = null
                selectedSlice = null
            }
            slice != null -> {
                selectedRow = row
                selectedSlice = slice
            }
        }
    }
    // The chart selects a slice or clears its selection (null); either way no row is marked.
    val onSelectSlice: (Int?) -> Unit = { slice ->
        selectedSlice = slice
        selectedRow = null
    }
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        if (maxWidth > maxHeight) {
            Row(modifier = Modifier.fillMaxSize()) {
                EpicChart(
                    detail.slices,
                    taskCount = detail.tasks.size,
                    selected = selectedSlice,
                    onSelect = onSelectSlice,
                    modifier = Modifier.weight(1f).fillMaxHeight(),
                )
                EpicList(detail, onOpenStep, selectedRow, onTapTask, modifier = Modifier.weight(1f).fillMaxHeight())
            }
        } else {
            val side = minOf(
                maxWidth - CHART_SIDE_PADDING * 2,
                CHART_MAX_WIDTH,
                maxHeight - LIST_MIN_HEIGHT - CHART_KEYS_HEIGHT,
            ).coerceAtLeast(0.dp)
            Column(modifier = Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                EpicChart(
                    detail.slices,
                    taskCount = detail.tasks.size,
                    selected = selectedSlice,
                    onSelect = onSelectSlice,
                    modifier = Modifier.size(side, side + CHART_KEYS_HEIGHT),
                )
                EpicList(detail, onOpenStep, selectedRow, onTapTask, modifier = Modifier.fillMaxWidth().weight(1f))
            }
        }
    }
}

// sliceOf is the index of the slice labelled with the row's slug, or null when no slice carries that slug.
private fun sliceOf(detail: EpicDetail, row: Int): Int? =
    detail.slices.indexOfFirst { it.task == detail.tasks[row].slug }.takeIf { it >= 0 }

@Composable
private fun EpicList(
    detail: EpicDetail,
    onOpenStep: (StepRow) -> Unit,
    selectedRow: Int?,
    onTapTask: (Int) -> Unit,
    modifier: Modifier,
) {
    val running = detail.steps.filter { it.end.isEmpty() }
    val ended = detail.steps.filter { it.end.isNotEmpty() }
    var endedOpen by remember { mutableStateOf(false) }

    LazyColumn(modifier = modifier) {
        item("header") {
            Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
                Text(detail.epic.slug, style = MaterialTheme.typography.titleLarge)
                Text(
                    "${detail.epic.repository} · ${detail.epic.outcome}",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
        if (detail.epic.problem.isNotEmpty()) {
            item("problem") {
                Text(
                    detail.epic.problem,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
        }
        if (detail.requirements.isNotEmpty()) {
            item("requirements-heading") { SectionHeading("Requirements") }
            itemsIndexed(detail.requirements, key = { index, _ -> "requirement:$index" }) { index, requirement ->
                RequirementRow(index, requirement)
            }
        }
        item("tasks-heading") { SectionHeading("Tasks") }
        itemsIndexed(detail.tasks, key = { index, _ -> "task:$index" }) { index, task ->
            TaskRow(index, task, selected = index == selectedRow, onTap = { onTapTask(index) })
        }
        item("steps-heading") { SectionHeading("Steps") }
        items(running, key = { "step:${it.id}" }) { step ->
            StepLine(step = step, onOpenStep = onOpenStep)
        }
        if (ended.isNotEmpty()) {
            item("ended-steps") {
                TextButton(
                    onClick = { endedOpen = !endedOpen },
                    modifier = Modifier.testTag("ended-steps"),
                ) {
                    Text("${ended.size} ended steps")
                }
            }
            if (endedOpen) {
                items(ended, key = { "step:${it.id}" }) { step ->
                    StepLine(step = step, onOpenStep = onOpenStep)
                }
            }
        }
    }
}

@Composable
private fun SectionHeading(title: String) {
    Text(
        title,
        style = MaterialTheme.typography.titleSmall,
        modifier = Modifier.padding(start = 16.dp, top = 12.dp, end = 16.dp, bottom = 4.dp),
    )
}

@Composable
private fun TaskRow(index: Int, task: EpicTaskRow, selected: Boolean, onTap: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("task-row:$index")
            .selectable(selected = selected, onClick = onTap)
            .background(if (selected) Mocha.Surface0 else Color.Transparent)
            .padding(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Text(task.slug, style = MaterialTheme.typography.bodyLarge)
        Text(task.subject, style = MaterialTheme.typography.bodySmall)
        Text(task.status, style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun RequirementRow(index: Int, requirement: EpicRequirementRow) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("requirement-row:$index")
            .padding(horizontal = 16.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(requirement.id, style = MaterialTheme.typography.bodyMedium)
        Text(requirement.text, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
        Text(requirement.kind, style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun StepLine(step: StepRow, onOpenStep: (StepRow) -> Unit) {
    val end = when {
        step.end.isNotEmpty() -> step.end
        step.held -> "held"
        else -> "running"
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("step-row:${step.id}")
            .clickable { onOpenStep(step) }
            .padding(horizontal = 16.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(step.agent, style = MaterialTheme.typography.bodyMedium)
            if (step.task.isNotEmpty()) {
                Text(step.task, style = MaterialTheme.typography.labelSmall)
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
            step.checks.forEach { check -> CheckPip(check) }
        }
        Text(end, style = MaterialTheme.typography.labelMedium)
    }
}

/** One stored check: green when it passed, red when it failed. */
@Composable
private fun CheckPip(check: StepCheckRow) {
    val outcome = if (check.ok) "passed" else "failed"
    Box(
        modifier = Modifier
            .size(8.dp)
            .background(if (check.ok) Mocha.Green else Mocha.Red, CircleShape)
            .testTag(if (check.ok) "pip:ok" else "pip:failed")
            .semantics { contentDescription = "${check.name}: $outcome" },
    )
}
