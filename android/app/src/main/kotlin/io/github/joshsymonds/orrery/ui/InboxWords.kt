package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.ContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.api.currentTool

/** The tone of an epic's gauge. */
internal enum class GaugeTone { NONE, LIVE, ENGINE, ATTENTION, OK }

/** The mark at a gauge's centre. */
internal enum class GaugeInner { CHECK, DASH, DOT }

/** An epic's gauge: its [done] and [total] tasks, its [tone], and the [inner] mark, null for none. */
internal data class GaugeShare(val done: Int, val total: Int, val tone: GaugeTone, val inner: GaugeInner?)

/**
 * What an epic or contract row says: its [name], its [count], its state [words], its [blocker] (empty when nothing
 * blocks it), and its [gauge], which a contract's row does not draw.
 */
internal data class EpicRowWords(
    val name: String,
    val count: String,
    val words: String,
    val blocker: String,
    val gauge: GaugeShare?,
)

private const val CONTRACT_PROPOSED = "proposed"
private const val WAITS_ON_ACCEPT = "waits to be read and accepted"
private const val RELEASED = "released"
private const val PAUSED_BY_LIMIT = "limit"
private const val RELEASE_FAILED = "failed"
private const val STATE_WAITING = "waiting"
private const val STATE_WORKING = "working"
private const val TERMINAL_WORDS = "a terminal with no conversation"
private const val WAITS_ON_YOU = "waits on you"
private const val RELEASING = "releasing"
private val DONE_STATUSES = setOf("done", "integrated")

/** What a row says of its epic or contract at [now], in unix seconds. A proposed contract draws as the contract. */
internal fun epicRowWords(hung: Hung, now: Long): EpicRowWords = when (hung) {
    is ContractNode -> contractWords(hung.contract, now)
    is EpicNode -> hung.contract?.takeIf { it.state == CONTRACT_PROPOSED }?.let { contractWords(it, now) }
        ?: epicWords(hung, now)
}

/** A conversation's second line at [now], in unix seconds; a stand-in row's words when it has no pane. */
internal fun convoLineOf(convo: Convo, now: Long): String {
    val pane = convo.pane ?: return standInWords(convo.standIn)
    return when (pane.state) {
        "" -> TERMINAL_WORDS
        STATE_WAITING -> waitingLine(pane)
        STATE_WORKING -> workingLine(pane, now)
        else -> lastWords(pane)
    }
}

// A headless session's last words; a tmux pane's summary is its title, so a pane has none here.
private fun lastWords(pane: Pane): String = if (pane.headless) pane.summary else ""

// A contract reads its version, its state with its age, and while it is proposed, the ask it waits on.
private fun contractWords(contract: ContractRow, now: Long): EpicRowWords {
    val words = listOf("contract", contract.state, sinceText(contract.changed, now, live = false))
    return EpicRowWords(
        name = contract.id.substringAfter('/'),
        count = "v${contract.version}",
        words = words.filter { it.isNotEmpty() }.joinToString(" · "),
        blocker = if (contract.state == CONTRACT_PROPOSED) WAITS_ON_ACCEPT else "",
        gauge = null,
    )
}

// An epic reads its count, its state word with the time since its phase began, its blocker, and its gauge.
private fun epicWords(node: EpicNode, now: Long): EpicRowWords {
    val listed = node.epic
    val detail = node.detail
    val state = stateWordOf(listed, detail)
    val timed = sinceText(listed.phaseAt, now, listed.live)
    val blocker = blockerWordsOf(listed, detail, now)
    return EpicRowWords(
        name = listed.slug,
        count = epicLineOf(detail, now).count,
        words = if (state.isEmpty()) "" else listOf(state, timed).filter { it.isNotEmpty() }.joinToString(" · "),
        blocker = blocker,
        gauge = gaugeOf(listed, detail, state, blocker),
    )
}

// The state word of an epic: shipped or ended short once it has ended; paused by you or on a usage limit, then parked;
// then the part of its track its phase is in. An epic with no detail has none while it is live.
private fun stateWordOf(listed: EpicRow, detail: EpicDetail?): String {
    if (!listed.live) return if (listed.outcome == RELEASED) "shipped" else "ended short"
    if (listed.paused) return if (listed.pauseSource == PAUSED_BY_LIMIT) "paused on a usage limit" else "paused by you"
    if (listed.parked.isNotEmpty()) return "parked"
    return if (detail == null) "" else trackPartOf(listed.outcome, detail) ?: ""
}

// The track part a phase is in: planning or building while accepted, building while built, reviewing or correcting,
// releasing while clean, releasing or stopped, and report once released or closed. Null for any other phase.
private fun trackPartOf(phase: String, detail: EpicDetail): String? = when (phase) {
    "accepted" -> if (detail.tasks.isEmpty() && detail.buildTasks.isEmpty()) "planning" else "building"
    "built", "reviewing", "correcting" -> "building"
    "clean", "releasing", "stopped" -> RELEASING
    "released", "done", "met_at_base", "ended_with_gaps", "closed_superseded", "closed_shipped", "closed_abandoned" ->
        "report"
    else -> null
}

// The first thing an epic waits on, as its row says it: a question with its wait, a failed release step with its
// reason, a park's reason, or any other blocker's text. An ended epic waits on nothing.
private fun blockerWordsOf(listed: EpicRow, detail: EpicDetail?, now: Long): String {
    if (detail == null) return ""
    val need = epicNeedsOf(listOf(listed), mapOf(listed.id to detail)).firstOrNull() ?: return ""
    return when (need.kind) {
        NeedKind.QUESTION -> blockerOf(detail)?.let { blockerTextOf(it, now) }.orEmpty()
        NeedKind.RELEASE -> detail.releaseProof.firstOrNull { it.state == RELEASE_FAILED }
            ?.let { "Release step ${it.position} failed: ${it.reason}" }.orEmpty()
        NeedKind.PARK -> detail.parked
        NeedKind.OTHER -> blockerOf(detail)?.text.orEmpty()
        NeedKind.ROW -> ""
    }
}

// A gauge: a check in ok when an ended epic shipped, a dash when it ended short, a dot in attention when blocked, no
// tone when paused, engine while releasing, and live otherwise. Its share is the done tasks out of all of them.
private fun gaugeOf(listed: EpicRow, detail: EpicDetail?, state: String, blocker: String): GaugeShare {
    val tasks = detail?.tasks.orEmpty()
    val done = tasks.count { it.status.lowercase() in DONE_STATUSES }
    val total = tasks.size
    return when {
        !listed.live && state == "shipped" -> GaugeShare(done, total, GaugeTone.OK, GaugeInner.CHECK)
        !listed.live -> GaugeShare(done, total, GaugeTone.NONE, GaugeInner.DASH)
        blocker.isNotEmpty() -> GaugeShare(done, total, GaugeTone.ATTENTION, GaugeInner.DOT)
        listed.paused -> GaugeShare(done, total, GaugeTone.NONE, null)
        state == RELEASING -> GaugeShare(done, total, GaugeTone.ENGINE, null)
        else -> GaugeShare(done, total, GaugeTone.LIVE, null)
    }
}

// The question led by a question mark when a pane waits on you; a waiting pane with no question waits on you.
// What a waiting row asks: its question's title led by "?", else a headless session's last words, else that it waits on
// you. A tmux pane's summary is its title, so a pane never repeats it here.
private fun waitingLine(pane: Pane): String = when {
    pane.question.isNotEmpty() -> "? ${parseQuestion(pane.question).title}"
    pane.headless && pane.summary.isNotEmpty() -> pane.summary
    else -> WAITS_ON_YOU
}

// The tool and its argument with a clock while a pane works: the clock runs from the tool's start, and a tool with no
// start draws none. A working row with no tool reads its last words.
private fun workingLine(pane: Pane, now: Long): String {
    val tool = pane.currentTool()
    if (tool == null || tool.name.isEmpty()) return lastWords(pane)
    val call = listOf(tool.name.substringAfterLast("__"), tool.input).filter { it.isNotEmpty() }.joinToString(" ")
    val clock = if (tool.start > 0L) clockText((now - tool.start).coerceAtLeast(0L)) else ""
    return listOf(call, clock).filter { it.isNotEmpty() }.joinToString(" · ")
}

// A running tool's clock, minutes and seconds: "0:46", "12:05".
private fun clockText(seconds: Long): String = "${seconds / 60}:${(seconds % 60).toString().padStart(2, '0')}"

private fun standInWords(standIn: StandIn): String = when (standIn) {
    StandIn.UNLISTED -> "its conversation is not listed"
    StandIn.ARCHIVED -> "its conversation is archived"
    StandIn.NONE -> ""
}

// An elapsed time since a stamp: "49 min" while live, "49 min ago" once ended. A stamp of zero reads none, and a clock
// behind the stamp reads zero.
private fun sinceText(since: Long, now: Long, live: Boolean): String {
    if (since <= 0L) return ""
    val elapsed = durationText((now - since).coerceAtLeast(0L))
    return if (live) elapsed else "$elapsed ago"
}
