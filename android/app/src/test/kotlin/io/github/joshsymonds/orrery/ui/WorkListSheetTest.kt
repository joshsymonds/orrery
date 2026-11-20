package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.BackgroundTask
import io.github.joshsymonds.orrery.mainThreadComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import io.github.joshsymonds.orrery.ui.theme.inLabelCase
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
class WorkListSheetTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private val now = 1_000_000L

    private fun task(
        id: String,
        type: String,
        subagentType: String,
        description: String,
        activity: String,
        started: Long,
    ): BackgroundTask = BackgroundTask(
        id = id,
        toolUseId = "tool-$id",
        description = description,
        type = type,
        subagentType = subagentType,
        status = "running",
        activity = activity,
        started = started,
        ended = 0L,
    )

    private fun spanColors(tag: String): List<Color?> = composeRule.onNodeWithTag(tag)
        .fetchSemanticsNode()
        .config[SemanticsProperties.Text]
        .flatMap { it.spanStyles }
        .map { it.item.color }

    private fun setWorkList(agents: Boolean, tasks: List<BackgroundTask>) {
        composeRule.setContent {
            MaterialTheme {
                WorkListSheet(agents = agents, tasks = tasks, now = now, onDismiss = {})
            }
        }
    }

    private fun setPicker(rows: List<PickerRow>, onOpen: (String) -> Unit, onDismiss: () -> Unit) {
        composeRule.setContent {
            MaterialTheme {
                RailPickerSheet(label = "epics", rows = rows, onOpen = onOpen, onDismiss = onDismiss)
            }
        }
    }

    @Test
    fun agentsSheetHeadsLeadsAndDrawsEachAgentWithActivityAndClock() {
        setWorkList(
            agents = true,
            tasks = listOf(
                task("a1", "agent", "Explore", "Map the rail", "reading files", started = now - 125),
                task("a2", "agent", "", "Check the build", "running tests", started = now - 10),
            ),
        )

        composeRule.onNodeWithTag("list-head").assertTextEquals("Sub-agents".inLabelCase())
        composeRule.onNodeWithTag("list-lead").assertTextEquals("These run for this conversation.")
        composeRule.onNodeWithTag("list-row:a1")
            .assertTextContains("Map the rail")
            .assertTextContains("Explore · reading files")
            .assertTextContains("2:05")
        composeRule.onNodeWithTag("list-row:a2")
            .assertTextContains("Check the build")
            .assertTextContains("agent · running tests")
            .assertTextContains("0:10")
    }

    @Test
    fun agentRowsAreLiveToned() {
        setWorkList(
            agents = true,
            tasks = listOf(task("a1", "agent", "Explore", "Map the rail", "reading files", started = now - 125)),
        )

        assertEquals(listOf<Color?>(OrreryTokens.Role.live), spanColors("list-row:a1"))
    }

    @Test
    fun runningSheetHeadsLeadsAndGreysEachShellRow() {
        setWorkList(
            agents = false,
            tasks = listOf(task("s1", "shell", "", "npm run dev", "listening on 3000", started = now - 600)),
        )

        composeRule.onNodeWithTag("list-head").assertTextEquals("Left running".inLabelCase())
        composeRule.onNodeWithTag("list-lead").assertTextEquals(
            "Nothing waits on these, and the session can be done while they run.",
        )
        composeRule.onNodeWithTag("list-row:s1")
            .assertTextContains("npm run dev")
            .assertTextContains("listening on 3000")
            .assertTextContains("10:00")
        assertEquals(listOf<Color?>(OrreryTokens.State.idle), spanColors("list-row:s1"))
    }

    @Test
    fun pickerHeadsByCountAndLabelAndDrawsEachEpicWithSlugAndState() {
        setPicker(
            rows = listOf(
                PickerRow(id = "e1", slug = "alpha-epic", state = "working", asks = false),
                PickerRow(id = "e2", slug = "beta-epic", state = "waiting", asks = true),
            ),
            onOpen = {},
            onDismiss = {},
        )

        composeRule.onNodeWithTag("picker-head").assertTextEquals("2 epics".inLabelCase())
        composeRule.onNodeWithTag("picker-row:e1")
            .assertTextContains("alpha-epic")
            .assertTextContains("working")
        composeRule.onNodeWithTag("picker-row:e2")
            .assertTextContains("beta-epic")
            .assertTextContains("waiting")
    }

    @Test
    fun pickerStateIsAttentionToneOnlyWhenTheEpicAsks() {
        setPicker(
            rows = listOf(
                PickerRow(id = "e1", slug = "alpha-epic", state = "working", asks = false),
                PickerRow(id = "e2", slug = "beta-epic", state = "waiting", asks = true),
            ),
            onOpen = {},
            onDismiss = {},
        )

        assertEquals(listOf<Color?>(OrreryTokens.Ink.two), spanColors("picker-row:e1"))
        assertEquals(listOf<Color?>(OrreryTokens.Role.attention), spanColors("picker-row:e2"))
    }

    @Test
    fun tappingPickerRowOpensItsOwnEpicThenDismisses() {
        val events = mutableListOf<String>()
        setPicker(
            rows = listOf(
                PickerRow(id = "e1", slug = "alpha-epic", state = "working", asks = false),
                PickerRow(id = "e2", slug = "beta-epic", state = "waiting", asks = true),
            ),
            onOpen = { events += "open:$it" },
            onDismiss = { events += "dismiss" },
        )

        composeRule.onNodeWithTag("picker-row:e2").performClick()

        assertEquals(listOf("open:e2", "dismiss"), events)
    }
}
