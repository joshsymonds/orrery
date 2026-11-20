package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Button
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

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
