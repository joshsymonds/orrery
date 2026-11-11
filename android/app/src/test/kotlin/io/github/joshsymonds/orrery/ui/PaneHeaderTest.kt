package io.github.joshsymonds.orrery.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertTextContains
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.api.HostStat
import io.github.joshsymonds.orrery.api.Pane
import io.github.joshsymonds.orrery.ui.theme.Mocha
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

private const val METER_WINDOW = 1_000_000L

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
        context: ConversationContext? = null,
        liveScreen: Boolean = false,
        compact: Boolean = false,
        onConversation: () -> Unit = {},
        onBack: () -> Unit = {},
    ) {
        composeRule.setContent {
            MaterialTheme {
                PaneHeader(
                    pane = pane,
                    origin = origin,
                    stats = stats,
                    context = context,
                    liveScreen = liveScreen,
                    onConversation = onConversation,
                    onBack = onBack,
                    compact = compact,
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
    fun headerHasNoStatusLine() {
        setHeader()

        composeRule.onAllNodesWithTag("status-line").assertCountEquals(0)
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

    private fun meterSpanColors(): List<Color?> = composeRule.onNodeWithTag("context-meter")
        .fetchSemanticsNode()
        .config[SemanticsProperties.Text]
        .single()
        .spanStyles
        .map { it.item.color }

    private fun assertMeter(tokens: Long, text: String, color: Color) {
        setHeader(context = ConversationContext(tokens = tokens, window = METER_WINDOW))

        composeRule.onNodeWithTag("context-meter").assertTextEquals(text)
        assertEquals(listOf(color), meterSpanColors())
    }

    @Test
    fun contextMeterAtZeroPercentShowsEmptyBlocksInGreen() {
        assertMeter(tokens = 1L, text = "▱▱▱▱▱ 0%", color = Mocha.Green)
    }

    @Test
    fun contextMeterAt39PercentIsGreen() {
        assertMeter(tokens = 390_000L, text = "▰▰▱▱▱ 39%", color = Mocha.Green)
    }

    @Test
    fun contextMeterAt40PercentIsYellow() {
        assertMeter(tokens = 400_000L, text = "▰▰▱▱▱ 40%", color = Mocha.Yellow)
    }

    @Test
    fun contextMeterAt79PercentIsPeach() {
        assertMeter(tokens = 790_000L, text = "▰▰▰▰▱ 79%", color = Mocha.Peach)
    }

    @Test
    fun contextMeterAt80PercentIsRed() {
        assertMeter(tokens = 800_000L, text = "▰▰▰▰▱ 80%", color = Mocha.Red)
    }

    @Test
    fun contextMeterAt100PercentIsFullAndRed() {
        assertMeter(tokens = METER_WINDOW, text = "▰▰▰▰▰ 100%", color = Mocha.Red)
    }

    @Test
    fun contextMeterAboveWindowReadsOneHundredPercent() {
        assertMeter(tokens = METER_WINDOW + 500_000L, text = "▰▰▰▰▰ 100%", color = Mocha.Red)
    }

    @Test
    fun contextMeterAbsentWithoutContext() {
        setHeader(context = null)

        composeRule.onAllNodesWithTag("context-meter").assertCountEquals(0)
    }

    @Test
    fun contextMeterAbsentWithZeroTokens() {
        setHeader(context = ConversationContext(tokens = 0L, window = METER_WINDOW))

        composeRule.onAllNodesWithTag("context-meter").assertCountEquals(0)
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

    @Test
    fun compactHeaderFoldsAwayTitleBackOriginMetersAndHairline() {
        setHeader(
            stats = listOf(HostStat("cpu", "42%", 42.0, "ok")),
            liveScreen = true,
            compact = true,
        )

        composeRule.onNodeWithTag("pane-header").assertHeightIsEqualTo(0.dp)
        composeRule.onAllNodesWithTag("pane-title").assertCountEquals(0)
        composeRule.onAllNodesWithTag("pane-back").assertCountEquals(0)
        composeRule.onAllNodesWithTag("pane-origin").assertCountEquals(0)
        composeRule.onAllNodesWithTag("host-meters").assertCountEquals(0)
        composeRule.onAllNodesWithTag("live-return").assertCountEquals(0)
    }

    @Test
    fun headerStartedCompactReturnsWhenCompactClears() {
        val compact = mutableStateOf(true)
        composeRule.setContent {
            MaterialTheme {
                PaneHeader(
                    pane = pane(),
                    origin = prompt,
                    stats = null,
                    context = null,
                    liveScreen = false,
                    onConversation = {},
                    onBack = {},
                    compact = compact.value,
                )
            }
        }
        composeRule.onAllNodesWithTag("pane-title").assertCountEquals(0)

        composeRule.runOnIdle { compact.value = false }
        composeRule.waitForIdle()

        composeRule.onNodeWithTag("pane-title").assertTextEquals("example-label")
        composeRule.onNodeWithTag("pane-origin")
            .assertTextContains("example-project", substring = true)
            .assertTextContains(prompt, substring = true)
    }
}
