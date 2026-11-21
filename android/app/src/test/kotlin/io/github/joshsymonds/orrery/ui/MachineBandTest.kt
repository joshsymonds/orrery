package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getUnclippedBoundsInRoot
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
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
class MachineBandTest {
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
        composeRule.setContent { MachineBand(host = "halyard", stats = stats, otherHost = null) }

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
        composeRule.setContent { MachineBand(host = "den", stats = stats, otherHost = "halyard") }

        composeRule.onNodeWithTag("head-other-host").assertIsDisplayed()
        composeRule.onNodeWithText("halyard").assertIsDisplayed()
        composeRule.onNodeWithText("on another host: no meters").assertIsDisplayed()
        composeRule.onAllNodesWithTag("head-meters").assertCountEquals(0)
        composeRule.onAllNodesWithTag("meter:cpu").assertCountEquals(0)
    }

    @Test
    fun noStatsDrawsNoMeters() {
        composeRule.setContent { MachineBand(host = "halyard", stats = null, otherHost = null) }

        composeRule.onAllNodesWithTag("head-meters").assertCountEquals(0)
        composeRule.onAllNodesWithTag("head-other-host").assertCountEquals(0)
    }

    // The stats web's A4 band draws for the machine the owner reads: two with no bar and three with one.
    private val machine = listOf(
        HostStat("net", "↓551K ↑72K", null, "ok"),
        HostStat("cpu", "93% 25/32", 93.0, "warn"),
        HostStat("ram", "19G", 30.0, "ok"),
        HostStat("disk io", "↓6K ↑2M", null, "ok"),
        HostStat("disk", "50%", 50.0, "ok"),
    )

    @Test
    fun theBandLeadsWithTheHostLabelAndItsIcon() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        composeRule.onNode(hasTestTag("machine-host") and hasAnyAncestor(hasTestTag("machine-band")))
            .assertTextEquals("vermission")
        composeRule.onNode(hasTestTag("icon:host") and hasAnyAncestor(hasTestTag("machine-host")), useUnmergedTree = true)
            .assertIsDisplayed()
        val host = composeRule.onNodeWithTag("machine-host").getUnclippedBoundsInRoot()
        val first = composeRule.onNodeWithTag("meter:net").getUnclippedBoundsInRoot()
        assertTrue("the host label comes before the first meter", host.right <= first.left)
    }

    @Test
    fun aHostWithNoStatsDrawsItsLabelAlone() {
        composeRule.setContent { MachineBand(host = "halyard", stats = emptyList(), otherHost = null) }

        composeRule.onNodeWithTag("machine-host").assertTextEquals("halyard")
        composeRule.onAllNodesWithTag("head-meters").assertCountEquals(0)
    }

    @Test
    fun anotherHostIsLabelledAsTheBandsHost() {
        composeRule.setContent { MachineBand(host = "den", stats = null, otherHost = "halyard") }

        composeRule.onNodeWithTag("machine-host").assertTextEquals("halyard")
    }

    @Test
    fun eachMeterIsOneLineOfNameThenBarThenValue() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val name = composeRule.onNode(hasText("CPU") and hasAnyAncestor(hasTestTag("meter:cpu"))).getUnclippedBoundsInRoot()
        val bar = composeRule.onNodeWithTag("meter-bar:cpu").getUnclippedBoundsInRoot()
        val value = composeRule.onNode(hasText("93% 25/32") and hasAnyAncestor(hasTestTag("meter:cpu")))
            .getUnclippedBoundsInRoot()
        assertTrue("the bar follows the name", name.right <= bar.left)
        assertTrue("the value follows the bar", bar.right <= value.left)
        assertTrue("the bar sits within the name's line", bar.top >= name.top && bar.bottom <= name.bottom)
        assertTrue("the value is on the name's line", value.top < name.bottom && value.bottom > name.top)
        assertEquals(OrreryTokens.Size.meterBarWidth.value, (bar.right - bar.left).value, 1f)
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun atAPhonesNarrowestNothingInTheBandOverlapsOrLeavesIt() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val band = composeRule.onNodeWithTag("machine-band").getUnclippedBoundsInRoot()
        val parts = listOf("machine-host") + machine.map { "meter:${it.label}" }
        val bounds = parts.map { it to composeRule.onNodeWithTag(it).getUnclippedBoundsInRoot() }
        for ((part, box) in bounds) {
            assertTrue("$part stays inside the band", box.left >= band.left && box.right <= band.right)
        }
        for (i in bounds.indices) {
            for (j in i + 1 until bounds.size) {
                val (a, boxA) = bounds[i]
                val (b, boxB) = bounds[j]
                val apart = boxA.right <= boxB.left || boxB.right <= boxA.left ||
                    boxA.bottom <= boxB.top || boxB.bottom <= boxA.top
                assertTrue("$a and $b do not overlap", apart)
            }
        }
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
