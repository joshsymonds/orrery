package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.scrollBy
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
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListLayoutInfo
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
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
import io.github.joshsymonds.orrery.api.WaitingItem
import io.github.joshsymonds.orrery.ui.theme.Mocha
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val TIME_OF_DAY: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

// Entry keys always start with their kind ("reply:", "tool:", ...), so these never equal one.
private const val FOOTER_KEY = "footer"
private const val WORKING_KEY = "working"
// Waiting rows are keyed by their position in the queue, oldest first, so two equal items stay apart.
private const val WAITING_KEY_PREFIX = "waiting:"
private const val WAITING_ALPHA = 0.6f

private enum class CallMark(val symbol: String, val colour: Color) {
    Done("✓", Mocha.Green),
    Failed("✗", Mocha.Red),
}

/**
 * The folded conversation as the phone draws it: time labels, prompt blocks, markdown replies, tool
 * rows that expand to their result, groups of calls that expand to their rows, and slim notice
 * rows, with an optional footer after the last entry. While [working], a working mark sits after
 * the last entry; the [waiting] rows follow it, one per queued message, and the footer comes last.
 * Which rows are open is kept by tool id (a group by its first call's id), so it survives new
 * entries. It opens at the end of the newest item. While the reader is at the bottom, new entries
 * keep the end of the newest item in view; once they scroll up, new entries leave their position
 * alone. A change of the viewport's height or top padding (the keyboard, a growing draft, the
 * header folding) follows the same way. Each change of [endRequests] brings the end into view and
 * resumes following, even when the reader has scrolled up; the first composition is not a change.
 */
@Composable
fun ConversationView(
    entries: List<ConvoEntry>,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
    contentPadding: PaddingValues = PaddingValues(),
    footer: (@Composable () -> Unit)? = null,
    working: Boolean = false,
    waiting: List<WaitingItem> = emptyList(),
    endRequests: Int = 0,
) {
    var expanded by remember { mutableStateOf(emptySet<String>()) }
    val toggle: (String) -> Unit = { id ->
        expanded = if (id in expanded) expanded - id else expanded + id
    }
    var followBottom by remember { mutableStateOf(true) }
    val markdownCache = remember { MarkdownCache() }
    val keys = remember(entries) { entryKeys(entries) }
    val lastIndex = entries.size + (if (working) 1 else 0) + waiting.size + (if (footer == null) 0 else 1) - 1
    // Following is decided when a scroll ends, not when the first visible item moves: a slid window
    // moves it without the reader scrolling.
    LaunchedEffect(listState) {
        var wasScrolling = false
        snapshotFlow { listState.isScrollInProgress }.collect { scrolling ->
            if (wasScrolling && !scrolling) followBottom = listState.layoutInfo.isAtEnd()
            wasScrolling = scrolling
        }
    }
    LaunchedEffect(entries, lastIndex) {
        if (followBottom && lastIndex >= 0) listState.followEnd(lastIndex)
    }
    val currentLast by rememberUpdatedState(lastIndex)
    LaunchedEffect(listState) {
        snapshotFlow { listState.layoutInfo.let { it.viewportSize.height to it.beforeContentPadding } }
            .collect { (height, _) ->
                if (height > 0 && followBottom && currentLast >= 0 && !listState.isScrollInProgress) {
                    listState.followEnd(currentLast)
                }
            }
    }
    var handledEndRequests by remember { mutableIntStateOf(endRequests) }
    LaunchedEffect(endRequests) {
        if (endRequests != handledEndRequests) {
            handledEndRequests = endRequests
            followBottom = true
            if (currentLast >= 0) listState.followEnd(currentLast)
        }
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 14.dp)
            .testTag("conversation-list"),
        state = listState,
        contentPadding = contentPadding,
    ) {
        items(count = entries.size, key = { index -> keys[index] }) { index ->
            when (val entry = entries[index]) {
                is ConvoEntry.Time -> TimeLabel(entry.seconds)
                is ConvoEntry.Prompt -> PromptBlock(entry.text)
                is ConvoEntry.Reply -> ReplyText(entry.text, markdownCache)
                is ConvoEntry.Tool -> ToolRow(entry.call, expanded, toggle)
                is ConvoEntry.Group -> GroupRow(entry.calls, expanded, toggle)
                is ConvoEntry.Notice -> NoticeRow(entry)
            }
        }
        if (working) item(key = WORKING_KEY) { WorkingMark() }
        waiting.forEachIndexed { index, waitingItem ->
            item(key = "$WAITING_KEY_PREFIX$index") { WaitingRow(waitingItem) }
        }
        footer?.let { content -> item(key = FOOTER_KEY) { content() } }
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

/** A queued message in the prompt's look, dimmed, labelled "waiting" or, once cleared, "not delivered". */
@Composable
private fun WaitingRow(item: WaitingItem) {
    Row(
        modifier = Modifier
            .testTag("waiting-row")
            .fillMaxWidth()
            .padding(top = 10.dp, bottom = 12.dp)
            .height(IntrinsicSize.Min)
            .clip(RoundedCornerShape(topStart = 4.dp, topEnd = 12.dp, bottomEnd = 12.dp, bottomStart = 4.dp))
            .background(Mocha.Surface0.copy(alpha = WAITING_ALPHA)),
    ) {
        Box(
            modifier = Modifier
                .width(3.dp)
                .fillMaxHeight()
                .background(Mocha.Mauve.copy(alpha = WAITING_ALPHA)),
        )
        Column(
            modifier = Modifier.padding(start = 12.dp, end = 12.dp, top = 9.dp, bottom = 10.dp),
        ) {
            Text(
                text = if (item.canceled) "not delivered" else "waiting",
                color = Mocha.Overlay1,
                fontSize = 10.sp,
                fontWeight = FontWeight.Bold,
                letterSpacing = 0.1.em,
            )
            Text(text = item.text, color = Mocha.Subtext0, fontSize = 14.sp)
        }
    }
}

/** The reply's markdown, rendered; the parse is kept in [cache] by the reply's text. */
@Composable
private fun ReplyText(text: String, cache: MarkdownCache) {
    val blocks = remember(text) { cache.blocks(text) }
    MarkdownBody(
        blocks = blocks,
        modifier = Modifier.padding(vertical = 12.dp),
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

/**
 * A session event on one quiet line: a small dot and its sort's label, then its text, cut with an
 * ellipsis (a teammate notice keeps a few of its summary lines). A failed task is red and marked
 * with a cross. The text is drawn as it comes, never as markdown.
 */
@Composable
private fun NoticeRow(notice: ConvoEntry.Notice) {
    val accent = if (notice.error) Mocha.Red else Mocha.Overlay1
    Row(
        modifier = Modifier
            .testTag("notice:${notice.sort}")
            .fillMaxWidth()
            .padding(start = 4.dp, end = 4.dp, top = 5.dp, bottom = 5.dp),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(7.dp),
    ) {
        NoticeDot(accent)
        Text(
            text = noticeLabel(notice.sort),
            color = accent,
            fontWeight = FontWeight.Bold,
            fontSize = NOTICE_TEXT_SIZE,
            lineHeight = NOTICE_LINE_HEIGHT,
            maxLines = 1,
        )
        Text(
            text = notice.text,
            modifier = Modifier.weight(1f),
            color = if (notice.error) Mocha.Red.copy(alpha = NOTICE_ERROR_TEXT_ALPHA) else Mocha.Overlay1,
            fontSize = NOTICE_TEXT_SIZE,
            lineHeight = NOTICE_LINE_HEIGHT,
            maxLines = noticeMaxLines(notice.sort),
            overflow = TextOverflow.Ellipsis,
        )
        if (notice.error) MarkText(CallMark.Failed)
    }
}

/** The mark that Claude is still working: a quiet notice-style line after the last entry. */
@Composable
private fun WorkingMark() {
    Row(
        modifier = Modifier
            .testTag("working-mark")
            .fillMaxWidth()
            .padding(start = 4.dp, end = 4.dp, top = 5.dp, bottom = 5.dp),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(7.dp),
    ) {
        NoticeDot(Mocha.Overlay1)
        Text(
            text = "Claude is still working",
            color = Mocha.Overlay1,
            fontSize = NOTICE_TEXT_SIZE,
            lineHeight = NOTICE_LINE_HEIGHT,
            maxLines = 1,
        )
    }
}

@Composable
private fun NoticeDot(accent: Color) {
    Box(
        modifier = Modifier
            .padding(top = NOTICE_DOT_TOP)
            .size(NOTICE_DOT)
            .clip(CircleShape)
            .background(accent.copy(alpha = NOTICE_DOT_ALPHA)),
    )
}

private val NOTICE_DOT = 5.dp
private val NOTICE_DOT_TOP = 6.dp
private const val NOTICE_DOT_ALPHA = 0.7f
private const val NOTICE_ERROR_TEXT_ALPHA = 0.8f
private val NOTICE_TEXT_SIZE = 12.sp
private val NOTICE_LINE_HEIGHT = 17.sp

/** The label a notice of [sort] shows before its text. */
fun noticeLabel(sort: String): String = when (sort) {
    "compaction" -> "context compacted"
    "interrupted" -> "interrupted"
    "task" -> "task"
    "teammate" -> "teammate"
    "" -> "notice"
    else -> sort
}

/** How many lines of text a notice of [sort] shows before the ellipsis. */
fun noticeMaxLines(sort: String): Int = if (sort == "teammate") 3 else 2

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

/** Brings the end of the item at [lastIndex] to the viewport's end. */
private suspend fun LazyListState.followEnd(lastIndex: Int) {
    scrollToItem(lastIndex)
    scrollToEnd()
}

/** Scrolls the rest of the way so the last item's end sits at the viewport's end. */
private suspend fun LazyListState.scrollToEnd() {
    val info = layoutInfo
    val last = info.visibleItemsInfo.lastOrNull() ?: return
    val overhang = last.offset + last.size - (info.viewportEndOffset - info.afterContentPadding)
    if (overhang > 0) scrollBy(overhang.toFloat())
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
