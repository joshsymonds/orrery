package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * A conversation's title row, after round 6's touch title row: back, then the project's name and host as the inbox
 * draws a project's name, over the title in bold body type, one step down from the title type, cut short at its second
 * line, then the bare context meter when there is a figure, and More. It draws no Orrery icon and no count. More opens a menu holding Find and Hide the header. A
 * non-empty [note] draws under the row, in the crit colour.
 */
@Composable
fun HeadTitleRow(
    dir: String,
    host: String,
    title: String,
    context: ConversationContext?,
    note: String,
    onBack: () -> Unit,
    onMore: () -> Unit,
    moreOpen: Boolean,
    onDismissMore: () -> Unit,
    onHideHeader: () -> Unit,
    onFind: () -> Unit = {},
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                start = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                end = OrreryTokens.Space.twelve + OrreryTokens.Space.two,
                top = OrreryTokens.Space.eight,
            ),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().testTag("head-title"),
            verticalAlignment = Alignment.Top,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight + OrreryTokens.Space.two),
        ) {
            Text(
                text = "‹",
                modifier = Modifier.testTag("head-back").clickable(onClick = onBack),
                color = OrreryTokens.Ink.two,
                style = OrreryTokens.Type.title,
            )
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = buildAnnotatedString {
                        withStyle(SpanStyle(color = OrreryTokens.Role.place)) { append(if (host.isEmpty()) dir else "$dir · on $host") }
                    },
                    modifier = Modifier.testTag("head-dir"),
                    style = ProjectNameStyle,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = title,
                    modifier = Modifier.testTag("head-text"),
                    color = OrreryTokens.Ink.one,
                    style = OrreryTokens.Type.body.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            bareContext(context)?.let { meter ->
                Text(
                    text = buildAnnotatedString { withStyle(SpanStyle(color = meter.color)) { append(meter.text) } },
                    modifier = Modifier.testTag("head-context"),
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                )
            }
            MoreMenu(open = moreOpen, onMore = onMore, onDismiss = onDismissMore, onFind = onFind) {
                DropdownMenuItem(
                    text = { Text("Hide the header") },
                    onClick = {
                        onDismissMore()
                        onHideHeader()
                    },
                    modifier = Modifier.testTag("head-menu-hide"),
                )
            }
        }
        if (note.isNotEmpty()) {
            Text(
                text = note,
                modifier = Modifier.padding(top = OrreryTokens.Space.four).testTag("head-note"),
                color = OrreryTokens.Role.bad,
                style = OrreryTokens.Type.small,
            )
        }
    }
}

/**
 * A title row's More: the ⋯ glyph, which opens a menu holding Find and then [items]. Find closes the menu, then calls
 * [onFind]; each of [items] closes it the same way through [onDismiss].
 */
@Composable
fun MoreMenu(
    open: Boolean,
    onMore: () -> Unit,
    onDismiss: () -> Unit,
    onFind: () -> Unit,
    items: @Composable ColumnScope.() -> Unit = {},
) {
    Box {
        Text(
            text = "⋯",
            modifier = Modifier
                .testTag("head-more")
                .semantics { contentDescription = "More" }
                .clickable(onClick = onMore),
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.title,
        )
        DropdownMenu(
            expanded = open,
            onDismissRequest = onDismiss,
            modifier = Modifier.testTag("head-menu"),
        ) {
            DropdownMenuItem(
                text = { Text("Find") },
                onClick = {
                    onDismiss()
                    onFind()
                },
                modifier = Modifier.testTag("head-menu-find"),
            )
            items()
        }
    }
}

/**
 * The host's stats as meters, from [HostMeters]. A host other than the serving one draws its [otherHost] name and
 * says it has no meters, and draws no stats. Null [stats] with no other host draws nothing.
 */
@Composable
fun HeadMeters(stats: List<HostStat>?, otherHost: String?) {
    if (otherHost != null) {
        Column(modifier = Modifier.fillMaxWidth().testTag("head-other-host")) {
            Text(text = otherHost, color = OrreryTokens.Ink.one, style = OrreryTokens.Type.small)
            Text(text = "on another host: no meters", color = OrreryTokens.Ink.three, style = OrreryTokens.Type.small)
        }
    } else if (!stats.isNullOrEmpty()) {
        Box(modifier = Modifier.fillMaxWidth().testTag("head-meters")) {
            HostMeters(stats, Modifier.padding(horizontal = OrreryTokens.Space.two))
        }
    }
}

/** A bare context meter: its block bar and percent as [text], and the level [color] the percent falls in. */
private class HeadContext(val text: String, val color: Color)

/**
 * The context meter for [context] without its label or token counts: the percent of the window in use, clamped to
 * 0..100, drawn as five blocks. Blocks round to the nearest, with any use showing at least one. Coloured ok below the
 * first limit, warn below the second and crit from there. Null when there is no figure: no context, or no tokens or
 * window.
 */
private fun bareContext(context: ConversationContext?): HeadContext? {
    if (context == null || context.tokens <= 0 || context.window <= 0) return null
    val percent = (100 * context.tokens / context.window).coerceIn(0L, 100L).toInt()
    val count = OrreryTokens.Rule.Context.blocks
    val perBlock = OrreryTokens.Rule.Context.percentPerBlock
    // round(percent / perBlock) in integers: a half rounds up.
    val rounded = (2 * percent + perBlock) / (2 * perBlock)
    val filled = minOf(count, maxOf(if (percent > 0) 1 else 0, rounded))
    val color = when {
        percent < OrreryTokens.Rule.Context.limits[0] -> OrreryTokens.Level.ok
        percent < OrreryTokens.Rule.Context.limits[1] -> OrreryTokens.Level.warn
        else -> OrreryTokens.Level.crit
    }
    val bar = "▰".repeat(filled) + "▱".repeat(count - filled)
    return HeadContext("$bar $percent%", color)
}
