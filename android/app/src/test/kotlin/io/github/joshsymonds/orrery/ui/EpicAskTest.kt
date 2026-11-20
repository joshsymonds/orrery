package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// 1_700_000_000 is 22:13:20 in UTC, the zone these tests run in. A limit's reset is written as this second.
private const val BASE = 1_700_000_000L

private fun step(
    limited: Boolean = false,
    limitKind: String = "",
    limitResets: Long = 0L,
): StepRow = StepRow(
    id = "step",
    task = "T1",
    agent = "builder",
    end = "",
    status = "",
    held = false,
    started = 100L,
    ended = 0L,
    doing = "",
    checks = emptyList(),
    reason = "",
    limited = limited,
    limitKind = limitKind,
    limitResets = limitResets,
)

private fun detail(
    steps: List<StepRow> = emptyList(),
    questions: List<EpicQuestionRow> = emptyList(),
    parked: String = "",
    plannerParked: Boolean = false,
): EpicDetail = EpicDetail(
    epic = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = "built",
        changed = 0L,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
    ),
    tasks = emptyList(),
    slices = emptyList(),
    steps = steps,
    questions = questions,
    parked = parked,
    plannerParked = plannerParked,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicAskTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @Before
    fun useUtc() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
    }

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    @Test
    fun aQuestionsAskComesRightAfterTheSentence() {
        val question = EpicQuestionRow(id = "q1", text = "Which branch should it build on?", turn = 1L, asked = BASE)
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(questions = listOf(question)),
                    onAnswer = { _, _ -> "" },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("It waits on your answer.")
        composeRule.onNodeWithTag("blocker-question").assertTextEquals("Which branch should it build on?")
        composeRule.onNodeWithTag("blocker-answer-send").assertIsDisplayed()
        val sentence = composeRule.onNodeWithTag("now-sentence").fetchSemanticsNode().boundsInRoot
        val ask = composeRule.onNodeWithTag("blocker-question").fetchSemanticsNode().boundsInRoot
        assertTrue(sentence.bottom <= ask.top)
    }

    @Test
    fun anOwnersParkGivesItsReasonAndExactlyRetryAndStop() {
        var retries = 0
        var stops = 0
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(parked = "budget exhausted"),
                    onAnswer = { _, _ -> "" },
                    onRetry = { retries++ },
                    onStop = { stops++ },
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("Parked. Nothing runs until you answer.")
        composeRule.onNodeWithText("It asks you what to do").assertIsDisplayed()
        composeRule.onNodeWithTag("epic-park-reason").assertTextEquals("Budget exhausted.")
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(2)
        val sentence = composeRule.onNodeWithTag("now-sentence").fetchSemanticsNode().boundsInRoot
        val park = composeRule.onNodeWithTag("epic-park").fetchSemanticsNode().boundsInRoot
        assertTrue(sentence.bottom <= park.top)

        composeRule.onNodeWithTag("epic-park-retry").performClick()
        assertEquals(1, retries)
        assertEquals(0, stops)

        composeRule.onNodeWithTag("epic-park-stop").performClick()
        assertEquals(1, retries)
        assertEquals(1, stops)
    }

    @Test
    fun aPlannersParkIsOneGreyLineWithNoClickableNode() {
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(parked = "budget exhausted", plannerParked = true),
                    onAnswer = { _, _ -> "" },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals(
            "The planner holds a park and is deciding what to do. Nothing is asked of you.",
        )
        composeRule.onNodeWithTag("epic-planner-park").assertTextEquals(
            "The planner holds this park: budget exhausted. It will retry, replan, or ask you.",
        )
        composeRule.onAllNodesWithTag("epic-park").assertCountEquals(0)
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
    }

    @Test
    fun aLimitWaitGivesOneResumeNowThatTriesBeforeTheReset() {
        var resumes = 0
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(steps = listOf(step(limited = true, limitKind = "weekly", limitResets = BASE))),
                    onAnswer = { _, _ -> "" },
                    onRetry = {},
                    onStop = {},
                    onResume = { resumes++ },
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals(
            "1 step waits on the weekly limit, which resets at 22:13. It resumes on its own.",
        )
        composeRule.onNodeWithText("Try before the reset. A step still over the limit stays held.").assertIsDisplayed()
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(1)

        composeRule.onNodeWithTag("epic-resume-now").performClick()
        assertEquals(1, resumes)
    }

    @Test
    fun nothingOpenDrawsNoAskAndNoControl() {
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(steps = listOf(step())),
                    onAnswer = { _, _ -> "" },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("One task is being worked on.")
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
        composeRule.onAllNodesWithTag("blocker-question").assertCountEquals(0)
        composeRule.onAllNodesWithTag("epic-park").assertCountEquals(0)
    }
}
