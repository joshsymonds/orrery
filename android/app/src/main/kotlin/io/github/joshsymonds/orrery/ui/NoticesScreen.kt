package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.github.joshsymonds.orrery.api.NoticeRow
import io.github.joshsymonds.orrery.api.OrreryClient
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.time.Instant
import java.time.ZoneId

/** How long the notice history waits between reads while the screen is started, in milliseconds. */
private const val LIST_EVERY_MILLIS = 60_000L

/** The view boxes the bell and clock icons are drawn in, and their stroke, in those units. */
private const val ICON_VIEW = 16f
private const val ICON_STROKE = 1.5f

private const val UNREAD_WORDS = "unread"
private const val BELL_WORDS = "notice"
private const val CLOCK_WORDS = "limit"
private const val LOADING_WORDS = "loading…"
private const val FAILED_WORDS = "The notices could not be read."
private const val EMPTY_WORDS = "No notices."
private const val EMPTY_DETAIL_WORDS = "Questions, endings and failures from sessions and epics you are not looking at collect here."

/**
 * The notice history as the phone holds it: [rows], newest first, null until a read has answered; [failed] when the last
 * read did not answer, which keeps the rows of the last one that did. [unread] counts the rows not yet read.
 */
internal class NoticeRowsState(private val client: OrreryClient) {
    var rows: List<NoticeRow>? by mutableStateOf(null)
        private set

    var failed: Boolean by mutableStateOf(false)
        private set

    val unread: Int get() = rows.orEmpty().count { !it.read }

    /** Reads the history again off the main thread. */
    suspend fun relist() {
        try {
            rows = withContext(Dispatchers.IO) { client.notices() }
            failed = false
        } catch (error: IOException) {
            failed = true
        }
    }
}

/**
 * The notice history of [client], read when the composition's lifecycle starts and every [LIST_EVERY_MILLIS] while it is
 * started. Nothing is read while it is stopped.
 */
@Composable
internal fun rememberNoticeRows(client: OrreryClient): NoticeRowsState {
    val state = remember(client) { NoticeRowsState(client) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(state, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                state.relist()
                delay(LIST_EVERY_MILLIS)
            }
        }
    }
    return state
}

/**
 * The notice history by day: the head with Back, the title and Mark all read while any row is unread; then each day's
 * head and its rows, newest first, each led and worded as its notice says. A tap sends Seen for the row and hands it to
 * [onOpen], except a limit or an alert, which opens nothing. A read follows each Seen and ReadAll. [now] is in unix
 * seconds.
 */
@Composable
fun NoticesScreen(
    client: OrreryClient,
    onBack: () -> Unit,
    onOpen: (NoticeRow) -> Unit,
    modifier: Modifier = Modifier,
    now: () -> Long = { System.currentTimeMillis() / 1000 },
    zone: ZoneId = ZoneId.systemDefault(),
) {
    val notices = rememberNoticeRows(client)
    // The tree the leads come from, and whether its read has finished: a read that did not answer leads with what it can.
    var tree by remember(client) { mutableStateOf<TreeData?>(null) }
    var treeRead by remember(client) { mutableStateOf(false) }
    LaunchedEffect(client) {
        tree = readTree(client, held = null)
        treeRead = true
    }
    val scope = rememberCoroutineScope()
    val rows = notices.rows
    Column(modifier = modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = onBack, modifier = Modifier.testTag("notices-back")) {
                Text("Back")
            }
            Text("Notices", modifier = Modifier.weight(1f), style = OrreryTokens.Type.title)
            if (notices.unread > 0) {
                TextButton(
                    onClick = {
                        scope.launch {
                            answered { client.readAllNotices() }
                            notices.relist()
                        }
                    },
                    modifier = Modifier.testTag("notices-read-all"),
                ) {
                    Text("Mark all read")
                }
            }
        }
        when {
            notices.failed -> Text(
                FAILED_WORDS,
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                style = OrreryTokens.Type.small,
                color = OrreryTokens.Role.bad,
            )
            rows == null || !treeRead -> Text(
                LOADING_WORDS,
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
                color = OrreryTokens.Ink.three,
            )
            rows.isEmpty() -> Column(
                modifier = Modifier.padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.eight),
            ) {
                Text(EMPTY_WORDS, style = OrreryTokens.Type.body)
                Text(EMPTY_DETAIL_WORDS, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
            }
            else -> {
                val today = Instant.ofEpochSecond(now()).atZone(zone).toLocalDate()
                val days = rows.sortedByDescending { it.time }.groupBy { Instant.ofEpochSecond(it.time).atZone(zone).toLocalDate() }
                LazyColumn(
                    modifier = Modifier.weight(1f).padding(horizontal = OrreryTokens.Space.sixteen),
                ) {
                    for ((day, dayRows) in days) {
                        item(key = "day:$day") {
                            Text(
                                dayHeadOf(day, today),
                                modifier = Modifier.padding(vertical = OrreryTokens.Space.eight),
                                style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                                color = OrreryTokens.Ink.three,
                            )
                        }
                        items(dayRows, key = { "${noticeKeyOf(it)}@${it.time}" }) { row ->
                            NoticeRowItem(
                                row = row,
                                words = noticeWordsOf(row, tree, now()),
                                clock = noticeClockOf(row.time, zone),
                                onTap = {
                                    scope.launch {
                                        answered { client.noticeRowSeen(row) }
                                        if (row.limit.isEmpty() && row.alert.isEmpty()) onOpen(row)
                                        notices.relist()
                                    }
                                },
                            )
                        }
                    }
                }
            }
        }
    }
}

/**
 * Runs one write off the main thread. A write that does not answer leaves the rows as they were, and the read after it
 * shows what the server kept.
 */
private suspend fun answered(write: () -> Unit) {
    try {
        withContext(Dispatchers.IO) { write() }
    } catch (error: IOException) {
        // The read that follows the write shows the notice as the server kept it.
    }
}

/** One row of the history: its unread dot, its lead, its name and words, and its clock. */
@Composable
private fun NoticeRowItem(row: NoticeRow, words: NoticeWords, clock: String, onTap: () -> Unit) {
    val unread = !row.read
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onTap)
            .padding(vertical = OrreryTokens.Space.eight),
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
        verticalAlignment = Alignment.Top,
    ) {
        UnreadDot(unread)
        NoticeLeadMark(words.lead, row.epic)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                words.name,
                style = OrreryTokens.Type.body.copy(
                    fontWeight = if (unread) OrreryTokens.Font.Weight.bold else OrreryTokens.Font.Weight.regular,
                ),
                color = if (words.lead is NoticeLead.State) OrreryTokens.Role.place else OrreryTokens.Ink.one,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (words.words.isNotEmpty()) {
                Text(
                    words.words,
                    style = OrreryTokens.Type.small,
                    color = OrreryTokens.Ink.three,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Text(clock, style = OrreryTokens.Type.small, color = OrreryTokens.Ink.three)
    }
}

// The new dot, as the inbox draws it: drawn only for an unread row, else a gap of the same width.
@Composable
private fun UnreadDot(unread: Boolean) {
    if (unread) {
        Box(
            modifier = Modifier
                .padding(top = OrreryTokens.Space.four + OrreryTokens.Space.two)
                .size(OrreryTokens.Size.dot)
                .background(OrreryTokens.Role.new, CircleShape)
                .semantics { contentDescription = UNREAD_WORDS },
        )
    } else {
        Spacer(modifier = Modifier.width(OrreryTokens.Size.dot))
    }
}

@Composable
internal fun NoticeLeadMark(lead: NoticeLead, epicId: String) {
    when (lead) {
        is NoticeLead.Mark -> EpicMark(epicId, lead.mark, OrreryTokens.Size.gauge)
        is NoticeLead.State -> StateGlyph(lead.state, Modifier.padding(top = OrreryTokens.Space.four))
        NoticeLead.Bell -> BellMark()
        NoticeLead.Clock -> ClockMark()
    }
}

// A bell: a dome on a rim, with its clapper.
@Composable
private fun BellMark() {
    Canvas(modifier = Modifier.size(OrreryTokens.Size.glyph).semantics { contentDescription = BELL_WORDS }) {
        val stroke = Stroke(width = iconPx(ICON_STROKE), cap = StrokeCap.Round, join = StrokeJoin.Round)
        drawPath(
            Path().apply {
                moveTo(iconPx(3.5f), iconPx(11.5f))
                lineTo(iconPx(3.5f), iconPx(7.5f))
                cubicTo(iconPx(3.5f), iconPx(4.5f), iconPx(12.5f), iconPx(4.5f), iconPx(12.5f), iconPx(7.5f))
                lineTo(iconPx(12.5f), iconPx(11.5f))
                lineTo(iconPx(13.5f), iconPx(12.5f))
                lineTo(iconPx(2.5f), iconPx(12.5f))
                close()
            },
            OrreryTokens.Ink.two,
            style = stroke,
        )
        drawCircle(OrreryTokens.Ink.two, iconPx(1f), Offset(iconPx(8f), iconPx(14f)))
    }
}

// A clock: a ring with its hands at the time.
@Composable
private fun ClockMark() {
    Canvas(modifier = Modifier.size(OrreryTokens.Size.glyph).semantics { contentDescription = CLOCK_WORDS }) {
        val stroke = Stroke(width = iconPx(ICON_STROKE), cap = StrokeCap.Round)
        drawCircle(OrreryTokens.Ink.two, iconPx(6.25f), Offset(iconPx(8f), iconPx(8f)), style = stroke)
        drawLine(
            OrreryTokens.Ink.two,
            Offset(iconPx(8f), iconPx(4.5f)),
            Offset(iconPx(8f), iconPx(8f)),
            strokeWidth = stroke.width,
            cap = StrokeCap.Round,
        )
        drawLine(
            OrreryTokens.Ink.two,
            Offset(iconPx(8f), iconPx(8f)),
            Offset(iconPx(10.5f), iconPx(9.5f)),
            strokeWidth = stroke.width,
            cap = StrokeCap.Round,
        )
    }
}

private fun DrawScope.iconPx(view: Float): Float = view * size.width / ICON_VIEW

private fun noticeKeyOf(row: NoticeRow): String =
    listOf(row.pane, row.session, row.epic, row.limit, row.alert).joinToString("|")
