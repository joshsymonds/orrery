package io.github.joshsymonds.orrery.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlin.math.sign

const val HEADER_HIDE_AFTER_DP = 28f
const val HEADER_SHOW_AFTER_DP = 10f

/**
 * The header's visibility while the list scrolls. A run of scrolling toward newer content
 * (positive deltas) hides the header once it passes [hideAfterPx]; a run back toward older
 * content shows it once it passes [showAfterPx]. A reversal starts a new run. At the top or
 * the bottom the header is always visible and the run resets.
 */
class HeaderScrollRule(private val hideAfterPx: Float, private val showAfterPx: Float) {
    var visible by mutableStateOf(true)
        private set

    private var run = 0f

    fun onScroll(deltaPx: Float, atTop: Boolean, atBottom: Boolean) {
        if (atTop || atBottom) {
            visible = true
            run = 0f
            return
        }
        run = if (sign(deltaPx) == sign(run)) run + deltaPx else deltaPx
        if (run > hideAfterPx) {
            visible = false
        } else if (run < -showAfterPx) {
            visible = true
        }
    }
}
