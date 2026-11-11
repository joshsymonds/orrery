package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.neverEqualPolicy
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
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
import io.github.joshsymonds.orrery.api.WaitingItem
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

// Unix seconds; the zone is UTC wherever a test says so.
private const val FIXED_NOW = 1_700_006_399L // 2023-11-14 23:59:59 UTC
private const val NOV_14_2213_UTC = 1_700_000_000L
private const val NOV_14_0800_UTC = 1_699_948_800L
private const val NOV_14_2330_UTC = 1_700_004_600L
private const val NOV_15_0010_UTC = 1_700_007_000L
private const val NOV_9_1030_UTC = 1_699_525_800L

// Made-up unix seconds for the new divider: the seen time sits between the two.
private const val EARLIER_TIME = 1_000L
private const val SEEN_TIME = 2_000L
private const val LATER_TIME = 3_000L
private const val NEW_LABEL = "new"

private fun inUtc() = TimeZone.setDefault(TimeZone.getTimeZone("UTC"))

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
        time: Long = 0L,
    ) = ToolCall(id, name, input, result, error, time)

    private fun setList(
        entries: List<ConvoEntry>,
        footer: (@Composable () -> Unit)? = null,
        working: Boolean = false,
        waiting: List<WaitingItem> = emptyList(),
    ) {
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = entries, footer = footer, working = working, waiting = waiting)
            }
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
    fun clockTimeForTodayShowsHourAndMinuteInTheDeviceZone() {
        inUtc()
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = listOf(ConvoEntry.Time(NOV_14_2213_UTC)), now = FIXED_NOW)
            }
        }

        composeRule.onNodeWithText("22:13").assertIsDisplayed()
    }

    @Test
    fun timeRowShowsJustNowUnderAMinuteAndFollowsTheClockAsItAdvances() {
        inUtc()
        val now = mutableLongStateOf(FIXED_NOW)
        composeRule.setContent {
            MaterialTheme {
                ConversationView(entries = listOf(ConvoEntry.Time(FIXED_NOW - 30)), now = now.longValue)
            }
        }

        composeRule.onNodeWithText("just now").assertIsDisplayed()
        composeRule.runOnIdle { now.longValue = FIXED_NOW + 600 }
        composeRule.onNodeWithText("10 min ago").assertIsDisplayed()
        composeRule.onAllNodesWithText("just now").assertCountEquals(0)
    }

    @Test
    fun timeLabelIsJustNowBelowAMinuteIncludingATimeAfterNow() {
        inUtc()
        assertEquals("just now", timeLabel(FIXED_NOW, FIXED_NOW))
        assertEquals("just now", timeLabel(FIXED_NOW - 59, FIXED_NOW))
        assertEquals("just now", timeLabel(FIXED_NOW + 120, FIXED_NOW))
    }

    @Test
    fun timeLabelCountsWholeMinutesFromSixtySecondsToAnHour() {
        inUtc()
        assertEquals("1 min ago", timeLabel(FIXED_NOW - 60, FIXED_NOW))
        assertEquals("59 min ago", timeLabel(FIXED_NOW - 3_540, FIXED_NOW))
    }

    @Test
    fun timeLabelFromSixtyMinutesOnIsTheClockTimeWhenItIsToday() {
        inUtc()
        assertEquals("22:59", timeLabel(FIXED_NOW - 3_600, FIXED_NOW))
    }

    @Test
    fun timeLabelFromEarlierTodayIsTheClockTime() {
        inUtc()
        assertEquals("08:00", timeLabel(NOV_14_0800_UTC, FIXED_NOW))
    }

    @Test
    fun timeLabelFromAPreviousDayIsTheDateWithoutPadding() {
        inUtc()
        assertEquals("Nov 9", timeLabel(NOV_9_1030_UTC, FIXED_NOW))
    }

    @Test
    fun timeLabelUnderAnHourAcrossMidnightIsRelative() {
        inUtc()
        assertEquals("40 min ago", timeLabel(NOV_14_2330_UTC, NOV_15_0010_UTC))
    }

    @Test
    fun timeLabelUsesTheDeviceZoneToDecideToday() {
        TimeZone.setDefault(TimeZone.getTimeZone("America/Los_Angeles"))
        // 22:13 UTC on Nov 14 is 14:13 in Los Angeles. Three hours on, it is 01:13 Nov 15 in UTC
        // but still 17:13 Nov 14 in Los Angeles, so the clock time shows.
        assertEquals("14:13", timeLabel(NOV_14_2213_UTC, NOV_14_2213_UTC + 10_800))
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

    private fun setSeen(entries: List<ConvoEntry>, seen: Long?) {
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries, seen = seen, now = FIXED_NOW) }
        }
    }

    /** The divider sits between the bottom of entry row [below] - 1 and the top of entry row [below]. */
    private fun assertDividerBefore(below: Int) {
        composeRule.onAllNodesWithTag("new-divider").assertCountEquals(1)
        composeRule.onNodeWithText(NEW_LABEL).assertIsDisplayed()
        val divider = composeRule.onNodeWithTag("new-divider").fetchSemanticsNode().boundsInRoot
        val row = composeRule.onNodeWithTag("entry-row:$below").fetchSemanticsNode().boundsInRoot
        assertTrue("the divider ends above entry row $below", divider.bottom <= row.top + 1f)
        if (below > 0) {
            val previous = composeRule.onNodeWithTag("entry-row:${below - 1}").fetchSemanticsNode().boundsInRoot
            assertTrue("the divider starts below entry row ${below - 1}", divider.top >= previous.bottom - 1f)
        }
    }

    /** Entry rows from [from] on carry the new tint; the rows before it do not. */
    private fun assertTintFromRow(from: Int, count: Int) {
        for (index in 0 until count) {
            val row = composeRule.onNodeWithTag("entry-row:$index")
            if (index >= from) {
                row.assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, NEW_LABEL))
            } else {
                row.assert(SemanticsMatcher.keyNotDefined(SemanticsProperties.StateDescription))
            }
        }
    }

    private fun assertNoDividerAndNoTint(count: Int) {
        composeRule.onAllNodesWithTag("new-divider").assertCountEquals(0)
        assertTintFromRow(from = count, count = count)
    }

    @Test
    fun newDividerSitsBeforeANewerTimeLabelAndTintsTheRowsFromThereOn() {
        setSeen(
            listOf(
                ConvoEntry.Time(EARLIER_TIME),
                ConvoEntry.Prompt("synthetic earlier prompt", EARLIER_TIME),
                ConvoEntry.Time(LATER_TIME),
                ConvoEntry.Reply("synthetic later reply", LATER_TIME),
            ),
            seen = SEEN_TIME,
        )

        assertDividerBefore(below = 2)
        assertTintFromRow(from = 2, count = 4)
    }

    @Test
    fun newDividerSitsBeforeANewerToolAndTintsOlderRowsAfterIt() {
        setSeen(
            listOf(
                ConvoEntry.Reply("synthetic earlier reply", EARLIER_TIME),
                ConvoEntry.Tool(call("new-tool", result = "synthetic output", time = LATER_TIME)),
                ConvoEntry.Reply("synthetic after reply", EARLIER_TIME),
            ),
            seen = SEEN_TIME,
        )

        assertDividerBefore(below = 1)
        assertTintFromRow(from = 1, count = 3)
    }

    @Test
    fun newDividerSitsBeforeAGroupWhoseNewestCallIsNewer() {
        setSeen(
            listOf(
                ConvoEntry.Reply("synthetic earlier reply", EARLIER_TIME),
                ConvoEntry.Group(
                    listOf(
                        call("g-a", time = EARLIER_TIME),
                        call("g-b", time = LATER_TIME),
                        call("g-c", time = EARLIER_TIME),
                    ),
                ),
            ),
            seen = SEEN_TIME,
        )

        assertDividerBefore(below = 1)
        assertTintFromRow(from = 1, count = 2)
    }

    @Test
    fun noDividerWhenTheSeenTimeIsAtOrAfterTheNewestEntry() {
        val entries = listOf(
            ConvoEntry.Reply("synthetic earlier reply", EARLIER_TIME),
            ConvoEntry.Tool(call("seen-tool", time = LATER_TIME)),
        )
        val seen = mutableStateOf<Long?>(LATER_TIME)
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries, seen = seen.value, now = FIXED_NOW) }
        }
        assertNoDividerAndNoTint(count = entries.size)

        composeRule.runOnIdle { seen.value = LATER_TIME + 1_000L }
        assertNoDividerAndNoTint(count = entries.size)
    }

    @Test
    fun noDividerWithoutASeenTime() {
        val entries = listOf(
            ConvoEntry.Reply("synthetic earlier reply", EARLIER_TIME),
            ConvoEntry.Tool(call("unseen-tool", time = LATER_TIME)),
        )
        setSeen(entries, seen = null)

        assertNoDividerAndNoTint(count = entries.size)
    }

    @Test
    fun aTimeZeroEntryIsNeverNewerEvenPastAnEarlierSeenTime() {
        val entries = listOf(ConvoEntry.Reply("synthetic untimed reply"), ConvoEntry.Tool(call("untimed-tool")))
        setSeen(entries, seen = -1L)

        assertNoDividerAndNoTint(count = entries.size)
    }

    @Test
    fun opensAtTheNewDividerAndNotTheNewestEntry() {
        val entries = (1..40).map { ConvoEntry.Reply("synthetic reply %02d".format(it), it * 100L) }
        setSeen(entries, seen = SEEN_TIME)

        // Replies 21 to 40 are newer; the divider rides in item 20 (reply 21), which opens at the top.
        composeRule.onNodeWithTag("new-divider").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 40").assertCountEquals(0)
        val listTop = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot.top
        val dividerTop = composeRule.onNodeWithTag("new-divider").fetchSemanticsNode().boundsInRoot.top
        assertEquals(listTop, dividerTop, 1f)
    }

    @Test
    fun whenEveryEntryIsNewerTheViewOpensAtTheFirstEntry() {
        val entries = (1..40).map { ConvoEntry.Reply("synthetic reply %02d".format(it), LATER_TIME + it) }
        setSeen(entries, seen = SEEN_TIME)

        composeRule.onNodeWithTag("new-divider").assertIsDisplayed()
        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 40").assertCountEquals(0)
    }

    @Test
    fun aLaterEntryOrSeenTimeLeavesTheLandingAlone() {
        val entries = mutableStateOf((1..40).map { ConvoEntry.Reply("synthetic reply %02d".format(it), it * 100L) })
        val seen = mutableStateOf<Long?>(SEEN_TIME)
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                ConversationView(entries = entries.value, listState = state, seen = seen.value, now = FIXED_NOW)
            }
        }
        composeRule.waitForIdle()
        assertEquals(20, list.firstVisibleItemIndex)
        // The reader moves on from the landing; a later entry or seen time must not take them back.
        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(25)
        composeRule.waitForIdle()
        val held = list.firstVisibleItemIndex
        assertNotEquals(20, held)

        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic reply 41", 4_100L)
        }
        composeRule.waitForIdle()
        assertEquals(held, list.firstVisibleItemIndex)

        composeRule.runOnIdle { seen.value = 2_050L }
        composeRule.waitForIdle()
        assertEquals(held, list.firstVisibleItemIndex)
    }

    @Test
    fun aShortLandedConversationFollowsANewTallReplyIntoView() {
        val entries = mutableStateOf(
            listOf(
                ConvoEntry.Reply("synthetic reply 01", EARLIER_TIME),
                ConvoEntry.Reply("synthetic reply 02", LATER_TIME),
            ),
        )
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                ConversationView(entries = entries.value, listState = state, seen = SEEN_TIME, now = FIXED_NOW)
            }
        }
        composeRule.onNodeWithTag("new-divider").assertIsDisplayed()

        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply(tallReply("synthetic tall A"), LATER_TIME + 1_000)
        }

        assertEndOfReplyInView(tallReply("synthetic tall A"))
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

    private fun waitingRow(text: String, label: String): SemanticsNodeInteraction =
        composeRule.onNode(
            hasTestTag("waiting-row") and hasAnyDescendant(hasText(text)) and hasAnyDescendant(hasText(label)),
        )

    @Test
    fun waitingRowsDrawInOrderBelowTheWorkingMarkAndAboveTheFooter() {
        setList(
            listOf(ConvoEntry.Reply("synthetic last reply")),
            footer = { Text("synthetic footer") },
            working = true,
            waiting = listOf(
                WaitingItem("synthetic first queued", 300L, false),
                WaitingItem("synthetic second queued", 301L, false),
            ),
        )

        composeRule.onAllNodesWithTag("waiting-row").assertCountEquals(2)
        composeRule.onAllNodesWithText("waiting").assertCountEquals(2)
        val mark = composeRule.onNodeWithTag("working-mark").fetchSemanticsNode().boundsInRoot
        val first = waitingRow("synthetic first queued", "waiting").fetchSemanticsNode().boundsInRoot
        val second = waitingRow("synthetic second queued", "waiting").fetchSemanticsNode().boundsInRoot
        val footerTop = composeRule.onNodeWithText("synthetic footer").fetchSemanticsNode().boundsInRoot.top
        assertTrue(first.top >= mark.bottom)
        assertTrue(second.top >= first.bottom)
        assertTrue(footerTop >= second.bottom)
    }

    @Test
    fun waitingRowsWithNoWorkingMarkSitBelowTheLastEntry() {
        setList(
            listOf(ConvoEntry.Reply("synthetic last reply")),
            waiting = listOf(
                WaitingItem("synthetic first queued", 300L, false),
                WaitingItem("synthetic second queued", 301L, false),
            ),
        )

        composeRule.onAllNodesWithTag("working-mark").assertCountEquals(0)
        val replyBottom = composeRule.onNodeWithText("synthetic last reply").fetchSemanticsNode().boundsInRoot.bottom
        val first = waitingRow("synthetic first queued", "waiting").fetchSemanticsNode().boundsInRoot
        val second = waitingRow("synthetic second queued", "waiting").fetchSemanticsNode().boundsInRoot
        assertTrue(first.top >= replyBottom)
        assertTrue(second.top >= first.bottom)
    }

    @Test
    fun canceledWaitingItemReadsNotDeliveredAndNotWaiting() {
        setList(
            listOf(ConvoEntry.Reply("synthetic last reply")),
            waiting = listOf(WaitingItem("synthetic cleared", 300L, true)),
        )

        waitingRow("synthetic cleared", "not delivered").assertIsDisplayed()
        composeRule.onAllNodesWithText("waiting").assertCountEquals(0)
    }

    @Test
    fun noWaitingRowWhenNothingIsQueued() {
        setList(listOf(ConvoEntry.Reply("synthetic last reply")), waiting = emptyList())

        composeRule.onAllNodesWithTag("waiting-row").assertCountEquals(0)
    }

    @Test
    fun waitingItemsWithIdenticalTextAndTimeBothDisplay() {
        setList(
            listOf(ConvoEntry.Reply("synthetic last reply")),
            waiting = listOf(
                WaitingItem("synthetic twin", 300L, false),
                WaitingItem("synthetic twin", 300L, false),
            ),
        )

        composeRule.onAllNodesWithText("synthetic twin").assertCountEquals(2)
    }

    @Test
    fun atTheBottomANewWaitingRowComesIntoView() {
        val entries = replies(1, 40)
        val waiting = mutableStateOf(emptyList<WaitingItem>())
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                ConversationView(entries = entries, listState = state, waiting = waiting.value)
            }
        }

        composeRule.onNodeWithText("synthetic reply 40").assertIsDisplayed()
        composeRule.runOnIdle {
            waiting.value = listOf(WaitingItem("synthetic queued", 300L, false))
        }

        composeRule.waitForIdle()
        assertFalse("the list can still scroll forward", list.canScrollForward)
        val listBottom = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot.bottom
        val rowBottom = waitingRow("synthetic queued", "waiting").fetchSemanticsNode().boundsInRoot.bottom
        assertTrue("the new row's end is below the list's end", rowBottom <= listBottom + 1f)
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
    fun aChangedEndRequestBringsTheEndIntoViewFromTheTop() {
        val requests = mutableStateOf(0)
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = replies(1, 40), endRequests = requests.value) }
        }

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.runOnIdle { requests.value += 1 }

        composeRule.onNodeWithText("synthetic reply 40").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 01").assertCountEquals(0)
        assertEndInView("synthetic reply 40")
    }

    @Test
    fun followingResumesAfterAnEndRequestSoANewReplyComesIntoView() {
        val entries = mutableStateOf(replies(1, 40))
        val requests = mutableStateOf(0)
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value, endRequests = requests.value) }
        }

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.runOnIdle { requests.value += 1 }
        composeRule.onNodeWithText("synthetic reply 40").assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic reply 41")
        }

        composeRule.onNodeWithText("synthetic reply 41").assertIsDisplayed()
        assertEndInView("synthetic reply 41")
    }

    @Test
    fun anUnchangedEndRequestMovesNothingWhenANewReplyArrives() {
        val entries = mutableStateOf(replies(1, 40))
        val requests = mutableStateOf(0)
        composeRule.setContent {
            MaterialTheme { ConversationView(entries = entries.value, endRequests = requests.value) }
        }

        composeRule.onNodeWithTag("conversation-list").performScrollToIndex(0)
        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.runOnIdle {
            entries.value = entries.value + ConvoEntry.Reply("synthetic reply 41")
        }

        composeRule.onNodeWithText("synthetic reply 01").assertIsDisplayed()
        composeRule.onAllNodesWithText("synthetic reply 41").assertCountEquals(0)
    }

    /** The text's node ends within the conversation list's visible bottom edge. */
    private fun assertEndInView(text: String) {
        val listBottom = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot.bottom
        val textBottom = composeRule.onNodeWithText(text).fetchSemanticsNode().boundsInRoot.bottom
        assertTrue("the end of '$text' is below the list's end", textBottom <= listBottom + 1f)
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

    private lateinit var scope: CoroutineScope
    private val readWindowHeight = 400.dp

    private fun timedReplies(from: Int, to: Int): List<ConvoEntry> =
        (from..to).map { ConvoEntry.Reply("synthetic reply %02d".format(it), it * 100L) }

    /** A window of [readWindowHeight] that opens at the end of [entries]; the clock is held still. */
    private fun setReadList(
        entries: MutableState<List<ConvoEntry>>,
        reads: MutableList<Long>,
        footer: (@Composable () -> Unit)? = null,
        contentPadding: PaddingValues = PaddingValues(),
    ) {
        composeRule.mainClock.autoAdvance = false
        composeRule.setContent {
            MaterialTheme {
                val state = rememberLazyListState()
                list = state
                scope = rememberCoroutineScope()
                Box(modifier = Modifier.height(readWindowHeight)) {
                    ConversationView(
                        entries = entries.value,
                        listState = state,
                        contentPadding = contentPadding,
                        footer = footer,
                        onRead = { reads.add(it) },
                    )
                }
            }
        }
        composeRule.waitForIdle()
    }

    private fun scrollListBy(pixels: Float) {
        composeRule.runOnIdle { scope.launch { list.scrollBy(pixels) } }
        composeRule.mainClock.advanceTimeByFrame()
        composeRule.waitForIdle()
    }

    @Test
    fun aRowIsReadAtOneSecondOnScreenAndNotBefore() {
        val entries = mutableStateOf(listOf<ConvoEntry>(ConvoEntry.Reply("synthetic dwell reply", 500L)))
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)

        composeRule.mainClock.advanceTimeBy(999, ignoreFrameDuration = true)
        assertTrue("unread at 999 ms", reads.isEmpty())
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertEquals(listOf(500L), reads)
    }

    @Test
    fun rowsScrolledPastWithinASecondAreNeverReported() {
        val entries = mutableStateOf(timedReplies(1, 40))
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)

        composeRule.mainClock.advanceTimeBy(500)
        composeRule.runOnIdle { scope.launch { list.scrollToItem(0) } }
        composeRule.mainClock.advanceTimeBy(1_000)

        assertTrue("the rows at the top, on screen a second, are read", reads.isNotEmpty())
        assertTrue("no row scrolled past within a second is read", reads.all { it < 3_000L })
    }

    @Test
    fun aTallReplyIsReadWhenHalfTheWindowShowsForASecond() {
        val entries = mutableStateOf(listOf<ConvoEntry>(ConvoEntry.Reply(tallReply("synthetic tall"), 700L)))
        val reads = mutableListOf<Long>()
        // The footer leaves 250 dp of the window below the reply: half of it shows.
        setReadList(entries, reads, footer = { Box(modifier = Modifier.height(150.dp)) })

        composeRule.mainClock.advanceTimeBy(999, ignoreFrameDuration = true)
        assertTrue("unread at 999 ms", reads.isEmpty())
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertEquals(listOf(700L), reads)
    }

    @Test
    fun aTallReplyIsNotReadWhenLessThanHalfTheWindowShows() {
        val entries = mutableStateOf(listOf<ConvoEntry>(ConvoEntry.Reply(tallReply("synthetic tall"), 700L)))
        val reads = mutableListOf<Long>()
        // The footer leaves 150 dp of the window below the reply: less than half of it shows.
        setReadList(entries, reads, footer = { Box(modifier = Modifier.height(250.dp)) })

        composeRule.mainClock.advanceTimeBy(2_000)
        assertTrue("less than half shows for two seconds", reads.isEmpty())
    }

    @Test
    fun aTallReplyIsReadAgainstTheWindowBetweenTheContentPadding() {
        val entries = mutableStateOf(listOf<ConvoEntry>(ConvoEntry.Reply(tallReply("synthetic tall"), 700L)))
        val reads = mutableListOf<Long>()
        // The top padding leaves a 300 dp window; the footer leaves 160 dp of it to the reply, over half.
        setReadList(
            entries,
            reads,
            footer = { Box(modifier = Modifier.height(140.dp)) },
            contentPadding = PaddingValues(top = 100.dp),
        )

        composeRule.mainClock.advanceTimeBy(1_000, ignoreFrameDuration = true)
        assertEquals(listOf(700L), reads)
    }

    @Test
    fun aShortRowWithUnderHalfShowingIsNotReported() {
        val entries = mutableStateOf(timedReplies(1, 40))
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)
        composeRule.mainClock.advanceTimeBy(500)

        val rowHeight = composeRule.onNodeWithTag("entry-row:39").fetchSemanticsNode().boundsInRoot.height
        scrollListBy(-0.75f * rowHeight)

        val row = composeRule.onNodeWithTag("entry-row:39").fetchSemanticsNode().boundsInRoot
        val window = composeRule.onNodeWithTag("conversation-list").fetchSemanticsNode().boundsInRoot
        val shown = minOf(row.bottom, window.bottom) - maxOf(row.top, window.top)
        assertTrue("part of the row still shows", shown > 0f)
        assertTrue("less than half of the row shows", shown * 2 < rowHeight)

        composeRule.mainClock.advanceTimeBy(2_000)
        assertFalse("the partly shown row is not read", reads.contains(4_000L))
    }

    @Test
    fun anEqualListRedeliveredMidCountDoesNotRestartIt() {
        // A never-equal policy hands the view a new, equal list on every set; the default would swallow it.
        val entries = mutableStateOf(
            listOf<ConvoEntry>(ConvoEntry.Reply("synthetic dwell reply", 500L)),
            neverEqualPolicy(),
        )
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)

        composeRule.mainClock.advanceTimeBy(600, ignoreFrameDuration = true)
        composeRule.runOnIdle { entries.value = listOf(ConvoEntry.Reply("synthetic dwell reply", 500L)) }
        // The view takes the new, equal list on the recomposition that idling runs; the count must carry on to 1000 ms.
        composeRule.waitForIdle()
        composeRule.mainClock.advanceTimeBy(999 - composeRule.mainClock.currentTime, ignoreFrameDuration = true)
        assertTrue("unread at 999 ms", reads.isEmpty())
        composeRule.mainClock.advanceTimeBy(1, ignoreFrameDuration = true)
        assertEquals(listOf(500L), reads)
    }

    @Test
    fun aTimeLabelAloneIsNeverReported() {
        val entries = mutableStateOf(listOf<ConvoEntry>(ConvoEntry.Time(500L)))
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)

        composeRule.mainClock.advanceTimeBy(3_000)
        assertTrue(reads.isEmpty())
    }

    @Test
    fun readTimesOnlyGrowAndAnUntimedRowIsNeverReported() {
        val entries = mutableStateOf(
            listOf<ConvoEntry>(
                ConvoEntry.Reply("synthetic untimed reply"),
                ConvoEntry.Reply("synthetic early reply", 300L),
                ConvoEntry.Reply("synthetic late reply", 900L),
            ),
        )
        val reads = mutableListOf<Long>()
        setReadList(entries, reads)

        composeRule.mainClock.advanceTimeBy(1_000)
        assertEquals(900L, reads.lastOrNull())
        assertTrue("reads only grow", reads.zipWithNext().all { (earlier, later) -> later > earlier })
        assertFalse("a row with no time is never reported", 0L in reads)
    }
}
