package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicSliceRow
import org.junit.Assert.assertEquals
import org.junit.Test

class OrbitModelTest {
    // Section 2's examples, each as the table lists serve's fields. An expected string reads the cells red test to gate
    // accepted: Y now, P held, R errored, G passed, . track.
    @Test
    fun everySection2ExampleShowsItsFiveCells() {
        val examples = listOf(
            Triple("running, just started", row("W", at = 1), "YG..."),
            Triple("running, red evidence given", row("RW", at = 3), "GGY.."),
            Triple("running, tests pass", row("RWT", at = 4), "GGGY."),
            Triple("running, checks passed", row("RWTC", at = 5), "GGGGY"),
            Triple("running, green evidence but no red", row("WT", at = 1), "YGG.."),
            Triple("held after tests pass", row("RWT", at = 4, held = true), "GGGP."),
            Triple("held, just started", row("W", at = 1, held = true), "PG..."),
            Triple("running, nothing reached", row("", at = 1), "Y...."),
            Triple("ended done", row("RWTCG", hue = "done"), "GGGGG"),
            Triple("ended done, checks not evidenced", row("RWTG", hue = "done"), "GGG.G"),
            Triple("ended checks_failed after tests pass", row("RWT", hue = "gap", errorAt = 4), "GGGR."),
            Triple("ended checks_failed, no green evidence", row("RW", hue = "gap", errorAt = 4), "GG.R."),
            Triple("ended goal_unmet", row("RWTC", hue = "gap", errorAt = 5), "GGGGR"),
            Triple("ended returned", row("RW", hue = "gap", errorAt = 3), "GGR.."),
            Triple("ended failed, no red test", row("W", hue = "gap", errorAt = 1), "RG..."),
            Triple("ended, nothing reached", row("", hue = "gap", errorAt = 1), "R...."),
        )
        for ((name, slice, expected) in examples) {
            assertEquals(name, expected, codes(cellTones(slice)))
        }
    }

    @Test
    fun atBeatsErrorAtAndEachBeatsATrueBooleanOnItsRing() {
        assertEquals("at over a true boolean", "GGY..", codes(cellTones(row("RWT", at = 3))))
        assertEquals("errorAt over a true boolean", "GGR..", codes(cellTones(row("RWT", errorAt = 3))))
        assertEquals("at over errorAt", "GGY..", codes(cellTones(row("RWT", at = 3, errorAt = 3))))
        assertEquals("held at over a true boolean", "GGP..", codes(cellTones(row("RWT", at = 3, held = true))))
    }

    @Test
    fun synthetic35ReadsElevenOfThirtyFive() {
        val slices = synthetic35()
        val readout = epicReadout(slices, recordTasks = 35)
        assertEquals("11/35", readout.count.text())
        assertEquals(listOf(Tone.PLAIN, Tone.DIM), readout.count.map { it.tone })
        assertEquals("tasks done", readout.caption.text)
        assertEquals(listOf("3 running · 1 held", "20 gap"), readout.tallies.map { it.text() })
        assertEquals("35 tasks: 11 done, 3 running, 1 held, 20 gap", contentDescription(slices))
    }

    @Test
    fun captionCountsTheRecordOnlyWhenItListsMoreTasksThanSlices() {
        val slices = synthetic35()
        assertEquals("tasks done", epicReadout(slices, recordTasks = 35).caption.text)
        assertEquals("tracked tasks done", epicReadout(slices, recordTasks = 40).caption.text)
    }

    @Test
    fun zeroCountsAreLeftOutAndTwoTalliesShareALine() {
        val done = epicReadout(listOf(slice("a", "RWTCG", "done")), recordTasks = 1)
        assertEquals("1/1", done.count.text())
        assertEquals(emptyList<List<Span>>(), done.tallies)

        val running = listOf(
            slice("a", "W", "dispatched"),
            slice("b", "", "gap", end = "failed"),
            slice("c", "", "gap", end = "failed"),
        )
        assertEquals(listOf("1 running · 2 gap"), epicReadout(running, recordTasks = 3).tallies.map { it.text() })
    }

    @Test
    fun aRunningTaskReadsItsRingAndItsCells() {
        val readout = taskReadout(synthetic35(), 32)
        assertEquals("33 of 35", readout.position.text)
        assertEquals("t33", readout.label.text)
        assertEquals(22.0, readout.labelSize, 0.0)
        assertEquals("running · checks", readout.status.text)
        assertEquals(Tone.NOW, readout.status.tone)
        assertEquals("RWTCG", readout.letters.text())
        assertEquals("GGGY.", codes(readout.letters.map { it.tone }))
    }

    @Test
    fun aHeldTaskReadsWhereItWaits() {
        val readout = taskReadout(synthetic35(), 34)
        assertEquals("35 of 35", readout.position.text)
        assertEquals("held · checks", readout.status.text)
        assertEquals(Tone.HELD, readout.status.tone)
        assertEquals("GGGP.", codes(readout.letters.map { it.tone }))
    }

    @Test
    fun aGapTaskReadsWhereItErrored() {
        val checksFailed = taskReadout(synthetic35(), 5)
        assertEquals("gap · checks", checksFailed.status.text)
        assertEquals(Tone.ERRORED, checksFailed.status.tone)
        assertEquals("GGGR.", codes(checksFailed.letters.map { it.tone }))

        val failedWithNoRed = taskReadout(synthetic35(), 9)
        assertEquals("gap · red test", failedWithNoRed.status.text)
        assertEquals("RG...", codes(failedWithNoRed.letters.map { it.tone }))
    }

    @Test
    fun aDoneTaskReadsDone() {
        val readout = taskReadout(synthetic35(), 11)
        assertEquals("done", readout.status.text)
        assertEquals(Tone.PASSED, readout.status.tone)
        assertEquals("GGGGG", codes(readout.letters.map { it.tone }))
    }

    @Test
    fun aLabelOfTwentySixCharactersIsKeptWhole() {
        val label = "abcdefghijklmnopqrstuvwxyz"
        val readout = taskReadout(listOf(slice(label, "RWTCG", "done")), 0)
        assertEquals(label, readout.label.text)
        assertEquals(146.0 / (0.6 * 26), readout.labelSize, 1e-9)
    }

    @Test
    fun aLabelOfTwentySevenCharactersIsCutToTwentyFiveAndAnEllipsis() {
        val readout = taskReadout(listOf(slice("abcdefghijklmnopqrstuvwxyz0", "RWTCG", "done")), 0)
        assertEquals("abcdefghijklmnopqrstuvwxy…", readout.label.text)
        assertEquals(146.0 / (0.6 * 26), readout.labelSize, 1e-9)
    }

    // A slice as serve sends it: [passed] lists the rings whose booleans are true, RWTCG letters in any order.
    private fun row(
        passed: String,
        hue: String = "dispatched",
        at: Int = 0,
        held: Boolean = false,
        errorAt: Int = 0,
    ): EpicSliceRow {
        val r = reached(passed)
        return EpicSliceRow(
            task = "t",
            hue = hue,
            redTest = r[0],
            working = r[1],
            testsPass = r[2],
            checks = r[3],
            gateAccepted = r[4],
            at = at,
            held = held,
            errorAt = errorAt,
        )
    }

    private fun reached(pattern: String): List<Boolean> = "RWTCG".map { pattern.contains(it) }

    private fun codes(tones: List<Tone>): String = tones.joinToString("") {
        when (it) {
            Tone.NOW -> "Y"
            Tone.PASSED -> "G"
            Tone.HELD -> "P"
            Tone.ERRORED -> "R"
            Tone.TRACK -> "."
            Tone.PLAIN, Tone.DIM -> "?"
        }
    }

    private fun List<Span>.text(): String = joinToString("") { it.text }

    // Ported from synthetic35() in chart-options.html. The slice fields are derived the way serve derives them: at is
    // the first ring not reached on a live task, and errorAt is set for a gap by the end it ended with.
    private fun slice(
        task: String,
        pattern: String,
        hue: String,
        end: String = "",
        held: Boolean = false,
    ): EpicSliceRow {
        val r = reached(pattern)
        val first = r.indexOf(false) + 1
        val live = hue == "dispatched"
        val errorAt = when {
            hue != "gap" -> 0
            end == "checks_failed" -> 4
            end == "goal_unmet" -> 5
            else -> first
        }
        return EpicSliceRow(
            task = task,
            hue = hue,
            redTest = r[0],
            working = r[1],
            testsPass = r[2],
            checks = r[3],
            gateAccepted = r[4],
            at = if (live) first else 0,
            held = live && held,
            errorAt = errorAt,
        )
    }

    private fun synthetic35(): List<EpicSliceRow> {
        val out = mutableListOf<EpicSliceRow>()
        fun put(from: Int, to: Int, pattern: String, hue: String, end: String = "", held: Boolean = false) {
            for (n in from..to) out.add(slice("t" + n.toString().padStart(2, '0'), pattern, hue, end, held))
        }
        put(1, 3, "RW___", "gap", "returned")
        put(4, 5, "RW___", "gap", "checks_failed")
        put(6, 9, "RWT__", "gap", "checks_failed")
        put(10, 11, "_W___", "gap", "failed")
        put(12, 20, "RWTCG", "done")
        put(21, 22, "RWT_G", "done")
        put(23, 26, "RWT__", "gap", "checks_failed")
        put(27, 29, "RWTC_", "gap", "goal_unmet")
        put(30, 31, "_W___", "gap", "stopped")
        put(32, 32, "_W___", "dispatched")
        put(33, 34, "RWT__", "dispatched")
        put(35, 35, "RWT__", "dispatched", held = true)
        return out
    }
}
