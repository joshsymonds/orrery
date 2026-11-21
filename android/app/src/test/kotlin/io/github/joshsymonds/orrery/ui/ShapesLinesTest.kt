package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertContentDescriptionEquals
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertWidthIsEqualTo
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private val READ = ToolCall(id = "call-1", name = "Read", input = "internal/roster/handler.go", result = "package roster", error = false)
private val READ_TWO = READ.copy(id = "call-2", input = "internal/roster/query.go")
private val FAILED = ToolCall(id = "call-3", name = "Bash", input = "go vet ./...", result = "vet: unused variable", error = true)
private val RUNNING = ToolCall(id = "call-4", name = "Edit", input = "internal/roster/query.go", result = null, error = false)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShapesLinesTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun toolLineDrawsItsGlyphNameInputAndDoneMarkAndFoldsItsResult() {
        setLine { expanded, onToggle -> ToolLine(READ, expanded = expanded, onToggle = onToggle) }

        composeRule.onNodeWithTag("line:tool")
            .assertTextContains("Read")
            .assertTextContains("internal/roster/handler.go")
            .assertTextContains("✓")
        composeRule.onNodeWithTag("glyph", useUnmergedTree = true).assertContentDescriptionEquals("doc")
        composeRule.onAllNodesWithText("package roster", useUnmergedTree = true).assertCountEquals(0)
        assertEquals(OrreryTokens.Line.firm, threadColour())
    }

    @Test
    fun toolLineOfAFailedCallMarksItAndOpensItsResult() {
        setLine { expanded, onToggle -> ToolLine(FAILED, expanded = expanded, onToggle = onToggle) }

        composeRule.onNodeWithTag("line:tool")
            .assertTextContains("Bash")
            .assertTextContains("✗")
        composeRule.onNodeWithTag("line:tool").performClick()
        composeRule.onNodeWithText("vet: unused variable", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun toolLineOfARunningCallDrawsRunningAndHasNoResultOrDoneMark() {
        setLine { expanded, onToggle -> ToolLine(RUNNING, running = true, expanded = expanded, onToggle = onToggle) }

        composeRule.onNodeWithTag("line:tool")
            .assertTextContains("Edit")
            .assertTextContains("running")
        composeRule.onAllNodesWithText("✓", useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("✗", useUnmergedTree = true).assertCountEquals(0)
        assertEquals(OrreryTokens.Role.live, threadColour())
    }

    @Test
    fun toolLineWithCountsDrawsItsAddedAndRemovedLinesInTheOkAndBadTones() {
        val edit = READ.copy(id = "call-5", name = "Edit", input = "internal/roster/query.go", added = 12, removed = 3)
        setLine { expanded, onToggle -> ToolLine(edit, expanded = expanded, onToggle = onToggle) }

        composeRule.onNodeWithTag("line:tool")
            .assertTextContains("internal/roster/query.go")
            .assertTextContains("+12")
            .assertTextContains("−3")
        composeRule.onNodeWithText("+12", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText("−3", useUnmergedTree = true).assertIsDisplayed()
        assertEquals(OrreryTokens.Role.ok, textColourOf("+12"))
        assertEquals(OrreryTokens.Role.bad, textColourOf("−3"))
    }

    @Test
    fun toolLineWithoutCountsDrawsNeitherPlusNorMinus() {
        setLine { expanded, onToggle -> ToolLine(READ, expanded = expanded, onToggle = onToggle) }

        composeRule.onNodeWithTag("line:tool").assertTextContains("internal/roster/handler.go")
        composeRule.onAllNodesWithText("+", substring = true, useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("−", substring = true, useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun toolRunOfThreeCallsIsOneLineThatOpensToItsCalls() {
        val calls = listOf(READ, READ_TWO, FAILED)
        setLine { expanded, onToggle -> ToolRun(calls, expanded = expanded, onToggle = onToggle) }

        composeRule.onAllNodesWithTag("line:run", useUnmergedTree = true).assertCountEquals(1)
        composeRule.onNodeWithTag("line:run")
            .assertTextContains("3 tool calls")
            .assertTextContains("Read ×2, Bash")
        composeRule.onAllNodesWithTag("dot", useUnmergedTree = true).assertCountEquals(3)
        composeRule.onAllNodesWithTag("line:tool", useUnmergedTree = true).assertCountEquals(0)

        composeRule.onNodeWithTag("line:run").performClick()
        composeRule.onAllNodesWithTag("line:tool", useUnmergedTree = true).assertCountEquals(3)
    }

    @Test
    fun thinkingLineDrawsThinkingAndOpensToItsText() {
        setLine { expanded, onToggle ->
            ThinkingLine("Check the handler before guessing.", key = "thinking-1", expanded = expanded, onToggle = onToggle)
        }

        composeRule.onNodeWithTag("line:thinking").assertTextContains("thinking")
        composeRule.onAllNodesWithText("Check the handler before guessing.", useUnmergedTree = true).assertCountEquals(0)

        composeRule.onNodeWithTag("line:thinking").performClick()
        composeRule.onNodeWithText("Check the handler before guessing.", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun briefFoldDrawsBriefFromTheEngineAndOpensToItsText() {
        setLine { expanded, onToggle ->
            EngineFold(brief = true, text = "Implement the week query.\nDo not change go.mod.", key = "brief-1", expanded = expanded, onToggle = onToggle)
        }

        composeRule.onNodeWithTag("line:fold")
            .assertTextContains("Brief")
            .assertTextContains("from the engine")
        composeRule.onAllNodesWithText("Do not change go.mod.", substring = true, useUnmergedTree = true).assertCountEquals(0)

        composeRule.onNodeWithTag("line:fold").performClick()
        composeRule.onNodeWithText("Do not change go.mod.", substring = true, useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun engineFoldDrawsEngineWithItsFirstLineAndOpensToTheWholeText() {
        setLine { expanded, onToggle ->
            EngineFold(
                brief = false,
                text = "Round 2: the owned-files check failed. Revert go.mod.\nThe other checks passed.",
                key = "engine-1",
                expanded = expanded,
                onToggle = onToggle,
            )
        }

        composeRule.onNodeWithTag("line:fold")
            .assertTextContains("Engine")
            .assertTextContains("Round 2: the owned-files check failed. Revert go.mod.")
        composeRule.onAllNodesWithText("The other checks passed.", substring = true, useUnmergedTree = true).assertCountEquals(0)

        composeRule.onNodeWithTag("line:fold").performClick()
        composeRule.onNodeWithText("The other checks passed.", substring = true, useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun agentTaskLineNamesItsTypeDescriptionAndFinishedEnd() {
        composeRule.setContent {
            TaskLine(type = "agent", subagentType = "Explore", description = "Find every caller of keeperFor", status = "done")
        }

        composeRule.onNodeWithTag("line:task")
            .assertTextContains("Explore")
            .assertTextContains("Find every caller of keeperFor")
            .assertTextContains("finished")
    }

    @Test
    fun shellTaskLineNamesShellAndItsStoppedEnd() {
        composeRule.setContent {
            TaskLine(type = "shell", subagentType = "", description = "pnpm dev --port 5173", status = "stopped")
        }

        composeRule.onNodeWithTag("line:task")
            .assertTextContains("shell")
            .assertTextContains("pnpm dev --port 5173")
            .assertTextContains("stopped")
    }

    @Test
    fun failedAgentTaskLineEndsWithFailed() {
        composeRule.setContent {
            TaskLine(type = "agent", subagentType = "reviewer", description = "Review the query change", status = "failed")
        }

        composeRule.onNodeWithTag("line:task")
            .assertTextContains("reviewer")
            .assertTextContains("failed")
    }

    @Test
    fun runningTaskLineEndsWithRunning() {
        composeRule.setContent {
            TaskLine(type = "agent", subagentType = "Explore", description = "Find every caller of keeperFor", status = "running")
        }

        composeRule.onNodeWithTag("line:task").assertTextContains("running")
    }

    @Test
    fun errorNoticeLineDrawsItsLabelTextAndFailedMark() {
        composeRule.setContent {
            NoticeLine(ConvoEntry.Notice(sort = "task", text = "The API refused the request.", error = true))
        }

        composeRule.onNodeWithTag("line:notice")
            .assertTextContains("TASK")
            .assertTextContains("The API refused the request.")
            .assertTextContains("✗")
        assertEquals(OrreryTokens.Role.bad, textColourOf("The API refused the request."))
    }

    @Test
    fun plainNoticeLineDrawsNoFailedMark() {
        composeRule.setContent {
            NoticeLine(ConvoEntry.Notice(sort = "task", text = "Task 3 of 4 is complete.", error = false))
        }

        composeRule.onNodeWithTag("line:notice").assertTextContains("TASK")
        composeRule.onAllNodesWithText("✗", useUnmergedTree = true).assertCountEquals(0)
        assertEquals(OrreryTokens.Ink.three, textColourOf("Task 3 of 4 is complete."))
    }

    @Test
    fun noLineDrawsARailAndEachLineHangsOnAHairlineThread() {
        composeRule.setContent {
            Column {
                ToolLine(READ, expanded = emptySet(), onToggle = {})
                ToolRun(listOf(READ, READ_TWO, FAILED), expanded = emptySet(), onToggle = {})
                ThinkingLine("Check the handler.", key = "thinking-1", expanded = emptySet(), onToggle = {})
                TaskLine(type = "shell", subagentType = "", description = "pnpm dev", status = "stopped")
                NoticeLine(ConvoEntry.Notice(sort = "task", text = "Task 3 of 4 is complete.", error = false))
                EngineFold(brief = true, text = "Implement the week query.", key = "brief-1", expanded = emptySet(), onToggle = {})
            }
        }

        composeRule.onAllNodesWithTag("rail", useUnmergedTree = true).assertCountEquals(0)
        composeRule.onAllNodesWithTag("line-thread", useUnmergedTree = true).assertCountEquals(6)
        for (index in 0 until 6) {
            composeRule.onAllNodesWithTag("line-thread", useUnmergedTree = true)[index]
                .assertWidthIsEqualTo(OrreryTokens.Size.hairline)
        }
    }

    @Test
    fun toolGlyphKindOfNamesTheKindOfEachTool() {
        assertEquals("doc", toolGlyphKindOf("Read"))
        assertEquals("edit", toolGlyphKindOf("MultiEdit"))
        assertEquals("shell", toolGlyphKindOf("Bash"))
        assertEquals("search", toolGlyphKindOf("Glob"))
        assertEquals("agent", toolGlyphKindOf("Task"))
        assertEquals("gear", toolGlyphKindOf("mcp__orrery__step_start"))
    }

    private fun setLine(line: @Composable (expanded: Set<String>, onToggle: (String) -> Unit) -> Unit) {
        composeRule.setContent {
            var expanded by remember { mutableStateOf(emptySet<String>()) }
            line(expanded) { id -> expanded = if (id in expanded) expanded - id else expanded + id }
        }
    }

    /** The colour the one line's thread is drawn in. */
    private fun threadColour(): Color =
        composeRule.onNodeWithTag("line-thread", useUnmergedTree = true).fetchSemanticsNode().config[LineColourKey]

    /** The colour the one text that reads [text] is drawn in. */
    private fun textColourOf(text: String): Color {
        val layouts = mutableListOf<TextLayoutResult>()
        composeRule.onNodeWithText(text, useUnmergedTree = true).fetchSemanticsNode().config
            .getOrNull(SemanticsActions.GetTextLayoutResult)?.action?.invoke(layouts)
        return layouts.single().layoutInput.style.color
    }
}
