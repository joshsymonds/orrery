package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ConversationItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
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
        notice: String = "",
    ) = ConversationItem(kind, time, text, tool, input, id, error, notice)

    private fun notice(sort: String, text: String = "", error: Boolean = false, time: Long = 0L) =
        item("notice", time = time, text = text, error = error, notice = sort)

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
            listOf(ConvoEntry.Time(1_700_000_000L), ConvoEntry.Prompt("synthetic prompt", 1_700_000_000L)),
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

    @Test
    fun keysOfSharedEntriesStayEqualWhenWindowSlides() {
        val windowBefore = listOf(
            item("prompt", time = 100L, text = "synthetic prompt one"),
            item("reply", time = 105L, text = "synthetic reply one"),
            call("t1"),
            call("t2"),
            call("t3"),
            item("prompt", time = 200L, text = "synthetic prompt two"),
        )
        val windowAfter = windowBefore.drop(1) + item("reply", time = 210L, text = "synthetic reply two")

        val entriesBefore = foldConversation(windowBefore)
        val entriesAfter = foldConversation(windowAfter)

        assertEquals(entriesBefore.subList(2, 6), entriesAfter.subList(0, 4))
        assertEquals(
            entryKeys(entriesBefore).subList(2, 6),
            entryKeys(entriesAfter).subList(0, 4),
        )
    }

    @Test
    fun repliesWithSameTextAndTimeGetDistinctKeys() {
        val entries = foldConversation(
            listOf(
                item("reply", time = 50L, text = "same text"),
                item("reply", time = 50L, text = "same text"),
                item("reply", time = 50L, text = "same text"),
            ),
        )

        val keys = entryKeys(entries)

        assertEquals(3, entries.size)
        assertEquals(entries.size, keys.toSet().size)
    }

    @Test
    fun promptsWithSameTextAtDifferentTimesGetDistinctKeys() {
        val pair = entryKeys(
            foldConversation(
                listOf(
                    item("prompt", time = 100L, text = "same text"),
                    item("prompt", time = 200L, text = "same text"),
                ),
            ),
        )
        val alone = entryKeys(foldConversation(listOf(item("prompt", time = 200L, text = "same text"))))

        assertNotEquals(pair[1], pair[3])
        assertEquals(alone.last(), pair.last())
    }

    @Test
    fun foldKeepsReplyAndPromptTimes() {
        val entries = foldConversation(
            listOf(
                item("reply", time = 300L, text = "synthetic reply"),
                item("prompt", time = 400L, text = "synthetic prompt"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Reply("synthetic reply", 300L),
                ConvoEntry.Time(400L),
                ConvoEntry.Prompt("synthetic prompt", 400L),
            ),
            entries,
        )
    }

    @Test
    fun eachNoticeSortBecomesANoticeEntryWithItsTextErrorAndTime() {
        val entries = foldConversation(
            listOf(
                notice("compaction", time = 10L),
                notice("task", text = "synthetic task", time = 20L),
                notice("task", text = "synthetic failed task", error = true, time = 30L),
                notice("teammate", text = "synthetic summary one\nsynthetic summary two", time = 40L),
                notice("interrupted", time = 50L),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Notice("compaction", "", false, 10L),
                ConvoEntry.Notice("task", "synthetic task", false, 20L),
                ConvoEntry.Notice("task", "synthetic failed task", true, 30L),
                ConvoEntry.Notice("teammate", "synthetic summary one\nsynthetic summary two", false, 40L),
                ConvoEntry.Notice("interrupted", "", false, 50L),
            ),
            entries,
        )
    }

    @Test
    fun noticeBetweenRepliesStaysInPlace() {
        val entries = foldConversation(
            listOf(
                item("reply", text = "synthetic reply one"),
                notice("compaction"),
                item("reply", text = "synthetic reply two"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Reply("synthetic reply one"),
                ConvoEntry.Notice("compaction", "", false),
                ConvoEntry.Reply("synthetic reply two"),
            ),
            entries,
        )
    }

    @Test
    fun noticeInsideARunOfThreeKeepsOneGroupAndFollowsIt() {
        val entries = foldConversation(
            listOf(
                call("a"),
                notice("task", text = "synthetic task"),
                call("b"),
                call("c"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Group(listOf(toolCall("a"), toolCall("b"), toolCall("c"))),
                ConvoEntry.Notice("task", "synthetic task", false),
            ),
            entries,
        )
    }

    @Test
    fun noticesAndResultsInsideARunKeepTheGroupAndTheNoticesKeepTheirOrder() {
        val entries = foldConversation(
            listOf(
                call("a"),
                result("a", text = "out a"),
                notice("task", text = "synthetic task one"),
                call("b"),
                notice("teammate", text = "synthetic summary"),
                result("b", text = "out b"),
                call("c"),
                notice("task", text = "synthetic task two", error = true),
                item("reply", text = "synthetic reply"),
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
                ConvoEntry.Notice("task", "synthetic task one", false),
                ConvoEntry.Notice("teammate", "synthetic summary", false),
                ConvoEntry.Notice("task", "synthetic task two", true),
                ConvoEntry.Reply("synthetic reply"),
            ),
            entries,
        )
    }

    @Test
    fun noticeInsideARunOfTwoKeepsItsPlaceBetweenTheRows() {
        val entries = foldConversation(
            listOf(
                call("a"),
                notice("task", text = "synthetic task"),
                call("b"),
                item("reply", text = "synthetic reply"),
            ),
        )

        assertEquals(
            listOf(
                toolEntry("a"),
                ConvoEntry.Notice("task", "synthetic task", false),
                toolEntry("b"),
                ConvoEntry.Reply("synthetic reply"),
            ),
            entries,
        )
    }

    @Test
    fun aReplyStillSplitsARunThatNoticesDoNot() {
        val entries = foldConversation(
            listOf(
                call("a"),
                notice("task", text = "synthetic task"),
                call("b"),
                item("reply", text = "synthetic reply"),
                call("c"),
                call("d"),
                notice("interrupted"),
            ),
        )

        assertEquals(
            listOf(
                toolEntry("a"),
                ConvoEntry.Notice("task", "synthetic task", false),
                toolEntry("b"),
                ConvoEntry.Reply("synthetic reply"),
                toolEntry("c"),
                toolEntry("d"),
                ConvoEntry.Notice("interrupted", "", false),
            ),
            entries,
        )
    }

    @Test
    fun noticesWithTheSameSortTextAndTimeGetDistinctKeys() {
        val entries = foldConversation(listOf(notice("interrupted", time = 5L), notice("interrupted", time = 5L)))

        val keys = entryKeys(entries)

        assertEquals(2, entries.size)
        assertEquals(2, keys.toSet().size)
    }

    @Test
    fun aNoticeKeyHoldsWhenItMovesBehindAGroup() {
        val alone = entryKeys(foldConversation(listOf(notice("task", text = "synthetic task", time = 7L))))
        val behindGroup = entryKeys(
            foldConversation(listOf(call("a"), notice("task", text = "synthetic task", time = 7L), call("b"), call("c"))),
        )

        assertEquals(alone.single(), behindGroup.last())
    }
}
