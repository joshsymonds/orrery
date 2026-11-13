package io.github.joshsymonds.orrery.ui

import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.EpicSliceRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.min
import kotlin.math.sin
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp-xxhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class EpicChartTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private val chartSize = 400.dp

    // alpha's dot is on tests pass, beta has ended, gamma's dot is on red test. Each ring's reached pattern across the
    // three slices differs from every other ring's, so a cell that read another ring's field would show.
    private val three = listOf(
        slice("alpha", "done", at = 3, true, true, true, false, false),
        slice("beta", "dispatched", at = 0, true, false, false, true, false),
        slice("gamma", "gap", at = 1, false, true, false, true, true),
    )

    @Test
    fun threeSlicesPaintEachCellInItsHueOrTheTrack() {
        composeRule.setContent { EpicChart(three, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        three.forEachIndexed { index, slice ->
            // The dot sits at the middle angle, so each cell is read a quarter span in from its start.
            val start = index * 120f
            for (ring in 0 until RING_COUNT) {
                val expected = if (slice.reached(ring)) hueColours.getValue(slice.hue) else Mocha.Surface0
                val actual = bitmap.argbAt(chart.spot(ringMiddle(ring), start + 30f))
                assertEquals("${slice.task} ring $ring", expected.toArgb(), actual)
            }
        }
    }

    @Test
    fun cellsAreEdgedInABaseHairline() {
        composeRule.setContent { EpicChart(three, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        // alpha and beta meet at 120 degrees; ring 0 and ring 1 of alpha meet at ringOuter(1).
        val between = listOf(chart.spot(ringMiddle(0), 120f), chart.spot(ringOuter(1), 30f))
        for (edge in between) {
            assertTrue("Base at $edge", bitmap.disc(edge, density()).any { it.second == Mocha.Base.toArgb() })
        }
        assertTrue(
            "no Base inside a cell",
            bitmap.disc(chart.spot(ringMiddle(0), 60f), density()).none { it.second == Mocha.Base.toArgb() },
        )
    }

    @Test
    fun runningSliceDotHasBaseEdgeOnServesRing() {
        composeRule.setContent { EpicChart(three, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        // alpha is slice 0 at 3, so its dot sits on ring 2 at the middle angle of 60 degrees.
        val centre = chart.spot(ringMiddle(2), 60f)
        assertEquals("dot fill", Mocha.Yellow.toArgb(), bitmap.argbAt(centre))

        val reach = chart.dotRadius + 2 * density()
        val base = bitmap.disc(centre, reach).filter { it.second == Mocha.Base.toArgb() }
        assertTrue("the dot has a Base edge", base.isNotEmpty())
        assertTrue(
            "every Base pixel lies on the dot's rim",
            base.all { abs(it.first - chart.dotRadius) <= density() },
        )
    }

    @Test
    fun endedSliceHasNoDotAtItsPlace() {
        composeRule.setContent { EpicChart(three, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        // beta is slice 1 at 0. Ring 2 at its middle angle is where a dot would sit.
        val place = chart.spot(ringMiddle(2), 180f)
        assertEquals("beta's cell there", Mocha.Surface0.toArgb(), bitmap.argbAt(place))
        val reach = chart.dotRadius + 2 * density()
        assertTrue("no Base at that place", bitmap.disc(place, reach).none { it.second == Mocha.Base.toArgb() })
    }

    @Test
    fun thirtyFiveSlicesPaintTheOutermostCellOfEachInOrder() {
        assertOutermostCellsInOrder(35)
    }

    @Test
    fun oneHundredFiftySlicesPaintTheOutermostCellOfEachInOrder() {
        assertOutermostCellsInOrder(150)
    }

    @Test
    fun outlinedSliceEdgeIsMauve() {
        composeRule.setContent { EpicChart(three, outlined = 1, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        // beta is slice 1, spanning 120 to 240 degrees. The outer edge of every ring of its cells is mauve.
        for (ring in 0 until RING_COUNT) {
            val edge = chart.spot(ringOuter(ring), 180f)
            assertEquals("beta ring $ring outer edge", Mocha.Mauve.toArgb(), bitmap.argbAt(edge))
        }
    }

    @Test
    fun unoutlinedSliceEdgesAreNotMauve() {
        composeRule.setContent { EpicChart(three, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        for (middle in listOf(60f, 180f, 300f)) {
            assertNotEquals(
                "outer edge at $middle degrees",
                Mocha.Mauve.toArgb(),
                bitmap.argbAt(chart.spot(ringOuter(0), middle)),
            )
        }
    }

    @Test
    fun stateDescriptionIsTheOutlinedTaskAndUnsetOtherwise() {
        var outlined by mutableStateOf<Int?>(1)
        composeRule.setContent { EpicChart(three, outlined = outlined, modifier = Modifier.size(chartSize)) }

        assertEquals("beta", stateDescription())

        composeRule.runOnIdle { outlined = null }
        composeRule.waitForIdle()
        assertNull(stateDescription())

        composeRule.runOnIdle { outlined = three.size + 4 }
        composeRule.waitForIdle()
        assertNull(stateDescription())
    }

    private fun assertOutermostCellsInOrder(count: Int) {
        val hues = listOf("done", "dispatched", "gap", "other")
        val colours = listOf(Mocha.Green, Mocha.Yellow, Mocha.Red, Mocha.Subtext0)
        val slices = List(count) { index ->
            slice("task $index", hues[index % hues.size], at = 0, true, false, false, false, false)
        }
        composeRule.setContent { EpicChart(slices, outlined = null, modifier = Modifier.size(chartSize)) }
        val bitmap = renderWindow()
        val chart = placement()

        // The middle angle of each slice is well clear of its edges even at 150 slices.
        val span = 360f / count
        slices.indices.forEach { index ->
            val actual = bitmap.argbAt(chart.spot(ringMiddle(0), (index + 0.5f) * span))
            assertEquals("slice $index of $count", colours[index % colours.size].toArgb(), actual)
        }
    }

    private fun stateDescription(): String? =
        composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.StateDescription)

    private fun renderWindow(): Bitmap {
        composeRule.waitForIdle()
        val decor = composeRule.activity.window.decorView
        val bitmap = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
        decor.draw(Canvas(bitmap))
        return bitmap
    }

    private fun placement(): Placement {
        val bounds = composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().boundsInWindow
        return Placement(bounds)
    }

    private fun density(): Float = composeRule.activity.resources.displayMetrics.density

    private fun Bitmap.argbAt(spot: Offset): Int = getPixel(spot.x.toInt(), spot.y.toInt())

    /** Every pixel whose centre lies within [radius] of [centre], paired with its distance from it. */
    private fun Bitmap.disc(centre: Offset, radius: Float): List<Pair<Float, Int>> {
        val reach = radius.toInt() + 1
        val centreX = centre.x.toInt()
        val centreY = centre.y.toInt()
        return buildList {
            for (y in centreY - reach..centreY + reach) {
                for (x in centreX - reach..centreX + reach) {
                    val distance = hypot(x + 0.5f - centre.x, y + 0.5f - centre.y)
                    if (distance <= radius) add(distance to getPixel(x, y))
                }
            }
        }
    }

    /** Where the chart's units fall in the window. The chart is a square of 2 * HALF units, centred. */
    private class Placement(bounds: Rect) {
        private val centreX = (bounds.left + bounds.right) / 2f
        private val centreY = (bounds.top + bounds.bottom) / 2f
        private val unit = min(bounds.width, bounds.height) / (2 * HALF)

        val dotRadius: Float = DOT_RADIUS * unit

        /** The point at [radius] units and [degrees] clockwise from the top. */
        fun spot(radius: Float, degrees: Float): Offset {
            val angle = degrees * PI.toFloat() / 180f
            return Offset(centreX + radius * unit * sin(angle), centreY - radius * unit * cos(angle))
        }
    }

    private fun slice(task: String, hue: String, at: Int, vararg reached: Boolean): EpicSliceRow =
        EpicSliceRow(
            task = task,
            hue = hue,
            redTest = reached[0],
            working = reached[1],
            testsPass = reached[2],
            checks = reached[3],
            gateAccepted = reached[4],
            at = at,
            held = false,
            errorAt = 0,
        )

    private fun EpicSliceRow.reached(ring: Int): Boolean =
        listOf(redTest, working, testsPass, checks, gateAccepted)[ring]

    private companion object {
        const val HALF = 61f
        const val DOT_RADIUS = 2.5f
        const val RING_COUNT = 5

        // Ring 0 is outermost: its inner radius is 24 + 4 * 7, and each ring is 7 thick.
        fun ringOuter(ring: Int): Float = 24f + (RING_COUNT - ring) * 7f
        fun ringMiddle(ring: Int): Float = ringOuter(ring) - 3.5f

        val hueColours: Map<String, Color> = mapOf(
            "done" to Mocha.Green,
            "dispatched" to Mocha.Yellow,
            "gap" to Mocha.Red,
        )
    }
}
