package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.currentTool
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * What a Claude pane is doing right now. Working shows the current tool in plain words with its
 * elapsed time; a background wait reads "background" instead of "working". Waiting shows its age;
 * done shows its age. Any other state draws nothing. While working or waiting the line is tinted
 * with its state's colour, so an assistant in flight, or one waiting on the reader, shows at a glance.
 */
@Composable
fun StatusLine(pane: Pane, nowSeconds: Long, modifier: Modifier = Modifier) {
    val status = statusOf(pane, nowSeconds) ?: return
    Row(
        modifier = modifier
            .testTag("status-line")
            .fillMaxWidth()
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(status.fill ?: Color.Transparent)
            .padding(
                horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                vertical = OrreryTokens.Space.four + OrreryTokens.Space.two,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Box(modifier = Modifier.size(OrreryTokens.Size.dot).clip(CircleShape).background(status.colour))
        Text(
            text = status.label,
            color = status.labelColour,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        if (status.tool.isNotEmpty()) {
            Text(
                text = status.tool,
                color = OrreryTokens.Role.ref,
                style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                maxLines = 1,
            )
        }
        Spacer(modifier = Modifier.weight(1f))
        Text(
            text = status.time,
            color = OrreryTokens.Ink.two,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}

private class StatusText(
    val label: String,
    val colour: Color,
    val time: String,
    val fill: Color?,
    val tool: String = "",
    val labelColour: Color = colour,
)

private fun statusOf(pane: Pane, nowSeconds: Long): StatusText? = when (pane.state) {
    "working" -> {
        val tool = pane.currentTool()
        val words = toolWords(tool?.name.orEmpty())
        StatusText(
            label = if (words.background) "background" else "working",
            colour = OrreryTokens.State.working,
            labelColour = if (words.background) OrreryTokens.Ink.three else OrreryTokens.State.working,
            tool = words.text,
            time = clock(nowSeconds - (tool?.start ?: pane.changed)),
            fill = OrreryTokens.Role.liveFill,
        )
    }
    "waiting" -> StatusText(
        label = "waiting for you",
        colour = OrreryTokens.State.waiting,
        time = age(nowSeconds - pane.changed),
        fill = OrreryTokens.Role.attentionFill,
    )
    "done" -> StatusText(
        label = "done",
        colour = OrreryTokens.State.done,
        time = "${age(nowSeconds - pane.changed)} ago",
        fill = null,
    )
    else -> null
}

/** Elapsed time as m:ss, or h:mm:ss from an hour. */
private fun clock(seconds: Long): String {
    val total = seconds.coerceAtLeast(0L)
    val hours = total / 3_600
    val minutes = total % 3_600 / 60
    val secs = total % 60
    return if (hours > 0) {
        "%d:%02d:%02d".format(hours, minutes, secs)
    } else {
        "%d:%02d".format(minutes, secs)
    }
}

/** Age as the largest whole unit: seconds, minutes, hours, then days. */
private fun age(seconds: Long): String {
    val total = seconds.coerceAtLeast(0L)
    return when {
        total < 60 -> "${total}s"
        total < 3_600 -> "${total / 60}m"
        total < 86_400 -> "${total / 3_600}h"
        else -> "${total / 86_400}d"
    }
}
