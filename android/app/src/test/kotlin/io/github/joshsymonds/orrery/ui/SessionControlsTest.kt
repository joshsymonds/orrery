package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StopResponse
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val SESSION_ID = "0123456789abcdef0123456789abcdef"
private const val REQUEST_ID = "synthetic-request"
private const val QUESTION = "Pick a synthetic option?"
private const val OPTION_ONE = "Synthetic one"
private const val OPTION_TWO = "Synthetic two"
private const val ANSWER_PATH = "/orrery.v1.Control/Answer"
private const val INTERRUPT_PATH = "/orrery.v1.Control/Interrupt"
private const val STOP_PATH = "/orrery.v1.Control/Stop"
private const val WAIT_MS = 5_000L
private const val NOT_ANSWERABLE = "not answerable"
private const val STOPPED = "stopped"
private const val NOT_SENT = "not sent"
private const val REQUEST_TAG = "session-request"
private const val INTERRUPT_TAG = "session-interrupt"
private const val STOP_TAG = "session-stop"
private const val CONFIRM_TAG = "session-stop-confirm"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SessionControlsTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun tappingAnOptionPostsItsAnswerThenCallsBack() {
        server.respond(ANSWER_PATH, AnswerResponse.getDefaultInstance())
        val answered = AtomicInteger()
        setCard(listOf(OPTION_ONE, OPTION_TWO)) { answered.incrementAndGet() }

        composeRule.onNodeWithTag(REQUEST_TAG).assertIsDisplayed()
        composeRule.onNodeWithText(QUESTION).assertIsDisplayed()
        composeRule.onNodeWithTag("session-option:$OPTION_TWO").performClick()
        composeRule.waitUntil(WAIT_MS) { answered.get() == 1 }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertAnswerRequest(request, OPTION_TWO)
        assertEquals(1, answered.get())
    }

    @Test
    fun emptyOptionsShowNotAnswerableAndPostNothing() {
        setCard(emptyList()) {}

        composeRule.onNodeWithText(NOT_ANSWERABLE).assertIsDisplayed()
        composeRule.waitForIdle()
        assertTrue(composeRule.onAllNodesWithTag("session-option:$OPTION_ONE").fetchSemanticsNodes().isEmpty())
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun invalidArgumentAnswerShowsNotAnswerableAndDoesNotCallBack() {
        server.fail(ANSWER_PATH, Code.INVALID_ARGUMENT, "synthetic refusal")
        val answered = AtomicInteger()
        setCard(listOf(OPTION_ONE)) { answered.incrementAndGet() }

        composeRule.onNodeWithTag("session-option:$OPTION_ONE").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NOT_ANSWERABLE).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(0, answered.get())
    }

    @Test
    fun otherAnswerFailureShowsNotSent() {
        server.fail(ANSWER_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        val answered = AtomicInteger()
        setCard(listOf(OPTION_ONE)) { answered.incrementAndGet() }

        composeRule.onNodeWithTag("session-option:$OPTION_ONE").performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(0, answered.get())
        assertTrue(composeRule.onAllNodesWithText(NOT_ANSWERABLE).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun workingShowsInterruptAndStop() {
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
    }

    @Test
    fun waitingShowsInterruptAndStop() {
        setActions("waiting")

        composeRule.onNodeWithTag(INTERRUPT_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
    }

    @Test
    fun interruptPostsInterruptThenCallsBack() {
        server.respond(INTERRUPT_PATH, InterruptResponse.getDefaultInstance())
        val changed = AtomicInteger()
        setActions("working") { changed.incrementAndGet() }

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { changed.get() == 1 }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(INTERRUPT_PATH, request.rawPath)
        assertEquals(
            InterruptRequest.newBuilder().setId(SESSION_ID).build(),
            InterruptRequest.parseFrom(request.body),
        )
    }

    @Test
    fun stopAsksForConfirmationBeforePosting() {
        server.respond(STOP_PATH, StopResponse.getDefaultInstance())
        val changed = AtomicInteger()
        setActions("waiting") { changed.incrementAndGet() }

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsDisplayed()
        assertTrue(server.requests.isEmpty())

        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { changed.get() == 1 }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(STOP_PATH, request.rawPath)
        assertEquals(
            StopRequest.newBuilder().setId(SESSION_ID).build(),
            StopRequest.parseFrom(request.body),
        )
    }

    @Test
    fun otherStatesShowNeitherInterruptNorStop() {
        val state = mutableStateOf("done")
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionActions(SESSION_ID, state.value, client, {}) }
        }
        for (other in listOf("done", "stopped", "failed", "interrupted")) {
            composeRule.runOnIdle { state.value = other }
            composeRule.waitForIdle()

            assertTrue(composeRule.onAllNodesWithTag(INTERRUPT_TAG).fetchSemanticsNodes().isEmpty())
            assertTrue(composeRule.onAllNodesWithTag(STOP_TAG).fetchSemanticsNodes().isEmpty())
        }
    }

    @Test
    fun interruptNotFoundShowsStopped() {
        server.fail(INTERRUPT_PATH, Code.NOT_FOUND, "synthetic not live")
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(STOPPED).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
    }

    @Test
    fun stopNotFoundShowsStopped() {
        server.fail(STOP_PATH, Code.NOT_FOUND, "synthetic not live")
        setActions("waiting")

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(STOPPED).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
    }

    @Test
    fun interruptOtherFailureShowsNotSent() {
        server.fail(INTERRUPT_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }

        assertTrue(composeRule.onAllNodesWithText(STOPPED).fetchSemanticsNodes().isEmpty())
    }

    private fun setCard(options: List<String>, onAnswered: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionRequestCard(SESSION_ID, REQUEST_ID, QUESTION, options, client, onAnswered)
            }
        }
    }

    private fun setActions(state: String, onChanged: () -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionActions(SESSION_ID, state, client, onChanged)
            }
        }
    }

    private fun assertAnswerRequest(request: RecordedRequest, label: String) {
        assertEquals("POST", request.method)
        assertEquals(ANSWER_PATH, request.rawPath)
        assertEquals(
            AnswerRequest.newBuilder().setId(SESSION_ID).setRequestId(REQUEST_ID).setAnswer(label).build(),
            AnswerRequest.parseFrom(request.body),
        )
    }
}
