package io.github.joshsymonds.orrery.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class DockWaitsWordsTest {
    @Test
    fun oneSpeakEventSpeaksSingular() {
        assertEquals("1 event waits until you speak", waitsLine(listOf("speak"), putOff = false))
    }

    @Test
    fun severalSpeakEventsCountThemAndFreeIsNotCounted() {
        assertEquals(
            "2 events wait until you speak",
            waitsLine(listOf("speak", "free", "speak"), putOff = false),
        )
    }

    @Test
    fun oneRequestEventCountedOnlyWhenPutOffSingular() {
        assertEquals(
            "1 event waits on the question you put off",
            waitsLine(listOf("request"), putOff = true),
        )
    }

    @Test
    fun severalRequestEventsWhenPutOffCountThem() {
        assertEquals(
            "2 events wait on the question you put off",
            waitsLine(listOf("request", "free", "request"), putOff = true),
        )
    }

    @Test
    fun requestNotCountedWhenNotPutOff() {
        assertEquals("", waitsLine(listOf("request", "request"), putOff = false))
    }

    @Test
    fun speakWinsOverRequestWhenBothWait() {
        assertEquals(
            "1 event waits until you speak",
            waitsLine(listOf("request", "speak", "request"), putOff = true),
        )
    }

    @Test
    fun freeEventsAreNeverCounted() {
        assertEquals("", waitsLine(listOf("free", "free"), putOff = true))
    }

    @Test
    fun emptyPendingGivesEmptyLine() {
        assertEquals("", waitsLine(emptyList(), putOff = true))
    }
}
