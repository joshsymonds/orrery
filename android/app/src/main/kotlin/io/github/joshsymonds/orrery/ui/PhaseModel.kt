package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
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
 * A wedge's tone, as serve computes it for the task: plan while planned, run while it runs or retries, mine while the owner
 * holds it, limited while a usage limit holds its step, bad for a gap or a task its ended epic left short, acc while it is
 * accepted and waits to integrate, and ok once integrated.
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

// A task as a wedge reads it: a build task, with the place and tone serve computed for it and the step labels that
// belong to it.
internal data class WedgeTask(
    val task: String,
    val title: String,
    val requirements: List<String>,
    val labels: Set<String>,
    val place: String,
    val tone: String,
)

// The epic's build tasks.
internal fun tasksOf(detail: EpicDetail): List<WedgeTask> =
    detail.buildTasks.map { WedgeTask(it.id, it.title, it.requirements, labelsOf(it), it.place, it.tone) }

// The prefix of the label an evidence judge step carries, before its run's id.
private const val EVIDENCE_LABEL = "evidence-"

// The step labels that belong to a build task: its id, and the evidence label of its red run and of its green run.
private fun labelsOf(task: EpicBuildTaskRow): Set<String> =
    setOf(task.id) + listOfNotNull(task.red, task.green).map { EVIDENCE_LABEL + it.id }

// The stages whose running step is checking.
private val CHECK_STAGES = listOf("checks", "gate", "judge")

// The phase a step is in.
private fun phaseOf(step: StepRow): Phase = when {
    step.readOnly -> Phase.REVIEWING
    (step.end == "" && step.stage in CHECK_STAGES) || step.end == "checks_failed" || step.end == "goal_unmet" ->
        Phase.CHECKING
    else -> Phase.IMPLEMENTING
}

// The wedge place of a task's place as serve names it: red and implement are implementing, green checking, review
// reviewing, integrate integrating. A place serve does not name, or none, reads as planned, the reading that claims no
// progress.
internal fun wedgePlaceOf(place: String): WedgePlace = when (place) {
    "red", "implement" -> WedgePlace.IMPLEMENTING
    "green" -> WedgePlace.CHECKING
    "review" -> WedgePlace.REVIEWING
    "integrate" -> WedgePlace.INTEGRATING
    "integrated" -> WedgePlace.INTEGRATED
    else -> WedgePlace.PLANNED
}

// The wedge tone of a task's tone as serve names it. A tone serve does not name, or none, reads as plan, the reading
// that claims nothing about the task's work.
internal fun wedgeToneOf(tone: String): WedgeTone = when (tone) {
    "run" -> WedgeTone.RUN
    "mine" -> WedgeTone.MINE
    "limited" -> WedgeTone.LIMITED
    "bad" -> WedgeTone.BAD
    "acc" -> WedgeTone.ACC
    "ok" -> WedgeTone.OK
    else -> WedgeTone.PLAN
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

/**
 * The wedges of an epic, one per task, in task order: each at the place and in the tone serve computed for its task, its
 * phase slices from the steps its task's id labels.
 */
fun wedgesOf(detail: EpicDetail): List<Wedge> = tasksOf(detail).map { task ->
    val place = wedgePlaceOf(task.place)
    Wedge(
        task = task.task,
        title = task.title,
        requirements = task.requirements,
        place = place,
        tone = wedgeToneOf(task.tone),
        slices = slicesOf(place, detail.steps.filter { it.task == task.task }),
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
