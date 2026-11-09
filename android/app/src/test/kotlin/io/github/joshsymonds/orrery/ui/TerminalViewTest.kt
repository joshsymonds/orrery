package io.github.joshsymonds.orrery.ui

import androidx.activity.ComponentActivity
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.pinch
import androidx.compose.ui.test.swipe
import androidx.compose.ui.test.swipeLeft
import androidx.compose.ui.text.TextLayoutResult
import io.github.joshsymonds.orrery.ui.theme.MochaTerminal
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private const val INITIAL_LINES = 200
private const val GROWN_LINES = 260
private const val WIDE_LINE_REPEATS = 30
private const val TOLERANCE_PX = 0.5f

private fun numberedLines(count: Int): String =
    (1..count).joinToString("\n") { "synthetic line $it" }

private val wideLine = "synthetic-wide-line ".repeat(WIDE_LINE_REPEATS).trimEnd()

@OptIn(ExperimentalTestApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class TerminalViewTest {
    @get:Rule
    val composeRule = createAndroidComposeRule<ComponentActivity>()

    private fun setTerminal(initial: String): MutableState<String> {
        val text = mutableStateOf(initial)
        composeRule.setContent {
            MaterialTheme { TerminalView(text.value) }
        }
        composeRule.waitForIdle()
        return text
    }

    private fun viewport(): SemanticsNode =
        composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).fetchSemanticsNode()

    private fun textNode(): SemanticsNode =
        composeRule.onNodeWithTag(TERMINAL_TEXT_TAG).fetchSemanticsNode()

    private fun SemanticsNode.textLayout(): TextLayoutResult {
        val results = mutableListOf<TextLayoutResult>()
        config[SemanticsActions.GetTextLayoutResult].action!!.invoke(results)
        return results.single()
    }

    private fun SemanticsNode.bottom(): Float = positionInRoot.y + size.height

    /** Moves the finger down, which scrolls the content up towards the top. */
    private fun swipeFingerDownOnViewport() {
        composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).performTouchInput {
            swipe(start = Offset(center.x, center.y - 100f), end = Offset(center.x, center.y + 100f))
        }
        composeRule.waitForIdle()
    }

    @Test
    fun openingShowsNewestLineAtLeftEdge() {
        setTerminal(numberedLines(INITIAL_LINES))

        val view = viewport()
        val text = textNode()
        assertTrue(text.size.height > view.size.height)
        assertEquals(view.bottom(), text.bottom(), TOLERANCE_PX)
        assertEquals(view.positionInRoot.x, text.positionInRoot.x, TOLERANCE_PX)
    }

    @Test
    fun longLineStaysOnOneLineAndWidensPastViewport() {
        setTerminal(wideLine)

        val view = viewport()
        val text = textNode()
        assertEquals(1, text.textLayout().lineCount)
        assertTrue(text.size.width > view.size.width)
    }

    @Test
    fun horizontalSwipePansTheText() {
        setTerminal(wideLine)
        val before = textNode().positionInRoot.x

        composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).performTouchInput { swipeLeft() }
        composeRule.waitForIdle()

        assertTrue(textNode().positionInRoot.x < before)
    }

    @Test
    fun verticalSwipePansTheText() {
        setTerminal(numberedLines(INITIAL_LINES))
        val before = textNode().positionInRoot.y

        swipeFingerDownOnViewport()

        assertTrue(textNode().positionInRoot.y > before)
    }

    @Test
    fun appendedTextKeepsNewestLineInViewWhenReaderIsAtBottom() {
        val text = setTerminal(numberedLines(INITIAL_LINES))

        text.value = numberedLines(GROWN_LINES)
        composeRule.waitForIdle()

        val view = viewport()
        val node = textNode()
        assertEquals(GROWN_LINES, node.textLayout().lineCount)
        assertEquals(view.bottom(), node.bottom(), TOLERANCE_PX)
    }

    @Test
    fun appendedTextLeavesPositionAloneAfterReaderScrolledUp() {
        val text = setTerminal(numberedLines(INITIAL_LINES))
        val bottomY = textNode().positionInRoot.y

        swipeFingerDownOnViewport()
        val scrolledY = textNode().positionInRoot.y
        assertTrue(scrolledY > bottomY)

        text.value = numberedLines(GROWN_LINES)
        composeRule.waitForIdle()

        assertEquals(scrolledY, textNode().positionInRoot.y, TOLERANCE_PX)
    }

    @Test
    fun pinchChangesTextSize() {
        setTerminal(numberedLines(INITIAL_LINES))
        val before = textNode().textLayout().layoutInput.style.fontSize.value

        composeRule.onNodeWithTag(TERMINAL_VIEWPORT_TAG).performTouchInput {
            val c = center
            pinch(
                start0 = Offset(c.x - 50f, c.y),
                start1 = Offset(c.x + 50f, c.y),
                end0 = Offset(c.x - 150f, c.y),
                end1 = Offset(c.x + 150f, c.y),
            )
        }
        composeRule.waitForIdle()

        val after = textNode().textLayout().layoutInput.style.fontSize.value
        assertTrue("expected a larger text size after pinch out, was $before then $after", after > before)
    }

    @Test
    fun sgrColouredTextShowsItsText() {
        setTerminal("\u001B[31mred\u001B[0m plain")

        composeRule.onNodeWithText("red plain").assertIsDisplayed()
        val annotated = textNode().config.getOrNull(SemanticsProperties.Text)!!.single()
        assertEquals("red plain", annotated.text)
        assertEquals(MochaTerminal.Ansi[1], annotated.spanStyles.single().item.color)
    }
}
