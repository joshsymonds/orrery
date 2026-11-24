package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/**
 * The sub-agents or the shells left running for a conversation, one row per task. [now] and each task's
 * started time are unix seconds, so a row's clock is the time since it started.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WorkListSheet(agents: Boolean, tasks: List<BackgroundTask>, now: Long, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
        ) {
            Text(
                text = (if (agents) "Sub-agents" else "Left running").inLabelCase(),
                style = OrreryTokens.Type.label,
                color = OrreryTokens.Ink.two,
                modifier = Modifier.testTag("list-head"),
            )
            Text(
                text = if (agents) {
                    "These run for this conversation."
                } else {
                    "Nothing waits on these, and the session can be done while they run."
                },
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("list-lead"),
            )
            tasks.forEach { task -> WorkRow(task, now) }
        }
    }
}

@Composable
private fun WorkRow(task: BackgroundTask, now: Long) {
    val agent = task.type == "agent"
    val tone = if (agent) OrreryTokens.Role.live else OrreryTokens.State.idle
    val lead = if (agent) task.subagentType.ifEmpty { "agent" } else ""
    val activity = listOf(lead, task.activity).filter { it.isNotEmpty() }.joinToString(" · ")
    Column(
        Modifier
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.eight)
            .testTag("list-row:${task.id}")
            .semantics(mergeDescendants = true) {},
    ) {
        Text(
            text = buildAnnotatedString {
                withStyle(SpanStyle(color = tone)) { append(task.description) }
            },
            style = OrreryTokens.Type.small,
        )
        Text(text = activity, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
        Text(text = clock(now - task.started), style = OrreryTokens.Type.small, color = OrreryTokens.Ink.four)
    }
}

/** Elapsed seconds as m:ss. */
internal fun clock(seconds: Long): String {
    val total = seconds.coerceAtLeast(0L)
    return "%d:%02d".format(total / 60, total % 60)
}
