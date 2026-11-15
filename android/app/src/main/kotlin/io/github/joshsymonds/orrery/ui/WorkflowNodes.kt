package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicNodeCheckRow
import io.github.joshsymonds.orrery.api.EpicNodeRow

/**
 * The requirement index a node's evidence runs are drawn under. A node's runs belong to no Requirement, so their
 * positions never collide with a Requirement's, whose indexes start at 0.
 */
private const val NODE_RUNS = -1

/**
 * One workflow node run of an epic, at [index] in the order the server lists them. Its head line shows its path, kind
 * and state, its outcome once it has one, its round, and its loop round and attempt when set. Beneath it are the rows its
 * kind holds: a plan's turns and tasks, a build's task, an evidence node's runs, a gate's or command's check, an agent's
 * step id, and any output the run ended or parked with. Every value is the row's; apart from the round, which is always
 * shown, a field the row leaves unset draws nothing.
 */
@Composable
internal fun WorkflowNodeRow(index: Int, node: EpicNodeRow, loadArtifact: ArtifactLoader) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("node-row:$index")
            .padding(horizontal = 16.dp, vertical = 6.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(node.path, style = MaterialTheme.typography.bodyMedium)
            Text(node.kind, style = MaterialTheme.typography.labelMedium)
            Text(node.state, style = MaterialTheme.typography.labelMedium)
            if (node.outcome.isNotEmpty()) {
                Text(node.outcome, style = MaterialTheme.typography.labelMedium)
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("round ${node.round}", style = MaterialTheme.typography.labelSmall)
            if (node.loopRound > 0L) {
                Text("loop round ${node.loopRound}", style = MaterialTheme.typography.labelSmall)
            }
            if (node.attempt > 0L) {
                Text("attempt ${node.attempt}", style = MaterialTheme.typography.labelSmall)
            }
        }
        node.turns.forEach { Text("turn $it", style = MaterialTheme.typography.bodySmall) }
        node.tasks.forEach { Text("task $it", style = MaterialTheme.typography.bodySmall) }
        if (node.runs.isNotEmpty()) {
            EvidenceRuns(requirement = NODE_RUNS, runs = node.runs, loadArtifact = loadArtifact)
        }
        node.check?.let { NodeCheck(index, it) }
        if (node.step.isNotEmpty()) {
            Text("step ${node.step}", style = MaterialTheme.typography.bodySmall)
        }
        if (node.output.isNotEmpty()) {
            Text(node.output, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
    }
}

/**
 * A gate's or command's check under node [index]: its state, its exit once it ended having run, whether it was killed
 * at its time limit, why it did not run, and each non-empty output tail under its label.
 */
@Composable
private fun NodeCheck(index: Int, check: EpicNodeCheckRow) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("check ${check.state}", style = MaterialTheme.typography.labelMedium)
        if (check.state == "ended" && check.notRun.isEmpty()) {
            Text("exit ${check.exit}", style = MaterialTheme.typography.labelMedium)
        }
        if (check.timedOut) {
            Text("timed out", style = MaterialTheme.typography.labelMedium)
        }
    }
    if (check.notRun.isNotEmpty()) {
        Text("not run · ${check.notRun}", style = MaterialTheme.typography.bodySmall)
    }
    CheckTail(index, "stdout", check.stdout)
    CheckTail(index, "stderr", check.stderr)
}

/** The [stream] tail of node [index]'s check under its label, when it has one. */
@Composable
private fun CheckTail(index: Int, stream: String, tail: String) {
    if (tail.isEmpty()) return
    Column(modifier = Modifier.fillMaxWidth().testTag("node-check-tail:$index:$stream")) {
        Text(stream, style = MaterialTheme.typography.labelSmall)
        Text(tail, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
    }
}
