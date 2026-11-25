package io.github.joshsymonds.orrery.ui.theme

import android.provider.Settings
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.InfiniteRepeatableSpec
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

// The phone's motion tokens. Each move tweens while animations are on, and takes one linear step of motion.reduced when the
// system's animator duration scale is 0.
enum class Move(val millis: Int, val easing: Easing) {
    PRESS(OrreryTokens.Motion.Move.press.millis, OrreryTokens.Motion.Move.press.easing),
    STATE(OrreryTokens.Motion.Move.state.millis, OrreryTokens.Motion.Move.state.easing),
    LAYOUT(OrreryTokens.Motion.Move.layout.millis, OrreryTokens.Motion.Move.layout.easing),
    DRILL(OrreryTokens.Motion.Move.drill.millis, OrreryTokens.Motion.Move.drill.easing),
    LEAVE(OrreryTokens.Motion.Move.leave.millis, OrreryTokens.Motion.Move.leave.easing),
    RISE(OrreryTokens.Motion.Move.rise.millis, OrreryTokens.Motion.Move.rise.easing),
    FOLD(OrreryTokens.Motion.Move.fold.millis, OrreryTokens.Motion.Move.fold.easing),
}

val EaseOut: Easing = OrreryTokens.Motion.Ease.out
val EaseIn: Easing = OrreryTokens.Motion.Ease.`in`

fun <T> Move.spec(animated: Boolean): FiniteAnimationSpec<T> =
    if (animated) {
        tween(millis, easing = easing)
    } else {
        tween(OrreryTokens.Motion.Reduced.millis, easing = OrreryTokens.Motion.Ease.linear)
    }

// The phone sheet's unfold: Move.RISE after motion.delay.rise, on its own ease. With animations off it is one linear step of
// motion.reduced, and no delay.
fun <T> riseSpec(animated: Boolean): FiniteAnimationSpec<T> =
    if (animated) {
        tween(Move.RISE.millis, delayMillis = OrreryTokens.Motion.Delay.rise, easing = Move.RISE.easing)
    } else {
        tween(OrreryTokens.Motion.Reduced.millis, easing = OrreryTokens.Motion.Ease.linear)
    }

// The phone sheet's alpha on its unfold. It follows the clock rather than the unfold's ease: it is solid 30% of the way
// through Move.RISE, counted from the end of motion.delay.rise, and linear in between.
fun riseFadeSpec(): FiniteAnimationSpec<Float> =
    tween(
        Move.RISE.millis * 3 / 10,
        delayMillis = OrreryTokens.Motion.Delay.rise,
        easing = OrreryTokens.Motion.Ease.linear,
    )

// The phone sheet's alpha on its fold. It follows the clock rather than the fold's ease: it stays solid until 70% of the way
// through Move.FOLD, then falls to 0 by the fold's end, linear in between.
fun foldFadeSpec(): FiniteAnimationSpec<Float> =
    tween(
        Move.FOLD.millis - Move.FOLD.millis * 7 / 10,
        delayMillis = Move.FOLD.millis * 7 / 10,
        easing = OrreryTokens.Motion.Ease.linear,
    )

// The chart's glow. One pulse is motion.loop.pulse: each way takes half of it and the value reverses rather than restarting.
fun pulseSpec(): InfiniteRepeatableSpec<Float> =
    infiniteRepeatable(
        tween(OrreryTokens.Motion.Loop.pulse / 2, easing = OrreryTokens.Motion.Ease.linear),
        RepeatMode.Reverse,
    )

// Whether animations run. It is false when the system's animator duration scale is 0, the read the chart's glow makes.
@Composable
fun animationsOn(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    }
}
