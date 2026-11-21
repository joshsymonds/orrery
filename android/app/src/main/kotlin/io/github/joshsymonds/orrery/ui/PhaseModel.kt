package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.StepRow

/** The phases a task's work goes through, in order. [word] is the phase's name as the phone shows it. */
enum class Phase(val word: String) {
    IMPLEMENTING("implementing"),
    CHECKING("checking"),
    REVIEWING("reviewing"),
    INTEGRATING("integrating"),
}

/** Where a wedge sits: at the rim, in one of the phases, or integrated. */
enum class WedgePlace { PLANNED, IMPLEMENTING, CHECKING, REVIEWING, INTEGRATING, INTEGRATED }

/**
 * A wedge's tone: plan at the rim, run while a step runs, mine while a held step runs, limited while a usage limit holds
 * a step that is not held, bad when the latest step ended otherwise, acc when it ended done, and ok when integrated.
 */
enum class WedgeTone { PLAN, RUN, MINE, LIMITED, BAD, ACC, OK }

/** A phase slice's state: passed, now (the phase the task is in), or ahead. */
enum class SliceState { PASSED, NOW, AHEAD }

/**
 * One phase of a wedge: its [state], the summary of its latest step, [since] the start of a now slice, and [seconds]
 * the ended steps of a passed slice took.
 */
data class PhaseSlice(val phase: Phase, val state: SliceState, val summary: String, val since: Long, val seconds: Long)

/** One task of an epic as a wedge: its [place] and [tone], and its four phase slices in [Phase] order. */
data class Wedge(
    val task: String,
    val title: String,
    val requirements: List<String>,
    val place: WedgePlace,
    val tone: WedgeTone,
    val slices: List<PhaseSlice>,
)

/** A part of a requirement's arc around the orrery, from [start] to [end] in wedge units, [proven] when it is. */
data class RequirementArc(val id: String, val start: Double, val end: Double, val proven: Boolean)

// A task as a wedge reads it: a build task, or a record task, which has no stage.
internal data class WedgeTask(
    val task: String,
    val title: String,
    val status: String,
    val stage: String,
    val requirements: List<String>,
)

// The build tasks when the epic has any, else the record's tasks.
internal fun tasksOf(detail: EpicDetail): List<WedgeTask> {
    if (detail.buildTasks.isNotEmpty()) {
        return detail.buildTasks.map { WedgeTask(it.id, it.title, it.status, it.stage, it.requirements) }
    }
    return detail.tasks.map { WedgeTask(it.slug, it.subject, it.status, "", it.requirements) }
}

// The stages whose running step is checking.
private val CHECK_STAGES = listOf("checks", "gate", "judge")

// The phase a step is in.
private fun phaseOf(step: StepRow): Phase = when {
    step.readOnly -> Phase.REVIEWING
    (step.end == "" && step.stage in CHECK_STAGES) || step.end == "checks_failed" || step.end == "goal_unmet" ->
        Phase.CHECKING
    else -> Phase.IMPLEMENTING
}

private fun Phase.toPlace(): WedgePlace = when (this) {
    Phase.IMPLEMENTING -> WedgePlace.IMPLEMENTING
    Phase.CHECKING -> WedgePlace.CHECKING
    Phase.REVIEWING -> WedgePlace.REVIEWING
    Phase.INTEGRATING -> WedgePlace.INTEGRATING
}

internal data class Placement(val place: WedgePlace, val tone: WedgeTone)

// The place of a task with no step, by its build stage. Null for a stage the rules do not name.
private fun stagePlacement(stage: String): Placement? = when (stage) {
    "red", "implement", "orchestrate" -> Placement(WedgePlace.IMPLEMENTING, WedgeTone.RUN)
    "green" -> Placement(WedgePlace.CHECKING, WedgeTone.RUN)
    "review", "verify" -> Placement(WedgePlace.REVIEWING, WedgeTone.RUN)
    else -> null
}

// The place of a task with no step and no build stage, by its lower-case status: dispatched, gated or gap, else
// planned at the rim.
private fun statusPlacement(status: String): Placement = when (status) {
    "dispatched" -> Placement(WedgePlace.IMPLEMENTING, WedgeTone.RUN)
    "gated" -> Placement(WedgePlace.INTEGRATING, WedgeTone.ACC)
    "gap" -> Placement(WedgePlace.IMPLEMENTING, WedgeTone.BAD)
    else -> Placement(WedgePlace.PLANNED, WedgeTone.PLAN)
}

// The tone of a running step: the owner's hold, else a usage limit, else running.
private fun runningToneOf(step: StepRow): WedgeTone = when {
    step.held -> WedgeTone.MINE
    step.limited -> WedgeTone.LIMITED
    else -> WedgeTone.RUN
}

// The place and tone of a task, by the first rule that matches.
internal fun placementOf(task: WedgeTask, steps: List<StepRow>): Placement {
    val status = task.status.lowercase()
    val latest = steps.lastOrNull()
    return when {
        status == "done" || status == "integrated" -> Placement(WedgePlace.INTEGRATED, WedgeTone.OK)
        latest != null && latest.end == "" ->
            Placement(phaseOf(latest).toPlace(), runningToneOf(latest))
        latest != null && latest.end == "done" -> Placement(WedgePlace.INTEGRATING, WedgeTone.ACC)
        latest != null -> Placement(phaseOf(latest).toPlace(), WedgeTone.BAD)
        else -> stagePlacement(task.stage) ?: statusPlacement(status)
    }
}

// The index of a place among the phases: -1 before the first phase, and the count of phases once integrated.
private fun reachedOf(place: WedgePlace): Int = when (place) {
    WedgePlace.PLANNED -> -1
    WedgePlace.IMPLEMENTING -> Phase.IMPLEMENTING.ordinal
    WedgePlace.CHECKING -> Phase.CHECKING.ordinal
    WedgePlace.REVIEWING -> Phase.REVIEWING.ordinal
    WedgePlace.INTEGRATING -> Phase.INTEGRATING.ordinal
    WedgePlace.INTEGRATED -> Phase.entries.size
}

// The state of the phase at index, given the index of the place's phase.
private fun stateOf(index: Int, reached: Int): SliceState = when {
    index < reached -> SliceState.PASSED
    index == reached -> SliceState.NOW
    else -> SliceState.AHEAD
}

// The summary of a phase's latest step: doing while that step runs.
private fun summaryOf(latest: StepRow?): String = when {
    latest == null -> ""
    latest.end == "" -> latest.doing
    else -> latest.summary
}

// The time a now slice has been in its phase: from the stage start of its running step, else from its start.
private fun sinceOf(latest: StepRow?): Long = when {
    latest == null -> 0L
    latest.end == "" -> latest.stageStarted
    else -> latest.started
}

// The time a phase's ended steps took, in seconds.
private fun secondsOf(steps: List<StepRow>): Long = steps.filter { it.end != "" }.sumOf { it.ended - it.started }

// The four phase slices of a wedge at place, each from the steps of its phase.
private fun slicesOf(place: WedgePlace, steps: List<StepRow>): List<PhaseSlice> {
    val reached = reachedOf(place)
    return Phase.entries.mapIndexed { index, phase ->
        val ofPhase = steps.filter { phaseOf(it) == phase }
        val latest = ofPhase.lastOrNull()
        val state = stateOf(index, reached)
        PhaseSlice(
            phase = phase,
            state = state,
            summary = summaryOf(latest),
            since = if (state == SliceState.NOW) sinceOf(latest) else 0L,
            seconds = if (state == SliceState.PASSED) secondsOf(ofPhase) else 0L,
        )
    }
}

/** The wedges of an epic, one per task, in task order. */
fun wedgesOf(detail: EpicDetail): List<Wedge> = tasksOf(detail).map { task ->
    val steps = detail.steps.filter { it.task == task.task }
    val placement = placementOf(task, steps)
    Wedge(
        task = task.task,
        title = task.title,
        requirements = task.requirements,
        place = placement.place,
        tone = placement.tone,
        slices = slicesOf(placement.place, steps),
    )
}

// Whether a requirement is proven: its own green run passed, or, with no requirement of that id, every wedge naming it
// is integrated.
internal fun provenOf(id: String, wedges: List<Wedge>, requirements: List<EpicRequirementRow>): Boolean {
    val requirement = requirements.find { it.id == id }
    if (requirement != null) {
        return requirement.runs.any { it.phase == "green" && it.passed }
    }
    return wedges.filter { id in it.requirements }.all { it.place == WedgePlace.INTEGRATED }
}

// The proven ids of an epic: every requirement's id and every wedge's requirement id that [provenOf] holds for.
internal fun provenIdsOf(requirements: List<EpicRequirementRow>, wedges: List<Wedge>): Set<String> {
    val ids = requirements.map { it.id } + wedges.flatMap { it.requirements }
    return ids.filter { provenOf(it, wedges, requirements) }.toSet()
}

/**
 * The arcs of the requirements around the wedges. Wedge i with k requirements gives part j the span from i + j/k to
 * i + (j+1)/k; a part continues the previous arc when it names the same requirement and starts where that arc ends.
 */
fun requirementArcs(wedges: List<Wedge>, requirements: List<EpicRequirementRow>): List<RequirementArc> {
    val arcs = mutableListOf<RequirementArc>()
    wedges.forEachIndexed { index, wedge ->
        val count = wedge.requirements.size
        wedge.requirements.forEachIndexed { part, id ->
            val start = index + part.toDouble() / count
            val end = index + (part + 1).toDouble() / count
            val previous = arcs.lastOrNull()
            if (previous != null && previous.id == id && previous.end == start) {
                arcs[arcs.lastIndex] = previous.copy(end = end)
            } else {
                arcs += RequirementArc(id, start, end, provenOf(id, wedges, requirements))
            }
        }
    }
    return arcs
}

/** A duration as text: seconds under a minute, whole minutes under an hour, then hours with any minutes left over. */
fun durationText(seconds: Long): String {
    if (seconds < 60) {
        return "$seconds s"
    }
    if (seconds < 3600) {
        return "${seconds / 60} min"
    }
    val hours = seconds / 3600
    val minutes = (seconds % 3600) / 60
    return if (minutes == 0L) "$hours h" else "$hours h $minutes min"
}
