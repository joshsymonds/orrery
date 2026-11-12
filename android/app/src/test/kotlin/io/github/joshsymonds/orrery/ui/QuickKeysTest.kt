package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import io.github.joshsymonds.orrery.api.FakeOrreryServer
import io.github.joshsymonds.orrery.api.OrreryClient
import com.connectrpc.Code
import io.github.joshsymonds.orrery.gen.orrery.v1.Pane as PaneProto
import io.github.joshsymonds.orrery.gen.orrery.v1.PanesListResponse
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysRequest
import io.github.joshsymonds.orrery.gen.orrery.v1.SendKeysResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val PANE_ID = "%3"
private const val KEYS_PATH = "/orrery.v1.Panes/SendKeys"
private const val PANES_PATH = "/orrery.v1.Panes/List"
private const val NOT_SENT = "key not sent"
private const val NOTHING_CHANGED = "nothing changed"
private const val TIMEOUT_MILLIS = 5_000L
private const val FLASH_MILLIS = 300L
private const val WINDOW_MILLIS = 5_000L
private const val CHANGED_AT = 1_000L
private const val LATER = 2_000L

/** The results a window can show, none of them the send's "key not sent". */
private val RESULTS = listOf(NOTHING_CHANGED, "now done", "now working", "now waiting for you", "now ended")

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class QuickKeysTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val haptic = RecordingHaptic()

    private val labelToKey = listOf(
        "Esc" to "Escape",
        "Ctrl-C" to "C-c",
        "Enter" to "Enter",
        "←" to "Left",
        "↑" to "Up",
        "↓" to "Down",
        "→" to "Right",
    )

    @Test
    fun rowLabelsAreTheSevenKeysInOrder() {
        FakeOrreryServer().use { server ->
            setQuickKeys(server)
            val labels = composeRule.onAllNodes(hasClickAction()).fetchSemanticsNodes().map { node ->
                node.config[SemanticsProperties.Text].joinToString("") { it.text }
            }
            assertEquals(labelToKey.map { it.first }, labels)
        }
    }

    @Test
    fun eachButtonPostsItsKeyWithoutExpectQuestion() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            labelToKey.forEachIndexed { index, (label, key) ->
                composeRule.onNodeWithText(label).performScrollTo().performClick()
                composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == index + 1 }

                val request = server.requests[index]
                assertEquals("POST", request.method)
                assertEquals(KEYS_PATH, request.rawPath)
                val decoded = SendKeysRequest.parseFrom(request.body)
                assertEquals("keys for $label", listOf(key), decoded.keysList)
                assertFalse("expect_question for $label", decoded.hasExpectQuestion())
            }
        }
    }

    @Test
    fun showsKeyNotSentWhenServerFails() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)

            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }
            composeRule.onNodeWithText(NOT_SENT).assertIsDisplayed()
        }
    }

    @Test
    fun showsKeyNotSentWhenPromptChanged() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.ABORTED, "question refused")
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }
            composeRule.onNodeWithText(NOT_SENT).assertIsDisplayed()
        }
    }

    @Test
    fun tapRecordsOneLongPressHaptic() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == 1 }

            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun twoTapsRecordTwoHaptics() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == 1 }
            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == 2 }

            assertEquals(listOf(HapticFeedbackType.LongPress, HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun tappedKeyFlashesForThreeHundredMillisecondsAndNoOtherKey() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()

            assertTrue("Esc flashes right after the tap", flashing("Esc"))
            labelToKey.drop(1).forEach { (label, _) ->
                assertFalse("$label does not flash", flashing(label))
            }
            composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
            assertTrue("Esc still flashes at 299 ms", flashing("Esc"))
            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            assertFalse("Esc stops flashing at 300 ms", flashing("Esc"))
        }
    }

    @Test
    fun secondTapRestartsTheFlash() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.mainClock.advanceTimeBy(200, ignoreFrameDuration = true)
            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()

            composeRule.mainClock.advanceTimeBy(FLASH_MILLIS - 1, ignoreFrameDuration = true)
            assertTrue("second tap restarts the flash", flashing("Esc"))
            composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
            assertFalse("flash ends 300 ms after the second tap", flashing("Esc"))
        }
    }

    @Test
    fun failedSendStillRecordsHapticAndFlashes() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            setQuickKeys(server)
            composeRule.mainClock.autoAdvance = false

            composeRule.onNodeWithText("Enter").performScrollTo().performClick()
            composeRule.waitUntil(TIMEOUT_MILLIS) { server.requests.size == 1 }

            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
            assertTrue("Enter flashes although the send failed", flashing("Enter"))

            composeRule.mainClock.autoAdvance = true
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }
            assertEquals(listOf(HapticFeedbackType.LongPress), haptic.performed)
        }
    }

    @Test
    fun escSentThenStateMovingToDoneReadsNowDone() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.waitUntil(TIMEOUT_MILLIS) { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.state = "done" }
            composeRule.waitUntilExactlyOneExists(hasText("now done"), TIMEOUT_MILLIS)
            assertEquals("the change shows the result without a read", 0, panesRequests(server))
        }
    }

    @Test
    fun aChangedTimeInsideTheWindowReadsNowWorking() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.waitUntil(TIMEOUT_MILLIS) { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.changed = LATER }
            composeRule.waitUntilExactlyOneExists(hasText("now working"), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun emptyStateInsideTheWindowReadsNowEnded() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Ctrl-C").performClick()
            composeRule.waitForIdle()
            composeRule.waitUntil(TIMEOUT_MILLIS) { keyRequests(server) == 1 }

            composeRule.runOnIdle { shown.state = "" }
            composeRule.waitUntilExactlyOneExists(hasText("now ended"), TIMEOUT_MILLIS)
        }
    }

    @Test
    fun failedSendShowsKeyNotSentAndNoWindow() {
        FakeOrreryServer().use { server ->
            server.fail(KEYS_PATH, Code.INTERNAL_ERROR, "boom")
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            composeRule.onNodeWithText("Esc").performClick()
            composeRule.waitForIdle()
            composeRule.waitUntil(TIMEOUT_MILLIS) { notSentShown() }

            composeRule.mainClock.autoAdvance = false
            composeRule.runOnIdle { shown.state = "done" }
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS, ignoreFrameDuration = true)
            composeRule.waitForIdle()
            assertEquals("a failed send opens no window, so no read", 0, panesRequests(server))
            assertNoResultShown("a failed send")
            assertTrue("the failure stays shown", notSentShown())
        }
    }

    @Test
    fun otherKeysNeverShowAResult() {
        FakeOrreryServer().use { server ->
            server.respond(KEYS_PATH, SendKeysResponse.getDefaultInstance())
            servePane(server, state = "working", changed = CHANGED_AT)
            val shown = ShownPane()
            setQuickKeys(server, shown)

            listOf("Enter", "←", "↑", "↓", "→").forEachIndexed { index, label ->
                composeRule.onNodeWithText(label).performScrollTo().performClick()
                composeRule.waitForIdle()
                composeRule.waitUntil(TIMEOUT_MILLIS) { keyRequests(server) == index + 1 }
            }
            composeRule.runOnIdle { shown.state = "done" }
            composeRule.mainClock.autoAdvance = false
            composeRule.mainClock.advanceTimeBy(WINDOW_MILLIS * 2, ignoreFrameDuration = true)
            composeRule.waitForIdle()

            assertEquals("no key opens a window", 0, panesRequests(server))
            assertNoResultShown("the other keys")
        }
    }

    private fun servePane(server: FakeOrreryServer, state: String, changed: Long) {
        server.respond(
            PANES_PATH,
            PanesListResponse.newBuilder()
                .addPanes(PaneProto.newBuilder().setId(PANE_ID).setState(state).setChanged(changed))
                .build(),
        )
    }

    private fun keyRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == KEYS_PATH }

    private fun panesRequests(server: FakeOrreryServer): Int =
        server.requests.count { it.method == "POST" && it.rawPath == PANES_PATH }

    private fun notSentShown(): Boolean =
        composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()

    private fun resultShown(text: String): Boolean =
        composeRule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty()

    private fun assertNoResultShown(context: String) {
        RESULTS.forEach { result ->
            assertFalse("$context shows no \"$result\"", resultShown(result))
        }
    }

    private fun flashing(label: String): Boolean =
        composeRule.onNode(hasText(label) and hasClickAction()).fetchSemanticsNode()
            .config.getOrElse(Flashing) { false }

    private fun setQuickKeys(server: FakeOrreryServer, shown: ShownPane = ShownPane()) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalHapticFeedback provides haptic) {
                QuickKeys(paneId = PANE_ID, client = client, state = shown.state, changed = shown.changed)
            }
        }
    }

    /** The pane's state and changed time as the screen shows them; a test moves them. */
    private class ShownPane {
        var state by mutableStateOf("working")
        var changed by mutableLongStateOf(CHANGED_AT)
    }

    private class RecordingHaptic : HapticFeedback {
        val performed = mutableListOf<HapticFeedbackType>()

        override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) {
            performed += hapticFeedbackType
        }
    }
}
