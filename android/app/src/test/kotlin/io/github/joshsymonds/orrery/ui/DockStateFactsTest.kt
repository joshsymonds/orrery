package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.Conversation
import io.github.joshsymonds.orrery.api.ConversationItem
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.SessionRequest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DockStateFactsTest {
    private fun item(
        kind: String,
        time: Long = 0L,
        tool: String = "",
        input: String = "",
        id: String = "",
    ) = ConversationItem(kind, time, "", tool, input, id, false)

    private fun call(id: String, name: String = "Read", input: String = "synthetic input", time: Long = 0L) =
        item("tool", time = time, tool = name, input = input, id = id)

    private fun result(id: String, time: Long = 0L) = item("result", time = time, id = id)

    private fun stream(
        items: List<ConversationItem> = emptyList(),
        pane: Pane? = null,
        request: SessionRequest? = null,
        interrupting: Boolean = false,
        interruptingSince: Long = 0L,
        turnSince: Long = 0L,
    ) = StreamedConversation(
        transcript = "",
        seq = 0L,
        conversation = Conversation(origin = "", items = items),
        pane = pane,
        request = request,
        interrupting = interrupting,
        interruptingSince = interruptingSince,
        turnSince = turnSince,
    )

    private fun pane(
        state: String = "",
        changed: Long = 0L,
        tool: String = "",
        putOff: Boolean = false,
    ) = Pane(
        id = "synthetic-pane",
        host = "synthetic-host",
        session = "synthetic-session",
        window = 0,
        pane = 0,
        cwd = "",
        command = "",
        state = state,
        summary = "",
        label = "",
        changed = changed,
        seen = 0L,
        question = "",
        questionHash = "",
        tool = tool,
        putOff = putOff,
    )

    private fun request(putOff: Boolean) = SessionRequest(
        id = "synthetic-request",
        kind = "question",
        question = "",
        options = emptyList(),
        putOff = putOff,
    )

    @Test
    fun interruptingFromTheStreamIsTrue() {
        val facts = dockFactsOf(DockKind.SESSION, "name", null, stream(interrupting = true))

        assertTrue(facts.interrupting)
    }

    @Test
    fun interruptingIsFalseWhenTheStreamDoesNotSayItIsInterrupting() {
        val facts = dockFactsOf(DockKind.SESSION, "name", null, stream())

        assertFalse(facts.interrupting)
    }

    @Test
    fun interruptingSinceAndTurnSinceAreReadFromTheStream() {
        val facts = dockFactsOf(
            DockKind.SESSION,
            "name",
            null,
            stream(interrupting = true, interruptingSince = 500L, turnSince = 400L),
        )

        assertEquals(500L, facts.interruptingSince)
        assertEquals(400L, facts.turnSince)
    }

    @Test
    fun sessionRunningCallIsTheTrailingCallWithoutAResult() {
        val facts = dockFactsOf(
            DockKind.SESSION,
            "name",
            null,
            stream(items = listOf(call("a", name = "Bash"), call("b", name = "mcp__orrery__step_start", input = "synthetic arg", time = 900L))),
        )

        assertEquals(DockTool("mcp__orrery__step_start", "synthetic arg", 900L), facts.tool)
    }

    @Test
    fun noRunningCallOnceItsResultFollows() {
        val facts = dockFactsOf(
            DockKind.SESSION,
            "name",
            null,
            stream(items = listOf(call("a"), result("a"))),
        )

        assertNull(facts.tool)
    }

    @Test
    fun sessionRunningCallSkipsCompletedCallsBeforeTheTrailingRun() {
        val facts = dockFactsOf(
            DockKind.STEP,
            "name",
            null,
            stream(items = listOf(call("a", name = "Grep", time = 100L), call("b", name = "Read", time = 200L), result("b"))),
        )

        assertEquals(DockTool("Grep", "synthetic input", 100L), facts.tool)
    }

    @Test
    fun paneToolIsTheCurrentToolOfThePane() {
        val tool = """{"id":"t1","name":"Bash","input":"synthetic command","start":1234}"""
        val facts = dockFactsOf(DockKind.PANE, "name", pane(tool = tool), null)

        assertEquals(DockTool("Bash", "synthetic command", 1234L), facts.tool)
    }

    @Test
    fun shellToolIsTheCurrentToolOfThePane() {
        val tool = """{"id":"t1","name":"Bash","input":"synthetic command","start":1234}"""
        val facts = dockFactsOf(DockKind.SHELL, "name", pane(tool = tool), null)

        assertEquals(DockTool("Bash", "synthetic command", 1234L), facts.tool)
    }

    @Test
    fun interruptedAtIsTheNewestItemWhenItIsInterrupted() {
        val facts = dockFactsOf(
            DockKind.SESSION,
            "name",
            null,
            stream(items = listOf(item("reply", time = 800L), item("interrupted", time = 900L))),
        )

        assertEquals(900L, facts.interruptedAt)
    }

    @Test
    fun interruptedAtIsZeroWhenAnInterruptedItemIsNotTheNewest() {
        val facts = dockFactsOf(
            DockKind.SESSION,
            "name",
            null,
            stream(items = listOf(item("interrupted", time = 900L), item("reply", time = 950L))),
        )

        assertEquals(0L, facts.interruptedAt)
    }

    @Test
    fun putOffComesFromTheRequest() {
        val facts = dockFactsOf(DockKind.SESSION, "name", null, stream(request = request(putOff = true)))

        assertTrue(facts.putOff)
    }

    @Test
    fun putOffComesFromThePaneEvenBesideARequestNotPutOff() {
        val facts = dockFactsOf(DockKind.SESSION, "name", pane(putOff = true), stream(request = request(putOff = false)))

        assertTrue(facts.putOff)
    }

    @Test
    fun putOffComesFromThePaneWhenThereIsNoRequest() {
        val facts = dockFactsOf(DockKind.PANE, "name", pane(putOff = true), null)

        assertTrue(facts.putOff)
    }

    @Test
    fun stateChangedReasonAndStepArePassedThrough() {
        val step = DockStep("the review step", held = true)
        val facts = dockFactsOf(
            DockKind.STEP,
            "name",
            pane(state = "failed", changed = 777L),
            null,
            reason = "synthetic reason",
            step = step,
        )

        assertEquals("failed", facts.state)
        assertEquals(777L, facts.changed)
        assertEquals("synthetic reason", facts.reason)
        assertEquals(step, facts.step)
        assertEquals(DockKind.STEP, facts.kind)
        assertEquals("name", facts.name)
    }
}
