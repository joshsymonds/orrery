package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.ButtonColors
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.SemanticsPropertyKey
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.ui.theme.Mocha
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val FLASH_MILLIS = 300L

/** Semantics flag: true while a tapped control is flashing. */
val Flashing = SemanticsPropertyKey<Boolean>("Flashing")

/**
 * Answer to a tap: a LongPress haptic at once, then a flash for [FLASH_MILLIS].
 * A tap during a flash restarts it.
 */
class TapFeedback internal constructor(
    private val haptic: HapticFeedback,
    private val scope: CoroutineScope,
) {
    var flashing by mutableStateOf(false)
        private set

    private var flash: Job? = null

    fun tap() {
        haptic.performHapticFeedback(HapticFeedbackType.LongPress)
        flash?.cancel()
        flashing = true
        flash = scope.launch {
            delay(FLASH_MILLIS)
            flashing = false
        }
    }
}

/** Feedback for one control; call [TapFeedback.tap] from its onClick. */
@Composable
fun rememberTapFeedback(): TapFeedback {
    val haptic = LocalHapticFeedback.current
    val scope = rememberCoroutineScope()
    return remember(haptic, scope) { TapFeedback(haptic, scope) }
}

/** Puts [Flashing] on the control's merged semantics node, alongside its label. */
fun Modifier.tapFeedback(feedback: TapFeedback): Modifier =
    semantics(mergeDescendants = true) { this[Flashing] = feedback.flashing }

/** Container colours for a button: the highlight while flashing, the Material default otherwise. */
@Composable
fun tapFeedbackColors(feedback: TapFeedback): ButtonColors =
    if (feedback.flashing) {
        ButtonDefaults.buttonColors(containerColor = Mocha.Yellow, contentColor = Mocha.Crust)
    } else {
        ButtonDefaults.buttonColors()
    }
