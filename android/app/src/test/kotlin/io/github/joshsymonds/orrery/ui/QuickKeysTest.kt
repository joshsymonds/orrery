package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.SemanticsProperties
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
private const val NOT_SENT = "key not sent"
private const val TIMEOUT_MILLIS = 5_000L
private const val FLASH_MILLIS = 300L

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

    private fun notSentShown(): Boolean =
        composeRule.onAllNodesWithText(NOT_SENT).fetchSemanticsNodes().isNotEmpty()

    private fun flashing(label: String): Boolean =
        composeRule.onNode(hasText(label) and hasClickAction()).fetchSemanticsNode()
            .config.getOrElse(Flashing) { false }

    private fun setQuickKeys(server: FakeOrreryServer) {
        val client = OrreryClient(server.baseUrl)
        composeRule.setContent {
            CompositionLocalProvider(LocalHapticFeedback provides haptic) {
                QuickKeys(paneId = PANE_ID, client = client)
            }
        }
    }

    private class RecordingHaptic : HapticFeedback {
        val performed = mutableListOf<HapticFeedbackType>()

        override fun performHapticFeedback(hapticFeedbackType: HapticFeedbackType) {
            performed += hapticFeedbackType
        }
    }
}
