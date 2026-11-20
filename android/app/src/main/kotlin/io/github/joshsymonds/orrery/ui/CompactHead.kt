package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * One badge on the folded header: a steered epic (its slug's first part, [asks] when it has an
 * ask), the running agents, or the running shells. A count above zero is drawn after the label.
 */
data class HeadBadge(val key: String, val label: String, val count: Int, val asks: Boolean)

/**
 * The phone header folded to one line of badges. When [asking] names what waits on you the line
 * is peach and draws those words, and each asking badge reads "?"; nothing asking leaves the line
 * plain. A tap opens the header again.
 */
@Composable
fun CompactHead(asking: String, badges: List<HeadBadge>, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val asks = asking.isNotEmpty()
    Row(
        modifier = modifier
            .testTag("compact-head")
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(if (asks) OrreryTokens.Role.attentionFill else Color.Transparent)
            .clickable(onClick = onOpen)
            .semantics { contentDescription = "Show the header" }
            .padding(horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        if (asks) {
            Text(
                text = AnnotatedString(asking, SpanStyle(color = OrreryTokens.Role.attention)),
                modifier = Modifier.weight(1f).testTag("compact-asking"),
                style = OrreryTokens.Type.small,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        badges.forEach { badge ->
            Text(
                text = AnnotatedString(
                    badgeText(badge),
                    SpanStyle(color = if (badge.asks) OrreryTokens.Role.attention else OrreryTokens.Ink.two),
                ),
                modifier = Modifier.testTag("compact-badge:${badge.key}"),
                style = OrreryTokens.Type.small,
                maxLines = 1,
            )
        }
        if (!asks) Spacer(Modifier.weight(1f))
        Text(
            text = "⌄",
            color = OrreryTokens.Ink.four,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}

private fun badgeText(badge: HeadBadge): String = when {
    badge.asks -> "${badge.label}?"
    badge.count > 0 -> "${badge.label} ${badge.count}"
    else -> badge.label
}
