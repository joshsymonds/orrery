package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.EpicSliceRow
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.min
import kotlin.math.sin
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
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

    // With the animator duration scale at 0 the glow holds still at 0.5, so the chart is idle and its pixels are fixed.
    @Before
    fun animatorDurationScaleIsZero() {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            0f,
        )
    }

    // One slice per state. P passed, N now, H held, E errored, T track, read ring by ring, red test first.
    private val states = listOf(
        slice("now", "dispatched", listOf(true, true, false, false, false), at = 3),
        slice("held", "dispatched", listOf(true, false, false, false, false), at = 2, held = true),
        slice("errored", "gap", listOf(true, true, false, false, false), errorAt = 3),
        slice("passed", "done", listOf(true, true, true, true, true)),
        slice("track", "gap", listOf(false, false, false, false, false)),
    )
    private val stateLetters = listOf("PPNTT", "PHTTT", "PPETT", "PPPPP", "TTTTT")

    // A now slice on ring 2 beside a held one on the same ring, so the glow can be checked on one and not the other.
    private val glowing = listOf(
        slice("now", "dispatched", listOf(true, false, false, false, false), at = 2),
        slice("held", "dispatched", listOf(true, false, false, false, false), at = 2, held = true),
    )

    @Test
    fun eachCellTakesItsStatesColourOrTheTrack() {
        composeRule.setContent { ChartUnderTest(states, selected = null) }
        val bitmap = renderWindow()
        val chart = placement()

        states.indices.forEach { slice ->
            for (ring in 0 until RING_COUNT) {
                val actual = bitmap.argbAt(chart.spot(RINGS[ring], middle(slice, states.size)))
                // Slice 0 is the running one: its glow tints its own cells, so only its cells are named by nearest colour.
                assertCell("slice $slice ring $ring", stateLetters[slice][ring], actual, nearGlow = slice == 0)
            }
        }
    }

    @Test
    fun noDotSitsOnAnyCell() {
        composeRule.setContent { ChartUnderTest(states, selected = null) }
        val bitmap = renderWindow()
        val chart = placement()

        // A dot would break the cell's colour round its middle, so every pixel within a few units of that middle is the
        // cell's colour.
        states.indices.forEach { slice ->
            for (ring in 0 until RING_COUNT) {
                val middle = chart.spot(RINGS[ring], middle(slice, states.size))
                val expected = stateLetters[slice][ring]
                val reach = bitmap.disc(middle, DISC_UNITS * chart.unit)
                assertTrue(
                    "slice $slice ring $ring",
                    reach.all { if (slice == 0) nearestState(it.second) == expected else it.second == colourOf(expected).toArgb() },
                )
            }
        }
    }

    @Test
    fun theTwelveOClockGapIsGround() {
        composeRule.setContent { ChartUnderTest(states, selected = null) }
        val bitmap = renderWindow()
        val chart = placement()

        // Two radii on 12 o'clock, each between two ring letters.
        assertEquals(Mocha.Base.toArgb(), bitmap.argbAt(chart.spot(142.5f, 0f)))
        assertEquals(Mocha.Base.toArgb(), bitmap.argbAt(chart.spot(108.5f, 0f)))
    }

    @Test
    fun aNowCellsGlowShowsBesideItAndAHeldCellsDoesNot() {
        composeRule.setContent { ChartUnderTest(glowing, selected = null) }
        val bitmap = renderWindow()
        val chart = placement()

        // Ring 1's inner edge is at 145.5 units and ring 2's outer edge at 139.5: the channel between them is ground
        // unless a glow spills into it.
        assertNotEquals("glow beside the now cell", Mocha.Base.toArgb(), bitmap.argbAt(chart.spot(142.5f, middle(0, 2))))
        assertEquals("no glow beside the held cell", Mocha.Base.toArgb(), bitmap.argbAt(chart.spot(142.5f, middle(1, 2))))
    }

    @Test
    fun theSelectedSliceHasAMauveOutlineAndAHandAndNoWashOnAShortChart() {
        composeRule.setContent { ChartUnderTest(states, selected = 1) }
        val bitmap = renderWindow()
        val chart = placement()

        assertEquals("outline", Mocha.Mauve.toArgb(), bitmap.argbAt(chart.spot(177f, middle(1, states.size))))
        assertEquals("hand", Mocha.Mauve.toArgb(), bitmap.argbAt(chart.spot(86f, middle(1, states.size))))
        assertNotEquals("no outline on another slice", Mocha.Mauve.toArgb(), bitmap.argbAt(chart.spot(177f, middle(0, states.size))))
        assertEquals("no wash under 18 slices", Mocha.Base.toArgb(), bitmap.argbAt(chart.spot(92.5f, middle(1, states.size))))
    }

    @Test
    fun theWashUnderTheSelectedSliceLeavesItsCellsAlone() {
        val slices = cycled(35)
        composeRule.setContent { ChartUnderTest(slices, selected = 10) }
        val bitmap = renderWindow()
        val chart = placement()

        // Between the wedge's inner edge and the innermost ring the wash shows; slice 10 is errored on red test and
        // track on the rest, and its cells keep those colours.
        assertEquals("wash", lerp(Mocha.Base, Mocha.Mauve, WASH_MIX).toArgb(), bitmap.argbAt(chart.spot(92.5f, middle(10, 35))))
        "ETTTT".forEachIndexed { ring, letter ->
            val expected = colourOf(letter).toArgb()
            assertEquals("slice 10 ring $ring", expected, bitmap.argbAt(chart.spot(RINGS[ring], middle(10, 35))))
        }
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
    fun theCanvasDescribesItsEpicAndItsSelectedTask() {
        val described = listOf(
            slice("a", "done", listOf(true, true, true, true, true)),
            slice("b", "dispatched", listOf(false, false, false, false, false), at = 1),
            slice("c", "dispatched", listOf(false, false, false, false, false), at = 1, held = true),
            slice("d", "gap", listOf(false, false, false, false, false), errorAt = 2),
        )
        var selected by mutableStateOf<Int?>(1)
        composeRule.setContent {
            EpicChart(described, taskCount = described.size, selected = selected, onSelect = {}, modifier = Modifier.size(chartSize))
        }

        assertEquals(listOf("4 tasks: 1 done, 1 running, 1 held, 1 gap"), contentDescription())
        assertEquals("b", stateDescription())

        composeRule.runOnIdle { selected = null }
        composeRule.waitForIdle()
        assertNull(stateDescription())
    }

    @Test
    fun noSlicesShowTheEmptyStateInPlaceOfTheChart() {
        composeRule.setContent { ChartUnderTest(emptyList(), selected = null) }

        composeRule.onNodeWithTag("epic-chart-none").assertIsDisplayed()
        composeRule.onNodeWithText("No orrery-tracked steps").assertIsDisplayed()
        assertEquals(0, composeRule.onAllNodesWithTag("epic-chart").fetchSemanticsNodes().size)
    }

    @Test
    fun aTapOnASliceSelectsIt() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = null, onSelect = { calls += it }) }

        tap(touch(150f, middle(1, states.size)))

        assertEquals(listOf(1), calls)
    }

    @Test
    fun aTapOnTheSelectedSliceClearsIt() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = 1, onSelect = { calls += it }) }

        tap(touch(150f, middle(1, states.size)))

        assertEquals(listOf(null), calls)
    }

    @Test
    fun aTapOnTheCentreClearsTheSelection() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = 2, onSelect = { calls += it }) }

        tap(touch(0f, 0f))

        assertEquals(listOf(null), calls)
    }

    @Test
    fun aTapInTheGapOrOutsideTheRingsDoesNothing() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = null, onSelect = { calls += it }) }

        tap(touch(150f, 0f))
        tap(touch(205f, 90f))

        assertEquals(emptyList<Int?>(), calls)
    }

    @Test
    fun aDragFromTheRingsSelectsEachSliceItCrosses() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = null, onSelect = { calls += it }) }
        val start = touch(150f, middle(0, states.size) - 6f)
        val near = touch(150f, middle(0, states.size) + 6f)
        val second = touch(150f, middle(1, states.size))
        val third = touch(150f, middle(2, states.size))

        composeRule.onNodeWithTag("epic-chart").performTouchInput {
            down(start)
            moveTo(near)
            moveTo(second)
            moveTo(third)
            up()
        }

        assertEquals(listOf(0, 1, 2), calls)
    }

    @Test
    fun aDragFromTheCentreDoesNothing() {
        val calls = mutableListOf<Int?>()
        composeRule.setContent { ChartUnderTest(states, selected = null, onSelect = { calls += it }) }
        val centre = touch(0f, 0f)
        val drifted = centre + Offset(0f, 90f)
        val ring = touch(150f, middle(1, states.size))

        composeRule.onNodeWithTag("epic-chart").performTouchInput {
            down(centre)
            moveTo(drifted)
            moveTo(ring)
            up()
        }

        assertEquals(emptyList<Int?>(), calls)
    }

    @Composable
    private fun ChartUnderTest(
        slices: List<EpicSliceRow>,
        selected: Int?,
        onSelect: (Int?) -> Unit = {},
    ) {
        EpicChart(
            slices,
            taskCount = slices.size,
            selected = selected,
            onSelect = onSelect,
            modifier = Modifier.size(chartSize),
        )
    }

    private fun assertOutermostCellsInOrder(count: Int) {
        val slices = cycled(count)
        composeRule.setContent { ChartUnderTest(slices, selected = null) }
        val bitmap = renderWindow()
        val chart = placement()

        slices.indices.forEach { index ->
            val actual = bitmap.argbAt(chart.spot(RINGS[0], middle(index, count)))
            // Kind 1 is running: its glow tints its own cell and, at 150 slices where it is wider than a cell, the cells up
            // to two slices away. Elsewhere every cell must be its exact colour.
            val reach = if (count >= 150) 2 else 0
            val nearGlow = (index - reach..index + reach).any { it in slices.indices && it % CYCLE_LETTERS.length == 1 }
            assertCell("slice $index of $count", CYCLE_LETTERS[index % CYCLE_LETTERS.length], actual, nearGlow)
        }
    }

    private fun tap(position: Offset) {
        composeRule.onNodeWithTag("epic-chart").performTouchInput { click(position) }
    }

    // The point [radius] units from the chart's centre at [degrees] clockwise from 12 o'clock, in the canvas's own
    // pixels: the coordinates a touch on the chart is sent in.
    private fun touch(radius: Float, degrees: Float): Offset {
        val size = composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().size
        val unit = min(size.width, size.height) / BOX
        val angle = degrees * PI.toFloat() / 180f
        return Offset(size.width / 2f + radius * unit * sin(angle), size.height / 2f - radius * unit * cos(angle))
    }

    private fun contentDescription(): List<String>? =
        composeRule.onNodeWithTag("epic-chart").fetchSemanticsNode().config
            .getOrNull(SemanticsProperties.ContentDescription)

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

    private fun Bitmap.argbAt(spot: Offset): Int = getPixel(spot.x.toInt(), spot.y.toInt())

    /** Every pixel whose centre lies within [radius] of [centre], paired with its ARGB value. */
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

    /** Where the chart's units fall in the window. The chart is a square of [BOX] units, centred in the canvas. */
    private class Placement(bounds: Rect) {
        private val centreX = (bounds.left + bounds.right) / 2f
        private val centreY = (bounds.top + bounds.bottom) / 2f
        val unit: Float = min(bounds.width, bounds.height) / BOX

        /** The point at [radius] units and [degrees] clockwise from the top. */
        fun spot(radius: Float, degrees: Float): Offset {
            val angle = degrees * PI.toFloat() / 180f
            return Offset(centreX + radius * unit * sin(angle), centreY - radius * unit * cos(angle))
        }
    }

    private fun slice(
        task: String,
        hue: String,
        reached: List<Boolean>,
        at: Int = 0,
        held: Boolean = false,
        errorAt: Int = 0,
    ): EpicSliceRow = EpicSliceRow(
        task = task,
        hue = hue,
        redTest = reached[0],
        working = reached[1],
        testsPass = reached[2],
        checks = reached[3],
        gateAccepted = reached[4],
        at = at,
        held = held,
        errorAt = errorAt,
    )

    // Slices in four kinds in turn: passed, now, errored and track. Each one's outermost cell is that kind's colour.
    private fun cycled(count: Int): List<EpicSliceRow> = List(count) { index ->
        val task = "task $index"
        val none = listOf(false, false, false, false, false)
        when (index % 4) {
            0 -> slice(task, "done", listOf(true, true, true, true, true))
            1 -> slice(task, "dispatched", none, at = 1)
            2 -> slice(task, "gap", none, errorAt = 1)
            else -> slice(task, "dispatched", none)
        }
    }

    // The state whose colour is nearest to [argb]: P, N, H, E or T, or B for the ground. A now cell's glow is drawn over
    // the cells and spills on its neighbours (spec sections 6 and 10), so a cell's middle is its colour give or take that
    // spill; the state colours lie far enough apart that the nearest one names the cell.
    private fun nearestState(argb: Int): Char = "PNHETB".minBy { letter ->
        val colour = if (letter == 'B') Mocha.Base.toArgb() else colourOf(letter).toArgb()
        val dr = ((argb shr 16) and 0xFF) - ((colour shr 16) and 0xFF)
        val dg = ((argb shr 8) and 0xFF) - ((colour shr 8) and 0xFF)
        val db = (argb and 0xFF) - (colour and 0xFF)
        dr * dr + dg * dg + db * db
    }

    // A cell shows its state's exact colour; within a now cell's glow it is the nearest state colour, and a now cell itself
    // is yellow give or take the glow's rounding (two levels a channel).
    private fun assertCell(label: String, expected: Char, argb: Int, nearGlow: Boolean) {
        val exact = colourOf(expected).toArgb()
        when {
            !nearGlow -> assertEquals(label, exact, argb)
            expected == 'N' -> assertTrue("$label: ${Integer.toHexString(argb)}", channelsWithin(argb, exact, 2))
            else -> assertEquals(label, expected, nearestState(argb))
        }
    }

    private fun channelsWithin(a: Int, b: Int, levels: Int): Boolean =
        listOf(16, 8, 0).all { shift -> kotlin.math.abs(((a shr shift) and 0xFF) - ((b shr shift) and 0xFF)) <= levels }

    private fun colourOf(letter: Char): Color = when (letter) {
        'P' -> Mocha.Green
        'N' -> Mocha.Yellow
        'H' -> Mocha.Peach
        'E' -> Mocha.Red
        'T' -> TRACK
        else -> error("no colour for $letter")
    }

    // The middle angle of slice [index] of [count], from the spec: a 8 degree start and 344 degrees shared out.
    private fun middle(index: Int, count: Int): Float = START + (index + 0.5f) * SWEEP / count

    private companion object {
        const val BOX = 412f
        const val START = 8f
        const val SWEEP = 344f
        const val RING_COUNT = 5
        const val WASH_MIX = 0.16f
        const val DISC_UNITS = 3f

        // The ring centre radii in units, red test first.
        val RINGS = listOf(168f, 151f, 134f, 117f, 100f)

        // Unreached cells are Surface1 at 60% over Base.
        val TRACK: Color = lerp(Mocha.Base, Mocha.Surface1, 0.6f)

        // The outermost cell's state for each of the four kinds in cycled().
        const val CYCLE_LETTERS = "PNET"
    }
}
