package io.github.joshsymonds.orrery.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha

private const val COMPACT_MILLIS = 180
private const val METER_BLOCKS = 5

/**
 * A pane's header: a back control and the title, the project and opening prompt (or the cwd) in
 * at most two lines, the host meters when there are stats, and a bar back to the conversation
 * when the live screen shows.
 *
 * [compact] folds the whole header away, to nothing, and brings it back when it clears.
 */
@Composable
fun PaneHeader(
    pane: Pane,
    origin: String?,
    stats: List<HostStat>?,
    context: ConversationContext?,
    liveScreen: Boolean,
    onConversation: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    compact: Boolean = false,
) {
    val fold = tween<Float>(COMPACT_MILLIS)
    val foldSize = tween<IntSize>(COMPACT_MILLIS)
    Column(
        modifier = modifier
            .testTag("pane-header")
            .fillMaxWidth()
            .background(Mocha.Base),
    ) {
        AnimatedVisibility(
            visible = !compact,
            enter = expandVertically(foldSize, expandFrom = Alignment.Top) + fadeIn(fold),
            exit = shrinkVertically(foldSize, shrinkTowards = Alignment.Top) + fadeOut(fold),
            label = "header",
        ) {
            Column {
                Column(modifier = Modifier.padding(start = 14.dp, end = 14.dp, top = 8.dp, bottom = 10.dp)) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        Text(
                            text = "‹",
                            modifier = Modifier.testTag("pane-back").clickable(onClick = onBack),
                            color = Mocha.Subtext0,
                            fontSize = 24.sp,
                            lineHeight = 24.sp,
                        )
                        Text(
                            text = pane.label.ifEmpty { pane.command },
                            modifier = Modifier.weight(1f).testTag("pane-title"),
                            color = Mocha.Text,
                            fontWeight = FontWeight.Bold,
                            fontSize = 15.5.sp,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        contextMeter(context)?.let { meter ->
                            Text(
                                text = buildAnnotatedString { withStyle(SpanStyle(color = meter.color)) { append(meter.text) } },
                                modifier = Modifier.testTag("context-meter"),
                                fontSize = 12.sp,
                                maxLines = 1,
                            )
                        }
                    }
                    Text(
                        text = originLine(pane, origin),
                        modifier = Modifier.padding(top = 5.dp).fillMaxWidth().testTag("pane-origin"),
                        color = Mocha.Subtext0,
                        fontSize = 12.5.sp,
                        lineHeight = 18.sp,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    stats?.takeIf { it.isNotEmpty() }?.let {
                        HostMeters(it, Modifier.padding(top = 10.dp, start = 2.dp, end = 2.dp))
                    }
                }
                if (liveScreen) {
                    HairLine()
                    LiveScreenBar(onConversation)
                }
                HairLine()
            }
        }
    }
}

@Composable
private fun LiveScreenBar(onConversation: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 14.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(
            text = "‹ conversation",
            modifier = Modifier
                .testTag("live-return")
                .clip(RoundedCornerShape(6.dp))
                .background(Mocha.Surface0)
                .clickable(onClick = onConversation)
                .padding(horizontal = 9.dp, vertical = 2.dp),
            color = Mocha.Mauve,
            fontSize = 12.sp,
            maxLines = 1,
        )
        Text(
            text = "live screen",
            color = Mocha.Overlay1,
            fontSize = 11.sp,
            maxLines = 1,
        )
    }
}

@Composable
private fun HairLine() {
    Box(modifier = Modifier.fillMaxWidth().height(1.dp).background(Mocha.Surface0))
}

/** The project in bold mauve, then the opening prompt, or the cwd when there is no prompt. */
private fun originLine(pane: Pane, origin: String?): AnnotatedString = buildAnnotatedString {
    withStyle(SpanStyle(color = Mocha.Mauve, fontWeight = FontWeight.Bold)) {
        append(projectOf(pane.cwd))
    }
    append("  ")
    append(origin ?: pane.cwd)
}

/** A context meter: its five-block bar and percent as [text], and the band [color] the percent falls in. */
private class ContextMeter(val text: String, val color: Color)

/**
 * steward's context meter for [context]: the percent of the window in use, clamped to 0..100, drawn
 * as blocks rounded to the nearest fifth, and coloured green below 40%, yellow below 60%, peach below
 * 80% and red from there. Null when there is no figure: no context, or no tokens or window.
 */
private fun contextMeter(context: ConversationContext?): ContextMeter? {
    if (context == null || context.tokens <= 0 || context.window <= 0) return null
    val percent = (100 * context.tokens / context.window).coerceIn(0L, 100L).toInt()
    // round(percent / 20) in integers: each block is 20%, and a half rounds up.
    val filled = minOf(METER_BLOCKS, (2 * percent + 20) / 40)
    val color = when {
        percent < 40 -> Mocha.Green
        percent < 60 -> Mocha.Yellow
        percent < 80 -> Mocha.Peach
        else -> Mocha.Red
    }
    val blocks = "▰".repeat(filled) + "▱".repeat(METER_BLOCKS - filled)
    return ContextMeter("$blocks $percent%", color)
}

/** The project is the last path segment of the pane's cwd, as orreryd names it. */
private fun projectOf(cwd: String): String = cwd.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }
