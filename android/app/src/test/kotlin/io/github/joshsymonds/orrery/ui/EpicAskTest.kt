package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
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
    steererParked: Boolean = false,
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
    steps = steps,
    questions = questions,
    parked = parked,
    plannerParked = plannerParked,
    steererParked = steererParked,
)

private val steererQuestion = EpicQuestionRow(
    id = "q1",
    text = "Which branch should it build on?",
    turn = 1L,
    asked = BASE,
    steererHeld = true,
)

private const val STEERER_QUESTION_LINE =
    "Its steerer holds this question, and answers it or escalates it to you: Which branch should it build on?"

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
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("It waits on your answer.")
        composeRule.onNodeWithTag("ask-question").assertTextEquals("Which branch should it build on?")
        composeRule.onNodeWithTag("ask-confirm").assertIsDisplayed()
        val sentence = composeRule.onNodeWithTag("now-sentence").fetchSemanticsNode().boundsInRoot
        val ask = composeRule.onNodeWithTag("ask").fetchSemanticsNode().boundsInRoot
        assertTrue(sentence.bottom <= ask.top)
    }

    @Test
    fun aDifferentOpenQuestionStartsWithAnEmptyAnswerBox() {
        val first = EpicQuestionRow(id = "q1", text = "Which branch should it build on?", turn = 1L, asked = BASE)
        val later = EpicQuestionRow(id = "q2", text = "Which lane should it use?", turn = 2L, asked = BASE)
        val current = mutableStateOf(detail(questions = listOf(first)))
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = current.value,
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("ask-other").performClick()
        composeRule.onNodeWithTag("ask-other-text").performTextReplacement("Use main")
        composeRule.runOnIdle { current.value = detail(questions = listOf(later)) }
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("ask-question").assertTextEquals("Which lane should it use?")
        composeRule.onNodeWithTag("ask-other").performClick()
        val typed = composeRule.onNodeWithTag("ask-other-text").fetchSemanticsNode().config[SemanticsProperties.EditableText]
        assertEquals("", typed.text)
    }

    @Test
    fun anOwnersParkGivesItsReasonAsTheAskWithExactlyRetryAndStop() {
        var retries = 0
        var stops = 0
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(parked = "budget exhausted"),
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = { retries++ },
                    onStop = { stops++ },
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("Parked. Nothing runs until you answer.")
        composeRule.onNodeWithTag("ask-label", useUnmergedTree = true).assertTextEquals("ASKS YOU")
        composeRule.onNodeWithTag("ask-question").assertTextEquals("Budget exhausted.")
        composeRule.onNodeWithTag("ask-option:1").assertTextContains("Retry")
        // The park's one answer, its confirm, and Stop…: nothing else in Now is pressable.
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(3)
        composeRule.onAllNodesWithTag("ask-option:2").assertCountEquals(0)
        composeRule.onAllNodesWithTag("ask-other").assertCountEquals(0)
        val sentence = composeRule.onNodeWithTag("now-sentence").fetchSemanticsNode().boundsInRoot
        val park = composeRule.onNodeWithTag("epic-park").fetchSemanticsNode().boundsInRoot
        assertTrue(sentence.bottom <= park.top)

        composeRule.onNodeWithTag("ask-option:1").performClick()
        composeRule.waitForIdle()
        assertEquals(0, retries)
        composeRule.onNodeWithTag("ask-confirm").performClick()
        composeRule.waitForIdle()
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
                    now = BASE,
                    onAnswer = { _, _, _ -> },
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
    fun aSteerersParkIsOneGreyLineWithNoAskAndNoClickableNode() {
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(parked = "budget exhausted", steererParked = true),
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals(
            "Parked. Its steerer holds the question; nothing is asked of you unless it escalates it.",
        )
        composeRule.onNodeWithTag("epic-steerer-holds").assertTextEquals(
            "Its steerer holds this park, and answers it or escalates it to you: budget exhausted",
        )
        composeRule.onAllNodesWithTag("epic-park").assertCountEquals(0)
        composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
    }

    @Test
    fun aQuestionItsSteererHoldsIsOneGreyLineInNowWithNoAskAndNoClickableNode() {
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(questions = listOf(steererQuestion)),
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("It waits on its steerer's answer.")
        composeRule.onNodeWithTag("epic-steerer-holds").assertTextEquals(STEERER_QUESTION_LINE)
        composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
    }

    @Test
    fun aConversationDrawsAQuestionItsSteererHoldsAsTheSteerersLineWithNoAsk() {
        FakeOrreryServer().use { server ->
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                MaterialTheme {
                    EpicQuestion(detail(questions = listOf(steererQuestion)), "q1", BASE, client)
                }
            }

            composeRule.onNodeWithTag("epic-steerer-question").assertTextEquals(STEERER_QUESTION_LINE)
            composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
            composeRule.onAllNodesWithTag("ask-settled").assertCountEquals(0)
            composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
        }
    }

    @Test
    fun aConversationDrawsTheSameQuestionOnceEscalatedAsAnAskCarryingItsEscalationLine() {
        val escalated = steererQuestion.copy(
            steererHeld = false,
            escalated = BASE,
            escalationReason = "the branch is the owner's call",
            escalationBy = "conversation",
            escalationSession = "example-session-1",
        )
        FakeOrreryServer().use { server ->
            val client = OrreryClient(server.baseUrl)
            composeRule.setContent {
                MaterialTheme {
                    EpicQuestion(detail(questions = listOf(escalated)), "q1", BASE, client)
                }
            }

            composeRule.onNodeWithTag("ask-question").assertTextEquals("Which branch should it build on?")
            composeRule.onNodeWithTag("ask-escalation")
                .assertTextEquals("escalated by session example-session-1: the branch is the owner's call")
            composeRule.onAllNodesWithTag("epic-steerer-question").assertCountEquals(0)
        }
    }

    @Test
    fun aLimitWaitGivesOneResumeNowThatTriesBeforeTheReset() {
        var resumes = 0
        composeRule.setContent {
            MaterialTheme {
                NowAsk(
                    detail = detail(steps = listOf(step(limited = true, limitKind = "weekly", limitResets = BASE))),
                    now = BASE,
                    onAnswer = { _, _, _ -> },
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
                    now = BASE,
                    onAnswer = { _, _, _ -> },
                    onRetry = {},
                    onStop = {},
                    onResume = {},
                )
            }
        }

        composeRule.onNodeWithTag("now-sentence").assertTextEquals("One task is being worked on.")
        composeRule.onAllNodes(hasClickAction()).assertCountEquals(0)
        composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
        composeRule.onAllNodesWithTag("epic-park").assertCountEquals(0)
    }
}
