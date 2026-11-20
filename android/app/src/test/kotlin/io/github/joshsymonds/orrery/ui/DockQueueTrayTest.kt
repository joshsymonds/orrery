package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockQueueTrayTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val takenBack = mutableListOf<String>()
    private val putBack = mutableListOf<String>()
    private var takeAllBack = 0
    private var sentNow = 0

    private fun row(
        key: String,
        text: String,
        waits: String = "turn",
        cancelled: Boolean = false,
        promptId: String = "prompt-$key",
    ) = DockTrayRow(key, text, cancelled, waits, promptId)

    private fun tray(
        rows: List<DockTrayRow>,
        pane: Boolean = false,
        working: Boolean = false,
        notes: Map<String, String> = emptyMap(),
        withSendNow: Boolean = true,
    ) {
        composeRule.setContent {
            MaterialTheme {
                DockTray(
                    pane = pane,
                    rows = rows,
                    working = working,
                    notes = notes,
                    onTakeBack = { takenBack += it },
                    onPutBack = { putBack += it },
                    onTakeAllBack = { takeAllBack++ },
                    onSendNow = if (withSendNow) ({ sentNow++ }) else null,
                )
            }
        }
    }

    @Test
    fun rowsAreDrawnOldestFirstUnderTheirCount() {
        tray(
            listOf(
                row("a", "first queued"),
                row("b", "second queued"),
                row("c", "third queued"),
            ),
        )

        composeRule.onNodeWithTag("dock-tray").assertIsDisplayed()
        composeRule.onNodeWithText("Queued 3", ignoreCase = true).assertIsDisplayed()
        composeRule.onAllNodesWithTag("dock-tray-row").assertCountEquals(3)
        composeRule.onAllNodesWithTag("dock-tray-row")[0].assert(hasAnyDescendant(hasText("first queued")))
        composeRule.onAllNodesWithTag("dock-tray-row")[1].assert(hasAnyDescendant(hasText("second queued")))
        composeRule.onAllNodesWithTag("dock-tray-row")[2].assert(hasAnyDescendant(hasText("third queued")))
    }

    @Test
    fun sessionTurnRowsSayClaudeTakesThemAtItsNextToolResult() {
        tray(listOf(row("a", "one", waits = "turn"), row("b", "two", waits = "turn")))

        composeRule.onNodeWithText("Claude takes them at its next tool result").assertIsDisplayed()
    }

    @Test
    fun sessionStartRowSaysClaudeTakesItAsItsNextTurn() {
        tray(listOf(row("a", "one", waits = "start"), row("b", "two", waits = "turn")))

        composeRule.onNodeWithText("Claude takes it as its next turn").assertIsDisplayed()
    }

    @Test
    fun paneSaysClaudeCodeTakesThemWhenItChooses() {
        tray(listOf(row("a", "one", waits = "turn")), pane = true)

        composeRule.onNodeWithText("Claude Code takes them when it chooses").assertIsDisplayed()
    }

    @Test
    fun takeBackCallsOnTakeBackWithThePromptId() {
        tray(listOf(row("row-a", "hello", promptId = "prompt-7")))

        composeRule.onNodeWithTag("dock-take-back").performClick()

        assertEquals(listOf("prompt-7"), takenBack)
        assertEquals(0, putBack.size)
    }

    @Test
    fun discardedSessionRowReadsNotDeliveredAndPutBackCallsOnPutBack() {
        tray(listOf(row("row-a", "gone", cancelled = true, promptId = "prompt-9")))

        composeRule.onNodeWithText("not delivered", ignoreCase = true).assertIsDisplayed()
        composeRule.onAllNodesWithTag("dock-take-back").assertCountEquals(0)
        composeRule.onNodeWithTag("dock-put-back").performClick()

        assertEquals(listOf("prompt-9"), putBack)
        assertEquals(0, takenBack.size)
    }

    @Test
    fun paneCancelledRowReadsNotDeliveredWithNoControl() {
        tray(listOf(row("row-a", "gone", cancelled = true)), pane = true)

        composeRule.onNodeWithText("not delivered", ignoreCase = true).assertIsDisplayed()
        composeRule.onAllNodesWithTag("dock-put-back").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-take-back").assertCountEquals(0)
    }

    @Test
    fun paneLiveRowDrawsNoRowControl() {
        tray(listOf(row("row-a", "live")), pane = true)

        composeRule.onAllNodesWithTag("dock-take-back").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-put-back").assertCountEquals(0)
    }

    @Test
    fun takeAllBackIsDrawnOnlyForAPaneAndCallsOnTakeAllBack() {
        tray(listOf(row("row-a", "live")), pane = true)

        composeRule.onNodeWithTag("dock-take-all-back").performClick()

        assertEquals(1, takeAllBack)
    }

    @Test
    fun sessionHeadDrawsNoTakeAllBack() {
        tray(listOf(row("row-a", "live")), pane = false)

        composeRule.onAllNodesWithTag("dock-take-all-back").assertCountEquals(0)
    }

    @Test
    fun sendNowIsDrawnForAWorkingSessionAndCallsOnSendNow() {
        tray(listOf(row("row-a", "live")), working = true)

        composeRule.onNodeWithTag("dock-send-now").performClick()

        assertEquals(1, sentNow)
    }

    @Test
    fun sendNowIsNotDrawnWhenTheSessionIsNotWorking() {
        tray(listOf(row("row-a", "live")), working = false)

        composeRule.onAllNodesWithTag("dock-send-now").assertCountEquals(0)
    }

    @Test
    fun sendNowIsNotDrawnWithoutACallback() {
        tray(listOf(row("row-a", "live")), working = true, withSendNow = false)

        composeRule.onAllNodesWithTag("dock-send-now").assertCountEquals(0)
    }

    @Test
    fun sendNowIsNotDrawnForAPane() {
        tray(listOf(row("row-a", "live")), pane = true, working = true)

        composeRule.onAllNodesWithTag("dock-send-now").assertCountEquals(0)
    }

    @Test
    fun notesAreDrawnUnderTheirRows() {
        tray(
            listOf(row("row-a", "live")),
            notes = mapOf("row-a" to "held until you speak"),
        )

        composeRule.onNodeWithText("held until you speak").assertIsDisplayed()
    }

    @Test
    fun noRowsDrawsNothing() {
        tray(emptyList(), working = true)

        composeRule.onAllNodesWithTag("dock-tray").assertCountEquals(0)
        composeRule.onAllNodesWithText("Queued 0", ignoreCase = true).assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-send-now").assertCountEquals(0)
    }
}
