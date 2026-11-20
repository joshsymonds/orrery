package io.github.joshsymonds.orrery.ui

import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class DockQueueSendNowTest {
    @Test
    fun interruptRunsBeforeAnySay() {
        val events = mutableListOf<String>()
        sendNow(
            interrupt = {
                events += "interrupt"
                listOf("one", "two")
            },
            say = { events += "say $it" },
        )
        assertEquals(listOf("interrupt", "say one", "say two"), events)
    }

    @Test
    fun everyTextIsSaidInSendOrderAndNoneIsHeld() {
        val said = mutableListOf<String>()
        val held = sendNow(
            interrupt = { listOf("first", "second", "third") },
            say = { said += it },
        )
        assertEquals(listOf("first", "second", "third"), said)
        assertEquals(emptyList<String>(), held)
    }

    @Test
    fun refusedSecondSayHoldsItAndEveryLaterTextAndSaysNoLaterText() {
        val attempted = mutableListOf<String>()
        val held = sendNow(
            interrupt = { listOf("first", "second", "third") },
            say = {
                attempted += it
                if (it == "second") throw IOException("refused")
            },
        )
        assertEquals(listOf("first", "second"), attempted)
        assertEquals(listOf("second", "third"), held)
    }

    @Test
    fun throwingInterruptSaysNothingAndPropagates() {
        val said = mutableListOf<String>()
        assertThrows(IOException::class.java) {
            sendNow(
                interrupt = { throw IOException("interrupt refused") },
                say = { said += it },
            )
        }
        assertEquals(emptyList<String>(), said)
    }

    @Test
    fun noTextsSaysNothing() {
        val said = mutableListOf<String>()
        val held = sendNow(
            interrupt = { emptyList() },
            say = { said += it },
        )
        assertEquals(emptyList<String>(), said)
        assertEquals(emptyList<String>(), held)
    }
}
