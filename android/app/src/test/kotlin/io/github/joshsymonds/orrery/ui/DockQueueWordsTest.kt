package io.github.joshsymonds.orrery.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class DockQueueWordsTest {
    private fun row(waits: String = "", cancelled: Boolean = false) = TrayRowFacts(waits, cancelled)

    @Test
    fun trayLabelCountsEveryDrawnRow() {
        assertEquals("Queued 3", trayLabel(3))
    }

    @Test
    fun paneWithALiveRowSaysClaudeCodeTakesThemWhenItChooses() {
        assertEquals(
            "Claude Code takes them when it chooses",
            trayWhy(pane = true, rows = listOf(row(), row(cancelled = true))),
        )
    }

    @Test
    fun paneWithEveryRowCancelledSaysDroppedWhenTheTurnWasCancelled() {
        assertEquals(
            "dropped when the turn was cancelled",
            trayWhy(pane = true, rows = listOf(row(cancelled = true), row(cancelled = true))),
        )
    }

    @Test
    fun sessionWithOnlyTurnRowsSaysClaudeTakesThemAtItsNextToolResult() {
        assertEquals(
            "Claude takes them at its next tool result",
            trayWhy(pane = false, rows = listOf(row(waits = "turn"), row(waits = "turn"))),
        )
    }

    @Test
    fun sessionWithOneStartRowSaysClaudeTakesItAsItsNextTurn() {
        assertEquals(
            "Claude takes it as its next turn",
            trayWhy(pane = false, rows = listOf(row(waits = "start"), row(waits = "turn"))),
        )
    }

    @Test
    fun sessionWithSeveralStartRowsSaysClaudeTakesThemAsItsNextTurns() {
        assertEquals(
            "Claude takes them as its next turns",
            trayWhy(pane = false, rows = listOf(row(waits = "start"), row(waits = "start"), row(waits = "turn"))),
        )
    }

    @Test
    fun sessionWithEveryRowDiscardedSaysDroppedWhenTheSessionEnded() {
        assertEquals(
            "dropped when the session ended",
            trayWhy(pane = false, rows = listOf(row(waits = "start", cancelled = true), row(waits = "turn", cancelled = true))),
        )
    }

    @Test
    fun cancelledStartRowIsIgnoredBesideALiveTurnRow() {
        assertEquals(
            "Claude takes them at its next tool result",
            trayWhy(pane = false, rows = listOf(row(waits = "start", cancelled = true), row(waits = "turn"))),
        )
    }

    @Test
    fun cancelledStartRowsAreIgnoredBesideALiveStartRow() {
        assertEquals(
            "Claude takes it as its next turn",
            trayWhy(
                pane = false,
                rows = listOf(row(waits = "start", cancelled = true), row(waits = "start", cancelled = true), row(waits = "start")),
            ),
        )
    }

    @Test
    fun sendRefusedLine() {
        assertEquals("Not sent. Your draft is kept; send again.", DockLines.sendRefused)
    }

    @Test
    fun sendUnknownLine() {
        assertEquals(
            "No answer from the server. It may have been sent: check the conversation before sending again.",
            DockLines.sendUnknown,
        )
    }

    @Test
    fun alreadyTookLine() {
        assertEquals("Claude already took it in.", DockLines.alreadyTook)
    }

    @Test
    fun takeBackFailedLine() {
        assertEquals("Could not take it back.", DockLines.takeBackFailed)
    }

    @Test
    fun takeAllBackFailedLine() {
        assertEquals("Could not take them back.", DockLines.takeAllBackFailed)
    }

    @Test
    fun backInBoxOneIsSingular() {
        assertEquals("Your queued message is back in the box.", DockLines.backInBox(1))
    }

    @Test
    fun backInBoxSeveralCountsThem() {
        assertEquals("Your 3 queued messages are back in the box.", DockLines.backInBox(3))
    }

    @Test
    fun sendNowHeldOneIsSingular() {
        assertEquals("1 message was not sent again. It is back in the box.", DockLines.sendNowHeld(1))
    }

    @Test
    fun sendNowHeldSeveralCountsThem() {
        assertEquals("2 messages were not sent again. They are back in the box.", DockLines.sendNowHeld(2))
    }

    @Test
    fun resumeRefusedNamesTheMessage() {
        assertEquals("Not resumed: synthetic refusal", DockLines.resumeRefused("synthetic refusal"))
    }

    @Test
    fun resumeUnknownLine() {
        assertEquals(
            "No answer from the server. It may have resumed: check before trying again.",
            DockLines.resumeUnknown,
        )
    }

    @Test
    fun notDeliveredLine() {
        assertEquals("not delivered", DockLines.notDelivered)
    }
}
