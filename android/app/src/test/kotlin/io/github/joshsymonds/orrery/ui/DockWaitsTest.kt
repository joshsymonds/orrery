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
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.PendingItem
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val WAITS_TAG = "dock-waits"
private const val STATE_LINE_TAG = "dock-state"
private const val SPEAK_ONE = "1 event waits until you speak"
private const val SPEAK_TWO = "2 events wait until you speak"
private const val REQUEST_ONE = "1 event waits on the question you put off"
private const val REPLY = "a reply"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockWaitsTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val sent = CopyOnWriteArrayList<String>()

    @Test
    fun speakEventsDrawTheSpeakLine() {
        setDock(pending = listOf(event("speak")))

        composeRule.onNodeWithTag(WAITS_TAG).assertTextEquals(SPEAK_ONE)
    }

    @Test
    fun severalSpeakEventsCountInThePlural() {
        setDock(pending = listOf(event("speak"), event("speak")))

        composeRule.onNodeWithTag(WAITS_TAG).assertTextEquals(SPEAK_TWO)
    }

    @Test
    fun putOffRequestDrawsTheQuestionLine() {
        setDock(pending = listOf(event("request")), putOff = true)

        composeRule.onNodeWithTag(WAITS_TAG).assertTextEquals(REQUEST_ONE)
    }

    @Test
    fun requestNotPutOffDrawsNoLine() {
        setDock(pending = listOf(event("request")), putOff = false)

        composeRule.onNodeWithTag(STATE_LINE_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(WAITS_TAG).assertCountEquals(0)
    }

    @Test
    fun freeEventDrawsNoLine() {
        setDock(pending = listOf(event("free")), putOff = true)

        composeRule.onNodeWithTag(STATE_LINE_TAG).assertIsDisplayed()
        composeRule.onAllNodesWithTag(WAITS_TAG).assertCountEquals(0)
    }

    @Test
    fun lineGoesAfterASendAndReturnsWhenThePendingEventsChange() {
        var events by mutableStateOf(listOf(event("speak")))
        val draft = DockDraft().also { it.append(listOf(REPLY)) }
        composeRule.setContent {
            MaterialTheme {
                Dock(
                    facts = STATE_FACTS,
                    nowSeconds = NOW,
                    client = OrreryClient("http://127.0.0.1:1"),
                    draft = draft,
                    send = { sent += it },
                    sessionId = SESSION_ID,
                    pending = events,
                )
            }
        }
        composeRule.waitForIdle()
        composeRule.onNodeWithTag(WAITS_TAG).assertTextEquals(SPEAK_ONE)

        composeRule.onNodeWithText("Send").performClick()
        composeRule.awaitUntil("the send succeeded") { sent == listOf(REPLY) }
        composeRule.awaitUntil("the waits line is gone after the send") {
            composeRule.onAllNodesWithTag(WAITS_TAG).fetchSemanticsNodes().isEmpty()
        }

        events = listOf(event("speak"), event("speak"))
        composeRule.awaitUntil("the waits line returns with the changed events") {
            composeRule.onAllNodesWithTag(WAITS_TAG).fetchSemanticsNodes().isNotEmpty()
        }
        composeRule.onNodeWithTag(WAITS_TAG).assertTextEquals(SPEAK_TWO)
    }

    private fun event(waits: String) = PendingItem(id = "synthetic-event", kind = "message", waits = waits)

    private fun setDock(pending: List<PendingItem>, putOff: Boolean = false) {
        composeRule.setContent {
            MaterialTheme {
                Dock(
                    facts = STATE_FACTS.copy(putOff = putOff),
                    nowSeconds = NOW,
                    client = OrreryClient("http://127.0.0.1:1"),
                    draft = DockDraft(),
                    send = {},
                    sessionId = SESSION_ID,
                    pending = pending,
                )
            }
        }
        composeRule.waitForIdle()
    }

    private companion object {
        const val SESSION_ID = "synthetic-session-1"
        const val NOW = 1_000_000L
        val STATE_FACTS = DockFacts(kind = DockKind.SESSION, name = "synthetic session", state = "waiting")
    }
}
