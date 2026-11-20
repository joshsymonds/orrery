package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextDecoration
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase

data class DockTrayRow(val key: String, val text: String, val cancelled: Boolean, val waits: String, val promptId: String)

/**
 * The queued messages on the dock, oldest first, under a head with the tray's label and why. A session's
 * live row can be taken back into the box, a discarded one put back; a pane's rows come back all together
 * from the head, and Take all back is drawn there only while a row is not cancelled. Send now is drawn on a session's
 * head only while it is working. Nothing is drawn with no rows.
 */
@Composable
fun DockTray(
    pane: Boolean,
    rows: List<DockTrayRow>,
    working: Boolean,
    modifier: Modifier = Modifier,
    notes: Map<String, String> = emptyMap(),
    onTakeBack: (String) -> Unit = {},
    onPutBack: (String) -> Unit = {},
    onTakeAllBack: () -> Unit = {},
    onSendNow: (() -> Unit)? = null,
) {
    if (rows.isEmpty()) return
    Column(
        modifier = modifier
            .testTag("dock-tray")
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.four),
    ) {
        TrayHead(pane = pane, rows = rows, working = working, onTakeAllBack = onTakeAllBack, onSendNow = onSendNow)
        rows.forEach { row ->
            TrayRow(row = row, pane = pane, onTakeBack = onTakeBack, onPutBack = onPutBack)
            notes[row.key]?.let { TrayNote(it) }
        }
    }
}

@Composable
private fun TrayHead(
    pane: Boolean,
    rows: List<DockTrayRow>,
    working: Boolean,
    onTakeAllBack: () -> Unit,
    onSendNow: (() -> Unit)?,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = OrreryTokens.Space.twelve, vertical = OrreryTokens.Space.two),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = trayLabel(rows.size).inLabelCase(),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.label,
        )
        Text(
            text = trayWhy(pane, rows.map { TrayRowFacts(it.waits, it.cancelled) }),
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.three,
            style = OrreryTokens.Type.small,
        )
        if (pane) {
            if (rows.any { !it.cancelled }) {
                TrayTextControl(label = "Take all back", tag = "dock-take-all-back", onClick = onTakeAllBack)
            }
        } else if (working && onSendNow != null) {
            TrayTextControl(label = "Send now", tag = "dock-send-now", onClick = onSendNow)
        }
    }
}

@Composable
private fun TrayRow(
    row: DockTrayRow,
    pane: Boolean,
    onTakeBack: (String) -> Unit,
    onPutBack: (String) -> Unit,
) {
    val rail = if (row.cancelled) OrreryTokens.Role.halt else OrreryTokens.Role.you
    Row(
        modifier = Modifier
            .testTag("dock-tray-row")
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.twelve, end = OrreryTokens.Space.twelve)
            .height(IntrinsicSize.Min),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Box(
            modifier = Modifier
                .width(OrreryTokens.Size.rail)
                .fillMaxHeight()
                .drawBehind { dashedRail(rail) },
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(vertical = OrreryTokens.Space.eight),
        ) {
            if (row.cancelled) {
                Text(
                    text = DockLines.notDelivered.inLabelCase(),
                    color = OrreryTokens.Ink.three,
                    style = OrreryTokens.Type.label,
                )
            }
            Text(
                text = row.text,
                color = if (row.cancelled) OrreryTokens.Ink.three else OrreryTokens.Ink.one,
                style = OrreryTokens.Type.body,
                textDecoration = if (row.cancelled) TextDecoration.LineThrough else null,
            )
        }
        when {
            pane -> Unit
            row.cancelled -> TrayIconControl(
                description = "Put back in the box",
                tag = "dock-put-back",
                onClick = { onPutBack(row.promptId) },
            )
            else -> TrayIconControl(
                description = "Take this back into the box",
                tag = "dock-take-back",
                onClick = { onTakeBack(row.promptId) },
            )
        }
    }
}

@Composable
private fun TrayNote(text: String) {
    Text(
        text = text,
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = OrreryTokens.Space.sixteen, end = OrreryTokens.Space.twelve, bottom = OrreryTokens.Space.eight),
        color = OrreryTokens.Ink.three,
        style = OrreryTokens.Type.small,
    )
}

@Composable
private fun TrayTextControl(label: String, tag: String, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag(tag)
            .height(OrreryTokens.Size.controlTouch)
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.raised)
            .clickable(onClick = onClick)
            .padding(horizontal = OrreryTokens.Space.twelve),
        contentAlignment = Alignment.Center,
    ) {
        Text(text = label, color = OrreryTokens.Ink.one, style = OrreryTokens.Type.small)
    }
}

@Composable
private fun TrayIconControl(description: String, tag: String, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .testTag(tag)
            .semantics { contentDescription = description }
            .size(OrreryTokens.Size.controlTouch)
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.size(OrreryTokens.Size.icon)) {
            backArrow(OrreryTokens.Ink.two)
        }
    }
}

/** The rail: a dashed line in the row's tone, down its full height. */
private fun DrawScope.dashedRail(color: Color) {
    val x = size.width / 2f
    drawLine(
        color = color,
        start = Offset(x, 0f),
        end = Offset(x, size.height),
        strokeWidth = size.width,
        pathEffect = PathEffect.dashPathEffect(
            floatArrayOf(OrreryTokens.Space.four.toPx(), OrreryTokens.Space.two.toPx()),
        ),
    )
}

/** An arrow pointing left, back into the box. */
private fun DrawScope.backArrow(color: Color) {
    val stroke = OrreryTokens.Size.hairline.toPx()
    val mid = size.height / 2f
    val tip = Offset(size.width * 0.15f, mid)
    drawLine(color, Offset(size.width * 0.85f, mid), tip, strokeWidth = stroke, cap = StrokeCap.Round)
    drawLine(color, tip, Offset(size.width * 0.45f, size.height * 0.2f), strokeWidth = stroke, cap = StrokeCap.Round)
    drawLine(color, tip, Offset(size.width * 0.45f, size.height * 0.8f), strokeWidth = stroke, cap = StrokeCap.Round)
}
