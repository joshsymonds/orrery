package io.github.joshsymonds.orrery.ui.theme

import android.provider.Settings
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.InfiniteRepeatableSpec
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

// The phone's motion tokens. Each move tweens while animations are on and snaps when the system's animator duration scale
// is 0.
enum class Move(val millis: Int, val easing: Easing) {
    PRESS(OrreryTokens.Motion.Move.press.millis, OrreryTokens.Motion.Move.press.easing),
    STATE(OrreryTokens.Motion.Move.state.millis, OrreryTokens.Motion.Move.state.easing),
    LAYOUT(OrreryTokens.Motion.Move.layout.millis, OrreryTokens.Motion.Move.layout.easing),
    DRILL(OrreryTokens.Motion.Move.drill.millis, OrreryTokens.Motion.Move.drill.easing),
    LEAVE(OrreryTokens.Motion.Move.leave.millis, OrreryTokens.Motion.Move.leave.easing),
}

val EaseOut: Easing = OrreryTokens.Motion.Ease.out
val EaseIn: Easing = OrreryTokens.Motion.Ease.`in`

fun <T> Move.spec(animated: Boolean): FiniteAnimationSpec<T> = if (animated) tween(millis, easing = easing) else snap()

// The chart's glow. One pulse is motion.loop.pulse: each way takes half of it and the value reverses rather than restarting.
fun pulseSpec(): InfiniteRepeatableSpec<Float> =
    infiniteRepeatable(tween(OrreryTokens.Motion.Loop.pulse / 2, easing = FastOutSlowInEasing), RepeatMode.Reverse)

// Whether animations run. It is false when the system's animator duration scale is 0, the read the chart's glow makes.
@Composable
fun animationsOn(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    }
}
