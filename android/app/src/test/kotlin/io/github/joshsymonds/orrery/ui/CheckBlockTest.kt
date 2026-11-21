package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// A step's check rounds, as the conversation and the task's record both draw them: one CheckBlock per round, of the
// round's checks only.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class CheckBlockTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val checks = listOf(
        StepCheckRow(round = 1, name = "lint", ok = false, stdout = "", stderr = ""),
        StepCheckRow(round = 2, name = "test", ok = true, stdout = "", stderr = ""),
        StepCheckRow(round = 2, name = "build", ok = false, stdout = "", stderr = ""),
    )

    private fun setBlock(round: Int, text: String, checks: List<StepCheckRow> = this.checks) {
        composeRule.setContent { CheckBlock(round, text, checks = roundChecks(checks, round)) }
    }

    private fun assertRailTone(tone: String) {
        composeRule.onNodeWithTag("block:checks").assert(
            hasAnyDescendant(hasTestTag("rail") and SemanticsMatcher.expectValue(ShapeToneKey, tone)),
        )
    }

    @Test
    fun roundChecksKeepsTheChecksOfThatRoundOnlyInTheStepsOrder() {
        assertEquals(listOf(CheckResult("test", true), CheckResult("build", false)), roundChecks(checks, 2))
        assertEquals(emptyList<CheckResult>(), roundChecks(checks, 3))
    }

    @Test
    fun theHeadNamesTheRoundAndTheEngine() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithText("CHECKS").assertIsDisplayed()
        composeRule.onNodeWithText("round 2 from the engine").assertIsDisplayed()
    }

    @Test
    fun oneChipPerCheckOfThatRoundOnly() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithText("test").assertIsDisplayed()
        composeRule.onNodeWithText("build").assertIsDisplayed()
        composeRule.onAllNodesWithText("lint").assertCountEquals(0)
    }

    @Test
    fun aFailedCheckChipIsBadToneAndAPassedOneIsOk() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithText("build").assert(SemanticsMatcher.expectValue(ChipToneKey, ChipTone.Bad))
        composeRule.onNodeWithText("test").assert(SemanticsMatcher.expectValue(ChipToneKey, ChipTone.Ok))
    }

    @Test
    fun theRailIsBadWhenACheckOfTheRoundFailed() {
        setBlock(round = 2, text = "Build failed")

        assertRailTone("bad")
    }

    @Test
    fun theRailIsOkWhenEveryCheckOfTheRoundPassedEvenWhenAnotherRoundFailed() {
        val passing = listOf(
            StepCheckRow(round = 1, name = "lint", ok = false, stdout = "", stderr = ""),
            StepCheckRow(round = 2, name = "test", ok = true, stdout = "", stderr = ""),
        )
        setBlock(round = 2, text = "Tests passed", checks = passing)

        assertRailTone("ok")
    }

    @Test
    fun aOneLineNoteShowsAsItIs() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithText("Build failed").assertIsDisplayed()
    }

    @Test
    fun theLinesUnderAMultiLineNoteShowOnlyWhenTheNoteIsTapped() {
        setBlock(round = 2, text = "Build failed\nerror: missing symbol\nsee log")

        composeRule.onNodeWithText("Build failed").assertIsDisplayed()
        composeRule.onAllNodesWithText("error: missing symbol", substring = true).assertCountEquals(0)

        composeRule.onNodeWithText("Build failed").performClick()

        composeRule.onNodeWithText("error: missing symbol", substring = true).assertIsDisplayed()
        composeRule.onNodeWithText("see log", substring = true).assertIsDisplayed()
    }

    @Test
    fun sixChecksWrapOntoMoreLinesAndEveryChipStaysInsideANarrowBlock() {
        val six = listOf("lint", "tests", "build", "types", "owned files", "gate")
            .map { StepCheckRow(round = 2, name = it, ok = true, stdout = "", stderr = "") }
        composeRule.setContent {
            Box(modifier = Modifier.width(240.dp)) { CheckBlock(2, "All six pass.", checks = roundChecks(six, 2)) }
        }

        val block = composeRule.onNodeWithTag("block:checks").fetchSemanticsNode().boundsInRoot
        val chips = composeRule.onAllNodes(SemanticsMatcher.keyIsDefined(ChipToneKey)).fetchSemanticsNodes()
            .map { it.boundsInRoot }
        assertEquals(6, chips.size)
        chips.forEach { assertTrue(it.right <= block.right) }
        assertTrue(chips.map { it.top }.distinct().size > 1)
    }

    @Test
    fun aPassedCheckChipCarriesTheCheckMarkAndAFailedOneTheCross() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithTag("check:test").assert(hasAnyDescendant(hasTestTag("icon:check")))
        composeRule.onNodeWithTag("check:build").assert(hasAnyDescendant(hasTestTag("icon:cross")))
    }
}
