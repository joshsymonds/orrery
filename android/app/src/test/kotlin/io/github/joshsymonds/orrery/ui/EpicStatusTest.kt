package io.github.joshsymonds.orrery.ui

import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.StepRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class EpicStatusTest {
    @Test
    fun outcomeIsTheStateAsWrittenAheadOfEveryOtherState() {
        val d = detail(epic = epic(outcome = "released", live = true, waiting = true), paused = true, parked = "budget")
        assertEquals("released", epicStateOf(d))
    }

    @Test
    fun pausedIsAheadOfParkedWaitingAndLive() {
        val d = detail(epic = epic(live = true, waiting = true), paused = true, parked = "budget")
        assertEquals("paused", epicStateOf(d))
    }

    @Test
    fun parkedIsAheadOfWaitingAndLive() {
        val d = detail(epic = epic(live = true, waiting = true), parked = "budget")
        assertEquals("parked", epicStateOf(d))
    }

    @Test
    fun waitingIsAheadOfLive() {
        assertEquals("waiting", epicStateOf(detail(epic = epic(live = true, waiting = true))))
    }

    @Test
    fun aLiveEpicWithNoTasksAndNoBuildTasksIsPlanning() {
        assertEquals("planning", epicStateOf(detail(epic = epic(live = true))))
    }

    @Test
    fun aLiveEpicWithATaskOrAPlannedBuildTaskIsBuilding() {
        val withTask = detail(epic = epic(live = true), tasks = listOf(EpicTaskRow(1, "t1", "", "")))
        assertEquals("building", epicStateOf(withTask))
        val withBuildTask = detail(epic = epic(live = true), buildTasks = listOf(buildTask("b1")))
        assertEquals("building", epicStateOf(withBuildTask))
    }

    @Test
    fun anEpicThatIsNotLiveAndHasNoOtherStateIsEmpty() {
        assertEquals("", epicStateOf(detail(epic = epic())))
    }

    @Test
    fun anEpicWithNoQuestionParkOrHeldOrLimitedStepHasNoBlocker() {
        assertNull(blockerOf(detail(steps = listOf(step(end = "")))))
    }

    @Test
    fun aRunningStepNeitherHeldNorLimitedGivesNoBlocker() {
        assertNull(blockerOf(detail(steps = listOf(step(task = "t-1", end = "", started = 300L)))))
    }

    @Test
    fun anEndedStepGivesNoBlockerEvenWhenItWasHeld() {
        assertNull(blockerOf(detail(steps = listOf(step(end = "done", held = true, started = 400L)))))
    }

    @Test
    fun theFirstOpenQuestionIsTheBlockerWaitingOnYourAnswerSinceItWasAsked() {
        val q1 = question("Q1", asked = 1700000100L)
        val d = detail(questions = listOf(q1, question("Q2", asked = 1700000200L)))
        assertEquals(EpicBlocker.Question("waits on your answer", 1700000100L, q1), blockerOf(d))
    }

    @Test
    fun aQuestionAskedAtZeroHasASinceOfZero() {
        val q = question("Q1", asked = 0L)
        assertEquals(EpicBlocker.Question("waits on your answer", 0L, q), blockerOf(detail(questions = listOf(q))))
    }

    @Test
    fun aQuestionIsAheadOfAParkAndAHeldStep() {
        val q = question("Q1", asked = 1700000100L)
        val held = step(task = "t-1", end = "", held = true, started = 400L)
        val d = detail(questions = listOf(q), parked = "budget", steps = listOf(held))
        assertEquals(EpicBlocker.Question("waits on your answer", 1700000100L, q), blockerOf(d))
    }

    @Test
    fun aParkIsTheBlockerWithItsReasonAndASinceOfZeroAheadOfAHeldStep() {
        val held = step(task = "t-1", end = "", held = true, started = 400L)
        val d = detail(parked = "budget", steps = listOf(held))
        assertEquals(EpicBlocker.Parked("parked · budget", 0L), blockerOf(d))
    }

    @Test
    fun aHeldStepIsTheBlockerHeldSinceItsStageStarted() {
        val s = step(task = "t-1", end = "", held = true, started = 400L, stageStarted = 500L)
        assertEquals(EpicBlocker.Held("t-1 held", 500L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun aHeldStepWithNoStageStartIsHeldSinceItsStart() {
        val s = step(task = "t-1", end = "", held = true, started = 400L, stageStarted = 0L)
        assertEquals(EpicBlocker.Held("t-1 held", 400L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun aHeldStepWithNoTaskIsNamedByItsAgent() {
        val s = step(task = "", agent = "builder", end = "", held = true, started = 400L, stageStarted = 500L)
        assertEquals(EpicBlocker.Held("builder held", 500L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun aLimitedStepWaitsOnItsLimitKindSinceItsStageStarted() {
        val s = step(task = "t-2", end = "", limited = true, limitKind = "weekly", started = 400L, stageStarted = 600L)
        assertEquals(EpicBlocker.Held("t-2 waits on a weekly limit", 600L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun aLimitedStepWithNoLimitKindWaitsOnALimit() {
        val s = step(task = "t-2", end = "", limited = true, limitKind = "", started = 400L, stageStarted = 600L)
        assertEquals(EpicBlocker.Held("t-2 waits on a limit", 600L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun aStepBothHeldAndLimitedReadsAsHeld() {
        val s = step(task = "t-3", end = "", held = true, limited = true, limitKind = "weekly", started = 400L)
        assertEquals(EpicBlocker.Held("t-3 held", 400L, s), blockerOf(detail(steps = listOf(s))))
    }

    @Test
    fun theFirstHeldOrLimitedStepInOrderIsTheBlocker() {
        val limitedFirst = detail(
            steps = listOf(
                step(id = "s1", task = "t-a", end = "", limited = true, limitKind = "weekly", started = 400L),
                step(id = "s2", task = "t-b", end = "", held = true, started = 410L),
            ),
        )
        assertEquals("t-a waits on a weekly limit", blockerOf(limitedFirst)?.text)

        val heldFirst = detail(
            steps = listOf(
                step(id = "s1", task = "t-b", end = "", held = true, started = 400L),
                step(id = "s2", task = "t-a", end = "", limited = true, limitKind = "weekly", started = 410L),
            ),
        )
        assertEquals("t-b held", blockerOf(heldFirst)?.text)
    }

    @Test
    fun aRunningStepNeitherHeldNorLimitedIsSkippedForALaterHeldStep() {
        val d = detail(
            steps = listOf(
                step(id = "s1", task = "t-a", end = "", started = 300L),
                step(id = "s2", task = "t-b", end = "", held = true, started = 400L),
            ),
        )
        assertEquals("t-b held", blockerOf(d)?.text)
    }

    private fun epic(outcome: String = "", live: Boolean = false, waiting: Boolean = false) = EpicRow(
        id = "repo-a/alpha",
        repository = "repo-a",
        slug = "alpha",
        outcome = outcome,
        changed = 0L,
        live = live,
        waiting = waiting,
        runningSteps = emptyList(),
        endedSteps = 0,
        problem = "",
    )

    private fun step(
        id: String = "s1",
        task: String = "",
        agent: String = "",
        end: String = "",
        held: Boolean = false,
        limited: Boolean = false,
        limitKind: String = "",
        started: Long = 0L,
        stageStarted: Long = 0L,
    ) = StepRow(
        id = id,
        task = task,
        agent = agent,
        end = end,
        status = "",
        held = held,
        started = started,
        ended = 0L,
        doing = "",
        checks = emptyList(),
        reason = "",
        stageStarted = stageStarted,
        limited = limited,
        limitKind = limitKind,
    )

    private fun question(id: String, asked: Long) =
        EpicQuestionRow(id = id, text = "Which branch?", turn = 1L, asked = asked)

    private fun buildTask(id: String) = EpicBuildTaskRow(
        id = id,
        title = "",
        status = "",
        stage = "",
        red = null,
        green = null,
    )

    private fun detail(
        epic: EpicRow = epic(),
        tasks: List<EpicTaskRow> = emptyList(),
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
        questions: List<EpicQuestionRow> = emptyList(),
        paused: Boolean = false,
        parked: String = "",
    ) = EpicDetail(
        epic = epic,
        tasks = tasks,
        steps = steps,
        buildTasks = buildTasks,
        questions = questions,
        paused = paused,
        parked = parked,
    )
}
