package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * The top of Now: its one sentence, then what asks, placed first under it. An open question draws its Ask, to pick a
 * choice and confirm it; an owner's park draws its Ask with the one answer that runs the parked work again, then Stop…;
 * the planner's park draws one grey line with no control; a limit wait draws Resume now. [onAnswer] receives the
 * question's id, the chosen choice's id or "other", and the text sent with it; the caller makes the call off the main
 * thread. [onStop] only opens Stop's confirm; the screen makes the call after the confirm.
 */
@Composable
fun NowAsk(
    detail: EpicDetail,
    now: Long,
    onAnswer: (questionId: String, choice: String, text: String) -> Unit,
    onRetry: () -> Unit,
    onStop: () -> Unit,
    onResume: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        Text(nowSentenceOf(detail), modifier = Modifier.testTag("now-sentence"))
        when (val asking = askingOf(detail)) {
            is Asking.Question -> key(asking.question.id) {
                Ask(
                    model = epicAsk(detail.epic, asking.question),
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
                        onAnswer(asking.question.id, choice, text)
                        ""
                    },
                )
            }
            is Asking.Park -> Column(
                modifier = Modifier.fillMaxWidth().testTag("epic-park"),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                Ask(
                    model = parkAsk(detail.epic, asking.reason),
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
                TextButton(onClick = onStop, modifier = Modifier.testTag("epic-park-stop")) {
                    Text("Stop…")
                }
            }
            is Asking.Planner -> Text(
                asking.line,
                color = OrreryTokens.Ink.three,
                modifier = Modifier.testTag("epic-planner-park"),
            )
            Asking.Limited -> Column(verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four)) {
                Button(onClick = onResume, modifier = Modifier.testTag("epic-resume-now")) {
                    Text("Resume now")
                }
                Text(
                    "Try before the reset. A step still over the limit stays held.",
                    color = OrreryTokens.Ink.three,
                )
            }
            null -> Unit
        }
    }
}
