package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.TaskFindingRow
import io.github.joshsymonds.orrery.api.TaskReviewRow

/** The tone a body line or chip draws in: ok, bad, attention (held), neutral, live (running), quiet, or acc (accepted). */
enum class BodyTone { OK, BAD, ATTENTION, NEUTRAL, LIVE, QUIET, ACC }

/** A line of words in its tone, or a chip: its word and its tone. */
data class ToneLine(val text: String, val tone: BodyTone)

/** A stage's run: the run when there is one, else the quiet line [none] that says so. */
data class RunSection(val run: EvidenceRunRow?, val none: ToneLine)

/** One implementer step of a task: its attempt [number], its [words] and its chip. */
data class AttemptRow(val number: Int, val words: String, val chip: ToneLine)

/** One check of a round: its [name] and whether it passed. */
data class RoundCheck(val name: String, val ok: Boolean)

/** One check round of the latest attempt: its [checks] and its [text], one line per failed check. */
data class RoundBlock(val round: Int, val checks: List<RoundCheck>, val text: List<String>)

/** The gate of the latest attempt's last round: its [label] and the non-empty lines of its tails. */
data class GateLog(val label: String, val lines: List<String>)

/** The latest attempt of a task: its [label], its check [rounds], its [gate] when its last round has one, and its [judge] line. */
data class LatestAttempt(
    val label: String,
    val rounds: List<RoundBlock>,
    val gate: GateLog?,
    val judge: ToneLine,
)

/** The implement stage: its [attempts] in started order, its [latest] attempt, and the step its session door opens. */
data class ImplementBody(val attempts: List<AttemptRow>, val latest: LatestAttempt?, val session: String?)

/** The green stage: its [reproduction] line, and its [run] or the quiet line that none has run. */
data class GreenBody(val reproduction: ToneLine, val run: RunSection)

/**
 * One row of the review table: the [gate] cell (on a review's first row only), the [what] and [where] of the row, its
 * [result] chip and the [reason] after it.
 */
data class ReviewRow(val gate: String, val what: String, val where: String, val result: ToneLine, val reason: String)

/** The review stage: its [rows], the quiet [none] line used when there are none, and the step its session door opens. */
data class ReviewBody(val rows: List<ReviewRow>, val none: ToneLine, val session: String?)

/** The integrate stage: its one line. */
data class IntegrateBody(val line: ToneLine)

/** What each of a task's five stages shows when opened. */
data class StageBodies(
    val red: RunSection,
    val implement: ImplementBody,
    val green: GreenBody,
    val review: ReviewBody,
    val integrate: IntegrateBody,
)

/** A task's stage bodies as an opened stage draws them, from one epic detail. Null when the epic has no task with that id. */
fun stageBodiesOf(detail: EpicDetail, task: String): StageBodies? {
    val record = recordTasksOf(detail).find { it.task == task } ?: return null
    val build = detail.buildTasks.find { it.id == task }
    val steps = detail.steps.filter { it.task == task }.sortedBy { it.started }
    val writing = steps.filter { !it.readOnly }
    val reading = steps.filter { it.readOnly }
    return StageBodies(
        red = RunSection(build?.red, ToneLine("No red run yet.", BodyTone.QUIET)),
        implement = implementOf(writing),
        green = GreenBody(
            reproduction = reproductionOf(build?.green),
            run = RunSection(build?.green, ToneLine("No green run yet.", BodyTone.QUIET)),
        ),
        review = reviewOf(build?.reviews ?: emptyList(), reading.lastOrNull()?.id),
        integrate = IntegrateBody(
            if (record.integrated) ToneLine("Merged.", BodyTone.OK)
            else ToneLine("Every gate took it. It waits for its merge.", BodyTone.ACC),
        ),
    )
}

// The implement stage of a task's writing steps, which are in started order.
private fun implementOf(writing: List<StepRow>): ImplementBody {
    val latest = writing.lastOrNull()
    return ImplementBody(
        attempts = writing.mapIndexed { index, step -> AttemptRow(index + 1, wordsOf(step), chipOf(step)) },
        latest = latest?.let { latestAttemptOf(it, writing.size) },
        session = latest?.id,
    )
}

// An attempt's words: its summary once it ended, else its reason, else what it is doing.
private fun wordsOf(step: StepRow): String = when {
    step.end != "" && step.summary.isNotEmpty() -> step.summary
    step.reason.isNotEmpty() -> step.reason
    else -> step.doing
}

// An attempt's chip: held while held, running while it runs, done when it ended done, spent when it ended otherwise.
private fun chipOf(step: StepRow): ToneLine = when {
    step.end == "" && step.held -> ToneLine("held", BodyTone.ATTENTION)
    step.end == "" -> ToneLine("running", BodyTone.LIVE)
    step.end == "done" -> ToneLine("done", BodyTone.OK)
    else -> ToneLine("spent", BodyTone.BAD)
}

// The latest attempt of a task, the number-th of its implementer steps: its rounds, its last round's gate and its judge.
private fun latestAttemptOf(step: StepRow, number: Int): LatestAttempt {
    val rounds = (step.checks.map { it.round } + step.verdicts.map { it.round }).toSortedSet()
    val lastRound = rounds.lastOrNull()
    val gateCheck = step.checks.firstOrNull { it.round == lastRound && it.name == "gate" }
    return LatestAttempt(
        label = "Attempt $number · check rounds",
        rounds = rounds.map { roundOf(step, it) },
        gate = gateCheck?.let {
            GateLog(
                "Gate · ${step.gateName}",
                listOf(it.tailStdout, it.tailStderr).filter { tail -> tail.isNotEmpty() },
            )
        },
        judge = judgeOf(step),
    )
}

// A round: its checks in the step's order, and its text, one line per failed check or else that all pass.
private fun roundOf(step: StepRow, round: Int): RoundBlock {
    val checks = step.checks.filter { it.round == round }
    val failed = checks.filter { !it.ok }
    return RoundBlock(
        round = round,
        checks = checks.map { RoundCheck(it.name, it.ok) },
        text = if (failed.isEmpty()) listOf("All checks pass.") else failed.map(::failureLineOf),
    )
}

private fun failureLineOf(check: StepCheckRow): String =
    if (check.detail.isEmpty()) "${check.name} failed" else "${check.name}: ${check.detail}"

// The judge's line: the goal met, or the last verdict with its reason, else that the judge waits for the gate.
private fun judgeOf(step: StepRow): ToneLine {
    val verdict = step.verdicts.filter { it.verdict.isNotEmpty() }.maxByOrNull { it.round }
        ?: return ToneLine("The judge reads the change once the gate passes.", BodyTone.QUIET)
    if (verdict.verdict == "met") return ToneLine("Judged: the goal is met.", BodyTone.OK)
    val words = listOf("Judged: ${verdict.verdict}.", verdict.reason).filter { it.isNotEmpty() }
    return ToneLine(words.joinToString(" "), BodyTone.BAD)
}

// The reproduction line from the green run: not run, running, or how it came out on the change.
private fun reproductionOf(green: EvidenceRunRow?): ToneLine = when {
    green == null -> ToneLine("The red test has not run on the change yet.", BodyTone.QUIET)
    green.state != "ended" -> ToneLine("The red test is running on the change.", BodyTone.QUIET)
    green.passed -> ToneLine("The red test, run on the change, now passes.", BodyTone.OK)
    else -> ToneLine("The red test, run on the change, still fails.", BodyTone.BAD)
}

// The review stage: each review's rows in order, and the step its session door opens.
private fun reviewOf(reviews: List<TaskReviewRow>, latestReading: String?): ReviewBody {
    val spansAttempts = reviews.map { it.attempt }.distinct().size > 1
    return ReviewBody(
        rows = reviews.flatMap { review ->
            val gate = if (spansAttempts) "${review.gate} · attempt ${review.attempt}" else review.gate
            if (review.findings.isEmpty()) {
                listOf(ReviewRow(gate, "no findings", "", verdictChipOf(review.verdict), review.reason))
            } else {
                review.findings.mapIndexed { index, finding ->
                    val first = if (index == 0) gate else ""
                    ReviewRow(first, finding.claim, finding.location, resultChipOf(finding), reasonOf(finding))
                }
            }
        },
        none = ToneLine("No review has run yet.", BodyTone.QUIET),
        session = latestReading,
    )
}

private fun verdictChipOf(verdict: String): ToneLine = when (verdict) {
    "holds" -> ToneLine("holds", BodyTone.OK)
    "fails" -> ToneLine("fails", BodyTone.BAD)
    else -> ToneLine(verdict, BodyTone.NEUTRAL)
}

private fun resultChipOf(finding: TaskFindingRow): ToneLine = when (finding.result) {
    "" -> ToneLine("verifying", BodyTone.LIVE)
    "confirmed" -> ToneLine("confirmed", BodyTone.BAD)
    "dropped" -> ToneLine("dropped", BodyTone.NEUTRAL)
    "undecided", "undetermined" -> ToneLine("undecided", BodyTone.NEUTRAL)
    else -> ToneLine(finding.result, BodyTone.NEUTRAL)
}

private fun reasonOf(finding: TaskFindingRow): String =
    if (finding.result == "") "a verifier is checking it" else finding.reason
