package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val BAND_TAG = "band"
private const val BAND_GOAL_TAG = "band-goal"
private const val BAND_HIDE_TAG = "band-hide"
private const val BAND_MORE_TAG = "band-more"
private const val BAND_LINE_PREFIX = "band-line:"

private val asks = listOf(
    AskLine(epicId = "epic-one", slug = "rota-export", text = "asks: Bump the embedded tz database?", way = AskWay.ANSWER),
    AskLine(epicId = "epic-two", slug = "notices-usable", text = "is parked: R5 is already met", way = AskWay.DECIDE),
    AskLine(epicId = "epic-three", slug = "orbit-labels", text = "release step 2 failed: CI red", way = AskWay.OPEN),
    AskLine(epicId = "epic-four", slug = "harbour-map", text = "blocked by another epic", way = AskWay.OPEN),
)

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class BandViewTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private fun setBand(goal: String, lines: List<AskLine>, onOpen: (String) -> Unit = {}, onHide: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme { Band(goal = goal, lines = lines, onOpen = onOpen, onHide = onHide) }
        }
    }

    @Test
    fun goalAloneDrawsGoalAndNoLines() {
        setBand("synthetic goal", emptyList())

        composeRule.onNodeWithTag(BAND_TAG).assertIsDisplayed()
        composeRule.onNodeWithTag(BAND_GOAL_TAG).assertIsDisplayed()
        composeRule.onNodeWithText("GOAL", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText("synthetic goal", useUnmergedTree = true).assertIsDisplayed()
        asks.forEach { line ->
            composeRule.onAllNodesWithTag(BAND_LINE_PREFIX + line.epicId).assertCountEquals(0)
        }
        composeRule.onAllNodesWithTag(BAND_MORE_TAG).assertCountEquals(0)
    }

    @Test
    fun noGoalAndNoLinesDrawsNoBand() {
        setBand("", emptyList())

        composeRule.onAllNodesWithTag(BAND_TAG).assertCountEquals(0)
    }

    @Test
    fun eachLineDrawsItsSlugTextAndWay() {
        setBand("synthetic goal", asks.take(3))

        asks.take(3).forEach { line ->
            composeRule.onNodeWithTag(BAND_LINE_PREFIX + line.epicId).assertIsDisplayed()
            composeRule.onNodeWithText(line.slug, useUnmergedTree = true).assertIsDisplayed()
            composeRule.onNodeWithText(line.text, useUnmergedTree = true).assertIsDisplayed()
            composeRule.onNodeWithText(line.way.word + " ›", useUnmergedTree = true).assertIsDisplayed()
        }
    }

    @Test
    fun aLineOpensItsOwnEpic() {
        val opened = mutableListOf<String>()
        setBand("synthetic goal", asks.take(3), onOpen = { opened += it })

        composeRule.onNodeWithTag(BAND_LINE_PREFIX + "epic-two").performClick()

        assertEquals(listOf("epic-two"), opened)
    }

    @Test
    fun fourLinesDrawThreeAndTheRestAsMore() {
        setBand("synthetic goal", asks)

        composeRule.onAllNodesWithTag(BAND_LINE_PREFIX + "epic-one").assertCountEquals(1)
        composeRule.onAllNodesWithTag(BAND_LINE_PREFIX + "epic-three").assertCountEquals(1)
        composeRule.onAllNodesWithTag(BAND_LINE_PREFIX + "epic-four").assertCountEquals(0)
        composeRule.onNodeWithText("and 1 more ask", useUnmergedTree = true).assertIsDisplayed()
        composeRule.onNodeWithText("harbour-map", useUnmergedTree = true).assertIsDisplayed()
    }

    @Test
    fun hideIsDrawnWithAGoalAndCallsOnHide() {
        var hidden = 0
        setBand("synthetic goal", asks.take(1), onHide = { hidden++ })

        composeRule.onNodeWithTag(BAND_HIDE_TAG).assertIsDisplayed()
        composeRule.onNodeWithContentDescription("Hide the header").assertIsDisplayed()
        composeRule.onNodeWithTag(BAND_HIDE_TAG).performClick()

        assertEquals(1, hidden)
    }

    @Test
    fun hideIsNotDrawnWithoutAGoal() {
        setBand("", asks.take(2))

        composeRule.onAllNodesWithTag(BAND_HIDE_TAG).assertCountEquals(0)
        composeRule.onNodeWithTag(BAND_TAG).assertIsDisplayed()
    }
}
