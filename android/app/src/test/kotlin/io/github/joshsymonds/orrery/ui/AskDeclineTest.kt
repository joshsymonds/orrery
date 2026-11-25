package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.DeclineRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.DeclineResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.EpicsArchiveResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// Decline is only in the More sheet, and it denies the request with the fields the server names the question by. Archive
// this session is the stale question's foot: it archives the conversation's own row, and is absent without one. Checked
// through the fake server's requests and the node tree, against the test's virtual clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskDeclineTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val DAY = 86_400L
        const val PANE_ID = "%3"
        const val SESSION_ID = "00000000000000000000000000000005"
        const val DECLINE = "/orrery.v1.Control/Decline"
        const val ARCHIVE = "/orrery.v1.Epics/Archive"
        const val BASH_QUESTION = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val model = mutableStateOf<AskModel?>(null)

    private fun pane(orreryId: String = "", changed: Long = NOW - 60): Pane = Pane(
        id = PANE_ID,
        host = "example-host",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = "/home/user/alpha",
        command = "claude",
        state = "waiting",
        summary = "",
        label = "label-alpha",
        changed = changed,
        seen = 0L,
        question = BASH_QUESTION,
        questionHash = "abc123",
        orreryId = orreryId,
    )

    private fun setHost(server: FakeOrreryServer, onRefresh: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                AskHost(model = model.value, client = client, now = NOW, onRefresh = onRefresh)
            }
        }
    }

    private fun epicRow(): EpicRow = EpicRow(
        id = "epic-1",
        repository = "example-repo",
        slug = "example-epic",
        outcome = "",
        changed = NOW - 30,
        live = true,
        runningSteps = emptyList(),
        endedSteps = 0,
        path = "/home/user/example-repo",
        phaseAt = NOW - 60,
    )

    private fun epicQuestion(): EpicQuestionRow = EpicQuestionRow(
        id = "question-1",
        text = "Pick a stack?",
        turn = 1L,
        asked = NOW - 60,
        choices = listOf(EpicQuestionChoiceRow(id = "alpha", does = "Use alpha")),
    )

    // The sheet's Decline is tapped through its click action, as the Ask's own sheet test taps it.
    private fun tapDecline() {
        composeRule.onNodeWithTag("ask-decline", useUnmergedTree = true)
            .performSemanticsAction(SemanticsActions.OnClick)
    }

    @Test
    fun declineIsOnlyInTheMoreSheetAndSendsItsQuestionFields() {
        FakeOrreryServer().use { server ->
            server.respond(DECLINE, DeclineResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onAllNodesWithTag("ask-decline").assertCountEquals(0)
            composeRule.onNodeWithTag("ask-more").performClick()
            composeRule.waitForIdle()
            tapDecline()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = DeclineRequest.parseFrom(server.requests.single().body)
            assertEquals("pane", sent.kind)
            assertEquals(PANE_ID, sent.id)
            assertEquals("", sent.requestId)
            assertEquals("abc123", sent.questionHash)
        }
    }

    @Test
    fun declineOnAHeadlessSessionSendsItsRequestFields() {
        FakeOrreryServer().use { server ->
            server.respond(DECLINE, DeclineResponse.getDefaultInstance())
            var refreshes = 0
            val sessionPane = pane().copy(id = SESSION_ID, headless = true)
            model.value = sessionAsk(
                sessionPane,
                SessionRequest(id = "req-1", kind = "question", question = "Pick a stack?", options = listOf("Alpha")),
            )
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-more").performClick()
            composeRule.waitForIdle()
            tapDecline()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = DeclineRequest.parseFrom(server.requests.single().body)
            assertEquals("headless", sent.kind)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("req-1", sent.requestId)
            assertEquals("", sent.questionHash)
        }
    }

    @Test
    fun refusedDeclineShowsItsReasonAndKeepsTheAsk() {
        FakeOrreryServer().use { server ->
            server.fail(DECLINE, Code.FAILED_PRECONDITION, "the turn already ended")
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-more").performClick()
            composeRule.waitForIdle()
            tapDecline()
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("ask-note").assertTextEquals("the turn already ended")
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-not-now").assertIsDisplayed()
            assertEquals(0, refreshes)
        }
    }

    @Test
    fun anEpicOrParkAskHasNoMoreAndNoDecline() {
        FakeOrreryServer().use { server ->
            setHost(server) {}

            model.value = epicAsk(epicRow(), epicQuestion())
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("ask").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-more").assertCountEquals(0)
            composeRule.onAllNodesWithTag("ask-decline").assertCountEquals(0)

            model.value = parkAsk(epicRow(), "Parked")
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("ask").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-more").assertCountEquals(0)
            composeRule.onAllNodesWithTag("ask-decline").assertCountEquals(0)
        }
    }

    @Test
    fun archiveThisSessionCallsEpicsArchiveForTheConversation() {
        FakeOrreryServer().use { server ->
            server.respond(ARCHIVE, EpicsArchiveResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane(orreryId = "orrery-session-1", changed = NOW - 2 * DAY))
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-stale").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-archive").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = EpicsArchiveRequest.parseFrom(server.requests.single().body)
            assertEquals("session", sent.kind)
            assertEquals("orrery-session-1", sent.id)
        }
    }

    @Test
    fun archiveThisSessionIsAbsentWithoutAnOrreryId() {
        FakeOrreryServer().use { server ->
            model.value = paneAsk(pane(changed = NOW - 2 * DAY))
            setHost(server) {}

            composeRule.onNodeWithTag("ask-stale").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-archive").assertCountEquals(0)
            assertEquals(0, server.requests.size)
        }
    }
}
