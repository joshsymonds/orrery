package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ConversationViewTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val originalZone: TimeZone = TimeZone.getDefault()

    @After
    fun restoreZone() {
        TimeZone.setDefault(originalZone)
    }

    private fun call(
        id: String,
        name: String = "Read",
        input: String = "synthetic input",
        result: String? = null,
        error: Boolean = false,
    ) = ToolCall(id, name, input, result, error)

    private fun setList(entries: List<ConvoEntry>, footer: (@Composable () -> Unit)? = null) {
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries, footer = footer) }
        }
    }

    @Test
    fun promptShowsYouLabelAndTextAndReplyShowsText() {
        setList(listOf(ConvoEntry.Prompt("synthetic prompt"), ConvoEntry.Reply("synthetic reply")))

        composeRule.onNodeWithText("YOU").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic prompt").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic reply").assertIsDisplayed()
    }

    @Test
    fun replyInlineMarkupShowsItsWordsWithoutMarkers() {
        val raw = "**synthetic bold** and `synthetic code`"
        setList(listOf(ConvoEntry.Reply(raw)))

        composeRule.onNodeWithText("synthetic bold and synthetic code").assertIsDisplayed()
        composeRule.onAllNodesWithText(raw).assertCountEquals(0)
        composeRule.onAllNodesWithText("**", substring = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("`", substring = true).assertCountEquals(0)
    }

    @Test
    fun replyBulletListShowsBulletMarkersAndItemText() {
        setList(listOf(ConvoEntry.Reply("- synthetic one\n- synthetic two")))

        composeRule.onNodeWithText("synthetic one").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic two").assertIsDisplayed()
        composeRule.onAllNodesWithText("•", substring = true).assertCountEquals(2)
        composeRule.onAllNodesWithText("- synthetic", substring = true).assertCountEquals(0)
    }

    @Test
    fun replyFencedCodeShowsItsBodyAndNoFenceLine() {
        setList(listOf(ConvoEntry.Reply("```\nsynthetic code line\n```")))

        composeRule.onNodeWithText("synthetic code line").assertIsDisplayed()
        composeRule.onAllNodesWithText("```", substring = true).assertCountEquals(0)
    }

    @Test
    fun replyTableShowsEachCellAndNoDelimiterRow() {
        setList(
            listOf(
                ConvoEntry.Reply(
                    "| synthetic head | synthetic other |\n|---|---|\n| synthetic cell | synthetic value |",
                ),
            ),
        )

        composeRule.onNodeWithText("synthetic head").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic other").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic cell").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic value").assertIsDisplayed()
        composeRule.onAllNodesWithText("|---", substring = true).assertCountEquals(0)
    }

    @Test
    fun promptShowsMarkupVerbatim() {
        val raw = "**synthetic bold** and `synthetic code`\n- synthetic item"
        setList(listOf(ConvoEntry.Prompt(raw)))

        composeRule.onNodeWithText(raw).assertIsDisplayed()
    }

    @Test
    fun timeShowsHourAndMinuteInTheDeviceZone() {
        TimeZone.setDefault(TimeZone.getTimeZone("UTC"))
        setList(listOf(ConvoEntry.Time(1_700_000_000L)))

        composeRule.onNodeWithText("22:13").assertIsDisplayed()
    }

    @Test
    fun toolRowShowsNameAndInputAndResultOnlyAfterTapsToggleIt() {
        setList(
            listOf(
                ConvoEntry.Tool(call("tool-1", name = "Bash", input = "synthetic command", result = "synthetic output line")),
            ),
        )

        composeRule.onNodeWithText("Bash").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic command").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic output line").assertCountEquals(0)

        composeRule.onNodeWithTag("tool-row:tool-1").performClick()
        composeRule.onNodeWithTag("tool-result:tool-1").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic output line").assertIsDisplayed()

        composeRule.onNodeWithTag("tool-row:tool-1").performClick()
        composeRule.onAllNodesWithText("synthetic output line").assertCountEquals(0)
    }

    @Test
    fun doneShowsTickErrorShowsCrossAndRunningShowsNeither() {
        setList(
            listOf(
                ConvoEntry.Tool(call("done-1", result = "synthetic output")),
                ConvoEntry.Tool(call("err-1", result = "synthetic failure", error = true)),
                ConvoEntry.Tool(call("run-1")),
            ),
        )

        composeRule.onAllNodesWithText("✓").assertCountEquals(1)
        composeRule.onAllNodesWithText("✗").assertCountEquals(1)
    }

    @Test
    fun groupOfThreeShowsCountAndHidesItsCallsUntilTapped() {
        setList(
            listOf(
                ConvoEntry.Group(
                    listOf(
                        call("g-a", name = "Read"),
                        call("g-b", name = "Grep"),
                        call("g-c", name = "Read"),
                    ),
                ),
            ),
        )

        composeRule.onNodeWithText("3 tool calls").assertIsDisplayed()
        composeRule.onNodeWithText("Read ×2 · Grep").assertIsDisplayed()
        composeRule.onAllNodesWithTag("tool-row:g-a").assertCountEquals(0)
        composeRule.onAllNodesWithTag("tool-row:g-b").assertCountEquals(0)
        composeRule.onAllNodesWithTag("tool-row:g-c").assertCountEquals(0)

        composeRule.onNodeWithTag("group-row:g-a").performClick()
        composeRule.onNodeWithTag("tool-row:g-a").assertIsDisplayed()
        composeRule.onNodeWithTag("tool-row:g-b").assertIsDisplayed()
        composeRule.onNodeWithTag("tool-row:g-c").assertIsDisplayed()
    }

    @Test
    fun groupTickWhenAllDoneAndCrossWhenAnyErrored() {
        setList(
            listOf(
                ConvoEntry.Group(
                    listOf(
                        call("d-a", result = "synthetic output"),
                        call("d-b", result = "synthetic output"),
                        call("d-c", result = "synthetic output"),
                    ),
                ),
                ConvoEntry.Group(
                    listOf(
                        call("e-a", result = "synthetic output"),
                        call("e-b", result = "synthetic failure", error = true),
                        call("e-c", result = "synthetic output"),
                    ),
                ),
            ),
        )

        composeRule.onAllNodesWithText("✓").assertCountEquals(1)
        composeRule.onAllNodesWithText("✗").assertCountEquals(1)
    }

    @Test
    fun expandedRowStaysExpandedWhenEntriesGainATrailingReply() {
        val entries = mutableStateOf(
            listOf(
                ConvoEntry.Reply("synthetic oldest reply"),
                ConvoEntry.Tool(call("keep-1", result = "synthetic kept output")),
            ),
        )
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value) }
        }

        composeRule.onNodeWithTag("tool-row:keep-1").performClick()
        composeRule.onNodeWithText("synthetic kept output").assertIsDisplayed()

        // The poll's window slides: the oldest entry drops off and a new one arrives, so the
        // expanded row moves from index 1 to index 0. A positional key would lose it.
        composeRule.runOnIdle {
            entries.value = entries.value.drop(1) + ConvoEntry.Reply("synthetic trailing reply")
        }

        composeRule.onNodeWithText("synthetic trailing reply").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic kept output").assertIsDisplayed()
    }

    @Test
    fun footerShowsBelowTheLastEntry() {
        composeRule.setContent {
            MaterialTheme {
                ConversationView(
                    entries = listOf(ConvoEntry.Reply("synthetic last reply")),
                    footer = { Text("synthetic footer") },
                )
            }
        }

        val replyBottom = composeRule.onNodeWithText("synthetic last reply").fetchSemanticsNode().boundsInRoot.bottom
        val footerTop = composeRule.onNodeWithText("synthetic footer").fetchSemanticsNode().boundsInRoot.top
        assertTrue(footerTop >= replyBottom)
    }

    @Test
    fun workingMarkShowsBelowTheLastEntryAndAboveTheFooter() {
        composeRule.setContent {
            MaterialTheme {
                ConversationView(
                    entries = listOf(ConvoEntry.Reply("synthetic last reply")),
                    working = true,
                    footer = { Text("synthetic footer") },
                )
            }
        }

        composeRule.onNodeWithTag("working-mark").assertIsDisplayed()
        val replyBottom = composeRule.onNodeWithText("synthetic last reply").fetchSemanticsNode().boundsInRoot.bottom
        val mark = composeRule.onNodeWithTag("working-mark").fetchSemanticsNode().boundsInRoot
        val footerTop = composeRule.onNodeWithText("synthetic footer").fetchSemanticsNode().boundsInRoot.top
        assertTrue(mark.top >= replyBottom)
        assertTrue(footerTop >= mark.bottom)
    }

    @Test
    fun noWorkingMarkWhenNotWorking() {
        setList(listOf(ConvoEntry.Reply("synthetic last reply")))

        composeRule.onAllNodesWithTag("working-mark").assertCountEquals(0)
    }

    @Test
    fun atTheBottomTheWorkingMarkComesIntoView() {
        val working = mutableStateOf(false)
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                ConversationView(entries = entries.value, listState = state, working = working.value)
            }
        }
        assertEndOfReplyInView(tallReply("synthetic tall A"))

        composeRule.runOnIdle { working.value = true }

        composeRule.waitForIdle()
        composeRule.onNodeWithTag("working-mark").assertIsDisplayed()
        assertFalse("the list can still scroll forward", list.canScrollForward)
        val listBottom = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot.bottom
        val markBottom = composeRule.onNodeWithTag("working-mark").fetchSemanticsNode().boundsInRoot.bottom
        assertTrue("the mark's end is below the list's end", markBottom <= listBottom + 1f)
    }

    private fun replies(from: Int, to: Int): List<ConvoEntry> =
        (from..to).map { ConvoEntry.Reply("synthetic reply %02d".format(it)) }

    @Test
    fun opensAtTheNewestReplyAndNotTheOldest() {
        setList(replies(1, 40))

        composeRule.onNodeWithText("synthetic reply 40").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 01").assertCountEquals(0)
    }

    @Test
    fun opensWithTheFooterDisplayedBelowATallLastReply() {
        val tallLastReply = List(100) { "synthetic line" }.joinToString("\n")
        setList(replies(1, 39) + ConvoEntry.Reply(tallLastReply), footer = { Text("synthetic footer") })

        composeRule.onNodeWithText("synthetic footer").assertIsDisplayed()
    }

    @Test
    fun atTheBottomANewReplyComesIntoView() {
        val entries = mutableStateOf(replies(1, 40))
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value) }
        }

        composeRule.onNodeWithText("synthetic reply 40").assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic reply 41")
        }

        composeRule.onNodeWithText("synthetic reply 41").assertIsDisplayed()
    }

    @Test
    fun afterTheReaderScrollsToTheTopANewReplyLeavesThePositionAlone() {
        val entries = mutableStateOf(replies(1, 40))
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value) }
        }

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic reply 41")
        }

        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 41").assertCountEquals(0)
    }

    @Test
    fun scrolledUpExpandedRowKeepsItsPlaceWhenTheWindowSlides() {
        val entries = mutableStateOf(
            replies(1, 19) + ConvoEntry.Tool(call("keep-1", result = "synthetic kept output")) + replies(21, 50),
        )
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value) }
        }

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(19)
        composeRule.onNodeWithTag("tool-row:keep-1").performClick()
        composeRule.onNodeWithText("synthetic kept output").assertIsDisplayed()
        val before = composeRule.onNodeWithTag("tool-row:keep-1").fetchSemanticsNode().boundsInRoot.top

        // The poll's window slides: the oldest entry drops off and one reply arrives. Without keys
        // the row at the top position becomes a different entry and the expanded row moves up.
        composeRule.runOnIdle {
            entries.value = entries.value.drop(1) + ConvoEntry.Reply("synthetic appended reply")
        }

        val after = composeRule.onNodeWithTag("tool-row:keep-1").fetchSemanticsNode().boundsInRoot.top
        assertEquals(before, after, 1f)
        composeRule.onNodeWithText("synthetic kept output").assertIsDisplayed()
    }

    @Test
    fun atTheBottomASlidWindowShowsTheAppendedReply() {
        val entries = mutableStateOf(replies(1, 50))
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value) }
        }

        composeRule.onNodeWithText("synthetic reply 50").assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value.drop(1) + ConvoEntry.Reply("synthetic reply 51")
        }

        composeRule.onNodeWithText("synthetic reply 51").assertIsDisplayed()
    }

    private lateinit var list: LazyListState

    private fun setHoistedList(entries: MutableState<List<ConvoEntry>>) {
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                ConversationView(entries = entries.value, listState = state)
            }
        }
    }

    private fun setSizedList(
        entries: MutableState<List<ConvoEntry>>,
        height: MutableState<Dp>,
        padding: MutableState<PaddingValues>,
    ) {
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                Box(modifier = Modifier.height(height.value)) {
                    ConversationView(entries = entries.value, listState = state, contentPadding = padding.value)
                }
            }
        }
    }

    private fun tallReply(name: String): String =
        (listOf(name) + List(99) { "synthetic line" }).joinToString("\n")

    /** The list cannot scroll further, and the reply's end is inside the list's bounds. */
    private fun assertEndOfReplyInView(text: String) {
        composeRule.waitForIdle()
        assertFalse("the list can still scroll forward", list.canScrollForward)
        val listBottom = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot.bottom
        val replyBottom = composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot.bottom
        assertTrue("the reply's end is below the list's end", replyBottom <= listBottom + 1f)
    }

    @Test
    fun opensWithTheEndOfATallLastReplyInView() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        setHoistedList(entries)

        assertEndOfReplyInView(tallReply("synthetic tall A"))
    }

    @Test
    fun followsANewTallReplyAfterOpeningOnATallReply() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        setHoistedList(entries)
        assertEndOfReplyInView(tallReply("synthetic tall A"))

        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply(tallReply("synthetic tall B"))
        }

        assertEndOfReplyInView(tallReply("synthetic tall B"))
    }

    @Test
    fun followsANewTallReplyInAWindowSlidByOne() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        setHoistedList(entries)
        assertEndOfReplyInView(tallReply("synthetic tall A"))

        composeRule.runOnIdle {
            entries.value = entries.value.drop(1) + ConvoEntry.Reply(tallReply("synthetic tall B"))
        }

        assertEndOfReplyInView(tallReply("synthetic tall B"))
    }

    @Test
    fun aReaderScrolledToATallReplyTopIsNotMovedByANewReply() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        setHoistedList(entries)

        // Index 39 is already partly visible at the end, so the reader first scrolls to the top.
        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(39)
        composeRule.waitForIdle()
        assertEquals(39, list.firstVisibleItemIndex)
        composeRule.onNodeWithText(tallReply("synthetic tall A")).assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic appended reply")
        }

        composeRule.waitForIdle()
        assertEquals(39, list.firstVisibleItemIndex)
        composeRule.onAllNodesWithText("synthetic appended reply").assertCountEquals(0)
    }

    @Test
    fun shrinkingTheViewportAtTheBottomKeepsTheEndOfTheTallReplyInView() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        val height = mutableStateOf(300.dp)
        setSizedList(entries, height, mutableStateOf(PaddingValues()))
        assertEndOfReplyInView(tallReply("synthetic tall A"))

        composeRule.runOnIdle { height.value = 200.dp }

        assertEndOfReplyInView(tallReply("synthetic tall A"))
    }

    @Test
    fun growingTheTopPaddingAtTheBottomKeepsTheEndOfTheTallReplyInView() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        val padding = mutableStateOf(PaddingValues())
        setSizedList(entries, mutableStateOf(300.dp), padding)
        assertEndOfReplyInView(tallReply("synthetic tall A"))

        composeRule.runOnIdle { padding.value = PaddingValues(top = 80.dp) }

        assertEndOfReplyInView(tallReply("synthetic tall A"))
    }

    @Test
    fun shrinkingTheViewportWhileScrolledToTheTopLeavesTheFirstItemInPlace() {
        val entries = mutableStateOf(replies(1, 39) + ConvoEntry.Reply(tallReply("synthetic tall A")))
        val height = mutableStateOf(300.dp)
        setSizedList(entries, height, mutableStateOf(PaddingValues()))

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.waitForIdle()
        assertEquals(0, list.firstVisibleItemIndex)
        val before = composeRule.onNodeWithText("synthetic reply 01").fetchSemanticsNode().boundsInRoot.top

        composeRule.runOnIdle { height.value = 200.dp }

        composeRule.waitForIdle()
        assertEquals(0, list.firstVisibleItemIndex)
        val after = composeRule.onNodeWithText("synthetic reply 01").fetchSemanticsNode().boundsInRoot.top
        assertEquals(before, after, 1f)
    }

    @Test
    fun repliesWithIdenticalTextAndTimeBothDisplay() {
        setList(
            replies(1, 39) +
                listOf(ConvoEntry.Reply("synthetic twin", 300L), ConvoEntry.Reply("synthetic twin", 300L)),
        )

        composeRule.onAllNodesWithText("synthetic twin").assertCountEquals(2)
    }

    private fun layoutOf(node: SemanticsNodeInteraction): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        node.fetchSemanticsNode().config[SemanticsActions.GetTextLayoutResult].action!!.invoke(results)
        return results.single()
    }

    @Test
    fun eachNoticeShowsItsLabelAndItsText() {
        setList(
            listOf(
                ConvoEntry.Notice("compaction", "", false),
                ConvoEntry.Notice("interrupted", "", false),
                ConvoEntry.Notice("task", "synthetic task line", false),
                ConvoEntry.Notice("teammate", "synthetic summary one\nsynthetic summary two", false),
            ),
        )

        composeRule.onNodeWithTag("notice:compaction").assertIsDisplayed()
        composeRule.onNodeWithText("context compacted").assertIsDisplayed()
        composeRule.onNodeWithText("interrupted").assertIsDisplayed()
        composeRule.onNodeWithText("task").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic task line").assertIsDisplayed()
        composeRule.onNodeWithText("teammate").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic summary one\nsynthetic summary two").assertIsDisplayed()
        composeRule.onAllNodesWithText("✗").assertCountEquals(0)
    }

    @Test
    fun failedTaskNoticeShowsCross() {
        setList(listOf(ConvoEntry.Notice("task", "synthetic failed task", true)))

        composeRule.onNodeWithText("synthetic failed task").assertIsDisplayed()
        composeRule.onNodeWithText("✗").assertIsDisplayed()
    }

    @Test
    fun noticeTextIsShownAsWrittenAndNotAsMarkdown() {
        val raw = "**synthetic** `task` [link](https://example.com)"
        setList(listOf(ConvoEntry.Notice("task", raw, false)))

        composeRule.onNodeWithText(raw).assertIsDisplayed()
    }

    @Test
    fun longTaskNoticeIsCutToTwoLines() {
        // Robolectric's text layout does not soft-wrap, so the lines are broken by hand.
        val long = (1..4).joinToString("\n") { "synthetic task line $it" }
        setList(listOf(ConvoEntry.Notice("task", long, false)))

        val layout = layoutOf(composeRule.onNodeWithText(long))
        assertEquals(2, layout.lineCount)
        assertTrue(layout.hasVisualOverflow)
    }

    @Test
    fun teammateNoticeShowsAtMostThreeLines() {
        val summaries = (1..6).joinToString("\n") { "synthetic summary $it" }
        setList(listOf(ConvoEntry.Notice("teammate", summaries, false)))

        val layout = layoutOf(composeRule.onNodeWithText(summaries))
        assertEquals(3, layout.lineCount)
        assertTrue(layout.hasVisualOverflow)
    }
}
