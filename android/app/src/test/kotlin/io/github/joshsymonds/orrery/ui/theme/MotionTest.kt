package io.github.joshsymonds.orrery.ui.theme

import android.content.Context
import android.provider.Settings
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.SnapSpec
import androidx.compose.animation.core.TweenSpec
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class MotionTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun pressStateLayoutAndDrillEaseOutAndLeaveEasesIn() {
        assertEquals(120, Move.PRESS.millis)
        assertEquals(200, Move.STATE.millis)
        assertEquals(280, Move.LAYOUT.millis)
        assertEquals(320, Move.DRILL.millis)
        assertEquals(200, Move.LEAVE.millis)
        listOf(Move.PRESS, Move.STATE, Move.LAYOUT, Move.DRILL).forEach { assertSame(EaseOut, it.easing) }
        assertSame(EaseIn, Move.LEAVE.easing)
    }

    @Test
    fun easeOutIsCubicBezierTwoZeroZeroOne() {
        assertSameCurve(CubicBezierEasing(0.2f, 0f, 0f, 1f), EaseOut)
    }

    @Test
    fun easeInIsCubicBezierFourZeroOneZero() {
        assertSameCurve(CubicBezierEasing(0.4f, 0f, 1f, 1f), EaseIn)
    }

    @Test
    fun animatedSpecsTweenEachMoveAndUnanimatedSpecsSnap() {
        Move.entries.forEach { move ->
            val tween = move.spec<Float>(animated = true) as? TweenSpec<*>
            assertEquals("${move.name} duration", move.millis, tween?.durationMillis)
            assertSame("${move.name} easing", move.easing, tween?.easing)
            assertTrue("${move.name} snaps when not animated", move.spec<Float>(animated = false) is SnapSpec<*>)
        }
    }

    @Test
    fun animationsAreOffWhenTheAnimatorDurationScaleIsZero() {
        setAnimatorDurationScale(0f)
        assertFalse(animationsOnInComposition())
    }

    @Test
    fun animationsAreOnWhenTheAnimatorDurationScaleIsOne() {
        setAnimatorDurationScale(1f)
        assertTrue(animationsOnInComposition())
    }

    private fun animationsOnInComposition(): Boolean {
        var on: Boolean? = null
        composeRule.setContent { on = animationsOn() }
        composeRule.waitForIdle()
        return checkNotNull(on)
    }

    private fun setAnimatorDurationScale(scale: Float) {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            scale,
        )
    }

    // Two easings are the same curve when they agree exactly at every sampled fraction.
    private fun assertSameCurve(expected: Easing, actual: Easing) {
        listOf(0f, 0.1f, 0.25f, 0.5f, 0.75f, 0.9f, 1f).forEach { fraction ->
            assertEquals("at $fraction", expected.transform(fraction), actual.transform(fraction), 0f)
        }
    }
}
