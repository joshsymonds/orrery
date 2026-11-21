package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertTextEquals
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.style.TextOverflow
import io.github.joshsymonds.orrery.api.ConversationContext
import io.github.joshsymonds.orrery.mainThreadAndroidComposeRule
import io.github.joshsymonds.orrery.ui.theme.OrreryTokens
import org.junit.Assert.assertEquals
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
class HeadTitleRowTest {
    @get:Rule
    val composeRule = mainThreadAndroidComposeRule<ComponentActivity>()

    private var moreOpen by mutableStateOf(false)
    private var backCalls = 0
    private var hideCalls = 0

    private fun setRow(
        context: ConversationContext? = null,
        note: String = "",
        host: String = "halyard",
        title: String = "Tide table cache warm-up",
    ) {
        composeRule.setContent {
            HeadTitleRow(
                dir = "tidepool",
                host = host,
                title = title,
                context = context,
                note = note,
                onBack = { backCalls++ },
                onMore = { moreOpen = true },
                moreOpen = moreOpen,
                onDismissMore = { moreOpen = false },
                onHideHeader = { hideCalls++ },
            )
        }
    }

    @Test
    fun drawsDirWithHostAndTheTitle() {
        setRow()

        composeRule.onNodeWithTag("head-dir").assertTextEquals("tidepool · on halyard")
        composeRule.onNodeWithTag("head-text").assertTextEquals("Tide table cache warm-up")
    }

    @Test
    fun theRowDrawsBackDirTitleAndMoreAndNoState() {
        setRow()

        val texts = composeRule.onAllNodes(hasAnyAncestor(hasTestTag("head-title")), useUnmergedTree = true)
            .fetchSemanticsNodes()
            .flatMap { node -> node.config.getOrElse(SemanticsProperties.Text) { emptyList() }.map { it.text } }
        assertEquals(setOf("‹", "tidepool · on halyard", "Tide table cache warm-up", "⋯"), texts.toSet())
    }

    @Test
    fun theTitleIsBodyBoldOnAtMostTwoLinesCutShortAsRound6sTouchTitleRowDrawsIt() {
        setRow()

        val input = textLayout("head-text").layoutInput
        assertEquals(OrreryTokens.Type.body.fontSize, input.style.fontSize)
        assertEquals(OrreryTokens.Font.Weight.bold, input.style.fontWeight)
        assertEquals(2, input.maxLines)
        assertEquals(TextOverflow.Ellipsis, input.overflow)
    }

    @Test
    fun aTitleTooLongForTheRowWrapsInsideItAndMoreStaysInView() {
        setRow(title = "Merging main into a story-out client branch, resolving four conflicts in the dock, the reader and the head")

        val row = composeRule.onNodeWithTag("head-title").getBoundsInRoot()
        val text = composeRule.onNodeWithTag("head-text").getBoundsInRoot()
        val more = composeRule.onNodeWithTag("head-more").getBoundsInRoot()
        assertTrue("More is inside the row: row $row, More $more", more.left >= row.left && more.right <= row.right)
        assertTrue("the title ends before More: title $text, More $more", text.right <= more.left)
        val layout = textLayout("head-text")
        assertEquals(2, layout.lineCount)
        assertTrue("the title is cut short at its second line", layout.isLineEllipsized(1))
    }

    @Test
    fun anUnknownHostDrawsTheDirAlone() {
        setRow(host = "")

        composeRule.onNodeWithTag("head-dir").assertTextEquals("tidepool")
    }

    @Test
    fun dirTakesThePlaceColour() {
        setRow()

        assertSpanColours("head-dir", listOf(OrreryTokens.Role.place))
    }

    @Test
    fun contextIsOkBelowSixty() {
        setRow(ConversationContext(tokens = 59, window = 100))

        composeRule.onNodeWithTag("head-context").assertTextEquals("▰▰▰▱▱ 59%")
        assertSpanColours("head-context", listOf(OrreryTokens.Level.ok))
    }

    @Test
    fun contextIsWarnFromSixty() {
        setRow(ConversationContext(tokens = 60, window = 100))

        composeRule.onNodeWithTag("head-context").assertTextEquals("▰▰▰▱▱ 60%")
        assertSpanColours("head-context", listOf(OrreryTokens.Level.warn))
    }

    @Test
    fun contextIsCritFromEighty() {
        setRow(ConversationContext(tokens = 80, window = 100))

        composeRule.onNodeWithTag("head-context").assertTextEquals("▰▰▰▰▱ 80%")
        assertSpanColours("head-context", listOf(OrreryTokens.Level.crit))
    }

    @Test
    fun noContextDrawsNoMeter() {
        setRow(context = null)

        composeRule.onAllNodesWithTag("head-context").assertCountEquals(0)
    }

    @Test
    fun moreOpensAMenuHoldingHideTheHeader() {
        setRow()

        composeRule.onAllNodesWithTag("head-menu-hide").assertCountEquals(0)
        composeRule.onNodeWithTag("head-more").performClick()
        composeRule.onNodeWithTag("head-menu-hide").assertTextEquals("Hide the header")
    }

    @Test
    fun hideTheHeaderCallsOnHideHeaderAndClosesTheMenu() {
        setRow()

        composeRule.onNodeWithTag("head-more").performClick()
        composeRule.onNodeWithTag("head-menu-hide").performClick()

        assertEquals(1, hideCalls)
        composeRule.onAllNodesWithTag("head-menu-hide").assertCountEquals(0)
    }

    @Test
    fun backCallsOnBack() {
        setRow()

        composeRule.onNodeWithTag("head-back").performClick()

        assertEquals(1, backCalls)
    }

    @Test
    fun aNonEmptyNoteDrawsUnderTheRow() {
        setRow(note = "Could not archive this session: the server did not answer. Nothing changed.")

        composeRule.onNodeWithTag("head-note")
            .assertTextEquals("Could not archive this session: the server did not answer. Nothing changed.")
        val row = composeRule.onNodeWithTag("head-title").fetchSemanticsNode().boundsInRoot
        val note = composeRule.onNodeWithTag("head-note").fetchSemanticsNode().boundsInRoot
        assertTrue("note must sit under the row: note=${note.top} row=${row.bottom}", note.top >= row.bottom)
    }

    @Test
    fun anEmptyNoteDrawsNothing() {
        setRow(note = "")

        composeRule.onAllNodesWithTag("head-note").assertCountEquals(0)
    }

    // The laid-out text of a tagged node, read through the semantics tree.
    private fun textLayout(tag: String): TextLayoutResult {
        val node = composeRule.onNodeWithTag(tag).fetchSemanticsNode()
        val layouts = mutableListOf<TextLayoutResult>()
        checkNotNull(node.config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action).invoke(layouts)
        return layouts.single()
    }

    // The colours a tagged text draws in, read from its annotated spans in the semantics tree.
    private fun assertSpanColours(tag: String, expected: List<Color>) {
        val text = composeRule.onNodeWithTag(tag).fetchSemanticsNode().config[SemanticsProperties.Text]
        val colours = text.flatMap { annotated -> annotated.spanStyles.mapNotNull { it.item.color } }
        assertEquals(expected, colours)
    }
}
