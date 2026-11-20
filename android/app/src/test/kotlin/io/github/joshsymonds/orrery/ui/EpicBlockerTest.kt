package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val QUESTION_TAG = "blocker-question"
private const val ANSWER_TAG = "blocker-answer"
private const val SEND_TAG = "blocker-answer-send"
private const val ERROR_TAG = "blocker-error"

private val question = EpicQuestionRow(id = "Q1", text = "Which branch should it use?", turn = 0L, asked = 0L)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicBlockerTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun anOpenQuestionDrawsItsTextAndAnAnswerFormWithADisabledAnswer() {
        setForm()

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which branch should it use?")
        composeRule.onNodeWithTag(ANSWER_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(SEND_TAG).assertIsNotEnabled()
    }

    @Test
    fun answerIsDisabledWhileTheTrimmedAnswerIsEmptyAndEnabledOnceItHasText() {
        setForm()

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("   ")
        composeRule.onNodeWithTag(SEND_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(SEND_TAG).assertIsEnabled()
    }

    @Test
    fun submittingSendsTheQuestionIdAndTheTrimmedAnswerAndClearsTheBoxWhenRecorded() {
        val calls = mutableListOf<Pair<String, String>>()
        setForm(
            onAnswer = { questionId, text ->
                calls += questionId to text
                ""
            },
        )

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(SEND_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf("Q1" to "Use main"), calls)
        assertEquals("", answerText())
        assertEquals(0, composeRule.onAllNodesWithTag(ERROR_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun aReturnedErrorShowsUnderTheFormAndKeepsTheAnswerAndALaterSuccessClearsIt() {
        var attempts = 0
        setForm(
            onAnswer = { _, _ ->
                attempts++
                if (attempts == 1) "question closed" else ""
            },
        )

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("Use main")
        composeRule.onNodeWithTag(SEND_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag(ERROR_TAG).assertTextEquals("question closed")
        assertEquals("Use main", answerText())

        composeRule.onNodeWithTag(SEND_TAG).performClick()
        composeRule.waitForIdle()
        assertEquals(0, composeRule.onAllNodesWithTag(ERROR_TAG).fetchSemanticsNodes().size)
        assertEquals("", answerText())
    }

    private fun setForm(onAnswer: (questionId: String, text: String) -> String = { _, _ -> "" }) {
        composeRule.setContent {
            MaterialTheme {
                AnswerForm(question, onAnswer = onAnswer)
            }
        }
    }

    private fun answerText(): String =
        composeRule.onNodeWithTag(ANSWER_TAG).fetchSemanticsNode().config[SemanticsProperties.EditableText].text
}
