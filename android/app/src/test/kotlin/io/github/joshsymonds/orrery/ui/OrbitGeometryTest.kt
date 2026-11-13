package io.github.joshsymonds.orrery.ui

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asAndroidPath
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.sin

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class OrbitGeometryTest {
    // The point at radius units and degrees clockwise from 12 o'clock, relative to the centre.
    private fun polar(degrees: Float, radius: Float): Offset {
        val angle = degrees * PI.toFloat() / 180f
        return Offset(radius * sin(angle), -radius * cos(angle))
    }

    @Test
    fun spanMatchesTheTableFromOneToOneFifty() {
        assertEquals(344f, sliceSpan(1), 0.05f)
        assertEquals(172f, sliceSpan(2), 0.05f)
        assertEquals(68.8f, sliceSpan(5), 0.05f)
        assertEquals(28.7f, sliceSpan(12), 0.05f)
        assertEquals(9.83f, sliceSpan(35), 0.005f)
        assertEquals(4.30f, sliceSpan(80), 0.005f)
        assertEquals(2.29f, sliceSpan(150), 0.005f)
    }

    @Test
    fun gapMatchesTheTableFromOneToOneFifty() {
        assertEquals(3.0f, sliceGap(1), 0.05f)
        assertEquals(3.0f, sliceGap(2), 0.05f)
        assertEquals(3.0f, sliceGap(5), 0.05f)
        assertEquals(3.0f, sliceGap(12), 0.05f)
        assertEquals(2.74f, sliceGap(35), 0.005f)
        assertEquals(1.20f, sliceGap(80), 0.005f)
        assertEquals(0.70f, sliceGap(150), 0.005f)
    }

    @Test
    fun cornerMatchesTheTableOnTheInnerAndOuterRing() {
        // Ring 4 is the innermost (radius 100), ring 0 the outermost (radius 168).
        assertEquals(2f, cellCorner(1, 4), 0.05f)
        assertEquals(2f, cellCorner(1, 0), 0.05f)
        assertEquals(2f, cellCorner(2, 4), 0.05f)
        assertEquals(2f, cellCorner(2, 0), 0.05f)
        assertEquals(2f, cellCorner(5, 4), 0.05f)
        assertEquals(2f, cellCorner(5, 0), 0.05f)
        assertEquals(2f, cellCorner(12, 4), 0.05f)
        assertEquals(2f, cellCorner(12, 0), 0.05f)
        assertEquals(2f, cellCorner(35, 4), 0.05f)
        assertEquals(2f, cellCorner(35, 0), 0.05f)
        assertEquals(1.9f, cellCorner(80, 4), 0.05f)
        assertEquals(2f, cellCorner(80, 0), 0.05f)
        assertEquals(1.0f, cellCorner(150, 4), 0.05f)
        assertEquals(1.8f, cellCorner(150, 0), 0.05f)
    }

    @Test
    fun firstSliceStartsAt8DegreesAndSlicesTileTo352() {
        assertEquals(8f, sliceStart(0, 12), 1e-4f)
        assertEquals(22.333f, sliceMid(0, 12), 1e-3f)
        for (count in listOf(1, 2, 5, 12, 35, 80, 150)) {
            assertEquals("N=$count", 352f, sliceStart(count, count), 1e-3f)
            assertEquals("N=$count", sliceStart(0, count) + sliceSpan(count), sliceStart(1, count), 1e-3f)
        }
    }

    @Test
    fun hitAtTheCentreIsTheCentreAndInsideTheHoleIsNotASlice() {
        assertEquals(OrbitHit.Centre, orbitHit(Offset.Zero, 12))
        assertEquals(OrbitHit.Centre, orbitHit(polar(90f, 80f), 12))
        assertEquals(OrbitHit.Slice(2), orbitHit(polar(90f, 80.6f), 12))
    }

    @Test
    fun hitOutsideTheChartIsNothing() {
        assertEquals(OrbitHit.Slice(2), orbitHit(polar(90f, 199.5f), 12))
        assertNull(orbitHit(polar(90f, 200.5f), 12))
    }

    @Test
    fun hitInTheStartGapIsNothing() {
        assertNull(orbitHit(polar(0f, 100f), 12))
        assertNull(orbitHit(polar(5f, 100f), 12))
        assertEquals(OrbitHit.Slice(0), orbitHit(polar(12f, 100f), 12))
    }

    @Test
    fun hitNearThe359thDegreeIsTheGapNotTheLastSlice() {
        assertNull(orbitHit(polar(359f, 100f), 150))
        assertEquals(OrbitHit.Slice(149), orbitHit(polar(351f, 100f), 150))
    }

    @Test
    fun hitFindsTheSliceByItsAngle() {
        assertEquals(OrbitHit.Slice(0), orbitHit(polar(8.5f, 100f), 150))
        assertEquals(OrbitHit.Slice(3), orbitHit(polar(17f, 100f), 150))
        assertEquals(OrbitHit.Slice(1), orbitHit(polar(90f, 150f), 5))
        assertEquals(OrbitHit.Slice(0), orbitHit(polar(180f, 150f), 1))
        assertEquals(OrbitHit.Slice(1), orbitHit(polar(270f, 150f), 2))
        assertEquals(OrbitHit.Slice(6), orbitHit(polar(185f, 150f), 12))
    }

    @Test
    fun bezelIsAbsentBelowTenSlices() {
        assertTrue(bezelTicks(5).isEmpty())
        assertTrue(bezelTicks(9).isEmpty())
    }

    @Test
    fun bezelTickOnTheFifthSliceSitsAtItsMiddleAndItsNumberAt192Point5() {
        val fifth = bezelTicks(10).first { it.slice == 5 }
        assertEquals(sliceMid(4, 10), fifth.angle, 1e-3f)
        assertEquals(179f, fifth.inner, 1e-3f)
        assertEquals(184f, fifth.outer, 1e-3f)
        val expected = polar(sliceMid(4, 10), 192.5f) + Offset(206f, 206f)
        val label = fifth.numberCentre!!
        assertEquals(expected.x, label.x, 0.01f)
        assertEquals(expected.y, label.y, 0.01f)
    }

    @Test
    fun bezelTicksAndNumbersForTwelve() {
        val ticks = bezelTicks(12)
        assertEquals(listOf(5, 10), ticks.map { it.slice })
        assertEquals(listOf(true, true), ticks.map { it.numberCentre != null })
    }

    @Test
    fun bezelTicksAndNumbersForEighty() {
        val ticks = bezelTicks(80)
        assertEquals((5..80 step 5).toList(), ticks.map { it.slice })
        assertEquals((10..80 step 10).toList(), ticks.filter { it.numberCentre != null }.map { it.slice })
        assertTrue(ticks.all { it.outer == if (it.numberCentre != null) 184f else 182f })
    }

    @Test
    fun bezelTicksAndNumbersForOneFifty() {
        val ticks = bezelTicks(150)
        assertEquals((5..150 step 5).toList(), ticks.map { it.slice })
        assertEquals(listOf(25, 50, 75, 100, 125, 150), ticks.filter { it.numberCentre != null }.map { it.slice })
    }

    @Test
    fun cellLiesBetweenItsRadii() {
        // Ring 0 is 162.5 to 173.5 from the centre; ring 4 is 94.5 to 105.5.
        assertBetweenRadii(cellPath(1, 0, 0, 1f), 162.5f, 173.5f, 1f)
        assertBetweenRadii(cellPath(1, 0, 4, 1f), 94.5f, 105.5f, 1f)
        assertBetweenRadii(cellPath(150, 149, 4, 1f), 94.5f, 105.5f, 1f)
    }

    @Test
    fun cellPathScalesWithUnits() {
        assertBetweenRadii(cellPath(1, 0, 0, 2f), 162.5f, 173.5f, 2f)
    }

    @Test
    fun cursorWedgeLiesBetweenItsRadii() {
        assertBetweenRadii(cursorWedge(1, 0, 1f), 91f, 177f, 1f)
    }

    // The distances from the centre of every point on the path's outline, in units. The outline comes from approximate(),
    // since getBounds() of a path with curves takes in their control points and reaches past the radii.
    private fun assertBetweenRadii(path: Path, inner: Float, outer: Float, u: Float) {
        val centre = Offset(206f * u, 206f * u)
        val coords = path.asAndroidPath().approximate(0.01f)
        val radii = (coords.indices step 3).map { (Offset(coords[it + 1], coords[it + 2]) - centre).getDistance() / u }
        assertTrue(radii.all { it >= inner - 0.05f && it <= outer + 0.05f })
        assertEquals(inner, radii.min(), 0.05f)
        assertEquals(outer, radii.max(), 0.05f)
    }

    @Test
    fun cursorHandPointsOutFromTheBase() {
        // One slice's middle is 180 degrees: the tip at radius 89, the base corners at 82.5 and 3.4 units either side.
        val hand = cursorHand(1, 0, 1f).getBounds()
        assertEquals(202.6f, hand.left, 0.01f)
        assertEquals(209.4f, hand.right, 0.01f)
        assertEquals(288.5f, hand.top, 0.01f)
        assertEquals(295f, hand.bottom, 0.01f)
    }
}
