package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.flow.drop

internal const val TERMINAL_VIEWPORT_TAG = "terminal-viewport"
internal const val TERMINAL_TEXT_TAG = "terminal-text"

private const val START_SIZE_SP = 11f
private const val MIN_SIZE_SP = 6f
private const val MAX_SIZE_SP = 24f
private const val LINE_HEIGHT_RATIO = 1.35f

/** A scroll state reports this as its maximum until its first layout. */
private const val UNMEASURED_MAX = Int.MAX_VALUE

/**
 * Shows a pane's screen the way a terminal does: each line on one row with no wrapping, panned
 * both ways, with a pinch setting the text size. It opens at the newest line and the left edge.
 * While the reader is at the bottom, new text keeps the newest line in view; once they scroll
 * away, new text leaves their position alone. The view is display only.
 *
 * [verticalState] is the vertical scroll position, which the caller may read; [topPadding] sits
 * above the first line, inside the scrolling content, so a caller can clear an overlay.
 */
@Composable
fun TerminalView(
    text: String,
    modifier: Modifier = Modifier,
    verticalState: ScrollState = rememberScrollState(),
    topPadding: Dp = 0.dp,
) {
    val styled = remember(text) { parseAnsi(text) }
    var sizeSp by rememberSaveable { mutableFloatStateOf(START_SIZE_SP) }
    val horizontal = rememberScrollState()
    var followBottom by remember { mutableStateOf(true) }

    LaunchedEffect(verticalState) {
        // The first value is where the view starts, not a move by the reader: when the text is
        // already there at the first layout, reading it would turn following off before the
        // view ever reached the newest line.
        snapshotFlow { verticalState.value }.drop(1).collect {
            if (verticalState.maxValue != UNMEASURED_MAX) followBottom = it >= verticalState.maxValue
        }
    }
    LaunchedEffect(verticalState) {
        snapshotFlow { verticalState.maxValue }.collect {
            if (it != UNMEASURED_MAX && followBottom) verticalState.scrollTo(it)
        }
    }

    Box(
        modifier
            .fillMaxSize()
            .testTag(TERMINAL_VIEWPORT_TAG)
            .pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) {
                        val event = awaitPointerEvent()
                        val pointers = event.changes.filter { it.pressed }
                        if (pointers.size < 2) continue
                        val (first, second) = pointers
                        val before = (first.previousPosition - second.previousPosition).getDistance()
                        val now = (first.position - second.position).getDistance()
                        if (before > 0f && now > 0f) {
                            sizeSp = (sizeSp * now / before).coerceIn(MIN_SIZE_SP, MAX_SIZE_SP)
                        }
                        event.changes.forEach { it.consume() }
                    }
                }
            }
            .verticalScroll(verticalState)
            .horizontalScroll(horizontal),
    ) {
        Text(
            styled,
            fontSize = sizeSp.sp,
            lineHeight = (sizeSp * LINE_HEIGHT_RATIO).sp,
            softWrap = false,
            modifier = Modifier.padding(top = topPadding).testTag(TERMINAL_TEXT_TAG),
        )
    }
}
