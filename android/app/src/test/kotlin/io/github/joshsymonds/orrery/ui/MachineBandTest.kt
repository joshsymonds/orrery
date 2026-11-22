package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.runtime.mutableStateOf
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
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.DpRect
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlin.math.abs
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
        val band = composeRule.onNodeWithTag("machine-band").getUnclippedBoundsInRoot()
        val host = composeRule.onNodeWithTag("machine-host").getUnclippedBoundsInRoot()
        val first = composeRule.onNodeWithTag("meter:net").getUnclippedBoundsInRoot()
        assertTrue("the host label starts the band's first line", host.top <= first.top && host.left >= band.left)
        assertTrue(
            "the host label is inset by the band's padding",
            abs((host.left - band.left).value - OrreryTokens.Layout.pagePadPhone.value) <= 0.5f,
        )
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

    // At 200dp the band is one column of cells, each wider than the meter cell line, so each meter keeps one line.
    @Test
    @Config(qualifiers = "w200dp-h800dp")
    fun eachMeterIsOneLineOfNameThenBarThenValue() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        assertTrue(
            "the cell is at least the meter cell line wide at 200dp",
            cell("cpu").width >= OrreryTokens.Layout.meterCellLine,
        )
        val name = part("cpu", "CPU")
        val bar = bar("cpu")
        val value = part("cpu", "93% 25/32")
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

    // A box's width, from its edges.
    private val DpRect.width: Dp get() = right - left

    // A meter's cell, the box it draws in, unclipped in the root.
    private fun cell(label: String): DpRect = composeRule.onNodeWithTag("meter:$label").getUnclippedBoundsInRoot()

    // A meter's bar, found by its tag.
    private fun bar(label: String): DpRect = composeRule.onNodeWithTag("meter-bar:$label").getUnclippedBoundsInRoot()

    // The text of one part of a meter, its name or its value, found under that meter's cell.
    private fun part(label: String, text: String): DpRect = composeRule
        .onNode(hasText(text) and hasAnyAncestor(hasTestTag("meter:$label")))
        .getUnclippedBoundsInRoot()

    // The fixture's longest value, which draws whole in a cell at 320dp.
    private val longestValue = "↓551K ↑72K"

    // A value that wraps inside a cell at 320dp and 412dp, changed on the last row so no cell below it moves.
    private val wrappingValue = "50% of 4 TB on every volume"

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun theCellsAreEqualAt320dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val widths = machine.map { cell(it.label).width.value }
        assertTrue(
            "every cell is the same width at 320dp: $widths",
            widths.all { abs(it - widths.first()) <= 0.5f },
        )
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun theCellsAreEqualAt412dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val widths = machine.map { cell(it.label).width.value }
        assertTrue(
            "every cell is the same width at 412dp: $widths",
            widths.all { abs(it - widths.first()) <= 0.5f },
        )
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun changingOnlyAValueMovesNoCellAt320dp() {
        val stats = mutableStateOf(machine)
        composeRule.setContent { MachineBand(host = "vermission", stats = stats.value, otherHost = null) }
        val before = machine.map { cell(it.label) }

        composeRule.runOnIdle {
            stats.value = machine.map { if (it.label == "disk") it.copy(value = wrappingValue) else it }
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(wrappingValue).assertIsDisplayed()
        machine.forEachIndexed { index, stat ->
            val was = before[index]
            val now = cell(stat.label)
            assertTrue("${stat.label} keeps its left at 320dp", abs(was.left.value - now.left.value) <= 0.5f)
            assertTrue("${stat.label} keeps its top at 320dp", abs(was.top.value - now.top.value) <= 0.5f)
            assertTrue("${stat.label} keeps its width at 320dp", abs(was.width.value - now.width.value) <= 0.5f)
        }
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun changingOnlyAValueMovesNoCellAt412dp() {
        val stats = mutableStateOf(machine)
        composeRule.setContent { MachineBand(host = "vermission", stats = stats.value, otherHost = null) }
        val before = machine.map { cell(it.label) }

        composeRule.runOnIdle {
            stats.value = machine.map { if (it.label == "disk") it.copy(value = wrappingValue) else it }
        }
        composeRule.waitForIdle()

        composeRule.onNodeWithText(wrappingValue).assertIsDisplayed()
        machine.forEachIndexed { index, stat ->
            val was = before[index]
            val now = cell(stat.label)
            assertTrue("${stat.label} keeps its left at 412dp", abs(was.left.value - now.left.value) <= 0.5f)
            assertTrue("${stat.label} keeps its top at 412dp", abs(was.top.value - now.top.value) <= 0.5f)
            assertTrue("${stat.label} keeps its width at 412dp", abs(was.width.value - now.width.value) <= 0.5f)
        }
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun cellsUnderTheLineStackNameValueAndBarAt320dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        for (stat in machine) {
            val meter = cell(stat.label)
            assertTrue(
                "${stat.label}'s cell is under the meter cell line at 320dp",
                meter.width < OrreryTokens.Layout.meterCellLine,
            )
            val name = part(stat.label, stat.label.uppercase())
            val value = part(stat.label, stat.value)
            assertTrue("${stat.label}'s name stacks above its value at 320dp", name.bottom <= value.top)
            if (stat.percent != null) {
                val bar = bar(stat.label)
                assertTrue("${stat.label}'s value stacks above its bar at 320dp", value.bottom <= bar.top)
                assertTrue(
                    "${stat.label}'s bar spans its cell at 320dp",
                    bar.width.value >= (meter.width - OrreryTokens.Space.eight).value - 0.5f,
                )
            }
        }
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun cellsUnderTheLineStackNameValueAndBarAt412dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        for (stat in machine) {
            val meter = cell(stat.label)
            assertTrue(
                "${stat.label}'s cell is under the meter cell line at 412dp",
                meter.width < OrreryTokens.Layout.meterCellLine,
            )
            val name = part(stat.label, stat.label.uppercase())
            val value = part(stat.label, stat.value)
            assertTrue("${stat.label}'s name stacks above its value at 412dp", name.bottom <= value.top)
            if (stat.percent != null) {
                val bar = bar(stat.label)
                assertTrue("${stat.label}'s value stacks above its bar at 412dp", value.bottom <= bar.top)
                assertTrue(
                    "${stat.label}'s bar spans its cell at 412dp",
                    bar.width.value >= (meter.width - OrreryTokens.Space.eight).value - 0.5f,
                )
            }
        }
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun cellsKeepTheMinimumWidthAndWrapByWidthAt320dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val minimum = OrreryTokens.Size.meterBarWidth.value * 2
        for (stat in machine) {
            assertTrue(
                "${stat.label}'s cell keeps two meter bars at 320dp",
                cell(stat.label).width.value >= minimum - 0.5f,
            )
        }
        // Five stats in three columns: the first row holds three, and the last two wrap to a second row that starts
        // over the same first two columns.
        val net = cell("net")
        val cpu = cell("cpu")
        val diskIo = cell("disk io")
        val disk = cell("disk")
        assertTrue(
            "the wrapped row starts over the first column at 320dp",
            abs(diskIo.left.value - net.left.value) <= 0.5f,
        )
        assertTrue(
            "the wrapped row starts over the second column at 320dp",
            abs(disk.left.value - cpu.left.value) <= 0.5f,
        )
        assertTrue(
            "the wrapped row sits below the first at 320dp",
            diskIo.top >= cpu.bottom && diskIo.top >= net.bottom,
        )
        assertEquals(
            "the cells sit in two rows at 320dp",
            2,
            machine.map { cell(it.label).top.value }.toSet().size,
        )
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun cellsKeepTheMinimumWidthAndWrapByWidthAt412dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val minimum = OrreryTokens.Size.meterBarWidth.value * 2
        for (stat in machine) {
            assertTrue(
                "${stat.label}'s cell keeps two meter bars at 412dp",
                cell(stat.label).width.value >= minimum - 0.5f,
            )
        }
        val net = cell("net")
        val cpu = cell("cpu")
        val diskIo = cell("disk io")
        val disk = cell("disk")
        assertTrue(
            "the wrapped row starts over the first column at 412dp",
            abs(diskIo.left.value - net.left.value) <= 0.5f,
        )
        assertTrue(
            "the wrapped row starts over the second column at 412dp",
            abs(disk.left.value - cpu.left.value) <= 0.5f,
        )
        assertTrue(
            "the wrapped row sits below the first at 412dp",
            diskIo.top >= cpu.bottom && diskIo.top >= net.bottom,
        )
        assertEquals(
            "the cells sit in two rows at 412dp",
            2,
            machine.map { cell(it.label).top.value }.toSet().size,
        )
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun theLongestValueDrawsWholeAt320dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val value = composeRule.onNode(hasText(longestValue) and hasAnyAncestor(hasTestTag("meter:net")))
        value.assertIsDisplayed()
        val text = value.getUnclippedBoundsInRoot()
        val net = cell("net")
        assertTrue(
            "the longest value stays inside its cell at 320dp",
            text.left.value >= net.left.value - 0.5f && text.right.value <= net.right.value + 0.5f,
        )
        assertTrue(
            "the longest value stays inside its cell's height at 320dp",
            text.bottom.value <= net.bottom.value + 0.5f,
        )
    }

    @Test
    @Config(qualifiers = "w320dp-h800dp")
    fun theHostIsOnItsOwnLineAboveTheCellsAt320dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val host = composeRule.onNodeWithTag("machine-host").getUnclippedBoundsInRoot()
        for (stat in machine) {
            assertTrue("${stat.label} sits below the host line at 320dp", cell(stat.label).top >= host.bottom)
        }
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun theHostIsOnItsOwnLineAboveTheCellsAt412dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val host = composeRule.onNodeWithTag("machine-host").getUnclippedBoundsInRoot()
        for (stat in machine) {
            assertTrue("${stat.label} sits below the host line at 412dp", cell(stat.label).top >= host.bottom)
        }
    }

    @Test
    @Config(qualifiers = "w412dp-h800dp")
    fun nothingLeavesTheBandAt412dp() {
        composeRule.setContent { MachineBand(host = "vermission", stats = machine, otherHost = null) }

        val band = composeRule.onNodeWithTag("machine-band").getUnclippedBoundsInRoot()
        val tags = listOf("machine-host") + machine.map { "meter:${it.label}" }
        for (tag in tags) {
            val box = composeRule.onNodeWithTag(tag).getUnclippedBoundsInRoot()
            assertTrue(
                "$tag stays inside the band at 412dp",
                box.left >= band.left && box.right <= band.right && box.top >= band.top && box.bottom <= band.bottom,
            )
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
