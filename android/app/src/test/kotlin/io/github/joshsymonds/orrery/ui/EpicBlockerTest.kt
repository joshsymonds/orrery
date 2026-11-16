package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SLUG = "api"
private const val SENTENCE = "api-2 · checking · gate running"
private const val NOW_TAG = "now-sentence"
private const val QUESTION_TAG = "blocker-question"
private const val ANSWER_TAG = "blocker-answer"
private const val SEND_TAG = "blocker-answer-send"
private const val ERROR_TAG = "blocker-error"
private const val RETRY_TAG = "blocker-retry"
private const val STOP_TAG = "blocker-stop"
private const val STOP_SURE_TAG = "blocker-stop-sure"
private const val STOP_CONFIRM_TAG = "blocker-stop-confirm"
private const val STOP_KEEP_TAG = "blocker-stop-keep"
private const val PARKED_TEXT = "parked · rate limit"

private val questionBlocker = EpicBlocker.Question(
    text = "waits on your answer",
    since = 0L,
    question = EpicQuestionRow(id = "Q1", text = "Which branch should it use?", turn = 0L, asked = 0L),
)
private val laterQuestionBlocker = EpicBlocker.Question(
    text = "waits on your answer",
    since = 0L,
    question = EpicQuestionRow(id = "Q2", text = "Which lane should it use?", turn = 0L, asked = 0L),
)
private val parkedBlocker = EpicBlocker.Parked(text = PARKED_TEXT, since = 0L)
private val heldBlocker = EpicBlocker.Held(
    text = "api-1 held",
    since = 0L,
    step = StepRow(
        id = "S1",
        task = "api-1",
        agent = "",
        end = "",
        status = "",
        held = true,
        started = 0L,
        ended = 0L,
        doing = "",
        checks = emptyList(),
        reason = "",
    ),
)

// wedge makes a wedge whose now slice is the phase named by place, with summary as that slice's summary.
private fun wedge(task: String, place: WedgePlace, tone: WedgeTone, summary: String = ""): Wedge {
    val slices = Phase.entries.map { phase ->
        val now = phase.name == place.name
        PhaseSlice(
            phase = phase,
            state = if (now) SliceState.NOW else SliceState.AHEAD,
            summary = if (now) summary else "",
            since = 0L,
            seconds = 0L,
        )
    }
    return Wedge(task = task, title = task, requirements = emptyList(), place = place, tone = tone, slices = slices)
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicBlockerTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun nowSentenceReadsTheFirstRunningWedgeTaskPhaseAndNowSummary() {
        assertEquals(
            SENTENCE,
            nowSentence(
                listOf(
                    wedge("api-1", WedgePlace.IMPLEMENTING, WedgeTone.MINE, "waiting on you"),
                    wedge("api-2", WedgePlace.CHECKING, WedgeTone.RUN, "gate running"),
                    wedge("api-3", WedgePlace.REVIEWING, WedgeTone.RUN, "review"),
                ),
            ),
        )
    }

    @Test
    fun nowSentenceLeavesOutTheSummaryWhenTheNowSliceHasNone() {
        assertEquals("api-2 · reviewing", nowSentence(listOf(wedge("api-2", WedgePlace.REVIEWING, WedgeTone.RUN))))
    }

    @Test
    fun nowSentenceIsEmptyWhenNoWedgeRuns() {
        assertEquals("", nowSentence(listOf(wedge("api-1", WedgePlace.IMPLEMENTING, WedgeTone.MINE, "waiting on you"))))
        assertEquals("", nowSentence(emptyList()))
    }

    @Test
    fun theNowSentenceDrawsWhenItHasText() {
        setBlock(null, sentence = SENTENCE)

        composeRule.onNodeWithTag(NOW_TAG).assertTextEquals(SENTENCE)
    }

    @Test
    fun noNowSentenceDrawsWhenItIsEmpty() {
        setBlock(null, sentence = "")

        assertEquals(0, composeRule.onAllNodesWithTag(NOW_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun withNoBlockerThereIsNoFormAndNoButton() {
        setBlock(null, sentence = SENTENCE)

        assertEquals(0, composeRule.onAllNodesWithTag(QUESTION_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(ANSWER_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodes(hasClickAction()).fetchSemanticsNodes().size)
    }

    @Test
    fun anOpenQuestionDrawsItsTextAndAnAnswerFormWithADisabledAnswer() {
        setBlock(questionBlocker)

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which branch should it use?")
        composeRule.onNodeWithTag(ANSWER_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(SEND_TAG).assertIsNotEnabled()
    }

    @Test
    fun answerIsDisabledWhileTheTrimmedAnswerIsEmptyAndEnabledOnceItHasText() {
        setBlock(questionBlocker)

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("   ")
        composeRule.onNodeWithTag(SEND_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(SEND_TAG).assertIsEnabled()
    }

    @Test
    fun submittingSendsTheQuestionIdAndTheTrimmedAnswerAndClearsTheBoxWhenRecorded() {
        val calls = mutableListOf<Pair<String, String>>()
        setBlock(
            questionBlocker,
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
        setBlock(
            questionBlocker,
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

    @Test
    fun aDifferentOpenQuestionStartsWithAnEmptyAnswerBox() {
        val current = mutableStateOf<EpicBlocker?>(questionBlocker)
        composeRule.setContent {
            MaterialTheme {
                NowBlock(
                    sentence = "",
                    blocker = current.value,
                    slug = SLUG,
                    onAnswer = { _, _ -> "" },
                    onRetry = {},
                    onStop = {},
                )
            }
        }

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("Use main")
        composeRule.runOnIdle { current.value = laterQuestionBlocker }
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which lane should it use?")
        assertEquals("", answerText())
    }

    @Test
    fun aParkDrawsItsTextAndARetryThatCallsOnRetryOnceWithNoAnswerForm() {
        var retries = 0
        setBlock(parkedBlocker, onRetry = { retries++ })

        composeRule.onNodeWithText(PARKED_TEXT).assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag(QUESTION_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(ANSWER_TAG).fetchSemanticsNodes().size)
        composeRule.onNodeWithTag(RETRY_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(1, retries)
    }

    @Test
    fun retryIsNotDrawnForAnOpenQuestion() {
        setBlock(questionBlocker)

        assertEquals(0, composeRule.onAllNodesWithTag(RETRY_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun retryIsNotDrawnForAHeldStep() {
        setBlock(heldBlocker)

        assertEquals(0, composeRule.onAllNodesWithTag(RETRY_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun anOpenQuestionStopAsksFirstAndCallsOnStopOnceWhenConfirmed() {
        assertStopAsksFirst(questionBlocker)
    }

    @Test
    fun aParkedStopAsksFirstAndCallsOnStopOnceWhenConfirmed() {
        assertStopAsksFirst(parkedBlocker)
    }

    @Test
    fun aHeldStopAsksFirstAndCallsOnStopOnceWhenConfirmed() {
        assertStopAsksFirst(heldBlocker)
    }

    private fun setBlock(
        blocker: EpicBlocker?,
        sentence: String = "",
        onAnswer: (questionId: String, text: String) -> String = { _, _ -> "" },
        onRetry: () -> Unit = {},
        onStop: () -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                NowBlock(
                    sentence = sentence,
                    blocker = blocker,
                    slug = SLUG,
                    onAnswer = onAnswer,
                    onRetry = onRetry,
                    onStop = onStop,
                )
            }
        }
    }

    private fun assertStopAsksFirst(blocker: EpicBlocker) {
        var stops = 0
        setBlock(blocker, onStop = { stops++ })

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        assertEquals(0, stops)
        composeRule.onNodeWithTag(STOP_SURE_TAG).assertTextEquals("Stop $SLUG?")
        composeRule.onNodeWithTag(STOP_CONFIRM_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(1, stops)
        assertEquals(0, composeRule.onAllNodesWithTag(STOP_SURE_TAG).fetchSemanticsNodes().size)
        composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
    }

    private fun answerText(): String =
        composeRule.onNodeWithTag(ANSWER_TAG).fetchSemanticsNode().config[SemanticsProperties.EditableText].text
}
