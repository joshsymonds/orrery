package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.onNodeWithTag
import io.github.joshsymonds.orrery.api.EpicDetail
import io.github.joshsymonds.orrery.api.EpicRow
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

// Invented names throughout: the serving host, another host, an epic, and the task and step the rows name.
private const val SERVING = "atlas"
private const val OTHER = "zeus"
private const val EPIC = "example-epic"
private const val TASK = "task-a"
private const val NOW = 1_700_000_000L

// Each title row names its host in its "host-label" node. The rows are drawn from invented values, so no server or
// clock is read: a row on two hosts names its own host, and a row on one host names the serving host.
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
class HostsTitleRowTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    @Test
    fun aTerminalPanesTitleRowNamesItsHostOnTwoHosts() {
        setHead(host = OTHER)

        composeRule.onNodeWithTag("host-label").assertTextEquals(OTHER)
    }

    @Test
    fun aTerminalPanesTitleRowNamesItsHostOnOneHost() {
        setHead(host = SERVING)

        composeRule.onNodeWithTag("host-label").assertTextEquals(SERVING)
    }

    @Test
    fun aHeadlessSessionsTitleRowNamesItsHostOnTwoHosts() {
        setHead(host = OTHER)

        composeRule.onNodeWithTag("host-label").assertTextEquals(OTHER)
    }

    @Test
    fun aHeadlessSessionsTitleRowNamesItsHostOnOneHost() {
        setHead(host = SERVING)

        composeRule.onNodeWithTag("host-label").assertTextEquals(SERVING)
    }

    @Test
    fun anEpicsTitleRowNamesItsHostOnTwoHosts() {
        show { EpicTitleRow(detail = epicDetail(host = OTHER), now = NOW, servingHost = SERVING, onBack = {}) }

        composeRule.onNodeWithTag("host-label").assertTextEquals(OTHER)
    }

    @Test
    fun anEpicsTitleRowNamesTheServingHostOnOneHost() {
        show { EpicTitleRow(detail = epicDetail(host = ""), now = NOW, servingHost = SERVING, onBack = {}) }

        composeRule.onNodeWithTag("host-label").assertTextEquals(SERVING)
    }

    @Test
    fun aTaskBandsEpicPinNamesItsHostOnTwoHosts() {
        show {
            ContextBand(
                detail = epicDetail(host = OTHER),
                currentTask = TASK,
                now = NOW,
                onOpenEpic = {},
                onOpenTask = { _, _ -> },
                servingHost = SERVING,
            )
        }

        composeRule.onNodeWithTag("host-label").assertTextEquals(OTHER)
    }

    @Test
    fun aTaskBandsEpicPinNamesTheServingHostOnOneHost() {
        show {
            ContextBand(
                detail = epicDetail(host = ""),
                currentTask = TASK,
                now = NOW,
                onOpenEpic = {},
                onOpenTask = { _, _ -> },
                servingHost = SERVING,
            )
        }

        composeRule.onNodeWithTag("host-label").assertTextEquals(SERVING)
    }

    @Test
    fun aStepsTitleRowNamesItsHostOnTwoHosts() {
        setStep(host = OTHER)

        composeRule.onNodeWithTag("host-label").assertTextEquals(OTHER)
    }

    @Test
    fun aStepsTitleRowNamesTheServingHostOnOneHost() {
        setStep(host = "")

        composeRule.onNodeWithTag("host-label").assertTextEquals(SERVING)
    }

    private fun show(content: @Composable () -> Unit) {
        composeRule.setContent(content)
    }

    private fun setHead(host: String) {
        show {
            Head(
                dir = "tidepool",
                host = host,
                title = "Tide table cache warm-up",
                context = null,
                note = "",
                goal = "",
                lines = emptyList(),
                asking = "",
                badges = emptyList(),
                fold = HeadFold.OPEN,
                onBack = {},
                onOpenEpic = {},
                onHide = {},
                onShow = {},
            )
        }
    }

    private fun setStep(host: String) {
        show {
            StepTitleRow(
                step = StepMode(held = false, label = TASK, epic = EPIC, task = TASK, host = host),
                detail = epicDetail(host = SERVING),
                title = "Parser task",
                phase = Phase.IMPLEMENTING,
                servingHost = SERVING,
                onBack = {},
                onOpenEpic = {},
                onOpenTask = { _, _ -> },
                onFind = {},
            )
        }
    }

    private fun epicDetail(host: String): EpicDetail = EpicDetail(
        epic = EpicRow(
            id = "example-repo/$EPIC",
            repository = "example-repo",
            slug = EPIC,
            outcome = "",
            changed = 0,
            live = true,
            runningSteps = emptyList(),
            endedSteps = 0,
            host = host,
        ),
        steps = emptyList(),
    )
}
