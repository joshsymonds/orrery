package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val NOW = 1_700_000_000L
private const val REASON = "The lane choice changes the schema."

private fun epicOf(): EpicRow = EpicRow(
    id = "example-repo/example-epic",
    repository = "example-repo",
    slug = "example-epic",
    outcome = "",
    changed = NOW - 30,
    live = true,
    runningSteps = emptyList(),
    endedSteps = 0,
    path = "/home/user/example-repo",
)

private fun question(
    escalated: Long = 0L,
    by: String = "",
    session: String = "",
    reason: String = "",
): EpicQuestionRow = EpicQuestionRow(
    id = "question-1",
    text = "Which lane should it use?",
    turn = 1L,
    asked = NOW - 120,
    choices = listOf(
        EpicQuestionChoiceRow("lane-a", "use lane A"),
        EpicQuestionChoiceRow("lane-b", "use lane B"),
    ),
    recommendedChoice = "lane-b",
    recommendedWhy = "it is already tested",
    escalated = escalated,
    escalationReason = reason,
    escalationBy = by,
    escalationSession = session,
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class EpicAskEscalationMakerTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun `an escalated question's ask names its maker and reason`() {
        val escalated = question(
            escalated = NOW - 60,
            by = "conversation",
            session = "example-session-1",
            reason = REASON,
        )

        assertEquals(
            "escalated by session example-session-1: $REASON",
            epicAsk(epicOf(), escalated).escalation,
        )
    }

    @Test
    fun `an escalated question by the owner names its maker you`() {
        val escalated = question(escalated = NOW - 60, by = "owner", reason = REASON)

        assertEquals("escalated by you: $REASON", epicAsk(epicOf(), escalated).escalation)
    }

    @Test
    fun `a question not escalated has no escalated maker line`() {
        assertEquals("", epicAsk(epicOf(), question()).escalation)
    }

    @Test
    fun `the ask draws an escalated question's maker and reason`() {
        val model = mutableStateOf(
            epicAsk(
                epicOf(),
                question(
                    escalated = NOW - 60,
                    by = "conversation",
                    session = "example-session-1",
                    reason = REASON,
                ),
            ),
        )
        composeRule.setContent {
            MaterialTheme {
                Ask(
                    model = model.value,
                    now = NOW,
                    phase = AskPhase.OPEN,
                    picked = null,
                    note = "",
                    onAnswer = null,
                    onNotNow = null,
                    onBringBack = null,
                    onDecline = null,
                    onArchive = null,
                    onConfirm = null,
                )
            }
        }

        composeRule.onNodeWithTag("ask-escalation")
            .assertTextEquals("escalated by session example-session-1: $REASON")

        composeRule.runOnIdle { model.value = epicAsk(epicOf(), question()) }
        composeRule.waitForIdle()

        composeRule.onAllNodesWithTag("ask-escalation").assertCountEquals(0)
    }
}
