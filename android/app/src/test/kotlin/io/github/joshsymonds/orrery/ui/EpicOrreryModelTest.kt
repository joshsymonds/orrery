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
        end: String = "",
        stage: String = "turn",
        stageStarted: Long = 0L,
        held: Boolean = false,
        limited: Boolean = false,
        limitKind: String = "",
        readOnly: Boolean = false,
        doing: String = "",
    ): StepRow = StepRow(
        id = "step",
        task = "T1",
        agent = "builder",
        end = end,
        status = "",
        held = held,
        started = 100L,
        ended = 0L,
        doing = doing,
        checks = emptyList(),
        reason = "",
        stage = stage,
        stageStarted = stageStarted,
        limited = limited,
        limitKind = limitKind,
        readOnly = readOnly,
        summary = "",
    )

    // The build task T1 at a status and build stage, requiring R1.
    private fun buildTask(status: String, stage: String): EpicBuildTaskRow = EpicBuildTaskRow(
        id = "T1",
        title = "Parser",
        status = status,
        stage = stage,
        red = null,
        green = null,
        requirements = listOf("R1"),
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

    // The record task of a detail that has one build task T1, at the given status and stage, with the given steps.
    private fun taskOf(status: String, stage: String = "", vararg steps: StepRow): RecordTask {
        val tasks = recordTasksOf(detail(buildTasks = listOf(buildTask(status, stage)), steps = steps.toList()))
        assertEquals(1, tasks.size)
        return tasks[0]
    }

    @Test
    fun aBuildTaskRunningInItsStageSitsInTheRingItsStageNames() {
        val cases = listOf(
            "red" to RingPlace.RED,
            "implement" to RingPlace.IMPLEMENT,
            "orchestrate" to RingPlace.IMPLEMENT,
            "green" to RingPlace.GREEN,
            "review" to RingPlace.REVIEW,
            "verify" to RingPlace.REVIEW,
        )
        for ((stage, ring) in cases) {
            val task = taskOf("open", stage, step())
            assertEquals(stage, ring, task.place)
            assertEquals(stage, WedgeTone.RUN, task.tone)
            // With no step yet, the stage alone places the task, running.
            val staged = taskOf("open", stage)
            assertEquals("$stage with no step", ring, staged.place)
            assertEquals("$stage with no step", WedgeTone.RUN, staged.tone)
        }
    }

    @Test
    fun aTaskWithNoBuildStageSitsInTheRingOfItsRunningStepsPhaseOrItsDispatch() {
        assertEquals(RingPlace.IMPLEMENT, taskOf("open", "", step()).place)
        assertEquals(RingPlace.GREEN, taskOf("open", "", step(stage = "checks")).place)
        assertEquals(RingPlace.REVIEW, taskOf("open", "", step(readOnly = true)).place)
        assertEquals(RingPlace.IMPLEMENT, taskOf("dispatched", "").place)
    }

    @Test
    fun anAcceptedTaskSitsAtIntegrateWithAcceptedCellsAndWords() {
        val gated = taskOf("gated")
        assertEquals(RingPlace.INTEGRATE, gated.place)
        assertEquals(false, gated.integrated)
        assertEquals(WedgeTone.ACC, gated.tone)
        assertEquals(listOf(CellTone.OK, CellTone.OK, CellTone.OK, CellTone.OK, CellTone.ACC), gated.cells)
        assertEquals("accepted · waits to integrate", gated.words)

        val ended = taskOf("open", "red", step(end = "done"))
        assertEquals(RingPlace.INTEGRATE, ended.place)
        assertEquals(WedgeTone.ACC, ended.tone)
    }

    @Test
    fun anIntegratedTaskIsDoneWithFiveOkCells() {
        val task = taskOf("done")
        assertEquals(true, task.integrated)
        assertEquals(WedgeTone.OK, task.tone)
        assertEquals(List(RINGS) { CellTone.OK }, task.cells)
        assertEquals("done", task.words)
    }

    @Test
    fun aPlannedTaskHasNoRingAndFiveNoneCells() {
        for (status in listOf("open", "pending")) {
            val task = taskOf(status)
            assertEquals(status, null, task.place)
            assertEquals(status, false, task.integrated)
            assertEquals(status, WedgeTone.PLAN, task.tone)
            assertEquals(status, List(RINGS) { CellTone.NONE }, task.cells)
            assertEquals(status, "not started", task.words)
        }
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
    fun aLimitedTaskWaitsOnAUsageLimitOrOnItsKindOfLimit() {
        val plain = taskOf("open", "", step(limited = true))
        assertEquals(RingPlace.IMPLEMENT, plain.place)
        assertEquals(WedgeTone.LIMITED, plain.tone)
        assertEquals("waits on a usage limit", plain.words)
        assertEquals(listOf(CellTone.OK, CellTone.LIMITED, CellTone.NONE, CellTone.NONE, CellTone.NONE), plain.cells)

        val weekly = taskOf("open", "", step(limited = true, limitKind = "weekly"))
        assertEquals("waits on the weekly limit", weekly.words)
    }

    @Test
    fun aHeldTaskIsMineAndSaysHeld() {
        val task = taskOf("open", "", step(held = true))
        assertEquals(WedgeTone.MINE, task.tone)
        assertEquals("implement · held", task.words)
        assertEquals(listOf(CellTone.OK, CellTone.MINE, CellTone.NONE, CellTone.NONE, CellTone.NONE), task.cells)
    }

    @Test
    fun aTaskWhoseLatestStepEndedOtherwiseIsBadAndSaysFailed() {
        val task = taskOf("open", "", step(end = "failed"))
        assertEquals(RingPlace.IMPLEMENT, task.place)
        assertEquals(WedgeTone.BAD, task.tone)
        assertEquals("implement · failed", task.words)
        assertEquals(listOf(CellTone.OK, CellTone.BAD, CellTone.NONE, CellTone.NONE, CellTone.NONE), task.cells)
    }

    @Test
    fun aRunningTaskSaysItsRingAndItsDoingOrItsRingAlone() {
        val doing = taskOf("open", "", step(doing = "writing the parser"))
        assertEquals(WedgeTone.RUN, doing.tone)
        assertEquals("implement · writing the parser", doing.words)

        val bare = taskOf("open", "red")
        assertEquals(RingPlace.RED, bare.place)
        assertEquals("red", bare.words)
        assertEquals(listOf(CellTone.RUN, CellTone.NONE, CellTone.NONE, CellTone.NONE, CellTone.NONE), bare.cells)
    }

    @Test
    fun aRunningTasksSinceIsItsStageStartAndZeroWhenNothingRuns() {
        assertEquals(175L, taskOf("open", "", step(stageStarted = 175L)).since)
        // A running step whose stage has no start reads from the step's own start.
        assertEquals(100L, taskOf("open", "", step()).since)
        assertEquals(0L, taskOf("open", "red").since)
        assertEquals(0L, taskOf("open", "", step(end = "done", stageStarted = 175L)).since)
    }

    @Test
    fun recordTasksAreTheTasksWedgesOfPicksWithTheirTitlesRequirementsAndStates() {
        val built = detail(
            buildTasks = listOf(buildTask("open", "")),
            tasks = listOf(EpicTaskRow(effort = 1, slug = "old", subject = "Old", status = "done", requirements = emptyList())),
        )
        assertEquals(wedgesOf(built).map { it.task }, recordTasksOf(built).map { it.task })
        assertEquals(listOf("T1"), recordTasksOf(built).map { it.task })
        assertEquals("Parser", recordTasksOf(built)[0].title)
        assertEquals(listOf("R1"), recordTasksOf(built)[0].requirements)

        val record = detail(
            tasks = listOf(
                EpicTaskRow(effort = 1, slug = "first", subject = "First task", status = "done", requirements = listOf("R2", "R3")),
                EpicTaskRow(effort = 1, slug = "second", subject = "Second", status = "pending", requirements = emptyList()),
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
