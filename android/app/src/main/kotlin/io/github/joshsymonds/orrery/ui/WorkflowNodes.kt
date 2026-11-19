package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicNodeCheckRow
import io.github.joshsymonds.orrery.api.EpicNodeRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

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
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four + OrreryTokens.Space.two),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text(node.path, style = OrreryTokens.Type.body)
            Text(node.kind, style = OrreryTokens.Type.small)
            Text(node.state, style = OrreryTokens.Type.small)
            if (node.outcome.isNotEmpty()) {
                Text(node.outcome, style = OrreryTokens.Type.small)
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
            Text("round ${node.round}", style = OrreryTokens.Type.small)
            if (node.loopRound > 0L) {
                Text("loop round ${node.loopRound}", style = OrreryTokens.Type.small)
            }
            if (node.attempt > 0L) {
                Text("attempt ${node.attempt}", style = OrreryTokens.Type.small)
            }
        }
        node.turns.forEach { Text("turn $it", style = OrreryTokens.Type.small) }
        node.tasks.forEach { Text("task $it", style = OrreryTokens.Type.small) }
        if (node.runs.isNotEmpty()) {
            EvidenceRuns(requirement = NODE_RUNS, runs = node.runs, loadArtifact = loadArtifact)
        }
        node.check?.let { NodeCheck(index, it) }
        if (node.step.isNotEmpty()) {
            Text("step ${node.step}", style = OrreryTokens.Type.small)
        }
        if (node.output.isNotEmpty()) {
            Text(node.output, style = OrreryTokens.Type.small)
        }
    }
}

/**
 * A gate's or command's check under node [index]: its state, its exit once it ended having run, whether it was killed
 * at its time limit, why it did not run, and each non-empty output tail under its label.
 */
@Composable
private fun NodeCheck(index: Int, check: EpicNodeCheckRow) {
    Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        Text("check ${check.state}", style = OrreryTokens.Type.small)
        if (check.state == "ended" && check.notRun.isEmpty()) {
            Text("exit ${check.exit}", style = OrreryTokens.Type.small)
        }
        if (check.timedOut) {
            Text("timed out", style = OrreryTokens.Type.small)
        }
    }
    if (check.notRun.isNotEmpty()) {
        Text("not run · ${check.notRun}", style = OrreryTokens.Type.small)
    }
    CheckTail(index, "stdout", check.stdout)
    CheckTail(index, "stderr", check.stderr)
}

/** The [stream] tail of node [index]'s check under its label, when it has one. */
@Composable
private fun CheckTail(index: Int, stream: String, tail: String) {
    if (tail.isEmpty()) return
    Column(modifier = Modifier.fillMaxWidth().testTag("node-check-tail:$index:$stream")) {
        Text(stream.inLabelCase(), style = OrreryTokens.Type.label)
        Text(tail, style = OrreryTokens.Type.small)
    }
}
