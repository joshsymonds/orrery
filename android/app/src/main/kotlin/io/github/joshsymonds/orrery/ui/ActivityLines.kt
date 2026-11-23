package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

// How many running things get a line before the rest fold into one count.
private const val LINE_CAP = 3

/**
 * The lines above the composer: one for each task that is running, in the order they started, saying who it is, what
 * it is doing and for how long. Tapping a line calls [onOpen] with its task; tapping the count past [LINE_CAP] calls
 * [onMore]. Nothing running draws nothing, and an ended, failed or stopped task has no line.
 */
@Composable
internal fun ActivityLines(
    tasks: List<BackgroundTask>,
    now: Long,
    onOpen: (BackgroundTask) -> Unit,
    onMore: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val running = tasks.filter { it.status == "running" }.sortedBy { it.started }
    if (running.isEmpty()) return
    Column(modifier.fillMaxWidth().testTag("activity-lines")) {
        running.take(LINE_CAP).forEach { task -> ActivityLine(task, now, onOpen) }
        if (running.size > LINE_CAP) ActivityMore(running.size - LINE_CAP, onMore)
    }
}

@Composable
private fun ActivityLine(task: BackgroundTask, now: Long, onOpen: (BackgroundTask) -> Unit) {
    val agent = task.type == "agent"
    val who = if (agent) task.subagentType.ifEmpty { "agent" } else "shell"
    val doing = if (!agent && task.activity == "shell") "" else task.activity
    Row(
        Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.eight)
            .testTag("activity:${task.id}")
            .clickable(role = Role.Button, onClick = { onOpen(task) })
            .semantics(mergeDescendants = true) {},
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        StateGlyph(if (agent) "working" else "idle")
        Text(
            text = who,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.two,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = task.description,
            style = OrreryTokens.Type.small,
            color = OrreryTokens.Ink.one,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (doing.isNotEmpty()) {
            Text(
                text = doing,
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(text = clock(now - task.started), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.four)
    }
}

@Composable
private fun ActivityMore(count: Int, onMore: () -> Unit) {
    Text(
        text = "and $count more running",
        style = OrreryTokens.Type.small,
        color = OrreryTokens.Ink.three,
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.eight)
            .testTag("activity-more")
            .clickable(role = Role.Button, onClick = onMore),
    )
}
