package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import java.time.Instant
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// 02:00 UTC on Monday 16 November 2026 is 18:00 on Sunday 15 November in Los Angeles, where the clock is UTC-8.
private val INSTANT = Instant.parse("2026-11-16T02:00:00Z").epochSecond
private val LOS_ANGELES = ZoneId.of("America/Los_Angeles")
private const val SUMMARY = "Summary of the session so far"

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class ShapesRulesTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun compactionRuleDrawsCompactedWithItsTriggerAndTokens() {
        composeRule.setContent {
            CompactionRule(trigger = "auto", tokens = 611_000L, expanded = false, onToggle = {}) {}
        }

        composeRule.onNodeWithTag("rule:compaction")
            .assertTextContains("Compacted".inLabelCase())
            .assertTextContains("auto · 611k tokens")
    }

    @Test
    fun compactionRuleOpensItsSummaryOnATapAndClosesOnTheNextTap() {
        composeRule.setContent {
            var expanded by remember { mutableStateOf(false) }
            CompactionRule(
                trigger = "auto",
                tokens = 611_000L,
                expanded = expanded,
                onToggle = { expanded = !expanded },
            ) {
                Text(SUMMARY)
            }
        }

        composeRule.onAllNodesWithText(SUMMARY).assertCountEquals(0)
        composeRule.onNodeWithTag("rule:compaction").performClick()
        composeRule.onNodeWithText(SUMMARY).assertIsDisplayed()
        composeRule.onNodeWithTag("rule:compaction").performClick()
        composeRule.onAllNodesWithText(SUMMARY).assertCountEquals(0)
    }

    @Test
    fun interruptedRuleDrawsItsLabelAndTimeInTheHaltTone() {
        composeRule.setContent { InterruptedRule(seconds = INSTANT, zone = LOS_ANGELES) }

        composeRule.onNodeWithTag("rule:interrupted")
            .assertTextContains("Interrupted by you".inLabelCase())
            .assertTextContains("18:00")
        assertEquals(OrreryTokens.Role.halt, toneOf("rule:interrupted"))
    }

    @Test
    fun newRuleDrawsItsLabelInTheNewTone() {
        composeRule.setContent { NewRule() }

        composeRule.onNodeWithTag("rule:new").assertTextContains("New since you looked".inLabelCase())
        assertEquals(OrreryTokens.Role.new, toneOf("rule:new"))
    }

    @Test
    fun dayRuleDrawsTheDayOfAFixedInstantInItsZone() {
        composeRule.setContent { DayRule(seconds = INSTANT, zone = LOS_ANGELES) }

        composeRule.onNodeWithTag("rule:day").assertTextContains("Sunday 15 November".inLabelCase())
    }

    @Test
    fun theRulesDrawNoRail() {
        composeRule.setContent {
            Column {
                CompactionRule(trigger = "auto", tokens = 611_000L, expanded = false, onToggle = {}) {}
                InterruptedRule(seconds = INSTANT, zone = LOS_ANGELES)
                NewRule()
                DayRule(seconds = INSTANT, zone = LOS_ANGELES)
            }
        }

        composeRule.onNodeWithTag("rule:compaction").assertIsDisplayed()
        composeRule.onNodeWithTag("rule:interrupted").assertIsDisplayed()
        composeRule.onNodeWithTag("rule:new").assertIsDisplayed()
        composeRule.onNodeWithTag("rule:day").assertIsDisplayed()
        composeRule.onAllNodesWithTag("rail").assertCountEquals(0)
    }

    /** The colour the one rule's label is drawn in, read from its node. */
    private fun toneOf(tag: String): Color =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[RuleToneKey]
}
