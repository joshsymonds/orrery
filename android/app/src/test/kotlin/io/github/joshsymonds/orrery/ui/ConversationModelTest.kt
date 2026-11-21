package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ConversationItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test
import java.time.ZoneId

// The items in this file are timed on 1 January 1970 and 14 November 2023 (UTC); a fold given the matching now draws no day rule.
private const val EPOCH_DAY_NOW = 100L
private const val NOV_14_2213_UTC = 1_700_000_000L
private val UTC: ZoneId = ZoneId.of("UTC")

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
        trigger: String = "",
        tokens: Long = 0L,
    ) = ConversationItem(kind, time, text, tool, input, id, error, notice, trigger, tokens)

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

    private fun fold(items: List<ConversationItem>, now: Long = EPOCH_DAY_NOW) =
        foldConversation(items, now = now, zone = UTC)

    @Test
    fun resultAttachesToCallWithItsIdAfterADifferentCall() {
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(listOf(call("a"), call("b")))

        assertEquals(listOf(toolEntry("a"), toolEntry("b")), entries)
    }

    @Test
    fun threeCallsBecomeOneGroupInOrder() {
        val entries = fold(listOf(call("a"), call("b"), call("c")))

        assertEquals(
            listOf(ConvoEntry.Group(listOf(toolCall("a"), toolCall("b"), toolCall("c")))),
            entries,
        )
    }

    @Test
    fun resultsBetweenCallsDoNotBreakARun() {
        val entries = fold(
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
        val entries = fold(
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
    fun compactionItemSplitsARunAndBecomesItsOwnEntry() {
        val entries = fold(
            listOf(
                call("a"),
                call("b"),
                item("compaction", time = NOV_14_2213_UTC, text = "synthetic summary", trigger = "auto", tokens = 611_000L),
                call("c"),
            ),
            now = NOV_14_2213_UTC,
        )

        assertEquals(
            listOf(
                toolEntry("a"),
                toolEntry("b"),
                ConvoEntry.Compaction("auto", 611_000L, "synthetic summary", NOV_14_2213_UTC),
                toolEntry("c"),
            ),
            entries,
        )
    }

    @Test
    fun compactionKeyHoldsWhenTheWindowSlides() {
        val compaction = item("compaction", time = NOV_14_2213_UTC, text = "synthetic summary", trigger = "auto", tokens = 611_000L)
        val earlier = item("reply", time = NOV_14_2213_UTC - 100L, text = "synthetic reply")

        val before = entryKeys(fold(listOf(earlier, compaction), now = NOV_14_2213_UTC))
        val after = entryKeys(fold(listOf(compaction), now = NOV_14_2213_UTC))

        assertEquals(after.single(), before.last())
    }

    @Test
    fun promptWithZeroTimeKeepsNoTime() {
        val entries = fold(listOf(item("prompt", time = 0L, text = "synthetic prompt")))

        assertEquals(listOf(ConvoEntry.Prompt("synthetic prompt")), entries)
    }

    @Test
    fun promptKeepsItsTimeAndPromptId() {
        val prompt = item("prompt", time = NOV_14_2213_UTC, text = "synthetic prompt").copy(promptId = "p-1")

        assertEquals(
            listOf(ConvoEntry.Prompt("synthetic prompt", NOV_14_2213_UTC, "p-1")),
            fold(listOf(prompt), now = NOV_14_2213_UTC),
        )
    }

    @Test
    fun replyWithTimeKeepsItsTime() {
        val entries = fold(listOf(item("reply", time = NOV_14_2213_UTC, text = "synthetic reply")), now = NOV_14_2213_UTC)

        assertEquals(listOf(ConvoEntry.Reply("synthetic reply", NOV_14_2213_UTC)), entries)
    }

    @Test
    fun replyWithZeroTimeKeepsNoTime() {
        val entries = fold(listOf(item("reply", time = 0L, text = "synthetic reply")))

        assertEquals(listOf(ConvoEntry.Reply("synthetic reply")), entries)
    }

    @Test
    fun timedItemsKeepTheirTimesAndTheRunsBetweenThemStay() {
        val entries = fold(
            listOf(
                item("prompt", time = 100L, text = "synthetic prompt one"),
                call("a"),
                call("b"),
                item("reply", time = 105L, text = "synthetic reply one"),
                notice("task", text = "synthetic task", time = 110L),
                item("reply", time = 0L, text = "synthetic reply two"),
                item("prompt", time = 200L, text = "synthetic prompt two"),
                call("c"),
                call("d"),
                call("e"),
                item("reply", time = 210L, text = "synthetic reply three"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Prompt("synthetic prompt one", 100L),
                toolEntry("a"),
                toolEntry("b"),
                ConvoEntry.Reply("synthetic reply one", 105L),
                ConvoEntry.Notice("task", "synthetic task", false, 110L),
                ConvoEntry.Reply("synthetic reply two"),
                ConvoEntry.Prompt("synthetic prompt two", 200L),
                ConvoEntry.Group(listOf(toolCall("c"), toolCall("d"), toolCall("e"))),
                ConvoEntry.Reply("synthetic reply three", 210L),
            ),
            entries,
        )
    }

    @Test
    fun orphanResultAndUnknownKindProduceNothing() {
        val entries = fold(
            listOf(
                result("no-such-call", text = "orphan output"),
                item("mystery", text = "synthetic unknown"),
            ),
        )

        assertEquals(emptyList<ConvoEntry>(), entries)
    }

    @Test
    fun questionItemBecomesCallWithQuestionsAsInput() {
        val entries = fold(
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

        val entriesBefore = fold(windowBefore)
        val entriesAfter = fold(windowAfter)

        assertEquals(entriesBefore.subList(1, 4), entriesAfter.subList(0, 3))
        assertEquals(
            entryKeys(entriesBefore).subList(1, 4),
            entryKeys(entriesAfter).subList(0, 3),
        )
    }

    @Test
    fun repliesWithSameTextAndTimeGetDistinctKeys() {
        val entries = fold(
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
            fold(
                listOf(
                    item("prompt", time = 100L, text = "same text"),
                    item("prompt", time = 200L, text = "same text"),
                ),
            ),
        )
        val alone = entryKeys(fold(listOf(item("prompt", time = 200L, text = "same text"))))

        assertNotEquals(pair[0], pair[1])
        assertEquals(alone.single(), pair.last())
    }

    @Test
    fun foldKeepsReplyAndPromptTimes() {
        val entries = fold(
            listOf(
                item("reply", time = 300L, text = "synthetic reply"),
                item("prompt", time = 400L, text = "synthetic prompt"),
            ),
        )

        assertEquals(
            listOf(
                ConvoEntry.Reply("synthetic reply", 300L),
                ConvoEntry.Prompt("synthetic prompt", 400L),
            ),
            entries,
        )
    }

    @Test
    fun eachNoticeSortBecomesANoticeEntryWithItsTextErrorAndTime() {
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(
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
        val entries = fold(listOf(notice("interrupted", time = 5L), notice("interrupted", time = 5L)))

        val keys = entryKeys(entries)

        assertEquals(2, entries.size)
        assertEquals(2, keys.toSet().size)
    }

    @Test
    fun aNoticeKeyHoldsWhenItMovesBehindAGroup() {
        val alone = entryKeys(fold(listOf(notice("task", text = "synthetic task", time = 7L))))
        val behindGroup = entryKeys(
            fold(listOf(call("a"), notice("task", text = "synthetic task", time = 7L), call("b"), call("c"))),
        )

        assertEquals(alone.single(), behindGroup.last())
    }
}
