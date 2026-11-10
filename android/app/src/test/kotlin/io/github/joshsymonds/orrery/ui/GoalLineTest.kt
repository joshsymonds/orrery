package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val GOAL_LINE_TAG = "goal-line"
private const val GOAL_TEXT_TAG = "goal-text"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class GoalLineTest {
    @get:Rule
    val composeRule = createComposeRule()

    // Robolectric's text layout does not soft-wrap, so the goal's extra lines are hard breaks.
    private val longGoal = "synthetic goal, first line\nsynthetic goal, second line\nsynthetic goal, third line"

    private fun setGoal(goal: String) {
        composeRule.setContent {
            MaterialTheme { GoalLine(goal) }
        }
    }

    private fun goalLayout(): TextLayoutResult {
        val node = composeRule.onNodeWithTag(GOAL_TEXT_TAG, useUnmergedTree = true).fetchSemanticsNode()
        val results = mutableListOf<TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(results)
        return results.single()
    }

    private fun stateIs(state: String) = SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, state)

    @Test
    fun goalShowsItsLabelAndText() {
        setGoal("synthetic goal")

        composeRule.onNodeWithTag(GOAL_LINE_TAG).assertIsDisplayed()
        composeRule.onNodeWithText("goal", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText("synthetic goal", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun emptyGoalDrawsNothing() {
        setGoal("")

        composeRule.onAllNodesWithTag(GOAL_LINE_TAG).assertCountEquals(0)
    }

    @Test
    fun longGoalIsCutToOneLine() {
        setGoal(longGoal)

        composeRule.onNodeWithTag(GOAL_LINE_TAG).assert(stateIs("collapsed"))
        val layout = goalLayout()
        assertEquals(1, layout.lineCount)
        assertTrue(layout.hasVisualOverflow)
    }

    @Test
    fun tapShowsTheWholeGoalAndASecondTapFoldsIt() {
        setGoal(longGoal)

        composeRule.onNodeWithTag(GOAL_LINE_TAG).performClick()
        composeRule.onNodeWithTag(GOAL_LINE_TAG).assert(stateIs("expanded"))
        val open = goalLayout()
        assertEquals(3, open.lineCount)
        assertFalse(open.hasVisualOverflow)

        composeRule.onNodeWithTag(GOAL_LINE_TAG).performClick()
        composeRule.onNodeWithTag(GOAL_LINE_TAG).assert(stateIs("collapsed"))
        assertEquals(1, goalLayout().lineCount)
    }

    @Test
    fun aNewGoalStartsFolded() {
        val goal = mutableStateOf(longGoal)
        composeRule.setContent {
            MaterialTheme { GoalLine(goal.value) }
        }
        composeRule.onNodeWithTag(GOAL_LINE_TAG).performClick()
        composeRule.onNodeWithTag(GOAL_LINE_TAG).assert(stateIs("expanded"))

        goal.value = "$longGoal and then some"
        composeRule.waitForIdle()

        composeRule.onNodeWithTag(GOAL_LINE_TAG).assert(stateIs("collapsed"))
    }

    @Test
    fun goalThatGoesEmptyLeavesNothing() {
        val goal = mutableStateOf("synthetic goal")
        composeRule.setContent {
            MaterialTheme { GoalLine(goal.value) }
        }
        composeRule.onNodeWithTag(GOAL_LINE_TAG).assertIsDisplayed()

        goal.value = ""
        composeRule.waitForIdle()

        composeRule.onAllNodesWithTag(GOAL_LINE_TAG).assertCountEquals(0)
    }
}
