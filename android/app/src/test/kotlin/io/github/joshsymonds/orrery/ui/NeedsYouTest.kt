package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextReplacement
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicReleaseProofRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val ALPHA_ID = "repo-a/alpha"
private const val BETA_ID = "repo-a/beta"
private const val HEADING = "Needs you"
private const val NEEDS_TAG = "needs-you"
private const val WHY_TAG = "need-why"
private const val ANSWER_TAG = "blocker-answer"
private const val SEND_TAG = "blocker-answer-send"
private const val RETRY_TAG = "need-retry"
private const val STOP_TAG = "need-stop"
private const val STOP_SURE_TAG = "need-stop-sure"
private const val STOP_CONFIRM_TAG = "need-stop-confirm"

private val alpha = epicRow(ALPHA_ID, "alpha")
private val beta = epicRow(BETA_ID, "beta")

private fun epicRow(id: String, slug: String) = EpicRow(
    id = id,
    repository = "repo-a",
    slug = slug,
    outcome = "",
    changed = 0L,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    problem = "",
)

private fun pane(id: String, label: String, summary: String = "") = Pane(
    id = id,
    host = "box",
    session = "example-session",
    window = 1,
    pane = 2,
    cwd = "/work/api",
    command = "claude",
    state = "waiting",
    summary = summary,
    label = label,
    changed = 0L,
    seen = 0L,
    question = "",
    questionHash = "",
)

private fun epicDetail(
    epic: EpicRow,
    questions: List<EpicQuestionRow> = emptyList(),
    parked: String = "",
    releaseProof: List<EpicReleaseProofRow> = emptyList(),
    steps: List<StepRow> = emptyList(),
) = EpicDetail(
    epic = epic,
    tasks = emptyList(),
    slices = emptyList(),
    steps = steps,
    questions = questions,
    parked = parked,
    releaseProof = releaseProof,
)

private fun openQuestion(id: String, text: String) = EpicQuestionRow(id = id, text = text, turn = 0L, asked = 0L)

private fun failedRelease(position: Long, step: String, reason: String) = EpicReleaseProofRow(
    position = position,
    step = step,
    target = "",
    undo = "",
    state = "failed",
    evidence = "",
    reason = reason,
    attempt = 0L,
    at = 0L,
)

private fun heldStep(task: String) = StepRow(
    id = "S1",
    task = task,
    agent = "",
    end = "",
    status = "",
    held = true,
    started = 0L,
    ended = 0L,
    doing = "",
    checks = emptyList(),
    reason = "",
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class NeedsYouTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun withNoNeedsThereIsNoHeading() {
        setNeeds()

        assertEquals(0, composeRule.onAllNodesWithTag(NEEDS_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithText(HEADING).fetchSemanticsNodes().size)
    }

    @Test
    fun withNeedsTheHeadingDrawsAboveThem() {
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(ALPHA_ID to epicDetail(alpha, parked = "budget")),
        )

        composeRule.onNodeWithTag(NEEDS_TAG).assertIsDisplayed()
        val heading = composeRule.onNodeWithText(HEADING).fetchSemanticsNode().positionInRoot.y
        val need = composeRule.onNodeWithText("alpha is parked: budget").fetchSemanticsNode().positionInRoot.y
        assertTrue("the heading draws above the need", heading < need)
    }

    @Test
    fun aWaitingPaneDrawsItsSentenceAndATapOpensThePane() {
        val opened = mutableListOf<Pane>()
        val api = pane("%3", "api")
        setNeeds(waiting = listOf(api), onOpenPane = { opened += it })

        composeRule.onNodeWithText("api waits on you").assertIsDisplayed()
        composeRule.onNodeWithText("api waits on you").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(api), opened)
    }

    @Test
    fun aWaitingPaneDrawsItsSummaryUnderItsSentenceAsTheWhy() {
        setNeeds(waiting = listOf(pane("%3", "web", summary = "build finished")))

        composeRule.onNodeWithText("web waits on you").assertIsDisplayed()
        composeRule.onNodeWithTag(WHY_TAG).assertTextEquals("build finished")
    }

    @Test
    fun aWaitingPaneWithNoSummaryDrawsNoWhyLine() {
        setNeeds(waiting = listOf(pane("%3", "web")))

        composeRule.onNodeWithText("web waits on you").assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag(WHY_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun aQuestionNeedDrawsItsSentenceAndAnAnswerFormWithoutRepeatingTheQuestion() {
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(
                ALPHA_ID to epicDetail(alpha, questions = listOf(openQuestion("Q1", "Which branch should it use?"))),
            ),
        )

        composeRule.onNodeWithText("alpha asks: Which branch should it use?").assertIsDisplayed()
        composeRule.onNodeWithTag(ANSWER_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(SEND_TAG).assertIsNotEnabled()
        assertEquals(
            1,
            composeRule.onAllNodesWithText("Which branch should it use?", substring = true).fetchSemanticsNodes().size,
        )
        assertEquals(0, composeRule.onAllNodesWithTag(RETRY_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(STOP_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun theAnswerSendCallsOnAnswerWithTheEpicQuestionIdAndTheTrimmedText() {
        val calls = mutableListOf<Triple<String, String, String>>()
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(
                ALPHA_ID to epicDetail(alpha, questions = listOf(openQuestion("Q1", "Which branch should it use?"))),
            ),
            onAnswer = { epic, questionId, text ->
                calls += Triple(epic, questionId, text)
                ""
            },
        )

        composeRule.onNodeWithTag(ANSWER_TAG).performTextReplacement("  Use main  ")
        composeRule.onNodeWithTag(SEND_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Triple(ALPHA_ID, "Q1", "Use main")), calls)
        assertEquals("", answerText())
    }

    @Test
    fun eachQuestionNeedsFormSendsToItsOwnEpicEvenWhenQuestionIdsMatch() {
        val calls = mutableListOf<Triple<String, String, String>>()
        setNeeds(
            epics = listOf(alpha, beta),
            details = mapOf(
                ALPHA_ID to epicDetail(alpha, questions = listOf(openQuestion("Q1", "Which branch should it use?"))),
                BETA_ID to epicDetail(beta, questions = listOf(openQuestion("Q1", "Which lane should it use?"))),
            ),
            onAnswer = { epic, questionId, text ->
                calls += Triple(epic, questionId, text)
                ""
            },
        )

        composeRule.onAllNodesWithTag(ANSWER_TAG)[1].performTextReplacement("Use lane two")
        composeRule.onAllNodesWithTag(SEND_TAG)[1].performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(Triple(BETA_ID, "Q1", "Use lane two")), calls)
    }

    @Test
    fun aParkNeedDrawsRetryThatCallsOnRetryWithItsEpicAndNoAnswerFormOrStop() {
        val retries = mutableListOf<String>()
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(ALPHA_ID to epicDetail(alpha, parked = "rate limit")),
            onRetry = { retries += it },
        )

        composeRule.onNodeWithText("alpha is parked: rate limit").assertIsDisplayed()
        composeRule.onNodeWithTag(RETRY_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(ALPHA_ID), retries)
        assertEquals(0, composeRule.onAllNodesWithTag(ANSWER_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(STOP_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun aReleaseNeedDrawsItsSentenceAndRetryThatCallsOnRetryWithItsEpic() {
        val retries = mutableListOf<String>()
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(
                ALPHA_ID to epicDetail(
                    alpha,
                    releaseProof = listOf(failedRelease(2L, "publish", "registry 503")),
                ),
            ),
            onRetry = { retries += it },
        )

        composeRule.onNodeWithText("alpha: release step 2 (publish) failed: registry 503").assertIsDisplayed()
        composeRule.onNodeWithTag(RETRY_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(ALPHA_ID), retries)
    }

    @Test
    fun aReleaseNeedsStopAsksFirstAndCallsOnStopWithItsEpicWhenConfirmed() {
        val stops = mutableListOf<String>()
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(
                ALPHA_ID to epicDetail(
                    alpha,
                    releaseProof = listOf(failedRelease(2L, "publish", "registry 503")),
                ),
            ),
            onStop = { stops += it },
        )

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        assertEquals(0, stops.size)
        composeRule.onNodeWithTag(STOP_SURE_TAG).assertTextEquals("Stop alpha?")
        composeRule.onNodeWithTag(STOP_CONFIRM_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(ALPHA_ID), stops)
    }

    @Test
    fun anOtherNeedDrawsItsOwnTextAloneWithNoControl() {
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(ALPHA_ID to epicDetail(alpha, steps = listOf(heldStep("web-line")))),
        )

        composeRule.onNodeWithText("alpha: web-line held").assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag(ANSWER_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(RETRY_TAG).fetchSemanticsNodes().size)
        assertEquals(0, composeRule.onAllNodesWithTag(STOP_TAG).fetchSemanticsNodes().size)
    }

    @Test
    fun aQuestionSentenceTapOpensItsEpic() {
        val opened = mutableListOf<String>()
        setNeeds(
            epics = listOf(alpha),
            details = mapOf(
                ALPHA_ID to epicDetail(alpha, questions = listOf(openQuestion("Q1", "Which branch should it use?"))),
            ),
            onOpenEpic = { opened += it },
        )

        composeRule.onNodeWithText("alpha asks: Which branch should it use?").performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(ALPHA_ID), opened)
    }

    private fun setNeeds(
        waiting: List<Pane> = emptyList(),
        epics: List<EpicRow> = emptyList(),
        details: Map<String, EpicDetail> = emptyMap(),
        onOpenPane: (Pane) -> Unit = {},
        onOpenEpic: (String) -> Unit = {},
        onAnswer: (epic: String, questionId: String, text: String) -> String = { _, _, _ -> "" },
        onRetry: (epic: String) -> Unit = {},
        onStop: (epic: String) -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                NeedsYou(
                    waiting = waiting,
                    epics = epics,
                    details = details,
                    onOpenPane = onOpenPane,
                    onOpenEpic = onOpenEpic,
                    onAnswer = onAnswer,
                    onRetry = onRetry,
                    onStop = onStop,
                )
            }
        }
    }

    private fun answerText(): String =
        composeRule.onNodeWithTag(ANSWER_TAG).fetchSemanticsNode().config[SemanticsProperties.EditableText].text
}
