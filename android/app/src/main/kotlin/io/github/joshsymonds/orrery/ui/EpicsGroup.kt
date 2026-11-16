package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.Mocha

/** Most running step rows one epic shows; the rest fold into one "more" line. */
private const val MAX_STEP_ROWS = 4

/** The side of a live epic's small orrery on its line. */
private val MINI_SIZE: Dp = 40.dp

/**
 * Home's Epics group. Each live epic's line shows its small orrery when it has [details], its slug, its
 * repository, and its count, state and blocker from [epicLineOf] at [now]; the blocker is peach and left out when
 * it is empty. Under the line, up to [MAX_STEP_ROWS] of its running and held steps (a held one marked), and a
 * "more" line for the rest; its ended steps show as one count. Live epics waiting to start fold into one line,
 * "n accepted, waiting to start", that opens to list them. Folded epics fold into one line that opens to list
 * them. An ended epic (an orreryd epic that is not live) that is unseen draws under "Just ended" after the folded line,
 * newest ending first, with its outcome and the time since it ended; the seen ones fold into one "n ended earlier ›"
 * line that opens them in place. Tapping an epic, a step or a "more" line opens the epic. Nothing here answers a step:
 * the group only opens epics. No epics draws nothing.
 */
@Composable
fun EpicsGroup(
    epics: List<EpicRow>,
    onOpenEpic: (String) -> Unit,
    modifier: Modifier = Modifier,
    details: Map<String, EpicDetail> = emptyMap(),
    now: Long = 0L,
) {
    var waitingOpen by remember { mutableStateOf(false) }
    var foldedOpen by remember { mutableStateOf(false) }
    var endedOpen by remember { mutableStateOf(false) }
    if (epics.isEmpty()) return
    val live = epics.filter { it.live && !it.waiting }
    val waiting = epics.filter { it.live && it.waiting }
    val folded = epics.filter { !it.live && !it.isEnded() }
    val justEnded = epics.filter { it.isEnded() && it.seen < it.phaseAt }.sortedByDescending { it.phaseAt }
    val endedEarlier = epics.filter { it.isEnded() && it.seen >= it.phaseAt }.sortedByDescending { it.phaseAt }
    Column(modifier = modifier.fillMaxWidth().testTag("epics-group").padding(vertical = 4.dp)) {
        Text(
            "Epics",
            style = MaterialTheme.typography.titleSmall,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )
        for (epic in live) {
            LiveEpic(epic, details[epic.id], now, onOpenEpic)
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
        if (justEnded.isNotEmpty() || endedEarlier.isNotEmpty()) {
            Text(
                "Just ended",
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
            )
            for (epic in justEnded) {
                EpicLine(epic, onOpenEpic, start = 16.dp, words = endedWordsOf(epic, now))
            }
            if (endedEarlier.isNotEmpty()) {
                FoldLine("${endedEarlier.size} ended earlier ›", "epics-ended-earlier") { endedOpen = !endedOpen }
                if (endedOpen) {
                    for (epic in endedEarlier) {
                        EpicLine(epic, onOpenEpic, start = 24.dp, words = endedWordsOf(epic, now))
                    }
                }
            }
        }
    }
}

// An orreryd epic that is not live has ended. A record epic that went quiet is not ended: it stays folded.
private fun EpicRow.isEnded(): Boolean = source == "orreryd" && !live

// An ended epic's words: its outcome, then the time since it ended, never below zero when the clock is behind its stamp.
// At phaseAt 0 there is no time to read, so the outcome stands alone.
private fun endedWordsOf(epic: EpicRow, now: Long): String =
    if (epic.phaseAt > 0L) "${epic.outcome} · ${durationText((now - epic.phaseAt).coerceAtLeast(0L))} ago" else epic.outcome

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
private fun LiveEpic(epic: EpicRow, detail: EpicDetail?, now: Long, onOpenEpic: (String) -> Unit) {
    val shown = epic.runningSteps.take(MAX_STEP_ROWS)
    val more = epic.runningSteps.size - shown.size
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
        LiveEpicLine(epic, detail, now, onOpenEpic)
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

// A live epic's line: its small orrery when it has detail, then its slug, repository and count, then its state and its
// blocker. Without detail the count reads "epic unavailable" and there is no orrery.
@Composable
private fun LiveEpicLine(epic: EpicRow, detail: EpicDetail?, now: Long, onOpenEpic: (String) -> Unit) {
    val words = epicLineOf(detail, now)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic:${epic.id}")
            .clickable { onOpenEpic(epic.id) }
            .padding(top = 4.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (detail != null) {
            val wedges = remember(detail) { wedgesOf(detail) }
            val arcs = remember(detail) { requirementArcs(wedges, detail.requirements) }
            MiniOrrery(wedges, arcs, Modifier.size(MINI_SIZE).testTag("mini-orrery:${epic.id}"))
        }
        Column(modifier = Modifier.weight(1f)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(epic.slug, style = MaterialTheme.typography.bodyLarge)
                Text(epic.repository, style = MaterialTheme.typography.labelSmall)
                Text(words.count, style = MaterialTheme.typography.labelSmall)
            }
            if (words.state.isNotEmpty()) {
                Text(words.state, style = MaterialTheme.typography.labelSmall)
            }
            if (words.blocker.isNotEmpty()) {
                Text(
                    AnnotatedString(words.blocker, SpanStyle(color = Mocha.Peach)),
                    style = MaterialTheme.typography.labelSmall,
                    modifier = Modifier.testTag("blocker:${epic.id}"),
                )
            }
        }
    }
}

@Composable
private fun EpicLine(epic: EpicRow, onOpenEpic: (String) -> Unit, start: Dp, words: String = "") {
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
        if (words.isNotEmpty()) {
            Text(words, style = MaterialTheme.typography.labelSmall)
        }
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
