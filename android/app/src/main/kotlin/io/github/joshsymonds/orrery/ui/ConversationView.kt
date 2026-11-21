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
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.TextUnit
import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.WaitingItem
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
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
 * with the contract's id; [contracts] supplies the words. A press on an epic's slug calls [onOpenEpic] with the epic's id.
 * With [find] set, every match of the find's query is marked in the text it draws, the current one solid. The entry
 * that holds the current match opens its fold or call, the list scrolls to that entry, and the text that holds the
 * match brings its run into view. Closing the find (a null
 * [find]) returns the list to the place it was at when the find opened. A check round the engine sent draws its round's
 * chips from the step's [checks].
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
    onRead: ((Long) -> Unit)? = null,
    onFollowing: ((Boolean) -> Unit)? = null,
    contracts: List<ContractRow> = emptyList(),
    onOpenContract: (String) -> Unit = {},
    onOpenEpic: (String) -> Unit = {},
    epicQuestion: @Composable (epic: String, question: String) -> Unit = { _, _ -> },
    find: TranscriptFind? = null,
    checks: List<StepCheckRow> = emptyList(),
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
    // The place the list was at when the find opened, kept until the find closes and the list returns to it.
    val findOpen = find != null
    var findReturn by remember { mutableStateOf<Pair<Int, Int>?>(null) }
    LaunchedEffect(findOpen) {
        if (findOpen) {
            findReturn = listState.firstVisibleItemIndex to listState.firstVisibleItemScrollOffset
        } else {
            findReturn?.let { (index, offset) ->
                findReturn = null
                ownScroll.around(listState) { listState.scrollToItem(index, offset) }
                followBottom = listState.layoutInfo.isAtEnd()
            }
        }
    }
    val marks = remember(find) { marksByEntry(find) }
    val currentMatch = find?.matches?.getOrNull(find.current)
    // One reveal for each step of the find, so a step that lands on the same match still brings it into view.
    val reveal = remember(find?.step) { find?.let { FindReveal() } }
    LaunchedEffect(currentMatch, find?.step) {
        val match = currentMatch ?: return@LaunchedEffect
        val entry = entries.getOrNull(match.entry) ?: return@LaunchedEffect
        expanded = expanded + foldKeysHolding(entry, keys[match.entry], match.range.first)
        ownScroll.around(listState) { listState.scrollToItem(match.entry) }
        reveal?.scrolled = true
        followBottom = listState.layoutInfo.isAtEnd()
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
    CompositionLocalProvider(LocalFindReveal provides reveal) {
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
                        EntryView(
                            entries[index],
                            keys[index],
                            expanded,
                            toggle,
                            markdownCache,
                            contracts,
                            onOpenContract,
                            onOpenEpic,
                            epicQuestion,
                            marks[index],
                            checks,
                        )
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
    onOpenEpic: (String) -> Unit,
    epicQuestion: @Composable (epic: String, question: String) -> Unit,
    marks: EntryMarks?,
    checks: List<StepCheckRow>,
) {
    when (entry) {
        is ConvoEntry.Prompt -> YouBlock(entry.text, timeOf(entry.time), marks = marks, phone = entry.phone)
        is ConvoEntry.Sending -> YouBlock(entry.text, sending = true)
        is ConvoEntry.Event -> EventBlock(entry.text, timeOf(entry.time), marks = marks)
        is ConvoEntry.Cron -> EventBlock(entry.text, timeOf(entry.time), cron = true, marks = marks)
        is ConvoEntry.StepEnd -> StepEndBlock(
            who = entry.who,
            agent = entry.agent,
            end = entry.end,
            summary = entry.summary,
            facts = entry.facts,
            time = timeOf(entry.time),
            marks = marks,
        )
        is ConvoEntry.EpicEvent -> EpicEventBlock(
            kind = epicEventKindOf(entry.kind),
            slug = entry.epic.substringAfter('/'),
            sentence = entry.sentence,
            time = timeOf(entry.time),
            onOpen = { onOpenEpic(entry.epic) },
            under = { if (entry.kind == "question") epicQuestion(entry.epic, entry.tail) },
            marks = marks,
        )
        is ConvoEntry.Created -> EpicEventBlock(
            kind = EpicEventKind.Created,
            slug = entry.epic.substringAfter('/'),
            sentence = "Created the epic from the accepted contract.",
            time = timeOf(entry.time),
            onOpen = { onOpenEpic(entry.epic) },
        )
        is ConvoEntry.Move -> MoveLine(entry.event)
        is ConvoEntry.Brief ->
            EngineFold(brief = true, text = entry.text, key = key, expanded = expanded, onToggle = onToggle, marks = marks)
        is ConvoEntry.Engine ->
            EngineFold(brief = false, text = entry.text, key = key, expanded = expanded, onToggle = onToggle, marks = marks)
        is ConvoEntry.Check -> CheckBlock(
            entry.round,
            entry.text,
            checks = roundChecks(checks, entry.round),
            time = timeOf(entry.time),
            marks = marks,
        )
        is ConvoEntry.Reply -> ReplyText(entry.text, cache, marks = marks)
        is ConvoEntry.LiveReply -> ReplyText(entry.text, cache, live = true, marks = marks)
        is ConvoEntry.Thinking -> ThinkingLine(entry.text, key, expanded, onToggle)
        is ConvoEntry.Tool -> ToolLine(entry.call, expanded = expanded, onToggle = onToggle, marks = marks)
        is ConvoEntry.Group -> ToolRun(entry.calls, expanded = expanded, onToggle = onToggle, marks = marks)
        is ConvoEntry.ContractLine -> ContractLineRow(entry.call.contract, contracts, onOpenContract)
        is ConvoEntry.Compaction -> CompactionRule(
            trigger = entry.trigger,
            tokens = entry.tokens,
            expanded = key in expanded,
            onToggle = { onToggle(key) },
            summary = { ReplyText(entry.text, cache, marks = marks) },
        )
        is ConvoEntry.Notice -> NoticeLine(entry, marks = marks)
        is ConvoEntry.Task -> TaskLine(entry.type, entry.subagentType, entry.description, entry.status, marks = marks)
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

/**
 * The reply's markdown, rendered; the parse is kept in [cache] by the reply's text. A [live] reply, one still being
 * written, ends in a caret.
 */
@Composable
private fun ReplyText(text: String, cache: MarkdownCache, live: Boolean = false, marks: EntryMarks? = null) {
    val blocks = remember(text, marks) { markedBlocks(cache.blocks(text), marks) }
    Column(modifier = Modifier.testTag("reply").padding(vertical = OrreryTokens.Space.twelve)) {
        MarkdownBody(
            blocks = blocks,
            color = OrreryTokens.Ink.one,
            fontSize = OrreryTokens.Type.body.fontSize,
        )
        if (live) {
            Box(
                modifier = Modifier
                    .testTag("caret")
                    .size(width = OrreryTokens.Size.hairline * 2, height = OrreryTokens.Size.icon)
                    .background(OrreryTokens.Ink.three),
            )
        }
    }
}

/**
 * The Ask of an epic's open question [question] in [detail]: it draws with no call of its own, and its confirm answers
 * the epic through [client]. Once the question is no longer open it draws the settled line, with the epic's project and
 * slug. Draws nothing while the epic is unread.
 */
@Composable
internal fun EpicQuestion(detail: EpicDetail?, question: String, now: Long, client: OrreryClient) {
    if (detail == null) return
    val open = detail.questions.firstOrNull { it.id == question }
    if (open == null) {
        AskSettledLine("${epicProject(detail.epic)} · ${detail.epic.slug}")
        return
    }
    Ask(
        model = epicAsk(detail.epic, open),
        now = now,
        phase = AskPhase.OPEN,
        picked = null,
        note = "",
        onAnswer = null,
        onNotNow = null,
        onBringBack = null,
        onDecline = null,
        onArchive = null,
        onConfirm = { choice, text -> answerQuestion(client, detail.epic.id, open.id, choice, text) },
    )
}

/**
 * The epic details a screen's questions draw from: [held], the head's, which hold only the epics the conversation steers,
 * and each epic a question names that [held] lacks. Each such epic is read once through [client] and kept for the screen,
 * so a settled question's epic is not read again.
 */
@Composable
internal fun questionEpicDetails(
    entries: List<ConvoEntry>,
    held: Map<String, EpicDetail>,
    client: OrreryClient,
): Map<String, EpicDetail> {
    val asked = remember(entries) {
        entries.filterIsInstance<ConvoEntry.EpicEvent>().filter { it.kind == "question" }.map { it.epic }.distinct()
    }
    val requested = remember { mutableSetOf<String>() }
    var read by remember { mutableStateOf(emptyMap<String, EpicDetail>()) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(asked, held) {
        for (epic in asked) {
            if (epic in held || !requested.add(epic)) continue
            // The read runs in the screen's scope, so a restart of this effect does not cancel it.
            scope.launch {
                val detail = withContext(Dispatchers.IO) {
                    try {
                        client.epic(epic)
                    } catch (e: IOException) {
                        null
                    }
                }
                detail?.let { read = read + (epic to it) }
            }
        }
    }
    return held + read
}

// answerQuestion records the owner's answer on the epic, and returns the words a refusal shows, or "" once it landed.
private suspend fun answerQuestion(
    client: OrreryClient,
    epicId: String,
    questionId: String,
    choice: String,
    text: String,
): String = when (val result = withContext(Dispatchers.IO) { client.answerEpic(epicId, questionId, text, choice) }) {
    StepCallResult.Landed -> ""
    is StepCallResult.Refused -> result.message
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

/**
 * The find bar while it is open: the [query] typed in it, the index of the current match among the matches, and the
 * [step] of the find, which each move of the find bumps, so a move to the same match still scrolls to it.
 */
data class FindSession(val query: String, val current: Int = 0, val step: Int = 0)

/** An open find as a transcript draws it: every match in entry order, the index of the [current] one (-1 for none), and the [step]. */
data class TranscriptFind(val matches: List<FindMatch>, val current: Int, val step: Int)

/**
 * One step of a find, carried to the drawn text that holds the current match. The view sets [scrolled] once it has
 * scrolled to the match's entry; the first such text laid out brings its run into view and sets [revealed].
 */
internal class FindReveal {
    var scrolled by mutableStateOf(false)
    var revealed = false
}

internal val LocalFindReveal = staticCompositionLocalOf<FindReveal?> { null }

// The run of a marked text that its find's current match covers, tagged so a text can bring it into view.
private const val FIND_CURRENT_TAG = "find-current"

/** The matches of [query] across [entries]' searched text, in entry order. */
fun transcriptMatches(entries: List<ConvoEntry>, query: String, cache: MarkdownCache): List<FindMatch> =
    findMatches(entries.map { entry -> findParts(entry, cache).joinToString("\n") }, query)

/**
 * The parts of [entry] that find searches, in the order they draw: the drawn text of a prompt, an event, a cron prompt, a
 * send-back's note and rest, a step's summary, an epic event's sentence, a reply, a live reply and a compaction's summary,
 * each as Markdown draws it; an engine or brief fold's text; a notice's label and text; a task's name and description;
 * and each call's input and result. The parts of an entry join with a line break, so a match never runs across two of them.
 */
internal fun findParts(entry: ConvoEntry, cache: MarkdownCache): List<String> = when (entry) {
    is ConvoEntry.Prompt -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.Event -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.Cron -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.Check ->
        markdownLeaves(cache.blocks(checkNote(entry.text))) + markdownLeaves(cache.blocks(checkRest(entry.text)))
    is ConvoEntry.StepEnd -> markdownLeaves(cache.blocks(entry.summary))
    is ConvoEntry.EpicEvent -> markdownLeaves(cache.blocks(entry.sentence))
    is ConvoEntry.Reply -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.LiveReply -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.Compaction -> markdownLeaves(cache.blocks(entry.text))
    is ConvoEntry.Engine -> listOf(entry.text)
    is ConvoEntry.Brief -> listOf(entry.text)
    is ConvoEntry.Notice -> listOf(noticeLabel(entry.sort).inLabelCase(), entry.text)
    is ConvoEntry.Task -> listOf(taskNameOf(entry.type, entry.subagentType), entry.description)
    is ConvoEntry.Tool -> listOfNotNull(entry.call.input, entry.call.result)
    is ConvoEntry.Group -> entry.calls.flatMap { call -> listOfNotNull(call.input, call.result) }
    else -> emptyList()
}

/** The characters the texts a Markdown reader draws take in a searched text, each followed by a line break. */
internal fun searchedSpan(text: String): Int = markdownLeaves(parseMarkdown(text)).sumOf { it.length + 1 }

/** The texts a Markdown reader draws, in draw order. */
internal fun markdownLeaves(blocks: List<MdBlock>): List<String> {
    val leaves = mutableListOf<String>()
    mapLeaves(blocks) { leaf ->
        leaves.add(leaf.text)
        leaf
    }
    return leaves
}

/** [blocks] with each text a reader draws passed through [leaf], in draw order, a code block's text included. */
private fun mapLeaves(blocks: List<MdBlock>, leaf: (AnnotatedString) -> AnnotatedString): List<MdBlock> = blocks.map { block ->
    when (block) {
        is MdBlock.Paragraph -> block.copy(text = leaf(block.text))
        is MdBlock.Heading -> block.copy(text = leaf(block.text))
        is MdBlock.Item -> block.copy(text = leaf(block.text))
        is MdBlock.Code -> block.copy(drawn = leaf(block.drawn))
        is MdBlock.Table -> block.copy(rows = block.rows.map { row -> row.map { cell -> leaf(cell) } })
        is MdBlock.Quote -> block.copy(blocks = mapLeaves(block.blocks, leaf))
        is MdBlock.Rule -> block
    }
}

/**
 * [blocks] with the find's marks drawn into each text, the texts counted in the order [markdownLeaves] reads them, the
 * first from [base] in the entry's searched text.
 */
internal fun markedBlocks(blocks: List<MdBlock>, marks: EntryMarks?, base: Int = 0): List<MdBlock> {
    if (marks == null) return blocks
    var at = base
    return mapLeaves(blocks) { leaf ->
        markedText(leaf, at, marks).also { at += leaf.length + 1 }
    }
}

/** The matches one entry marks: those of the find that fall in its searched text, and the current match when it is one. */
class EntryMarks(val matches: List<FindMatch>, val current: FindMatch?)

/** Each entry's marks for [find], by the entry's index. */
private fun marksByEntry(find: TranscriptFind?): Map<Int, EntryMarks> {
    if (find == null) return emptyMap()
    val current = find.matches.getOrNull(find.current)
    return find.matches.groupBy { it.entry }.mapValues { (_, matches) -> EntryMarks(matches, current) }
}

/**
 * [source], drawn from [base] in its entry's searched text, with the entry's matches marked as Find.kt marks them: each in
 * the match tint, the current one solid. The current one's run is tagged for [FindText]. The source's links and styles stay.
 */
internal fun markedText(source: AnnotatedString, base: Int, marks: EntryMarks?): AnnotatedString {
    if (marks == null) return source
    val end = base + source.length
    val inside = marks.matches.filter { it.range.first >= base && it.range.last < end }
    if (inside.isEmpty()) return source
    val local = inside.map { FindMatch(0, (it.range.first - base)..(it.range.last - base)) }
    val current = marks.current?.let { inside.indexOf(it) } ?: -1
    val highlight = findHighlight(source.text, 0, local, current)
    return buildAnnotatedString {
        append(source)
        highlight.spanStyles.forEach { addStyle(it.item, it.start, it.end) }
        if (current >= 0) addStringAnnotation(FIND_CURRENT_TAG, "", local[current].range.first, local[current].range.last + 1)
    }
}

/**
 * A [Text] of a transcript that a find marks. The text that holds the find's current match brings that match's run into
 * view, once its entry is scrolled to: the first such text laid out does it, once for each step of the find.
 */
@Composable
internal fun FindText(
    text: AnnotatedString,
    modifier: Modifier = Modifier,
    color: Color = Color.Unspecified,
    fontSize: TextUnit = TextUnit.Unspecified,
    fontWeight: FontWeight? = null,
    style: TextStyle = LocalTextStyle.current,
    softWrap: Boolean = true,
    maxLines: Int = Int.MAX_VALUE,
    overflow: TextOverflow = TextOverflow.Clip,
) {
    val reveal = LocalFindReveal.current
    val run = text.getStringAnnotations(FIND_CURRENT_TAG, 0, text.length).firstOrNull()
    val holds = reveal != null && run != null
    val scrolled = reveal?.scrolled == true
    val requester = remember { BringIntoViewRequester() }
    var layout by remember { mutableStateOf<TextLayoutResult?>(null) }
    LaunchedEffect(holds, scrolled, layout) {
        val drawn = layout ?: return@LaunchedEffect
        if (reveal == null || run == null || !scrolled || reveal.revealed) return@LaunchedEffect
        reveal.revealed = true
        requester.bringIntoView(runRect(drawn, run.start, run.end))
    }
    Text(
        text = text,
        modifier = if (holds) modifier.bringIntoViewRequester(requester) else modifier,
        color = color,
        fontSize = fontSize,
        fontWeight = fontWeight,
        style = style,
        softWrap = softWrap,
        maxLines = maxLines,
        overflow = overflow,
        onTextLayout = { if (holds) layout = it },
    )
}

/** The box that the characters from [start] to [end] (exclusive) of [layout] cover, over every line they reach. */
private fun runRect(layout: TextLayoutResult, start: Int, end: Int): Rect {
    val first = layout.getBoundingBox(start)
    val last = layout.getBoundingBox(end - 1)
    return Rect(
        left = minOf(first.left, last.left),
        top = minOf(first.top, last.top),
        right = maxOf(first.right, last.right),
        bottom = maxOf(first.bottom, last.bottom),
    )
}

/** Whether [marks] hold a match that starts within [length] characters from [base]: text that holds one draws whole. */
internal fun holdsMatch(marks: EntryMarks?, base: Int, length: Int): Boolean =
    marks?.matches?.any { it.range.first >= base && it.range.first < base + length } == true

/** The length of a call's searched text: its input, then its result after a line break when it has one. */
internal fun callLength(call: ToolCall): Int = call.input.length + (call.result?.let { it.length + 1 } ?: 0)

/** Where each call of [calls] starts in its run's searched text, the calls joined by a line break. */
internal fun callBases(calls: List<ToolCall>): List<Int> {
    var base = 0
    return calls.map { call -> base.also { base += callLength(call) + 1 } }
}

/**
 * The keys of the folds that open for the match at [at] in [entry]'s searched text: an engine, brief or compaction fold by
 * its [key], a call by its id, and a run by its first call's id with each call that holds the match. A send-back's rest
 * opens on its own, from its drawing, when the match is in it.
 */
private fun foldKeysHolding(entry: ConvoEntry, key: String, at: Int): Set<String> = when (entry) {
    is ConvoEntry.Engine, is ConvoEntry.Brief, is ConvoEntry.Compaction -> setOf(key)
    is ConvoEntry.Tool -> setOf(entry.call.id)
    is ConvoEntry.Group -> {
        val bases = callBases(entry.calls)
        val holding = entry.calls.filterIndexed { index, call -> at >= bases[index] && at < bases[index] + callLength(call) }
        setOf(entry.calls.first().id) + holding.map { it.id }
    }
    else -> emptySet()
}
