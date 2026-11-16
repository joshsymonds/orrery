package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepCheckRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.api.StepVerdictRow
import io.github.joshsymonds.orrery.api.TaskReviewRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// A task's phase output over synthetic epic details: its own steps, runs, reviews and rounds, and its rows.
class PhaseRowsTest {
    // A labelled step of task T1, writing unless readOnly, with the given checks and verdicts.
    private fun step(
        id: String,
        task: String = "T1",
        end: String = "",
        readOnly: Boolean = false,
        started: Long = 100,
        ended: Long = 0,
        checks: List<StepCheckRow> = emptyList(),
        gateName: String = "",
        verdicts: List<StepVerdictRow> = emptyList(),
    ): StepRow = StepRow(
        id = id,
        task = task,
        agent = "builder",
        end = end,
        status = "",
        held = false,
        started = started,
        ended = ended,
        doing = "",
        checks = checks,
        reason = "",
        readOnly = readOnly,
        gateName = gateName,
        verdicts = verdicts,
    )

    // A check of a round, with the detail and redacted tails the round's check carries.
    private fun check(
        round: Int,
        name: String,
        ok: Boolean,
        detail: String,
        tailStdout: String = "",
        tailStderr: String = "",
    ): StepCheckRow = StepCheckRow(
        round = round,
        name = name,
        ok = ok,
        stdout = "",
        stderr = "",
        detail = detail,
        tailStdout = tailStdout,
        tailStderr = tailStderr,
    )

    private fun run(id: String, phase: String): EvidenceRunRow = EvidenceRunRow(
        id = id,
        phase = phase,
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "",
        judgeReason = "",
        passed = true,
        revision = "",
        created = 0,
        ended = 0,
        logBytes = 0,
        stderrBytes = 0,
        artifacts = emptyList(),
    )

    private fun review(step: String, verdict: String): TaskReviewRow = TaskReviewRow(
        attempt = 1L,
        gate = "review",
        step = step,
        verdict = verdict,
        findings = emptyList(),
    )

    private fun buildTask(
        id: String,
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
        reviews: List<TaskReviewRow> = emptyList(),
        status: String = "open",
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = id,
        title = "Parser $id",
        status = status,
        stage = "",
        red = red,
        green = green,
        requirements = listOf("R1"),
        reviews = reviews,
    )

    private fun detail(
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        tasks: List<EpicTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/example-epic",
            repository = "example-repo",
            slug = "example-epic",
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
        ),
        tasks = tasks,
        slices = emptyList(),
        steps = steps,
        buildTasks = buildTasks,
    )

    @Test
    fun interleavedTasksEachOutputNamesOnlyItsOwnStepsRunsReviewsAndRounds() {
        val epic = detail(
            buildTasks = listOf(
                buildTask("T1", red = run("red-a", "red"), green = run("green-a", "green"), reviews = listOf(review("r-a", "holds"))),
                buildTask("T2", red = run("red-b", "red"), reviews = listOf(review("r-b", "fails"))),
            ),
            steps = listOf(
                step("w-a1", task = "T1", end = "returned", ended = 130, checks = listOf(check(1, "lint", true, "lint of T1"))),
                step("w-b1", task = "T2", end = "returned", ended = 140, checks = listOf(check(1, "lint", false, "lint of T2"))),
                step("r-a", task = "T1", readOnly = true, end = "done", started = 150, ended = 170),
                step("r-b", task = "T2", readOnly = true, end = "done", started = 160, ended = 180),
                step("w-b3", task = "T2", end = "", started = 190),
            ),
        )

        val first = phaseOutputOf(epic, "T1")!!
        assertEquals("T1", first.task)
        assertEquals("Parser T1", first.title)
        assertEquals(listOf("w-a1"), first.attempts.map { it.step })
        assertEquals(listOf("r-a"), first.reviewers.map { it.step })
        assertEquals(listOf("w-a1"), first.rounds.map { it.step })
        assertEquals(listOf("lint of T1"), first.rounds.single().checks.map { it.detail })
        assertEquals("red-a", first.red?.id)
        assertEquals("green-a", first.green?.id)
        assertEquals(listOf("r-a"), first.reviews.map { it.step })
        assertNull(first.running)

        val second = phaseOutputOf(epic, "T2")!!
        assertEquals("T2", second.task)
        assertEquals(listOf("w-b1", "w-b3"), second.attempts.map { it.step })
        assertEquals(listOf("r-b"), second.reviewers.map { it.step })
        assertEquals(listOf("w-b1"), second.rounds.map { it.step })
        assertEquals(listOf("lint of T2"), second.rounds.single().checks.map { it.detail })
        assertEquals(false, second.rounds.single().checks.single().ok)
        assertEquals("red-b", second.red?.id)
        assertNull(second.green)
        assertEquals(listOf("r-b"), second.reviews.map { it.step })
        assertEquals("fails", second.reviews.single().verdict)
        assertEquals("w-b3", second.running?.id)
    }

    @Test
    fun aRoundsChecksGateAndJudgeAreItsOwnAndRanNamesWhatTheRoundHas() {
        val epic = detail(
            buildTasks = listOf(buildTask("T1")),
            steps = listOf(
                step(
                    "s1",
                    end = "returned",
                    ended = 130,
                    gateName = "gradle test",
                    checks = listOf(
                        check(1, "fmt", true, "fmt round 1"),
                        check(1, "gate", false, "gate round 1", tailStdout = "out 1", tailStderr = "err 1"),
                        check(2, "fmt", true, "fmt round 2"),
                        check(2, "gate", true, "gate round 2", tailStdout = "out 2", tailStderr = "err 2"),
                    ),
                    verdicts = listOf(StepVerdictRow(round = 2, verdict = "unmet", reason = "reason 2")),
                ),
            ),
        )

        val rounds = phaseOutputOf(epic, "T1")!!.rounds
        assertEquals(2, rounds.size)
        assertEquals(
            CheckRound(
                step = "s1",
                round = 1,
                checks = listOf(CheckLine("fmt", true, "fmt round 1")),
                gate = GateLine("gradle test", false, "gate round 1", "out 1", "err 1"),
                judge = null,
                ran = listOf("checks", "gate"),
            ),
            rounds[0],
        )
        assertEquals(
            CheckRound(
                step = "s1",
                round = 2,
                checks = listOf(CheckLine("fmt", true, "fmt round 2")),
                gate = GateLine("gradle test", true, "gate round 2", "out 2", "err 2"),
                judge = JudgeLine("unmet", "reason 2"),
                ran = listOf("checks", "gate", "judge"),
            ),
            rounds[1],
        )
    }

    @Test
    fun eachPhaseOpensTheLatestWritingStepForImplementingAndCheckingTheLatestReadingForReviewingAndNoneForIntegrating() {
        val epic = detail(
            buildTasks = listOf(buildTask("T1"), buildTask("T2", status = "done"), buildTask("T3")),
            steps = listOf(
                step("w1", task = "T1", end = "returned", ended = 130),
                step("r1", task = "T1", readOnly = true, end = "done", started = 140, ended = 160),
                step("w2", task = "T1", end = "", started = 170),
                step("w3", task = "T2", end = "done", started = 100, ended = 150),
                step("w4", task = "T3", end = "returned", ended = 130),
                step("r3", task = "T3", readOnly = true, end = "", started = 140),
            ),
        )

        val first = phaseOutputOf(epic, "T1")!!
        assertEquals(listOf("w2", "w2", "r1", ""), first.rows.map { it.session })
        assertEquals("w2", first.running?.id)

        val integrated = phaseOutputOf(epic, "T2")!!
        assertEquals(listOf("w3", "w3", "", ""), integrated.rows.map { it.session })
        assertEquals(true, integrated.integrated)
        assertNull(integrated.running)

        val reviewing = phaseOutputOf(epic, "T3")!!
        assertEquals(listOf("w4", "w4", "r3", ""), reviewing.rows.map { it.session })
        assertEquals(listOf(SliceState.PASSED, SliceState.PASSED, SliceState.NOW, SliceState.AHEAD), reviewing.rows.map { it.state })
        assertEquals(false, reviewing.integrated)
        assertEquals("r3", reviewing.running?.id)
    }

    @Test
    fun aRecordTaskHasNoRunsOrReviewsAndKeepsItsSteps() {
        val epic = detail(
            tasks = listOf(
                EpicTaskRow(effort = 1, slug = "first", subject = "First task", status = "done", requirements = listOf("R2")),
            ),
            steps = listOf(
                step("w-first", task = "first", end = "done", ended = 130),
                step("r-first", task = "first", readOnly = true, end = "done", started = 140, ended = 160),
            ),
        )

        val output = phaseOutputOf(epic, "first")!!
        assertEquals("First task", output.title)
        assertEquals(listOf("R2"), output.requirements)
        assertNull(output.red)
        assertNull(output.green)
        assertEquals(emptyList<TaskReviewRow>(), output.reviews)
        assertEquals(listOf("w-first"), output.attempts.map { it.step })
        assertEquals(listOf("r-first"), output.reviewers.map { it.step })
    }

    @Test
    fun anUnknownTaskGivesNull() {
        val epic = detail(
            buildTasks = listOf(buildTask("T1")),
            tasks = listOf(EpicTaskRow(effort = 1, slug = "old", subject = "Old", status = "done", requirements = emptyList())),
            steps = listOf(step("w1", task = "T1", end = "returned", ended = 130)),
        )

        assertNull(phaseOutputOf(epic, "T9"))
        assertNull(phaseOutputOf(epic, "old"))
    }
}
