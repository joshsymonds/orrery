package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.abs
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class HostMetersTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private val mixed = listOf(
        HostStat("cpu", "42%", 42.0, "ok"),
        HostStat("mem", "8.1 GB", null, "ok"),
        HostStat("disk", "71%", 71.0, "warn"),
        HostStat("temp", "93 C", 93.0, "crit"),
    )

    @Test
    fun showsEveryStatLabelUppercasedAndValue() {
        composeRule.setContent { HostMeters(mixed) }

        composeRule.onNodeWithTag("host-meters").assertIsDisplayed()
        for (stat in mixed) {
            composeRule.onNodeWithTag("meter:${stat.label}").assertIsDisplayed()
            composeRule.onNodeWithText(stat.label.uppercase()).assertIsDisplayed()
            composeRule.onNodeWithText(stat.value).assertIsDisplayed()
        }
    }

    @Test
    fun barAppearsOnlyForStatsWithPercent() {
        composeRule.setContent { HostMeters(mixed) }

        composeRule.onNodeWithTag("meter-bar:cpu").assertIsDisplayed()
        composeRule.onNodeWithTag("meter-bar:disk").assertIsDisplayed()
        composeRule.onNodeWithTag("meter-bar:temp").assertIsDisplayed()
        composeRule.onAllNodesWithTag("meter-bar:mem").assertCountEquals(0)
    }

    @Test
    fun fillIsPercentOfTrackWidth() {
        composeRule.setContent { HostMeters(listOf(HostStat("cpu", "25%", 25.0, "ok"))) }

        val track = composeRule.onNodeWithTag("meter-bar:cpu").fetchSemanticsNode().boundsInRoot
        val fill = composeRule.onNodeWithTag("meter-fill:cpu").fetchSemanticsNode().boundsInRoot

        assertTrue("track must have a width", track.width > 0f)
        assertTrue(
            "25% fill must be a quarter of the track: fill=${fill.width} track=${track.width}",
            abs(fill.width - track.width * 0.25f) <= 1f,
        )
    }

    @Test
    fun eachPartTakesItsSeriesAtOkAndItsLevelAtWarnOrCrit() {
        composeRule.setContent {
            HostMeters(
                listOf(
                    HostStat("cpu", "50%", 50.0, "ok"),
                    HostStat("disk", "60%", 60.0, "warn"),
                    HostStat("temp", "70%", 70.0, "crit"),
                ),
            )
        }

        assertColour(hasTestTag("meter-fill:cpu"), OrreryTokens.Series.one)
        assertColour(hasTestTag("meter-fill:disk"), OrreryTokens.Level.warn)
        assertColour(hasTestTag("meter-fill:temp"), OrreryTokens.Level.crit)
        assertColour(hasText("CPU"), OrreryTokens.Series.one)
        assertColour(hasText("DISK"), OrreryTokens.Level.warn)
        assertColour(hasText("TEMP"), OrreryTokens.Level.crit)
        assertColour(hasText("50%"), OrreryTokens.Ink.one)
        assertColour(hasText("60%"), OrreryTokens.Level.warn)
        assertColour(hasText("70%"), OrreryTokens.Level.crit)
    }

    // The colour the meters drew the part [matcher] names in, as the part's node carries it.
    private fun assertColour(matcher: SemanticsMatcher, expected: Color) {
        composeRule.onNode(matcher, useUnmergedTree = true).assert(SemanticsMatcher.expectValue(MeterColourKey, expected))
    }
}
