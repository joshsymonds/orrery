package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.EpicQuestionChoiceRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.BringBackRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.BringBackResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.NotNowRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.NotNowResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// Not now and Bring it back take the question off and put it back, each with the fields the server names the question by.
// A refusal leaves the Ask as it was and shows the server's reason. Checked through the fake server's requests and the
// node tree, against the test's virtual clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskNotNowTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val PANE_ID = "%3"
        const val SESSION_ID = "00000000000000000000000000000005"
        const val NOT_NOW = "/orrery.v1.Control/NotNow"
        const val BRING_BACK = "/orrery.v1.Control/BringBack"
        const val BASH_QUESTION = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val model = mutableStateOf<AskModel?>(null)

    private fun pane(putOff: Boolean = false): Pane = Pane(
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
        changed = NOW - 60,
        seen = 0L,
        question = BASH_QUESTION,
        questionHash = "abc123",
        putOff = putOff,
    )

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

    private fun setHost(server: FakeOrreryServer, onRefresh: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                AskHost(model = model.value, client = client, now = NOW, onRefresh = onRefresh)
            }
        }
    }

    @Test
    fun aPutOffPaneDrawsBringBackAndNoNotNowUntilItsPutOffClears() {
        FakeOrreryServer().use { server ->
            model.value = paneAsk(pane(putOff = true))
            setHost(server) {}

            composeRule.onNodeWithTag("ask-bring-back").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-not-now").assertCountEquals(0)

            model.value = paneAsk(pane())
            composeRule.waitForIdle()

            composeRule.onNodeWithTag("ask-not-now").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-bring-back").assertCountEquals(0)
        }
    }

    @Test
    fun anEpicOrParkAskDrawsNoNotNowOrBringBack() {
        FakeOrreryServer().use { server ->
            setHost(server) {}

            model.value = epicAsk(epicRow(), epicQuestion())
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("ask").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-not-now").assertCountEquals(0)
            composeRule.onAllNodesWithTag("ask-bring-back").assertCountEquals(0)

            model.value = parkAsk(epicRow(), "Parked")
            composeRule.waitForIdle()
            composeRule.onNodeWithTag("ask").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-not-now").assertCountEquals(0)
            composeRule.onAllNodesWithTag("ask-bring-back").assertCountEquals(0)
        }
    }

    @Test
    fun notNowOnAPaneSendsItsQuestionFieldsAndRefreshes() {
        FakeOrreryServer().use { server ->
            server.respond(NOT_NOW, NotNowResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-not-now").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = NotNowRequest.parseFrom(server.requests.single().body)
            assertEquals("pane", sent.kind)
            assertEquals(PANE_ID, sent.id)
            assertEquals("", sent.requestId)
            assertEquals("abc123", sent.questionHash)
            assertEquals(NOW - 60, sent.changed)
        }
    }

    @Test
    fun notNowOnAHeadlessSessionSendsItsRequestFieldsAndRefreshes() {
        FakeOrreryServer().use { server ->
            server.respond(NOT_NOW, NotNowResponse.getDefaultInstance())
            var refreshes = 0
            val sessionPane = pane().copy(id = SESSION_ID, headless = true)
            model.value = sessionAsk(
                sessionPane,
                SessionRequest(id = "req-1", kind = "question", question = "Pick a stack?", options = listOf("Alpha")),
            )
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-not-now").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = NotNowRequest.parseFrom(server.requests.single().body)
            assertEquals("headless", sent.kind)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("req-1", sent.requestId)
            assertEquals("", sent.questionHash)
            assertEquals(0L, sent.changed)
        }
    }

    @Test
    fun bringBackOnAPutOffPaneSendsItsQuestionFieldsAndRefreshes() {
        FakeOrreryServer().use { server ->
            server.respond(BRING_BACK, BringBackResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane(putOff = true))
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-bring-back").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = BringBackRequest.parseFrom(server.requests.single().body)
            assertEquals("pane", sent.kind)
            assertEquals(PANE_ID, sent.id)
            assertEquals("", sent.requestId)
            assertEquals("abc123", sent.questionHash)
            assertEquals(NOW - 60, sent.changed)
        }
    }

    @Test
    fun refusedNotNowShowsItsReasonAndKeepsTheAsk() {
        FakeOrreryServer().use { server ->
            server.fail(NOT_NOW, Code.FAILED_PRECONDITION, "the question moved on")
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-not-now").performClick()
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("ask-note").assertTextEquals("the question moved on")
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-not-now").assertIsDisplayed()
            assertEquals(0, refreshes)
        }
    }

    @Test
    fun refusedBringBackShowsItsReasonAndKeepsTheAsk() {
        FakeOrreryServer().use { server ->
            server.fail(BRING_BACK, Code.FAILED_PRECONDITION, "the question moved on")
            var refreshes = 0
            model.value = paneAsk(pane(putOff = true))
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-bring-back").performClick()
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("ask-note").assertTextEquals("the question moved on")
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-bring-back").assertIsDisplayed()
            assertEquals(0, refreshes)
        }
    }
}
