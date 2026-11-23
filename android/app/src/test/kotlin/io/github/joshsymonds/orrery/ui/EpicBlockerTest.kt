package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val NOW = 1_700_000_000L
private const val EPIC_ID = "example-repo/example-epic"
private const val SLUG = "example-epic"
private const val ASK_TAG = "ask"
private const val QUESTION_TAG = "ask-question"
private const val OTHER_TAG = "ask-other"
private const val OTHER_TEXT_TAG = "ask-other-text"
private const val CONFIRM_TAG = "ask-confirm"
private const val PARK_STOP_TAG = "epic-park-stop"

private val question = EpicQuestionRow(id = "Q1", text = "Which branch should it use?", turn = 0L, asked = 0L)
private val laterQuestion = EpicQuestionRow(id = "Q2", text = "Which lane should it use?", turn = 0L, asked = 0L)

private fun step(held: Boolean = false): StepRow = StepRow(
    id = "S1",
    task = "api-1",
    agent = "",
    end = "",
    status = "",
    held = held,
    started = 0L,
    ended = 0L,
    doing = "",
    checks = emptyList(),
    reason = "",
)

private fun epicOf(): EpicRow = EpicRow(
    id = EPIC_ID,
    repository = "example-repo",
    slug = SLUG,
    outcome = "",
    changed = NOW - 30,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    path = "/home/user/example-repo",
)

private fun detail(
    questions: List<EpicQuestionRow> = emptyList(),
    parked: String = "",
    steps: List<StepRow> = emptyList(),
): EpicDetail = EpicDetail(
    epic = epicOf(),
    tasks = emptyList(),
    steps = steps,
    questions = questions,
    parked = parked,
)

// What each blocker of an epic draws in Now: an open question its Ask, an owner's park its Ask with Retry and its Stop…,
// and a held step nothing that answers.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicBlockerTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun withNoBlockerThereIsNoAskAndNoButton() {
        setNow(detail(steps = listOf(step())))

        assertEquals(0, composeRule.onAllNodesWithTag(ASK_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(QUESTION_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodes(hasClickAction()).fetchSemanticsNodes().size)
    }

    @Test
    fun anOpenQuestionDrawsItsTextAndAnAskWithItsConfirmDisabledUntilPicked() {
        setNow(detail(questions = listOf(question)))

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which branch should it use?")
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsNotEnabled()
    }

    @Test
    fun confirmIsDisabledWhileSomethingElseIsBlankAndEnabledOnceItHasText() {
        setNow(detail(questions = listOf(question)))

        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("   ")
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsEnabled()
    }

    @Test
    fun aDifferentOpenQuestionStartsWithAnEmptySomethingElseBox() {
        val current = mutableStateOf(detail(questions = listOf(question)))
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = current.value,
                    now = NOW,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("Use main")
        composeRule.runOnIdle { current.value = detail(questions = listOf(laterQuestion)) }
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which lane should it use?")
        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        assertEquals("", textOf(OTHER_TEXT_TAG))
    }

    @Test
    fun aParkDrawsItsReasonAsTheAskWithNoSomethingElseBox() {
        setNow(detail(parked = "rate limit"))

        composeRule.onNodeWithText("Rate limit.").assertIsDisplayed()
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag(OTHER_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(OTHER_TEXT_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun retryIsNotDrawnForAnOpenQuestion() {
        setNow(detail(questions = listOf(question)))

        assertEquals(0, composeRule.onAllNodes(hasText("Retry")).fetchSemanticsNodes().size)
    }

    @Test
    fun retryIsNotDrawnForAHeldStep() {
        setNow(detail(steps = listOf(step(held = true))))

        assertEquals(0, composeRule.onAllNodes(hasText("Retry")).fetchSemanticsNodes().size)
    }

    @Test
    fun aParksStopCallsOnStopOnceAndNoQuestionOrHeldStepDrawsOne() {
        var stops = 0
        val current = mutableStateOf(detail(parked = "rate limit"))
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = current.value,
                    now = NOW,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = { stops++ },
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag(PARK_STOP_TAG).assertTextEquals("Stop…").performClick()
        composeRule.waitForIdle()
        assertEquals(1, stops)

        for (blocked in listOf(detail(questions = listOf(question)), detail(steps = listOf(step(held = true))))) {
            composeRule.runOnIdle { current.value = blocked }
            composeRule.waitForIdle()
            assertEquals(0, composeRule.onAllNodesWithTag(PARK_STOP_TAG).fetchSemanticsNodes().size)
        }
    }

    private fun setNow(detail: EpicDetail) {
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail,
                    now = NOW,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }
    }

    private fun textOf(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text
}
