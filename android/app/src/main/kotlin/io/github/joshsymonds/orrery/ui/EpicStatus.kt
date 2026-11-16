package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.StepRow

/**
 * The one blocker an epic waits on: its text, and the unix second it began. A question carries the question, a held
 * or limited step carries the step.
 */
sealed interface EpicBlocker {
    val text: String
    val since: Long

    data class Question(
        override val text: String,
        override val since: Long,
        val question: EpicQuestionRow,
    ) : EpicBlocker

    data class Parked(override val text: String, override val since: Long) : EpicBlocker

    data class Held(override val text: String, override val since: Long, val step: StepRow) : EpicBlocker
}

/**
 * The state word of an epic, first match: its outcome as written, paused, parked, waiting, then planning or building
 * while it is live. An epic with no state gives the empty string.
 */
fun epicStateOf(detail: EpicDetail): String {
    val epic = detail.epic
    return when {
        epic.outcome.isNotEmpty() -> epic.outcome
        detail.paused -> "paused"
        detail.parked.isNotEmpty() -> "parked"
        epic.waiting -> "waiting"
        epic.live -> if (detail.tasks.isEmpty() && detail.buildTasks.isEmpty()) "planning" else "building"
        else -> ""
    }
}

/**
 * The blocker an epic waits on, first match: its first open question, its park, then its first step that has not
 * ended and is held or limited. Null when none of them holds.
 */
fun blockerOf(detail: EpicDetail): EpicBlocker? {
    val question = detail.questions.firstOrNull()
    if (question != null) {
        return EpicBlocker.Question(text = "waits on your answer", since = question.asked, question = question)
    }
    if (detail.parked.isNotEmpty()) {
        return EpicBlocker.Parked(text = "parked · ${detail.parked}", since = 0L)
    }
    val step = detail.steps.firstOrNull { it.end.isEmpty() && (it.held || it.limited) } ?: return null
    val name = step.task.ifEmpty { step.agent }
    val since = if (step.stageStarted != 0L) step.stageStarted else step.started
    return EpicBlocker.Held(text = name + stepWait(step), since = since, step = step)
}

// The wait a held or limited step shows after its name: held wins over limited, and a limit with no kind reads as a
// plain limit.
private fun stepWait(step: StepRow): String = when {
    step.held -> " held"
    step.limitKind.isEmpty() -> " waits on a limit"
    else -> " waits on a ${step.limitKind} limit"
}
