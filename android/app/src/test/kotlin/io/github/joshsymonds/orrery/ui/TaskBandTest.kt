package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.EpicBuildTaskRow
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicQuestionRow
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.mainThreadComposeRule
import java.util.concurrent.CopyOnWriteArrayList
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val EPIC = "example-epic"
private const val NOW = 1_700_000_000L

// The pin's words and the chips' pips, as the tests read them. Each task's pips name its five rings outside in.
private const val QUIET_WORDS = "building · 1 of 4 tasks done"
private const val ASKING_QUESTION = "asks you a question"
private const val PASSED_ALL = "red passed, implement passed, green passed, review passed, integrate passed"
private const val IMPLEMENTING_NOW =
    "red passed, implement running, green not reached, review not reached, integrate not reached"
private const val NOT_STARTED =
    "red not reached, implement not reached, green not reached, review not reached, integrate not reached"
private const val CHECKING_NOW =
    "red passed, implement passed, green running, review not reached, integrate not reached"

// The epic's tasks in task order: a integrated, b implementing (the current one in the strip), c planned, d checking.
private const val DONE_TASK = "a"
private const val CURRENT_TASK = "b"
private const val PLANNED_TASK = "c"
private const val CHECKING_TASK = "d"

// Task and epic states are synthetic; the tests read no clock, only the fixed NOW above.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w600dp-h1200dp")
class TaskBandTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    @Test
    fun aQuietEpicsPinNamesTheEpicAndItsWordsWithNoBlocker() {
        showPin(detail())

        composeRule.onNodeWithText(EPIC, useUnmergedTree = true).assertTextEquals(EPIC)
        composeRule.onNodeWithText(QUIET_WORDS, useUnmergedTree = true).assertTextEquals(QUIET_WORDS)
        composeRule.onAllNodesWithTag("epic-pin-blocker", useUnmergedTree = true).assertCountEquals(0)
    }

    @Test
    fun anAskingEpicsPinShowsItsBlockerAndATapOnTheBlockerOpensTheEpicOnce() {
        val opened = CopyOnWriteArrayList<Unit>()
        showPin(detail(questions = listOf(question())), onOpenEpic = { opened.add(Unit) })

        composeRule.onNodeWithTag("epic-pin-blocker", useUnmergedTree = true).assertTextEquals(ASKING_QUESTION)
        composeRule.onNodeWithTag("epic-pin-blocker", useUnmergedTree = true).performClick()
        composeRule.waitForIdle()

        assertEquals(1, opened.size)
    }

    @Test
    fun theStripDrawsOneChipPerTaskInTaskOrderWithItsPips() {
        showBand(detail())

        composeRule.onNodeWithContentDescription("this epic's tasks").assertExists()
        listOf(DONE_TASK, CURRENT_TASK, PLANNED_TASK, CHECKING_TASK).forEach { id ->
            composeRule.onNodeWithTag("task-chip:$id").assertTextContains("Parser $id")
        }
        assertLeftToRightInOrder(listOf(DONE_TASK, CURRENT_TASK, PLANNED_TASK, CHECKING_TASK))
        assertPips(DONE_TASK, PASSED_ALL)
        assertPips(CURRENT_TASK, IMPLEMENTING_NOW)
        assertPips(PLANNED_TASK, NOT_STARTED)
        assertPips(CHECKING_TASK, CHECKING_NOW)
    }

    @Test
    fun theCurrentChipIsSelectedAndPressingItOpensNothing() {
        val opened = CopyOnWriteArrayList<Pair<String, Phase>>()
        showBand(detail(), onOpenTask = { task, phase -> opened.add(task to phase) })

        composeRule.onNodeWithTag("task-chip:$CURRENT_TASK").assertIsSelected()
        composeRule.onNodeWithTag("task-chip:$CURRENT_TASK").performClick()
        composeRule.waitForIdle()

        assertTrue(opened.isEmpty())
    }

    @Test
    fun anotherChipOpensItsTaskAtItsPhaseAndAPlannedTaskAtImplementing() {
        val opened = CopyOnWriteArrayList<Pair<String, Phase>>()
        showBand(detail(), onOpenTask = { task, phase -> opened.add(task to phase) })

        composeRule.onNodeWithTag("task-chip:$DONE_TASK").performClick()
        composeRule.onNodeWithTag("task-chip:$PLANNED_TASK").performClick()
        composeRule.onNodeWithTag("task-chip:$CHECKING_TASK").performClick()
        composeRule.waitForIdle()

        assertEquals(
            listOf(
                DONE_TASK to Phase.INTEGRATING,
                PLANNED_TASK to Phase.IMPLEMENTING,
                CHECKING_TASK to Phase.CHECKING,
            ),
            opened.toList(),
        )
    }

    private fun showPin(detail: EpicDetail, onOpenEpic: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme {
                EpicPin(detail = detail, now = NOW, onOpenEpic = onOpenEpic)
            }
        }
    }

    private fun showBand(
        detail: EpicDetail,
        onOpenEpic: () -> Unit = {},
        onOpenTask: (String, Phase) -> Unit = { _, _ -> },
    ) {
        composeRule.setContent {
            MaterialTheme {
                BandUnderTest(detail, onOpenEpic, onOpenTask)
            }
        }
    }

    @Composable
    private fun BandUnderTest(detail: EpicDetail, onOpenEpic: () -> Unit, onOpenTask: (String, Phase) -> Unit) {
        ContextBand(
            detail = detail,
            currentTask = CURRENT_TASK,
            now = NOW,
            onOpenEpic = onOpenEpic,
            onOpenTask = onOpenTask,
        )
    }

    private fun assertPips(id: String, expected: String) {
        composeRule.onNode(hasTestTag("pips") and hasAnyAncestor(hasTestTag("task-chip:$id")), useUnmergedTree = true)
            .assert(hasContentDescription(expected))
    }

    private fun assertLeftToRightInOrder(ids: List<String>) {
        val lefts = ids.map { composeRule.onNodeWithTag("task-chip:$it").fetchSemanticsNode().boundsInRoot.left }
        assertTrue("chips are not in task order: $lefts", lefts.zipWithNext().all { (before, after) -> before < after })
    }

    // The epic's tasks: a integrated, b dispatched at its implement stage, c planned, d dispatched at its green stage.
    private fun detail(questions: List<EpicQuestionRow> = emptyList()): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "built",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            problem = "",
            source = "orreryd",
        ),
        tasks = emptyList(),
        slices = emptyList(),
        steps = emptyList(),
        buildTasks = listOf(
            buildTask(DONE_TASK, status = "done"),
            buildTask(CURRENT_TASK, stage = "implement"),
            buildTask(PLANNED_TASK, status = "planned"),
            buildTask(CHECKING_TASK, stage = "green"),
        ),
        questions = questions,
    )

    private fun question(): EpicQuestionRow =
        EpicQuestionRow(id = "q1", text = "synthetic question", turn = 0L, asked = 0L)

    private fun buildTask(id: String, status: String = "dispatched", stage: String = ""): EpicBuildTaskRow =
        EpicBuildTaskRow(
        id = id,
        title = "Parser $id",
        status = status,
        stage = stage,
        red = null,
        green = null,
        requirements = listOf("R1"),
    )
}
