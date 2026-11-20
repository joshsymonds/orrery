package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicContractRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicHistoryRow
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.StepRow
import java.time.Instant
import java.time.ZoneId

/** The tone of a word on the record: live, attention when it asks, quiet, the engine's, ok, or bad. */
enum class WordTone { LIVE, ATTENTION, QUIET, ENGINE, OK, BAD }

/** A track stage's state: done, now, or ahead. */
enum class StageState { DONE, NOW, AHEAD }

/** One stage of an orreryd epic's track: its [name], its [state], its [tone] and its [words]. */
data class Stage(val name: String, val state: StageState, val tone: WordTone, val words: String)

/** A word on the record with its [tone]: a title-row pill, or a stage's words. */
data class Pill(val word: String, val tone: WordTone)

/** Which sections of the record an epic reaches. */
data class Reached(
    val summary: Boolean,
    val track: Boolean,
    val now: Boolean,
    val history: Boolean,
    val tasks: Boolean,
    val requirements: Boolean,
)

/** What asks of the owner under Now: an open question, the owner's park, the planner's park, or a limit. */
sealed interface Asking {
    data class Question(val question: EpicQuestionRow) : Asking

    data class Park(val reason: String) : Asking

    data class Planner(val line: String) : Asking

    data object Limited : Asking
}

/** A running step's state glyph: working, held by the owner, or waiting on a usage limit. */
enum class StepGlyph { WORKING, HELD, LIMITED }

/** One running step as a row of Now: its [glyph], [label], [task], what it [doing], and the unix second it [since]. */
data class StepLine(
    val id: String,
    val glyph: StepGlyph,
    val label: String,
    val task: String,
    val doing: String,
    val since: Long,
)

private const val ORREYD = "orreryd"

private val STAGE_NAMES = listOf("Contract", "Build", "Review", "Release", "Report")
private const val CONTRACT = 0
private const val BUILD = 1
private const val REVIEW = 2
private const val RELEASE = 3
private const val REPORT = 4

/** Whether the owner holds the epic's park: a park the planner does not hold. */
fun ownerParked(detail: EpicDetail): Boolean = detail.parked.isNotEmpty() && !detail.plannerParked

/** Whether the planner holds the epic's park. */
fun plannerParked(detail: EpicDetail): Boolean = detail.plannerParked

/** Whether at least one step runs and every running step is waiting on a usage limit. */
fun allLimited(detail: EpicDetail): Boolean {
    val steps = runningOf(detail)
    return steps.isNotEmpty() && steps.all { it.limited }
}

/** The track of an orreryd epic: five stages, each done, now or ahead. Empty for a record epic. */
fun trackOf(detail: EpicDetail): List<Stage> {
    if (!isOrreryd(detail)) {
        return emptyList()
    }
    val now = stageNowOf(detail.epic.outcome)
    return STAGE_NAMES.mapIndexed { index, name ->
        when {
            index < now -> Stage(name, StageState.DONE, WordTone.OK, doneWordsOf(index, detail))
            index == now -> nowStageOf(index, detail)
            else -> Stage(name, StageState.AHEAD, WordTone.QUIET, "not reached")
        }
    }
}

/** The title row's state pill: the epic's state word and tone, with how long its phase has lasted. */
fun statePillOf(detail: EpicDetail, now: Long): Pill {
    val state = stateWordOf(detail)
    return Pill(stateLineOf(state.word, detail.epic.phaseAt, now), state.tone)
}

/** The blocker pill, when something asks: an open question, the owner's park, or a step the owner held. */
fun blockerPillOf(detail: EpicDetail, now: Long): Pill? {
    val question = detail.questions.firstOrNull()
    return when {
        question != null -> Pill(stateLineOf("asks you a question", question.asked, now), WordTone.ATTENTION)
        ownerParked(detail) -> Pill("asks you what to do", WordTone.ATTENTION)
        else -> runningOf(detail).firstOrNull { it.held }?.let { Pill("${labelOf(it)} held", WordTone.ATTENTION) }
    }
}

/** The Contract door's line: when the contract was accepted, its care, and its requirements. Empty with no contract. */
fun contractDoorOf(detail: EpicDetail): String {
    val contract = detail.contract ?: return ""
    return "${acceptedWordsOf(contract)} · ${contract.care} care · ${detail.requirements.size} requirements"
}

/** The sections of the record an epic reaches. A record epic reaches only its Now and its tasks. */
fun reachedOf(detail: EpicDetail, history: List<EpicHistoryRow>): Reached {
    val orreryd = isOrreryd(detail)
    val title = detail.contract?.title.orEmpty()
    return Reached(
        summary = orreryd && detail.summary.ifEmpty { title }.isNotEmpty(),
        track = orreryd,
        now = true,
        history = orreryd && history.isNotEmpty(),
        tasks = wedgesOf(detail).isNotEmpty(),
        requirements = orreryd && detail.requirements.isNotEmpty(),
    )
}

/** The one sentence Now opens with: the first state that holds. */
fun nowSentenceOf(detail: EpicDetail): String {
    val steps = runningOf(detail)
    return when {
        ownerParked(detail) -> "Parked. Nothing runs until you answer."
        detail.paused -> "Paused. The steps that were running finish; nothing new starts until you resume."
        detail.plannerParked -> "The planner holds a park and is deciding what to do. Nothing is asked of you."
        allLimited(detail) -> limitedSentenceOf(steps)
        detail.questions.isNotEmpty() -> questionSentenceOf(steps)
        isOrreryd(detail) && stageNowOf(detail.epic.outcome) == REPORT -> "It ended: ${stateWordOf(detail).word}."
        isOrreryd(detail) && wedgesOf(detail).isEmpty() ->
            "The planner is reading the contract and outlining tasks. No task has started."
        else -> runningSentenceOf(steps)
    }
}

/** What asks of the owner, first match: an open question, the owner's park, the planner's park, then a limit. */
fun askingOf(detail: EpicDetail): Asking? {
    val question = detail.questions.firstOrNull()
    return when {
        question != null -> Asking.Question(question)
        ownerParked(detail) -> Asking.Park(plainReasonOf(detail.parked))
        detail.plannerParked ->
            Asking.Planner("The planner holds this park: ${detail.parked}. It will retry, replan, or ask you.")
        allLimited(detail) -> Asking.Limited
        else -> null
    }
}

/** The running steps of the epic as rows of Now, in the order they ran. */
fun stepLinesOf(detail: EpicDetail): List<StepLine> = runningOf(detail).map { step ->
    StepLine(
        id = step.id,
        glyph = when {
            step.held -> StepGlyph.HELD
            step.limited -> StepGlyph.LIMITED
            else -> StepGlyph.WORKING
        },
        label = labelOf(step),
        task = step.task,
        doing = doingOf(step),
        since = if (step.stageStarted != 0L) step.stageStarted else step.started,
    )
}

private fun isOrreryd(detail: EpicDetail): Boolean = detail.epic.source == ORREYD

private fun runningOf(detail: EpicDetail): List<StepRow> = detail.steps.filter { it.end.isEmpty() }

private fun asks(detail: EpicDetail): Boolean = detail.questions.isNotEmpty() || ownerParked(detail)

private fun labelOf(step: StepRow): String = step.task.ifEmpty { step.agent }

// stageNowOf is the track's stage an outcome is in.
private fun stageNowOf(outcome: String): Int = when (outcome) {
    "accepted", "built" -> BUILD
    "reviewing", "correcting" -> REVIEW
    "clean", "releasing", "stopped" -> RELEASE
    "released", "done", "met_at_base", "ended_with_gaps", "closed_superseded", "closed_shipped", "closed_abandoned" ->
        REPORT
    else -> CONTRACT
}

// nowStageOf is the stage the epic is in, with its words and tone.
private fun nowStageOf(index: Int, detail: EpicDetail): Stage {
    val pill = when (index) {
        CONTRACT -> Pill(detail.epic.outcome, WordTone.LIVE)
        BUILD -> Pill(buildWordsOf(detail, now = true), buildToneOf(detail))
        REVIEW -> Pill(if (detail.epic.outcome == "correcting") "correcting" else "reviewing", WordTone.LIVE)
        RELEASE -> releaseNowOf(detail.epic.outcome)
        else -> stateWordOf(detail)
    }
    return Stage(STAGE_NAMES[index], StageState.NOW, pill.tone, pill.word)
}

// doneWordsOf is a stage's words once it is done. The report is the last stage and is never done.
private fun doneWordsOf(index: Int, detail: EpicDetail): String = when (index) {
    CONTRACT -> acceptedWordsOf(detail.contract)
    BUILD -> buildWordsOf(detail, now = false)
    REVIEW -> "clean"
    RELEASE -> "released"
    else -> ""
}

// buildWordsOf is the build stage's count of done tasks. While it is now, it says what asks, pauses or waits on a
// limit; with no task yet it says only that the planner is outlining.
private fun buildWordsOf(detail: EpicDetail, now: Boolean): String {
    val wedges = wedgesOf(detail)
    if (now && wedges.isEmpty()) {
        return "the planner is outlining tasks"
    }
    val head = "${wedges.count { it.place == WedgePlace.INTEGRATED }} of ${wedges.size} tasks done"
    if (!now) {
        return head
    }
    val suffixes = listOfNotNull(
        if (asks(detail)) "1 waits on you" else null,
        if (detail.paused) "paused" else null,
        if (runningOf(detail).any { it.limited }) "waiting on a limit" else null,
    )
    return suffixes.fold(head) { words, suffix -> "$words · $suffix" }
}

private fun buildToneOf(detail: EpicDetail): WordTone = when {
    asks(detail) -> WordTone.ATTENTION
    detail.paused || allLimited(detail) -> WordTone.QUIET
    else -> WordTone.LIVE
}

private fun releaseNowOf(outcome: String): Pill = when (outcome) {
    "clean" -> Pill("ready to release", WordTone.QUIET)
    "releasing" -> Pill("releasing", WordTone.ENGINE)
    else -> Pill("stopped", WordTone.BAD)
}

// stateWordOf is the epic's state word and tone before any duration, the title row's word for an orreryd epic, and the
// record's own state word for a record epic.
private fun stateWordOf(detail: EpicDetail): Pill {
    if (!isOrreryd(detail)) {
        return Pill(epicStateOf(detail), WordTone.QUIET)
    }
    if (detail.paused) {
        return Pill("paused", WordTone.QUIET)
    }
    if (ownerParked(detail)) {
        return Pill("parked", WordTone.ATTENTION)
    }
    val live = if (allLimited(detail)) WordTone.QUIET else WordTone.LIVE
    return when (detail.epic.outcome) {
        "accepted" -> Pill(if (wedgesOf(detail).isEmpty()) "planning" else "building", live)
        "built" -> Pill("building", live)
        "reviewing", "correcting" -> Pill("reviewing", live)
        "clean" -> Pill("ready to release", WordTone.QUIET)
        "releasing" -> Pill("releasing", WordTone.ENGINE)
        "stopped" -> Pill("stopped", WordTone.BAD)
        "released", "done" -> Pill(detail.epic.outcome, WordTone.OK)
        "closed_shipped" -> Pill("shipped outside orrery", WordTone.OK)
        "met_at_base" -> Pill("already met", WordTone.QUIET)
        "ended_with_gaps" -> Pill("ended with gaps", WordTone.ATTENTION)
        "closed_superseded" -> Pill("superseded", WordTone.QUIET)
        "closed_abandoned" -> Pill("abandoned", WordTone.QUIET)
        else -> Pill(detail.epic.outcome, WordTone.QUIET)
    }
}

// acceptedWordsOf is the contract's acceptance as the track and the Contract door write it: with its time, or bare when
// the time is unknown.
private fun acceptedWordsOf(contract: EpicContractRow?): String =
    if (contract == null || contract.accepted <= 0L) "accepted" else "accepted ${clockTimeOf(contract.accepted)}"

// limitedSentenceOf is the Now sentence while every running step waits on a usage limit, by the first of them.
private fun limitedSentenceOf(steps: List<StepRow>): String {
    val first = steps.first()
    val kind = if (first.limitKind.isEmpty()) "a usage limit" else "the ${first.limitKind} limit"
    val reset = if (first.limitResets > 0L) ", which resets at ${clockTimeOf(first.limitResets)}" else ""
    return if (steps.size == 1) {
        "1 step waits on $kind$reset. It resumes on its own."
    } else {
        "${steps.size} steps wait on $kind$reset. They resume on their own."
    }
}

private fun questionSentenceOf(steps: List<StepRow>): String = when (val tasks = runningTasksOf(steps)) {
    0 -> "It waits on your answer."
    1 -> "One task is being worked on, and one waits on you."
    else -> "$tasks tasks are being worked on, and one waits on you."
}

private fun runningSentenceOf(steps: List<StepRow>): String = when (val tasks = runningTasksOf(steps)) {
    0 -> "Nothing is running."
    1 -> "One task is being worked on."
    else -> "$tasks tasks are being worked on."
}

// runningTasksOf counts the distinct tasks that have a running step.
private fun runningTasksOf(steps: List<StepRow>): Int = steps.map { it.task }.filter { it.isNotEmpty() }.distinct().size

// plainReasonOf is a park's reason as a sentence: its first letter capitalised, with a final full stop.
private fun plainReasonOf(reason: String): String {
    val sentence = reason.replaceFirstChar { it.uppercaseChar() }
    return if (sentence.endsWith(".")) sentence else "$sentence."
}

private fun doingOf(step: StepRow): String = when {
    step.held -> if (step.reason.isEmpty()) "held" else "held · ${step.reason}"
    step.limited -> limitWaitOf(step)
    else -> step.doing
}

private fun limitWaitOf(step: StepRow): String {
    val wait = if (step.limitKind.isEmpty()) "waits on a usage limit" else "waits on the ${step.limitKind} limit"
    return if (step.limitResets > 0L) "$wait · resets ${clockTimeOf(step.limitResets)}" else wait
}

// clockTimeOf is a unix second as the phone's clock shows it, 24-hour, in the system zone.
private fun clockTimeOf(time: Long): String =
    Instant.ofEpochSecond(time).atZone(ZoneId.systemDefault()).format(CLOCK_TIME)
