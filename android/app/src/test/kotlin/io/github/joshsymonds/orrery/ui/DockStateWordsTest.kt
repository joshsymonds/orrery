package io.github.joshsymonds.orrery.ui

import java.time.Instant
import java.time.ZoneOffset
import org.junit.Assert.assertEquals
import org.junit.Test

class DockStateWordsTest {
    private val now = 1_000_000L
    private val zone = ZoneOffset.UTC
    private val name = "the auth work"

    private fun facts(
        kind: DockKind = DockKind.SESSION,
        state: String = "",
        interrupting: Boolean = false,
        interruptingSince: Long = 0L,
        turnSince: Long = 0L,
        tool: DockTool? = null,
        changed: Long = 0L,
        reason: String = "",
        interruptedAt: Long = 0L,
        putOff: Boolean = false,
        step: DockStep? = null,
    ) = DockFacts(
        kind = kind,
        name = name,
        state = state,
        interrupting = interrupting,
        interruptingSince = interruptingSince,
        turnSince = turnSince,
        tool = tool,
        changed = changed,
        reason = reason,
        interruptedAt = interruptedAt,
        putOff = putOff,
        step = step,
    )

    private fun stepFacts(
        held: Boolean,
        turnSince: Long = 0L,
        tool: DockTool? = null,
    ) = facts(
        kind = DockKind.STEP,
        state = "working",
        turnSince = turnSince,
        tool = tool,
        step = DockStep("the build step", held),
    )

    private fun line(facts: DockFacts, zone: java.time.ZoneId = this.zone) = stateLine(facts, now, zone)

    @Test
    fun rule1HeldStepReadsHeldWithNoControl() {
        assertEquals(
            StateLine("held", "held", "", "", "", DockControl.NONE),
            line(stepFacts(held = true)),
        )
    }

    @Test
    fun rule2RunningStepShowsToolArgAndElapsedFromTurn() {
        val f = stepFacts(
            held = false,
            turnSince = now - 75,
            tool = DockTool("mcp__orrery__step_start", "build the app", now - 3),
        )
        assertEquals(
            StateLine("working", "running", "step_start", "build the app", "1:15", DockControl.INTERRUPT),
            line(f),
        )
    }

    @Test
    fun rule2RunningStepWithoutToolSaysThinkingAndNoClockWhenTurnUnset() {
        assertEquals(
            StateLine("working", "running", "", "thinking", "", DockControl.INTERRUPT),
            line(stepFacts(held = false)),
        )
    }

    @Test
    fun rule3InterruptingWhileWorkingSaysInterruptingWithElapsedSinceInterrupt() {
        val f = facts(state = "working", interrupting = true, interruptingSince = now - 10)
        assertEquals(
            StateLine(
                "interrupting",
                "interrupting…",
                "",
                "waiting for the turn to end",
                "0:10",
                DockControl.INTERRUPTING,
            ),
            line(f),
        )
    }

    @Test
    fun rule3ClockIsEmptyWhenInterruptingSinceIsUnknown() {
        val f = facts(state = "working", interrupting = true, interruptingSince = 0L)
        assertEquals(
            StateLine("interrupting", "interrupting…", "", "waiting for the turn to end", "", DockControl.INTERRUPTING),
            line(f),
        )
    }

    @Test
    fun interruptingStopsOnceTheStateIsNoLongerWorking() {
        val f = facts(state = "waiting", interrupting = true, interruptingSince = now - 10)
        assertEquals(
            StateLine("waiting", "waiting on you", "", "", "", DockControl.NONE),
            line(f),
        )
    }

    @Test
    fun rule4WorkingClockPrefersTurnSinceThenToolStartThenChanged() {
        val tool = DockTool("Bash", "ls -la", now - 5)
        assertEquals(
            StateLine("working", "working", "Bash", "ls -la", "1:15", DockControl.INTERRUPT),
            line(facts(state = "working", turnSince = now - 75, tool = tool, changed = now - 100)),
        )
        assertEquals(
            StateLine("working", "working", "Bash", "ls -la", "0:05", DockControl.INTERRUPT),
            line(facts(state = "working", tool = tool, changed = now - 100)),
        )
        assertEquals(
            StateLine("working", "working", "", "thinking", "1:40", DockControl.INTERRUPT),
            line(facts(state = "working", changed = now - 100)),
        )
        assertEquals(
            StateLine("working", "working", "", "thinking", "", DockControl.INTERRUPT),
            line(facts(state = "working")),
        )
    }

    @Test
    fun rule4EmptyToolArgSaysThinking() {
        assertEquals(
            StateLine("working", "working", "Bash", "thinking", "0:05", DockControl.INTERRUPT),
            line(facts(state = "working", tool = DockTool("Bash", "", now - 5))),
        )
    }

    @Test
    fun rule5WaitingOnAPutOffQuestionIsIdle() {
        assertEquals(
            StateLine("idle", "waiting on a question you put off", "", "", "", DockControl.NONE),
            line(facts(state = "waiting", putOff = true)),
        )
    }

    @Test
    fun rule6WaitingReadsWaitingOnYou() {
        assertEquals(
            StateLine("waiting", "waiting on you", "", "", "", DockControl.NONE),
            line(facts(state = "waiting")),
        )
    }

    @Test
    fun rule7InterruptedByYouShowsTimeOfDayInTheGivenZone() {
        val interruptedAt = Instant.parse("2026-11-19T09:30:00Z").epochSecond
        val f = facts(state = "done", interruptedAt = interruptedAt)
        assertEquals(
            StateLine("interrupted", "interrupted by you", "", "· 09:30", "", DockControl.NONE),
            line(f),
        )
        assertEquals(
            StateLine("interrupted", "interrupted by you", "", "· 11:30", "", DockControl.NONE),
            line(f, ZoneOffset.ofHours(2)),
        )
    }

    @Test
    fun rule8DoneShowsAgoOfItsChange() {
        assertEquals(
            StateLine("done", "done", "", "· 4m ago", "", DockControl.NONE),
            line(facts(state = "done", changed = now - 240)),
        )
        assertEquals(
            StateLine("done", "done", "", "", "", DockControl.NONE),
            line(facts(state = "done")),
        )
    }

    @Test
    fun rule9CutOffShowsReasonAndAgoAndResumes() {
        assertEquals(
            StateLine("interrupted", "cut off", "", "· tool timed out · 2h ago", "", DockControl.RESUME),
            line(facts(state = "interrupted", reason = "tool timed out", changed = now - 7_200)),
        )
        assertEquals(
            StateLine("interrupted", "cut off", "", "", "", DockControl.RESUME),
            line(facts(state = "interrupted")),
        )
    }

    @Test
    fun rule10FailedShowsReasonAndAgo() {
        assertEquals(
            StateLine("failed", "failed", "", "· boom · 1d ago", "", DockControl.NONE),
            line(facts(state = "failed", reason = "boom", changed = now - 86_400)),
        )
    }

    @Test
    fun rule11StoppedShowsAgoJustNowUnderAMinute() {
        assertEquals(
            StateLine("stopped", "stopped", "", "· just now", "", DockControl.NONE),
            line(facts(state = "stopped", changed = now - 30)),
        )
    }

    @Test
    fun rule12AnythingElseIsIdle() {
        assertEquals(
            StateLine("idle", "idle", "", "", "", DockControl.NONE),
            line(facts(kind = DockKind.SHELL)),
        )
    }

    @Test
    fun placeholderForShellTypesACommand() {
        assertEquals("Type a command…", dockPlaceholder(facts(kind = DockKind.SHELL)))
    }

    @Test
    fun placeholderForHeldStepNudgesAndResumes() {
        assertEquals(
            "Nudge the build step. It resumes with your words",
            dockPlaceholder(stepFacts(held = true)),
        )
    }

    @Test
    fun placeholderForRunningStepInterruptsThenGivesWords() {
        assertEquals(
            "Nudge the build step. It interrupts the turn, then gives it your words",
            dockPlaceholder(stepFacts(held = false)),
        )
    }

    @Test
    fun placeholderWhileInterruptingSaysStopping() {
        assertEquals(
            "Stopping the auth work…",
            dockPlaceholder(facts(state = "working", interrupting = true, interruptingSince = now - 10)),
        )
    }

    @Test
    fun placeholderWhileWorkingIsQueueForTouch() {
        assertEquals("Queue for the auth work", dockPlaceholder(facts(state = "working")))
    }

    @Test
    fun placeholderWhileWaitingIsAnswer() {
        assertEquals("Answer the auth work", dockPlaceholder(facts(state = "waiting")))
        assertEquals("Answer the auth work", dockPlaceholder(facts(state = "waiting", putOff = true)))
    }

    @Test
    fun placeholderForInterruptedByYouSaysStoppedWhereItWas() {
        assertEquals(
            "the auth work stopped where it was. Say what to do next",
            dockPlaceholder(facts(state = "done", interruptedAt = now - 60)),
        )
    }

    @Test
    fun placeholderForCutOffSaysResumeOrSay() {
        assertEquals(
            "the auth work was cut off. Resume it, or say what to do next",
            dockPlaceholder(facts(state = "interrupted")),
        )
    }

    @Test
    fun placeholderForFailedAndStoppedSaysAMessageStartsIt() {
        assertEquals(
            "the auth work failed. A message starts it again",
            dockPlaceholder(facts(state = "failed")),
        )
        assertEquals(
            "the auth work is stopped. A message starts it again",
            dockPlaceholder(facts(state = "stopped")),
        )
    }

    @Test
    fun placeholderOtherwiseIsMessage() {
        assertEquals("Message the auth work", dockPlaceholder(facts(state = "done")))
        assertEquals("Message the auth work", dockPlaceholder(facts(state = "")))
    }

    @Test
    fun elapsedTextIsMinutesAndSecondsThenHoursFromAnHour() {
        assertEquals("0:00", elapsedText(0))
        assertEquals("1:15", elapsedText(75))
        assertEquals("59:59", elapsedText(3_599))
        assertEquals("1:00:00", elapsedText(3_600))
        assertEquals("1:02:03", elapsedText(3_723))
    }

    @Test
    fun elapsedTextIsNeverNegative() {
        assertEquals("0:00", elapsedText(-5))
    }

    @Test
    fun agoTextIsJustNowUnderAMinuteAndLargestWholeUnitAfter() {
        assertEquals("just now", agoText(now - 59, now))
        assertEquals("just now", agoText(now + 30, now))
        assertEquals("1m ago", agoText(now - 60, now))
        assertEquals("59m ago", agoText(now - 3_599, now))
        assertEquals("1h ago", agoText(now - 3_600, now))
        assertEquals("1d ago", agoText(now - 86_400, now))
    }
}
