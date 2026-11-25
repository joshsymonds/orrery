package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail

/** An epic's line: its [count], its [state] phrase and its [blocker] phrase, each empty when it has none. */
data class EpicLine(val count: String, val state: String, val blocker: String)

/**
 * The words of an epic's line at [now], in unix seconds. The count is `<p> of <n> proven` over the requirements, or
 * `<d> of <t> tasks done` with none. The state is the title row's state phrase, then `<i> of <w> units integrated` when
 * the epic has wedges. The blocker is the blocker's text and wait, a park included. A null detail reads
 * `epic unavailable`, with the rest empty.
 */
fun epicLineOf(detail: EpicDetail?, now: Long): EpicLine {
    if (detail == null) return EpicLine(count = "epic unavailable", state = "", blocker = "")
    val wedges = wedgesOf(detail)
    return EpicLine(
        count = countOf(detail, wedges),
        state = stateWordsOf(detail, wedges, now),
        blocker = blockerOf(detail)?.let { blockerTextOf(it, now) }.orEmpty(),
    )
}

// The task statuses that count as done, compared without case, as the web's taskStateOf counts them.
private val DONE_STATUSES = setOf("done", "integrated")

// The count: the proven requirements over all of them, else the done tasks over all of them.
private fun countOf(detail: EpicDetail, wedges: List<Wedge>): String {
    val requirements = detail.requirements
    if (requirements.isNotEmpty()) {
        val proven = requirements.count { provenOf(it.id, wedges, requirements) }
        return "$proven of ${requirements.size} proven"
    }
    val done = detail.buildTasks.count { it.status.lowercase() in DONE_STATUSES }
    return "$done of ${detail.buildTasks.size} tasks done"
}

// The state: the title row's state phrase, then the integrated units of the wedges when there are any.
private fun stateWordsOf(detail: EpicDetail, wedges: List<Wedge>, now: Long): String {
    val state = stateLineOf(epicStateOf(detail), detail.epic.phaseAt, now)
    if (wedges.isEmpty()) return state
    val integrated = wedges.count { it.place == WedgePlace.INTEGRATED }
    val units = "$integrated of ${wedges.size} units integrated"
    return if (state.isEmpty()) units else "$state · $units"
}

// A blocker's text, and how long it has waited once it has begun, never below zero when the clock is behind its stamp.
internal fun blockerTextOf(blocker: EpicBlocker, now: Long): String =
    if (blocker.since > 0L) "${blocker.text} · ${durationText((now - blocker.since).coerceAtLeast(0L))}" else blocker.text
