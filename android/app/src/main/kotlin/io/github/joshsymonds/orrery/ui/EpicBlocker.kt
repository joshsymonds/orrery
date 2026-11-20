package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The one live sentence of an epic: the task and phase of its first running wedge, and the summary of that phase's
 * now slice when it has one. Empty when no wedge runs.
 */
fun nowSentence(wedges: List<Wedge>): String {
    val running = wedges.firstOrNull { it.tone == WedgeTone.RUN } ?: return ""
    val summary = running.slices.firstOrNull { it.state == SliceState.NOW }?.summary ?: ""
    val head = "${running.task} · ${running.place.name.lowercase()}"
    return if (summary.isEmpty()) head else "$head · $summary"
}

/**
 * The now sentence over the epic's blocker. An open question draws its Ask, to pick a choice and confirm it, and a park
 * draws its Ask with the one answer that runs the parked work again. Any blocker has a Stop that asks first. [onAnswer]
 * receives the question's id, the chosen choice's id or "other", and the text sent with it; the caller makes the call
 * off the main thread.
 */
@Composable
fun NowBlock(
    sentence: String,
    blocker: EpicBlocker?,
    epic: EpicRow,
    now: Long,
    onAnswer: (questionId: String, choice: String, text: String) -> Unit,
    onRetry: () -> Unit,
    onStop: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        if (sentence.isNotEmpty()) {
            Text(sentence, modifier = Modifier.testTag("now-sentence"))
        }
        when (blocker) {
            is EpicBlocker.Question -> key(blocker.question.id) {
                Ask(
                    model = epicAsk(epic, blocker.question),
                    now = now,
                    phase = AskPhase.OPEN,
                    picked = null,
                    note = "",
                    onAnswer = null,
                    onNotNow = null,
                    onBringBack = null,
                    onDecline = null,
                    onArchive = null,
                    onConfirm = { choice, text ->
                        onAnswer(blocker.question.id, choice, text)
                        ""
                    },
                )
            }
            is EpicBlocker.Parked -> Ask(
                model = parkAsk(epic, blocker.text),
                now = now,
                phase = AskPhase.OPEN,
                picked = null,
                note = "",
                onAnswer = null,
                onNotNow = null,
                onBringBack = null,
                onDecline = null,
                onArchive = null,
                onConfirm = { _, _ ->
                    onRetry()
                    ""
                },
            )
            is EpicBlocker.Held, null -> Unit
        }
        if (blocker != null) {
            AskFirst(
                label = "Stop…",
                question = "Stop ${epic.slug}?",
                confirm = "Stop ${epic.slug}",
                keep = "Keep it running",
                tag = "blocker-stop",
                onConfirm = onStop,
            )
        }
    }
}
