package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import io.github.joshsymonds.orrery.api.Pane
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
    private val landsRow = "a message sent now lands at Claude's next step"

    private fun pane(state: String, changed: Long, tool: String = "", lastTurnEnd: Long = 0L): Pane = Pane(
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
        lastTurnEnd = lastTurnEnd,
    )

    private fun toolStartedAt(start: Long, name: String = "Bash"): String =
        """{"id":"tool-1","name":"$name","input":"$toolInput","start":$start}"""

    private fun setLine(pane: Pane) {
        composeRule.setContent {
            MaterialTheme { StatusLine(pane, now) }
        }
    }

    @Test
    fun workingWithToolShowsStateToolWordsAndElapsedSinceToolStart() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12)))

        composeRule.onNodeWithTag("status-line").assertIsDisplayed()
        composeRule.onNodeWithText("working").assertIsDisplayed()
        composeRule.onNodeWithText("running a command").assertIsDisplayed()
        composeRule.onNodeWithText("0:12").assertIsDisplayed()
        composeRule.onAllNodesWithText(toolInput, substring = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("Bash", substring = true).assertCountEquals(0)
    }

    @Test
    fun unknownMcpToolShowsItsShortName() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12, name = "mcp__other__frobnicate")))

        composeRule.onNodeWithText("frobnicate").assertIsDisplayed()
        composeRule.onAllNodesWithText("mcp__", substring = true).assertCountEquals(0)
    }

    @Test
    fun stepWaitReadsAsABackgroundWait() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 40, name = "mcp__orrery__step_wait")))

        composeRule.onNodeWithText("background").assertIsDisplayed()
        composeRule.onNodeWithText("waiting on background work").assertIsDisplayed()
        composeRule.onNodeWithText("0:40").assertIsDisplayed()
        composeRule.onAllNodesWithText("working").assertCountEquals(0)
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
    fun workingShowsTheLandsAtNextStepRow() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12)))

        composeRule.onNodeWithText(landsRow).assertIsDisplayed()
    }

    @Test
    fun workingRowEndsWithLastTurnAgeWhenLastTurnEndIsSet() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12), lastTurnEnd = now - 180))

        composeRule.onNodeWithText("$landsRow · last turn ended 3m ago").assertIsDisplayed()
    }

    @Test
    fun workingRowHasNoLastTurnTextWhenLastTurnEndIsZero() {
        setLine(pane("working", changed = now - 5_000, tool = toolStartedAt(now - 12), lastTurnEnd = 0L))

        composeRule.onNodeWithText(landsRow).assertIsDisplayed()
        composeRule.onAllNodesWithText("last turn", substring = true).assertCountEquals(0)
    }

    @Test
    fun waitingShowsStateAndAgeSinceChangedWithoutTheLandsRow() {
        setLine(pane("waiting", changed = now - 60, lastTurnEnd = now - 180))

        composeRule.onNodeWithText("waiting for you").assertIsDisplayed()
        composeRule.onNodeWithText("1m").assertIsDisplayed()
        composeRule.onAllNodesWithText("lands at Claude", substring = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("last turn", substring = true).assertCountEquals(0)
    }

    @Test
    fun doneShowsStateAndAgeWithAgoWithoutTheLandsRow() {
        setLine(pane("done", changed = now - 120, lastTurnEnd = now - 180))

        composeRule.onNodeWithText("done").assertIsDisplayed()
        composeRule.onNodeWithText("2m ago").assertIsDisplayed()
        composeRule.onAllNodesWithText("lands at Claude", substring = true).assertCountEquals(0)
        composeRule.onAllNodesWithText("last turn", substring = true).assertCountEquals(0)
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
    fun theLineHasNoClickAction() {
        setLine(pane("waiting", changed = now - 60))

        composeRule.onNodeWithTag("status-line").assert(SemanticsMatcher.keyNotDefined(SemanticsActions.OnClick))
    }
}
