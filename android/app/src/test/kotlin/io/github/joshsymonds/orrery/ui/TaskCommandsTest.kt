package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.performSemanticsAction
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.api.StepCallResult
import io.github.joshsymonds.orrery.api.StepRow
import io.github.joshsymonds.orrery.awaitNone
import io.github.joshsymonds.orrery.awaitUntil
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "example-epic"
private const val TASK = "T1"
private const val TITLE = "Parser T1"
private const val NOW = 1_700_000_000L

// The record's commands: Interrupt and Stop on the running step, Stop asking first, and the lines a refused or unknown
// call leaves, driven as the record's stateless body over synthetic epic details.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w600dp-h1200dp")
class TaskCommandsTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun interruptCallsHoldOnceForTheRunningStepAndReloadsOnceItLands() {
        val holds = CopyOnWriteArrayList<String>()
        val reloads = AtomicInteger()
        showBody(
            runningDetail(),
            hold = { id ->
                holds.add(id)
                StepCallResult.Landed
            },
            onLanded = { reloads.incrementAndGet() },
        )

        tap("task-interrupt")
        composeRule.awaitUntil("the hold call and its reload") { holds.size == 1 && reloads.get() == 1 }

        assertEquals(listOf("w1"), holds.toList())
    }

    @Test
    fun interruptIsNotShownWhileTheStepIsHeld() {
        showBody(runningDetail(held = true))

        assertNoNode("task-interrupt")
        composeRule.onNodeWithTag("task-more").assertExists()
    }

    @Test
    fun noCommandIsShownWithNothingRunning() {
        showBody(detail(buildTasks = listOf(buildTask()), steps = listOf(step("w1", end = "returned", ended = 130))))

        assertNoNode("task-interrupt")
        assertNoNode("task-more")
    }

    @Test
    fun stopCallsNothingUntilConfirmedAndThenCallsOnce() {
        val stops = CopyOnWriteArrayList<String>()
        showBody(
            runningDetail(),
            stop = { id ->
                stops.add(id)
                StepCallResult.Landed
            },
        )

        tap("task-more")
        composeRule.onNodeWithTag("task-stop-title").assertTextEquals("Stop $TITLE?")
        composeRule.onNodeWithText("The task ends short and the planner decides what to do next.").assertExists()
        composeRule.waitForIdle()
        assertTrue(stops.isEmpty())

        tap("task-stop-keep")
        composeRule.waitForIdle()
        assertTrue(stops.isEmpty())
        composeRule.awaitNone(hasTestTag("task-stop-title"))

        tap("task-more")
        tap("task-stop-confirm")
        composeRule.awaitUntil("the stop call") { stops.size == 1 }

        assertEquals(listOf("w1"), stops.toList())
    }

    @Test
    fun aRefusedInterruptShowsItsLineWithAFullStop() {
        val reloads = AtomicInteger()
        showBody(
            runningDetail(),
            hold = { StepCallResult.Refused("synthetic refusal") },
            onLanded = { reloads.incrementAndGet() },
        )

        tap("task-interrupt")
        composeRule.awaitUntil("the refusal line") {
            composeRule.onAllNodesWithText("Interrupt was refused: synthetic refusal.").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("task-outcome").assertTextEquals("Interrupt was refused: synthetic refusal.")
        assertEquals(0, reloads.get())
    }

    @Test
    fun aRefusalReasonThatEndsInAFullStopGetsNoSecond() {
        showBody(runningDetail(), stop = { StepCallResult.Refused("synthetic refusal.") })

        tap("task-more")
        tap("task-stop-confirm")
        composeRule.awaitUntil("the refusal line") {
            composeRule.onAllNodesWithText("Stop was refused: synthetic refusal.").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("task-outcome").assertTextEquals("Stop was refused: synthetic refusal.")
    }

    @Test
    fun aStopWhoseCallGotNoAnswerSaysItMayHaveLanded() {
        showBody(runningDetail(), stop = { throw IOException("synthetic transport failure") })

        tap("task-more")
        tap("task-stop-confirm")
        composeRule.awaitUntil("the unknown-call line") {
            composeRule.onAllNodesWithText("Stop may have landed: no answer came.").fetchSemanticsNodes().isNotEmpty()
        }

        composeRule.onNodeWithTag("task-outcome").assertTextEquals("Stop may have landed: no answer came.")
    }

    @Test
    fun aLandedCallClearsTheLineAnEarlierCallLeft() {
        val holds = CopyOnWriteArrayList<String>()
        showBody(
            runningDetail(),
            hold = {
                holds.add("call")
                if (holds.size == 1) StepCallResult.Refused("synthetic refusal") else StepCallResult.Landed
            },
        )

        tap("task-interrupt")
        composeRule.awaitUntil("the refusal line") { holds.size == 1 }
        composeRule.onNodeWithTag("task-outcome").assertExists()

        tap("task-interrupt")
        composeRule.awaitUntil("the second call") { holds.size == 2 }
        composeRule.waitForIdle()

        assertNoNode("task-outcome")
    }

    // A tap is the node's click action, as a sheet's own buttons take it while the sheet animates in.
    private fun tap(tag: String) {
        composeRule.onNodeWithTag(tag).performSemanticsAction(SemanticsActions.OnClick)
    }

    private fun assertNoNode(tag: String) {
        composeRule.onAllNodesWithTag(tag, useUnmergedTree = true).assertCountEquals(0)
    }

    private fun showBody(
        detail: EpicDetail,
        hold: (String) -> StepCallResult = { StepCallResult.Landed },
        stop: (String) -> StepCallResult = { StepCallResult.Landed },
        onLanded: () -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                PhaseOutputBody(
                    detail = detail,
                    task = TASK,
                    brief = null,
                    now = NOW,
                    onOpenEpic = {},
                    onOpenTask = { _, _ -> },
                    onOpenSession = {},
                    hold = hold,
                    stop = stop,
                    onLanded = onLanded,
                    loadArtifact = { _, _, _ -> null },
                )
            }
        }
    }

    // A task whose one implementer step runs, which may be held.
    private fun runningDetail(held: Boolean = false): EpicDetail = detail(
        buildTasks = listOf(buildTask()),
        steps = listOf(step("w1", end = "", started = 100, held = held)),
    )

    private fun step(
        id: String,
        end: String = "",
        started: Long = 100,
        ended: Long = 0,
        held: Boolean = false,
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
        checks = emptyList(),
        reason = "",
    )

    private fun buildTask(): EpicBuildTaskRow = EpicBuildTaskRow(
        id = TASK,
        title = TITLE,
        status = "open",
        stage = "implement",
        red = null,
        green = null,
        requirements = listOf("R1"),
        place = "implement",
        tone = "run",
    )

    private fun detail(
        buildTasks: List<EpicBuildTaskRow> = emptyList(),
        steps: List<StepRow> = emptyList(),
    ): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
        ),
        steps = steps,
        buildTasks = buildTasks,
    )
}
