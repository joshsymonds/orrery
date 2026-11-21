package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.scrollBy
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.WaitingItem
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

internal val CLOCK_TIME: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

// Entry keys always start with their kind ("reply:", "tool:", ...), so these never equal one.
private const val FOOTER_KEY = "footer"
// Waiting rows are keyed by their position in the queue, oldest first, so two equal items stay apart.
private const val WAITING_KEY_PREFIX = "waiting:"
private const val WAITING_MORE_KEY = "waiting-more"
// At most this many waiting rows draw; the rest are counted on one "+N more" row after them.
private const val WAITING_SHOWN = 3
// The new rule's label and the tint on the rows from it on, both in the lavender of the palette.
private const val NEW_LABEL = "new"
// How long an entry must stay on screen, enough of it, to count as read.
private const val READ_DWELL_MS = 1_000L

/**
 * The folded conversation as the phone draws it: each entry in its shape, a block someone speaks in, a line of
 * machinery, a rule between, or the session's Markdown reply, with an optional footer after the last entry. The
 * [waiting] rows follow the last entry, one per queued message up to three, then a "+N more" row for the rest, and the
 * footer comes last. Which lines and rules are open is kept by their key, so it survives new entries. The first
 * entries it shows open at the new rule when one is drawn, or at the end of the newest item otherwise. Landing leaves
 * following on only when the end is in view; otherwise the view waits for the reader to reach the end or an end
 * request before following. While the reader is at the bottom, new entries keep the end of the newest item in view;
 * once they scroll up, new entries leave their position alone. A change of the viewport's height or top padding (the
 * keyboard, a growing draft, the header folding) follows the same way. Each change of [endRequests] brings the end
 * into view and resumes following, even when the reader has scrolled up; the first composition is not a change. A
 * new rule sits before the first entry newer than [seen] (unix seconds, null for none), and every entry from there on
 * carries a faint tint; without a newer entry neither is drawn. [onRead] receives each new, larger time of the newest
 * entry read: one that stays on screen for a second, at least half of it (or of the window, for one taller than the
 * window) between the top and bottom content padding. A call that writes a contract, with a result that did not fail,
 * draws that contract's line (see [contractLineWords]) in place of its row, and a press on it calls [onOpenContract]
 * with the contract's id; [contracts] supplies the words.
 */
@Composable
fun ConversationView(
    entries: List<ConvoEntry>,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
    contentPadding: PaddingValues = PaddingValues(),
    footer: (@Composable () -> Unit)? = null,
    waiting: List<WaitingItem> = emptyList(),
    endRequests: Int = 0,
    seen: Long? = null,
    now: Long = System.currentTimeMillis() / 1000,
    onRead: ((Long) -> Unit)? = null,
    onFollowing: ((Boolean) -> Unit)? = null,
    contracts: List<ContractRow> = emptyList(),
    onOpenContract: (String) -> Unit = {},
) {
    var expanded by remember { mutableStateOf(emptySet<String>()) }
    val toggle: (String) -> Unit = { id ->
        expanded = if (id in expanded) expanded - id else expanded + id
    }
    var followBottom by remember { mutableStateOf(true) }
    // Whether the view follows the newest line: the reader's place, which a change of the padding or the content
    // never moves, unlike the list's momentary can-scroll-forward.
    LaunchedEffect(followBottom) { onFollowing?.invoke(followBottom) }
    var landed by remember { mutableStateOf(false) }
    val markdownCache = remember { MarkdownCache() }
    val keys = remember(entries) { entryKeys(entries) }
    val newerAt = remember(entries, seen) { firstNewerIndex(entries, seen) }
    val currentNewer by rememberUpdatedState(newerAt)
    val waitingRows = minOf(waiting.size, WAITING_SHOWN) + (if (waiting.size > WAITING_SHOWN) 1 else 0)
    val lastIndex = entries.size + waitingRows + (if (footer == null) 0 else 1) - 1
    // Following is decided from where the list is, whoever moved it: a reader's drag or fling, an
    // accessibility service's jump to a position, or code. A move is told by the first visible item's key
    // and offset, compared with the last seen, never by watching a scroll start and end: an instant jump
    // starts and ends within one step and would go unseen. Only the view's own follow scrolls, changes
    // of the content (a new reply, the window sliding past older entries) and changes of the top padding
    // (the head above growing or folding) move the list without the reader, so none is taken as leaving the end.
    val ownScroll = remember { OwnScroll() }
    val contentKeys by rememberUpdatedState(keys)
    LaunchedEffect(listState) {
        var seen: ListPlace? = null
        snapshotFlow { ListPlace(listState.anchor(), contentKeys, listState.layoutInfo.beforeContentPadding) }.collect { place ->
            val previous = seen
            seen = place
            // Where the view's own last scroll left the list counts as its own only until the list is seen
            // anywhere else, so a reader who scrolls back to the end there is still seen arriving.
            val ownPlace = place.anchor == ownScroll.leftAt
            if (!ownPlace) ownScroll.leftAt = null
            val readerMoved = previous != null && place.keys === previous.keys && place.padding == previous.padding &&
                place.anchor != previous.anchor &&
                ownScroll.running == 0 && !ownPlace
            if (readerMoved) followBottom = listState.layoutInfo.isAtEnd()
        }
    }
    LaunchedEffect(entries, lastIndex) {
        // The first entries land on the divider, when one is drawn; the view follows on only if the end
        // is then in view. Without a divider the view follows the end as before.
        if (!landed && entries.isNotEmpty()) {
            landed = true
            if (currentNewer >= 0) {
                ownScroll.around(listState) { listState.scrollToItem(currentNewer) }
                followBottom = listState.layoutInfo.isAtEnd()
                return@LaunchedEffect
            }
        }
        if (followBottom && lastIndex >= 0) ownScroll.around(listState) { listState.followEnd(lastIndex) }
    }
    val currentLast by rememberUpdatedState(lastIndex)
    LaunchedEffect(listState) {
        snapshotFlow { listState.layoutInfo.let { it.viewportSize.height to it.beforeContentPadding } }
            .collect { (height, _) ->
                if (height > 0 && followBottom && currentLast >= 0 && !listState.isScrollInProgress) {
                    ownScroll.around(listState) { listState.followEnd(currentLast) }
                }
            }
    }
    var handledEndRequests by remember { mutableIntStateOf(endRequests) }
    LaunchedEffect(endRequests) {
        if (endRequests != handledEndRequests) {
            handledEndRequests = endRequests
            followBottom = true
            if (currentLast >= 0) ownScroll.around(listState) { listState.followEnd(currentLast) }
        }
    }
    // Each entry's dwell is kept by its key, so an equal list re-delivered mid-count leaves it standing.
    val latestKeys by rememberUpdatedState(keys)
    val latestEntries by rememberUpdatedState(entries)
    val reportRead by rememberUpdatedState(onRead)
    var newestRead by remember { mutableLongStateOf(0L) }
    LaunchedEffect(listState) {
        val dwelling = HashMap<String, Job>()
        snapshotFlow { readingEntries(listState.layoutInfo, latestEntries, latestKeys) }
            .distinctUntilChanged()
            .collect { reading ->
                dwelling.keys.filter { it !in reading }.forEach { dwelling.remove(it)?.cancel() }
                reading.forEach { (key, time) ->
                    if (key !in dwelling) {
                        dwelling[key] = launch {
                            delay(READ_DWELL_MS)
                            if (time > newestRead) {
                                newestRead = time
                                reportRead?.invoke(time)
                            }
                        }
                    }
                }
            }
    }
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = OrreryTokens.Space.twelve + OrreryTokens.Space.two)
            .testTag("conversation-list"),
        state = listState,
        contentPadding = contentPadding,
    ) {
        items(count = entries.size, key = { index -> keys[index] }) { index ->
            // The new rule rides in its entry's item, so the item count, and the end that follows it, stay put.
            val tinted = newerAt >= 0 && index >= newerAt
            Column(modifier = Modifier.fillMaxWidth()) {
                if (index == newerAt) NewRule()
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("entry-row:$index")
                        .then(
                            if (tinted) {
                                Modifier
                                    .semantics { stateDescription = NEW_LABEL }
                                    .background(OrreryTokens.Role.newFill)
                            } else {
                                Modifier
                            },
                        ),
                ) {
                    EntryView(entries[index], keys[index], expanded, toggle, markdownCache, contracts, onOpenContract)
                }
            }
        }
        waiting.take(WAITING_SHOWN).forEachIndexed { index, waitingItem ->
            item(key = "$WAITING_KEY_PREFIX$index") { WaitingRow(waitingItem, expanded, toggle) }
        }
        if (waiting.size > WAITING_SHOWN) {
            item(key = WAITING_MORE_KEY) { WaitingMore(waiting.size - WAITING_SHOWN) }
        }
        footer?.let { content -> item(key = FOOTER_KEY) { content() } }
    }
}

/** The clock time a block or a rule shows for [seconds] in the device zone, or null for a time of 0, which is no time. */
private fun timeOf(seconds: Long): String? =
    if (seconds <= 0) null else Instant.ofEpochSecond(seconds).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)

/** The shape each entry draws as. */
@Composable
private fun EntryView(
    entry: ConvoEntry,
    key: String,
    expanded: Set<String>,
    onToggle: (String) -> Unit,
    cache: MarkdownCache,
    contracts: List<ContractRow>,
    onOpenContract: (String) -> Unit,
) {
    when (entry) {
        is ConvoEntry.Prompt -> YouBlock(entry.text, timeOf(entry.time))
        is ConvoEntry.Event -> EventBlock(entry.text, timeOf(entry.time))
        is ConvoEntry.Cron -> EventBlock(entry.text, timeOf(entry.time), cron = true)
        is ConvoEntry.StepEnd -> StepEndBlock(entry.agent, entry.end, entry.summary, entry.facts, timeOf(entry.time))
        is ConvoEntry.EpicEvent -> EpicEventBlock(
            kind = epicEventKindOf(entry.kind),
            slug = entry.epic.substringAfter('/'),
            sentence = entry.sentence,
            time = timeOf(entry.time),
        )
        is ConvoEntry.Created -> EpicEventBlock(
            kind = EpicEventKind.Created,
            slug = entry.epic.substringAfter('/'),
            sentence = "Created the epic from the accepted contract.",
            time = timeOf(entry.time),
        )
        is ConvoEntry.Move -> MoveLine(entry.event)
        is ConvoEntry.Brief -> EngineFold(brief = true, text = entry.text, key = key, expanded = expanded, onToggle = onToggle)
        is ConvoEntry.Engine -> EngineFold(brief = false, text = entry.text, key = key, expanded = expanded, onToggle = onToggle)
        is ConvoEntry.Check -> CheckBlock(entry.round, entry.text, time = timeOf(entry.time))
        is ConvoEntry.Reply -> ReplyText(entry.text, cache)
        is ConvoEntry.Tool -> ToolLine(entry.call, expanded = expanded, onToggle = onToggle)
        is ConvoEntry.Group -> ToolRun(entry.calls, expanded = expanded, onToggle = onToggle)
        is ConvoEntry.ContractLine -> ContractLineRow(entry.call.contract, contracts, onOpenContract)
        is ConvoEntry.Compaction -> CompactionRule(
            trigger = entry.trigger,
            tokens = entry.tokens,
            expanded = key in expanded,
            onToggle = { onToggle(key) },
            summary = { ReplyText(entry.text, cache) },
        )
        is ConvoEntry.Notice -> NoticeLine(entry)
        is ConvoEntry.Task -> TaskLine(entry.type, entry.subagentType, entry.description, entry.status)
        is ConvoEntry.Interrupted -> InterruptedRule(entry.time)
        is ConvoEntry.Day -> DayRule(entry.seconds)
    }
}

// The kind of epic event a question, park, release or end entry draws as; the fold gives no other kind.
private fun epicEventKindOf(kind: String): EpicEventKind = when (kind) {
    "question" -> EpicEventKind.Question
    "park" -> EpicEventKind.Park
    "release" -> EpicEventKind.Release
    else -> EpicEventKind.End
}

/**
 * A queued message in the prompt's look, dimmed, labelled "waiting" or, once cleared, "not delivered".
 * It draws as one line, cut with an ellipsis; a tap shows all of it, and a second tap folds it again.
 */
@Composable
private fun WaitingRow(item: WaitingItem, expanded: Set<String>, onToggle: (String) -> Unit) {
    // Open by the message's time and text, not its queue position, so it stays open as the queue ahead clears.
    val id = "$WAITING_KEY_PREFIX${item.time}:${item.text}"
    val isOpen = id in expanded
    // The tap is read here rather than by clickable, which would merge the message's text into the row's node.
    // The tap sits before the clip: a hit test against the clip's non-uniform outline misses in Robolectric.
    Row(
        modifier = Modifier
            .testTag("waiting-row")
            .fillMaxWidth()
            .padding(top = OrreryTokens.Space.eight + OrreryTokens.Space.two, bottom = OrreryTokens.Space.twelve)
            .height(IntrinsicSize.Min)
            .semantics { onClick(action = { onToggle(id); true }) }
            .pointerInput(id) { detectTapGestures(onTap = { onToggle(id) }) }
            .clip(RoundedCornerShape(topEnd = OrreryTokens.Radius.two, bottomEnd = OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.raised),
    ) {
        Box(
            modifier = Modifier
                .width(OrreryTokens.Size.rail)
                .fillMaxHeight()
                .background(OrreryTokens.Ink.three),
        )
        Column(
            modifier = Modifier.padding(
                start = OrreryTokens.Space.twelve,
                end = OrreryTokens.Space.twelve,
                top = OrreryTokens.Space.eight,
                bottom = OrreryTokens.Space.eight,
            ),
        ) {
            Text(
                text = (if (item.canceled) "not delivered" else "waiting").inLabelCase(),
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.label,
            )
            Text(
                text = item.text,
                color = OrreryTokens.Ink.three,
                style = OrreryTokens.Type.body,
                maxLines = if (isOpen) Int.MAX_VALUE else 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** The count of queued messages past the [WAITING_SHOWN] drawn, on one quiet line below them. */
@Composable
private fun WaitingMore(count: Int) {
    Text(
        text = "+$count more",
        modifier = Modifier
            .testTag("waiting-more")
            .fillMaxWidth()
            .padding(
                start = OrreryTokens.Space.sixteen,
                top = OrreryTokens.Space.eight + OrreryTokens.Space.two,
                bottom = OrreryTokens.Space.twelve,
            ),
        color = OrreryTokens.Ink.three,
        style = OrreryTokens.Type.small,
    )
}

/** The reply's markdown, rendered; the parse is kept in [cache] by the reply's text. */
@Composable
private fun ReplyText(text: String, cache: MarkdownCache) {
    val blocks = remember(text) { cache.blocks(text) }
    MarkdownBody(
        blocks = blocks,
        modifier = Modifier.testTag("reply").padding(vertical = OrreryTokens.Space.twelve),
        color = OrreryTokens.Ink.one,
        fontSize = OrreryTokens.Type.body.fontSize,
    )
}

/** The one line a contract's call draws: its title and state words, pressed to open the contract. */
@Composable
private fun ContractLineRow(id: String, contracts: List<ContractRow>, onOpen: (String) -> Unit) {
    val words = contractLineWords(id, contracts.firstOrNull { it.id == id })
    val labelColour = when (words.tone) {
        ContractLineTone.Attention -> OrreryTokens.Role.attention
        ContractLineTone.Ok -> OrreryTokens.Role.ok
        ContractLineTone.Quiet -> OrreryTokens.Ink.three
    }
    Row(
        modifier = Modifier
            .testTag("contract-line:$id")
            .fillMaxWidth()
            .padding(vertical = OrreryTokens.Space.four)
            .clip(RoundedCornerShape(OrreryTokens.Radius.two))
            .background(OrreryTokens.Surface.well)
            .clickable { onOpen(id) }
            .padding(horizontal = OrreryTokens.Space.eight + OrreryTokens.Space.two, vertical = OrreryTokens.Space.eight),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            text = "Contract",
            color = labelColour,
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
        )
        Text(
            text = buildAnnotatedString {
                append(words.title)
                withStyle(SpanStyle(color = OrreryTokens.Ink.three)) { append(words.rest) }
            },
            modifier = Modifier.weight(1f),
            color = OrreryTokens.Ink.one,
            style = OrreryTokens.Type.small,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The label a notice of [sort] shows before its text. */
fun noticeLabel(sort: String): String = when (sort) {
    "compaction" -> "context compacted"
    "interrupted" -> "interrupted"
    "task" -> "task"
    "teammate" -> "teammate"
    "" -> "notice"
    else -> sort
}

/**
 * The read-counting entries on screen, by key, with their times: those whose part between the top
 * and bottom content padding is at least half of the smaller of the row and the window. Rules
 * and untimed entries never count.
 */
private fun readingEntries(info: LazyListLayoutInfo, entries: List<ConvoEntry>, keys: List<String>): Map<String, Long> {
    val contentEnd = info.viewportEndOffset - info.afterContentPadding
    val window = contentEnd
    val reading = HashMap<String, Long>()
    for (item in info.visibleItemsInfo) {
        if (item.index >= entries.size) continue
        val entry = entries[item.index]
        val time = entryTime(entry)
        if (time <= 0L) continue
        val shown = minOf(item.offset + item.size, contentEnd) - maxOf(item.offset, 0)
        if (shown > 0 && shown * 2 >= minOf(item.size, window)) reading[keys[item.index]] = time
    }
    return reading
}

/** Where the list is: its first visible item's key and how far that item is scrolled, or null before a layout. */
private fun LazyListState.anchor(): Pair<Any, Int>? =
    layoutInfo.visibleItemsInfo.firstOrNull()?.let { it.key to firstVisibleItemScrollOffset }

/** The list's place and the content it was laid out from, as the follow decision compares them. */
private data class ListPlace(val anchor: Pair<Any, Int>?, val keys: Any, val padding: Int)

/**
 * The view's own scrolls: how many are running, and where the last one left the list. The follow decision
 * skips moves they make, so following the end never reads as the reader leaving it.
 */
private class OwnScroll {
    var running = 0
    var leftAt: Pair<Any, Int>? = null

    suspend fun around(state: LazyListState, scroll: suspend () -> Unit) {
        running++
        try {
            scroll()
        } finally {
            running--
            leftAt = state.anchor()
        }
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
