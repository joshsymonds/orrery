package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ActivityLinesTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val now = 1_000_000L

    private fun task(
        id: String,
        description: String,
        started: Long,
        type: String = "agent",
        status: String = "running",
        subagentType: String = "",
        activity: String = "",
    ): BackgroundTask = BackgroundTask(
        id = id,
        type = type,
        status = status,
        description = description,
        subagentType = subagentType,
        activity = activity,
        started = started,
        ended = 0L,
    )

    private fun setLines(
        tasks: List<BackgroundTask>,
        onOpen: (BackgroundTask) -> Unit = {},
        onMore: () -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                ActivityLines(tasks = tasks, now = now, onOpen = onOpen, onMore = onMore)
            }
        }
    }

    private fun textsOf(tag: String): List<String> = composeRule.onNodeWithTag(tag)
        .fetchSemanticsNode()
        .config[SemanticsProperties.Text]
        .map { it.text }

    private fun contentDescriptionsOf(tag: String): List<String> = composeRule.onNodeWithTag(tag)
        .fetchSemanticsNode()
        .config[SemanticsProperties.ContentDescription]

    @Test
    fun drawsNoLinesNodeWhenNothingRuns() {
        setLines(emptyList())

        composeRule.onAllNodesWithTag("activity-lines").assertCountEquals(0)
    }

    @Test
    fun drawsOneLinePerRunningAgentOrShellWithWhoWhatAndClock() {
        setLines(
            listOf(
                task("a1", "Map the rail", now - 125, subagentType = "Explore", activity = "reading files"),
                task("s1", "npm run dev", now - 30, type = "shell", activity = "shell"),
                task("s2", "vite preview", now - 600, type = "shell", activity = "listening on 3000"),
            ),
        )

        composeRule.onAllNodesWithTag("activity-lines").assertCountEquals(1)
        assertEquals(listOf("Explore", "Map the rail", "reading files", "2:05"), textsOf("activity:a1"))
        assertEquals(listOf("shell", "npm run dev", "0:30"), textsOf("activity:s1"))
        assertEquals(listOf("shell", "vite preview", "listening on 3000", "10:00"), textsOf("activity:s2"))
        assertEquals(listOf("working"), contentDescriptionsOf("activity:a1"))
        assertEquals(listOf("idle"), contentDescriptionsOf("activity:s1"))
    }

    @Test
    fun anAgentWithNoSubagentTypeIsNamedAgent() {
        setLines(listOf(task("a2", "Check the build", now - 10, activity = "running tests")))

        assertEquals(listOf("agent", "Check the build", "running tests", "0:10"), textsOf("activity:a2"))
    }

    @Test
    fun endedFailedAndStoppedTasksHaveNoLine() {
        setLines(
            listOf(
                task("r1", "Map the rail", now - 125),
                task("d1", "Find every caller", now - 300, status = "done"),
                task("f1", "Review the query change", now - 200, status = "failed"),
                task("x1", "pnpm dev", now - 100, type = "shell", status = "stopped"),
            ),
        )

        composeRule.onAllNodesWithTag("activity:r1").assertCountEquals(1)
        composeRule.onAllNodesWithTag("activity:d1").assertCountEquals(0)
        composeRule.onAllNodesWithTag("activity:f1").assertCountEquals(0)
        composeRule.onAllNodesWithTag("activity:x1").assertCountEquals(0)
    }

    @Test
    fun fiveRunningDrawsTheFirstThreeByStartAndAMoreLineForTheRest() {
        setLines(
            listOf(
                task("r1", "one", now - 10),
                task("r2", "two", now - 500),
                task("r3", "three", now - 300),
                task("r4", "four", now - 50),
                task("r5", "five", now - 100),
            ),
        )

        composeRule.onAllNodesWithTag("activity:r2").assertCountEquals(1)
        composeRule.onAllNodesWithTag("activity:r3").assertCountEquals(1)
        composeRule.onAllNodesWithTag("activity:r5").assertCountEquals(1)
        composeRule.onAllNodesWithTag("activity:r1").assertCountEquals(0)
        composeRule.onAllNodesWithTag("activity:r4").assertCountEquals(0)
        composeRule.onNodeWithTag("activity-more").assertTextEquals("and 2 more running")
    }

    @Test
    fun tappingALineOpensItsTaskAndTheMoreLineCallsOnMore() {
        val tasks = listOf(
            task("r1", "one", now - 10),
            task("r2", "two", now - 500),
            task("r3", "three", now - 300),
            task("r4", "four", now - 50),
            task("r5", "five", now - 100),
        )
        val opened = mutableListOf<BackgroundTask>()
        var more = 0
        setLines(tasks, onOpen = { opened += it }, onMore = { more++ })

        composeRule.onNodeWithTag("activity:r3")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .performClick()
        composeRule.onNodeWithTag("activity-more")
            .assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
            .performClick()

        assertEquals(listOf(tasks[2]), opened)
        assertEquals(1, more)
    }
}
