package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.TaskReviewRow

/**
 * One of a task's five stage rows, in [RingPlace] order: [state] is passed, now or ahead, and [tone] is the task's tone on
 * the now row. [summary] is the row's words, and [time] is a passed row's duration or the now row's clock, "" when none is
 * drawn. [open] is whether the record opens this row.
 */
data class StageRow(
    val place: RingPlace,
    val state: SliceState,
    val tone: WedgeTone?,
    val summary: String,
    val time: String,
    val open: Boolean,
)

/**
 * A task's five stage rows at [now] (unix seconds), outside in. The task's place splits them: the rows before it are
 * passed, its place is now, and the rest are ahead; an integrated task has every row passed, and a planned one none.
 * The record opens the now row, or the integrate row when the task is integrated.
 */
fun stagesOf(detail: EpicDetail, task: String, now: Long): List<StageRow> {
    val record = recordTasksOf(detail).first { it.task == task }
    val build = detail.buildTasks.find { it.id == task }
    val steps = detail.steps.filter { it.task == task }
    val implementers = steps.filter { !it.readOnly }
    val reviewers = steps.filter { it.readOnly }
    val reached = when {
        record.integrated -> RingPlace.entries.size
        record.place == null -> -1
        else -> record.place.ordinal
    }
    val openPlace = if (record.integrated) RingPlace.INTEGRATE else record.place
    val clock = if (record.since > 0) elapsedClock(now - record.since) else ""
    return RingPlace.entries.map { ring ->
        val state = stageStateOf(ring.ordinal, reached)
        StageRow(
            place = ring,
            state = state,
            tone = if (state == SliceState.NOW) record.tone else null,
            summary = if (state == SliceState.AHEAD) {
                "not reached"
            } else {
                wordsOf(ring, build, implementers, record.integrated)
            },
            time = when (state) {
                SliceState.PASSED -> durationOrEmpty(passedSecondsOf(ring, build, implementers, reviewers))
                SliceState.NOW -> clock
                SliceState.AHEAD -> ""
            },
            open = ring == openPlace,
        )
    }
}

// The state of the stage at index, given the index of the place's stage.
private fun stageStateOf(index: Int, reached: Int): SliceState = when {
    index < reached -> SliceState.PASSED
    index == reached -> SliceState.NOW
    else -> SliceState.AHEAD
}

// A stage's words, for a row that is passed or now.
private fun wordsOf(
    ring: RingPlace,
    build: EpicBuildTaskRow?,
    implementers: List<StepRow>,
    integrated: Boolean,
): String = when (ring) {
    RingPlace.RED -> runWordsOf(
        build?.red,
        capturing = "capturing the failing test",
        judging = "a judge is reading the red run",
        passed = "the test fails before any change",
        failed = "the test passes before any change",
    )
    RingPlace.IMPLEMENT -> implementWordsOf(build?.stage.orEmpty(), implementers)
    RingPlace.GREEN -> runWordsOf(
        build?.green,
        capturing = "capturing the green run",
        judging = "a judge is reading the green run",
        passed = "the test passes on the change",
        failed = "the test still fails on the change",
    )
    RingPlace.REVIEW -> reviewWordsOf(build?.stage.orEmpty(), build?.reviews.orEmpty())
    RingPlace.INTEGRATE -> if (integrated) "merged" else "accepted · waits for its merge"
}

// A red or green run's words: capturing while it has no run or captures, waiting for the gatherer or being gathered,
// judging while a judge reads it, then whether it passed, with judged to hold when its verdict holds.
private fun runWordsOf(
    run: EvidenceRunRow?,
    capturing: String,
    judging: String,
    passed: String,
    failed: String,
): String = when {
    run == null || run.state == "capturing" -> capturing
    run.state == "waiting" -> WAITING_FOR_GATHERER
    run.state == "gathering" -> BEING_GATHERED
    run.state == "judging" -> judging
    else -> (if (run.passed) passed else failed) + if (run.verdict == "holds") " · judged to hold" else ""
}

// The implement words: an orchestrator decides when the build is orchestrating, else the attempts and the latest one.
private fun implementWordsOf(stage: String, implementers: List<StepRow>): String {
    if (stage == "orchestrate") {
        return "an orchestrator is deciding on the implementer’s request"
    }
    val latest = implementers.lastOrNull() ?: return "not started"
    val attempt = "attempt ${implementers.size}"
    return if (latest.end == "") {
        attempt + roundWordsOf(latest) + stepStageWordsOf(latest)?.let { " · $it" }.orEmpty()
    } else {
        attempt + endedWordsOf(latest)
    }
}

// A running step's last round, passed when every check of that round is ok, failed otherwise; "" when it has no round.
private fun roundWordsOf(step: StepRow): String {
    val round = (step.checks.map { it.round } + step.verdicts.map { it.round }).maxOrNull() ?: return ""
    val passed = step.checks.filter { it.round == round }.all { it.ok }
    return " · round $round ${if (passed) "passed" else "failed"}"
}

// What a running step is doing: held, else waiting on a usage limit, else its stage's words; null for a stage with none.
private fun stepStageWordsOf(step: StepRow): String? = when {
    step.held -> "held"
    step.limited -> "waits on a usage limit"
    else -> when (step.stage) {
        "turn" -> "the worker is writing"
        "checks" -> "the checks are running"
        "gate" -> "the gate is running"
        "judge" -> "a judge is reading the change"
        else -> null
    }
}

// How an ended step went: done with its last gate check and its last verdict said, else ended with its end.
private fun endedWordsOf(step: StepRow): String {
    if (step.end != "done") {
        return " · ended: ${step.end}"
    }
    val gate = if (step.checks.lastOrNull { it.name == "gate" }?.ok == true) " · the gate passed" else ""
    val judged = if (step.verdicts.lastOrNull()?.verdict == "met") " · judged met" else ""
    return gate + judged
}

// The review words: a verifier checks the open findings when the build is verifying, else each review in order, and
// the review gates read the change when there is none.
private fun reviewWordsOf(stage: String, reviews: List<TaskReviewRow>): String {
    if (stage == "verify") {
        val open = reviews.sumOf { review -> review.findings.count { it.result == "" } }
        return when (open) {
            0 -> "a verifier is checking its findings"
            1 -> "a verifier is checking 1 finding"
            else -> "a verifier is checking $open findings"
        }
    }
    if (reviews.isEmpty()) {
        return "the review gates are reading the change"
    }
    return reviews.joinToString(" · ") { reviewLineOf(it) }
}

// One review's words: its gate and verdict when it found nothing, else its gate, its findings and those confirmed.
private fun reviewLineOf(review: TaskReviewRow): String {
    if (review.findings.isEmpty()) {
        return "${review.gate} ${review.verdict}"
    }
    val count = review.findings.size
    val confirmed = review.findings.count { it.result == "confirmed" }
    val found = "${review.gate}: $count ${if (count == 1) "finding" else "findings"}"
    return if (confirmed > 0) "$found, $confirmed confirmed" else found
}

// The seconds a passed row took: a run's end less its start, the span of the implementer or reviewer steps, none for
// integrate.
private fun passedSecondsOf(
    ring: RingPlace,
    build: EpicBuildTaskRow?,
    implementers: List<StepRow>,
    reviewers: List<StepRow>,
): Long = when (ring) {
    RingPlace.RED -> runSecondsOf(build?.red)
    RingPlace.IMPLEMENT -> spanSecondsOf(implementers)
    RingPlace.GREEN -> runSecondsOf(build?.green)
    RingPlace.REVIEW -> spanSecondsOf(reviewers)
    RingPlace.INTEGRATE -> 0L
}

// A run's seconds from its creation to its end, 0 when either is not known.
private fun runSecondsOf(run: EvidenceRunRow?): Long =
    if (run == null || run.created == 0L || run.ended == 0L) 0L else run.ended - run.created

// The seconds from the first step's start to the last step's end, 0 when there is no step or the last has not ended.
private fun spanSecondsOf(steps: List<StepRow>): Long {
    val first = steps.firstOrNull() ?: return 0L
    val last = steps.last()
    return if (first.started == 0L || last.ended == 0L) 0L else last.ended - first.started
}

// A passed row's time: its duration, or "" for no time.
private fun durationOrEmpty(seconds: Long): String = if (seconds > 0) durationText(seconds) else ""
