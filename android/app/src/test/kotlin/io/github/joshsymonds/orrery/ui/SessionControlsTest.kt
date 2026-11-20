package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.RecordedRequest
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.awaitUntilAfterIdle
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.AnswerResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.InterruptResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.ResumeResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.StopRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.StopResponse
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import java.util.concurrent.atomic.AtomicInteger
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
private const val RESUME_PATH = "/orrery.v1.Control/Resume"
private const val NOT_ANSWERABLE = "not answerable"
private const val STOPPED = "stopped"
private const val NOT_SENT = "not sent"
private const val REFUSAL = "synthetic refusal reason"
private const val REQUEST_TAG = "session-request"
private const val INTERRUPT_TAG = "session-interrupt"
private const val STOP_TAG = "session-stop"
private const val CONFIRM_TAG = "session-stop-confirm"
private const val RESUME_TAG = "session-resume"
private const val FLASH_MILLIS = 300L

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class SessionControlsTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val server = FakeOrreryServer()

    private val haptic = RecordingHaptic()

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
        composeRule.awaitUntil { answered.get() == 1 }

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
        composeRule.awaitUntil {
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
        composeRule.awaitUntil {
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
        assertFalse(shown(RESUME_TAG))
    }

    @Test
    fun waitingShowsInterruptAndStop() {
        setActions("waiting")

        composeRule.onNodeWithTag(INTERRUPT_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(STOP_TAG).assertIsDisplayed()
        assertFalse(shown(RESUME_TAG))
    }

    @Test
    fun inLineButtonsDrawTwentyFourHigh() {
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).assertHeightIsEqualTo(OrreryTokens.Size.gauge)
        composeRule.onNodeWithTag(STOP_TAG).assertHeightIsEqualTo(OrreryTokens.Size.gauge)
    }

    @Test
    fun interruptedShowsResumeOnly() {
        setActions("interrupted")

        composeRule.onNodeWithTag(RESUME_TAG).assertIsDisplayed()
        assertFalse(shown(INTERRUPT_TAG))
        assertFalse(shown(STOP_TAG))
    }

    @Test
    fun resumePostsResumeThenCallsBack() {
        server.respond(RESUME_PATH, ResumeResponse.getDefaultInstance())
        val changed = AtomicInteger()
        val taken = mutableListOf<LiveCall>()
        setActions("interrupted", onTaken = { taken += it }) { changed.incrementAndGet() }

        composeRule.onNodeWithTag(RESUME_TAG).performClick()
        composeRule.awaitUntil { changed.get() == 1 }

        assertEquals(1, server.requests.size)
        val request = server.requests.single()
        assertEquals("POST", request.method)
        assertEquals(RESUME_PATH, request.rawPath)
        assertEquals(
            ResumeRequest.newBuilder().setId(SESSION_ID).build(),
            ResumeRequest.parseFrom(request.body),
        )
        assertEquals(listOf(LiveCall.RESUME), taken)
        assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
    }

    @Test
    fun resumeRefusalShowsTheServerReason() {
        server.fail(RESUME_PATH, Code.FAILED_PRECONDITION, REFUSAL)
        val changed = AtomicInteger()
        setActions("interrupted") { changed.incrementAndGet() }

        composeRule.onNodeWithTag(RESUME_TAG).performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(REFUSAL).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(0, changed.get())
        assertTrue(shown(RESUME_TAG))
        assertTrue(composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun resumeOtherFailureShowsNotSent() {
        server.fail(RESUME_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        val changed = AtomicInteger()
        setActions("interrupted") { changed.incrementAndGet() }

        composeRule.onNodeWithTag(RESUME_TAG).performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
        assertEquals(0, changed.get())
    }

    @Test
    fun interruptPostsInterruptThenCallsBack() {
        server.respond(INTERRUPT_PATH, InterruptResponse.getDefaultInstance())
        val changed = AtomicInteger()
        setActions("working") { changed.incrementAndGet() }

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntil { changed.get() == 1 }

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
        composeRule.awaitUntil { changed.get() == 1 }

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
    fun otherStatesShowNoControls() {
        val state = mutableStateOf("done")
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme { SessionActions(SESSION_ID, state.value, client, {}) }
        }
        for (other in listOf("done", "stopped", "failed", "")) {
            composeRule.runOnIdle { state.value = other }
            composeRule.waitForIdle()

            assertTrue(composeRule.onAllNodesWithTag(INTERRUPT_TAG).fetchSemanticsNodes().isEmpty())
            assertTrue(composeRule.onAllNodesWithTag(STOP_TAG).fetchSemanticsNodes().isEmpty())
            assertTrue(composeRule.onAllNodesWithTag(RESUME_TAG).fetchSemanticsNodes().isEmpty())
        }
    }

    @Test
    fun interruptNotFoundShowsStopped() {
        server.fail(INTERRUPT_PATH, Code.NOT_FOUND, "synthetic not live")
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntil {
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
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(STOPPED).fetchSemanticsNodes().isNotEmpty()
        }

        assertEquals(1, server.requests.size)
    }

    @Test
    fun interruptOtherFailureShowsNotSent() {
        server.fail(INTERRUPT_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        setActions("working")

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }

        assertTrue(composeRule.onAllNodesWithText(STOPPED).fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun interruptTapRecordsOneLongPressHapticAndFlashes() {
        server.respond(INTERRUPT_PATH, InterruptResponse.getDefaultInstance())
        setActions("working")
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        assertTrue("Interrupt flashes right after the tap", flashing(INTERRUPT_TAG))
        composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
        assertTrue("Interrupt still flashes at 299 ms", flashing(INTERRUPT_TAG))
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertFalse("Interrupt stops flashing at 300 ms", flashing(INTERRUPT_TAG))
    }

    @Test
    fun interruptFailureStillRecordsHapticAndFlashes() {
        server.fail(INTERRUPT_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        setActions("working")
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(INTERRUPT_TAG).performClick()
        composeRule.awaitUntilAfterIdle { server.requests.size == 1 }

        assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        assertTrue("Interrupt flashes although the call failed", flashing(INTERRUPT_TAG))

        composeRule.mainClock.autoAdvance = true
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }
        assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
    }

    @Test
    fun stopTapRecordsOneLongPressHapticAndFlashes() {
        setActions("waiting")
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        assertTrue("Stop flashes right after the tap", flashing(STOP_TAG))
        composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
        assertTrue("Stop still flashes at 299 ms", flashing(STOP_TAG))
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertFalse("Stop stops flashing at 300 ms", flashing(STOP_TAG))
        assertTrue(server.requests.isEmpty())
    }

    @Test
    fun confirmStopTapRecordsOneLongPressHapticAndFlashes() {
        server.respond(STOP_PATH, StopResponse.getDefaultInstance())
        setActions("waiting")
        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.waitForIdle()

        assertEquals(listOf(HapticFeedbackType.LongPress, HapticFeedbackType.LongPress), haptic.performed)
        assertTrue("Confirm stop flashes right after the tap", flashing(CONFIRM_TAG))
        composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
        assertTrue("Confirm stop still flashes at 299 ms", flashing(CONFIRM_TAG))
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertFalse("Confirm stop stops flashing at 300 ms", shown(CONFIRM_TAG) && flashing(CONFIRM_TAG))
        composeRule.mainClock.advanceTimeByFrame()
        assertFalse("Confirm stop leaves the screen once its flash has ended", shown(CONFIRM_TAG))
    }

    @Test
    fun confirmStopFailureStillRecordsHapticAndFlashes() {
        server.fail(STOP_PATH, Code.INTERNAL_ERROR, "synthetic failure")
        setActions("waiting")
        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.awaitUntilAfterIdle { server.requests.size == 1 }

        assertEquals(listOf(HapticFeedbackType.LongPress, HapticFeedbackType.LongPress), haptic.performed)
        assertTrue("Confirm stop flashes although the call failed", flashing(CONFIRM_TAG))

        composeRule.mainClock.autoAdvance = true
        composeRule.awaitUntil {
            composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()
        }
        assertEquals(listOf(HapticFeedbackType.LongPress, HapticFeedbackType.LongPress), haptic.performed)
    }

    @Test
    fun confirmStopTappedAgainWhileFlashingPostsNothingMore() {
        server.respond(STOP_PATH, StopResponse.getDefaultInstance())
        setActions("waiting")
        composeRule.onNodeWithTag(STOP_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.mainClock.autoAdvance = false

        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.awaitUntilAfterIdle { server.requests.size == 1 }
        composeRule.onNodeWithTag(CONFIRM_TAG).performClick()
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeBy(FLASH_MILLIS, ignoreFrameDuration = true)
        composeRule.waitForIdle()

        assertEquals(1, server.requests.size)
        assertEquals(3, haptic.performed.size)
    }

    private fun flashing(tag: String): Boolean =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.getOrElse(Flashing) { false }

    private fun shown(tag: String): Boolean =
        composeRule.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty()

    private fun setCard(options: List<String>, onAnswered: () -> Unit) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            MaterialTheme {
                SessionRequestCard(SESSION_ID, REQUEST_ID, QUESTION, options, client, onAnswered)
            }
        }
    }

    private fun setActions(state: String, onTaken: (LiveCall) -> Unit = {}, onChanged: () -> Unit = {}) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalHapticFeedback provides haptic) {
                MaterialTheme {
                    SessionActions(SESSION_ID, state, client, onChanged, onTaken)
                }
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

    private class RecordingHaptic : HapticFeedback {
        val performed = mutableListOf<HapticFeedbackType>()

        override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) {
            performed += hapticFeedbackType
        }
    }
}
