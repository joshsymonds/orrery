package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.currentTool
import io.github.joshsymonds.orrery.ui.theme.Mocha

/**
 * What a Claude pane is doing right now. Working shows the current tool in plain words with its
 * elapsed time, and a second row on when a message sent now lands; a background wait reads
 * "background" instead of "working". Waiting shows its age; done shows its age. Any other state
 * draws nothing. While working or waiting the line is tinted with its state's colour, so an
 * assistant in flight, or one waiting on the reader, shows at a glance.
 */
@Composable
fun StatusLine(pane: Pane, nowSeconds: Long, modifier: Modifier = Modifier) {
    val status = statusOf(pane, nowSeconds) ?: return
    Column(
        modifier = modifier
            .testTag("status-line")
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .background(if (status.tinted) status.colour.copy(alpha = TINT_ALPHA) else Color.Transparent)
            .padding(horizontal = 10.dp, vertical = 6.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Box(modifier = Modifier.size(8.dp).clip(CircleShape).background(status.colour))
            Text(
                text = status.label,
                color = status.labelColour,
                fontWeight = FontWeight.Bold,
                fontSize = 12.5.sp,
                maxLines = 1,
            )
            if (status.tool.isNotEmpty()) {
                Text(
                    text = status.tool,
                    color = Mocha.Blue,
                    fontWeight = FontWeight.Bold,
                    fontSize = 12.5.sp,
                    maxLines = 1,
                )
            }
            Spacer(modifier = Modifier.weight(1f))
            Text(
                text = status.time,
                color = Mocha.Subtext1,
                fontSize = 12.5.sp,
                maxLines = 1,
            )
        }
        if (status.note.isNotEmpty()) {
            Text(
                text = status.note,
                color = Mocha.Subtext0,
                fontSize = 12.5.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

private const val TINT_ALPHA = 0.12f

private class StatusText(
    val label: String,
    val colour: Color,
    val time: String,
    val tinted: Boolean,
    val tool: String = "",
    val note: String = "",
    val labelColour: Color = colour,
)

private fun statusOf(pane: Pane, nowSeconds: Long): StatusText? = when (pane.state) {
    "working" -> {
        val tool = pane.currentTool()
        val words = toolWords(tool?.name.orEmpty())
        StatusText(
            label = if (words.background) "background" else "working",
            colour = Mocha.Yellow,
            labelColour = if (words.background) Mocha.Lavender else Mocha.Yellow,
            tool = words.text,
            time = clock(nowSeconds - (tool?.start ?: pane.changed)),
            tinted = true,
            note = landsNote(pane, nowSeconds),
        )
    }
    "waiting" -> StatusText(
        label = "waiting for you",
        colour = Mocha.Peach,
        time = age(nowSeconds - pane.changed),
        tinted = true,
    )
    "done" -> StatusText(
        label = "done",
        colour = Mocha.Green,
        time = "${age(nowSeconds - pane.changed)} ago",
        tinted = false,
    )
    else -> null
}

/** Where a message sent now lands, and when the last turn ended once the pane has one. */
private fun landsNote(pane: Pane, nowSeconds: Long): String {
    val lands = "a message sent now lands at Claude's next step"
    return if (pane.lastTurnEnd > 0L) {
        "$lands · last turn ended ${age(nowSeconds - pane.lastTurnEnd)} ago"
    } else {
        lands
    }
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
