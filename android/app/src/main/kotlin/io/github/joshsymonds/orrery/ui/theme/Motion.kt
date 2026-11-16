package io.github.joshsymonds.orrery.ui.theme

import android.provider.Settings
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.Easing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.snap
import androidx.compose.animation.core.tween
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

// The phone's motion tokens. Each move tweens while animations are on and snaps when the system's animator duration scale
// is 0.
enum class Move(val millis: Int, val easing: Easing) {
    PRESS(120, EaseOut),
    STATE(200, EaseOut),
    LAYOUT(280, EaseOut),
    DRILL(320, EaseOut),
    LEAVE(200, EaseIn),
}

val EaseOut: Easing = CubicBezierEasing(0.2f, 0f, 0f, 1f)
val EaseIn: Easing = CubicBezierEasing(0.4f, 0f, 1f, 1f)

fun <T> Move.spec(animated: Boolean): FiniteAnimationSpec<T> = if (animated) tween(millis, easing = easing) else snap()

// Whether animations run. It is false when the system's animator duration scale is 0, the read the chart's glow makes.
@Composable
fun animationsOn(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    }
}
