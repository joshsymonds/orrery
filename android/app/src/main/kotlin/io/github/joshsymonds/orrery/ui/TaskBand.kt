package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The epic's one line, over a task's record and its worker's session: the epic's slug in bold, its state word and its
 * done tasks, then its blocker pill in its tone (attention when the epic asks the owner, quiet when its steerer holds
 * the park or the question), and a chevron. Pressing it calls [onOpenEpic].
 */
@Composable
fun EpicPin(detail: EpicDetail, now: Long, onOpenEpic: () -> Unit, modifier: Modifier = Modifier) {
    val state = statePillOf(detail, now)
    val blocker = blockerPillOf(detail, now)
    val tasks = recordTasksOf(detail)
    val done = tasks.count { it.integrated }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .testTag("epic-pin")
            .clickable(role = Role.Button, onClick = onOpenEpic)
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            detail.epic.slug,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            color = OrreryTokens.Ink.one,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            "${state.word} · $done of ${tasks.size} tasks done",
            modifier = Modifier.weight(1f),
            style = OrreryTokens.Type.small,
            color = toneColourOf(state.tone),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (blocker != null) {
            Text(
                blocker.word,
                modifier = Modifier.testTag("epic-pin-blocker"),
                style = OrreryTokens.Type.small,
                color = toneColourOf(blocker.tone),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text("›", style = OrreryTokens.Type.small, color = OrreryTokens.Ink.four)
    }
}

/**
 * The band over a task's record and its worker's session: the [EpicPin] over the epic's task strip, one chip per task
 * in task order, each with its five pips. The [currentTask]'s chip is selected and opens nothing; every other chip
 * calls [onOpenTask] with its task and the phase the task is in, implementing for a planned task.
 */
@Composable
fun ContextBand(
    detail: EpicDetail,
    currentTask: String,
    now: Long,
    onOpenEpic: () -> Unit,
    onOpenTask: (task: String, phase: Phase) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        EpicPin(detail = detail, now = now, onOpenEpic = onOpenEpic)
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .testTag("task-strip")
                .semantics { contentDescription = "this epic's tasks" }
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            recordTasksOf(detail).forEach { task ->
                TaskChip(task = task, current = task.task == currentTask, onOpenTask = onOpenTask)
            }
        }
    }
}

// A task's chip: its title over its pips. The current one is selected and inert; any other opens its task and phase.
@Composable
private fun TaskChip(task: RecordTask, current: Boolean, onOpenTask: (String, Phase) -> Unit) {
    val shape = RoundedCornerShape(OrreryTokens.Radius.one)
    val tapped = if (current) {
        Modifier
    } else {
        val phase = phaseOfPlace(task.place, task.integrated) ?: Phase.IMPLEMENTING
        Modifier.clickable(role = Role.Button) { onOpenTask(task.task, phase) }
    }
    Column(
        modifier = Modifier
            .testTag("task-chip:${task.task}")
            .semantics(mergeDescendants = true) { selected = current }
            .clip(shape)
            .border(OrreryTokens.Size.hairline, if (current) OrreryTokens.Ink.two else OrreryTokens.Line.firm, shape)
            .background(if (current) OrreryTokens.Surface.raised else Color.Transparent, shape)
            .then(tapped)
            .padding(horizontal = OrreryTokens.Space.eight, vertical = OrreryTokens.Space.four),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        Text(
            task.title,
            style = OrreryTokens.Type.small.copy(
                fontWeight = if (current) OrreryTokens.Font.Weight.bold else OrreryTokens.Font.Weight.regular,
            ),
            color = if (current) OrreryTokens.Ink.one else OrreryTokens.Ink.two,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Pips(cells = task.cells)
    }
}
