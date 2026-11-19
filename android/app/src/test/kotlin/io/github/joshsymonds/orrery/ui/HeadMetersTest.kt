package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HeadMetersTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val stats = listOf(
        HostStat("cpu", "42%", 42.0, "ok"),
        HostStat("mem", "8.1 GB", null, "ok"),
        HostStat("disk", "71%", 71.0, "warn"),
        HostStat("temp", "93 C", 93.0, "crit"),
    )

    @Test
    fun drawsTheHostStatsAsMeters() {
        composeRule.setContent { HeadMeters(stats = stats, otherHost = null) }

        composeRule.onNodeWithTag("head-meters").assertIsDisplayed()
        for (stat in stats) {
            composeRule.onNodeWithTag("meter:${stat.label}").assertIsDisplayed()
            composeRule.onNodeWithText(stat.value).assertIsDisplayed()
        }
        composeRule.onNodeWithTag("meter-bar:disk").assertIsDisplayed()
        composeRule.onAllNodesWithTag("meter-bar:mem").assertCountEquals(0)
    }

    @Test
    fun anotherHostDrawsItsNameAndNoMeters() {
        composeRule.setContent { HeadMeters(stats = stats, otherHost = "halyard") }

        composeRule.onNodeWithTag("head-other-host").assertIsDisplayed()
        composeRule.onNodeWithText("halyard").assertIsDisplayed()
        composeRule.onNodeWithText("on another host: no meters").assertIsDisplayed()
        composeRule.onAllNodesWithTag("head-meters").assertCountEquals(0)
        composeRule.onAllNodesWithTag("meter:cpu").assertCountEquals(0)
    }

    @Test
    fun noStatsDrawsNoMeters() {
        composeRule.setContent { HeadMeters(stats = null, otherHost = null) }

        composeRule.onAllNodesWithTag("head-meters").assertCountEquals(0)
        composeRule.onAllNodesWithTag("head-other-host").assertCountEquals(0)
    }

    @Test
    fun eachStatTakesItsSeriesAtOkAndItsLevelAtWarnOrCritNeverPeach() {
        val series = listOf(
            OrreryTokens.Series.one, OrreryTokens.Series.two, OrreryTokens.Series.three,
            OrreryTokens.Series.four, OrreryTokens.Series.five,
        )
        series.forEachIndexed { index, colour -> assertEquals(colour, fillColour(index, "ok")) }
        assertEquals(OrreryTokens.Series.one, fillColour(5, "ok"))
        assertEquals(OrreryTokens.Level.warn, fillColour(1, "warn"))
        assertEquals(OrreryTokens.Level.crit, fillColour(2, "crit"))
        assertEquals(OrreryTokens.Level.warn, valueColour("warn"))
        assertEquals(OrreryTokens.Level.crit, valueColour("crit"))
        for (level in listOf("ok", "warn", "crit", "unknown")) {
            for (index in 0..5) assertNotEquals(OrreryTokens.Role.attention, fillColour(index, level))
            assertNotEquals(OrreryTokens.Role.attention, valueColour(level))
        }
    }
}
