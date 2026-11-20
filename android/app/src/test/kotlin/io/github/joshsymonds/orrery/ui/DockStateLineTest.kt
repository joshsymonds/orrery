package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import io.github.joshsymonds.orrery.mainThreadComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DockStateLineTest {
    @get:Rule
    val composeRule = mainThreadComposeRule()

    private var interrupts = 0
    private var resumes = 0
    private var stops = 0

    private fun setLine(line: StateLine, stepLabel: String? = null) {
        val stop = stepLabel?.let { label ->
            DockStop(more = "Stop this step…", name = label, onStop = { stops++ })
        }
        composeRule.setContent {
            MaterialTheme {
                DockStateLine(
                    line = line,
                    onInterrupt = { interrupts++ },
                    onResume = { resumes++ },
                    stop = stop,
                )
            }
        }
    }

    private fun working(control: DockControl = DockControl.INTERRUPT) = StateLine(
        tone = "working",
        word = "working",
        tool = "Bash",
        what = "go test ./internal/tui/...",
        clock = "0:12",
        control = control,
    )

    @Test
    fun drawsTheWordToolWhatAndClock() {
        setLine(working())

        composeRule.onNodeWithTag("dock-state").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-word").assertTextEquals("working")
        composeRule.onNodeWithText("Bash").assertIsDisplayed()
        composeRule.onNodeWithText("go test ./internal/tui/...").assertIsDisplayed()
        composeRule.onNodeWithText("0:12").assertIsDisplayed()
    }

    @Test
    fun interruptCallsOnInterrupt() {
        setLine(working())

        composeRule.onNodeWithText("Interrupt").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-interrupt").performClick()

        composeRule.runOnIdle {
            check(interrupts == 1 && resumes == 0) { "interrupts=$interrupts resumes=$resumes" }
        }
    }

    @Test
    fun interruptingIsDrawnAndSpent() {
        setLine(
            StateLine(
                tone = "interrupting",
                word = "interrupting…",
                tool = "",
                what = "waiting for the turn to end",
                clock = "0:03",
                control = DockControl.INTERRUPTING,
            ),
        )

        composeRule.onNodeWithText("Interrupting…").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-interrupt").assertIsNotEnabled()
        composeRule.onNodeWithTag("dock-interrupt").performClick()

        composeRule.runOnIdle {
            check(interrupts == 0) { "a spent Interrupting… called onInterrupt $interrupts times" }
        }
    }

    @Test
    fun resumeCallsOnResume() {
        setLine(
            StateLine(
                tone = "interrupted",
                word = "cut off",
                tool = "",
                what = "· network",
                clock = "",
                control = DockControl.RESUME,
            ),
        )

        composeRule.onNodeWithText("Resume").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-resume").performClick()

        composeRule.runOnIdle {
            check(resumes == 1 && interrupts == 0) { "resumes=$resumes interrupts=$interrupts" }
        }
    }

    @Test
    fun noneDrawsNoControl() {
        setLine(
            StateLine(
                tone = "done",
                word = "done",
                tool = "",
                what = "· 2m ago",
                clock = "",
                control = DockControl.NONE,
            ),
        )

        composeRule.onAllNodesWithTag("dock-interrupt").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-resume").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-more").assertCountEquals(0)
    }

    @Test
    fun stepMoreRevealsTheConfirmAndStopCallsOnStopStepOnce() {
        setLine(working(), stepLabel = "build")

        composeRule.onAllNodesWithTag("dock-stop-confirm").assertCountEquals(0)
        composeRule.onNodeWithTag("dock-more").performClick()

        composeRule.onNodeWithText("Stop build?").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-keep-running").assertIsDisplayed()
        composeRule.onNodeWithTag("dock-stop-confirm").assertTextEquals("Stop build")
        composeRule.onNodeWithTag("dock-stop-confirm").performClick()

        composeRule.runOnIdle {
            check(stops == 1 && interrupts == 0) { "stops=$stops interrupts=$interrupts" }
        }
        composeRule.onAllNodesWithTag("dock-stop-confirm").assertCountEquals(0)
    }

    @Test
    fun keepItRunningClosesTheConfirmAndCallsNothing() {
        setLine(working(), stepLabel = "build")

        composeRule.onNodeWithTag("dock-more").performClick()
        composeRule.onNodeWithTag("dock-keep-running").performClick()

        composeRule.runOnIdle {
            check(stops == 0 && interrupts == 0) { "stops=$stops interrupts=$interrupts" }
        }
        composeRule.onAllNodesWithTag("dock-keep-running").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-stop-confirm").assertCountEquals(0)
        composeRule.onAllNodesWithText("Stop build?").assertCountEquals(0)
    }

    @Test
    fun heldStepDrawsMoreButNoInterrupt() {
        setLine(
            StateLine(
                tone = "held",
                word = "held",
                tool = "",
                what = "",
                clock = "",
                control = DockControl.NONE,
            ),
            stepLabel = "build",
        )

        composeRule.onNodeWithTag("dock-more").assertIsDisplayed()
        composeRule.onAllNodesWithTag("dock-interrupt").assertCountEquals(0)
        composeRule.onAllNodesWithTag("dock-resume").assertCountEquals(0)
    }
}
