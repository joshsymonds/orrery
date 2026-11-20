package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/**
 * What the dock's More stops: [more] is the More's description ("Stop this step…", "Stop this session…"), [name] is
 * what the confirm asks about, and [onStop] is the stop itself, called once the confirm is tapped.
 */
data class DockStop(val more: String, val name: String, val onStop: () -> Unit)

/**
 * The dock's state line: the word in its state's colour, the tool and what, the clock, then the controls at its end.
 * [stop] adds More at the end, which asks before it calls [DockStop.onStop]; a held step has More and no Interrupt.
 */
@Composable
fun DockStateLine(
    line: StateLine,
    onInterrupt: () -> Unit,
    onResume: () -> Unit,
    modifier: Modifier = Modifier,
    stop: DockStop? = null,
) {
    var confirming by remember(stop?.name) { mutableStateOf(false) }

    Column(modifier = modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .testTag("dock-state")
                .fillMaxWidth()
                .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.four),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        ) {
            val plain = line.tone == "done" || line.tone == "idle" || line.tone == "stopped"
            Text(
                text = line.word,
                modifier = Modifier.testTag("dock-word"),
                color = if (plain) OrreryTokens.Ink.two else stateColour(line.tone),
                style = OrreryTokens.Type.small.copy(
                    fontWeight = if (plain) OrreryTokens.Font.Weight.regular else OrreryTokens.Font.Weight.bold,
                ),
                maxLines = 1,
            )
            if (line.tool.isNotEmpty()) {
                Text(
                    text = line.tool,
                    color = OrreryTokens.Role.ref,
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                )
            }
            if (line.what.isNotEmpty()) {
                Text(
                    text = line.what,
                    modifier = Modifier.weight(1f),
                    color = OrreryTokens.Ink.two,
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            } else {
                Box(modifier = Modifier.weight(1f))
            }
            if (line.clock.isNotEmpty()) {
                Text(
                    text = line.clock,
                    color = stateColour(line.tone),
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                )
            }
            when (line.control) {
                DockControl.INTERRUPT -> DockCtl("Interrupt", "dock-interrupt", onClick = onInterrupt)
                DockControl.INTERRUPTING -> DockCtl("Interrupting…", "dock-interrupt", enabled = false, onClick = {})
                DockControl.RESUME -> DockCtl("Resume", "dock-resume", onClick = onResume)
                DockControl.NONE -> Unit
            }
            if (stop != null) {
                MoreButton(description = stop.more, onClick = { confirming = true })
            }
        }
        if (stop != null && confirming) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.four),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            ) {
                Text(
                    text = "Stop ${stop.name}?",
                    color = OrreryTokens.Ink.one,
                    style = OrreryTokens.Type.small,
                    maxLines = 1,
                )
                DockCtl("Stop ${stop.name}", "dock-stop-confirm") {
                    confirming = false
                    stop.onStop()
                }
                DockCtl("Keep it running", "dock-keep-running") {
                    confirming = false
                }
            }
        }
    }
}

private fun stateColour(tone: String): Color = when (tone) {
    "working" -> OrreryTokens.State.working
    "waiting" -> OrreryTokens.State.waiting
    "held" -> OrreryTokens.State.held
    "done" -> OrreryTokens.State.done
    "interrupted" -> OrreryTokens.State.interrupted
    "interrupting" -> OrreryTokens.State.interrupting
    "failed" -> OrreryTokens.State.failed
    "stopped" -> OrreryTokens.State.stopped
    else -> OrreryTokens.State.idle
}

/** A control at the line's height: text in the ink colour, spent when disabled. */
@Composable
private fun DockCtl(label: String, tag: String, enabled: Boolean = true, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag(tag)
            .height(OrreryTokens.Space.twentyFour)
            .clip(RoundedCornerShape(OrreryTokens.Radius.one))
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .padding(horizontal = OrreryTokens.Space.eight),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            color = if (enabled) OrreryTokens.Ink.two else OrreryTokens.Ink.four,
            style = OrreryTokens.Type.small,
            maxLines = 1,
        )
    }
}

/** The dock's More: three dots, icon only, named [description]. */
@Composable
private fun MoreButton(description: String, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag("dock-more")
            .size(OrreryTokens.Space.twentyFour)
            .clip(RoundedCornerShape(OrreryTokens.Radius.one))
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.size(OrreryTokens.Size.icon)) {
            val radius = OrreryTokens.Size.dot.toPx() / 2
            val y = size.height / 2
            listOf(0.2f, 0.5f, 0.8f).forEach { fraction ->
                drawCircle(color = OrreryTokens.Ink.two, radius = radius, center = Offset(size.width * fraction, y))
            }
        }
    }
}
