package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w400dp-h800dp")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class PaneHeaderTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val now = 1_000_000L
    private val cwd = "/home/user/example-project"
    private val prompt = "Example prompt about the header."
    private val longPrompt = "Colour the tree's rows by state, and give the cursor row a full-width bar. ".repeat(6)

    private fun pane(
        label: String = "example-label",
        command: String = "claude",
        state: String = "working",
    ): Pane = Pane(
        id = "%3",
        host = "example-host",
        session = "example-session",
        window = 1,
        pane = 2,
        cwd = cwd,
        command = command,
        state = state,
        summary = "Example summary.",
        label = label,
        changed = now - 5_000,
        seen = now - 5_000,
        question = "",
        questionHash = "",
    )

    private fun setHeader(
        pane: Pane = pane(),
        origin: String? = prompt,
        stats: List<HostStat>? = null,
        showStatus: Boolean = false,
        liveScreen: Boolean = false,
        onStatusClick: () -> Unit = {},
        onConversation: () -> Unit = {},
        onBack: () -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                PaneHeader(
                    pane = pane,
                    origin = origin,
                    stats = stats,
                    nowSeconds = now,
                    showStatus = showStatus,
                    liveScreen = liveScreen,
                    onStatusClick = onStatusClick,
                    onConversation = onConversation,
                    onBack = onBack,
                )
            }
        }
    }

    private fun originText(): String = composeRule.onNodeWithTag("pane-origin")
        .fetchSemanticsNode()
        .config[SemanticsProperties.Text]
        .joinToString("") { it.text }

    @Test
    fun titleIsTheLabel() {
        setHeader(pane(label = "Example label", command = "claude"))

        composeRule.onNodeWithTag("pane-title").assertTextEquals("Example label")
    }

    @Test
    fun titleFallsBackToCommandWhenLabelIsEmpty() {
        setHeader(pane(label = "", command = "zsh"))

        composeRule.onNodeWithTag("pane-title").assertTextEquals("zsh")
    }

    @Test
    fun originLineHoldsProjectAndOpeningPromptNotCwd() {
        setHeader(origin = prompt)

        composeRule.onNodeWithTag("pane-origin")
            .assertTextContains("example-project", substring = true)
            .assertTextContains(prompt, substring = true)
        assertFalse(originText().contains("/home/user"))
    }

    @Test
    fun originLineHoldsProjectAndCwdWhenOriginIsNull() {
        setHeader(origin = null)

        composeRule.onNodeWithTag("pane-origin")
            .assertTextContains("example-project", substring = true)
            .assertTextContains(cwd, substring = true)
    }

    @Test
    fun longOriginLaysOutInTwoLinesWithVisualOverflow() {
        setHeader(origin = longPrompt)

        val layouts = mutableListOf<TextLayoutResult>()
        val action = composeRule.onNodeWithTag("pane-origin")
            .fetchSemanticsNode()
            .config[SemanticsActions.GetTextLayoutResult]
            .action
        assertTrue(action!!.invoke(layouts))
        assertEquals(2, layouts.single().lineCount)
        assertTrue(layouts.single().hasVisualOverflow)
    }

    @Test
    fun statusLineAbsentWhenNotShown() {
        setHeader(showStatus = false)

        composeRule.onAllNodesWithTag("status-line").assertCountEquals(0)
    }

    @Test
    fun statusLineShownWhenAskedForAWorkingPane() {
        setHeader(showStatus = true)

        composeRule.onNodeWithTag("status-line").assertIsDisplayed()
    }

    @Test
    fun tappingStatusLineCallsOnStatusClick() {
        var tapped = false
        setHeader(showStatus = true, onStatusClick = { tapped = true })

        composeRule.onNodeWithTag("status-line").performClick()

        assertTrue(tapped)
    }

    @Test
    fun metersAbsentWhenStatsNull() {
        setHeader(stats = null)

        composeRule.onAllNodesWithTag("host-meters").assertCountEquals(0)
    }

    @Test
    fun metersAbsentWhenStatsEmpty() {
        setHeader(stats = emptyList())

        composeRule.onAllNodesWithTag("host-meters").assertCountEquals(0)
    }

    @Test
    fun metersShownWhenStatsPresent() {
        setHeader(stats = listOf(HostStat("cpu", "42%", 42.0, "ok")))

        composeRule.onNodeWithTag("host-meters").assertIsDisplayed()
        composeRule.onNodeWithText("CPU").assertIsDisplayed()
    }

    @Test
    fun liveReturnAbsentWithoutLiveScreen() {
        setHeader(liveScreen = false)

        composeRule.onAllNodesWithTag("live-return").assertCountEquals(0)
    }

    @Test
    fun liveScreenBarShowsConversationControlAndTapCallsOnConversation() {
        var returned = false
        setHeader(liveScreen = true, onConversation = { returned = true })

        composeRule.onNodeWithTag("live-return").assertTextEquals("‹ conversation").performClick()
        composeRule.onNodeWithText("live screen").assertIsDisplayed()

        assertTrue(returned)
    }

    @Test
    fun tappingBackCallsOnBack() {
        var backed = false
        setHeader(onBack = { backed = true })

        composeRule.onNodeWithTag("pane-back").performClick()

        assertTrue(backed)
    }
}
