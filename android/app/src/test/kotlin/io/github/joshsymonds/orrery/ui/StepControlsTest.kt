package io.github.joshsymonds.orrery.ui

import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StepsNudgeResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
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
private const val NUDGE_PATH = "/orrery.v1.Steps/Nudge"
private const val NUDGE_LANDED = "nudge landed"
private const val UNREACHABLE = "could not reach the step"
private const val REFUSAL_REASON = "synthetic refusal"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class StepControlsTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val server = FakeOrreryServer()

    @After
    fun tearDown() {
        server.close()
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
    fun droppedNudgeRethrowsAsIOExceptionAndShowsCouldNotReachTheStep() {
        server.drop(NUDGE_PATH)
        val calls = StepCalls(OrreryClient(server.baseUrl), STEP_ID)

        val thrown = assertThrows(IOException::class.java) { calls.nudge(NUDGE_TEXT) }

        assertTrue(thrown !is NudgeRefusedException)
        assertEquals(UNREACHABLE, calls.outcome)
    }
}
