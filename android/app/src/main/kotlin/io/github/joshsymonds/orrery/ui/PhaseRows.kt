package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.TaskReviewRow

/** A phase of a task: its wedge's slice, and the step the phase's session opens, "" for none. */
data class PhaseRow(
    val phase: Phase,
    val state: SliceState,
    val summary: String,
    val since: Long,
    val seconds: Long,
    val session: String,
)

/** A step of a task and how it went. */
data class Attempt(
    val step: String,
    val end: String,
    val reason: String,
    val summary: String,
    val doing: String,
    val held: Boolean,
)

/** A check of a round other than the gate: its name, whether it passed, and its detail. */
data class CheckLine(val name: String, val ok: Boolean, val detail: String)

/** The gate of a round: its gate's name, whether it passed, its detail, and the redacted tails of its output. */
data class GateLine(val name: String, val ok: Boolean, val detail: String, val stdout: String, val stderr: String)

/** The done-judge's verdict in a round, and why. */
data class JudgeLine(val verdict: String, val reason: String)

/** One check round of a writing step: its checks other than the gate, the gate, the judge's verdict, and which ran. */
data class CheckRound(
    val step: String,
    val round: Int,
    val checks: List<CheckLine>,
    val gate: GateLine?,
    val judge: JudgeLine?,
    val ran: List<String>,
)

/** A task's phase output as the screen reads it, from one epic detail: its own steps, runs, reviews and rounds. */
data class PhaseOutput(
    val task: String,
    val title: String,
    val requirements: List<String>,
    val rows: List<PhaseRow>,
    val red: EvidenceRunRow?,
    val green: EvidenceRunRow?,
    val attempts: List<Attempt>,
    val rounds: List<CheckRound>,
    val reviews: List<TaskReviewRow>,
    val reviewers: List<Attempt>,
    val integrated: Boolean,
    val running: StepRow?,
)

// The step a phase's session opens: the latest writing step for implementing and checking, the latest read-only step
// for reviewing, none for integrating.
private fun sessionOf(phase: Phase, latestWriting: String, latestReading: String): String = when (phase) {
    Phase.IMPLEMENTING, Phase.CHECKING -> latestWriting
    Phase.REVIEWING -> latestReading
    Phase.INTEGRATING -> ""
}

private fun attemptOf(step: StepRow): Attempt =
    Attempt(step.id, step.end, step.reason, step.summary, step.doing, step.held)

// The round of a writing step: its checks, its gate and its verdict, each only when the round has it.
private fun roundOf(step: StepRow, round: Int): CheckRound {
    val checks = step.checks.filter { it.round == round }
    val gateCheck = checks.firstOrNull { it.name == "gate" }
    val others = checks.filter { it.name != "gate" }.map { CheckLine(it.name, it.ok, it.detail) }
    val verdict = step.verdicts.firstOrNull { it.round == round }
    val gate = gateCheck?.let { GateLine(step.gateName, it.ok, it.detail, it.tailStdout, it.tailStderr) }
    val judge = verdict?.let { JudgeLine(it.verdict, it.reason) }
    val ran = listOfNotNull(
        "checks".takeIf { others.isNotEmpty() },
        "gate".takeIf { gate != null },
        "judge".takeIf { judge != null },
    )
    return CheckRound(step.id, round, others, gate, judge, ran)
}

// The rounds of a writing step, ascending: each round a check or a verdict names.
private fun roundsOf(step: StepRow): List<CheckRound> =
    (step.checks.map { it.round } + step.verdicts.map { it.round }).toSortedSet().map { roundOf(step, it) }

/**
 * A task's phase output as the screen reads it, from one epic detail: its own steps, runs, reviews and rounds. Null
 * when the epic has no task with that id.
 */
fun phaseOutputOf(detail: EpicDetail, task: String): PhaseOutput? {
    val wedge = wedgesOf(detail).find { it.task == task } ?: return null
    val steps = detail.steps.filter { it.task == task }
    val writing = steps.filter { !it.readOnly }
    val reading = steps.filter { it.readOnly }
    val latestWriting = writing.lastOrNull()?.id ?: ""
    val latestReading = reading.lastOrNull()?.id ?: ""
    val build = detail.buildTasks.find { it.id == task }
    return PhaseOutput(
        task = task,
        title = wedge.title,
        requirements = wedge.requirements,
        rows = wedge.slices.map { slice ->
            PhaseRow(
                phase = slice.phase,
                state = slice.state,
                summary = slice.summary,
                since = slice.since,
                seconds = slice.seconds,
                session = sessionOf(slice.phase, latestWriting, latestReading),
            )
        },
        red = build?.red,
        green = build?.green,
        attempts = writing.map(::attemptOf),
        rounds = writing.flatMap(::roundsOf),
        reviews = build?.reviews ?: emptyList(),
        reviewers = reading.map(::attemptOf),
        integrated = wedge.place == WedgePlace.INTEGRATED,
        running = steps.lastOrNull { it.end == "" },
    )
}
