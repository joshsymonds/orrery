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
private const val SENTENCE = "api-2 · checking · gate running"
private const val NOW_TAG = "now-sentence"
private const val ASK_TAG = "ask"
private const val QUESTION_TAG = "ask-question"
private const val OTHER_TAG = "ask-other"
private const val OTHER_TEXT_TAG = "ask-other-text"
private const val CONFIRM_TAG = "ask-confirm"
private const val STOP_TAG = "blocker-stop"
private const val STOP_SURE_TAG = "blocker-stop-sure"
private const val STOP_CONFIRM_TAG = "blocker-stop-confirm"
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
    fun withNoBlockerThereIsNoAskAndNoButton() {
        setBlock(null, sentence = SENTENCE)

        assertEquals(0, composeRule.onAllNodesWithTag(ASK_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(QUESTION_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodes(hasClickAction()).fetchSemanticsNodes().size)
    }

    @Test
    fun anOpenQuestionDrawsItsTextAndAnAskWithItsConfirmDisabledUntilPicked() {
        setBlock(questionBlocker)

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which branch should it use?")
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsNotEnabled()
    }

    @Test
    fun confirmIsDisabledWhileSomethingElseIsBlankAndEnabledOnceItHasText() {
        setBlock(questionBlocker)

        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("   ")
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsNotEnabled()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsEnabled()
    }

    @Test
    fun aDifferentOpenQuestionStartsWithAnEmptySomethingElseBox() {
        val current = mutableStateOf<EpicBlocker?>(questionBlocker)
        composeRule.setContent {
            MaterialTheme {
                NowBlock(
                    sentence = "",
                    blocker = current.value,
                    epic = epicOf(),
                    now = NOW,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                )
            }
        }

        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        composeRule.onNodeWithTag(OTHER_TEXT_TAG).performTextReplacement("Use main")
        composeRule.runOnIdle { current.value = laterQuestionBlocker }
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(QUESTION_TAG).assertTextEquals("Which lane should it use?")
        composeRule.onNodeWithTag(OTHER_TAG).performClick()
        assertEquals("", textOf(OTHER_TEXT_TAG))
    }

    @Test
    fun aParkDrawsItsReasonAsTheAskWithNoSomethingElseBox() {
        setBlock(parkedBlocker)

        composeRule.onNodeWithText(PARKED_TEXT).assertIsDisplayed()
        composeRule.onNodeWithTag(ASK_TAG).assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag(OTHER_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(OTHER_TEXT_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun retryIsNotDrawnForAnOpenQuestion() {
        setBlock(questionBlocker)

        assertEquals(0, composeRule.onAllNodes(hasText("Retry")).fetchSemanticsNodes().size)
    }

    @Test
    fun retryIsNotDrawnForAHeldStep() {
        setBlock(heldBlocker)

        assertEquals(0, composeRule.onAllNodes(hasText("Retry")).fetchSemanticsNodes().size)
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

    private fun epicOf(): EpicRow = EpicRow(
        id = EPIC_ID,
        repository = "example-repo",
        slug = SLUG,
        outcome = "",
        changed = NOW - 30,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        path = "/home/user/example-repo",
    )

    private fun setBlock(blocker: EpicBlocker?, sentence: String = "", onStop: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                NowBlock(
                    sentence = sentence,
                    blocker = blocker,
                    epic = epicOf(),
                    now = NOW,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
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

    private fun textOf(tag: String): String =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.EditableText].text
}
