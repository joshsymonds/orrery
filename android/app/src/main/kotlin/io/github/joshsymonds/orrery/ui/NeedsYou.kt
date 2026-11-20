package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.animationsOn
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

/**
 * Home's Needs you: a "Needs you" heading, then each waiting pane's sentence with its summary as the why under it, then
 * each epic need with its control. A tap on a pane's sentence calls [onOpenPane] with that pane, and a tap on an epic need's sentence calls [onOpenEpic]
 * with its epic. A question need draws the answer form for its question, a park draws Retry, and a release need draws
 * Retry and Stop…, which asks first. Any other epic need draws its sentence alone. [onAnswer], [onRetry] and [onStop]
 * take the need's epic id; [onAnswer] returns "" when the answer is recorded, else the error to show under its form.
 * No needs draws nothing.
 */
@Composable
fun NeedsYou(
    waiting: List<Pane>,
    epics: List<EpicRow>,
    details: Map<String, EpicDetail>,
    onOpenPane: (Pane) -> Unit,
    onOpenEpic: (String) -> Unit,
    onAnswer: (epic: String, questionId: String, text: String) -> String,
    onRetry: (epic: String) -> Unit,
    onStop: (epic: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val paneNeeds = remember(waiting) { waiting.map { pane -> pane to paneNeedOf(pane) } }
    val epicNeeds = remember(epics, details) {
        epics.flatMap { epic -> epicNeedsOf(listOf(epic), details).map { need -> epic to need } }
    }
    val animated = animationsOn()
    if (paneNeeds.isEmpty() && epicNeeds.isEmpty()) return
    Column(modifier = modifier.fillMaxWidth().testTag("needs-you").padding(vertical = OrreryTokens.Space.four)) {
        Text(
            "Needs you".inLabelCase(),
            style = OrreryTokens.Type.label,
            modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        )
        for ((pane, need) in paneNeeds) {
            key(pane.id) {
                EnteringRow(animated) {
                    Column(modifier = Modifier.fillMaxWidth()) {
                        Text(
                            need.text,
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
                                .testTag("need-sentence")
                                .clickable { onOpenPane(pane) },
                        )
                        if (need.summary.isNotEmpty()) {
                            Text(
                                need.summary,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(horizontal = OrreryTokens.Space.sixteen)
                                    .testTag("need-why"),
                            )
                        }
                    }
                }
            }
        }
        for ((epic, need) in epicNeeds) {
            key(epic.id, need.kind, need.question) {
                EnteringRow(animated) {
                    EpicNeedEntry(
                        epic = epic,
                        need = need,
                        onOpenEpic = onOpenEpic,
                        onAnswer = onAnswer,
                        onRetry = onRetry,
                        onStop = onStop,
                    )
                }
            }
        }
    }
}

// EpicNeedEntry draws one epic need: its sentence, which opens the epic on a tap, then its control. A question draws the
// answer form for its question, without the question's line, since the sentence already says it.
@Composable
private fun EpicNeedEntry(
    epic: EpicRow,
    need: Need,
    onOpenEpic: (String) -> Unit,
    onAnswer: (epic: String, questionId: String, text: String) -> String,
    onRetry: (epic: String) -> Unit,
    onStop: (epic: String) -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen),
        verticalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            need.text,
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = OrreryTokens.Space.four)
                .testTag("need-sentence")
                .clickable { onOpenEpic(epic.id) },
        )
        when (need.kind) {
            NeedKind.QUESTION -> AnswerForm(
                question = EpicQuestionRow(id = need.question, text = "", turn = 0L, asked = 0L),
                hideQuestion = true,
                onAnswer = { questionId, text -> onAnswer(epic.id, questionId, text) },
            )
            NeedKind.PARK -> NeedRetry { onRetry(epic.id) }
            NeedKind.RELEASE -> {
                NeedRetry { onRetry(epic.id) }
                AskFirst(
                    label = "Stop…",
                    question = "Stop ${epic.slug}?",
                    confirm = "Stop ${epic.slug}",
                    keep = "Keep it running",
                    tag = "need-stop",
                    onConfirm = { onStop(epic.id) },
                )
            }
            NeedKind.ROW, NeedKind.OTHER -> Unit
        }
    }
}

@Composable
private fun NeedRetry(onRetry: () -> Unit) {
    TextButton(onClick = onRetry, modifier = Modifier.testTag("need-retry")) {
        Text("Retry")
    }
}
