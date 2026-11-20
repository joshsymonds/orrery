package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.NoticeRow
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** What leads a notice row: an epic's gauge, a session's or pane's state glyph, or the bell or the clock. */
internal sealed interface NoticeLead {
    data class Gauge(val share: GaugeShare) : NoticeLead

    data class State(val state: String) : NoticeLead

    data object Bell : NoticeLead

    data object Clock : NoticeLead
}

/** What a notice row says: its [lead], its [name] (a directory, an epic's slug or a title) and its [words]. */
internal data class NoticeWords(val lead: NoticeLead, val name: String, val words: String)

// A session's or pane's title reads its directory, then the separator, then its id.
private const val TITLE_SEPARATOR = " · "
private const val BLOCK_QUESTION = "question"
private const val BLOCK_RELEASE_STEP = "release_step"
private const val BLOCK_REPORT = "report"
private const val BLOCK_CONTRACT = "contract"
private val DAY_HEAD: DateTimeFormatter = DateTimeFormatter.ofPattern("EEEE d MMMM", Locale.ENGLISH)

/** What a notice row says at [now], in unix seconds, against the [tree] read, null when no tree was read. */
internal fun noticeWordsOf(row: NoticeRow, tree: TreeData?, now: Long): NoticeWords = when {
    row.limit.isNotEmpty() -> NoticeWords(NoticeLead.Clock, row.title, "")
    row.alert.isNotEmpty() -> NoticeWords(NoticeLead.Bell, row.title, "")
    row.epic.isNotEmpty() -> epicNoticeWords(row, tree, now)
    else -> conversationNoticeWords(row, tree)
}

// An epic leads with the gauge its inbox card draws, then its slug and its block's words. An epic the tree does not list
// leads with the bell, then the text after the last / of its id.
private fun epicNoticeWords(row: NoticeRow, tree: TreeData?, now: Long): NoticeWords {
    val words = blockWordsOf(row.blockKind)
    val listed = tree?.epics?.firstOrNull { it.id == row.epic }
        ?: return NoticeWords(NoticeLead.Bell, row.epic.substringAfterLast('/'), words)
    val node = EpicNode(listed, tree.details[listed.id], contract = null)
    val lead = epicWords(node, now).gauge?.let(NoticeLead::Gauge) ?: NoticeLead.Bell
    return NoticeWords(lead, listed.slug, words)
}

// A session or a pane leads with its state's glyph, then its listed directory's last element, else the title before the
// separator, then its state's words. Its id is never named.
private fun conversationNoticeWords(row: NoticeRow, tree: TreeData?): NoticeWords {
    val key = row.pane.ifEmpty { row.session }
    val directory = tree?.panes?.firstOrNull { it.id == key }?.cwd.orEmpty()
    val name = directory.trimEnd('/').substringAfterLast('/').ifEmpty { row.title.substringBefore(TITLE_SEPARATOR) }
    return NoticeWords(NoticeLead.State(row.state), name, stateWordsOf(row.state))
}

// The words of an epic's block, as the web's notice says them; a notice with no block waits on you.
private fun blockWordsOf(kind: String): String = when (kind) {
    BLOCK_QUESTION -> "asks you a question"
    BLOCK_RELEASE_STEP -> "a release step failed"
    BLOCK_REPORT -> "finished"
    BLOCK_CONTRACT -> "has a contract for you"
    else -> "waits on you"
}

// The words of a session's or pane's state, as SYSTEM.md's state table words them.
private fun stateWordsOf(state: String): String = when (state) {
    "waiting" -> "waiting on you"
    "done" -> "done"
    "failed" -> "failed"
    "interrupted" -> "cut off"
    "stopped" -> "stopped"
    else -> ""
}

/** The head of a day of notices: "Today", "Yesterday", else the weekday and date, as "Monday 16 November". */
internal fun dayHeadOf(day: LocalDate, today: LocalDate): String = when (day) {
    today -> "Today"
    today.minusDays(1) -> "Yesterday"
    else -> DAY_HEAD.format(day)
}

/** The clock of a notice at [time], in unix seconds, in [zone], as HH:mm. */
internal fun noticeClockOf(time: Long, zone: ZoneId): String = Instant.ofEpochSecond(time).atZone(zone).format(CLOCK_TIME)
