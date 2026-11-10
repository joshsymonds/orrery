package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListLayoutInfo
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val TIME_OF_DAY: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

private enum class CallMark(val symbol: String, val colour: Color) {
    Done("✓", Mocha.Green),
    Failed("✗", Mocha.Red),
}

/**
 * The folded conversation as the phone draws it: time labels, prompt blocks, plain replies, tool
 * rows that expand to their result, and groups of calls that expand to their rows, with an
 * optional footer after the last entry. Which rows are open is kept by tool id (a group by its
 * first call's id), so it survives new entries. It opens at the newest item. While the reader is at
 * the bottom, new entries keep the newest item in view; once they scroll up, new entries leave
 * their position alone.
 */
@Composable
fun ConversationView(
    entries: List<ConvoEntry>,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
    contentPadding: PaddingValues = PaddingValues(),
    footer: (@Composable () -> Unit)? = null,
) {
    var expanded by remember { mutableStateOf(emptySet<String>()) }
    val toggle: (String) -> Unit = { id ->
        expanded = if (id in expanded) expanded - id else expanded + id
    }
    var followBottom by remember { mutableStateOf(true) }
    val lastIndex = entries.size + (if (footer == null) 0 else 1) - 1
    LaunchedEffect(listState) {
        var previousStart: Pair<Int, Int>? = null
        snapshotFlow { listState.layoutInfo }.collect { info ->
            val first = info.visibleItemsInfo.firstOrNull() ?: return@collect
            val start = first.index to first.offset
            val previous = previousStart
            previousStart = start
            if (previous != null && start != previous) followBottom = info.isAtEnd()
        }
    }
    LaunchedEffect(entries, lastIndex) {
        if (followBottom && lastIndex >= 0) listState.scrollToItem(lastIndex)
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 14.dp)
            .testTag("conversation-list"),
        state = listState,
        contentPadding = contentPadding,
    ) {
        items(entries.size) { index ->
            when (val entry = entries[index]) {
                is ConvoEntry.Time -> TimeLabel(entry.seconds)
                is ConvoEntry.Prompt -> PromptBlock(entry.text)
                is ConvoEntry.Reply -> ReplyText(entry.text)
                is ConvoEntry.Tool -> ToolRow(entry.call, expanded, toggle)
                is ConvoEntry.Group -> GroupRow(entry.calls, expanded, toggle)
            }
        }
        footer?.let { content -> item { content() } }
    }
}

@Composable
private fun TimeLabel(seconds: Long) {
    Text(
        text = Instant.ofEpochSecond(seconds).atZone(ZoneId.systemDefault()).format(TIME_OF_DAY),
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 10.dp, bottom = 2.dp),
        color = Mocha.Overlay0,
        fontSize = 11.sp,
        textAlign = TextAlign.Center,
    )
}

@Composable
private fun PromptBlock(text: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 10.dp, bottom = 12.dp)
            .height(IntrinsicSize.Min)
            .clip(RoundedCornerShape(topStart = 4.dp, topEnd = 12.dp, bottomEnd = 12.dp, bottomStart = 4.dp))
            .background(Mocha.Surface0),
    ) {
        Box(
            modifier = Modifier
                .width(3.dp)
                .fillMaxHeight()
                .background(Mocha.Mauve),
        )
        Column(
            modifier = Modifier.padding(start = 12.dp, end = 12.dp, top = 9.dp, bottom = 10.dp),
        ) {
            Text(
                text = "YOU",
                color = Mocha.Mauve,
                fontSize = 10.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 0.1.em,
            )
            Text(text = text, color = Mocha.Text, fontSize = 14.sp)
        }
    }
}

@Composable
private fun ReplyText(text: String) {
    Text(
        text = text,
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 12.dp),
        color = Mocha.Text,
        fontSize = 14.sp,
    )
}

@Composable
private fun ToolRow(call: ToolCall, expanded: Set<String>, onToggle: (String) -> Unit) {
    val isOpen = call.id in expanded
    Column(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
        Row(
            modifier = Modifier
                .testTag("tool-row:${call.id}")
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .background(Mocha.Mantle)
                .clickable { onToggle(call.id) }
                .padding(horizontal = 10.dp, vertical = 7.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Caret(isOpen)
            Text(
                text = call.name,
                color = Mocha.Blue,
                fontWeight = FontWeight.Bold,
                fontSize = 12.5.sp,
                maxLines = 1,
            )
            Text(
                text = call.input,
                modifier = Modifier.weight(1f),
                color = Mocha.Subtext0,
                fontSize = 12.5.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            MarkText(toolMark(call))
        }
        if (isOpen) ResultBox(call)
    }
}

@Composable
private fun GroupRow(calls: List<ToolCall>, expanded: Set<String>, onToggle: (String) -> Unit) {
    val firstId = calls.first().id
    val isOpen = firstId in expanded
    Column(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
        Row(
            modifier = Modifier
                .testTag("group-row:$firstId")
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .background(Mocha.Mantle)
                .clickable { onToggle(firstId) }
                .padding(horizontal = 10.dp, vertical = 7.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Caret(isOpen)
            Text(
                text = "${calls.size} tool calls",
                color = Mocha.Subtext1,
                fontWeight = FontWeight.Bold,
                fontSize = 12.5.sp,
                maxLines = 1,
            )
            Text(
                text = groupSummary(calls),
                modifier = Modifier.weight(1f),
                color = Mocha.Subtext0,
                fontSize = 12.5.sp,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            MarkText(groupMark(calls))
        }
        if (isOpen) {
            Row(
                modifier = Modifier
                    .height(IntrinsicSize.Min)
                    .padding(start = 10.dp, top = 2.dp),
            ) {
                Box(
                    modifier = Modifier
                        .width(1.dp)
                        .fillMaxHeight()
                        .background(Mocha.Surface0),
                )
                Column(modifier = Modifier.weight(1f).padding(start = 8.dp)) {
                    calls.forEach { call -> ToolRow(call, expanded, onToggle) }
                }
            }
        }
    }
}

@Composable
private fun Caret(isOpen: Boolean) {
    Text(
        text = "›",
        modifier = Modifier
            .width(9.dp)
            .graphicsLayer { rotationZ = if (isOpen) 90f else 0f },
        color = Mocha.Overlay0,
        fontSize = 12.5.sp,
        maxLines = 1,
    )
}

@Composable
private fun MarkText(mark: CallMark?) {
    mark?.let {
        Text(
            text = it.symbol,
            color = it.colour,
            fontSize = 11.5.sp,
            maxLines = 1,
        )
    }
}

/** The result, unwrapped and panning sideways, in a crust box below its row. */
@Composable
private fun ResultBox(call: ToolCall) {
    val scroll = rememberScrollState()
    Box(
        modifier = Modifier
            .testTag("tool-result:${call.id}")
            .fillMaxWidth()
            .padding(bottom = 6.dp)
            .clip(RoundedCornerShape(8.dp))
            .background(Mocha.Crust)
            .horizontalScroll(scroll)
            .padding(horizontal = 10.dp, vertical = 8.dp),
    ) {
        Text(
            text = call.result.orEmpty(),
            color = Mocha.Subtext1,
            fontSize = 11.5.sp,
            lineHeight = 17.sp,
            softWrap = false,
        )
    }
}

/** True when the last item's end, with the bottom padding, sits at the viewport's end. */
private fun LazyListLayoutInfo.isAtEnd(): Boolean {
    val last = visibleItemsInfo.lastOrNull() ?: return true
    return last.index == totalItemsCount - 1 &&
        last.offset + last.size <= viewportEndOffset - afterContentPadding
}

private fun toolMark(call: ToolCall): CallMark? = when {
    call.result == null -> null
    call.error -> CallMark.Failed
    else -> CallMark.Done
}

private fun groupMark(calls: List<ToolCall>): CallMark? = when {
    calls.any { it.error } -> CallMark.Failed
    calls.all { it.result != null } -> CallMark.Done
    else -> null
}
