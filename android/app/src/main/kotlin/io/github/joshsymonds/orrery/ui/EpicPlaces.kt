package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

/** The five places a task goes through, outside in: its ring on the chart, and its cell in the pips. */
enum class RingPlace(val word: String) {
    RED("red"),
    IMPLEMENT("implement"),
    GREEN("green"),
    REVIEW("review"),
    INTEGRATE("integrate"),
}

/** One cell of a task: none before or past its place, else the tone of the task at its place. */
enum class CellTone { NONE, PLAN, RUN, MINE, LIMITED, BAD, ACC, OK }

/**
 * A task of an epic at the place serve computed for it: [place] is its ring, null while planned, and [integrated] is true
 * once it is integrated. [labels] are the step labels that belong to it: its id, and for a build task the evidence label
 * of its red and green runs. [cells] are its five cells outside in, [words] its words in the task table, and [since]
 * when its running step's stage started, 0 when none runs.
 */
data class RecordTask(
    val task: String,
    val title: String,
    val requirements: List<String>,
    val labels: Set<String>,
    val place: RingPlace?,
    val integrated: Boolean,
    val tone: WedgeTone,
    val cells: List<CellTone>,
    val words: String,
    val since: Long,
)

/**
 * The record's tasks, one per wedge of [wedgesOf] in the same order, each with the place and tone serve computed for it,
 * its cells and its words. Its running step is its latest unended step among the steps its labels name.
 */
fun recordTasksOf(detail: EpicDetail): List<RecordTask> = tasksOf(detail).map { task ->
    val integrated = task.place == INTEGRATED
    val place = ringPlaceOf(task.place)
    val tone = wedgeToneOf(task.tone)
    val running = detail.steps.lastOrNull { it.task in task.labels && it.end == "" }
    RecordTask(
        task = task.task,
        title = task.title,
        requirements = task.requirements,
        labels = task.labels,
        place = place,
        integrated = integrated,
        tone = tone,
        cells = cellsOf(place, integrated, tone),
        words = wordsOf(place, integrated, tone, running),
        since = running?.let { if (it.stageStarted != 0L) it.stageStarted else it.started } ?: 0L,
    )
}

/**
 * The task a step labelled [label] belongs to: the task whose id the label is, or, for an evidence judge step labelled
 * evidence-<run id>, the build task whose red or green run has that id. Null when no task claims the label.
 */
fun List<RecordTask>.taskOfStep(label: String): RecordTask? = find { label in it.labels }

// The place serve names once the integrate run that claims a task passed.
private const val INTEGRATED = "integrated"

// The ring of a task's place as serve names it: integrate for integrate and integrated. Planned, or a place serve does
// not name, has none, the reading that claims no progress.
private fun ringPlaceOf(place: String): RingPlace? =
    if (place == INTEGRATED) RingPlace.INTEGRATE else RingPlace.entries.find { it.word == place }

/**
 * A task's five cells, outside in: integrated is five ok, planned is five none, else the cells before its place are ok, its
 * place's cell is its tone, and the cells after it are none.
 */
fun cellsOf(place: RingPlace?, integrated: Boolean, tone: WedgeTone): List<CellTone> = when {
    integrated -> RingPlace.entries.map { CellTone.OK }
    place == null -> RingPlace.entries.map { CellTone.NONE }
    else -> RingPlace.entries.map { ring ->
        when {
            ring.ordinal < place.ordinal -> CellTone.OK
            ring == place -> cellOf(tone)
            else -> CellTone.NONE
        }
    }
}

// The cell a task's tone draws at its own place.
private fun cellOf(tone: WedgeTone): CellTone = when (tone) {
    WedgeTone.PLAN -> CellTone.PLAN
    WedgeTone.RUN -> CellTone.RUN
    WedgeTone.MINE -> CellTone.MINE
    WedgeTone.LIMITED -> CellTone.LIMITED
    WedgeTone.BAD -> CellTone.BAD
    WedgeTone.ACC -> CellTone.ACC
    WedgeTone.OK -> CellTone.OK
}

/** The phase a place is in, null while planned. An integrated task is in integrating. */
fun phaseOfPlace(place: RingPlace?, integrated: Boolean): Phase? = when {
    integrated -> Phase.INTEGRATING
    else -> when (place) {
        null -> null
        RingPlace.RED, RingPlace.IMPLEMENT -> Phase.IMPLEMENTING
        RingPlace.GREEN -> Phase.CHECKING
        RingPlace.REVIEW -> Phase.REVIEWING
        RingPlace.INTEGRATE -> Phase.INTEGRATING
    }
}

// A task's words in the task table's now column.
private fun wordsOf(place: RingPlace?, integrated: Boolean, tone: WedgeTone, running: StepRow?): String = when {
    place == null -> "not started"
    integrated -> "done"
    place == RingPlace.INTEGRATE && tone == WedgeTone.ACC -> "accepted · waits to integrate"
    tone == WedgeTone.LIMITED -> limitWordsOf(running)
    tone == WedgeTone.MINE -> "${place.word} · held"
    tone == WedgeTone.BAD -> "${place.word} · failed"
    running != null && running.doing.isNotEmpty() -> "${place.word} · ${running.doing}"
    else -> place.word
}

// A limited task's words: the usage limit its running step waits on, named by its kind when it has one.
private fun limitWordsOf(running: StepRow?): String {
    val kind = running?.limitKind.orEmpty()
    return if (kind.isEmpty()) "waits on a usage limit" else "waits on the $kind limit"
}

// A cell's words, as the pips name it to a screen reader after its ring.
private fun cellWordOf(cell: CellTone): String = when (cell) {
    CellTone.NONE -> "not reached"
    CellTone.PLAN -> "planned"
    CellTone.RUN -> "running"
    CellTone.MINE -> "held"
    CellTone.LIMITED -> "waiting on a limit"
    CellTone.BAD -> "failed"
    CellTone.ACC -> "accepted"
    CellTone.OK -> "passed"
}

// A cell's colour: ok, running, held, accepted and failed as the tones are, a limit or plan in grey, and none in the
// line's soft colour.
internal fun cellColourOf(cell: CellTone): Color = when (cell) {
    CellTone.OK -> OrreryTokens.Role.ok
    CellTone.RUN -> OrreryTokens.Tone.run
    CellTone.MINE -> OrreryTokens.Tone.mine
    CellTone.LIMITED, CellTone.PLAN -> OrreryTokens.Ink.three
    CellTone.BAD -> OrreryTokens.Role.bad
    CellTone.ACC -> OrreryTokens.Tone.acc
    CellTone.NONE -> OrreryTokens.Line.soft
}

/** A task's five [cells] as five small rounded bars, outside in, named ring by ring in words. */
@Composable
fun Pips(cells: List<CellTone>, modifier: Modifier = Modifier) {
    val description = RingPlace.entries.zip(cells) { ring, cell -> "${ring.word} ${cellWordOf(cell)}" }.joinToString(", ")
    Row(
        modifier = modifier
            .testTag("pips")
            .semantics { contentDescription = description },
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.two),
    ) {
        cells.forEach { cell ->
            Box(
                modifier = Modifier
                    .width(OrreryTokens.Space.twelve)
                    .height(OrreryTokens.Space.four)
                    .clip(RoundedCornerShape(OrreryTokens.Radius.one))
                    .background(cellColourOf(cell)),
            )
        }
    }
}
