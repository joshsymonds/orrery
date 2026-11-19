package io.github.joshsymonds.orrery.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.fadeIn
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.unit.Dp
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.Move
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import io.github.joshsymonds.orrery.ui.theme.spec

/** Most running step rows one epic shows; the rest fold into one "more" line. */
private const val MAX_STEP_ROWS = 4

/** The side of a live epic's small orrery on its line. */
private val MINI_SIZE: Dp = OrreryTokens.Space.thirtyTwo + OrreryTokens.Space.eight

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
    val animated = animationsOn()
    if (epics.isEmpty()) return
    val live = epics.filter { it.live && !it.waiting }
    val waiting = epics.filter { it.live && it.waiting }
    val folded = epics.filter { !it.live && !it.isEnded() }
    val justEnded = epics.filter { it.isEnded() && it.seen < it.phaseAt }.sortedByDescending { it.phaseAt }
    val endedEarlier = epics.filter { it.isEnded() && it.seen >= it.phaseAt }.sortedByDescending { it.phaseAt }
    Column(modifier = modifier.fillMaxWidth().testTag("epics-group").padding(vertical = OrreryTokens.Space.four)) {
        Text(
            "Epics".inLabelCase(),
            style = OrreryTokens.Type.label,
            modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        )
        for (epic in live) {
            LiveEpic(epic, details[epic.id], now, onOpenEpic)
        }
        if (waiting.isNotEmpty()) {
            FoldLine("${waiting.size} accepted, waiting to start", "epics-waiting") { waitingOpen = !waitingOpen }
            if (waitingOpen) {
                for (epic in waiting) {
                    EpicLine(epic, onOpenEpic, start = OrreryTokens.Space.twentyFour)
                }
            }
        }
        if (folded.isNotEmpty()) {
            FoldLine("${folded.size} folded", "epics-folded") { foldedOpen = !foldedOpen }
            if (foldedOpen) {
                for (epic in folded) {
                    EpicLine(epic, onOpenEpic, start = OrreryTokens.Space.twentyFour)
                }
            }
        }
        if (justEnded.isNotEmpty() || endedEarlier.isNotEmpty()) {
            Text(
                "Just ended".inLabelCase(),
                style = OrreryTokens.Type.label,
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
            )
            for (epic in justEnded) {
                key(epic.id) {
                    EnteringRow(animated) {
                        EpicLine(epic, onOpenEpic, start = OrreryTokens.Space.sixteen, words = endedWordsOf(epic, now))
                    }
                }
            }
            if (endedEarlier.isNotEmpty()) {
                FoldLine("${endedEarlier.size} ended earlier ›", "epics-ended-earlier") { endedOpen = !endedOpen }
                if (endedOpen) {
                    for (epic in endedEarlier) {
                        EpicLine(epic, onOpenEpic, start = OrreryTokens.Space.twentyFour, words = endedWordsOf(epic, now))
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
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
    ) {
        Text(text, style = OrreryTokens.Type.small)
    }
}

@Composable
private fun LiveEpic(epic: EpicRow, detail: EpicDetail?, now: Long, onOpenEpic: (String) -> Unit) {
    val shown = epic.runningSteps.take(MAX_STEP_ROWS)
    val more = epic.runningSteps.size - shown.size
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)) {
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
                    .padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
            ) {
                Text("$more more", style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
            }
        }
        if (epic.endedSteps > 0) {
            Text(
                "${epic.endedSteps} ended",
                style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                modifier = Modifier.padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four),
            )
        }
    }
}

// A live epic's line: its small orrery when it has detail, then its slug, repository and count, then its state and its
// blocker. Without detail the count reads "epic unavailable" and there is no orrery.
@Composable
private fun LiveEpicLine(epic: EpicRow, detail: EpicDetail?, now: Long, onOpenEpic: (String) -> Unit) {
    val words = epicLineOf(detail, now)
    val animated = animationsOn()
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic:${epic.id}")
            .clickable { onOpenEpic(epic.id) }
            .padding(top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (detail != null) {
            val wedges = remember(detail) { wedgesOf(detail) }
            val arcs = remember(detail) { requirementArcs(wedges, detail.requirements) }
            MiniOrrery(wedges, arcs, Modifier.size(MINI_SIZE).testTag("mini-orrery:${epic.id}"))
        }
        Column(modifier = Modifier.weight(1f)) {
            // Its words wrap whole onto the next line when the width runs out, rather than squeezing the last ones.
            FlowRow(horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight), itemVerticalAlignment = Alignment.CenterVertically) {
                Text(epic.slug, style = OrreryTokens.Type.body)
                Text(epic.repository, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
                Text(words.count, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
            }
            ChangingPhrase(words.state, animated) { shown ->
                if (shown.isNotEmpty()) {
                    Text(shown, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
                }
            }
            ChangingPhrase(words.blocker, animated) { shown ->
                if (shown.isNotEmpty()) {
                    Text(
                        AnnotatedString(shown, SpanStyle(color = OrreryTokens.Role.attention)),
                        style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                        modifier = Modifier.testTag("blocker:${epic.id}"),
                    )
                }
            }
        }
    }
}

// A phrase that changes crossfades at Move.STATE. With animations off it changes at once: a snap still waits for a
// frame to finish, so the phrase is drawn directly, as PhaseOutputScreen draws its body.
@Composable
private fun ChangingPhrase(phrase: String, animated: Boolean, content: @Composable (String) -> Unit) {
    if (animated) {
        Crossfade(targetState = phrase, animationSpec = Move.STATE.spec(animated = true)) { shown -> content(shown) }
    } else {
        content(phrase)
    }
}

// A row entering a list fades in at Move.LAYOUT, as the web's entering rows do on mount. Its spec is gated by
// animationsOn(), so animator scale 0 snaps it.
private fun layoutEnterOf(animated: Boolean): EnterTransition = fadeIn(Move.LAYOUT.spec(animated))

// The enter transition an entering row runs, on the row's wrapper, so a test reads which transition the row uses.
internal val EnterSemantics = SemanticsPropertyKey<EnterTransition>("EnterTransition")

// A row composed for the first time enters: it starts hidden and its enter runs on the first frame, through
// [layoutEnterOf]. Callers key the row by its epic or need, so a row that moves within a list keeps its own transition.
@Composable
internal fun EnteringRow(animated: Boolean, content: @Composable () -> Unit) {
    val visibleState = remember { MutableTransitionState(false).apply { targetState = true } }
    val enter = layoutEnterOf(animated)
    AnimatedVisibility(
        visibleState = visibleState,
        modifier = Modifier.semantics { set(EnterSemantics, enter) },
        enter = enter,
    ) { content() }
}

@Composable
private fun EpicLine(epic: EpicRow, onOpenEpic: (String) -> Unit, start: Dp, words: String = "") {
    // Its words wrap whole onto the next line when the width runs out, rather than squeezing the last ones.
    FlowRow(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("epic:${epic.id}")
            .clickable { onOpenEpic(epic.id) }
            .padding(start = start, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        itemVerticalAlignment = Alignment.CenterVertically,
    ) {
        Text(epic.slug, style = OrreryTokens.Type.body)
        Text(epic.repository, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
        if (words.isNotEmpty()) {
            Text(words, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
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
            .padding(start = OrreryTokens.Space.twentyFour, top = OrreryTokens.Space.four, bottom = OrreryTokens.Space.four),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.Top,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(step.agent, style = OrreryTokens.Type.small)
            Text(step.task, style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold))
            Text(step.doing, style = OrreryTokens.Type.small)
        }
        if (step.held) {
            Text(
                "held",
                style = OrreryTokens.Type.small,
                modifier = Modifier.testTag("held:${step.id}"),
            )
        }
    }
}
