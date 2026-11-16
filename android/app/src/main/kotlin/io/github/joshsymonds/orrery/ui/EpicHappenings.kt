package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicTurnRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.time.Instant
import java.time.ZoneId

// SHOWN_FIRST is how many of the newest history entries show before the +n button opens the rest.
private const val SHOWN_FIRST = 3

/**
 * The tasks in flight: each wedge in a phase that runs or is held by the owner, with its phase and how long it has been
 * there, as of [now]. A tap calls [onOpenPhase] with the row's task and the phase it is in; without it rows take no tap.
 */
@Composable
fun NowRows(wedges: List<Wedge>, now: Long, onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null) {
    val rows = wedges.filter(::inFlight).mapNotNull { wedge -> nowSliceOf(wedge)?.let { wedge to it } }
    Column(modifier = Modifier.fillMaxWidth()) {
        HappenHeading("Now")
        rows.forEachIndexed { index, (wedge, slice) ->
            val onTap = onOpenPhase?.let { open -> { open(wedge.task, slice.phase) } }
            Text(
                AnnotatedString(nowText(wedge, slice.since, now), SpanStyle(color = toneColourOf(wedge))),
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("now-row:$index")
                    .padding(horizontal = 16.dp, vertical = 4.dp)
                    .tapping(onTap),
            )
        }
    }
}

/**
 * The tasks not started: each planned wedge, with its title. A tap calls [onOpenPhase] with the row's task and the
 * implementing phase; without it rows take no tap.
 */
@Composable
fun NextRows(wedges: List<Wedge>, onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null) {
    val planned = wedges.filter { it.place == WedgePlace.PLANNED }
    Column(modifier = Modifier.fillMaxWidth()) {
        HappenHeading("Next")
        planned.forEachIndexed { index, wedge ->
            val onTap = onOpenPhase?.let { open -> { open(wedge.task, Phase.IMPLEMENTING) } }
            Text(
                "${wedge.task} · ${wedge.title}",
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("next-row:$index")
                    .padding(horizontal = 16.dp, vertical = 4.dp)
                    .tapping(onTap),
            )
        }
    }
}

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
        title,
        style = MaterialTheme.typography.titleSmall,
        modifier = Modifier.padding(start = 16.dp, top = 12.dp, end = 16.dp, bottom = 4.dp),
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
            .padding(horizontal = 16.dp, vertical = 4.dp)
            .semantics(mergeDescendants = true) {}
            .tapping(onTap),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(clockOf(entry.time), style = MaterialTheme.typography.labelSmall)
            Text(whatOf(entry), modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall)
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
            .padding(start = 16.dp, bottom = 4.dp),
    ) {
        if (check.stdout.isNotEmpty()) {
            Text("stdout", style = MaterialTheme.typography.labelSmall)
            Text(check.stdout, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
        if (check.stderr.isNotEmpty()) {
            Text("stderr", style = MaterialTheme.typography.labelSmall)
            Text(check.stderr, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
        }
    }
}

// inFlight is whether a wedge is in a phase and running, or held by the owner.
private fun inFlight(wedge: Wedge): Boolean =
    (wedge.tone == WedgeTone.RUN || wedge.tone == WedgeTone.MINE) &&
        wedge.place != WedgePlace.PLANNED &&
        wedge.place != WedgePlace.INTEGRATED

// nowSliceOf is the slice of the phase a wedge is in, null when no phase is now.
private fun nowSliceOf(wedge: Wedge): PhaseSlice? = wedge.slices.find { it.state == SliceState.NOW }

// toneColourOf is a now row's colour, as the web's k-now and k-held: yellow while a step runs, peach while the
// owner holds it. inFlight admits no other tone.
private fun toneColourOf(wedge: Wedge): Color = if (wedge.tone == WedgeTone.MINE) Mocha.Peach else Mocha.Yellow

// nowText is a row's text: its task and place, then how long it has been in the phase once the clock has started it.
private fun nowText(wedge: Wedge, since: Long, now: Long): String {
    val head = "${wedge.task} · ${wedge.place.name.lowercase()}"
    return if (since > 0) "$head · ${durationText(now - since)}" else head
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
    "decision" -> entry.decision?.let { "decision · ${it.text}" }.orEmpty()
    "tell" -> entry.tell?.text.orEmpty()
    else -> ""
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
