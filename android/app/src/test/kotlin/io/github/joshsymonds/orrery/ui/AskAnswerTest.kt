package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.Reply
import io.github.joshsymonds.orrery.api.SessionRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A tap on the Ask answers once: a pane's digit goes with its question hash, a headless session's label with its request
// id. The answered line folds in only once the model it answered is gone. Checked through the fake server's requests and
// the node tree; the in-flight test holds the server's reply on signals, never on the clock.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h1200dp")
class AskAnswerTest {
    private companion object {
        const val NOW = 1_700_000_000L
        const val PANE_ID = "%3"
        const val SESSION_ID = "00000000000000000000000000000005"
        const val KEYS = "/orrery.v1.Panes/SendKeys"
        const val CONTROL_ANSWER = "/orrery.v1.Control/Answer"
        const val HANG_GUARD_SECONDS = 30L
        const val REFUSED_NOTE =
            "Not answered: the question changed before your answer arrived. This is what it asks now."
        const val BASH_QUESTION = """{"tool":"Bash","input":{"command":"echo synthetic"}}"""
    }

    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val model = mutableStateOf<AskModel?>(null)

    private fun pane(question: String = BASH_QUESTION): Pane = Pane(
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
        question = question,
        questionHash = "abc123",
    )

    private fun sessionAskModel(): AskModel {
        val sessionPane = pane().copy(id = SESSION_ID, headless = true)
        val request = SessionRequest(
            id = "req-1",
            kind = "question",
            question = "Pick a stack?",
            options = listOf("Alpha", "Beta"),
        )
        return sessionAsk(sessionPane, request)
    }

    private fun setHost(server: FakeOrreryServer, onRefresh: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                AskHost(model = model.value, client = client, now = NOW, onRefresh = onRefresh)
            }
        }
    }

    @Test
    fun paneOptionTapSendsItsDigitWithTheQuestionHash() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS, SendKeysResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = SendKeysRequest.parseFrom(server.requests.single().body)
            assertEquals(PANE_ID, sent.pane)
            assertEquals(listOf("2"), sent.keysList.toList())
            assertEquals("abc123", sent.expectQuestion)
        }
    }

    @Test
    fun sessionOptionTapSendsItsLabelWithTheRequestId() {
        FakeOrreryServer().use { server ->
            server.respond(CONTROL_ANSWER, AnswerResponse.getDefaultInstance())
            var refreshes = 0
            model.value = sessionAskModel()
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            val sent = AnswerRequest.parseFrom(server.requests.single().body)
            assertEquals(SESSION_ID, sent.id)
            assertEquals("req-1", sent.requestId)
            assertEquals("Beta", sent.answer)
        }
    }

    @Test
    fun aTapWhileTheAnswerIsInFlightSendsNothing() {
        FakeOrreryServer().use { server ->
            val arrived = CountDownLatch(1)
            val release = CountDownLatch(1)
            server.route(KEYS) {
                arrived.countDown()
                check(release.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)) { "the test never released the answer" }
                Reply.Message(SendKeysResponse.getDefaultInstance(), 0L)
            }
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:1").performClick()
            composeRule.waitForIdle()
            assertTrue(arrived.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS))
            composeRule.onNodeWithTag("ask-option:2").performClick()
            release.countDown()
            composeRule.awaitUntil { refreshes == 1 }

            assertEquals(1, server.requests.size)
        }
    }

    @Test
    fun refusedPaneAnswerLeavesTheAskWithItsNote() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS, Code.ABORTED, "question refused")
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:1").performClick()
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("ask-note").assertTextEquals(REFUSED_NOTE)
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-option:1").assertIsDisplayed()
            assertEquals(0, refreshes)
        }
    }

    @Test
    fun failedSessionAnswerLeavesTheAskWithItsNote() {
        FakeOrreryServer().use { server ->
            server.fail(CONTROL_ANSWER, Code.INTERNAL_ERROR, "boom")
            var refreshes = 0
            model.value = sessionAskModel()
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:1").performClick()
            composeRule.awaitUntil {
                composeRule.onAllNodesWithTag("ask-note").fetchSemanticsNodes().isNotEmpty()
            }

            composeRule.onNodeWithTag("ask-note").assertTextEquals(REFUSED_NOTE)
            composeRule.onNodeWithTag("ask-question").assertIsDisplayed()
            assertEquals(0, refreshes)
        }
    }

    @Test
    fun theAnsweredLineShowsOnlyOnceTheModelIsGone() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS, SendKeysResponse.getDefaultInstance())
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.awaitUntil { refreshes == 1 }

            composeRule.onNodeWithTag("ask-sending").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask-answered").assertCountEquals(0)

            composeRule.runOnIdle { model.value = null }

            composeRule.onNodeWithTag("ask-answered").assertIsDisplayed()
            composeRule.onNodeWithText("Yes, and don’t ask again").assertIsDisplayed()
            composeRule.onNodeWithText("· Run this command?").assertIsDisplayed()
            composeRule.onAllNodesWithTag("ask").assertCountEquals(0)
        }
    }

    @Test
    fun aPaneAnswerHeldByTheServerDrawsItsPickSending() {
        FakeOrreryServer().use { server ->
            val arrived = CountDownLatch(1)
            val release = CountDownLatch(1)
            server.route(KEYS) {
                arrived.countDown()
                check(release.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)) { "the test never released the answer" }
                Reply.Message(SendKeysResponse.getDefaultInstance(), 0L)
            }
            var refreshes = 0
            model.value = paneAsk(pane())
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.waitForIdle()
            assertTrue(arrived.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS))

            composeRule.onNodeWithTag("ask-sending").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-option:2").assertIsSelected()
            release.countDown()
            composeRule.awaitUntil { refreshes == 1 }
        }
    }

    @Test
    fun aSessionAnswerHeldByTheServerDrawsItsPickSending() {
        FakeOrreryServer().use { server ->
            val arrived = CountDownLatch(1)
            val release = CountDownLatch(1)
            server.route(CONTROL_ANSWER) {
                arrived.countDown()
                check(release.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)) { "the test never released the answer" }
                Reply.Message(AnswerResponse.getDefaultInstance(), 0L)
            }
            var refreshes = 0
            model.value = sessionAskModel()
            setHost(server) { refreshes++ }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.waitForIdle()
            assertTrue(arrived.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS))

            composeRule.onNodeWithTag("ask-sending").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-option:2").assertIsSelected()
            release.countDown()
            composeRule.awaitUntil { refreshes == 1 }
        }
    }

    @Test
    fun aPaneAnswerHeldByTheServerSurvivesTheAskBeingDrawnAgain() {
        FakeOrreryServer().use { server ->
            val arrived = CountDownLatch(1)
            val release = CountDownLatch(1)
            server.route(KEYS) {
                arrived.countDown()
                check(release.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS)) { "the test never released the answer" }
                Reply.Message(SendKeysResponse.getDefaultInstance(), 0L)
            }
            var refreshes = 0
            val drawn = mutableStateOf(true)
            val client = OrreryClient(server.baseUrl)
            model.value = paneAsk(pane())
            composeRule.setContent {
                MaterialTheme {
                    // The screen's life outlives the Ask's composition, as the footer item's does.
                    val life = rememberAskLife(PANE_ID)
                    if (drawn.value) {
                        AskHost(
                            model = model.value,
                            client = client,
                            now = NOW,
                            onRefresh = { refreshes++ },
                            life = life,
                        )
                    }
                }
            }

            composeRule.onNodeWithTag("ask-option:2").performClick()
            composeRule.waitForIdle()
            assertTrue(arrived.await(HANG_GUARD_SECONDS, TimeUnit.SECONDS))

            // A footer item scrolled off and back again: the Ask leaves the composition and is drawn anew.
            composeRule.runOnIdle { drawn.value = false }
            composeRule.runOnIdle { drawn.value = true }

            composeRule.onNodeWithTag("ask-sending").assertIsDisplayed()
            composeRule.onNodeWithTag("ask-option:1").performClick()
            release.countDown()
            composeRule.awaitUntil { refreshes == 1 }
            assertEquals(1, server.requests.size)

            composeRule.runOnIdle { model.value = null }
            composeRule.onNodeWithTag("ask-answered").assertIsDisplayed()
        }
    }
}
