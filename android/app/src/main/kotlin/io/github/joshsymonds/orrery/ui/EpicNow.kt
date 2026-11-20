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
 * The top of Now: its one sentence, then what asks, placed first under it. An open question draws its Ask; an owner's
 * park draws its reason with Retry and Stop…; the planner's park draws one grey line with no control; a limit wait
 * draws Resume now. [onStop] only opens Stop's confirm; the screen makes the call after the confirm.
 */
@Composable
fun NowAsk(
    detail: EpicDetail,
    onAnswer: (questionId: String, text: String) -> String,
    onRetry: () -> Unit,
    onStop: () -> Unit,
    onResume: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight)) {
        Text(nowSentenceOf(detail), modifier = Modifier.testTag("now-sentence"))
        when (val asking = askingOf(detail)) {
            is Asking.Question -> key(asking.question.id) {
                AnswerForm(asking.question, onAnswer = onAnswer)
            }
            is Asking.Park -> Column(
                modifier = Modifier.fillMaxWidth().testTag("epic-park"),
                verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.four),
            ) {
                Text("It asks you what to do")
                Text(asking.reason, modifier = Modifier.testTag("epic-park-reason"))
                TextButton(onClick = onRetry, modifier = Modifier.testTag("epic-park-retry")) {
                    Text("Retry")
                }
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
