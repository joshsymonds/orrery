package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicTurnRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.time.Instant
import java.time.ZoneId

// SHOWN_FIRST is how many of the newest history entries show before the +n button opens the rest.
private const val SHOWN_FIRST = 3

/**
 * The newest history entries, newest first: the first three, then a "+n earlier" button that opens the rest in place.
 * A step entry whose step is in [steps] opens that step when tapped, and shows the output tails of its failed checks.
 */
@Composable
fun JustHappened(entries: List<EpicHistoryRow>, steps: List<StepRow>, onOpenStep: (StepRow) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val newest = entries.asReversed()
    val shown = if (open) newest else newest.take(SHOWN_FIRST)
    val earlier = newest.size - shown.size
    Column(modifier = Modifier.fillMaxWidth()) {
        HappenHeading("Just happened")
        shown.forEachIndexed { index, entry -> HappenedRow(index, entry, steps, onOpenStep) }
        if (earlier > 0) {
            TextButton(onClick = { open = true }, modifier = Modifier.testTag("happened-more")) {
                Text("+$earlier earlier")
            }
        }
    }
}

@Composable
private fun HappenHeading(title: String) {
    Text(
        title.inLabelCase(),
        style = OrreryTokens.Type.label,
        modifier = Modifier.padding(
            start = OrreryTokens.Space.sixteen,
            top = OrreryTokens.Space.twelve,
            end = OrreryTokens.Space.sixteen,
            bottom = OrreryTokens.Space.four,
        ),
    )
}

@Composable
private fun HappenedRow(index: Int, entry: EpicHistoryRow, steps: List<StepRow>, onOpenStep: (StepRow) -> Unit) {
    val step = entry.step?.let { own -> steps.find { it.id == own.id } }
    val onTap = step?.let { found -> { onOpenStep(found) } }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("happened:$index")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .semantics(mergeDescendants = true) {}
            .tapping(onTap),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve)) {
            Text(clockOf(entry.time), style = OrreryTokens.Type.small)
            Text(whatOf(entry), modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
        }
        step?.checks?.filter(::tailed)?.forEach { check ->
            CheckTails(tag = "happened-tail:$index:${check.name}", check = check)
        }
    }
}

// tailed is whether a check failed with output to show. A passed check shows none, whatever it carries.
private fun tailed(check: StepCheckRow): Boolean =
    !check.ok && (check.stdout.isNotEmpty() || check.stderr.isNotEmpty())

/** The output tails of a failed check, stdout then stderr, each under its label and shown as given. */
@Composable
private fun CheckTails(tag: String, check: StepCheckRow) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag(tag)
            .padding(start = OrreryTokens.Space.sixteen, bottom = OrreryTokens.Space.four),
    ) {
        if (check.stdout.isNotEmpty()) {
            Text("stdout".inLabelCase(), style = OrreryTokens.Type.label)
            Text(check.stdout, style = OrreryTokens.Type.small)
        }
        if (check.stderr.isNotEmpty()) {
            Text("stderr".inLabelCase(), style = OrreryTokens.Type.label)
            Text(check.stderr, style = OrreryTokens.Type.small, color = OrreryTokens.Role.halt)
        }
    }
}

// tapping makes a row clickable when it has a tap, and leaves it without a click action when it has none.
private fun Modifier.tapping(onTap: (() -> Unit)?): Modifier = if (onTap == null) this else clickable(onClick = onTap)

// clockOf is an entry's time as the phone's clock shows it.
private fun clockOf(time: Long): String =
    if (time <= 0) "" else Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)

// whatOf is an entry's text: its kind's label and its detail.
private fun whatOf(entry: EpicHistoryRow): String = when (entry.kind) {
    "turn" -> entry.turn?.let(::turnText).orEmpty()
    "trigger" -> entry.trigger?.let { "trigger · ${it.cause}" }.orEmpty()
    "step" -> entry.step?.let(::stepText).orEmpty()
    "evidence" -> entry.evidence?.let(::evidenceText).orEmpty()
    "decision" -> entry.decision?.let { "decision by ${makerWord(it.by, it.session)} · ${it.text}" }.orEmpty()
    "tell" -> entry.tell?.text.orEmpty()
    else -> ""
}

// makerWord names who made a decision or escalated a question: you, orreryd, or the session of the conversation that
// did. A maker with none on record reads unknown.
internal fun makerWord(by: String, session: String): String = when (by) {
    "owner" -> "you"
    "orreryd" -> "orreryd"
    "conversation" -> "session $session"
    else -> "unknown"
}

private fun turnText(turn: EpicTurnRow): String {
    val head = "turn ${turn.seq} · ${turn.state}"
    return if (turn.outcome.isEmpty()) head else "$head · ${turn.outcome}"
}

private fun stepText(step: StepRow): String = "${step.task.ifEmpty { step.agent }} · ${step.end.ifEmpty { "started" }}"

private fun evidenceText(run: EvidenceRunRow): String = "${run.item} ${run.phase} run · ${runResultOf(run)}"

// runResultOf is an evidence run's result: running until it has ended, then passed or failed.
private fun runResultOf(run: EvidenceRunRow): String = when {
    run.state != "ended" -> "running"
    run.passed -> "passed"
    else -> "failed"
}
