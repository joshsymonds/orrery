package io.github.joshsymonds.orrery.ui

import com.connectrpc.Code
import io.github.joshsymonds.orrery.api.NoAnswerException
import io.github.joshsymonds.orrery.api.OrreryCallException
import io.github.joshsymonds.orrery.api.StepCallResult
import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OwnerCallTest {
    private val readingLanded = "Sent. Reading the epic again…"
    private val readingUnanswered = "No answer came: it may have gone through. Reading the epic again…"
    private val restUnanswered = "No answer came: it may have gone through."

    @Test
    fun sendIsTrueOnceFromAskingAndFalseInEveryOtherPhase() {
        val call = OwnerCall()
        assertFalse("rest sends nothing", call.send())
        call.ask("Release")
        assertTrue("asking sends once", call.send())
        assertFalse("sending sends nothing", call.send())
        call.answered(CallOutcome.Landed) { 1 }
        assertFalse("reading sends nothing", call.send())
        call.loaded(1)
        assertEquals(CallPhase.Rest, call.phase)
        assertFalse("rest again sends nothing", call.send())
    }

    @Test
    fun keepReturnsToRestAndSendsNothing() {
        val call = OwnerCall()
        call.ask("Release")
        call.keep()
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals("", call.asking)
        assertFalse("a kept call cannot be sent", call.send())
    }

    @Test
    fun askOnlyStartsFromRest() {
        val call = OwnerCall()
        call.ask("Release")
        call.ask("Close as…")
        assertEquals("Release", call.asking)
        call.send()
        call.ask("Close as…")
        assertEquals(CallPhase.Sending, call.phase)
    }

    @Test
    fun sendingShowsTheSendingLineAndIsDisabled() {
        val call = OwnerCall()
        call.ask("Release")
        call.send()
        assertEquals(CallPhase.Sending, call.phase)
        assertEquals("Sending…", call.line)
        assertTrue(call.disabled)
    }

    @Test
    fun aRefusedAnswerIsRestAtOnceWithItsReasonAndNeverReads() {
        val call = OwnerCall()
        var reads = 0
        call.ask("Release")
        call.send()
        call.answered(CallOutcome.Refused("boom")) {
            reads++
            1
        }
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals("Refused: boom", call.line)
        assertTrue(call.refused)
        assertFalse(call.disabled)
        assertEquals("a refusal does not read the epic", 0, reads)
        call.ask("Release")
        assertTrue("enabled again, a second send is confirmed again", call.send())
    }

    @Test
    fun aLandedAnswerReadsDisabledUntilALoadAtItsGenerationLands() {
        val call = OwnerCall()
        var reads = 0
        call.ask("Release")
        call.send()
        call.answered(CallOutcome.Landed) {
            reads++
            7
        }
        assertEquals(CallPhase.Reading, call.phase)
        assertEquals(readingLanded, call.line)
        assertTrue(call.disabled)
        assertEquals(1, reads)
        call.loaded(6)
        assertEquals("an older load keeps the call reading", CallPhase.Reading, call.phase)
        call.loaded(7)
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals("a landed call ends with no line", "", call.line)
        assertFalse(call.refused)
        assertFalse(call.disabled)
    }

    @Test
    fun aLoadAtALaterGenerationAlsoEndsReading() {
        val call = OwnerCall()
        call.ask("Release")
        call.send()
        call.answered(CallOutcome.Landed) { 3 }
        call.loaded(4)
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals("", call.line)
    }

    @Test
    fun anUnansweredCallReadsWithItsReadingLineThenRestsWithItsRestLine() {
        val call = OwnerCall()
        call.ask("Release")
        call.send()
        call.answered(CallOutcome.Unanswered) { 2 }
        assertEquals(CallPhase.Reading, call.phase)
        assertEquals(readingUnanswered, call.line)
        assertTrue(call.disabled)
        call.loaded(2)
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals(restUnanswered, call.line)
        assertFalse(call.refused)
    }

    @Test
    fun answersAreIgnoredOutsideSending() {
        val call = OwnerCall()
        call.answered(CallOutcome.Refused("boom")) { 1 }
        assertEquals("", call.line)
        call.ask("Release")
        call.answered(CallOutcome.Landed) { 1 }
        assertEquals(CallPhase.Asking, call.phase)
        assertEquals("", call.line)
    }

    @Test
    fun loadsAreIgnoredOutsideReading() {
        val call = OwnerCall()
        call.loaded(0)
        assertEquals(CallPhase.Rest, call.phase)
        assertEquals("", call.line)
    }

    @Test
    fun aStepRefusalIsARefusalWithItsMessage() {
        val outcome = outcomeOf { StepCallResult.Refused("the step is not held") }
        assertEquals(CallOutcome.Refused("the step is not held"), outcome)
    }

    @Test
    fun anEmptyRefusalReasonBecomesNoReasonWasGiven() {
        assertEquals(CallOutcome.Refused("no reason was given"), outcomeOf { StepCallResult.Refused("") })
        val empty = OrreryCallException(Code.FAILED_PRECONDITION, "")
        assertEquals(CallOutcome.Refused("no reason was given"), outcomeOf { throw empty })
    }

    @Test
    fun aLandedCallIsLanded() {
        assertEquals(CallOutcome.Landed, outcomeOf { StepCallResult.Landed })
    }

    @Test
    fun aFailedPreconditionOrNotFoundIsARefusalWithItsMessage() {
        val failed = OrreryCallException(Code.FAILED_PRECONDITION, "not while it runs")
        assertEquals(CallOutcome.Refused("not while it runs"), outcomeOf { throw failed })
        val missing = OrreryCallException(Code.NOT_FOUND, "no such epic")
        assertEquals(CallOutcome.Refused("no such epic"), outcomeOf { throw missing })
    }

    @Test
    fun anUnavailableUnknownOrDeadlineCallIsUnanswered() {
        for (code in listOf(Code.UNAVAILABLE, Code.DEADLINE_EXCEEDED, Code.UNKNOWN)) {
            val error = OrreryCallException(code, "gone")
            assertEquals(code.name, CallOutcome.Unanswered, outcomeOf { throw error })
        }
    }

    @Test
    fun aNoAnswerExceptionIsUnanswered() {
        val error = NoAnswerException(IOException("timed out"))
        assertEquals(CallOutcome.Unanswered, outcomeOf { throw error })
    }

    @Test
    fun anOtherIoExceptionIsUnanswered() {
        assertEquals(CallOutcome.Unanswered, outcomeOf { throw IOException("reset") })
    }
}
