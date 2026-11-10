package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import org.junit.After
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
}
