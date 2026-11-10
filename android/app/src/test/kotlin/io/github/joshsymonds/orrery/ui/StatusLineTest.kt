package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class StatusLineTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val now = 1_000_000L
    private val toolInput = "go test ./internal/tui/..."

    private fun pane(state: String, changed: Long, tool: String = ""): Pane = Pane(
        id = "%3",
        host = "example-host",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = "/home/user/project",
        command = "claude",
        state = state,
        summary = "Example summary.",
        label = "example-label",
        changed = changed,
        seen = changed,
        question = "",
        questionHash = "",
        tool = tool,
    )

    private fun toolStartedAt(start: Long): String =
        """{"id":"tool-1","name":"Bash","input":"$toolInput","start":$start}"""

    private fun setLine(pane: Pane, onClick: () -> Unit = {}) {
        composeRule.setContent {
            MaterialTheme { StatusLine(pane, now, onClick) }
        }
    }

    @Test
    fun workingWithToolShowsStateToolInputAndElapsedSinceToolStart() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12)))

        composeRule.onNodeWithTag("status-line").assertIsDisplayed()
        composeRule.onNodeWithText("working").assertIsDisplayed()
        composeRule.onNodeWithText("Bash").assertIsDisplayed()
        composeRule.onNodeWithText(toolInput).assertIsDisplayed()
        composeRule.onNodeWithText("0:12").assertIsDisplayed()
    }

    @Test
    fun elapsedOverAnHourShowsHoursMinutesSeconds() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 3_725)))

        composeRule.onNodeWithText("1:02:05").assertIsDisplayed()
    }

    @Test
    fun workingWithoutToolShowsElapsedSinceChanged() {
        setLine(pane("working", changed = now - 75))

        composeRule.onNodeWithText("working").assertIsDisplayed()
        composeRule.onNodeWithText("1:15").assertIsDisplayed()
    }

    @Test
    fun toolStartedAfterNowShowsZero() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now + 5)))

        composeRule.onNodeWithText("0:00").assertIsDisplayed()
    }

    @Test
    fun waitingShowsStateAndAgeSinceChanged() {
        setLine(pane("waiting", changed = now - 60))

        composeRule.onNodeWithText("waiting for you").assertIsDisplayed()
        composeRule.onNodeWithText("1m").assertIsDisplayed()
    }

    @Test
    fun doneShowsStateAndAgeWithAgo() {
        setLine(pane("done", changed = now - 120))

        composeRule.onNodeWithText("done").assertIsDisplayed()
        composeRule.onNodeWithText("2m ago").assertIsDisplayed()
    }

    @Test
    fun doneUnderAMinuteShowsSeconds() {
        setLine(pane("done", changed = now - 30))

        composeRule.onNodeWithText("30s ago").assertIsDisplayed()
    }

    @Test
    fun doneUnderADayShowsHours() {
        setLine(pane("done", changed = now - 7_200))

        composeRule.onNodeWithText("2h ago").assertIsDisplayed()
    }

    @Test
    fun doneOverADayShowsDays() {
        setLine(pane("done", changed = now - 172_800))

        composeRule.onNodeWithText("2d ago").assertIsDisplayed()
    }

    @Test
    fun otherStateDrawsNothing() {
        setLine(pane("idle", changed = now - 60))

        composeRule.onAllNodesWithTag("status-line").assertCountEquals(0)
    }

    @Test
    fun tappingTheLineCallsOnClickOnce() {
        var taps = 0
        setLine(pane("waiting", changed = now - 60)) { taps++ }

        composeRule.onNodeWithTag("status-line").performClick()

        assertEquals(1, taps)
    }
}
