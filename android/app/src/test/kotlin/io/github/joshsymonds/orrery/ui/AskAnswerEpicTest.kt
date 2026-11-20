package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// The epic's Ask as the now block draws it: a question's choices and its pick-then-confirm answer, a park's Retry. What
// a confirm sends is recorded at the block's callbacks, against a synthetic clock: NOW is the instant ages are measured
// from.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskAnswerEpicTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val EPIC_ID = "example-repo/example-epic"
        const val PARKED = "parked · rate limit"
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private data class Answer(val questionId: String, val choice: String, val text: String)

    private val answers = mutableListOf<Answer>()
    private var retries = 0

    private fun epicOf(): EpicRow = EpicRow(
        id = EPIC_ID,
        repository = "example-repo",
        slug = "example-epic",
        outcome = "",
        changed = NOW - 30,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        path = "/home/user/example-repo",
    )

    private fun question(): EpicQuestionRow = EpicQuestionRow(
        id = "question-1",
        text = "Which way?",
        turn = 1L,
        asked = NOW - 120,
        choices = listOf(
            EpicQuestionChoiceRow("keep", "keep the current layout"),
            EpicQuestionChoiceRow("split", "split the screen in two"),
        ),
        recommendedChoice = "split",
        recommendedWhy = "it reads better on a phone",
    )

    private fun setBlock(blocker: EpicBlocker) {
        composeRule.setContent {
            MaterialTheme {
                NowBlock(
                    sentence = "",
                    blocker = blocker,
                    epic = epicOf(),
                    now = NOW,
                    onAnswer = { questionId, choice, text -> answers += Answer(questionId, choice, text) },
                    onRetry = { retries++ },
                    onStop = {},
                )
            }
        }
    }

    @Test
    fun anEpicQuestionDrawsItsChoicesWithTheRecommendedOneMarkedAndNoNotNowOrMore() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-option:1").assertExists()
        composeRule.onNodeWithTag("ask-option:2").assertExists()
        composeRule.onNodeWithTag("ask-recommended:2", useUnmergedTree = true).assertExists()
        assertEquals(0, composeRule.onAllNodesWithTag("ask-recommended:1", useUnmergedTree = true).fetchSemanticsNodes().size)
        composeRule.onNodeWithTag("ask-not-now").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-more").assertDoesNotExist()
    }

    @Test
    fun aTapPicksAChoiceAndSendsNothing() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.waitForIdle()

        assertEquals(emptyList<Answer>(), answers)
        composeRule.onNodeWithTag("ask-confirm").assertIsEnabled()
    }

    @Test
    fun confirmSendsTheChosenChoicesIdAndItsDoesText() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Answer("question-1", "keep", "keep the current layout")), answers)
    }

    @Test
    fun confirmOfSomethingElseSendsOtherAndTheTrimmedTypedText() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-confirm").assertIsNotEnabled()
        composeRule.onNodeWithTag("ask-other-text").performTextReplacement("  go with both  ")
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Answer("question-1", "other", "go with both")), answers)
    }

    @Test
    fun aParkConfirmCallsRetryOnceAndSendsNoAnswer() {
        setBlock(EpicBlocker.Parked(text = PARKED, since = 0L))

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(1, retries)
        assertEquals(emptyList<Answer>(), answers)
    }

    @Test
    fun theRecordLineFollowsThePickAndIsWhatConfirmWillSend() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-record").assertTextContains("owner: split the screen in two")
        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-record").assertTextContains("owner: keep the current layout")
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Answer("question-1", "keep", "keep the current layout")), answers)
    }

    @Test
    fun somethingElseRecordsTheTrimmedTextThatConfirmWillSend() {
        setBlock(EpicBlocker.Question(text = "waits on your answer", since = NOW - 120, question = question()))

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-record").assertDoesNotExist()
        composeRule.onNodeWithTag("ask-other-text").performTextReplacement("  go with both  ")
        composeRule.onNodeWithTag("ask-record").assertTextContains("owner: go with both")
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Answer("question-1", "other", "go with both")), answers)
    }

    @Test
    fun anEpicAnswerInFlightDrawsSendingAndSendsNothingMore() {
        val release = CompletableDeferred<String>()
        var sends = 0
        setAsk { _, _ ->
            sends++
            release.await()
        }

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag("ask-sending").assertIsDisplayed()
        composeRule.onNodeWithTag("ask-confirm").assertIsNotEnabled()

        composeRule.onNodeWithTag("ask-option:2").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag("ask-option:1").assertIsSelected()
        composeRule.onNodeWithTag("ask-option:2").assertIsNotSelected()
        assertEquals(1, sends)

        release.complete("")
        composeRule.awaitUntil { composeRule.onAllNodesWithTag("ask-sending").fetchSemanticsNodes().isEmpty() }
        assertEquals(1, sends)
    }

    @Test
    fun aRefusedEpicAnswerKeepsItsWordsAndItsPick() {
        setAsk { _, _ -> "the question changed" }

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-other-text").performTextReplacement("go with both")
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("ask-note").assertTextEquals("the question changed")
        composeRule.onNodeWithTag("ask-other").assertIsSelected()
        composeRule.onNodeWithTag("ask-other-text").assertTextEquals("go with both")
        composeRule.onNodeWithTag("ask-confirm").assertIsEnabled()
    }

    @Test
    fun anEpicAnswerThatFailsToSendKeepsItsPickAndSaysItIsKept() {
        setAsk { _, _ -> throw IOException("offline") }

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("ask-note").assertTextEquals("Not answered. Your answer is kept.")
        composeRule.onNodeWithTag("ask-option:1").assertIsSelected()
    }

    private fun setAsk(onConfirm: suspend (choice: String, text: String) -> String) {
        composeRule.setContent {
            MaterialTheme {
                Ask(
                    model = epicAsk(epicOf(), question()),
                    now = NOW,
                    phase = AskPhase.OPEN,
                    picked = null,
                    note = "",
                    onAnswer = null,
                    onNotNow = null,
                    onBringBack = null,
                    onDecline = null,
                    onArchive = null,
                    onConfirm = onConfirm,
                )
            }
        }
    }
}
