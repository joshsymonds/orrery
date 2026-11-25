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
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
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

// HappenedGlyphKey and HappenedHueKey are the head's icon and the colour it is drawn in, on the icon's node, so a test
// can read them.
internal val HappenedGlyphKey = SemanticsPropertyKey<Glyph>("HappenedGlyph")
internal val HappenedHueKey = SemanticsPropertyKey<Color>("HappenedHue")

/**
 * The newest history entries, newest first, under the section head: the first three, then a "+n earlier" button that
 * opens the rest in place. Each line reads its entry's sentence from the server. A line with detail opens in place on a
 * tap to it. A step's detail has a door to the step's page, and an evidence run's detail has one to its judge step.
 */
@Composable
fun JustHappened(entries: List<EpicHistoryRow>, steps: List<StepRow>, onOpenStep: (StepRow) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val newest = entries.asReversed()
    val shown = if (open) newest else newest.take(SHOWN_FIRST)
    val earlier = newest.size - shown.size
    Column(modifier = Modifier.fillMaxWidth()) {
        HappenHeading()
        shown.forEachIndexed { index, entry ->
            key(entry.cursor) { HappenedRow(index, entry, steps, onOpenStep) }
        }
        if (earlier > 0) {
            TextButton(onClick = { open = true }, modifier = Modifier.testTag("happened-more")) {
                Text("+$earlier earlier")
            }
        }
    }
}

// HappenHeading is the section's head: the retry icon in series one, then the round-6 section head's title.
@Composable
private fun HappenHeading() {
    Row(
        modifier = Modifier
            .testTag("happened-head")
            .padding(
                start = OrreryTokens.Space.sixteen,
                top = OrreryTokens.Space.twelve,
                end = OrreryTokens.Space.sixteen,
                bottom = OrreryTokens.Space.four,
            ),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        GlyphIcon(
            Glyph.RETRY,
            OrreryTokens.Series.one,
            modifier = Modifier.testTag("happened-head:icon").semantics {
                set(HappenedGlyphKey, Glyph.RETRY)
                set(HappenedHueKey, OrreryTokens.Series.one)
            },
        )
        SectionHead("Just happened")
    }
}

@Composable
private fun HappenedRow(index: Int, entry: EpicHistoryRow, steps: List<StepRow>, onOpenStep: (StepRow) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val detailed = hasDetail(entry, steps)
    val onTap: (() -> Unit)? = if (detailed) ({ open = !open }) else null
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("happened:$index")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .semantics(mergeDescendants = true) {
                if (detailed) stateDescription = if (open) "expanded" else "collapsed"
            }
            .tapping(onTap),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.twelve)) {
            Text(
                clockOf(entry.time),
                modifier = Modifier.testTag("happened-at:$index"),
                style = OrreryTokens.Type.code,
            )
            Text(entry.sentence, modifier = Modifier.weight(1f), style = OrreryTokens.Type.small)
        }
        if (open && detailed) {
            HappenedDetail(index, entry, steps, onOpenStep)
        }
    }
}

// hasDetail is whether a line opens to detail: a turn with an outline or decisions, a step the epic holds, or an
// evidence line with runs. Any other line has none and does not open.
private fun hasDetail(entry: EpicHistoryRow, steps: List<StepRow>): Boolean = when (entry.kind) {
    "turn" -> entry.turn?.let { it.outline.isNotEmpty() || it.decisions.isNotEmpty() } ?: false
    "step" -> stepOf(entry, steps) != null
    "evidence" -> entry.runs.isNotEmpty()
    else -> false
}

// stepOf is the step a step line names, when the epic holds it.
private fun stepOf(entry: EpicHistoryRow, steps: List<StepRow>): StepRow? =
    entry.step?.let { own -> steps.find { it.id == own.id } }

// judgeStepOf is the step an evidence run was judged in, when the run names one and the epic holds it.
private fun judgeStepOf(run: EvidenceRunRow, steps: List<StepRow>): StepRow? =
    if (run.judgeStep.isEmpty()) null else steps.find { it.id == run.judgeStep }

// HappenedDetail is a line's detail, under the line: a turn's outline and decisions, a step's agent, checks and its
// door, and each run an evidence line groups, with its results, its judge and the door to its judge step.
@Composable
private fun HappenedDetail(index: Int, entry: EpicHistoryRow, steps: List<StepRow>, onOpenStep: (StepRow) -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("happened-detail:$index")
            .padding(start = OrreryTokens.Space.sixteen, bottom = OrreryTokens.Space.four),
    ) {
        entry.turn?.let { TurnDetail(index, it) }
        stepOf(entry, steps)?.let { StepDetail(index, it, onOpenStep) }
        entry.runs.forEachIndexed { runIndex, run -> RunDetail(index, runIndex, run, steps, onOpenStep) }
    }
}

// TurnDetail is a turn's outline, then each decision the turn recorded, in its order.
@Composable
private fun TurnDetail(index: Int, turn: EpicTurnRow) {
    if (turn.outline.isNotEmpty()) {
        Text(turn.outline, modifier = Modifier.testTag("happened-outline:$index"), style = OrreryTokens.Type.small)
    }
    turn.decisions.forEachIndexed { decisionIndex, decision ->
        Text(
            decision,
            modifier = Modifier.testTag("happened-decision:$index:$decisionIndex"),
            style = OrreryTokens.Type.small,
        )
    }
}

// StepDetail is a step's agent, when it has one, then its checks, then the door to the step's page. The agent shows
// only here: the collapsed line never names it.
@Composable
private fun StepDetail(index: Int, step: StepRow, onOpenStep: (StepRow) -> Unit) {
    if (step.agent.isNotEmpty()) {
        Text(
            "agent: ${step.agent}",
            modifier = Modifier.testTag("happened-agent:$index"),
            style = OrreryTokens.Type.small,
        )
    }
    step.checks.forEach { check -> CheckDetail(index, check) }
    TextButton(onClick = { onOpenStep(step) }, modifier = Modifier.testTag("happened-open:$index")) {
        Text("Open the step")
    }
}

// CheckDetail is one check a step ran: its round, name and result, its detail, and the output tails of a failed check.
@Composable
private fun CheckDetail(index: Int, check: StepCheckRow) {
    Column(modifier = Modifier.testTag("happened-check:$index:${check.name}")) {
        val result = if (check.ok) "passed" else "failed"
        Text("round ${check.round} · ${check.name} · $result", style = OrreryTokens.Type.small)
        if (check.detail.isNotEmpty()) {
            Text(check.detail, style = OrreryTokens.Type.small)
        }
        if (tailed(check)) {
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

// RunDetail is one run an evidence line groups: its results, its judge's verdict and reason, and the door to its judge
// step when the run has one the epic holds.
@Composable
private fun RunDetail(
    index: Int,
    runIndex: Int,
    run: EvidenceRunRow,
    steps: List<StepRow>,
    onOpenStep: (StepRow) -> Unit,
) {
    Column(modifier = Modifier.testTag("happened-run:$index:$runIndex")) {
        Text(runResultsOf(run), style = OrreryTokens.Type.small)
        Text(judgeOf(run), style = OrreryTokens.Type.small)
        judgeStepOf(run, steps)?.let { judge ->
            TextButton(
                onClick = { onOpenStep(judge) },
                modifier = Modifier.testTag("happened-judge:$index:$runIndex"),
            ) {
                Text("Open the judge step")
            }
        }
    }
}

// runResultsOf is an evidence run's results: its state while it has not ended, else whether it passed, its exit, and
// whether it timed out or was not captured.
private fun runResultsOf(run: EvidenceRunRow): String {
    if (run.state != "ended") {
        return run.state
    }
    val parts = mutableListOf(if (run.passed) "passed" else "failed", "exit ${run.exit}")
    if (run.timedOut) {
        parts.add("timed out")
    }
    if (run.notCaptured.isNotEmpty()) {
        parts.add("not captured: ${run.notCaptured}")
    }
    return parts.joinToString(" · ")
}

// judgeOf is an evidence run's judge: its verdict, or none, and the reason it gave when it gave one.
private fun judgeOf(run: EvidenceRunRow): String {
    val verdict = run.verdict.ifEmpty { "no verdict" }
    return if (run.judgeReason.isEmpty()) "judge: $verdict" else "judge: $verdict · ${run.judgeReason}"
}

// tapping makes a row clickable when it has a tap, and leaves it without a click action when it has none.
private fun Modifier.tapping(onTap: (() -> Unit)?): Modifier = if (onTap == null) this else clickable(onClick = onTap)

// clockOf is an entry's time as the phone's clock shows it.
private fun clockOf(time: Long): String =
    if (time <= 0) "" else Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)

// makerWord names who made a decision or escalated a question: you, orreryd, or the session of the conversation that
// did. A maker with none on record reads unknown.
internal fun makerWord(by: String, session: String): String = when (by) {
    "owner" -> "you"
    "orreryd" -> "orreryd"
    "conversation" -> "session $session"
    else -> "unknown"
}
