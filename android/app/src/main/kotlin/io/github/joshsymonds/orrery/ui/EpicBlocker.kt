package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicQuestionRow
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
 * The now sentence over the epic's blocker. An open question draws its text and an answer form, a park draws its text
 * and a Retry, and any blocker has a Stop that asks first. [onAnswer] returns "" when the answer is recorded, else the
 * error to show under the form; the caller makes the call off the main thread.
 */
@Composable
fun NowBlock(
    sentence: String,
    blocker: EpicBlocker?,
    slug: String,
    onAnswer: (questionId: String, text: String) -> String,
    onRetry: () -> Unit,
    onStop: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        if (sentence.isNotEmpty()) {
            Text(sentence, modifier = Modifier.testTag("now-sentence"))
        }
        when (blocker) {
            is EpicBlocker.Question -> key(blocker.question.id) {
                AnswerForm(blocker.question, onAnswer = onAnswer)
            }
            is EpicBlocker.Parked -> {
                Text(blocker.text, color = OrreryTokens.Role.bad)
                TextButton(onClick = onRetry, modifier = Modifier.testTag("blocker-retry")) {
                    Text("Retry")
                }
            }
            is EpicBlocker.Held, null -> Unit
        }
        if (blocker != null) {
            AskFirst(
                label = "Stop…",
                question = "Stop $slug?",
                confirm = "Stop $slug",
                keep = "Keep it running",
                tag = "blocker-stop",
                onConfirm = onStop,
            )
        }
    }
}

// AnswerForm answers one open question. Answer stays disabled while the trimmed answer is empty and sends the trimmed
// answer. A returned error shows under the form and keeps the answer; an empty return clears it. hideQuestion leaves
// out the question's own line, for a caller whose sentence already says the question.
@Composable
internal fun AnswerForm(
    question: EpicQuestionRow,
    hideQuestion: Boolean = false,
    onAnswer: (questionId: String, text: String) -> String,
) {
    var text by remember { mutableStateOf("") }
    var error by remember { mutableStateOf("") }
    if (!hideQuestion) {
        Text(question.text, modifier = Modifier.testTag("blocker-question"))
    }
    OutlinedTextField(
        value = text,
        onValueChange = { text = it },
        modifier = Modifier.fillMaxWidth().testTag("blocker-answer"),
    )
    Button(
        onClick = {
            error = onAnswer(question.id, text.trim())
            if (error.isEmpty()) {
                text = ""
            }
        },
        enabled = text.trim().isNotEmpty(),
        modifier = Modifier.testTag("blocker-answer-send"),
    ) {
        Text("Answer")
    }
    if (error.isNotEmpty()) {
        Text(error, color = OrreryTokens.Role.bad, modifier = Modifier.testTag("blocker-error"))
    }
}
