package io.github.joshsymonds.orrery.ui

import android.graphics.Bitmap
import android.graphics.Canvas
import androidx.activity.ComponentActivity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlin.math.abs
import org.junit.Assert.assertEquals
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
    val composeRule = createAndroidComposeRule<ComponentActivity>()

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
    fun fillColourFollowsLevel() {
        composeRule.setContent {
            HostMeters(
                listOf(
                    HostStat("cpu", "50%", 50.0, "ok"),
                    HostStat("disk", "50%", 50.0, "warn"),
                    HostStat("temp", "50%", 50.0, "crit"),
                ),
            )
        }
        composeRule.waitForIdle()

        val decor = composeRule.activity.window.decorView
        val bitmap = Bitmap.createBitmap(decor.width, decor.height, Bitmap.Config.ARGB_8888)
        decor.draw(Canvas(bitmap))

        assertFillColour(bitmap, "cpu", Mocha.Lavender)
        assertFillColour(bitmap, "disk", Mocha.Peach)
        assertFillColour(bitmap, "temp", Mocha.Red)
    }

    private fun assertFillColour(bitmap: Bitmap, label: String, expected: Color) {
        val bounds = composeRule.onNodeWithTag("meter-fill:$label").fetchSemanticsNode().boundsInWindow
        val x = ((bounds.left + bounds.right) / 2f).toInt()
        val y = ((bounds.top + bounds.bottom) / 2f).toInt()
        assertEquals("fill colour for $label", expected.toArgb(), bitmap.getPixel(x, y))
    }
}
