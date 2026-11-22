package io.github.joshsymonds.orrery.ui

import android.content.Context
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.EpicTaskRow
import io.github.joshsymonds.orrery.api.EvidenceRunRow
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

// The five rings of the record's places, outside in, each cell of a task's five.
private const val RINGS = 5

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp-xxhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class EpicOrreryModelTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    // The animator duration scale is 0 for every test, so the orrery's glow holds still and the composition goes idle.
    @Before
    fun animatorDurationScaleIsZero() {
        Settings.Global.putFloat(
            ApplicationProvider.getApplicationContext<Context>().contentResolver,
            Settings.Global.ANIMATOR_DURATION_SCALE,
            0f,
        )
    }

    // A labelled step of task T1, running in stage turn unless the arguments say otherwise.
    private fun step(
        id: String = "step",
        task: String = "T1",
        end: String = "",
        stage: String = "turn",
        stageStarted: Long = 0L,
        held: Boolean = false,
        limited: Boolean = false,
        limitKind: String = "",
        readOnly: Boolean = false,
        role: String = "",
        doing: String = "",
    ): StepRow = StepRow(
        id = id,
        task = task,
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = 100L,
        ended = if (end == "") 0L else 160L,
        doing = doing,
        checks = emptyList(),
        reason = "",
        stage = stage,
        stageStarted = stageStarted,
        limited = limited,
        limitKind = limitKind,
        readOnly = readOnly,
        role = role,
        summary = "",
    )

    // An ended evidence run with the given id and phase.
    private fun run(id: String, phase: String) = EvidenceRunRow(
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
        created = 0L,
        ended = 0L,
        logBytes = 0L,
        stderrBytes = 0L,
        artifacts = emptyList(),
    )

    // The build task T1, requiring R1, at the place and tone serve computed for it.
    private fun buildTask(
        place: String,
        tone: String,
        status: String = "open",
        stage: String = "",
        red: EvidenceRunRow? = null,
        green: EvidenceRunRow? = null,
    ): EpicBuildTaskRow = EpicBuildTaskRow(
        id = "T1",
        title = "Parser",
        status = status,
        stage = stage,
        red = red,
        green = green,
        requirements = listOf("R1"),
        place = place,
        tone = tone,
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
        steps = steps,
        buildTasks = buildTasks,
    )

    // The one record task of a detail with the one build task.
    private fun taskOf(build: EpicBuildTaskRow, vararg steps: StepRow): RecordTask {
        val tasks = recordTasksOf(detail(buildTasks = listOf(build), steps = steps.toList()))
        assertEquals(1, tasks.size)
        return tasks[0]
    }

    // The record task of a detail that has one build task T1, at the given place and tone, with the given steps.
    private fun taskOf(place: String, tone: String, vararg steps: StepRow): RecordTask =
        taskOf(buildTask(place, tone), *steps)

    @Test
    fun aTaskSitsInTheRingServesPlaceNames() {
        val cases = listOf(
            "red" to RingPlace.RED,
            "implement" to RingPlace.IMPLEMENT,
            "green" to RingPlace.GREEN,
            "review" to RingPlace.REVIEW,
            "integrate" to RingPlace.INTEGRATE,
        )
        for ((place, ring) in cases) {
            val task = taskOf(place, "run")
            assertEquals(place, ring, task.place)
            assertEquals(place, false, task.integrated)
            assertEquals(place, WedgeTone.RUN, task.tone)
        }
    }

    @Test
    fun aTaskReadsItsPlaceAndToneFromServeNotFromItsStepsStageOrStatus() {
        // Its steps, build stage and status would each have placed it elsewhere; serve's place and tone stand.
        val task = taskOf(
            buildTask("review", "mine", status = "done", stage = "red"),
            step(stage = "checks", limited = true),
        )
        assertEquals(RingPlace.REVIEW, task.place)
        assertEquals(false, task.integrated)
        assertEquals(WedgeTone.MINE, task.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.OK, CellTone.MINE, CellTone.NONE), task.cells)
    }

    @Test
    fun aPlaceOrToneServeDoesNotNameReadsAsPlannedAndPlan() {
        for (place in listOf("", "orbiting")) {
            val task = taskOf(place, "run")
            assertEquals(place, null, task.place)
            assertEquals(place, false, task.integrated)
            assertEquals(place, List(RINGS) { CellTone.NONE }, task.cells)
            assertEquals(place, "not started", task.words)
        }
        val toneless = taskOf("green", "")
        assertEquals(WedgeTone.PLAN, toneless.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.PLAN, CellTone.NONE, CellTone.NONE), toneless.cells)
    }

    @Test
    fun anAcceptedTaskSitsAtIntegrateWithAcceptedCellsAndWords() {
        val gated = taskOf("integrate", "acc")
        assertEquals(RingPlace.INTEGRATE, gated.place)
        assertEquals(false, gated.integrated)
        assertEquals(WedgeTone.ACC, gated.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.OK, CellTone.OK, CellTone.ACC), gated.cells)
        assertEquals("accepted · waits to integrate", gated.words)
    }

    @Test
    fun aTaskWhoseImplementerEndedGoalUnmetAndWhoseReviewerEndedDoneIsAcceptedNotFailed() {
        val task = taskOf(
            buildTask("integrate", "acc", status = "done", stage = "review"),
            step(id = "implementer", end = "goal_unmet", role = "implementer"),
            step(id = "reviewer", end = "done", readOnly = true, role = "reviewer"),
        )
        assertEquals(RingPlace.INTEGRATE, task.place)
        assertEquals(false, task.integrated)
        assertEquals(WedgeTone.ACC, task.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.OK, CellTone.OK, CellTone.ACC), task.cells)
        assertEquals("accepted · waits to integrate", task.words)
    }

    @Test
    fun anIntegratedTaskIsDoneWithFiveOkCells() {
        val task = taskOf("integrated", "ok")
        assertEquals(RingPlace.INTEGRATE, task.place)
        assertEquals(true, task.integrated)
        assertEquals(WedgeTone.OK, task.tone)
        assertEquals(List(RINGS) { CellTone.OK }, task.cells)
        assertEquals("done", task.words)
    }

    @Test
    fun aPlannedTaskHasNoRingAndFiveNoneCells() {
        val task = taskOf("planned", "plan")
        assertEquals(null, task.place)
        assertEquals(false, task.integrated)
        assertEquals(WedgeTone.PLAN, task.tone)
        assertEquals(List(RINGS) { CellTone.NONE }, task.cells)
        assertEquals("not started", task.words)
    }

    @Test
    fun cellsOfMarkTheCellsBeforeAtAndAfterThePlaceAndTheWholeTaskIntegratedOrPlanned() {
        assertEquals(
            listOf(CellTone.OK, CellTone.RUN, CellTone.NONE, CellTone.NONE, CellTone.NONE),
            cellsOf(RingPlace.IMPLEMENT, false, WedgeTone.RUN),
        )
        assertEquals(
            listOf(CellTone.OK, CellTone.OK, CellTone.LIMITED, CellTone.NONE, CellTone.NONE),
            cellsOf(RingPlace.GREEN, false, WedgeTone.LIMITED),
        )
        assertEquals(
            listOf(CellTone.BAD, CellTone.NONE, CellTone.NONE, CellTone.NONE, CellTone.NONE),
            cellsOf(RingPlace.RED, false, WedgeTone.BAD),
        )
        assertEquals(
            listOf(CellTone.OK, CellTone.OK, CellTone.OK, CellTone.OK, CellTone.ACC),
            cellsOf(RingPlace.INTEGRATE, false, WedgeTone.ACC),
        )
        assertEquals(List(RINGS) { CellTone.OK }, cellsOf(RingPlace.INTEGRATE, true, WedgeTone.OK))
        assertEquals(List(RINGS) { CellTone.NONE }, cellsOf(null, false, WedgeTone.PLAN))
    }

    @Test
    fun aRunningTaskAtImplementDrawsOkThenRunThenNothingWithNoHoles() {
        val task = taskOf("implement", "run", step(doing = "writing the parser"))
        assertEquals(listOf(CellTone.OK, CellTone.RUN, CellTone.NONE, CellTone.NONE, CellTone.NONE), task.cells)
        assertEquals("implement · writing the parser", task.words)
    }

    @Test
    fun aRetryingTaskWhoseLatestStepEndedGoalUnmetWithNothingRunningIsRunningNotFailed() {
        val task = taskOf("implement", "run", step(end = "goal_unmet", doing = "writing the parser"))
        assertEquals(RingPlace.IMPLEMENT, task.place)
        assertEquals(WedgeTone.RUN, task.tone)
        assertEquals(listOf(CellTone.OK, CellTone.RUN, CellTone.NONE, CellTone.NONE, CellTone.NONE), task.cells)
        assertEquals("implement", task.words)
        assertEquals(0L, task.since)
    }

    @Test
    fun aGapIsBadAtItsPlaceAndSaysFailed() {
        val task = taskOf(buildTask("green", "bad", status = "gap", stage = "green"))
        assertEquals(RingPlace.GREEN, task.place)
        assertEquals(WedgeTone.BAD, task.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.BAD, CellTone.NONE, CellTone.NONE), task.cells)
        assertEquals("green · failed", task.words)
    }

    @Test
    fun aLimitedTaskWaitsOnAUsageLimitOrOnItsKindOfLimit() {
        val plain = taskOf("implement", "limited", step(limited = true))
        assertEquals(RingPlace.IMPLEMENT, plain.place)
        assertEquals(WedgeTone.LIMITED, plain.tone)
        assertEquals("waits on a usage limit", plain.words)
        assertEquals(listOf(CellTone.OK, CellTone.LIMITED, CellTone.NONE, CellTone.NONE, CellTone.NONE), plain.cells)

        val weekly = taskOf("implement", "limited", step(limited = true, limitKind = "weekly"))
        assertEquals("waits on the weekly limit", weekly.words)
    }

    @Test
    fun aHeldTaskIsMineAndSaysHeld() {
        val task = taskOf("implement", "mine", step(held = true))
        assertEquals(WedgeTone.MINE, task.tone)
        assertEquals("implement · held", task.words)
        assertEquals(listOf(CellTone.OK, CellTone.MINE, CellTone.NONE, CellTone.NONE, CellTone.NONE), task.cells)
    }

    @Test
    fun aRunningTaskSaysItsRingAndItsDoingOrItsRingAlone() {
        val doing = taskOf("implement", "run", step(doing = "writing the parser"))
        assertEquals(WedgeTone.RUN, doing.tone)
        assertEquals("implement · writing the parser", doing.words)

        val bare = taskOf("red", "run")
        assertEquals(RingPlace.RED, bare.place)
        assertEquals("red", bare.words)
        assertEquals(listOf(CellTone.RUN, CellTone.NONE, CellTone.NONE, CellTone.NONE, CellTone.NONE), bare.cells)
    }

    @Test
    fun aRunningTasksSinceIsItsStageStartAndZeroWhenNothingRuns() {
        assertEquals(175L, taskOf("implement", "run", step(stageStarted = 175L)).since)
        // A running step whose stage has no start reads from the step's own start.
        assertEquals(100L, taskOf("implement", "run", step()).since)
        assertEquals(0L, taskOf("red", "run").since)
        assertEquals(0L, taskOf("integrate", "acc", step(end = "done", stageStarted = 175L)).since)
    }

    @Test
    fun anEvidenceJudgeStepBelongsToTheBuildTaskWhoseRedOrGreenRunItJudges() {
        val build = buildTask("green", "run", red = run("run-red", "red"), green = run("run-green", "green"))
        val judging = step(
            id = "judge",
            task = "evidence-run-green",
            stage = "judge",
            stageStarted = 150L,
            readOnly = true,
            doing = "judging the green run",
        )
        val tasks = recordTasksOf(detail(buildTasks = listOf(build), steps = listOf(judging)))

        assertEquals("T1", tasks.taskOfStep("evidence-run-green")?.task)
        assertEquals("T1", tasks.taskOfStep("evidence-run-red")?.task)
        assertEquals("T1", tasks.taskOfStep("T1")?.task)
        assertEquals(null, tasks.taskOfStep("evidence-run-other"))
        assertEquals(null, tasks.taskOfStep("T2"))
        // The judge step is the task's running step: its words and its since read from it.
        assertEquals("green · judging the green run", tasks[0].words)
        assertEquals(150L, tasks[0].since)
    }

    @Test
    fun aRecordTasksStepIsTheOneItsSlugLabels() {
        val tasks = recordTasksOf(
            detail(tasks = listOf(EpicTaskRow(effort = 1, slug = "first", subject = "First", status = "open"))),
        )
        assertEquals("first", tasks.taskOfStep("first")?.task)
        assertEquals(null, tasks.taskOfStep("evidence-first"))
    }

    @Test
    fun recordTasksAreTheTasksWedgesOfPicksWithTheirTitlesRequirementsAndStates() {
        val built = detail(
            buildTasks = listOf(buildTask("planned", "plan")),
            tasks = listOf(EpicTaskRow(effort = 1, slug = "old", subject = "Old", status = "done", place = "integrated", tone = "ok")),
        )
        assertEquals(wedgesOf(built).map { it.task }, recordTasksOf(built).map { it.task })
        assertEquals(listOf("T1"), recordTasksOf(built).map { it.task })
        assertEquals("Parser", recordTasksOf(built)[0].title)
        assertEquals(listOf("R1"), recordTasksOf(built)[0].requirements)

        val record = detail(
            tasks = listOf(
                EpicTaskRow(
                    effort = 1,
                    slug = "first",
                    subject = "First task",
                    status = "done",
                    requirements = listOf("R2", "R3"),
                    place = "integrated",
                    tone = "ok",
                ),
                EpicTaskRow(effort = 1, slug = "second", subject = "Second", status = "pending", place = "planned", tone = "plan"),
            ),
        )
        assertEquals(wedgesOf(record).map { it.task }, recordTasksOf(record).map { it.task })
        assertEquals(listOf("First task", "Second"), recordTasksOf(record).map { it.title })
        assertEquals(listOf("R2", "R3"), recordTasksOf(record)[0].requirements)
        assertEquals(listOf(RingPlace.INTEGRATE, null), recordTasksOf(record).map { it.place })
        assertEquals(listOf(true, false), recordTasksOf(record).map { it.integrated })
    }

    @Test
    fun phaseOfPlaceNamesTheWedgePhaseOfEachRing() {
        assertEquals(Phase.IMPLEMENTING, phaseOfPlace(RingPlace.RED, false))
        assertEquals(Phase.IMPLEMENTING, phaseOfPlace(RingPlace.IMPLEMENT, false))
        assertEquals(Phase.CHECKING, phaseOfPlace(RingPlace.GREEN, false))
        assertEquals(Phase.REVIEWING, phaseOfPlace(RingPlace.REVIEW, false))
        assertEquals(Phase.INTEGRATING, phaseOfPlace(RingPlace.INTEGRATE, false))
        assertEquals(Phase.INTEGRATING, phaseOfPlace(RingPlace.INTEGRATE, true))
        assertEquals(null, phaseOfPlace(null, false))
    }

    @Test
    fun pipsNameEachRingsCellInItsWords() {
        composeRule.setContent {
            Pips(listOf(CellTone.OK, CellTone.RUN, CellTone.MINE, CellTone.LIMITED, CellTone.BAD))
        }

        assertEquals(
            listOf("red passed, implement running, green held, review waiting on a limit, integrate failed"),
            contentDescriptionOf("pips"),
        )
    }

    @Test
    fun pipsNameAcceptedPlannedAndUnreachedCells() {
        composeRule.setContent {
            Pips(listOf(CellTone.OK, CellTone.OK, CellTone.ACC, CellTone.PLAN, CellTone.NONE))
        }

        assertEquals(
            listOf("red passed, implement passed, green accepted, review planned, integrate not reached"),
            contentDescriptionOf("pips"),
        )
    }

    @Test
    fun anOrreryNamesALimitedWedgeAsWaitingOnALimit() {
        val wedge = Wedge(
            task = "T1",
            title = "Parser",
            requirements = emptyList(),
            place = WedgePlace.IMPLEMENTING,
            tone = WedgeTone.LIMITED,
            slices = Phase.entries.map { phase ->
                PhaseSlice(
                    phase = phase,
                    state = if (phase == Phase.IMPLEMENTING) SliceState.NOW else SliceState.AHEAD,
                    summary = "",
                    since = 0L,
                    seconds = 0L,
                )
            },
        )
        composeRule.setContent {
            Orrery(listOf(wedge), emptyList(), modifier = Modifier.size(400.dp))
        }

        assertEquals(
            listOf("0 of 1 integrated", "T1: implementing, waits on a limit"),
            contentDescriptionOf("orrery"),
        )
    }

    private fun contentDescriptionOf(tag: String): List<String>? =
        composeRule.onNodeWithTag(tag).fetchSemanticsNode().config.getOrNull(SemanticsProperties.ContentDescription)
}
