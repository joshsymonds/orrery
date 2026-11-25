package io.github.joshsymonds.orrery.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens

// SHOWN_TASKS is how many task rows show before the fold line opens the rest.
private const val SHOWN_TASKS = 12

/**
 * The epic's tasks as rows: each task's name, the requirements it proves, its pips, its words and, while a step runs in
 * it, how long that stage has run as of [now]. A row whose task is in a phase opens that phase when tapped, through
 * [onOpenPhase]. More than twelve tasks show twelve and a fold line naming the rest, which opens them all.
 */
@Composable
fun TaskTable(tasks: List<RecordTask>, now: Long, onOpenPhase: ((task: String, phase: Phase) -> Unit)? = null) {
    var showAll by remember { mutableStateOf(false) }
    val shown = if (showAll) tasks else tasks.take(SHOWN_TASKS)
    Column(modifier = Modifier.fillMaxWidth()) {
        shown.forEach { task ->
            val phase = phaseOfPlace(task.place, task.integrated)
            val onTap: (() -> Unit)? = if (onOpenPhase != null && phase != null) {
                ({ onOpenPhase(task.task, phase) })
            } else {
                null
            }
            TaskRow(task = task, now = now, onTap = onTap)
        }
        if (!showAll && tasks.size > SHOWN_TASKS) {
            TextButton(onClick = { showAll = true }, modifier = Modifier.testTag("task-fold")) {
                Text("${tasks.size - SHOWN_TASKS} more tasks")
            }
        }
    }
}

/**
 * The running steps of an epic as rows: each glyph names its state, then the label (the step's purpose, else its task,
 * else its role), what the step does, the pips of the task in [tasks] the step belongs to by [taskOfStep] when it has a
 * task (five not reached when none does), and how long its stage has run as of [now].
 */
@Composable
fun StepRows(lines: List<StepLine>, tasks: List<RecordTask>, now: Long) {
    Column(modifier = Modifier.fillMaxWidth()) {
        lines.forEach { line ->
            StepLineRow(line = line, cells = stepCellsOf(line.task, tasks), now = now)
        }
    }
}

/**
 * The five cells of the task among [tasks] a step labelled [task] belongs to by [taskOfStep], five not reached when none
 * does.
 */
internal fun stepCellsOf(task: String, tasks: List<RecordTask>): List<CellTone> =
    tasks.taskOfStep(task)?.cells ?: RingPlace.entries.map { CellTone.NONE }

/** Elapsed seconds as m:ss, or h:mm:ss from an hour. Never negative. */
internal fun elapsedClock(seconds: Long): String {
    val total = seconds.coerceAtLeast(0L)
    val hours = total / 3_600
    val minutes = total % 3_600 / 60
    val secs = total % 60
    return if (hours > 0) {
        "%d:%02d:%02d".format(hours, minutes, secs)
    } else {
        "%d:%02d".format(minutes, secs)
    }
}

@Composable
private fun TaskRow(task: RecordTask, now: Long, onTap: (() -> Unit)?) {
    val tapped = if (onTap == null) Modifier else Modifier.clickable(onClick = onTap)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("task-row:${task.task}")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four)
            .then(tapped),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            task.title,
            modifier = Modifier.weight(1.2f),
            style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(task.requirements.joinToString(" "), style = OrreryTokens.Type.small)
        Pips(cells = task.cells)
        Text(
            task.words,
            modifier = Modifier.weight(1f).testTag("task-words:${task.task}"),
            style = OrreryTokens.Type.small,
        )
        if (task.since > 0) {
            Text(
                elapsedClock(now - task.since),
                modifier = Modifier.testTag("task-clock:${task.task}"),
                style = OrreryTokens.Type.small,
            )
        }
    }
}

@Composable
private fun StepLineRow(line: StepLine, cells: List<CellTone>, now: Long) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("step-row:${line.id}")
            .padding(horizontal = OrreryTokens.Space.sixteen, vertical = OrreryTokens.Space.four),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(OrreryTokens.Space.eight),
    ) {
        Text(
            AnnotatedString("●", SpanStyle(color = glyphColourOf(line.glyph))),
            modifier = Modifier
                .testTag("step-glyph:${line.id}")
                .semantics { stateDescription = glyphWordOf(line.glyph) },
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                line.label,
                style = OrreryTokens.Type.small.copy(fontWeight = OrreryTokens.Font.Weight.bold),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                AnnotatedString(line.doing, SpanStyle(color = doingColourOf(line.glyph))),
                modifier = Modifier.testTag("step-doing:${line.id}"),
                style = OrreryTokens.Type.small,
            )
        }
        if (line.task.isNotEmpty()) {
            Pips(cells = cells)
        }
        if (line.since > 0) {
            Text(
                elapsedClock(now - line.since),
                modifier = Modifier.testTag("step-clock:${line.id}"),
                style = OrreryTokens.Type.small,
            )
        }
    }
}

// A step's glyph colour: held is peach, limited grey, working the live yellow.
private fun glyphColourOf(glyph: StepGlyph): Color = when (glyph) {
    StepGlyph.HELD -> OrreryTokens.Tone.mine
    StepGlyph.LIMITED -> OrreryTokens.Ink.three
    StepGlyph.WORKING -> OrreryTokens.Role.live
}

// A step's doing colour: held is attention, limited grey, working the quiet secondary ink.
private fun doingColourOf(glyph: StepGlyph): Color = when (glyph) {
    StepGlyph.HELD -> OrreryTokens.Role.attention
    StepGlyph.LIMITED -> OrreryTokens.Ink.three
    StepGlyph.WORKING -> OrreryTokens.Ink.two
}

// A step's state as a screen reader names it.
private fun glyphWordOf(glyph: StepGlyph): String = when (glyph) {
    StepGlyph.HELD -> "held"
    StepGlyph.LIMITED -> "limited"
    StepGlyph.WORKING -> "working"
}
