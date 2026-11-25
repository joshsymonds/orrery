package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow

/** A need's kind: an open question, a failed release step, a park, or any other blocker. */
enum class NeedKind { QUESTION, RELEASE, PARK, OTHER }

/**
 * One thing an epic waits on, as a sentence. It names its epic, and a question need its question's id.
 */
data class Need(
    val kind: NeedKind,
    val text: String,
    val epic: String,
    val question: String,
)

// The state of a release step that did not pass.
private const val RELEASE_FAILED = "failed"

/**
 * The needs of each live epic with a detail, in list order. Each open question the owner is asked is a question need,
 * even when the planner or the steerer holds the epic's park; a question the epic's steerer holds (steererHeld) raises
 * none until the steerer escalates it. An epic whose park the planner (plannerParked) or the steerer (steererParked)
 * holds raises no release, park or other need from that park: the holder answers it. For any other epic, the epic's
 * first failed release step is a release need, and it replaces the park. Otherwise a park is a park need, and any other
 * blocker is an other need with its own text, read by [otherBlockerOf].
 */
fun epicNeedsOf(epics: List<EpicRow>, details: Map<String, EpicDetail>): List<Need> {
    val needs = mutableListOf<Need>()
    for (epic in epics) {
        if (!epic.live) continue
        val detail = details[epic.id] ?: continue
        for (question in detail.questions) {
            if (question.steererHeld) continue
            needs.add(Need(NeedKind.QUESTION, "${epic.slug} asks: ${question.text}", epic.id, question.id))
        }
        val held = heldPark(detail)
        val failed = if (held) null else detail.releaseProof.firstOrNull { it.state == RELEASE_FAILED }
        if (failed != null) {
            val text = "${epic.slug}: release step ${failed.position} (${failed.step}) failed: ${failed.reason}"
            needs.add(epicNeed(NeedKind.RELEASE, epic, text))
        } else if (detail.parked.isNotEmpty() && !held) {
            needs.add(epicNeed(NeedKind.PARK, epic, "${epic.slug} is parked: ${detail.parked}"))
        } else {
            val blocker = otherBlockerOf(detail) ?: continue
            needs.add(epicNeed(NeedKind.OTHER, epic, "${epic.slug}: ${blocker.text}"))
        }
    }
    return needs
}

/**
 * The blocker an other need reads: the epic's blocker without its questions, which are raised first, and without its
 * park, which a park need or its holder answers.
 */
internal fun otherBlockerOf(detail: EpicDetail): EpicBlocker? =
    blockerOf(detail.copy(questions = emptyList(), parked = ""))

// heldPark is whether the planner or the epic's steerer holds the epic's park, so the owner is not asked about it.
private fun heldPark(detail: EpicDetail): Boolean = detail.plannerParked || detail.steererParked

private fun epicNeed(kind: NeedKind, epic: EpicRow, text: String): Need =
    Need(kind, text, epic.id, "")
