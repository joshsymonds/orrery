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
 * The needs of each live epic with a detail, in list order. Each open question is a question need. The epic's first
 * failed release step is a release need, and it replaces the park. Otherwise a park is a park need, and any other
 * blocker is an other need with its own text. The blocker is read without the questions, which are raised first.
 */
fun epicNeedsOf(epics: List<EpicRow>, details: Map<String, EpicDetail>): List<Need> {
    val needs = mutableListOf<Need>()
    for (epic in epics) {
        if (!epic.live) continue
        val detail = details[epic.id] ?: continue
        for (question in detail.questions) {
            needs.add(Need(NeedKind.QUESTION, "${epic.slug} asks: ${question.text}", epic.id, question.id))
        }
        val failed = detail.releaseProof.firstOrNull { it.state == RELEASE_FAILED }
        if (failed != null) {
            val text = "${epic.slug}: release step ${failed.position} (${failed.step}) failed: ${failed.reason}"
            needs.add(epicNeed(NeedKind.RELEASE, epic, text))
        } else if (detail.parked.isNotEmpty()) {
            needs.add(epicNeed(NeedKind.PARK, epic, "${epic.slug} is parked: ${detail.parked}"))
        } else {
            val blocker = blockerOf(detail.copy(questions = emptyList())) ?: continue
            needs.add(epicNeed(NeedKind.OTHER, epic, "${epic.slug}: ${blocker.text}"))
        }
    }
    return needs
}

private fun epicNeed(kind: NeedKind, epic: EpicRow, text: String): Need =
    Need(kind, text, epic.id, "")
