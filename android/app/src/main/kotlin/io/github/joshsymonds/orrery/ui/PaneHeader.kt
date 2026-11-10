package io.github.joshsymonds.orrery.ui

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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha

/**
 * A pane's header: a back control and the title, the project and opening prompt (or the cwd) in
 * at most two lines, the status line when asked, the host meters when there are stats, and a bar
 * back to the conversation when the live screen shows.
 */
@Composable
fun PaneHeader(
    pane: Pane,
    origin: String?,
    stats: List<HostStat>?,
    nowSeconds: Long,
    showStatus: Boolean,
    liveScreen: Boolean,
    onStatusClick: () -> Unit,
    onConversation: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .testTag("pane-header")
            .fillMaxWidth()
            .background(Mocha.Base),
    ) {
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
            if (showStatus) {
                StatusLine(pane, nowSeconds, onStatusClick, Modifier.padding(top = 9.dp))
            }
            if (stats != null && stats.isNotEmpty()) {
                HostMeters(stats, Modifier.padding(top = 10.dp, start = 2.dp, end = 2.dp))
            }
        }
        if (liveScreen) {
            HairLine()
            LiveScreenBar(onConversation)
        }
        HairLine()
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

/** The project is the last path segment of the pane's cwd, as orreryd names it. */
private fun projectOf(cwd: String): String = cwd.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }
