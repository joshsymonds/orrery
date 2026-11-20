package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

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
        composeRule.setContent { CheckBlock(text = text, round = round, checks = checks) }
    }

    @Test
    fun capNamesTheRoundAndTheEngine() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithText("Checks · round 2", substring = true, ignoreCase = true).assertIsDisplayed()
        composeRule.onNodeWithText("from the engine").assertIsDisplayed()
    }

    @Test
    fun oneChipPerCheckOfThatRoundOnlyWithItsOutcomeMark() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithTag("check-chip:test").assertTextEquals("test ✓")
        composeRule.onNodeWithTag("check-chip:build").assertTextEquals("build ✗")
        composeRule.onAllNodesWithTag("check-chip:lint").assertCountEquals(0)
    }

    @Test
    fun aFailedCheckChipIsBadToneAndAPassedOneIsNeutral() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithTag("check-chip:build").assert(SemanticsMatcher.expectValue(ChipToneKey, ChipTone.Bad))
        composeRule.onNodeWithTag("check-chip:test").assert(SemanticsMatcher.expectValue(ChipToneKey, ChipTone.Neutral))
    }

    @Test
    fun theEdgeIsBadWhenACheckOfTheRoundFailed() {
        setBlock(round = 2, text = "Build failed")

        composeRule.onNodeWithTag("check-block:2")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "bad"))
    }

    @Test
    fun theEdgeIsOkWhenEveryCheckOfTheRoundPassedEvenWhenAnotherRoundFailed() {
        val passing = listOf(
            StepCheckRow(round = 1, name = "lint", ok = false, stdout = "", stderr = ""),
            StepCheckRow(round = 2, name = "test", ok = true, stdout = "", stderr = ""),
        )
        setBlock(round = 2, text = "Tests passed", checks = passing)

        composeRule.onNodeWithTag("check-block:2")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "ok"))
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

        composeRule.onNodeWithTag("check-note").performClick()

        composeRule.onNodeWithText("error: missing symbol", substring = true).assertIsDisplayed()
        composeRule.onNodeWithText("see log", substring = true).assertIsDisplayed()
    }
}
