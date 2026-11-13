package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicSliceRow

/**
 * What a piece of the Orbits chart shows, named by its tone; the drawing maps each tone to a colour. A cell takes
 * [PASSED], [NOW], [HELD], [ERRORED] or [TRACK]. [PLAIN] and [DIM] are for text only.
 */
enum class Tone { PASSED, NOW, HELD, ERRORED, TRACK, PLAIN, DIM }

/** A run of text in one [tone]. */
data class Span(val text: String, val tone: Tone)

/**
 * The epic's centre readout. [count] is the done count over the total, [caption] sits under it, and [tallies] are
 * the lines of running, held and gap counts, each a run of spans.
 */
data class EpicReadout(val count: List<Span>, val caption: Span, val tallies: List<List<Span>>)

/**
 * The centre readout for one selected task. [label] is drawn at [labelSize] units. [letters] are the five rings,
 * red test first, each in its cell's tone.
 */
data class TaskReadout(
    val position: Span,
    val label: Span,
    val labelSize: Double,
    val status: Span,
    val letters: List<Span>,
)

private const val RING_LETTERS = "RWTCG"
private val RING_NAMES = listOf("red test", "working", "tests pass", "checks", "gate accepted")
private const val SEPARATOR = " · "
private const val ELLIPSIS = "…"
private const val LABEL_CUT_OVER = 26
private const val LABEL_CUT_TO = 25

/**
 * The five cells of a slice, red test first. The first of these that applies decides a cell: now or held when
 * `at` names its ring, errored when `errorAt` does, passed when the ring's boolean is true, and track otherwise.
 * Serve's fields are the only input.
 */
fun cellTones(slice: EpicSliceRow): List<Tone> {
    val passed = listOf(slice.redTest, slice.working, slice.testsPass, slice.checks, slice.gateAccepted)
    return passed.mapIndexed { index, ringPassed ->
        val ring = index + 1
        when {
            slice.at == ring -> if (slice.held) Tone.HELD else Tone.NOW
            slice.errorAt == ring -> Tone.ERRORED
            ringPassed -> Tone.PASSED
            else -> Tone.TRACK
        }
    }
}

/**
 * The epic's centre readout with no task selected. [recordTasks] is how many tasks the epic's record lists; when it
 * lists more than there are slices, the caption says the count is of tracked tasks.
 */
fun epicReadout(slices: List<EpicSliceRow>, recordTasks: Int): EpicReadout {
    val counts = countsOf(slices)
    val tallies = buildList {
        if (counts.running > 0) add(Span("${counts.running} running", Tone.NOW))
        if (counts.held > 0) add(Span("${counts.held} held", Tone.HELD))
        if (counts.gap > 0) add(Span("${counts.gap} gap", Tone.ERRORED))
    }
    val lines = when {
        tallies.isEmpty() -> emptyList()
        tallies.size == 3 -> listOf(tallies.take(2), tallies.drop(2))
        else -> listOf(tallies)
    }
    return EpicReadout(
        count = listOf(Span(counts.done.toString(), Tone.PLAIN), Span("/${slices.size}", Tone.DIM)),
        caption = Span(if (recordTasks > slices.size) "tracked tasks done" else "tasks done", Tone.DIM),
        tallies = lines.map { withSeparators(it) },
    )
}

/** The centre readout for the slice at [index], which the caller selected from [slices]. */
fun taskReadout(slices: List<EpicSliceRow>, index: Int): TaskReadout {
    val slice = slices[index]
    val label = if (slice.task.length > LABEL_CUT_OVER) slice.task.take(LABEL_CUT_TO) + ELLIPSIS else slice.task
    val cells = cellTones(slice)
    return TaskReadout(
        position = Span("${index + 1} of ${slices.size}", Tone.DIM),
        label = Span(label, Tone.PLAIN),
        labelSize = (146.0 / (0.6 * label.length)).coerceIn(9.0, 22.0),
        status = statusOf(slice),
        letters = RING_LETTERS.mapIndexed { ring, letter -> Span(letter.toString(), cells[ring]) },
    )
}

/** The chart's content description, read aloud for the chart. */
fun contentDescription(slices: List<EpicSliceRow>): String {
    val counts = countsOf(slices)
    return "${slices.size} tasks: ${counts.done} done, ${counts.running} running, " +
        "${counts.held} held, ${counts.gap} gap"
}

private class Counts(val done: Int, val running: Int, val held: Int, val gap: Int)

private fun countsOf(slices: List<EpicSliceRow>) = Counts(
    done = slices.count { it.hue == "done" },
    running = slices.count { it.hue == "dispatched" && !it.held },
    held = slices.count { it.held },
    gap = slices.count { it.hue == "gap" },
)

private fun statusOf(slice: EpicSliceRow): Span = when {
    slice.hue == "done" -> Span("done", Tone.PASSED)
    slice.hue == "dispatched" && slice.held -> Span(ringed("held", slice.at), Tone.HELD)
    slice.hue == "dispatched" -> Span(ringed("running", slice.at), Tone.NOW)
    else -> Span(ringed("gap", slice.errorAt), Tone.ERRORED)
}

/** "{word} · {ring name}", or the word alone when [ring] is not one of the five rings. */
private fun ringed(word: String, ring: Int): String {
    val name = RING_NAMES.getOrNull(ring - 1) ?: return word
    return word + SEPARATOR + name
}

private fun withSeparators(spans: List<Span>): List<Span> = buildList {
    spans.forEachIndexed { index, span ->
        if (index > 0) add(Span(SEPARATOR, Tone.DIM))
        add(span)
    }
}
