package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StepVerdictRow
import io.github.joshsymonds.orrery.api.TaskFindingRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import org.junit.Assert.assertEquals
import org.junit.Test

// The five stage rows of a task, over synthetic epic values at a fixed now. The words are SPEC section 4's.
class TaskStagesModelTest {
    private val now = 10_000L

    private fun step(
        id: String = "s",
        readOnly: Boolean = false,
        end: String = "",
        stage: String = "turn",
        stageStarted: Long = 0L,
        started: Long = 100L,
        ended: Long = 0L,
        held: Boolean = false,
        limited: Boolean = false,
        checks: List<StepCheckRow> = emptyList(),
        verdicts: List<StepVerdictRow> = emptyList(),
    ): StepRow = StepRow(
        id = id,
        task = TASK,
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = started,
        ended = ended,
        doing = "",
        checks = checks,
        reason = "",
        stage = stage,
        stageStarted = stageStarted,
        limited = limited,
        readOnly = readOnly,
        verdicts = verdicts,
    )

    private fun check(round: Int, name: String, ok: Boolean): StepCheckRow =
        StepCheckRow(round = round, name = name, ok = ok, stdout = "", stderr = "")

    private fun verdict(round: Int, verdict: String): StepVerdictRow = StepVerdictRow(round, verdict, "")

    private fun run(
        state: String = "ended",
        passed: Boolean = true,
        verdict: String = "",
        created: Long = 0L,
        ended: Long = 0L,
    ): EvidenceRunRow = EvidenceRunRow(
        id = "run",
        phase = "red",
        source = "captured",
        state = state,
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = verdict,
        judgeReason = "",
        passed = passed,
        revision = "",
        created = created,
        ended = ended,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )

    private fun finding(result: String): TaskFindingRow =
        TaskFindingRow(id = "f", claim = "claim", location = "here", step = "", result = result, reason = "")

    private fun review(gate: String, verdict: String = "holds", findings: List<TaskFindingRow> = emptyList()): TaskReviewRow =
        TaskReviewRow(attempt = 1, gate = gate, step = "review", verdict = verdict, findings = findings)

    // The build task at the place and tone serve computed for it, on its build stage; planned unless the arguments say
    // otherwise.
    private fun build(
        place: String = "planned",
        tone: String = "plan",
        stage: String = "",
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
        reviews: List<TaskReviewRow> = emptyList(),
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = TASK,
        title = "Parser",
        status = if (place == "integrated") "done" else "open",
        stage = stage,
        red = red,
        green = green,
        requirements = listOf("R1"),
        reviews = reviews,
        place = place,
        tone = tone,
    )

    private fun detail(build: EpicBuildTaskRow, steps: List<StepRow> = emptyList()): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
        ),
        tasks = emptyList(),
        steps = steps,
        buildTasks = listOf(build),
    )

    private fun rows(detail: EpicDetail): List<StageRow> = stagesOf(detail, TASK, now)

    private fun statesOf(detail: EpicDetail): List<SliceState> = rows(detail).map { it.state }

    private fun summaryOf(detail: EpicDetail, ring: RingPlace): String = rows(detail).first { it.place == ring }.summary

    private fun timeOf(detail: EpicDetail, ring: RingPlace): String = rows(detail).first { it.place == ring }.time

    @Test
    fun `the five rows come in place order, red, implement, green, review, integrate`() {
        assertEquals(
            listOf("red", "implement", "green", "review", "integrate"),
            rows(detail(build(place = "red", tone = "run", stage = "red"))).map { it.place.word },
        )
    }

    @Test
    fun `a planned task has every row ahead, and no row open`() {
        val rows = rows(detail(build()))
        assertEquals(List(5) { SliceState.AHEAD }, rows.map { it.state })
        assertEquals(emptyList<RingPlace>(), rows.filter { it.open }.map { it.place })
    }

    @Test
    fun `a task at each place passes the rows before it, has its place now, and leaves the rest ahead`() {
        assertEquals(
            listOf(SliceState.NOW, SliceState.AHEAD, SliceState.AHEAD, SliceState.AHEAD, SliceState.AHEAD),
            statesOf(detail(build(place = "red", tone = "run", stage = "red"))),
        )
        assertEquals(
            listOf(SliceState.PASSED, SliceState.NOW, SliceState.AHEAD, SliceState.AHEAD, SliceState.AHEAD),
            statesOf(detail(build(place = "implement", tone = "run", stage = "implement"))),
        )
        assertEquals(
            listOf(SliceState.PASSED, SliceState.PASSED, SliceState.NOW, SliceState.AHEAD, SliceState.AHEAD),
            statesOf(detail(build(place = "green", tone = "run", stage = "green"))),
        )
        assertEquals(
            listOf(SliceState.PASSED, SliceState.PASSED, SliceState.PASSED, SliceState.NOW, SliceState.AHEAD),
            statesOf(detail(build(place = "review", tone = "run", stage = "review"))),
        )
        assertEquals(
            listOf(SliceState.PASSED, SliceState.PASSED, SliceState.PASSED, SliceState.PASSED, SliceState.NOW),
            statesOf(detail(build(place = "integrate", tone = "acc"))),
        )
    }

    @Test
    fun `an integrated task has every row passed, and the integrate row open`() {
        val rows = rows(detail(build(place = "integrated", tone = "ok")))
        assertEquals(List(5) { SliceState.PASSED }, rows.map { it.state })
        assertEquals(listOf(RingPlace.INTEGRATE), rows.filter { it.open }.map { it.place })
    }

    @Test
    fun `the record opens on the now row, and no other row`() {
        assertEquals(listOf(RingPlace.IMPLEMENT), rows(detail(build(place = "implement", tone = "run", stage = "implement"))).filter { it.open }.map { it.place })
    }

    @Test
    fun `an ahead row reads not reached and shows no time`() {
        val rows = rows(detail(build(place = "green", tone = "run", stage = "green", green = run(created = 1_000L, ended = 1_030L))))
        assertEquals(listOf("not reached", "not reached"), rows.drop(3).map { it.summary })
        assertEquals(listOf("", ""), rows.drop(3).map { it.time })
    }

    @Test
    fun `the now row carries the task's tone, run, mine, limited, bad or acc`() {
        val cases = listOf(
            WedgeTone.RUN to detail(build("implement", "run"), listOf(step(end = ""))),
            WedgeTone.MINE to detail(build("implement", "mine"), listOf(step(end = "", held = true))),
            WedgeTone.LIMITED to detail(build("implement", "limited"), listOf(step(end = "", limited = true))),
            WedgeTone.BAD to detail(build("green", "bad"), listOf(step(end = "checks_failed", stage = "checks", ended = 200L))),
            WedgeTone.ACC to detail(build("integrate", "acc"), listOf(step(end = "done", stage = "gate", ended = 200L))),
        )
        cases.forEach { (tone, detail) ->
            assertEquals(tone, rows(detail).single { it.state == SliceState.NOW }.tone)
        }
    }

    @Test
    fun `the red words name the red run until it ends, then whether the test fails before any change`() {
        val cases = listOf(
            "capturing the failing test" to run(state = "capturing"),
            "capturing the failing test" to null,
            "a judge is reading the red run" to run(state = "judging"),
            "the test fails before any change" to run(passed = true),
            "the test passes before any change" to run(passed = false),
            "the test fails before any change · judged to hold" to run(passed = true, verdict = "holds"),
        )
        cases.forEach { (words, red) ->
            assertEquals(words, summaryOf(detail(build(place = "implement", tone = "run", stage = "implement", red = red)), RingPlace.RED))
        }
    }

    @Test
    fun `the green words name the green run until it ends, then whether the test passes on the change`() {
        val cases = listOf(
            "capturing the green run" to run(state = "capturing"),
            "a judge is reading the green run" to run(state = "judging"),
            "the test passes on the change" to run(passed = true),
            "the test still fails on the change" to run(passed = false),
            "the test passes on the change · judged to hold" to run(passed = true, verdict = "holds"),
        )
        cases.forEach { (words, green) ->
            assertEquals(words, summaryOf(detail(build(place = "review", tone = "run", stage = "review", green = green)), RingPlace.GREEN))
        }
    }

    @Test
    fun `the implement words say an orchestrator decides when the build stage is orchestrate`() {
        assertEquals(
            "an orchestrator is deciding on the implementer’s request",
            summaryOf(detail(build(place = "implement", tone = "run", stage = "orchestrate")), RingPlace.IMPLEMENT),
        )
    }

    @Test
    fun `the implement words are not started with no implementer step`() {
        assertEquals("not started", summaryOf(detail(build(place = "implement", tone = "run", stage = "implement")), RingPlace.IMPLEMENT))
    }

    @Test
    fun `the implement words count attempts and say what the latest implementer step is doing`() {
        val cases = listOf(
            "attempt 1 · the worker is writing" to listOf(step(stage = "turn")),
            "attempt 1 · held" to listOf(step(stage = "turn", held = true)),
            "attempt 1 · waits on a usage limit" to listOf(step(stage = "turn", limited = true)),
            "attempt 1 · round 1 passed · the checks are running" to listOf(
                step(stage = "checks", checks = listOf(check(1, "tests", true), check(1, "lint", true))),
            ),
            "attempt 1 · round 1 failed · the checks are running" to listOf(
                step(stage = "checks", checks = listOf(check(1, "tests", true), check(1, "lint", false))),
            ),
            "attempt 1 · round 1 passed · the gate is running" to listOf(
                step(stage = "gate", checks = listOf(check(1, "tests", true), check(1, "gate", true))),
            ),
            "attempt 1 · the gate passed · judged met" to listOf(
                step(end = "done", stage = "judge", ended = 200L, checks = listOf(check(1, "gate", true)), verdicts = listOf(verdict(1, "met"))),
            ),
            "attempt 1" to listOf(
                step(end = "done", stage = "judge", ended = 200L, checks = listOf(check(1, "gate", false)), verdicts = listOf(verdict(1, "unmet"))),
            ),
            "attempt 1 · ended: stopped" to listOf(step(end = "stopped", ended = 200L)),
            "attempt 2 · the worker is writing" to listOf(step(id = "a", end = "stopped", ended = 200L), step(id = "b", stage = "turn")),
        )
        cases.forEach { (words, steps) ->
            assertEquals(words, summaryOf(detail(build(place = "implement", tone = "run", stage = "implement"), steps), RingPlace.IMPLEMENT))
        }
    }

    @Test
    fun `the review words say a verifier checks the open findings when the build stage is verify`() {
        val cases = listOf(
            "a verifier is checking 2 findings" to listOf(finding(""), finding(""), finding("confirmed")),
            "a verifier is checking 1 finding" to listOf(finding(""), finding("dropped")),
            "a verifier is checking its findings" to listOf(finding("confirmed")),
        )
        cases.forEach { (words, findings) ->
            val reviews = listOf(review("lint", "fails", findings))
            assertEquals(words, summaryOf(detail(build(place = "review", tone = "run", stage = "verify", reviews = reviews)), RingPlace.REVIEW))
        }
    }

    @Test
    fun `the review words say the gates read the change with no review, else each review in order`() {
        assertEquals(
            "the review gates are reading the change",
            summaryOf(detail(build(place = "review", tone = "run", stage = "review")), RingPlace.REVIEW),
        )
        val reviews = listOf(
            review("tests"),
            review("lint", "fails", listOf(finding("confirmed"), finding("dropped"))),
            review("types", "fails", listOf(finding("dropped"))),
        )
        assertEquals(
            "tests holds · lint: 2 findings, 1 confirmed · types: 1 finding",
            summaryOf(detail(build(place = "review", tone = "run", stage = "review", reviews = reviews)), RingPlace.REVIEW),
        )
    }

    @Test
    fun `the integrate words say merged once integrated, and accepted while it waits for its merge`() {
        assertEquals("merged", summaryOf(detail(build(place = "integrated", tone = "ok")), RingPlace.INTEGRATE))
        assertEquals(
            "accepted · waits for its merge",
            summaryOf(detail(build(place = "integrate", tone = "acc")), RingPlace.INTEGRATE),
        )
    }

    @Test
    fun `a passed row shows its stage's duration, from the runs or the implementer and reviewer steps`() {
        val steps = listOf(
            step(id = "a", end = "checks_failed", started = 100L, ended = 200L),
            step(id = "b", end = "done", started = 300L, ended = 500L),
            step(id = "c", readOnly = true, started = 600L),
        )
        val detail = detail(
            build(
                place = "review",
                tone = "run",
                stage = "review",
                red = run(created = 1_000L, ended = 1_125L),
                green = run(created = 2_000L, ended = 2_030L),
            ),
            steps,
        )
        assertEquals("2 min", timeOf(detail, RingPlace.RED))
        assertEquals("6 min", timeOf(detail, RingPlace.IMPLEMENT))
        assertEquals("30 s", timeOf(detail, RingPlace.GREEN))
    }

    @Test
    fun `a passed review row shows its time from its first reviewer's start to its last end`() {
        val steps = listOf(
            step(id = "c", readOnly = true, end = "done", started = 600L, ended = 700L),
            step(id = "d", readOnly = true, end = "done", started = 800L, ended = 900L),
        )
        assertEquals("5 min", timeOf(detail(build(place = "integrate", tone = "acc"), steps), RingPlace.REVIEW))
    }

    @Test
    fun `a passed row with no data or zero seconds shows no time`() {
        assertEquals("", timeOf(detail(build(place = "implement", tone = "run", stage = "implement", red = run(created = 1_000L, ended = 1_000L))), RingPlace.RED))
        assertEquals("", timeOf(detail(build(place = "implement", tone = "run", stage = "implement")), RingPlace.RED))
        assertEquals("", timeOf(detail(build(place = "integrated", tone = "ok")), RingPlace.INTEGRATE))
    }

    @Test
    fun `the now row shows the running step's elapsed clock since its stage started, else since its start`() {
        val sinceStage = detail(build("implement", "run"), listOf(step(end = "", stageStarted = 9_000L, started = 100L)))
        assertEquals("16:40", timeOf(sinceStage, RingPlace.IMPLEMENT))
        val sinceStart = detail(build("implement", "run"), listOf(step(end = "", started = 100L)))
        assertEquals("2:45:00", timeOf(sinceStart, RingPlace.IMPLEMENT))
    }

    @Test
    fun `the now row shows no clock when no step runs`() {
        assertEquals("", timeOf(detail(build(place = "integrate", tone = "acc")), RingPlace.INTEGRATE))
    }

    private companion object {
        const val TASK = "T1"
        const val EPIC = "synthetic-epic"
    }
}
