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
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.IntSize
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

private const val COMPACT_MILLIS = 180

/**
 * A pane's header: a back control and the title, the project and opening prompt (or the cwd) in
 * at most two lines, and the host meters when there are stats.
 *
 * [compact] folds the whole header away, to nothing, and brings it back when it clears.
 */
@Composable
fun PaneHeader(
    pane: Pane,
    origin: String?,
    stats: List<HostStat>?,
    context: ConversationContext?,
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
            .background(OrreryTokens.Surface.page),
    ) {
        AnimatedVisibility(
            visible = !compact,
            enter = expandVertically(foldSize, expandFrom = Alignment.Top) + fadeIn(fold),
            exit = shrinkVertically(foldSize, shrinkTowards = Alignment.Top) + fadeOut(fold),
            label = "header",
        ) {
            Column {
                Column(
                    modifier = Modifier.padding(
                        start = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                        end = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                        top = OrreryTokens.Space.eight,
                        bottom = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                    ),
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight + OrreryTokens.Space.two),
                    ) {
                        Text(
                            text = "‹",
                            modifier = Modifier.testTag("pane-back").clickable(onClick = onBack),
                            color = OrreryTokens.Ink.two,
                            style = OrreryTokens.Type.title,
                        )
                        Text(
                            text = pane.label.ifEmpty { pane.command },
                            modifier = Modifier.weight(1f).testTag("pane-title"),
                            color = OrreryTokens.Ink.one,
                            style = OrreryTokens.Type.body,
                            fontWeight = OrreryTokens.Font.Weight.bold,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        contextMeter(context)?.let { meter ->
                            Text(
                                text = buildAnnotatedString { withStyle(SpanStyle(color = meter.color)) { append(meter.text) } },
                                modifier = Modifier.testTag("context-meter"),
                                style = OrreryTokens.Type.small,
                                maxLines = 1,
                            )
                        }
                    }
                    Text(
                        text = originLine(pane, origin),
                        modifier = Modifier
                            .padding(top = OrreryTokens.Space.four + OrreryTokens.Size.hairline)
                            .fillMaxWidth()
                            .testTag("pane-origin"),
                        color = OrreryTokens.Ink.two,
                        style = OrreryTokens.Type.small,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    stats?.takeIf { it.isNotEmpty() }?.let {
                        HostMeters(
                            it,
                            Modifier.padding(
                                top = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                                start = OrreryTokens.Space.two,
                                end = OrreryTokens.Space.two,
                            ),
                        )
                    }
                }
                HairLine()
            }
        }
    }
}

@Composable
private fun HairLine() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(OrreryTokens.Size.hairline)
            .background(OrreryTokens.Line.soft),
    )
}

/** The project in bold in the place colour, then the opening prompt, or the cwd when there is no prompt. */
private fun originLine(pane: Pane, origin: String?): AnnotatedString = buildAnnotatedString {
    withStyle(SpanStyle(color = OrreryTokens.Role.place, fontWeight = OrreryTokens.Font.Weight.bold)) {
        append(projectOf(pane.cwd))
    }
    append("  ")
    append(origin ?: pane.cwd)
}

/** A context meter: its block bar and percent as [text], and the level [color] the percent falls in. */
private class ContextMeter(val text: String, val color: Color)

/**
 * steward's context meter for [context]: the percent of the window in use, clamped to 0..100, drawn
 * as blocks rounded to the nearest block, and coloured ok below the context's first limit, warn below
 * its second and crit from there. Null when there is no figure: no context, or no tokens or window.
 */
private fun contextMeter(context: ConversationContext?): ContextMeter? {
    if (context == null || context.tokens <= 0 || context.window <= 0) return null
    val percent = (100 * context.tokens / context.window).coerceIn(0L, 100L).toInt()
    val count = OrreryTokens.Rule.Context.blocks
    val perBlock = OrreryTokens.Rule.Context.percentPerBlock
    // round(percent / perBlock) in integers: a half rounds up.
    val filled = minOf(count, (2 * percent + perBlock) / (2 * perBlock))
    val color = when {
        percent < OrreryTokens.Rule.Context.limits[0] -> OrreryTokens.Level.ok
        percent < OrreryTokens.Rule.Context.limits[1] -> OrreryTokens.Level.warn
        else -> OrreryTokens.Level.crit
    }
    val bar = "▰".repeat(filled) + "▱".repeat(count - filled)
    return ContextMeter("$bar $percent%", color)
}

/** The project is the last path segment of the pane's cwd, as orreryd names it. */
private fun projectOf(cwd: String): String = cwd.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }
