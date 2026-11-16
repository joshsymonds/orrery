package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRequirementRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Test

// The words of an epic's line, over synthetic details at a synthetic clock.
class EpicLineTest {
    private val now = 100_000L

    private fun epic(live: Boolean = true, phaseAt: Long = 0L) = EpicRow(
        id = "example-repo/example-epic",
        repository = "example-repo",
        slug = "example-epic",
        outcome = "",
        changed = 0L,
        live = live,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
        phaseAt = phaseAt,
    )

    private fun run(phase: String, passed: Boolean) = EvidenceRunRow(
        id = "run-$phase",
        phase = phase,
        source = "captured",
        state = "ended",
        exit = 0,
        timedOut = false,
        notCaptured = "",
        verdict = "",
        judgeReason = "",
        passed = passed,
        revision = "",
        created = 0,
        ended = 0,
        logBytes = 0,
        stderrBytes = 0,
        artifacts = emptyList(),
    )

    private fun requirement(id: String, vararg runs: EvidenceRunRow) = EpicRequirementRow(
        id = id,
        text = "",
        evidence = "",
        kind = "check",
        runs = runs.toList(),
    )

    private fun task(status: String) = EpicTaskRow(effort = 1, slug = "task", subject = "", status = status)

    private fun buildTask(id: String, status: String) = EpicBuildTaskRow(
        id = id,
        title = "",
        status = status,
        stage = "",
        red = null,
        green = null,
    )

    private fun question(asked: Long) = EpicQuestionRow(id = "Q1", text = "Which branch?", turn = 1L, asked = asked)

    private fun detail(
        epic: EpicRow = epic(),
        tasks: List<EpicTaskRow> = emptyList(),
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        requirements: List<EpicRequirementRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        parked: String = "",
    ): EpicDetail = EpicDetail(
        epic = epic,
        tasks = tasks,
        slices = emptyList(),
        steps = emptyList<StepRow>(),
        requirements = requirements,
        buildTasks = buildTasks,
        questions = questions,
        parked = parked,
    )

    @Test
    fun aCountOverRequirementsIsTheProvenOnesOfAll() {
        val d = detail(
            requirements = listOf(
                requirement("R1", run("green", true)),
                requirement("R2", run("red", true), run("green", true)),
                requirement("R3", run("red", true)),
            ),
            tasks = listOf(task("done")),
        )
        assertEquals("2 of 3 proven", epicLineOf(d, now).count)
    }

    @Test
    fun aCountWithNoRequirementsIsTheDoneTasksIntegratedOnesIncluded() {
        val d = detail(tasks = listOf(task("done"), task("Integrated"), task("gated"), task("open")))
        assertEquals("2 of 4 tasks done", epicLineOf(d, now).count)
    }

    @Test
    fun aStateIsTheTitleRowsStateThenTheIntegratedUnitsOfItsWedges() {
        val d = detail(
            epic = epic(phaseAt = now - 2_940L),
            buildTasks = listOf(
                buildTask("T1", "done"),
                buildTask("T2", "done"),
                buildTask("T3", "open"),
                buildTask("T4", "open"),
                buildTask("T5", "open"),
                buildTask("T6", "open"),
                buildTask("T7", "open"),
            ),
        )
        assertEquals("building · 49 min · 2 of 7 units integrated", epicLineOf(d, now).state)
    }

    @Test
    fun aStateWithNoWedgesIsTheTitleRowsStateAlone() {
        val d = detail(epic = epic(phaseAt = now - 2_940L))
        assertEquals("planning · 49 min", epicLineOf(d, now).state)
    }

    @Test
    fun aStateWithNoStateWordIsTheUnitsAlone() {
        val d = detail(
            epic = epic(live = false),
            buildTasks = listOf(buildTask("T1", "done"), buildTask("T2", "open")),
        )
        assertEquals("1 of 2 units integrated", epicLineOf(d, now).state)
    }

    @Test
    fun anOpenQuestionIsTheBlockerWithItsWait() {
        val d = detail(questions = listOf(question(asked = now - 120L)))
        assertEquals("waits on your answer · 2 min", epicLineOf(d, now).blocker)
    }

    @Test
    fun anOpenQuestionNeverLeavesItsBlockerEmpty() {
        val d = detail(questions = listOf(question(asked = 0L)))
        assertEquals("waits on your answer", epicLineOf(d, now).blocker)
    }

    @Test
    fun aParkIsTheBlockerWithItsReason() {
        val d = detail(parked = "rate limit")
        assertEquals("parked · rate limit", epicLineOf(d, now).blocker)
    }

    @Test
    fun anEpicWithNothingToWaitOnHasNoBlocker() {
        assertEquals("", epicLineOf(detail(), now).blocker)
    }

    @Test
    fun aNullDetailReadsEpicUnavailableWithTheRestEmpty() {
        assertEquals(EpicLine(count = "epic unavailable", state = "", blocker = ""), epicLineOf(null, now))
    }
}
