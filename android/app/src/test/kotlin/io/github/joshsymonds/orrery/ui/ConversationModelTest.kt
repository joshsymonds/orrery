package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ConversationItem
import org.junit.Assert.assertEquals
import org.junit.Test

class ConversationModelTest {
    private fun item(
        kind: String,
        time: Long = 0L,
        text: String = "",
        tool: String = "",
        input: String = "",
        id: String = "",
        error: Boolean = false,
    ) = ConversationItem(kind, time, text, tool, input, id, error)

    private fun call(id: String, name: String = "Read", input: String = "synthetic input") =
        item("tool", tool = name, input = input, id = id)

    private fun result(id: String, text: String = "synthetic output", error: Boolean = false) =
        item("result", text = text, id = id, error = error)

    private fun toolEntry(id: String, name: String = "Read", input: String = "synthetic input", result: String? = null, error: Boolean = false) =
        ConvoEntry.Tool(ToolCall(id, name, input, result, error))

    private fun toolCall(id: String, name: String = "Read", input: String = "synthetic input", result: String? = null, error: Boolean = false) =
        ToolCall(id, name, input, result, error)

    @Test
    fun resultAttachesToCallWithItsIdAfterADifferentCall() {
        val entries = foldConversation(
            listOf(
                call("call-a"),
                call("call-b"),
                result("call-a", text = "output for a"),
            ),
        )

        assertEquals(
            listOf(
                toolEntry("call-a", result = "output for a"),
                toolEntry("call-b"),
            ),
            entries,
        )
    }

    @Test
    fun errorResultSetsErrorFlagAndText() {
        val entries = foldConversation(
            listOf(
                call("call-x", name = "Bash", input = "synthetic command"),
                result("call-x", text = "synthetic failure", error = true),
            ),
        )

        assertEquals(
            listOf(toolEntry("call-x", name = "Bash", input = "synthetic command", result = "synthetic failure", error = true)),
            entries,
        )
    }

    @Test
    fun twoCallsStayTwoToolEntries() {
        val entries = foldConversation(listOf(call("a"), call("b")))

        assertEquals(listOf(toolEntry("a"), toolEntry("b")), entries)
    }

    @Test
    fun threeCallsBecomeOneGroupInOrder() {
        val entries = foldConversation(listOf(call("a"), call("b"), call("c")))

        assertEquals(
            listOf(ConvoEntry.Group(listOf(toolCall("a"), toolCall("b"), toolCall("c")))),
            entries,
        )
    }

    @Test
    fun resultsBetweenCallsDoNotBreakARun() {
        val entries = foldConversation(
            listOf(
                call("a"),
                result("a", text = "out a"),
                call("b"),
                result("b", text = "out b"),
                call("c"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Group(
                    listOf(
                        toolCall("a", result = "out a"),
                        toolCall("b", result = "out b"),
                        toolCall("c"),
                    ),
                ),
            ),
            entries,
        )
    }

    @Test
    fun replyBreaksARunAndTwoCallsBeforeItStayRows() {
        val entries = foldConversation(
            listOf(
                call("a"),
                call("b"),
                item("reply", text = "synthetic reply"),
                call("c"),
                call("d"),
                call("e"),
            ),
        )

        assertEquals(
            listOf(
                toolEntry("a"),
                toolEntry("b"),
                ConvoEntry.Reply("synthetic reply"),
                ConvoEntry.Group(listOf(toolCall("c"), toolCall("d"), toolCall("e"))),
            ),
            entries,
        )
    }

    @Test
    fun promptWithZeroTimeHasNoTimeEntry() {
        val entries = foldConversation(listOf(item("prompt", time = 0L, text = "synthetic prompt")))

        assertEquals(listOf(ConvoEntry.Prompt("synthetic prompt")), entries)
    }

    @Test
    fun promptWithTimeGetsTimeEntryFirst() {
        val entries = foldConversation(listOf(item("prompt", time = 1_700_000_000L, text = "synthetic prompt")))

        assertEquals(
            listOf(ConvoEntry.Time(1_700_000_000L), ConvoEntry.Prompt("synthetic prompt")),
            entries,
        )
    }

    @Test
    fun orphanResultAndUnknownKindProduceNothing() {
        val entries = foldConversation(
            listOf(
                result("no-such-call", text = "orphan output"),
                item("mystery", text = "synthetic unknown"),
            ),
        )

        assertEquals(emptyList<ConvoEntry>(), entries)
    }

    @Test
    fun questionItemBecomesCallWithQuestionsAsInput() {
        val entries = foldConversation(
            listOf(
                item("question", tool = "AskUserQuestion", text = "Pick one?\nPick two?", id = "q-1"),
                result("q-1", text = "synthetic answer"),
            ),
        )

        assertEquals(
            listOf(toolEntry("q-1", name = "AskUserQuestion", input = "Pick one?\nPick two?", result = "synthetic answer")),
            entries,
        )
    }

    @Test
    fun groupSummaryCountsRepeatedNamesInFirstAppearanceOrder() {
        val summary = groupSummary(listOf(toolCall("a", "Read"), toolCall("b", "Read"), toolCall("c", "Grep")))

        assertEquals("Read ×2 · Grep", summary)
    }
}
