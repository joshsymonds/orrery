package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsHoldResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsStopResponse
import java.io.IOException
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val STEP_ID = "synthetic-step-1"
private const val NUDGE_TEXT = "synthetic nudge text"
private const val HOLD_PATH = "/orrery.v1.Steps/Hold"
private const val NUDGE_PATH = "/orrery.v1.Steps/Nudge"
private const val STOP_PATH = "/orrery.v1.Steps/Stop"
private const val HOLD_TAG = "step-hold"
private const val STOP_TAG = "step-stop"
private const val CONFIRM_TAG = "step-stop-confirm"
private const val HOLD_LANDED = "hold landed"
private const val NUDGE_LANDED = "nudge landed"
private const val STOPPED = "stopped"
private const val UNREACHABLE = "could not reach the step"
private const val REFUSAL_REASON = "synthetic refusal"
private const val WAIT_MS = 5_000L

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class StepControlsTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
    }

    @Test
    fun holdPostsOnceAndShowsHoldLanded() {
        server.respond(HOLD_PATH, StepsHoldResponse.getDefaultInstance())
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)
        setActions(calls, held = false)

        composeRule.onNodeWithTag(HOLD_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { calls.outcome == HOLD_LANDED }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(HOLD_LANDED).assertIsDisplayed()
        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(HOLD_PATH, request.rawPath)
        assertEquals(StepsHoldRequest.newBuilder().setId(STEP_ID).build(), StepsHoldRequest.parseFrom(request.body))
    }

    @Test
    fun heldStepShowsNoHoldButtonButKeepsStop() {
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)
        setActions(calls, held = true)

        composeRule.waitForIdle()
        assertTrue(composeRule.onAllNodesWithTag(HOLD_TAG).fetchSemanticsNodes().isEmpty())
        composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
    }

    @Test
    fun stopPostsNothingUntilConfirmThenPostsOnceAndShowsStopped() {
        server.respond(STOP_PATH, StepsStopResponse.getDefaultInstance())
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)
        setActions(calls, held = false)

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.onNodeWithTag(CONFIRM_TAG).assertIsDisplayed()
        assertTrue(server.requests.isEmpty())

        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { calls.outcome == STOPPED }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(STOPPED).assertIsDisplayed()
        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(STOP_PATH, request.rawPath)
        assertEquals(StepsStopRequest.newBuilder().setId(STEP_ID).build(), StepsStopRequest.parseFrom(request.body))
    }

    @Test
    fun refusedHoldShowsTheRefusalReason() {
        server.fail(HOLD_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)
        setActions(calls, held = false)

        composeRule.onNodeWithTag(HOLD_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { calls.outcome == "refused: $REFUSAL_REASON" }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("refused: $REFUSAL_REASON").assertIsDisplayed()
    }

    @Test
    fun nudgePostsTheTextAndShowsNudgeLanded() {
        server.respond(NUDGE_PATH, StepsNudgeResponse.getDefaultInstance())
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)

        calls.nudge(NUDGE_TEXT)

        assertEquals(NUDGE_LANDED, calls.outcome)
        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(NUDGE_PATH, request.rawPath)
        val sent = StepsNudgeRequest.parseFrom(request.body)
        assertEquals(STEP_ID, sent.id)
        assertEquals(NUDGE_TEXT, sent.text)
    }

    @Test
    fun refusedNudgeThrowsAndShowsTheRefusal() {
        server.fail(NUDGE_PATH, Code.FAILED_PRECONDITION, REFUSAL_REASON)
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)

        val thrown = assertThrows(NudgeRefusedException::class.java) { calls.nudge(NUDGE_TEXT) }

        assertEquals(REFUSAL_REASON, thrown.message)
        assertEquals("refused: $REFUSAL_REASON", calls.outcome)
        assertEquals(1, server.requests.size)
    }

    @Test
    fun droppedHoldShowsCouldNotReachTheStep() {
        server.drop(HOLD_PATH)
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)
        setActions(calls, held = false)

        composeRule.onNodeWithTag(HOLD_TAG).performClick()
        composeRule.waitUntil(WAIT_MS) { calls.outcome == UNREACHABLE }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(UNREACHABLE).assertIsDisplayed()
    }

    @Test
    fun droppedNudgeRethrowsAsIOExceptionAndShowsCouldNotReachTheStep() {
        server.drop(NUDGE_PATH)
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)

        val thrown = assertThrows(IOException::class.java) { calls.nudge(NUDGE_TEXT) }

        assertTrue(thrown !is NudgeRefusedException)
        assertEquals(UNREACHABLE, calls.outcome)
    }

    private fun setActions(calls: StepCalls, held: Boolean) {
        composeRule.setContent {
            MaterialTheme {
                StepActions(calls, held)
            }
        }
    }
}
